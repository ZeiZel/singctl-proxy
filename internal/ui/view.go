package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

func (m Model) View() string {
	// The alt-screen delivers the size right after start; until then render
	// nothing rather than a mis-sized 0×0 flash.
	if m.width == 0 {
		return ""
	}
	// The Cisco warning is the most important thing on screen and has its own
	// fits-any-width renderer, so it wins even over the too-small fallback.
	if m.modal != "" {
		return m.modalView()
	}
	if layoutFor(m.width, m.height) == layoutTooSmall {
		return m.tooSmallView()
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
func (m Model) topBar(title, right string) string {
	s := m.styles
	w := max(m.width, 1)
	left := s.Title.Render(title)
	lw, rw := lipgloss.Width(left), lipgloss.Width(right)
	bar := left
	if right != "" && lw+1+rw <= w {
		bar = left + strings.Repeat(" ", w-lw-rw) + right
	} else {
		bar = s.clampLine(left, w)
	}
	return lipgloss.JoinVertical(lipgloss.Left, bar, s.rule(w))
}

// --- dashboard ---

func (m Model) dashboardView() string {
	s := m.styles
	lay := layoutFor(m.width, m.height)
	compact := m.height < 20 // tight height: drop borders/spacers so nothing is clipped

	header := m.topBar("singctl", s.modeBadge(m.mode))
	footer := s.clampBlock(m.help.View(m.keys), max(m.width, 1))

	opts := []string{"ВЫКЛ", "ПРОКСИ", "VPN"}
	disabled := map[int]bool{}
	if m.cisco {
		disabled[2] = true
	}
	vertical := lay == layoutNarrow
	sel := s.segmented(opts, int(m.mode), m.segCursor, disabled, vertical)

	var body string
	switch {
	case lay == layoutWide && !compact:
		pw := min((m.width-2)/2, sideMax)
		cw := pw - 4
		status := s.panel("СТАТУС", m.statusBody(cw), pw, false, true)
		mode := s.panel("РЕЖИМ", m.modeBody(sel, cw, true), pw, false, true)
		top := lipgloss.JoinHorizontal(lipgloss.Top, status, "  ", mode)
		// A wide СОЕДИНЕНИЯ panel spans the same total width as the two top panels.
		fpw := min(m.width, panelMax+sideMax)
		conns := s.panel("СОЕДИНЕНИЯ", m.dashConnsBody(fpw-4), fpw, false, true)
		body = lipgloss.JoinVertical(lipgloss.Left, top, "", m.dashSectionsRow(), "", conns)
	default:
		bordered := !compact && lay != layoutNarrow
		var pw, cw int
		if bordered {
			pw = min(m.width, panelMax)
			cw = pw - 4
		} else {
			pw = m.width
			cw = max(m.width-1, 1)
		}
		status := s.panel("СТАТУС", m.statusBody(cw), pw, false, bordered)
		mode := s.panel("РЕЖИМ", m.modeBody(sel, cw, !compact), pw, false, bordered)
		if compact {
			// Tight height: collapse connections to one line + a compact разделы
			// hint so the footer survives.
			body = lipgloss.JoinVertical(lipgloss.Left, status, mode,
				m.dashSectionsLine(),
				s.clampLine(m.dashConnsSummary(), max(m.width, 1)))
		} else {
			conns := s.panel("СОЕДИНЕНИЯ", m.dashConnsBody(cw), pw, false, bordered)
			body = lipgloss.JoinVertical(lipgloss.Left, status, "", mode, "", m.dashSectionsRow(), "", conns)
		}
	}
	return m.frame(header, body, footer)
}

// dashSectionsRow renders the разделы chips (expandable full-screen sections).
// The focused chip (Tab moves focus) is highlighted; Enter opens it. This makes
// every feature discoverable from the dashboard.
func (m Model) dashSectionsRow() string {
	s := m.styles
	hint := s.Subtle.Render("Tab — выбрать раздел " + s.gl.Sep + " Enter — открыть")
	return lipgloss.JoinVertical(lipgloss.Left,
		m.dashSectionsLine(),
		s.clampLine(hint, max(m.width, 1)),
	)
}

// dashSectionsLine is the one-line разделы chips row (no hint) for compact height.
func (m Model) dashSectionsLine() string {
	s := m.styles
	chips := make([]string, len(dashSectionLabels))
	for i, l := range dashSectionLabels {
		if i == m.focus {
			chips[i] = s.SegSelected.Render(" " + l + " ")
		} else {
			chips[i] = s.SegNormal.Render(" " + l + " ")
		}
	}
	return s.clampLine(s.Subtle.Render("разделы: ")+strings.Join(chips, " "), max(m.width, 1))
}

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

// dashConnsSummary is the one-line connections fallback for very short terminals.
func (m Model) dashConnsSummary() string {
	s := m.styles
	line := fmt.Sprintf("%d соединений", len(m.conns))
	if sum := m.latencySummary(); sum != "" {
		line += " " + s.gl.Sep + " " + sum
	}
	return s.Subtle.Render(line)
}

// modeBody assembles the РЕЖИМ panel: the selector, an optional navigation hint,
// and — when Cisco is active — a wrapped "VPN blocked" note. That note is the
// colour-independent affordance for the greyed-out VPN segment (the grey alone
// is invisible under NO_COLOR / ascii, e.g. under sudo).
func (m Model) modeBody(sel string, cw int, withHint bool) string {
	s := m.styles
	parts := []string{sel}
	if withHint {
		parts = append(parts, "", s.Subtle.Render("Tab/"+s.gl.ArrowsLR+"  "+s.gl.Sep+"  Enter"))
	}
	if m.cisco {
		note := s.colored(s.th.Warn, s.gl.Warn+" VPN заблокирован: Cisco активен")
		parts = append(parts, "", wrap(note, max(cw, 1)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
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
		s.kv("iface", s.clampLine(phys, valW), keyW),
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
		title = "singctl " + s.gl.Dash + " строки подключения"
	}
	header := m.topBar(title, "")

	maskChar := "•"
	if !m.caps.Unicode {
		maskChar = "*"
	}

	var rows []string
	// Show the already-loaded keys, masked like a password.
	if len(m.currentLinks) > 0 {
		rows = append(rows, s.Subtle.Render("текущие ключи (скрыты):"))
		for i, link := range m.currentLinks {
			rows = append(rows, s.clampLine(strconv.Itoa(i+1)+". "+maskLink(link, maskChar), subW))
		}
		rows = append(rows, "", s.Subtle.Render("добавить второй ключ:"))
	}

	var box string
	if lay == layoutNarrow {
		// No border to save cells; clamp defensively so a long link can never
		// overflow (a focused input already windows itself to its Width).
		box = s.clampLine(m.input.View(), max(m.width, 1))
	} else {
		bw := min(m.width-2, 60)
		box = s.Panel.Width(max(bw-2, 1)).Render(m.input.View())
	}
	rows = append(rows, box)
	if m.errText != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.errText, subW)))
	}
	if len(m.currentLinks) > 0 {
		rows = append(rows, "", s.Muted.Render(wrap("Несколько ключей образуют группу с авто-выбором самого быстрого.", subW)))
	} else {
		rows = append(rows, "", s.Muted.Render(wrap("Ничего не запустится, пока вы сами не выберете режим.", subW)))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	enterLabel := "загрузить"
	if len(m.currentLinks) > 0 {
		enterLabel = "добавить"
	}
	pairs := [][2]string{{"Enter", enterLabel}}
	if m.loaded {
		pairs = append(pairs, [2]string{"esc", "назад"})
	}
	pairs = append(pairs, [2]string{"ctrl+c", "выход"})
	footer := s.clampLine(s.footerHints(pairs), max(m.width, 1))
	return m.frame(header, body, footer)
}

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
		// No size yet (unit tests): plain tail of the buffer.
		body = tailLines(m.logs, maxLogLines(m.height))
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
	for _, r := range m.latency {
		if r.Selected || r.Tag == m.latencySel {
			return r.Tag + " " + delayText(r.Delay)
		}
	}
	// No explicit selection (single server): show the first.
	return m.latency[0].Tag + " " + delayText(m.latency[0].Delay)
}

func delayText(ms int) string {
	if ms <= 0 {
		return "—"
	}
	return fmt.Sprintf("%dms", ms)
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
		marker := "  "
		if r.Selected || r.Tag == m.latencySel {
			marker = s.colored(s.th.Accent, s.gl.DotOn+" ")
		}
		rows = append(rows, s.clampLine(marker+r.Tag+"  "+delayText(r.Delay), w))
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
		line := fmt.Sprintf("%s  %s %s %s  [%s]", proc, c.Source, s.gl.ArrowR, c.Dest, c.Network)
		if c.Chain != "" {
			line += "  " + s.Subtle.Render(c.Chain)
		}
		rows = append(rows, s.clampLine(line, w))
	}
	if hidden > 0 {
		rows = append(rows, s.Subtle.Render(fmt.Sprintf("…ещё %d", hidden)))
	}
	return strings.Join(rows, "\n")
}

// --- per-process routing prompt ---

func (m Model) procView() string {
	s := m.styles
	lay := layoutFor(m.width, m.height)
	header := m.topBar("singctl "+s.gl.Dash+" проксировать процесс", "")

	var box string
	if lay == layoutNarrow {
		box = s.clampLine(m.procInput.View(), max(m.width, 1))
	} else {
		bw := min(m.width-2, 64)
		box = s.Panel.Width(max(bw-2, 1)).Render(m.procInput.View())
	}
	subW := max(m.width-2, 1)
	hint := "Фильтруйте по имени/PID и выберите процесс (↑/↓, Enter), " +
		"введите PID, или команду для запуска через прокси."
	rows := []string{box, "", s.Muted.Render(wrap(hint, subW))}

	// Process picker list (filtered).
	fp := m.filteredProcs()
	if m.procErr != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.procErr, subW)))
	} else if len(fp) > 0 {
		rows = append(rows, s.rule(subW))
		shown := fp
		const maxRows = 12
		if len(shown) > maxRows {
			shown = shown[:maxRows]
		}
		cur := clampIdx(m.procCursor, len(fp))
		for i, p := range shown {
			marker := "  "
			label := fmt.Sprintf("%-6d %s", p.PID, p.Name)
			if p.Ports != "" {
				label += "  " + s.Subtle.Render(p.Ports)
			}
			if i == cur {
				marker = s.colored(s.th.Accent, s.gl.Cursor+" ")
				label = s.colored(s.th.Accent, fmt.Sprintf("%-6d %s", p.PID, p.Name))
				if p.Ports != "" {
					label += "  " + s.Subtle.Render(p.Ports)
				}
			}
			rows = append(rows, s.clampLine(marker+label, subW))
		}
		if len(fp) > maxRows {
			rows = append(rows, s.Subtle.Render(fmt.Sprintf("…ещё %d", len(fp)-maxRows)))
		}
	} else if strings.TrimSpace(m.procInput.Value()) == "" {
		rows = append(rows, "", s.Subtle.Render("(процессы с сетевой активностью не найдены)"))
	}

	if m.errText != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.errText, subW)))
	}
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	footer := s.clampLine(s.footerHints([][2]string{
		{s.gl.ArrowsUD, "выбор"},
		{"Enter", "проксировать"},
		{"^R", "перезапуск"},
		{"esc", "отмена"},
	}), max(m.width, 1))
	return m.frame(header, body, footer)
}

// --- warning modal (the narrow-terminal overflow fix) ---

func (m Model) modalView() string {
	s := m.styles
	w, h := max(m.width, 1), max(m.height, 1)
	tight := h < 16 // short terminal: shed vertical padding/spacers so it fits

	maxW := max(w-2, 1)
	titleText := s.gl.Warn + " Cisco активен"

	maxBox := min(w-4, 56)
	vpad := 1
	if tight {
		vpad = 0
	}
	box := s.r.NewStyle().Border(s.gl.Border).BorderForeground(m.theme.Warn).Padding(vpad, 2)
	inner := maxBox - box.GetHorizontalFrameSize() // subtract border+padding before wrapping

	var card string
	if inner < 8 {
		// Ultra-narrow terminal: drop the border, just centred wrapped text.
		card = lipgloss.JoinVertical(lipgloss.Left,
			s.colored(m.theme.Warn, wrap(titleText, maxW)),
			"",
			wrap(m.modal, maxW),
			"",
			s.Subtle.Render(wrap("(любая клавиша)", maxW)),
		)
	} else {
		title := s.colored(m.theme.Warn, wrap(titleText, inner))
		body := wrap(m.modal, inner)
		hint := s.Subtle.Render("(любая клавиша)")
		content := lipgloss.JoinVertical(lipgloss.Left, title, "", body, "", hint)
		if tight {
			content = lipgloss.JoinVertical(lipgloss.Left, title, body, hint)
		}
		card = box.Width(inner).Render(content)
	}
	placed := lipgloss.Place(w, h, lipgloss.Center, lipgloss.Center, card)
	// Final clamp: a long warning on a short terminal can be taller than h, and
	// Place never truncates — keep it within the screen so nothing spills.
	return s.r.NewStyle().MaxWidth(w).MaxHeight(h).Render(placed)
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
