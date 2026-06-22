package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// These widgets are stateless styled-string builders (the Charm/opencode
// pattern — no widget framework). The model owns focus/selection state and
// passes it in as flags.

// panel renders a titled card. When width > 0 the body wraps to fit. A bordered
// panel draws a rounded box with the title on its first inner row; a borderless
// panel (narrow layouts) draws a bold title with an indented body to save the
// two horizontal cells a border would cost.
func (s Styles) panel(title, body string, width int, active, bordered bool) string {
	if !bordered {
		head := s.PanelTitle.Render(title)
		bodyW := width - 1
		if bodyW < 1 {
			bodyW = 1
		}
		indented := s.r.NewStyle().Width(bodyW).MarginLeft(1).Render(body)
		return lipgloss.JoinVertical(lipgloss.Left, head, indented)
	}
	st := s.Panel
	if active {
		st = s.PanelActive
	}
	if width > 0 {
		// Width is the content+padding box; the border adds one cell per side,
		// so subtract 2 to make the rendered block exactly `width` wide.
		st = st.Width(width - 2)
	}
	head := s.PanelTitle.Render(title)
	return st.Render(lipgloss.JoinVertical(lipgloss.Left, head, body))
}

// segmented renders the OFF|PROXY|VPN selector. selected is the running mode;
// cursor is the keyboard position; disabled greys a segment (VPN while Cisco is
// active). The whole control sits in a focused-coloured box so it reads as the
// active widget.
func (s Styles) segmented(opts []string, selected, cursor int, disabled map[int]bool, vertical bool, mark func(i int, seg string) string) string {
	// A filled/blank radio marker prefixes every segment so the running mode
	// reads even with no colour (the accent fill alone vanishes under NO_COLOR /
	// ascii). DotOn and a same-width blank keep the columns aligned.
	markW := lipgloss.Width(s.gl.DotOn)
	blank := strings.Repeat(" ", markW)
	segs := make([]string, len(opts))
	for i, label := range opts {
		marker := blank
		if i == selected {
			marker = s.gl.DotOn
		}
		txt := marker + " " + label
		var st lipgloss.Style
		switch {
		case disabled[i]:
			st = s.SegDisabled
		case i == selected:
			st = s.SegSelected
		default:
			st = s.SegNormal
		}
		if i == cursor {
			txt = txt + " " + s.gl.Cursor
			if !disabled[i] && i != selected {
				st = st.Foreground(s.th.Text).Bold(true)
			}
		}
		segs[i] = st.Render(txt)
		if mark != nil {
			segs[i] = mark(i, segs[i]) // clickable zone (bubblezone)
		}
	}
	joined := lipgloss.JoinHorizontal(lipgloss.Center, segs...)
	if vertical {
		joined = lipgloss.JoinVertical(lipgloss.Left, segs...)
	}
	box := s.SegBox
	if cursor >= 0 {
		box = box.BorderForeground(s.th.BorderActive)
	}
	return box.Render(joined)
}

// modeBadge renders the current running mode as a coloured dot + label.
func (s Styles) modeBadge(m RunMode) string {
	c := modeColor(s.th, m)
	label := map[RunMode]string{RunOff: "ВЫКЛ", RunProxy: "ПРОКСИ", RunVPN: "VPN"}[m]
	return s.dot(c) + " " + s.colored(c, label)
}

// ciscoBadge renders the Cisco indicator: amber dot + "активен" when present,
// muted hollow dot + "неактивен" otherwise.
func (s Styles) ciscoBadge(active bool) string {
	if active {
		return s.colored(s.th.Warn, s.gl.DotOn+" активен")
	}
	return s.Muted.Render(s.gl.DotOff + " неактивен")
}

// kv renders an aligned "key   value" status row. The key is padded to keyW
// cells (measured, ANSI/cyrillic-aware).
func (s Styles) kv(key, val string, keyW int) string {
	pad := keyW - lipgloss.Width(key)
	if pad < 1 {
		pad = 1
	}
	return s.Key.Render(key) + strings.Repeat(" ", pad) + val
}

// clampLine hard-truncates a single line to w cells with an ellipsis tail, so a
// long value (interface name, status note) can never push past the terminal.
func (s Styles) clampLine(text string, w int) string {
	if w < 1 {
		w = 1
	}
	return ansi.Truncate(text, w, s.gl.Ellipsis)
}

// clampBlock clamps every line of a multi-line string to w cells. Used for the
// help footer, whose full (ShowAll) view is multi-line — feeding that to the
// single-line clampLine would flatten and clip it.
func (s Styles) clampBlock(text string, w int) string {
	lines := strings.Split(text, "\n")
	for i := range lines {
		lines[i] = s.clampLine(lines[i], w)
	}
	return strings.Join(lines, "\n")
}

// wrap word-wraps prose to w cells, hard-breaking any word longer than w so a
// line can never overflow (lipgloss Width = word-wrap + overflow break). Output
// lines are padded to w; that's harmless inside the panels/centred overlays.
func wrap(text string, w int) string {
	if w < 1 {
		w = 1
	}
	return lipgloss.NewStyle().Width(w).Render(text)
}
