package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	zone "github.com/lrstanley/bubblezone"
)

// --- mouse-zone test helpers ----------------------------------------------
//
// bubblezone records zone bounds asynchronously from a background worker fed by
// Scan (called inside View). waitZone renders the model until the requested
// zone's bounds are known (or a short deadline elapses), so a click can be
// targeted at it deterministically.

func waitZone(m Model, id string) *zone.ZoneInfo {
	_ = m.View()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if z := m.zm.Get(id); !z.IsZero() {
			return z
		}
		time.Sleep(time.Millisecond)
		_ = m.View()
	}
	return m.zm.Get(id)
}

// clickZone renders m, locates the named zone and dispatches a left-press in its
// interior, returning the resulting model + command.
func clickZone(t *testing.T, m Model, id string) (Model, tea.Cmd) {
	t.Helper()
	z := waitZone(m, id)
	if z.IsZero() {
		t.Fatalf("zone %q never became known", id)
	}
	click := tea.MouseMsg{
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
		X:      z.StartX + 1,
		Y:      z.StartY,
	}
	return step(m, click)
}

// --- height: footer must never be pushed off-screen (the height analogue of
// TestView_NeverExceedsWidth), now swept across the previously-untested band. ---

func TestView_FillsHeight_AllBands(t *testing.T) {
	for _, w := range []int{40, 70, 110} {
		for h := 8; h <= 44; h++ {
			for _, cisco := range []bool{false, true} {
				m := asciiModel()
				m.cisco = cisco
				m.errText = "не удалось разобрать ссылку: неизвестный протокол в длинном сообщении"
				m, _ = step(m, tea.WindowSizeMsg{Width: w, Height: h})
				if got := strings.Count(m.View(), "\n") + 1; got > h {
					t.Errorf("dashboard @ %dx%d (cisco=%v) rendered %d lines > %d", w, h, cisco, got, h)
				}
			}
		}
	}
}

// --- disabled VPN affordance survives NO_COLOR/ascii (review finding #3) ---
//
// The pane-grid layout squeezes pane bodies into equal slices, so the colour-
// independent note shows whenever the СТАТУС pane has body room (narrow + wide,
// and a tall medium). The tight medium case is covered by the height sweep.

func TestDashboard_CiscoBlockedAffordance(t *testing.T) {
	for _, sz := range []tea.WindowSizeMsg{{Width: 40, Height: 20}, {Width: 70, Height: 40}, {Width: 110, Height: 30}} {
		m := asciiModel()
		m.cisco = true
		m, _ = step(m, sz)
		out := m.View()
		if !strings.Contains(out, "заблокирован") {
			t.Errorf("@ %dx%d: expected a colour-independent 'VPN blocked' note, got:\n%s", sz.Width, sz.Height, out)
		}
	}
}

// --- busy / spinner lifecycle (review findings #15, #24) ---

func TestBusy_SetThenClearedOnSuccess(t *testing.T) {
	b := &fakeBackend{}
	m := New(b, nil).WithLoadedProfile()
	m, cmd := step(m, rune_("p"))
	if !m.Busy() {
		t.Fatal("issuing PROXY should set busy")
	}
	m, _ = step(m, cmd())
	if m.Busy() {
		t.Error("busy must clear once the result arrives")
	}
}

func TestBusy_ClearedOnError(t *testing.T) {
	b := &fakeBackend{proxyErr: errors.New("boom")}
	m := New(b, nil).WithLoadedProfile()
	m, cmd := step(m, rune_("p"))
	m, _ = step(m, cmd())
	if m.Busy() {
		t.Error("busy must clear on errMsg")
	}
	if m.ErrText() == "" {
		t.Error("error should be shown")
	}
	if m.Status() != "" {
		t.Errorf("stale '…' status should be dropped on error, got %q", m.Status())
	}
}

func TestStatusMsg_ClearsBusy(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.busy = true
	m.mode = RunVPN
	m, _ = step(m, StatusMsg{Mode: RunOff, Note: "Cisco активен — остановлено"})
	if m.Busy() {
		t.Error("an executor StatusMsg (auto-suspend) must clear busy")
	}
	if m.SegCursor() != int(RunOff) {
		t.Errorf("cursor should follow the new mode, got %d", m.SegCursor())
	}
}

func TestSpinnerTick_ReschedulesItself(t *testing.T) {
	m := New(&fakeBackend{}, nil)
	_, cmd := step(m, spinner.TickMsg{})
	if cmd == nil {
		t.Fatal("spinner.TickMsg must return a follow-up command so the busy animation stays alive")
	}
}

// --- selector navigation wrap-around (review finding #23) ---

func TestSelector_WrapNavigation(t *testing.T) {
	mk := func() Model {
		m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
		m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
		return m
	}
	// The OFF/PROXY/VPN selector is driven by ←/→ while the СТАТУС pane is focused
	// (focus == -1, the default). Tab moves the focused pane around the grid ring.
	left := tea.KeyMsg{Type: tea.KeyLeft}
	right := tea.KeyMsg{Type: tea.KeyRight}

	m := mk()
	for _, want := range []int{1, 2, 0} {
		m, _ = step(m, right)
		if m.SegCursor() != want {
			t.Fatalf("right: cursor = %d, want %d", m.SegCursor(), want)
		}
	}
	m = mk()
	m, _ = step(m, left)
	if m.SegCursor() != 2 {
		t.Errorf("left from 0 should wrap to 2, got %d", m.SegCursor())
	}
	m.segCursor = 2
	m, _ = step(m, right)
	if m.SegCursor() != 0 {
		t.Errorf("right from 2 should wrap to 0, got %d", m.SegCursor())
	}
}

// --- sidebar model: ↑/↓/Tab move m.section; Enter/→/1-7/'o'/click open ---

func TestSidebar_TabCyclesSectionsAndModeIsDefault(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// Selection defaults to the Режим home (the OFF/PROXY/VPN selector lives there).
	if m.Section() != navMode {
		t.Fatalf("section should default to navMode, got %d", m.Section())
	}
	// Tab walks the nav ring; the first Tab focuses Соединения.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Section() != navConns {
		t.Fatalf("first Tab should focus navConns (%d), got %d", navConns, m.Section())
	}
	// Tab all the way around returns to navMode.
	for i := 0; i < navCount-1; i++ {
		m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	}
	if m.Section() != navMode {
		t.Errorf("Tab should wrap back to navMode, got %d", m.Section())
	}
}

func TestSidebar_ArrowsMoveSelection(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.Section() != navConns {
		t.Fatalf("Down should move to navConns, got %d", m.Section())
	}
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.Section() != navMode {
		t.Errorf("Up should return to navMode, got %d", m.Section())
	}
}

func TestSidebar_EnterOpensSection(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab}) // → navConns
	m, _ = step(m, enterKey)
	if !m.ShowingConns() {
		t.Error("Enter on navConns should open the connections view")
	}
}

func TestSidebar_NumberJumpOpens(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// '3' → navApps (index 2) → opens the Приложения picker.
	m, _ = step(m, rune_("3"))
	if m.Section() != navApps || !m.showProc {
		t.Errorf("'3' should open Приложения (section=%d showProc=%v)", m.Section(), m.showProc)
	}
}

func TestSidebar_OOpensConsole(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	m, _ = step(m, rune_("o"))
	if !m.ShowingConsole() || m.Section() != navConsole {
		t.Errorf("'o' should open the console section (showConsole=%v section=%d)", m.ShowingConsole(), m.Section())
	}
	m, _ = step(m, escKey)
	if m.ShowingConsole() {
		t.Error("Esc should close the console overlay")
	}
}

func TestSidebar_SelectorRightDrivesSegCursor(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// ←/→ on the Режим home drives the OFF/PROXY/VPN cursor.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.SegCursor() != 1 {
		t.Fatalf("right on the Режим home should advance the selector to PROXY, got %d", m.SegCursor())
	}
	// Enter on the selector applies the mode.
	m, cmd := step(m, enterKey)
	if cmd == nil {
		t.Fatal("Enter on the selector should issue a mode command")
	}
	m, _ = step(m, cmd())
	if m.Mode() != RunProxy {
		t.Errorf("Enter on the PROXY selector should start proxy, got %v", m.Mode())
	}
}

func TestSelector_CursorRestoredAfterCiscoBlock(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m.cisco = true
	m.segCursor = 2 // on VPN
	m, _ = step(m, enterKey)
	if !m.ModalShown() {
		t.Fatal("VPN while Cisco active should show modal")
	}
	if m.SegCursor() != int(RunOff) {
		t.Errorf("cursor should be restored off the blocked VPN segment, got %d", m.SegCursor())
	}
}

// --- mouse: click selects a nav item, click again opens it ---

func TestMouse_ClickNavSelectsThenOpens(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 120, Height: 36})
	if m.Section() != navMode {
		t.Fatalf("precondition: navMode selected, got %d", m.Section())
	}
	// First click on Соединения selects it without opening.
	m, _ = clickZone(t, m, zoneNav(navConns))
	if m.Section() != navConns {
		t.Fatalf("clicking a nav item should select it, got %d", m.Section())
	}
	if m.ShowingConns() {
		t.Error("first click should only select, not open")
	}
	// Second click on the now-selected item opens it.
	m, _ = clickZone(t, m, zoneNav(navConns))
	if !m.ShowingConns() {
		t.Error("clicking the already-selected nav item should open it")
	}
}

// --- logs close keys & scroll/follow (review findings #13, #14, #26) ---

func TestLogs_CloseKeysDoNotQuit(t *testing.T) {
	for _, k := range []tea.KeyMsg{escKey, rune_("q"), rune_("l")} {
		m := New(&fakeBackend{}, nil).WithLoadedProfile()
		m, _ = step(m, rune_("l"))
		if !m.ShowingLogs() {
			t.Fatalf("precondition: logs open before %q", k.String())
		}
		m, cmd := step(m, k)
		if m.ShowingLogs() {
			t.Errorf("%q should close the logs view", k.String())
		}
		if cmd != nil {
			if _, ok := cmd().(tea.QuitMsg); ok {
				t.Errorf("%q must not quit while logs are open", k.String())
			}
		}
	}
}

func bigLog() string {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("logline\n")
	}
	return sb.String()
}

func TestLogs_AutoFollowVsPinned(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.showLogs = true
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 20})
	m, _ = step(m, logsMsg{content: bigLog()})
	if !m.vp.AtBottom() {
		t.Fatal("fresh logs should auto-follow to the bottom")
	}
	// Scroll up: should pin (stop following).
	m, _ = step(m, rune_("k"))
	if m.vp.AtBottom() {
		t.Fatal("scrolling up should leave the bottom")
	}
	m, _ = step(m, logsMsg{content: bigLog() + "newline\n"})
	if m.vp.AtBottom() {
		t.Error("new logs must NOT force-scroll a user who scrolled up")
	}
	// Jump back to the bottom: follow resumes.
	m, _ = step(m, rune_("G"))
	if !m.vp.AtBottom() {
		t.Error("G should jump to the bottom")
	}
}

func TestRelayout_ViewportResizesAndKeepsPosition(t *testing.T) {
	m := New(&fakeBackend{}, nil).WithLoadedProfile()
	m.showLogs = true
	m, _ = step(m, logsMsg{content: bigLog()})
	m, _ = step(m, tea.WindowSizeMsg{Width: 80, Height: 20})
	if !m.vpReady {
		t.Fatal("viewport should be ready after first size")
	}
	m, _ = step(m, rune_("g")) // go to top
	if !m.vp.AtTop() {
		t.Fatal("g should go to top")
	}
	m, _ = step(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	if m.vp.Width != 120 {
		t.Errorf("viewport width = %d, want 120 after resize", m.vp.Width)
	}
	if m.vp.AtBottom() {
		t.Error("resize must not force a top-pinned viewport to the bottom")
	}
}

// --- glyph capability contract (review finding #25) ---

func isASCII(s string) bool { return utf8.RuneCountInString(s) == len(s) }

func TestPickGlyphs_AsciiPortable(t *testing.T) {
	g := PickGlyphs(false)
	fields := map[string]string{
		"Warn": g.Warn, "DotOn": g.DotOn, "DotOff": g.DotOff, "Cursor": g.Cursor,
		"Sep": g.Sep, "Dash": g.Dash, "Ellipsis": g.Ellipsis, "ScrollAt": g.ScrollAt,
		"ArrowsLR": g.ArrowsLR, "ArrowsUD": g.ArrowsUD, "Enter": g.Enter, "Prompt": g.Prompt,
	}
	for name, v := range fields {
		if !isASCII(v) {
			t.Errorf("ascii fallback glyph %s = %q is not pure ASCII", name, v)
		}
	}
	if g.unicode() {
		t.Error("ascii glyph set should report unicode()==false")
	}
	u := PickGlyphs(true)
	if !u.unicode() || u.DotOn != "●" {
		t.Error("unicode glyph set misconfigured")
	}
}
