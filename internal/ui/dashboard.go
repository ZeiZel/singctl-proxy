package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Pane grid (spy-control style). paneStatus..paneSettings form the focusable
// ring promoted to a full-screen renderer on expand; paneActionLog is an
// always-visible footer outside the ring.
const (
	paneStatus = iota
	paneConns
	paneApps
	paneSingbox
	paneConsole
	paneSettings
	paneCount // number of focusable panes (ring length)

	paneActionLog = paneCount // footer pane (not in the ring)
)

// paneLabels are the Russian titles shown in each pane header.
var paneLabels = []string{
	paneStatus:    "СТАТУС / РЕЖИМ",
	paneConns:     "СОЕДИНЕНИЯ",
	paneApps:      "ПРИЛОЖЕНИЯ",
	paneSingbox:   "ЛОГИ SING-BOX",
	paneConsole:   "КОНСОЛЬ ПРИЛОЖЕНИЙ",
	paneSettings:  "НАСТРОЙКИ",
	paneActionLog: "ДЕЙСТВИЯ",
}

// zonePane is the bubblezone namespace for clickable panes (distinct from
// sec-/mode-/proc-/set-).
func zonePane(i int) string { return "pane-" + strconv.Itoa(i) }

// paneSection maps a focusable pane to the разделы section whose overlay renders
// its expanded form (and whose load cmd is fired on expand). Returns -1 for panes
// with no dedicated overlay (paneStatus/paneConsole render via expandedPaneBody).
func paneSection(pane int) int {
	switch pane {
	case paneConns:
		return secConns
	case paneApps:
		return secApps
	case paneSingbox:
		return secLogs
	case paneSettings:
		return secSettings
	default: // paneStatus, paneConsole
		return -1
	}
}

// sectionPane maps a разделы section to the pane that renders it in the grid (the
// inverse of paneSection). secKeys has no grid pane, so it falls back to paneStatus.
func sectionPane(sec int) int {
	switch sec {
	case secConns:
		return paneConns
	case secLogs:
		return paneSingbox
	case secApps:
		return paneApps
	case secSettings:
		return paneSettings
	default: // secKeys
		return paneStatus
	}
}

// syncPaneToFocus mirrors the разделы focus ring onto the grid's focused pane so
// the highlighted pane tracks Tab/arrow navigation. focus<0 (mode selector) leaves
// the pane on Статус.
func (m *Model) syncPaneToFocus() {
	if m.focus < 0 {
		m.pane = paneStatus
		return
	}
	m.pane = sectionPane(m.focus)
}

// expandSection opens a разделы section full-screen and marks the grid expanded so
// a later Esc collapses back to the grid. Shared by the keyboard ring (Enter on a
// focused chip).
func (m Model) expandSection(sec int) (tea.Model, tea.Cmd) {
	m.expanded = true
	return m.openSection(sec)
}

// expandPane promotes the focused pane to its full-screen renderer. Panes backed
// by an overlay (conns/logs/apps/settings) delegate to openSection so the matching
// show* flag is set and its load cmd fires; paneConsole primes its viewport;
// paneStatus just flips expanded. Shared by the mouse and keyboard paths so the
// expand behaviour is identical.
func (m Model) expandPane() (tea.Model, tea.Cmd) {
	m.expanded = true
	if sec := paneSection(m.pane); sec >= 0 {
		return m.openSection(sec)
	}
	if m.pane == paneConsole {
		w := max(m.width, 1)
		bodyH := max(m.height-6, 1)
		if !m.consoleVPReady {
			m.consoleVP = viewport.New(w, bodyH)
			m.consoleVPReady = true
		} else {
			m.consoleVP.Width, m.consoleVP.Height = w, bodyH
		}
		m.refreshConsoleViewport()
	}
	return m, nil
}

// collapsePane returns from an expanded pane to the grid, clearing every overlay
// show* flag so the next render falls back to the grid body. Shared by the mouse
// and keyboard paths.
func (m Model) collapsePane() (tea.Model, tea.Cmd) {
	m.expanded = false
	m.showConns = false
	m.showLogs = false
	m.showProc = false
	m.showSettings = false
	return m, nil
}

// gridView is the dashboard entry point: a responsive pane grid with an
// always-visible action-log footer, or — when a pane is expanded — its
// full-screen renderer. The header stays in both cases.
func (m Model) gridView() string {
	// Expanded panes that have a dedicated full-screen view are rendered by the
	// existing overlay renderers (show* flags are set on expand in update.go), so
	// screenView never reaches here for them. paneStatus/paneConsole have no
	// overlay and render their expanded form below.
	// The action-button bar is always-visible chrome (every primary action is a
	// clickable pill AND a key), so it rides directly under the header in both the
	// grid and the expanded views. Joining it into the header lets frame() budget
	// its height automatically.
	header := lipgloss.JoinVertical(lipgloss.Left, m.dashHeader(), m.renderButtonBar(max(m.width, 1)))
	footer := m.gridFooter()

	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyH < 1 {
		bodyH = 1
	}

	var body string
	if m.expanded {
		body = m.expandedPaneBody(bodyH)
	} else {
		body = m.gridBody(bodyH)
	}
	return m.frame(header, body, footer)
}

// dashHeader is the dashboard top bar (title + mode/attached/activity badges).
func (m Model) dashHeader() string {
	s := m.styles
	right := s.modeBadge(m.mode)
	if m.attached {
		arrow := "↔"
		if !m.caps.Unicode {
			arrow = "<->"
		}
		right = s.colored(s.th.Accent, fmt.Sprintf("%s attached PID %d", arrow, m.attachedPID)) + "  " + right
	}
	if m.animPos > 0.02 {
		right = m.prog.ViewAs(clampF(m.animPos, 0, 1)) + "  " + right
	}
	return m.topBar("singctl", right)
}

// gridFooter renders the always-visible action-log pane plus the help line. Its
// height is budgeted by breakpoint so frame() never pushes it off-screen.
func (m Model) gridFooter() string {
	s := m.styles
	help := s.clampBlock(m.help.View(m.keys), max(m.width, 1))

	rows := m.footerLogRows()
	if rows <= 0 {
		// Too short to afford a footer pane: just the help line.
		return help
	}
	lay := layoutFor(m.width, m.height)
	bordered := lay == layoutMedium || lay == layoutWide
	w := m.width
	cw := max(w-2, 1)
	if bordered {
		cw = max(w-4, 1)
	} else {
		cw = max(w-1, 1)
	}
	title := s.PanelTitle.Render(s.clampLine(paneLabels[paneActionLog], cw))
	logBody := clampHeight(m.actions.render(s, cw, rows), rows)
	var box string
	if bordered {
		inner := lipgloss.JoinVertical(lipgloss.Left, title, logBody)
		box = s.Panel.Width(max(w-2, 1)).Render(inner)
	} else {
		inner := lipgloss.JoinVertical(lipgloss.Left, title, s.clampBlock(logBody, cw))
		box = inner
	}
	box = m.zm.Mark(zonePane(paneActionLog), box)
	return lipgloss.JoinVertical(lipgloss.Left, box, help)
}

// footerLogRows is how many action-log lines the footer shows, by breakpoint.
// Returns 0 when the terminal is too short to spare a footer pane.
func (m Model) footerLogRows() int {
	if m.height < 18 {
		return 0
	}
	switch layoutFor(m.width, m.height) {
	case layoutWide:
		return 6
	case layoutMedium:
		return 4
	case layoutNarrow:
		return 2
	default:
		return 0
	}
}

// gridBody lays out the collapsed pane grid for the current breakpoint.
func (m Model) gridBody(bodyH int) string {
	lay := layoutFor(m.width, m.height)
	switch lay {
	case layoutWide:
		return m.gridWide(bodyH)
	default:
		return m.gridStacked(bodyH, lay)
	}
}

// gridWide is the 2-column × 3-row pane grid (spy-control geometry).
func (m Model) gridWide(bodyH int) string {
	colW := m.width / 2
	rowH := bodyH / 3
	if rowH < 3 {
		rowH = 3
	}
	var rows []string
	for r := 0; r < 3; r++ {
		li, ri := r*2, r*2+1
		left := m.renderPane(li, m.pane == li, colW, rowH)
		right := m.renderPane(ri, m.pane == ri, m.width-colW, rowH)
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, left, right))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// gridStacked is the single-column layout (medium = bordered, narrow =
// borderless). Each focusable pane gets an equal share of the body height.
func (m Model) gridStacked(bodyH int, lay layout) string {
	rowH := bodyH / paneCount
	if rowH < 2 {
		rowH = 2
	}
	var rows []string
	for i := 0; i < paneCount; i++ {
		rows = append(rows, m.renderPane(i, m.pane == i, m.width, rowH))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// renderPane draws one bordered (or, on narrow, borderless) pane: a title row +
// a compact summary body, clamped to the given box. The whole box is marked as a
// clickable bubblezone.
func (m Model) renderPane(i int, focused bool, totalW, totalH int) string {
	s := m.styles
	lay := layoutFor(m.width, m.height)
	// A bordered box needs at least 4 rows to close cleanly (top border + title +
	// ≥1 content + bottom border). When the per-pane height is smaller — e.g. the
	// medium single-column layout squeezing six panes — fall back to borderless so
	// boxes never render half-open.
	bordered := lay != layoutNarrow && totalH >= 4

	if totalH < 2 {
		totalH = 2
	}
	var box string
	if bordered {
		boxW := max(totalW-2, 6)
		boxH := max(totalH-2, 2)
		contentW := max(totalW-4, 4)
		title := s.PanelTitle.Render(s.clampLine(paneLabels[i], contentW))
		content := m.paneBody(i, contentW, boxH-1)
		inner := lipgloss.JoinVertical(lipgloss.Left, title, content)
		st := s.Panel
		if focused {
			st = s.PanelActive
		}
		box = st.Width(boxW).Height(boxH).MaxWidth(totalW).MaxHeight(totalH).Render(inner)
	} else {
		contentW := max(totalW-1, 1)
		marker := "  "
		titleText := paneLabels[i]
		if focused {
			marker = s.colored(s.th.Accent, s.gl.SelBar+" ")
		}
		title := marker + s.PanelTitle.Render(s.clampLine(titleText, contentW))
		content := m.paneBody(i, contentW, max(totalH-1, 1))
		content = s.clampBlock(clampHeight(content, max(totalH-1, 1)), contentW)
		box = lipgloss.JoinVertical(lipgloss.Left, title, content)
	}
	return m.zm.Mark(zonePane(i), box)
}

// paneBody renders the compact summary for a collapsed pane, reusing the
// existing body helpers wherever possible.
func (m Model) paneBody(i, w, h int) string {
	s := m.styles
	switch i {
	case paneStatus:
		return clampHeight(m.statusGridBody(w), h)
	case paneConns:
		return clampHeight(m.dashConnsBody(w), h)
	case paneApps:
		return clampHeight(m.appsSummaryBody(w), h)
	case paneSingbox:
		body := tailLines(m.logs, h)
		if strings.TrimSpace(body) == "" {
			body = s.Subtle.Render("(логи появятся после запуска режима)")
		}
		return clampHeight(s.clampBlock(body, w), h)
	case paneConsole:
		return clampHeight(m.console.render(s, w, h, 0), h)
	case paneSettings:
		return clampHeight(m.settingsSummaryBody(w), h)
	}
	return ""
}

// statusGridBody is the СТАТУС/РЕЖИМ pane summary: the status rows, with the
// "VPN blocked" note pinned right under them (before the longer status notice)
// so it survives the pane's height clamp.
func (m Model) statusGridBody(w int) string {
	s := m.styles
	var parts []string
	if m.cisco {
		parts = append(parts, s.colored(s.th.Warn, s.clampLine(s.gl.Warn+" VPN заблокирован: Cisco активен", w)))
	}
	parts = append(parts, m.statusBody(w))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// appsSummaryBody is the ПРИЛОЖЕНИЯ pane summary: the macOS hint (if any) and
// the currently-routed PIDs.
func (m Model) appsSummaryBody(w int) string {
	s := m.styles
	var rows []string
	if m.isDarwin {
		rows = append(rows, s.Subtle.Render(s.clampLine("Cursor: добавьте --proxy-server=socks5://127.0.0.1:1080 (для Chromium-приложений)", w)))
	}
	if len(m.routedPIDs) == 0 {
		rows = append(rows, s.Subtle.Render("(пока никого не проксируем)"))
	} else {
		parts := make([]string, len(m.routedPIDs))
		for i, pid := range m.routedPIDs {
			parts[i] = strconv.Itoa(pid)
		}
		rows = append(rows, s.clampLine("PID: "+strings.Join(parts, ", "), w))
	}
	rows = append(rows, s.Subtle.Render(s.clampLine("Enter — запустить приложение в прокси", w)))
	return strings.Join(rows, "\n")
}

// settingsSummaryBody is the НАСТРОЙКИ pane summary: the key runtime values.
func (m Model) settingsSummaryBody(w int) string {
	s := m.styles
	const keyW = 12
	st := m.settings
	rows := []string{
		s.kv("SOCKS", strconv.Itoa(st.SocksPort), keyW),
		s.kv("Clash API", onOff(st.ClashEnabled), keyW),
		s.kv("Clash адрес", orDash(st.ClashAddr), keyW),
		s.kv("urltest", orDash(st.URLTestInterval), keyW),
	}
	for i := range rows {
		rows[i] = s.clampLine(rows[i], w)
	}
	return strings.Join(rows, "\n")
}

// expandedPaneBody renders the focused pane full-screen. Panes with a dedicated
// overlay (conns/logs/apps/settings) are dispatched by screenView via their
// show* flags; only paneStatus and paneConsole are rendered here.
func (m Model) expandedPaneBody(bodyH int) string {
	s := m.styles
	w := max(m.width, 1)
	switch m.pane {
	case paneConsole:
		title := s.PanelTitle.Render(paneLabels[paneConsole])
		chips := m.consoleFilterChips(w)
		// The chips row eats one line of body height.
		innerH := max(bodyH-2, 1)
		var body string
		if m.consoleVPReady {
			// Re-size + re-fill a local copy so the console always renders at the
			// current terminal width (robust to a resize after expand, or to expand
			// firing before the first WindowSizeMsg). Operating on the copy keeps
			// View pure — m.consoleVP is untouched, so scroll offset is preserved.
			vp := m.consoleVP
			vp.Width, vp.Height = w, innerH
			content := m.console.render(s, max(w, 1), 0, m.consoleFilter)
			vp.SetContent(content)
			// SetContent clamps the scroll offset only to the line count, not to the
			// viewport height, so a stale offset (e.g. from a resize that grew the
			// pane) can hide content. Re-clamp to the valid range here.
			if maxOff := max(0, lipgloss.Height(content)-innerH); vp.YOffset > maxOff {
				vp.SetYOffset(maxOff)
			}
			body = vp.View()
		} else {
			body = m.console.render(s, w, innerH, m.consoleFilter)
		}
		return lipgloss.JoinVertical(lipgloss.Left, title, chips, clampHeight(body, innerH))
	default: // paneStatus (and any pane without an overlay)
		title := s.PanelTitle.Render(paneLabels[paneStatus])
		body := m.statusGridBody(w)
		return lipgloss.JoinVertical(lipgloss.Left, title, clampHeight(body, max(bodyH-1, 1)))
	}
}

// clampHeight truncates a multi-line block to at most h lines, so a pane body
// never overflows its box (the height analogue of clampLine).
func clampHeight(body string, h int) string {
	if h < 1 {
		return ""
	}
	lines := strings.Split(body, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}
