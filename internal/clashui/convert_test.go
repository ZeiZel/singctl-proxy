package clashui

import "testing"

import "singctl/internal/clashapi"

func TestConnRows(t *testing.T) {
	rows := ConnRows([]clashapi.Connection{{
		Metadata: clashapi.Metadata{Network: "tcp", SourceIP: "127.0.0.1", SourcePort: "54321",
			Host: "api.openai.com", DestinationPort: "443", Process: "codex"},
		Chains: []string{"proxy-0", "direct"},
	}})
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Process != "codex" || r.Dest != "api.openai.com:443" || r.Source != "127.0.0.1:54321" {
		t.Errorf("row = %+v", r)
	}
	if r.Chain != "proxy-0→direct" {
		t.Errorf("chain = %q, want proxy-0→direct", r.Chain)
	}
}

func TestLatencyMsg_MultiServer(t *testing.T) {
	msg, ok := LatencyMsg(map[string]clashapi.ProxyState{
		"proxy":   {Type: "URLTest", Now: "proxy-1", All: []string{"proxy-0", "proxy-1"}},
		"proxy-0": {History: []clashapi.DelayHistory{{Delay: 42}}},
		"proxy-1": {History: []clashapi.DelayHistory{{Delay: 88}}},
	})
	if !ok {
		t.Fatal("expected ok for present group")
	}
	if msg.Selected != "proxy-1" || len(msg.Rows) != 2 {
		t.Fatalf("msg = %+v", msg)
	}
	if msg.Rows[1].Tag != "proxy-1" || msg.Rows[1].Delay != 88 || !msg.Rows[1].Selected {
		t.Errorf("row1 = %+v", msg.Rows[1])
	}
}

func TestLatencyMsg_SingleServer(t *testing.T) {
	msg, ok := LatencyMsg(map[string]clashapi.ProxyState{
		"proxy": {Type: "Vless", History: []clashapi.DelayHistory{{Delay: 30}}},
	})
	if !ok || len(msg.Rows) != 1 || msg.Rows[0].Tag != "proxy" || msg.Rows[0].Delay != 30 {
		t.Errorf("single-server msg = %+v ok=%v", msg, ok)
	}
}

func TestLatencyMsg_Missing(t *testing.T) {
	if _, ok := LatencyMsg(map[string]clashapi.ProxyState{}); ok {
		t.Error("missing group should return ok=false")
	}
}
