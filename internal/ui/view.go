package ui

import (
	"fmt"
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
		body = lipgloss.JoinHorizontal(lipgloss.Top, status, "  ", mode)
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
			body = lipgloss.JoinVertical(lipgloss.Left, status, mode)
		} else {
			body = lipgloss.JoinVertical(lipgloss.Left, status, "", mode)
		}
	}
	return m.frame(header, body, footer)
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
	header := m.topBar("singctl "+s.gl.Dash+" вставьте VLESS-ссылку", "")

	var box string
	if lay == layoutNarrow {
		// No border to save cells; clamp defensively so a long link can never
		// overflow (a focused input already windows itself to its Width).
		box = s.clampLine(m.input.View(), max(m.width, 1))
	} else {
		bw := min(m.width-2, 60)
		box = s.Panel.Width(max(bw-2, 1)).Render(m.input.View())
	}
	subW := max(m.width-2, 1)
	rows := []string{box}
	if m.errText != "" {
		rows = append(rows, "", s.Err.Render(wrap(s.gl.Warn+" "+m.errText, subW)))
	}
	rows = append(rows, "", s.Muted.Render(wrap("Ничего не запустится, пока вы сами не выберете режим.", subW)))
	body := lipgloss.JoinVertical(lipgloss.Left, rows...)

	pairs := [][2]string{{"Enter", "загрузить"}}
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

func (m Model) connsView() string {
	s := m.styles
	header := m.connsHeaderView()
	footer := s.clampLine(s.footerHints([][2]string{
		{"c/esc", "назад"},
		{"ctrl+c", "выход"},
	}), max(m.width, 1))

	w := max(m.width, 1)
	var rows []string
	// Latency table for the failover group (only meaningful with data).
	if len(m.latency) > 0 {
		rows = append(rows, s.Subtle.Render("серверы:"))
		for _, r := range m.latency {
			marker := "  "
			if r.Selected || r.Tag == m.latencySel {
				marker = s.colored(s.th.Accent, s.gl.DotOn+" ")
			}
			rows = append(rows, s.clampLine(marker+r.Tag+"  "+delayText(r.Delay), w))
		}
		rows = append(rows, s.rule(w))
	}

	if len(m.conns) == 0 {
		rows = append(rows, s.Subtle.Render("(нет активных соединений)"))
	} else {
		for _, c := range m.conns {
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
	}
	body := strings.Join(rows, "\n")
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
