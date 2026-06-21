package ui

import (
	"strings"
	"testing"
)

// The dashboard must surface live connections + server latency WITHOUT opening
// the `c` overlay, so the interface visibly shows the functionality.
func TestDashboard_ShowsConnectionsPanel(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m.mode = RunProxy
	m.width, m.height = 100, 32
	m.relayout()

	// Deliver live data via the normal async messages.
	m2, _ := m.Update(ConnectionsMsg{Rows: []ConnRow{
		{Process: "codex", Source: "127.0.0.1:54321", Dest: "api.openai.com:443", Network: "tcp", Chain: "proxy-0"},
	}})
	m = m2.(Model)
	m3, _ := m.Update(LatencyMsg{Selected: "proxy-0", Rows: []LatencyRow{
		{Tag: "proxy-0", Delay: 42, Selected: true}, {Tag: "proxy-1", Delay: 88},
	}})
	m = m3.(Model)

	if m.ShowingConns() {
		t.Fatal("connections must show on the dashboard, not via the overlay")
	}
	out := m.View()
	for _, want := range []string{"СОЕДИНЕНИЯ", "codex", "api.openai.com:443", "proxy-0", "42ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("dashboard missing %q:\n%s", want, out)
		}
	}
}

func TestDashboard_ConnectionsEmptyState(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m.width, m.height = 100, 32
	m.relayout()
	out := m.View()
	if !strings.Contains(out, "нет активных соединений") {
		t.Errorf("dashboard should show the empty connections state:\n%s", out)
	}
}

func TestDashboard_ConnsCappedAtDashRows(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m.width, m.height = 100, 40
	m.relayout()
	for i := 0; i < dashConnRows+5; i++ {
		m.conns = append(m.conns, ConnRow{Process: "p", Source: "127.0.0.1:1", Dest: "h:443", Network: "tcp"})
	}
	out := m.View()
	if !strings.Contains(out, "…ещё 5") {
		t.Errorf("dashboard should cap connection rows and show the overflow tail:\n%s", out)
	}
}

func TestDashboard_CompactCollapsesToSummary(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m.mode = RunProxy
	m.width, m.height = 80, 16 // compact (h < 20)
	m.relayout()
	m.conns = []ConnRow{{Process: "codex", Source: "127.0.0.1:1", Dest: "h:443", Network: "tcp"}}
	out := m.View()
	if !strings.Contains(out, "1 соединений") {
		t.Errorf("compact dashboard should collapse connections to a one-line summary:\n%s", out)
	}
}
