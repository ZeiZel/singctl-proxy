package ui

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	// The alt-screen delivers the size right after start; until then render
	// nothing rather than a mis-sized 0×0 flash.
	if m.width == 0 {
		return ""
	}
	// bubblezone Scan strips the (zero-width) zone markers and records bounds for
	// mouse hit-testing; it wraps every screen so clicks work everywhere.
	return m.zm.Scan(m.screenView())
}

func (m Model) screenView() string {
	// The Cisco warning is the most important thing on screen and has its own
	// fits-any-width renderer, so it wins even over the too-small fallback.
	if m.modal != "" {
		return m.modalView()
	}
	if layoutFor(m.width, m.height) == layoutTooSmall {
		return m.tooSmallView()
	}
	if m.screen == ScreenIntro {
		return m.introView()
	}
	if m.showProc {
		return m.procView()
	}
	if m.showConns {
		return m.connsView()
	}
	if m.showLogs {
		return m.logsView()
	}
	if m.showSettings {
		return m.settingsView()
	}
	if m.showConsole {
		return m.consoleView()
	}
	switch m.screen {
	case ScreenLink:
		return m.linkView()
	default:
		return m.dashboardView()
	}
}

// frame stacks header / body / footer vertically. The body is truncated to the
// available rows (so the footer is never pushed off-screen) and then padded to
// fill them (so the footer pins to the bottom). PlaceVertical only ever pads, so
// the explicit truncation is what guards the height — the analogue of the width
// invariant.
func (m Model) frame(header, body, footer string) string {
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(footer)
	if bodyH < 0 {
		bodyH = 0
	}
	if lipgloss.Height(body) > bodyH {
		if bodyH == 0 {
			body = ""
		} else {
			body = strings.Join(strings.Split(body, "\n")[:bodyH], "\n")
		}
	}
	body = lipgloss.PlaceVertical(bodyH, lipgloss.Top, body)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// topBar renders the title row (title left, optional badge right) plus a rule.
// The app name renders as a filled pill; any " — section" suffix is dim subtitle.
func (m Model) topBar(title, right string) string {
	s := m.styles
	w := max(m.width, 1)
	left := m.titlePill(title)
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	bar := left
	if right != "" && lw+1+rw <= w {
		bar = left + strings.Repeat(" ", w-lw-rw) + right
	} else {
		bar = s.clampLine(left, w)
	}
	return lipgloss.JoinVertical(lipgloss.Left, bar, s.rule(w))
}

// titlePill renders the app name as a filled pill; a " — section" suffix becomes
// dim subtitle text next to it.
func (m Model) titlePill(title string) string {
	s := m.styles
	sep := " " + s.gl.Dash + " "
	if app, rest, ok := strings.Cut(title, sep); ok {
		return s.Title.Render(app) + " " + s.Muted.Render(rest)
	}
	return s.Title.Render(title)
}

// --- dashboard zones ---

func zoneMode(i int) string    { return "mode-" + strconv.Itoa(i) }
func zoneProc(i int) string    { return "proc-" + strconv.Itoa(i) }
func zoneSetting(i int) string { return "set-" + strconv.Itoa(i) }

// dashConnsBody is the body of the always-on dashboard СОЕДИНЕНИЯ panel: the
// per-server latency list (when present) above a live-connection list capped to
// the rows that fit the current terminal height (so the panel fills the screen
// instead of showing a fixed handful).
func (m Model) dashConnsBody(cw int) string {
	w := max(cw, 1)
	var parts []string
	if lat := m.latencyBody(w); lat != "" {
		parts = append(parts, lat, m.styles.rule(w))
	}
	parts = append(parts, m.connsBody(w, m.dashConnLimit()))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// dashConnLimit is how many connection rows the dashboard panel shows: as many
// as the terminal height allows (the status+mode panels take the top), clamped
// to a sane band. The full, scrollable list lives in the expanded view (c).
func (m Model) dashConnLimit() int {
	n := m.height - 18 // header + status + mode panels + spacers + footer
	if n < dashConnRows {
		n = dashConnRows
	}
	if n > 40 {
		n = 40
	}
	return n
}

// statusBody renders the status panel's inner rows (mode, Cisco, iface, notice),
// wrapped/clamped to contentW.
func (m Model) statusBody(contentW int) string {
	s := m.styles
	const keyW = 8
	valW := max(contentW-keyW, 1)
	phys := m.phys
	if phys == "" {
		phys = s.gl.Dash
	}
	rows := []string{
		s.kv("режим", s.modeBadge(m.mode), keyW),
		s.kv("Cisco", s.ciscoBadge(m.cisco), keyW),
		s.kv("iface", s.colored(s.th.Accent, s.clampLine(phys, valW)), keyW),
	}
	if sum := m.latencySummary(); sum != "" {
		rows = append(rows, s.kv("сервер", s.clampLine(sum, valW), keyW))
	}
	if notice := m.noticeLine(); notice != "" {
		rows = append(rows, s.rule(contentW), wrap(notice, contentW))
	}
	if m.errText != "" {
		rows = append(rows, s.Err.Render(wrap(m.errText, contentW)))
	}
	return strings.Join(rows, "\n")
}

// noticeLine is the status/activity line: a spinner + message while busy, the
// last status otherwise.
func (m Model) noticeLine() string {
	if m.busy {
		return m.spin.View() + " " + orEmpty(m.status, "переключение…")
	}
	return m.status
}

// --- link input ---

func (m Model) linkView() string {
	s := m.styles
	lay := layoutFor(m.width, m.height)
	subW := max(m.width-2, 1)

	title := "singctl " + s.gl.Dash + " вставьте VLESS-ссылку"
	if len(m.currentLinks) > 0 {
		title = "singctl " + s.gl.Dash + " ключи подключения"
	}
	header := m.topBar(title, "")

	maskChar := "•"
	if !m.caps.Unicode {
		maskChar = "*"
	}

	var rows []string
	// The "add key" input, full width, focused-styled when keyFocus==0.
	rows = append(rows, m.fieldTitle("Добавить ключ", m.keyFocus == 0))
	if lay == layoutNarrow {
		rows = append(rows, s.clampLine(m.input.View(), max(m.width, 1)))
	} else {
		// Full-width box (the round-1 hardcoded 60-cell cap is gone).
		st := s.Panel
		if m.keyFocus == 0 {
			st = s.PanelActive
		}
		rows = append(rows, st.Width(max(m.width-4, 1)).Render(m.input.View()))
	}

	// The navigable list of loaded keys.
	if len(m.currentLinks) > 0 {
		rows = append(rows, s.rule(subW), m.fieldTitle("Ключи (скрыты)", m.keyFocus == 1))
		cur := clampIdx(m.keyCursor, len(m.currentLinks))
		for i, link := range m.currentLinks {
			focused := m.keyFocus == 1 && i == cur
			marker := "  "
			label := "Ключ " + strconv.Itoa(i+1) + ": " + maskLink(link, maskChar)
			if focused {
				marker = s.colored(s.th.Accent, s.gl.SelBar+" ")
				label = s.Accent.Render(label)
			}
			rows = append(rows, m.zm.Mark(zoneKey(i), s.clampLine(marker+label, subW)))
			if focused && m.keyReveal {
				rows = append(rows, s.Subtle.Render(s.clampBlock(wrap(link, subW), subW)))
			}
			if focused {
				rows = append(rows, s.Subtle.Render(s.clampLine("   Enter — показать · n — имя · e — ред. · d — удалить", subW)))
			}
		}
		rows = append(rows, "", s.Muted.Render(wrap("Несколько ключей образуют группу с авто-выбором самого быстрого.", subW)))
	} else {
		rows = append(rows, "", s.Muted.Render(wrap("Ничего не запустится, пока вы сами не выберете режим.", subW)))
	}
	if m.errText != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.errText, subW)))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	var pairs [][2]string
	if m.keyFocus == 1 {
		pairs = [][2]string{{s.gl.ArrowsUD, "ключ"}, {"Enter", "показать"}, {"n", "имя"}, {"e", "ред."}, {"d", "удалить"}, {"Tab", "ввод"}}
	} else {
		enterLabel := "загрузить"
		if len(m.currentLinks) > 0 {
			enterLabel = "добавить"
		}
		pairs = [][2]string{{"Enter", enterLabel}}
		if len(m.currentLinks) > 0 {
			pairs = append(pairs, [2]string{"Tab", "список"})
		}
	}
	if m.loaded {
		pairs = append(pairs, [2]string{"esc", "назад"})
	}
	pairs = append(pairs, [2]string{"ctrl+c", "выход"})
	footer := s.clampLine(s.footerHints(pairs), max(m.width, 1))
	return m.frame(header, body, footer)
}

func zoneKey(i int) string { return "key-" + strconv.Itoa(i) }

// --- logs ---

func (m Model) logsHeaderView() string {
	s := m.styles
	w := max(m.width, 1)
	left := s.Title.Render("логи sing-box")
	info := fmt.Sprintf("%d строк %s %s", countLines(m.logs), s.gl.Sep, s.gl.ScrollAt)
	right := s.Subtle.Render(info)
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	bar := left
	if lw+1+rw <= w {
		bar = left + strings.Repeat(" ", w-lw-rw) + right
	} else {
		bar = s.clampLine(left, w)
	}
	return lipgloss.JoinVertical(lipgloss.Left, bar, s.rule(w))
}

func (m Model) logsFooterView() string {
	s := m.styles
	return s.clampLine(s.footerHints([][2]string{
		{s.gl.ArrowsUD + "/jk", "прокрутка"},
		{"g/G", "верх/низ"},
		{"l/esc", "назад"},
		{"ctrl+c", "выход"},
	}), max(m.width, 1))
}

func (m Model) logsChromeHeight() int {
	return lipgloss.Height(m.logsHeaderView()) + lipgloss.Height(m.logsFooterView())
}

func (m Model) logsView() string {
	header := m.logsHeaderView()
	footer := m.logsFooterView()
	var body string
	if m.vpReady {
		body = m.vp.View()
	} else {
		// No size yet (unit tests): plain tail of the buffer, level-styled.
		body = m.styleLogText(tailLines(m.logs, maxLogLines(m.height)))
		if strings.TrimSpace(body) == "" {
			body = "(пусто)"
		}
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// --- connections ---

func (m Model) connsHeaderView() string {
	s := m.styles
	w := max(m.width, 1)
	left := s.Title.Render("соединения")
	right := s.Subtle.Render(fmt.Sprintf("%d %s %s", len(m.conns), s.gl.Sep, m.latencySummary()))
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	bar := left
	if right != "" && lw+1+rw <= w {
		bar = left + strings.Repeat(" ", w-lw-rw) + right
	} else {
		bar = s.clampLine(left, w)
	}
	return lipgloss.JoinVertical(lipgloss.Left, bar, s.rule(w))
}

// latencySummary renders the selected server and its latency for the header /
// status panel (e.g. "proxy-0 42ms"). Empty when no latency data yet.
func (m Model) latencySummary() string {
	if len(m.latency) == 0 {
		return ""
	}
	s := m.styles
	for _, r := range m.latency {
		if r.Selected || r.Tag == m.latencySel {
			return s.colored(s.th.Accent, r.Tag) + " " + s.latencyColor(r.Delay)
		}
	}
	// No explicit selection (single server): show the first.
	return s.colored(s.th.Accent, m.latency[0].Tag) + " " + s.latencyColor(m.latency[0].Delay)
}

func delayText(ms int) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%dms", ms)
}

// latencyColor grades a latency: green (fast) → amber → red (slow), subtle when
// unknown — so the eye reads link health instantly.
func (s Styles) latencyColor(ms int) string {
	col := s.th.Subtle
	switch {
	case ms <= 0:
		col = s.th.Subtle
	case ms < 100:
		col = s.th.Ok
	case ms < 250:
		col = s.th.Warn
	default:
		col = s.th.Error
	}
	return s.colored(col, delayText(ms))
}

func (m Model) connsFooterView() string {
	s := m.styles
	return s.clampLine(s.footerHints([][2]string{
		{s.gl.ArrowsUD + "/jk", "прокрутка"},
		{"g/G", "верх/низ"},
		{"c/esc", "назад"},
		{"ctrl+c", "выход"},
	}), max(m.width, 1))
}

func (m Model) connsChromeHeight() int {
	return lipgloss.Height(m.connsHeaderView()) + lipgloss.Height(m.connsFooterView())
}

func (m Model) connsView() string {
	header := m.connsHeaderView()
	footer := m.connsFooterView()
	var body string
	if m.connVPReady {
		// Render from a copy seeded with current content so the view never goes
		// stale (the real viewport keeps the scroll offset, which the copy reuses).
		vp := m.connVP
		vp.SetContent(m.connContent(max(vp.Width, 1)))
		body = vp.View()
	} else {
		body = m.connContent(max(m.width, 1)) // no size yet (unit tests): plain render
	}
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

// latencyBody renders the failover group's per-server latency list (selected
// server marked). Returns "" when there is no latency data. Shared by the conns
// overlay and the dashboard panel.
func (m Model) latencyBody(w int) string {
	if len(m.latency) == 0 {
		return ""
	}
	s := m.styles
	rows := []string{s.Subtle.Render("серверы:")}
	for _, r := range m.latency {
		marker := s.Subtle.Render(s.gl.DotOff + " ")
		tag := s.Muted.Render(r.Tag)
		if r.Selected || r.Tag == m.latencySel {
			marker = s.colored(s.th.Accent, s.gl.DotOn+" ")
			tag = s.colored(s.th.Accent, r.Tag)
		}
		rows = append(rows, s.clampLine(marker+tag+"  "+s.latencyColor(r.Delay), w))
	}
	return strings.Join(rows, "\n")
}

// connsBody renders the live connection list (process → destination [network]
// via chain), each line clamped to w. limit>0 caps the number of rows shown
// (with a "…ещё N" tail); limit<=0 shows all. Shared by the conns overlay and
// the dashboard panel.
func (m Model) connsBody(w, limit int) string {
	s := m.styles
	if len(m.conns) == 0 {
		return s.Subtle.Render("(нет активных соединений)")
	}
	shown := m.conns
	hidden := 0
	if limit > 0 && len(shown) > limit {
		hidden = len(shown) - limit
		shown = shown[:limit]
	}
	rows := make([]string, 0, len(shown)+1)
	for _, c := range shown {
		proc := c.Process
		if proc == "" {
			proc = s.gl.Dash
		}
		// Colour-coded fields: process (accent) · src (subtle) → dest (text) ·
		// [network] tag (purple) · chain/outbound (green).
		line := s.colored(s.th.Accent, proc) + "  " +
			s.Subtle.Render(c.Source) + " " + s.colored(s.th.Muted, s.gl.ArrowR) + " " +
			s.colored(s.th.Text, c.Dest) + "  " +
			s.colored(s.th.Vpn, "["+c.Network+"]")
		if c.Chain != "" {
			line += "  " + s.colored(s.th.Ok, c.Chain)
		}
		rows = append(rows, s.clampLine(line, w))
	}
	if hidden > 0 {
		rows = append(rows, s.Subtle.Render(fmt.Sprintf("…ещё %d", hidden)))
	}
	return strings.Join(rows, "\n")
}

// --- Приложения: launch app in proxy + route running processes ---

// inputBox renders a text input, bordered on wide/medium, clamped on narrow,
// highlighting when focused.
func (m Model) inputBox(ti textinput.Model, focused bool) string {
	s := m.styles
	if layoutFor(m.width, m.height) == layoutNarrow {
		return s.clampLine(ti.View(), max(m.width, 1))
	}
	bw := min(m.width-2, 64)
	box := s.Panel
	if focused {
		box = s.PanelActive
	}
	return box.Width(max(bw-2, 1)).Render(ti.View())
}

func (m Model) procView() string {
	s := m.styles
	header := m.topBar("singctl "+s.gl.Dash+" приложения", "")
	subW := max(m.width-2, 1)

	rows := []string{
		m.fieldTitle("Запустить приложение в прокси", m.appFocus == 0),
		m.inputBox(m.launchInput, m.appFocus == 0),
		s.Muted.Render(wrap("Введите имя или путь приложения и нажмите Enter — оно запустится с трафиком через прокси (напр. zen).", subW)),
	}
	if m.procBusy {
		rows = append(rows, s.colored(s.th.Accent, m.spin.View()+" проксирование приложения…"))
	}
	if m.leakyEditorProxied() {
		warn := s.colored(s.th.Warn, s.gl.Warn+" Cursor/VS Code: агентский трафик extension-host течёт мимо прокси — нажмите v, чтобы включить VPN (полное покрытие)")
		rows = append(rows, m.zm.Mark(zoneEnableVPN, s.clampLine(warn, subW)))
	} else if m.isDarwin {
		rows = append(rows, s.Subtle.Render(wrap(
			"Cursor/VS Code: Chromium-слой проксируется автоматически, но агентский трафик extension-host (Node) может течь мимо — для полного покрытия включите VPN (v). Подробнее: docs/macos.md", subW)))
	}
	rows = append(rows,
		s.rule(subW),
		m.fieldTitle("Проксировать запущенный процесс", m.appFocus == 1),
		m.inputBox(m.procInput, m.appFocus == 1),
	)

	// Process picker list (filtered).
	fp := m.filteredProcs()
	if m.procErr != "" {
		rows = append(rows, s.Err.Render(wrap(s.gl.Warn+" "+m.procErr, subW)))
	} else if len(fp) > 0 {
		shown := fp
		const maxRows = 10
		if len(shown) > maxRows {
			shown = shown[:maxRows]
		}
		cur := clampIdx(m.procCursor, len(fp))
		for i, p := range shown {
			marker := "  "
			label := fmt.Sprintf("%-6d %s", p.PID, p.Label())
			if i == cur {
				marker = s.colored(s.th.Accent, s.gl.SelBar+" ")
				label = s.Accent.Render(fmt.Sprintf("%-6d %s", p.PID, p.Label()))
			}
			if p.Ports != "" {
				label += "  " + s.Subtle.Render(p.Ports)
			}
			rows = append(rows, m.zm.Mark(zoneProc(i), s.clampLine(marker+label, subW)))
		}
		if len(fp) > maxRows {
			rows = append(rows, s.Subtle.Render(fmt.Sprintf("…ещё %d", len(fp)-maxRows)))
		}
	} else {
		rows = append(rows, s.Subtle.Render("(процессы с сетевой активностью не найдены)"))
	}

	// Currently proxied apps + a traffic summary, each row with unroute/kill
	// actions when focused.
	rows = append(rows, s.rule(subW), m.fieldTitle("Проксируются сейчас", m.appFocus == 2))
	rows = append(rows, "  "+s.Muted.Render("приложений: ")+s.colored(s.th.Accent, strconv.Itoa(len(m.proxied)))+
		s.Muted.Render("  ·  соединений: ")+s.colored(s.th.Ok, strconv.Itoa(len(m.conns))))
	if len(m.proxied) == 0 {
		rows = append(rows, s.Subtle.Render("  (пока никого)"))
	} else {
		cur := clampIdx(m.proxiedCur, len(m.proxied))
		for i, a := range m.proxied {
			marker := "  "
			dot := s.colored(s.th.Ok, s.gl.DotOn)
			name := s.colored(s.th.Accent, orDash(a.Name))
			meta := s.Subtle.Render(fmt.Sprintf(" PID %d", a.PID))
			if n := m.connCountFor(a.Name); n > 0 {
				meta += s.colored(s.th.Ok, fmt.Sprintf(" · %d соед.", n))
			}
			focused := m.appFocus == 2 && i == cur
			if focused {
				marker = s.colored(s.th.Accent, s.gl.SelBar+" ")
			}
			line := marker + dot + " " + name + meta
			if focused {
				line += "   " + s.Subtle.Render("u — отключить прокси · k — завершить")
			}
			rows = append(rows, m.zm.Mark(zoneProxied(i), s.clampLine(line, subW)))
		}
	}

	if m.errText != "" {
		rows = append(rows, s.Err.Render(wrap(s.gl.Warn+" "+m.errText, subW)))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	footer := s.clampLine(s.footerHints([][2]string{
		{"Tab", "поле"},
		{s.gl.ArrowsUD, "выбор"},
		{"Enter", "запуск/прокси"},
		{"u/k", "откл./стоп"},
		{"^R", "перезапуск"},
		{"esc", "назад"},
	}), max(m.width, 1))
	return m.frame(header, body, footer)
}

// fieldTitle renders a section/field label, marked with the focus bar + accent
// when its zone is focused (so the Apps view's three zones read clearly).
func (m Model) fieldTitle(label string, focused bool) string {
	s := m.styles
	if focused {
		return s.colored(s.th.Accent, s.gl.SelBar+" ") + s.PanelTitle.Render(label)
	}
	return "  " + s.PanelTitle.Render(label)
}

func zoneProxied(i int) string { return "proxied-" + strconv.Itoa(i) }

// zoneEnableVPN is the clickable "switch to VPN" warning shown when a leaky
// editor (Cursor/VS Code) is proxied but VPN is off (see leakyEditorProxied).
const zoneEnableVPN = "enable-vpn"

// connCountFor counts live connections whose source process matches an app name
// (case-insensitive substring), so each proxied app can show its traffic.
func (m Model) connCountFor(name string) int {
	if strings.TrimSpace(name) == "" {
		return 0
	}
	ln := strings.ToLower(name)
	n := 0
	for _, c := range m.conns {
		if strings.Contains(strings.ToLower(c.Process), ln) {
			n++
		}
	}
	return n
}

// --- settings ---

func (m Model) settingsView() string {
	s := m.styles
	header := m.topBar("singctl "+s.gl.Dash+" настройки", "")
	w := max(m.width, 1)
	const labelW = 22

	fields := m.settingsFieldsFor()
	rows := make([]string, 0, len(fields)+2)
	for i, f := range fields {
		focused := i == m.setForm.focus
		marker := "  "
		if focused {
			marker = s.colored(s.th.Accent, s.gl.SelBar+" ")
		}
		var line string
		switch f.kind {
		case sfAction:
			label := "[ " + f.label + " ]"
			if focused {
				label = s.colored(s.th.Accent, label)
			}
			line = marker + label
		default:
			val := m.setForm.draft.display(i)
			if focused && m.setForm.editing {
				val = m.setForm.edit.View()
			} else if focused {
				val = s.colored(s.th.Accent, val)
			}
			line = marker + s.kv(f.label, val, labelW)
		}
		rows = append(rows, m.zm.Mark(zoneSetting(i), s.clampLine(line, w)))
	}
	if m.errText != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.errText, max(w-2, 1))))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	footer := s.clampLine(s.footerHints([][2]string{
		{s.gl.ArrowsUD, "поле"},
		{"Enter", "изменить/применить"},
		{"esc", "назад"},
	}), w)
	return m.frame(header, body, footer)
}

// --- warning modal (the narrow-terminal overflow fix) ---

func (m Model) modalView() string {
	s := m.styles
	w, h := max(m.width, 1), max(m.height, 1)
	tight := h < 16 // short terminal: shed vertical padding/spacers so it fits

	maxW := max(w-2, 1)
	// Three kinds: info (Cisco) — warn-bordered, dismiss on any key; confirm —
	// accent-bordered Да/Нет; input — accent-bordered text field (rename/edit).
	titleText := s.gl.Warn + " Cisco активен"
	hintText := "(любая клавиша)"
	borderColor := m.theme.Warn
	switch m.modalKind {
	case modalConfirm:
		titleText = "Подтвердите действие"
		hintText = "Да — y / Enter   ·   Нет — n / Esc"
		borderColor = m.theme.Accent
	case modalInput:
		titleText = "Изменить ключ"
		hintText = "Enter — сохранить   ·   Esc — отмена"
		borderColor = m.theme.Accent
	}

	maxBox := min(w-4, 56)
	vpad := 1
	if tight {
		vpad = 0
	}
	box := s.r.NewStyle().Border(s.gl.Border).BorderForeground(borderColor).Padding(vpad, 2)
	inner := maxBox - box.GetHorizontalFrameSize() // subtract border+padding before wrapping

	// For an input popup, render the text field sized to the card (no extra
	// border — the card already frames it; the field shows the prompt + cursor).
	field := ""
	if m.modalKind == modalInput {
		pi := m.prompt
		pi.Width = max(inner-4, 8)
		field = pi.View()
	}

	var card string
	if inner < 8 {
		// Ultra-narrow terminal: drop the border, just centred wrapped text.
		parts := []string{s.colored(borderColor, wrap(titleText, maxW)), "", wrap(m.modal, maxW)}
		if field != "" {
			parts = append(parts, s.clampBlock(m.prompt.View(), maxW))
		}
		parts = append(parts, "", s.Subtle.Render(wrap(hintText, maxW)))
		card = lipgloss.JoinVertical(lipgloss.Left, parts...)
	} else {
		title := s.colored(borderColor, wrap(titleText, inner))
		body := wrap(m.modal, inner)
		hint := s.Subtle.Render(hintText)
		var content string
		if field != "" {
			content = lipgloss.JoinVertical(lipgloss.Left, title, "", body, field, "", hint)
		} else {
			content = lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hint)
			if tight {
				content = lipgloss.JoinVertical(lipgloss.Left, title, body, hint)
			}
		}
		card = box.Width(inner).Render(content)
	}
	placed := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
	// Final clamp: a long warning on a short terminal can be taller than h, and
	// Place never truncates — keep it within the screen so nothing spills.
	return s.r.NewStyle().MaxWidth(w).MaxHeight(h).Render(placed)
}

// --- entry animation ---

// introView renders the entry animation: a reveal-animated banner, a bouncing
// ball, and a component-loading progress bar + checklist, centred. Any key skips.
func (m Model) introView() string {
	s := m.styles
	w, h := max(m.width, 1), max(m.height, 1)

	// Banner: the title in a bordered accent box, the letters spaced out and
	// revealed left-to-right by the spring on the first run.
	const logo = "SINGCTL"
	shown := logo
	if m.introFull {
		n := int(clampF(m.introPos, 0, 1) * float64(len(logo)))
		if n < 1 {
			n = 1
		}
		if n > len(logo) {
			n = len(logo)
		}
		shown = logo[:n]
	}
	spaced := strings.Join(strings.Split(shown, ""), " ")
	bw := clampWidth(w-6, 14, 40)
	banner := s.r.NewStyle().
		Border(s.gl.Border).BorderForeground(s.th.Accent).
		Foreground(s.th.Accent).Bold(true).
		Padding(1, 3).Align(lipgloss.Center).Width(bw).Render(spaced)

	// Progress bar + component checklist.
	bar := m.prog
	bar.Width = clampWidth(w-8, 12, 36)
	frac := float64(m.introStage) / float64(len(introStages))
	progress := bar.ViewAs(clampF(frac, 0, 1))
	cells := make([]string, len(introStages))
	for i, c := range introStages {
		switch {
		case i < m.introStage:
			cells[i] = s.colored(s.th.Accent, s.gl.DotOn+" "+c)
		case i == m.introStage:
			cells[i] = m.spin.View() + " " + c
		default:
			cells[i] = s.Subtle.Render(s.gl.DotOff + " " + c)
		}
	}
	checklist := s.clampLine(strings.Join(cells, "   "), max(w-2, 1))

	sub := "загрузка компонентов…"
	if m.introFull {
		sub = "добро пожаловать в singctl"
	}

	content := lipgloss.JoinVertical(lipgloss.Center,
		banner, "",
		s.Muted.Render(sub), "",
		progress, "",
		checklist, "",
		s.Subtle.Render("(любая клавиша — пропустить)"),
	)
	placed := lipgloss.Place(w, max(h-3, 1), lipgloss.Center, lipgloss.Center, content)
	// A ball bounces along the bottom three rows — clear, lively motion.
	full := lipgloss.JoinVertical(lipgloss.Left, placed, m.bouncingBall(w))
	// Final clamp so a tall layout on a short terminal can never spill.
	return s.r.NewStyle().MaxWidth(w).MaxHeight(h).Render(full)
}

// bouncingBall renders a 3-row strip with a ball that travels left↔right and
// hops, animated by the intro frame counter (deterministic; no rand/time).
func (m Model) bouncingBall(w int) string {
	s := m.styles
	if w < 4 {
		return ""
	}
	ball := s.gl.DotOn
	if !m.caps.Unicode {
		ball = "o"
	}
	span := w - 1
	travel := 2 * span // one full left→right→left cycle
	p := (m.introFrame * 2) % travel
	x := p
	if x > span {
		x = travel - p // reflect for the return leg
	}
	hop := math.Abs(math.Sin(float64(m.introFrame) * 0.45)) // 0..1
	const rows = 3
	row := (rows - 1) - int(hop*float64(rows-1)+0.5) // higher hop → higher row
	lines := make([]string, rows)
	for r := 0; r < rows; r++ {
		if r == row {
			lines[r] = strings.Repeat(" ", clampWidth(x, 0, span)) + s.colored(s.th.Accent, ball)
		} else {
			lines[r] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// --- too-small fallback ---

func (m Model) tooSmallView() string {
	s := m.styles
	w, h := max(m.width, 1), max(m.height, 1)
	needRows := minRowsWide
	if m.width < narrowMax {
		needRows = minRowsNarr
	}
	msg := fmt.Sprintf("Окно слишком маленькое.\nНужно ≥ %d×%d, сейчас %d×%d.\nРастяните терминал.",
		minCols, needRows, m.width, m.height)
	return lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, s.Subtle.Render(wrap(msg, max(w-2, 1))))
}

// --- footer hints (link / logs screens) ---

// footerHints renders "key desc · key desc" pairs in accent + subtle styles.
func (s Styles) footerHints(pairs [][2]string) string {
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = s.colored(s.th.Accent, p[0]) + " " + s.Subtle.Render(p[1])
	}
	return strings.Join(parts, "  "+s.gl.Sep+"  ")
}

// --- small helpers ---

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func orEmpty(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func countLines(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func maxLogLines(h int) int {
	n := h - 6
	if n < 5 {
		n = 5
	}
	if n > 200 {
		n = 200
	}
	return n
}

func tailLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
