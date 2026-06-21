package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var errSample = errors.New("sample error")

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

func TestProcPrompt_RoutePID(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m.width, m.height = 100, 30
	m.relayout()

	// 'x' opens Приложения (launch field focused). Tab to the process filter,
	// type a PID and submit → RoutePID called.
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if !m.showProc {
		t.Fatal("'x' should open the Приложения view")
	}
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab}) // → process filter
	m = next.(Model)
	m.procInput.SetValue("12345")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if m.showProc {
		t.Error("submitting should close the view")
	}
	if cmd == nil {
		t.Fatal("expected a command from submit")
	}
	if msg := cmd(); msg == nil {
		t.Fatal("route command produced no message")
	}
	if b.routedPID != 12345 {
		t.Errorf("RoutePID got %d, want 12345", b.routedPID)
	}
}

func TestProcPrompt_LaunchCommand(t *testing.T) {
	b := &fakeBackend{}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	// Open the view so the launch field is focused (appFocus=0).
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	m.launchInput.SetValue("zen --private")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(Model)
	if cmd == nil {
		t.Fatal("expected launch command")
	}
	_ = cmd()
	if len(b.launchedArgv) != 2 || b.launchedArgv[0] != "zen" {
		t.Errorf("LaunchProxied argv = %v, want [zen --private]", b.launchedArgv)
	}
}

func TestProcPicker_ListAndRouteHighlighted(t *testing.T) {
	b := &fakeBackend{procRows: []ProcInfo{
		{PID: 100, Name: "alpha", Ports: ":80"},
		{PID: 200, Name: "codex", Ports: ":54321"},
	}}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m.width, m.height = 100, 30
	m.relayout()

	// Open the process view (fires the list command via a batch).
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	m = next.(Model)
	if !m.showProc {
		t.Fatal("'x' should open the process prompt")
	}
	// Deliver the process list message (as the command would).
	m2, _ := m.Update(procListMsg{rows: b.procRows})
	m = m2.(Model)
	if len(m.procRows) != 2 {
		t.Fatalf("expected 2 process rows, got %d", len(m.procRows))
	}
	out := m.View()
	if !strings.Contains(out, "codex") || !strings.Contains(out, "alpha") {
		t.Errorf("picker should list processes, got:\n%s", out)
	}
	// Tab to the process filter, move cursor to the second row and route it.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	m3, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = m3.(Model)
	_, rcmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if rcmd == nil {
		t.Fatal("expected a route command")
	}
	_ = rcmd()
	if b.routedPID != 200 {
		t.Errorf("routed PID = %d, want 200 (highlighted codex)", b.routedPID)
	}
}

func TestProcPicker_FilterByName(t *testing.T) {
	b := &fakeBackend{procRows: []ProcInfo{
		{PID: 100, Name: "alpha"}, {PID: 200, Name: "codex"},
	}}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m.showProc = true
	m.procRows = b.procRows
	m.procInput.SetValue("cod")
	fp := m.filteredProcs()
	if len(fp) != 1 || fp[0].Name != "codex" {
		t.Errorf("filter 'cod' = %+v, want only codex", fp)
	}
}

func TestProcPicker_RestartHighlighted(t *testing.T) {
	b := &fakeBackend{procRows: []ProcInfo{{PID: 321, Name: "codex"}}}
	m := newWithCaps(b, nil, asciiCaps()).WithLoadedProfile()
	m.showProc = true
	m.procRows = b.procRows
	// ctrl+r restarts the highlighted process.
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	if next.(Model).ShowingConns() {
		t.Error("ctrl+r should not open connections")
	}
	if cmd == nil {
		t.Fatal("expected a restart command")
	}
	_ = cmd()
	if b.restartedPID != 321 {
		t.Errorf("restarted PID = %d, want 321", b.restartedPID)
	}
}

func TestProcResultMsg_ShowsError(t *testing.T) {
	m := dashboardModel(t)
	next, _ := m.Update(procResultMsg{err: errSample})
	if got := next.(Model).ErrText(); got != errSample.Error() {
		t.Errorf("errText = %q, want %q", got, errSample.Error())
	}
}
