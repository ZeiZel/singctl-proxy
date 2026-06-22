package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// ===========================================================================
// consoleBuf — ring trim, per-PID tagging, ConsoleMsg append rendering
// ===========================================================================

func TestConsoleBuf_RingTrim(t *testing.T) {
	c := &consoleBuf{max: 5, apps: map[int]string{}}
	for i := 0; i < 20; i++ {
		c.append(consoleEntry{PID: 1, App: "zen", Stream: "stdout", Text: "line"})
	}
	if len(c.lines) != 5 {
		t.Fatalf("ring should be trimmed to max=5, got %d", len(c.lines))
	}
}

func TestConsoleBuf_PerPIDTagging(t *testing.T) {
	c := newConsoleBuf()
	c.append(consoleEntry{PID: 42, App: "zen", Stream: "stdout", Text: "a"})
	c.append(consoleEntry{PID: 99, App: "cursor", Stream: "stdout", Text: "b"})
	// A later line for PID 42 with no app name still resolves to "zen".
	c.append(consoleEntry{PID: 42, App: "", Stream: "stdout", Text: "c"})

	if got := c.appName(42); got != "zen" {
		t.Errorf("appName(42) = %q, want zen", got)
	}
	if got := c.appName(99); got != "cursor" {
		t.Errorf("appName(99) = %q, want cursor", got)
	}
	if got := c.appName(7); got != "app" {
		t.Errorf("unknown PID should fall back to app, got %q", got)
	}
	if pids := c.pids(); len(pids) != 2 || pids[0] != 42 || pids[1] != 99 {
		t.Errorf("pids() = %v, want [42 99] (sorted)", pids)
	}
}

func TestConsoleBuf_RenderTagsAppPID(t *testing.T) {
	s := asciiModel().styles
	c := newConsoleBuf()
	c.append(consoleEntry{PID: 42, App: "zen", Stream: "stdout", Text: "hello world"})
	out := c.render(s, 80, 0, 0)
	if !strings.Contains(out, "[zen 42]") {
		t.Errorf("rendered console line should be tagged [app pid], got:\n%s", out)
	}
	if !strings.Contains(out, "hello world") {
		t.Errorf("rendered console should contain the text, got:\n%s", out)
	}
}

func TestConsoleBuf_RenderFilterByPID(t *testing.T) {
	s := asciiModel().styles
	c := newConsoleBuf()
	c.append(consoleEntry{PID: 42, App: "zen", Stream: "stdout", Text: "from-zen"})
	c.append(consoleEntry{PID: 99, App: "cursor", Stream: "stdout", Text: "from-cursor"})
	out := c.render(s, 80, 0, 99)
	if strings.Contains(out, "from-zen") {
		t.Errorf("filter=99 should hide zen's output:\n%s", out)
	}
	if !strings.Contains(out, "from-cursor") {
		t.Errorf("filter=99 should keep cursor's output:\n%s", out)
	}
}

// A ConsoleMsg pushed through the reducer appends to the ring and surfaces in the
// (collapsed) КОНСОЛЬ pane tagged "[app pid]".
func TestConsoleMsg_AppendRendersTagged(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 40})
	m, _ = step(m, ConsoleMsg{PID: 1234, App: "sleep", Stream: "stdout", Text: "tick"})
	if n := len(m.console.lines); n != 1 {
		t.Fatalf("ConsoleMsg should append one console line, got %d", n)
	}
	out := m.View()
	if !strings.Contains(out, "[sleep 1234]") {
		t.Errorf("console pane should render the tagged line:\n%s", out)
	}
}

// ===========================================================================
// actionLog — ring trim, level rendering, ActionMsg append
// ===========================================================================

func TestActionLog_RingTrim(t *testing.T) {
	a := &actionLog{max: 4}
	for i := 0; i < 30; i++ {
		a.add(ActInfo, "x")
	}
	if len(a.lines) != 4 {
		t.Fatalf("action log should trim to max=4, got %d", len(a.lines))
	}
}

func TestActionLog_LevelRendering(t *testing.T) {
	s := asciiModel().styles
	a := newActionLog()
	a.add(ActOk, "готово")
	a.add(ActWarn, "осторожно")
	a.add(ActErr, "ошибка")
	out := a.render(s, 80, 0)
	// The ascii-safe level glyphs keep severity legible under NO_COLOR.
	for _, want := range []string{"OK", "!!", "ERR", "готово", "осторожно", "ошибка"} {
		if !strings.Contains(out, want) {
			t.Errorf("action-log render missing %q:\n%s", want, out)
		}
	}
}

// An async ActionMsg (an event the UI never initiated, e.g. Cisco auto-suspend)
// pushed through the reducer lands in the action log.
func TestActionMsg_AppendToLog(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, ActionMsg{Level: ActWarn, Text: "Cisco активен — авто-приостановка"})
	if n := len(m.actions.lines); n != 1 {
		t.Fatalf("ActionMsg should append one action entry, got %d", n)
	}
	last := m.actions.lines[0]
	if last.Level != ActWarn || last.Text != "Cisco активен — авто-приостановка" {
		t.Errorf("ActionMsg recorded wrong entry: level=%d text=%q", last.Level, last.Text)
	}
}

// ===========================================================================
// button bar — each btnSpec.fire mirrors its key shortcut; zones are marked
// ===========================================================================

// fireButton runs the named button's fire and resolves any command it returns.
func fireButton(t *testing.T, m Model, id string) (Model, tea.Msg) {
	t.Helper()
	for _, sp := range m.actionButtons() {
		if sp.id == id {
			mm, cmd := sp.fire(m)
			var msg tea.Msg
			if cmd != nil {
				msg = cmd()
			}
			return mm.(Model), msg
		}
	}
	t.Fatalf("no button with id %q", id)
	return m, nil
}

// runKey runs a single key on a fresh dashboard and resolves its command.
func runKey(m Model, k tea.KeyMsg) (Model, tea.Msg) {
	m, cmd := step(m, k)
	var msg tea.Msg
	if cmd != nil {
		msg = cmd()
	}
	return m, msg
}

func TestButtons_ModeFireMatchesKey(t *testing.T) {
	cases := []struct {
		id  string
		key tea.KeyMsg
		seg int
	}{
		{"btn-mode-1", rune_("p"), 1},
		{"btn-stop", rune_("s"), 0},
	}
	for _, tc := range cases {
		base := func() Model { return New(&fakeBackend{}, nil).WithLoadedProfile() }
		mb, msgb := fireButton(t, base(), tc.id)
		mk, msgk := runKey(base(), tc.key)
		if mb.busy != mk.busy {
			t.Errorf("%s: busy mismatch button=%v key=%v", tc.id, mb.busy, mk.busy)
		}
		if mb.segCursor != tc.seg || mk.segCursor != tc.seg {
			t.Errorf("%s: segCursor button=%d key=%d want %d", tc.id, mb.segCursor, mk.segCursor, tc.seg)
		}
		if _, ok := msgb.(errMsg); ok {
			t.Errorf("%s: button fire produced errMsg", tc.id)
		}
		if msgTypeName(msgb) != msgTypeName(msgk) {
			t.Errorf("%s: button cmd %T != key cmd %T", tc.id, msgb, msgk)
		}
	}
}

func msgTypeName(m tea.Msg) string {
	if m == nil {
		return "<nil>"
	}
	switch m.(type) {
	case proxyEnabledMsg:
		return "proxy"
	case vpnEnabledMsg:
		return "vpn"
	case stoppedMsg:
		return "stopped"
	default:
		return "other"
	}
}

func TestButtons_SectionFireOpensOverlay(t *testing.T) {
	cases := []struct {
		id    string
		check func(Model) bool
		pane  int
	}{
		{"btn-logs", func(m Model) bool { return m.showLogs }, paneSingbox},
		{"btn-conns", func(m Model) bool { return m.showConns }, paneConns},
		{"btn-apps", func(m Model) bool { return m.showProc }, paneApps},
		{"btn-settings", func(m Model) bool { return m.showSettings }, paneSettings},
		{"btn-console", func(m Model) bool { return m.expanded && m.pane == paneConsole }, paneConsole},
	}
	for _, tc := range cases {
		m := New(&fakeBackend{}, nil).WithLoadedProfile()
		m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
		m, _ = fireButton(t, m, tc.id)
		if !tc.check(m) {
			t.Errorf("%s: fire did not open the expected overlay (pane=%d expanded=%v)", tc.id, m.pane, m.expanded)
		}
		if m.pane != tc.pane {
			t.Errorf("%s: fire should focus pane %d, got %d", tc.id, tc.pane, m.pane)
		}
	}
}

func TestButtons_DaemonFireIssuesCmd(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m, msg := fireButton(t, m, "btn-daemon")
	if m.status == "" {
		t.Error("daemon button should set a status note")
	}
	if _, ok := msg.(daemonizedMsg); !ok {
		t.Errorf("daemon button should run daemonizeCmd, got %T", msg)
	}
	if b.daemonCalls != 1 {
		t.Errorf("daemon button should call Daemonize once, got %d", b.daemonCalls)
	}
}

func TestButtons_DaemonBecomesStopWhenAttached(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile().WithAttached(4242)
	var found btnSpec
	for _, sp := range m.actionButtons() {
		if sp.id == "btn-daemon" {
			found = sp
		}
	}
	if found.label != "Остановить демон" {
		t.Fatalf("attached daemon button label = %q, want Остановить демон", found.label)
	}
	mm, cmd := found.fire(m)
	_ = mm
	if cmd == nil {
		t.Fatal("attached daemon button should issue a stop command")
	}
	if _, ok := cmd().(daemonStoppedMsg); !ok {
		t.Error("attached daemon button should run stopDaemonCmd")
	}
	if b.stopDaemonCalls != 1 {
		t.Errorf("StopDaemon should be called once, got %d", b.stopDaemonCalls)
	}
}

func TestButtons_ModeActiveHighlight(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.mode = RunProxy
	if !m.buttonActive(btnSpec{id: "btn-mode-1"}) {
		t.Error("PROXY mode button should be active when mode==RunProxy")
	}
	if m.buttonActive(btnSpec{id: "btn-mode-0"}) || m.buttonActive(btnSpec{id: "btn-mode-2"}) {
		t.Error("only the running mode button should be active")
	}
}

// The button pills are bubblezone-marked so handleMouse can hit-test them before
// the pane grid; clicking ПРОКСИ runs the same path as the key shortcut.
func TestButtons_ZonesMarkedAndClickFires(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 120, Height: 36})
	// Sanity: the button pill is rendered with its zone marker.
	z := waitZone(m, "btn-mode-1")
	if z.IsZero() {
		t.Fatal("btn-mode-1 zone should be marked in the action bar")
	}
	m, cmd := clickZone(t, m, "btn-mode-1")
	if m.segCursor != 1 {
		t.Errorf("clicking ПРОКСИ should move the selector to PROXY, got %d", m.segCursor)
	}
	if !m.busy {
		t.Error("clicking ПРОКСИ should set busy (mode command in flight)")
	}
	if cmd == nil {
		t.Fatal("clicking ПРОКСИ should issue a command")
	}
	if _, ok := cmd().(proxyEnabledMsg); !ok {
		t.Errorf("clicking ПРОКСИ should run EnableProxy, got %T", cmd())
	}
	if b.proxyCalls != 1 {
		t.Errorf("EnableProxy should be called once, got %d", b.proxyCalls)
	}
}

// ===========================================================================
// robustness — a panicking safe() command degrades to errMsg (no crash) and the
// reducer records it as an ActErr action.
// ===========================================================================

func TestSafe_PanicBecomesErrMsg(t *testing.T) {
	cmd := safe(func() tea.Msg { panic("kaboom") })
	var msg tea.Msg
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("safe() must not let the panic escape, got %v", r)
			}
		}()
		msg = cmd()
	}()
	em, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("panicking command should yield errMsg, got %T", msg)
	}
	if !strings.Contains(em.err.Error(), "kaboom") {
		t.Errorf("errMsg should mention the panic value, got %q", em.err.Error())
	}
}

func TestErrMsg_SetsErrTextAndActErr(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	cmd := safe(func() tea.Msg { panic("kaboom") })
	m, _ = step(m, cmd())
	if m.ErrText() == "" {
		t.Error("a surfaced errMsg should set errText")
	}
	if len(m.actions.lines) == 0 {
		t.Fatal("errMsg should record an action-log entry")
	}
	last := m.actions.lines[len(m.actions.lines)-1]
	if last.Level != ActErr {
		t.Errorf("errMsg action should be recorded at ActErr level, got %d", last.Level)
	}
	if !strings.Contains(last.Text, "kaboom") {
		t.Errorf("errMsg action text should carry the error, got %q", last.Text)
	}
}
