package ui

import (
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
)

// init pins ambiguous-width runes (● ○ ◄ ⚠ …) to a single cell so our width
// math (lipgloss.Width / ansi.Truncate) matches what the terminal draws. Under
// CJK locales these would otherwise measure as 2 cells and break alignment.
func init() {
	runewidth.DefaultCondition.EastAsianWidth = false
}

// Glyphs is the small set of decorative symbols the UI draws. It is picked once
// at startup from terminal capabilities: a Unicode set for modern terminals and
// an ASCII fallback for terminals that can't render box-drawing/symbols (and for
// SINGCTL_ASCII / non-UTF locales). Russian body text is always width-1, so only
// the decoration ever degrades.
type Glyphs struct {
	Warn     string // warning marker
	DotOn    string // filled status dot
	DotOff   string // hollow status dot
	Cursor   string // keyboard-cursor marker on the focused segment
	Sep      string // footer / inline separator
	Dash     string // em-dash placeholder
	Ellipsis string // truncation tail
	ScrollAt string // "at bottom" indicator in the logs header
	ArrowsLR string // left/right hint
	ArrowsUD string // up/down hint
	Enter    string // enter-key hint
	Prompt   string // text-input prompt
	Border   lipgloss.Border
}

// PickGlyphs returns the Unicode set when the terminal supports it, otherwise a
// portable ASCII set. Everything decorative degrades here so nothing turns to
// mojibake (or shows a stray arrow) in a non-UTF / SINGCTL_ASCII terminal.
func PickGlyphs(unicode bool) Glyphs {
	if unicode {
		return Glyphs{
			Warn:     "⚠",
			DotOn:    "●",
			DotOff:   "○",
			Cursor:   "◄",
			Sep:      "·",
			Dash:     "—",
			Ellipsis: "…",
			ScrollAt: "↓",
			ArrowsLR: "←→",
			ArrowsUD: "↑↓",
			Enter:    "↵",
			Prompt:   "» ",
			Border:   lipgloss.RoundedBorder(),
		}
	}
	return Glyphs{
		Warn:     "(!)",
		DotOn:    "[*]",
		DotOff:   "[ ]",
		Cursor:   "<",
		Sep:      "-",
		Dash:     "-",
		Ellipsis: "...",
		ScrollAt: "v",
		ArrowsLR: "<>",
		ArrowsUD: "^v",
		Enter:    "enter",
		Prompt:   "> ",
		Border: lipgloss.Border{
			Top: "-", Bottom: "-", Left: "|", Right: "|",
			TopLeft: "+", TopRight: "+", BottomLeft: "+", BottomRight: "+",
		},
	}
}
