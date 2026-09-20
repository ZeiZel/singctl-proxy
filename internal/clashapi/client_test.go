package clashapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const connectionsJSON = `{
  "downloadTotal": 100, "uploadTotal": 50,
  "connections": [
    {"id":"c1","metadata":{"network":"tcp","sourceIP":"127.0.0.1","sourcePort":"54321",
      "destinationIP":"1.2.3.4","destinationPort":"443","host":"api.openai.com",
      "process":"codex","processPath":"/usr/bin/codex"},
      "upload":10,"download":20,"chains":["proxy-0"],"rule":"final"}
  ]
}`

const proxiesJSON = `{"proxies":{
  "proxy":{"type":"URLTest","now":"proxy-0","all":["proxy-0","proxy-1"],
    "history":[{"delay":42}]},
  "proxy-0":{"type":"Vless","history":[{"delay":42}]},
  "proxy-1":{"type":"Vless","history":[{"delay":88}]}
}}`

// lastSelect records the last PUT /proxies/{group} body newTestServer saw, so
// TestSelectOutbound can assert on it.
type lastSelect struct {
	group string
	name  string
}

func newTestServer(t *testing.T, secret string) (*Client, *httptest.Server) {
	return newTestServerWithSelect(t, secret, nil)
}

func newTestServerWithSelect(t *testing.T, secret string, sel *lastSelect) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch {
		case r.URL.Path == "/connections":
			_, _ = w.Write([]byte(connectionsJSON))
		case r.URL.Path == "/proxies":
			_, _ = w.Write([]byte(proxiesJSON))
		case strings.HasPrefix(r.URL.Path, "/proxies/") && strings.HasSuffix(r.URL.Path, "/delay"):
			_, _ = w.Write([]byte(`{"delay":42}`))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			if sel != nil {
				var body struct {
					Name string `json:"name"`
				}
				_ = json.NewDecoder(r.Body).Decode(&body)
				sel.group = strings.TrimPrefix(r.URL.Path, "/proxies/")
				sel.name = body.Name
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	c := &Client{BaseURL: srv.URL, Secret: secret, HTTP: srv.Client()}
	return c, srv
}

func TestConnections(t *testing.T) {
	c, srv := newTestServer(t, "tok")
	defer srv.Close()
	conns, err := c.Connections(context.Background())
	if err != nil {
		t.Fatalf("Connections: %v", err)
	}
	if len(conns) != 1 {
		t.Fatalf("want 1 connection, got %d", len(conns))
	}
	m := conns[0].Metadata
	if m.Process != "codex" {
		t.Errorf("process = %q, want codex", m.Process)
	}
	if got := m.Dest(); got != "api.openai.com:443" {
		t.Errorf("Dest() = %q", got)
	}
	if got := m.Source(); got != "127.0.0.1:54321" {
		t.Errorf("Source() = %q", got)
	}
	if got := m.SourcePortNum(); got != 54321 {
		t.Errorf("SourcePortNum() = %d", got)
	}
}

func TestConnections_AuthRequired(t *testing.T) {
	c, srv := newTestServer(t, "tok")
	defer srv.Close()
	c.Secret = "wrong"
	if _, err := c.Connections(context.Background()); err == nil {
		t.Fatal("expected auth error with wrong secret")
	}
}

func TestTraffic(t *testing.T) {
	c, srv := newTestServer(t, "tok")
	defer srv.Close()
	up, down, err := c.Traffic(context.Background())
	if err != nil {
		t.Fatalf("Traffic: %v", err)
	}
	if up != 50 || down != 100 {
		t.Errorf("Traffic() = up %d down %d, want up 50 down 100", up, down)
	}
}

func TestProxies(t *testing.T) {
	c, srv := newTestServer(t, "")
	defer srv.Close()
	proxies, err := c.Proxies(context.Background())
	if err != nil {
		t.Fatalf("Proxies: %v", err)
	}
	group := proxies["proxy"]
	if group.Now != "proxy-0" {
		t.Errorf("selected = %q, want proxy-0", group.Now)
	}
	if proxies["proxy-1"].LastDelay() != 88 {
		t.Errorf("proxy-1 delay = %d, want 88", proxies["proxy-1"].LastDelay())
	}
}

func TestSelectOutbound(t *testing.T) {
	var sel lastSelect
	c, srv := newTestServerWithSelect(t, "", &sel)
	defer srv.Close()
	if err := c.SelectOutbound(context.Background(), "proxy", "proxy-1"); err != nil {
		t.Fatalf("SelectOutbound: %v", err)
	}
	if sel.group != "proxy" || sel.name != "proxy-1" {
		t.Errorf("PUT /proxies/%s {name:%q}, want proxy {name:proxy-1}", sel.group, sel.name)
	}
}

func TestDelay(t *testing.T) {
	c, srv := newTestServer(t, "")
	defer srv.Close()
	d, err := c.Delay(context.Background(), "proxy-0", "https://example.com", 3*time.Second)
	if err != nil {
		t.Fatalf("Delay: %v", err)
	}
	if d != 42 {
		t.Errorf("delay = %d, want 42", d)
	}
}
