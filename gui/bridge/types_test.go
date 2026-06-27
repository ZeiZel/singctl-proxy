package bridge

import (
	"testing"

	"singctl/internal/clashapi"
)

func TestMaskKey(t *testing.T) {
	// Full link with credential, query and #fragment label.
	k := maskKey("vless://uuid-secret@example.com:443?type=grpc#Tokyo", 0)
	if k.Name != "Tokyo" {
		t.Errorf("name = %q, want Tokyo", k.Name)
	}
	if k.Masked != "vless://••••@example.com:443" {
		t.Errorf("masked = %q, want vless://••••@example.com:443", k.Masked)
	}
	if got := maskKey("vless://x@h:1", 2).Name; got != "Key 3" {
		t.Errorf("default name = %q, want Key 3", got)
	}
	// The raw secret must never appear in the masked form.
	if m := maskKey("vless://supersecret@h:1#n", 0).Masked; m == "" || containsSecret(m) {
		t.Errorf("masked leaks secret: %q", m)
	}
}

func containsSecret(s string) bool {
	return len(s) > 0 && (indexOf(s, "supersecret") >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestLatencyFrom(t *testing.T) {
	// Multi-server urltest group.
	proxies := map[string]clashapi.ProxyState{
		"proxy":   {Type: "URLTest", Now: "proxy-0", All: []string{"proxy-0", "proxy-1"}},
		"proxy-0": {History: []clashapi.DelayHistory{{Delay: 42}}},
		"proxy-1": {History: []clashapi.DelayHistory{{Delay: 88}}},
	}
	lat, ok := latencyFrom(proxies)
	if !ok || lat.Selected != "proxy-0" || len(lat.Rows) != 2 {
		t.Fatalf("latency = %+v ok=%v", lat, ok)
	}
	if lat.Rows[0].Delay != 42 || !lat.Rows[0].Selected || lat.Rows[1].Delay != 88 {
		t.Errorf("rows = %+v", lat.Rows)
	}
	// No "proxy" entry → not ok.
	if _, ok := latencyFrom(map[string]clashapi.ProxyState{}); ok {
		t.Error("latencyFrom with no group should be !ok")
	}
}

func TestConnRows(t *testing.T) {
	conns := []clashapi.Connection{{
		Metadata: clashapi.Metadata{
			Network: "tcp", Process: "curl", SourceIP: "127.0.0.1", SourcePort: "5000",
			DestinationIP: "1.1.1.1", DestinationPort: "443", Host: "example.com",
		},
		Chains: []string{"proxy-0", "proxy"},
	}}
	rows := connRows(conns)
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Process != "curl" || r.Dest != "example.com:443" || r.Source != "127.0.0.1:5000" || r.Chain != "proxy-0→proxy" {
		t.Errorf("row = %+v", r)
	}
}
