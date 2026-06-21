package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func dashboardModel(t *testing.T) Model {
	t.Helper()
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps())
	m = m.WithLoadedProfile()
	m.width, m.height = 100, 30
	m.relayout()
	return m
}

func TestConnectionsMsg_StoresRows(t *testing.T) {
	m := dashboardModel(t)
	rows := []ConnRow{{Process: "codex", Source: "127.0.0.1:54321", Dest: "api.openai.com:443", Network: "tcp", Chain: "proxy-0"}}
	next, _ := m.Update(ConnectionsMsg{Rows: rows})
	got := next.(Model)
	if len(got.Conns()) != 1 || got.Conns()[0].Process != "codex" {
		t.Fatalf("ConnectionsMsg not stored: %+v", got.Conns())
	}
}

func TestLatencyMsg_StoresAndSummarizes(t *testing.T) {
	m := dashboardModel(t)
	next, _ := m.Update(LatencyMsg{Selected: "proxy-1", Rows: []LatencyRow{
		{Tag: "proxy-0", Delay: 42},
		{Tag: "proxy-1", Delay: 88, Selected: true},
	}})
	got := next.(Model)
	if len(got.Latency()) != 2 {
		t.Fatalf("latency not stored: %+v", got.Latency())
	}
	if sum := got.latencySummary(); !strings.Contains(sum, "proxy-1") || !strings.Contains(sum, "88ms") {
		t.Errorf("latencySummary = %q, want selected proxy-1 88ms", sum)
	}
}

func TestConnsToggle_OpenAndClose(t *testing.T) {
	m := dashboardModel(t)
	// 'c' opens the connections overlay.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("c")})
	got := next.(Model)
	if !got.ShowingConns() {
		t.Fatal("pressing 'c' should open the connections overlay")
	}
	// 'esc' closes it.
	next2, _ := got.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if next2.(Model).ShowingConns() {
		t.Error("esc should close the connections overlay")
	}
}

func TestConnsView_RendersRows(t *testing.T) {
	m := dashboardModel(t)
	m.showConns = true
	m.conns = []ConnRow{{Process: "codex", Source: "127.0.0.1:54321", Dest: "api.openai.com:443", Network: "tcp", Chain: "proxy-0"}}
	m.latency = []LatencyRow{{Tag: "proxy-0", Delay: 42, Selected: true}}
	out := m.View()
	for _, want := range []string{"codex", "api.openai.com:443", "proxy-0", "42ms"} {
		if !strings.Contains(out, want) {
			t.Errorf("connsView missing %q in:\n%s", want, out)
		}
	}
}

func TestConnsView_EmptyState(t *testing.T) {
	m := dashboardModel(t)
	m.showConns = true
	out := m.View()
	if !strings.Contains(out, "нет активных соединений") {
		t.Errorf("expected empty-state text, got:\n%s", out)
	}
}
