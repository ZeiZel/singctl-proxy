package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Sidebar dashboard. The resting view is a left-nav list of sections + a content
// panel (master/detail); every item is reachable from the keyboard (↑/↓ select,
// Enter/→ open, 1..7 jump) and by mouse. navMode is the "Режим" home: its content
// is the status block + the OFF/PROXY/VPN selector, driven by ←→ + Enter (and the
// global p/v/s shortcuts).
const (
	navMode = iota // Режим (status + OFF/PROXY/VPN selector)
	navConns       // Соединения
	navApps        // Приложения
	navLogs        // Логи sing-box
	navConsole     // Консоль приложений
	navSettings    // Настройки
	navKeys        // Ключи
	navCount
)

// navLabels are the Russian sidebar titles, one per nav item.
var navLabels = []string{
	navMode:     "Режим",
	navConns:    "Соединения",
	navApps:     "Приложения",
	navLogs:     "Логи",
	navConsole:  "Консоль",
	navSettings: "Настройки",
	navKeys:     "Ключи",
}

const actionLogTitle = "ДЕЙСТВИЯ"

// sidebarWidth is the fixed width of the left-nav column on wide/medium layouts.
const sidebarWidth = 22

// zoneNav is the bubblezone namespace for a clickable sidebar item.
func zoneNav(i int) string { return "nav-" + strconv.Itoa(i) }

// navToSection maps a nav item to the разделы section index its overlay uses
// (navMode/navConsole have no openSection overlay). Returns -1 otherwise.
func navToSection(n int) int {
	switch n {
	case navConns:
		return secConns
	case navApps:
		return secApps
	case navLogs:
		return secLogs
	case navSettings:
		return secSettings
	case navKeys:
		return secKeys
	default:
		return -1
	}
}

// stepNav moves the sidebar cursor around the [0, navCount) ring (wrap).
func stepNav(n, d int) int {
	return ((n+d)%navCount + navCount) % navCount
}

// openNav opens the highlighted sidebar item full-screen. navMode is the resting
// home (Enter on it applies the selected mode, handled by the caller); navConsole
// primes + shows the console; the rest delegate to openSection so the matching
// show* flag is set and any load cmd fires.
func (m Model) openNav(n int) (tea.Model, tea.Cmd) {
	m.section = n
	if n == navConsole {
		m.ensureConsoleViewport()
		m.consoleFilter = 0
		m.showConsole = true
		m.refreshConsoleViewport()
		return m, nil
	}
	if sec := navToSection(n); sec >= 0 {
		return m.openSection(sec)
	}
	return m, nil // navMode: no overlay
}

// ensureConsoleViewport (re)sizes the console viewport for a full-screen render.
func (m *Model) ensureConsoleViewport() {
	w := max(m.width, 1)
	bodyH := max(m.height-6, 1)
	if !m.consoleVPReady {
		m.consoleVP = viewport.New(w, bodyH)
		m.consoleVPReady = true
	} else {
		m.consoleVP.Width, m.consoleVP.Height = w, bodyH
	}
}

// closeOverlays clears every section overlay flag, returning focus to the resting
// sidebar. Shared by the per-overlay Esc handlers.
func (m Model) closeOverlays() Model {
	m.showConns = false
	m.showLogs = false
	m.showProc = false
	m.showSettings = false
	m.showConsole = false
	return m
}

// --- resting dashboard render (sidebar + content) -------------------------

// dashboardView is the resting dashboard: header, a left-nav sidebar beside a
// content panel for the selected section, and the action-log + help footer.
func (m Model) dashboardView() string {
	header := m.dashHeader()
	footer := m.dashFooter()
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyH < 1 {
		bodyH = 1
	}
	body := m.dashBody(bodyH)
	return m.frame(header, body, footer)
}

// dashHeader is the dashboard top bar (title + a daemon/activity loader + the
// mode badge). When attached to a background daemon, an always-spinning loader
// reads "демон активен PID N" so the running daemon is obvious at the top.
func (m Model) dashHeader() string {
	s := m.styles
	segs := []string{}
	if m.attached {
		segs = append(segs, s.colored(s.th.Ok, m.spin.View()+" демон активен")+s.Subtle.Render(fmt.Sprintf(" PID %d", m.attachedPID)))
	} else if m.mode != RunOff {
		// Local run with a live core: a spinning loader signals it's active.
		segs = append(segs, s.colored(modeColor(s.th, m.mode), m.spin.View()+" активен"))
	}
	if m.animPos > 0.02 {
		segs = append(segs, m.prog.ViewAs(clampF(m.animPos, 0, 1)))
	}
	segs = append(segs, s.modeBadge(m.mode))
	return m.topBar("singctl", strings.Join(segs, "  "))
}

// dashBody lays out the sidebar + content for the current breakpoint. Narrow
// terminals stack a horizontal nav chip row above the content.
func (m Model) dashBody(bodyH int) string {
	if layoutFor(m.width, m.height) == layoutNarrow {
		nav := m.navChips(max(m.width, 1))
		content := m.dashContent(max(m.width, 1), max(bodyH-lipgloss.Height(nav), 1))
		return lipgloss.JoinVertical(lipgloss.Left, nav, content)
	}
	sideW := sidebarWidth
	if sideW > m.width/2 {
		sideW = m.width / 2
	}
	contentW := max(m.width-sideW, 1)
	side := m.sidebarColumn(sideW, bodyH)
	content := m.contentPanel(contentW, bodyH)
	return lipgloss.JoinHorizontal(lipgloss.Top, side, content)
}

// contentPanel wraps the section content in a bordered panel beside the sidebar,
// so the two columns read as distinct framed cards.
func (m Model) contentPanel(w, h int) string {
	s := m.styles
	inner := max(w-4, 1)
	body := m.dashContent(inner, max(h-2, 1))
	return s.Panel.Width(max(w-2, 1)).Height(max(h-2, 1)).Render(body)
}

// sidebarColumn renders the nav list inside a bordered panel; the selected item
// is a full-width accent bar so the active section is unmistakable.
func (m Model) sidebarColumn(w, h int) string {
	s := m.styles
	inner := max(w-4, 1) // border (1 each side) + padding (1 each side)
	rows := make([]string, 0, len(navLabels)+2)
	rows = append(rows, s.PanelTitle.Render(s.clampLine("РАЗДЕЛЫ", inner)), s.rule(inner))
	for i, l := range navLabels {
		rows = append(rows, m.navRow(i, l, inner))
	}
	body := clampHeight(strings.Join(rows, "\n"), max(h-2, 1))
	return s.PanelActive.Width(max(w-2, 1)).Height(max(h-2, 1)).Render(body)
}

// navRow renders one sidebar entry: "N  Название". The selected row is a
// full-width filled accent bar (the active tab); inactive rows are dim.
func (m Model) navRow(i int, label string, cw int) string {
	s := m.styles
	num := strconv.Itoa(i + 1)
	if i == m.section {
		line := s.clampLine(s.gl.SelBar+" "+num+"  "+label, max(cw-2, 1))
		return m.zm.Mark(zoneNav(i), s.TabActive.Width(cw).Render(line))
	}
	line := s.clampLine("  "+num+"  "+label, max(cw-2, 1))
	return m.zm.Mark(zoneNav(i), s.Tab.Width(cw).Render(line))
}

// navChips renders the nav list as a single horizontal tab bar (narrow layout).
func (m Model) navChips(w int) string {
	labels := make([]string, len(navLabels))
	for i, l := range navLabels {
		labels[i] = strconv.Itoa(i+1) + " " + l
	}
	return m.styles.clampBlock(m.styles.tabBar(labels, m.section, func(i int, seg string) string {
		return m.zm.Mark(zoneNav(i), seg)
	}), w)
}

// dashContent renders the right-hand panel: the status block + OFF/PROXY/VPN
// selector (the selector is "live" only when the Режим item is focused), then a
// preview of the currently-highlighted section.
func (m Model) dashContent(w, h int) string {
	s := m.styles
	parts := []string{
		s.PanelTitle.Render(s.clampLine("СТАТУС", w)),
		m.statusBody(w),
		m.modeSelector(w),
	}
	// A wide progress bar makes a long operation (mode switch, daemon start)
	// visible beyond the small header gauge. Driven by the same harmonica spring.
	if m.animPos > 0.02 {
		bar := m.prog
		bar.Width = clampWidth(w, 16, 40)
		label := m.status
		if label == "" {
			label = "выполняется…"
		}
		parts = append(parts, bar.ViewAs(clampF(m.animPos, 0, 1)), s.Subtle.Render(s.clampLine(m.spin.View()+" "+label, w)))
	}
	if m.cisco {
		parts = append(parts, s.colored(s.th.Warn, s.clampLine(s.gl.Warn+" VPN заблокирован: Cisco активен", w)))
	}
	parts = append(parts, s.rule(w), s.PanelTitle.Render(s.clampLine(navLabels[m.section], w)))

	used := 0
	for _, p := range parts {
		used += lipgloss.Height(p)
	}
	previewH := max(h-used, 1)
	parts = append(parts, m.sectionPreview(m.section, w, previewH))
	// clampBlock enforces the width invariant: the segmented selector box can be
	// wider than a narrow content column, so truncate every line to w.
	return s.clampBlock(clampHeight(lipgloss.JoinVertical(lipgloss.Left, parts...), h), w)
}

// modeSelector renders the OFF/PROXY/VPN segmented control. Its keyboard cursor
// shows only when the Режим item is focused (so it reads as inactive otherwise);
// p/v/s always work regardless. Segments are bubblezone-marked for clicks. The
// control stacks vertically on a narrow content column so its box still fits.
func (m Model) modeSelector(w int) string {
	s := m.styles
	opts := []string{"ВЫКЛ", "ПРОКСИ", "VPN"}
	disabled := map[int]bool{}
	if m.cisco {
		disabled[2] = true
	}
	cursor := -1
	if m.section == navMode {
		cursor = m.segCursor
	}
	vertical := layoutFor(m.width, m.height) == layoutNarrow || w < 46
	return s.segmented(opts, int(m.mode), cursor, disabled, vertical,
		func(i int, seg string) string { return m.zm.Mark(zoneMode(i), seg) })
}

// sectionPreview renders the compact body shown in the content panel for the
// highlighted nav item (the detail half of the master/detail dashboard).
func (m Model) sectionPreview(n, w, h int) string {
	s := m.styles
	switch n {
	case navMode:
		hint := "Выберите режим: p · v · s, или " + s.gl.ArrowsLR + " + " + s.gl.Enter + "."
		return clampHeight(s.Subtle.Render(wrap(hint, w)), h)
	case navConns:
		return clampHeight(m.dashConnsBody(w), h)
	case navApps:
		return clampHeight(m.appsSummaryBody(w), h)
	case navLogs:
		body := tailLines(m.logs, h)
		if strings.TrimSpace(body) == "" {
			body = s.Subtle.Render("(логи появятся после запуска режима)")
		}
		return clampHeight(s.clampBlock(body, w), h)
	case navConsole:
		return clampHeight(m.console.render(s, w, h, 0), h)
	case navSettings:
		return clampHeight(m.settingsSummaryBody(w), h)
	case navKeys:
		return clampHeight(m.keysSummaryBody(w), h)
	}
	return ""
}

// appsSummaryBody is the ПРИЛОЖЕНИЯ preview: the macOS hint (if any) and the
// currently-routed PIDs, plus how to open.
func (m Model) appsSummaryBody(w int) string {
	s := m.styles
	var rows []string
	if m.isDarwin {
		rows = append(rows, s.Subtle.Render(s.clampLine("Cursor: Chromium авто; extension-host (Node) — полное покрытие через VPN (v)", w)))
	}
	rows = append(rows, s.Muted.Render("приложений: ")+s.colored(s.th.Accent, strconv.Itoa(len(m.proxied)))+
		s.Muted.Render("  ·  соединений: ")+s.colored(s.th.Ok, strconv.Itoa(len(m.conns))))
	if len(m.proxied) == 0 {
		rows = append(rows, s.Subtle.Render("(пока никого не проксируем)"))
	} else {
		for _, a := range m.proxied {
			line := s.colored(s.th.Ok, s.gl.DotOn) + " " + s.colored(s.th.Accent, orDash(a.Name)) + s.Subtle.Render(fmt.Sprintf(" PID %d", a.PID))
			if n := m.connCountFor(a.Name); n > 0 {
				line += s.colored(s.th.Ok, fmt.Sprintf(" · %d соед.", n))
			}
			rows = append(rows, s.clampLine(line, w))
		}
	}
	rows = append(rows, s.Subtle.Render(s.clampLine("Enter — запустить приложение в прокси", w)))
	return strings.Join(rows, "\n")
}

// settingsSummaryBody is the НАСТРОЙКИ preview: the key runtime values.
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

// keysSummaryBody is the КЛЮЧИ preview: how many keys are loaded (masked).
func (m Model) keysSummaryBody(w int) string {
	s := m.styles
	if len(m.currentLinks) == 0 {
		return s.Subtle.Render(s.clampLine("(ключи не загружены) — Enter, чтобы добавить", w))
	}
	line := fmt.Sprintf("загружено ключей: %d", len(m.currentLinks))
	return lipgloss.JoinVertical(lipgloss.Left,
		s.clampLine(line, w),
		s.Subtle.Render(s.clampLine("Enter — открыть / добавить ключ", w)),
	)
}

// dashFooter renders the always-visible action-log pane plus the help line. Its
// height is budgeted by breakpoint so frame() never pushes it off-screen.
func (m Model) dashFooter() string {
	s := m.styles
	help := s.clampBlock(m.help.View(m.keys), max(m.width, 1))

	rows := m.footerLogRows()
	if rows <= 0 {
		return help
	}
	lay := layoutFor(m.width, m.height)
	bordered := lay == layoutMedium || lay == layoutWide
	w := m.width
	cw := max(w-2, 1)
	if !bordered {
		cw = max(w-1, 1)
	}
	title := s.PanelTitle.Render(s.clampLine(actionLogTitle, cw))
	logBody := clampHeight(m.actions.render(s, cw, rows), rows)
	var box string
	if bordered {
		inner := lipgloss.JoinVertical(lipgloss.Left, title, logBody)
		box = s.Panel.Width(max(w-2, 1)).Render(inner)
	} else {
		box = lipgloss.JoinVertical(lipgloss.Left, title, s.clampBlock(logBody, cw))
	}
	box = m.zm.Mark("action-log", box)
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

// --- console overlay (full-screen Консоль приложений) ---------------------

// consoleView renders the per-app console full-screen: a header, the clickable
// per-app filter chips, the scrollable output viewport, and a footer.
func (m Model) consoleView() string {
	s := m.styles
	w := max(m.width, 1)
	header := m.topBar("singctl "+s.gl.Dash+" консоль приложений", "")
	chips := m.consoleFilterChips(w)
	footer := s.clampLine(s.footerHints([][2]string{
		{s.gl.ArrowsUD + "/jk", "прокрутка"},
		{"g/G", "верх/низ"},
		{"esc", "назад"},
		{"ctrl+c", "выход"},
	}), w)

	bodyH := max(m.height-lipgloss.Height(header)-lipgloss.Height(chips)-lipgloss.Height(footer), 1)
	var body string
	if m.consoleVPReady {
		vp := m.consoleVP
		vp.Width, vp.Height = w, bodyH
		content := m.console.render(s, w, 0, m.consoleFilter)
		if strings.TrimSpace(content) == "" {
			content = s.Subtle.Render("(вывод приложений появится после запуска)")
		}
		vp.SetContent(content)
		if maxOff := max(0, lipgloss.Height(content)-bodyH); vp.YOffset > maxOff {
			vp.SetYOffset(maxOff)
		}
		body = vp.View()
	} else {
		body = clampHeight(m.console.render(s, w, bodyH, m.consoleFilter), bodyH)
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, chips, body, footer)
}

// clampWidth bounds v to [lo, hi].
func clampWidth(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampHeight truncates a multi-line block to at most h lines, so a body never
// overflows its box (the height analogue of clampLine).
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
