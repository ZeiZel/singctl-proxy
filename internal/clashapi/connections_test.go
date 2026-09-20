package clashapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- Empty-state diagnoses (docs/v2-spec.md F6 item 3) ---

func TestConnections_StateNoMode(t *testing.T) {
	// No client at all is exactly what the executor passes when no mode is
	// running (it never even builds a Client) — modeRunning is what decides
	// here, so nil client + modeRunning=false must still report no_mode, not
	// api_disabled.
	got := Connections(context.Background(), nil, false, false)
	if got.State != StateNoMode {
		t.Errorf("State = %q, want %q", got.State, StateNoMode)
	}
	if len(got.Rows) != 0 {
		t.Errorf("Rows = %v, want empty", got.Rows)
	}
}

func TestConnections_StateAPIDisabled(t *testing.T) {
	// A mode is running, but the Clash API was never enabled in Settings —
	// there is no client to call.
	got := Connections(context.Background(), nil, true, false)
	if got.State != StateAPIDisabled {
		t.Errorf("State = %q, want %q", got.State, StateAPIDisabled)
	}
}

func TestConnections_StateAPIUnreachable(t *testing.T) {
	// A mode is running and the API is enabled, but the address does not
	// answer at all (closed test server) — the request itself fails.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.URL
	srv.Close() // now guaranteed unreachable

	client := &Client{BaseURL: addr, HTTP: &http.Client{}}
	got := Connections(context.Background(), client, true, true)
	if got.State != StateAPIUnreachable {
		t.Fatalf("State = %q, want %q", got.State, StateAPIUnreachable)
	}
	if !strings.Contains(got.Detail, addr) {
		t.Errorf("Detail = %q, want it to mention the address %q", got.Detail, addr)
	}
}

func TestConnections_StateIdle(t *testing.T) {
	// A mode is running, the API is enabled and reachable, but there is
	// simply no traffic right now.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"downloadTotal":0,"uploadTotal":0,"connections":[]}`))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	got := Connections(context.Background(), client, true, true)
	if got.State != StateIdle {
		t.Errorf("State = %q, want %q", got.State, StateIdle)
	}
	if len(got.Rows) != 0 {
		t.Errorf("Rows = %v, want empty", got.Rows)
	}
}

func TestConnections_StateActive(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(connectionsJSON))
	}))
	defer srv.Close()

	client := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	got := Connections(context.Background(), client, true, true)
	if got.State != StateActive {
		t.Fatalf("State = %q, want %q", got.State, StateActive)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("Rows = %v, want 1 row", got.Rows)
	}
	if got.Rows[0].App != "codex" {
		t.Errorf("Rows[0].App = %q, want codex", got.Rows[0].App)
	}
}

// --- Row mapping ---

func TestBuildRows_NoProcessMetadata(t *testing.T) {
	conns := []Connection{{
		ID: "c1",
		Metadata: Metadata{
			Network: "tcp", DestinationIP: "1.2.3.4", DestinationPort: "443",
			// Process and ProcessPath deliberately left empty.
		},
		Upload: 5, Download: 7, Chains: []string{"proxy"}, Rule: "final",
	}}
	rows := BuildRows(conns)
	if len(rows) != 1 {
		t.Fatalf("BuildRows: got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.App != "" || r.Process != "" {
		t.Errorf("App/Process = %q/%q, want both empty", r.App, r.Process)
	}
	if r.Host != "1.2.3.4" {
		t.Errorf("Host = %q, want the destination IP fallback", r.Host)
	}
	if r.Upload != 5 || r.Download != 7 {
		t.Errorf("Upload/Download = %d/%d, want 5/7", r.Upload, r.Download)
	}
	if r.Rule != "final" || len(r.Chain) != 1 || r.Chain[0] != "proxy" {
		t.Errorf("Rule/Chain = %q/%v, want final/[proxy]", r.Rule, r.Chain)
	}
}

func TestAppName_PrefersProcessPathBasename(t *testing.T) {
	m := Metadata{Process: "codex", ProcessPath: "/usr/local/bin/codex-cli"}
	if got, want := AppName(m), "codex-cli"; got != want {
		t.Errorf("AppName() = %q, want %q", got, want)
	}
}

func TestAppName_FallsBackToProcess(t *testing.T) {
	m := Metadata{Process: "codex"}
	if got, want := AppName(m), "codex"; got != want {
		t.Errorf("AppName() = %q, want %q", got, want)
	}
}

// --- Aggregates ---

func TestAggregate_SumsAcrossRows(t *testing.T) {
	rows := []Row{
		{App: "codex", Host: "api.openai.com", Upload: 10, Download: 20},
		{App: "codex", Host: "api.openai.com", Upload: 5, Download: 1},
		{App: "codex", Host: "other.example.com", Upload: 1, Download: 1},
		{App: "", Host: "", Upload: 2, Download: 2}, // no process metadata at all
	}
	apps, dests := Aggregate(rows)

	var codex, unknown *AppTotal
	for i := range apps {
		switch apps[i].App {
		case "codex":
			codex = &apps[i]
		case "?":
			unknown = &apps[i]
		}
	}
	if codex == nil || codex.Upload != 16 || codex.Download != 22 || codex.Count != 3 {
		t.Errorf("codex app total = %+v, want upload=16 download=22 count=3", codex)
	}
	if unknown == nil || unknown.Count != 1 {
		t.Errorf("unresolved app total = %+v, want count=1", unknown)
	}

	var dest1, dest2 *DestTotal
	for i := range dests {
		switch dests[i].Host {
		case "api.openai.com":
			dest1 = &dests[i]
		case "other.example.com":
			dest2 = &dests[i]
		}
	}
	if dest1 == nil || dest1.Upload != 15 || dest1.Download != 21 || dest1.Count != 2 {
		t.Errorf("api.openai.com dest total = %+v, want upload=15 download=21 count=2", dest1)
	}
	if dest2 == nil || dest2.Count != 1 {
		t.Errorf("other.example.com dest total = %+v, want count=1", dest2)
	}
}

// --- Close connection ---

func TestCloseConnection_CallsDeleteEndpoint(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.CloseConnection(context.Background(), "conn-123"); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	if gotPath != "/connections/conn-123" {
		t.Errorf("path = %q, want /connections/conn-123", gotPath)
	}
}

func TestCloseConnection_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &Client{BaseURL: srv.URL, HTTP: srv.Client()}
	if err := c.CloseConnection(context.Background(), "missing"); err == nil {
		t.Fatal("expected error for 404 status")
	}
}
