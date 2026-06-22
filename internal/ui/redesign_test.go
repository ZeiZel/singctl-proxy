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

// --- pane-grid model: Tab cycles m.pane; Enter/'o'/click expand ---

func TestPaneRing_TabCyclesPanesAndStatusIsDefault(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// Focus defaults to the mode selector (-1) → the СТАТУС pane is highlighted.
	if m.Focus() != -1 {
		t.Fatalf("focus should default to the selector (-1), got %d", m.Focus())
	}
	if m.pane != paneStatus {
		t.Fatalf("the focused pane should default to paneStatus, got %d", m.pane)
	}
	// Tab walks the focus ring and the highlighted pane follows it. The first Tab
	// focuses Соединения → paneConns.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.Focus() != secConns {
		t.Fatalf("first Tab should focus Соединения (%d), got %d", secConns, m.Focus())
	}
	if m.pane != paneConns {
		t.Fatalf("Tab should move the highlighted pane to paneConns (%d), got %d", paneConns, m.pane)
	}
	// Tab all the way around returns to the selector.
	seen := map[int]bool{m.pane: true}
	for i := 0; i < sectionPaneRingLen(); i++ {
		m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
		seen[m.pane] = true
	}
	if m.Focus() != -1 || m.pane != paneStatus {
		t.Errorf("Tab should wrap back to the selector/paneStatus, got focus=%d pane=%d", m.Focus(), m.pane)
	}
}

// sectionPaneRingLen is the number of разделы chips (the Tab ring length minus the
// selector slot); a small local helper to keep the wrap loop readable.
func sectionPaneRingLen() int { return len(dashSectionLabels) }

func TestPaneRing_EnterExpandsFocusedSection(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// Tab to Соединения, then Enter expands it full-screen.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.pane != paneConns {
		t.Fatalf("precondition: paneConns focused, got %d", m.pane)
	}
	m, _ = step(m, enterKey)
	if !m.expanded {
		t.Error("Enter on a focused pane should set expanded")
	}
	if !m.ShowingConns() {
		t.Error("Enter on the focused Соединения pane should open the connections view")
	}
}

func TestPaneRing_OExpandsConsolePane(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	m.pane = paneConsole
	m, _ = step(m, rune_("o"))
	if !m.expanded {
		t.Error("'o' should expand the focused pane")
	}
	if m.pane != paneConsole {
		t.Errorf("'o' should keep paneConsole focused, got %d", m.pane)
	}
	// Esc collapses back to the grid.
	m, _ = step(m, escKey)
	if m.expanded {
		t.Error("Esc should collapse an expanded pane back to the grid")
	}
}

func TestPaneRing_SelectorRightStillDrivesSegCursor(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 110, Height: 32})
	// ←/→ on the focused СТАТУС pane (focus == -1) drives the OFF/PROXY/VPN cursor.
	m, _ = step(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.SegCursor() != 1 {
		t.Fatalf("right on the СТАТУС pane should advance the selector to PROXY, got %d", m.SegCursor())
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

// --- mouse: click focuses a pane, click again expands it ---

func TestMouse_ClickPaneFocusesThenExpands(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps()).WithLoadedProfile()
	m, _ = step(m, tea.WindowSizeMsg{Width: 120, Height: 36})
	if m.pane != paneStatus {
		t.Fatalf("precondition: paneStatus focused, got %d", m.pane)
	}
	// First click on the (unfocused) console pane focuses it without expanding.
	m, _ = clickZone(t, m, zonePane(paneConsole))
	if m.pane != paneConsole {
		t.Fatalf("clicking the console pane should focus it, got %d", m.pane)
	}
	if m.expanded {
		t.Error("first click should only focus, not expand")
	}
	// Second click on the now-focused pane expands it.
	m, _ = clickZone(t, m, zonePane(paneConsole))
	if !m.expanded {
		t.Error("clicking the already-focused pane should expand it")
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
