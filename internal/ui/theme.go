package ui

import (
	"os"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Theme is the semantic colour palette. Every colour is an AdaptiveColor so the
// UI looks right on both light and dark terminals; nothing in the views ever
// writes a hex literal directly. Colour is always paired with a glyph/label so
// state stays readable when colour degrades (NO_COLOR, non-truecolor, or a
// background mis-detected under sudo — which singctl always runs under).
type Theme struct {
	Accent       lipgloss.AdaptiveColor // brand / focus
	OnAccent     lipgloss.AdaptiveColor // text drawn on an accent fill
	Text         lipgloss.AdaptiveColor
	Muted        lipgloss.AdaptiveColor
	Subtle       lipgloss.AdaptiveColor
	Border       lipgloss.AdaptiveColor
	BorderActive lipgloss.AdaptiveColor

	Off   lipgloss.AdaptiveColor // mode OFF
	Proxy lipgloss.AdaptiveColor // mode PROXY
	Vpn   lipgloss.AdaptiveColor // mode VPN
	Warn  lipgloss.AdaptiveColor
	Ok    lipgloss.AdaptiveColor
	Error lipgloss.AdaptiveColor
}

// DefaultTheme is the shipped palette: an indigo accent with semantic
// success/warn/error/muted roles.
func DefaultTheme() Theme {
	return Theme{
		Accent:       lipgloss.AdaptiveColor{Light: "#5B53D6", Dark: "#8B7FF5"},
		OnAccent:     lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B0B12"},
		Text:         lipgloss.AdaptiveColor{Light: "#1C1C1C", Dark: "#E4E4E4"},
		Muted:        lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#9A9A9A"},
		Subtle:       lipgloss.AdaptiveColor{Light: "#9A9A9A", Dark: "#6E6E6E"},
		Border:       lipgloss.AdaptiveColor{Light: "#D0D0D0", Dark: "#3A3A3A"},
		BorderActive: lipgloss.AdaptiveColor{Light: "#5B53D6", Dark: "#8B7FF5"},
		Off:          lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#9A9A9A"},
		Proxy:        lipgloss.AdaptiveColor{Light: "#1A7F4B", Dark: "#3FD17B"},
		Vpn:          lipgloss.AdaptiveColor{Light: "#2563EB", Dark: "#60A5FA"},
		Warn:         lipgloss.AdaptiveColor{Light: "#B26A00", Dark: "#E0A100"},
		Ok:           lipgloss.AdaptiveColor{Light: "#1A7F4B", Dark: "#3FD17B"},
		Error:        lipgloss.AdaptiveColor{Light: "#C02626", Dark: "#FF6B6B"},
	}
}

// modeColor maps a running mode to its semantic colour.
func modeColor(th Theme, m RunMode) lipgloss.AdaptiveColor {
	switch m {
	case RunProxy:
		return th.Proxy
	case RunVPN:
		return th.Vpn
	default:
		return th.Off
	}
}

// Caps is the terminal capability probe, taken once at startup. It carries a
// dedicated renderer so colour degradation (truecolor→256→16→ascii) and
// background detection are explicit and consistent — important because sudo
// scrubs the environment Bubble Tea would otherwise read.
type Caps struct {
	R       *lipgloss.Renderer
	Unicode bool
	Color   bool
}

// DetectCaps probes stdout for colour support and the locale for UTF-8. NO_COLOR
// forces ascii colour; SINGCTL_ASCII forces ascii glyphs.
func DetectCaps() Caps {
	r := lipgloss.NewRenderer(os.Stdout)
	if _, noColor := os.LookupEnv("NO_COLOR"); noColor {
		r.SetColorProfile(termenv.Ascii)
	}
	color := r.ColorProfile() != termenv.Ascii

	// Pin the background once, before Bubble Tea owns stdin — singctl runs under
	// sudo with a scrubbed env, where lazy OSC-11 probing is fragile. An explicit
	// override wins; otherwise probe now and cache the answer.
	switch strings.ToLower(os.Getenv("SINGCTL_BACKGROUND")) {
	case "light":
		r.SetHasDarkBackground(false)
	case "dark":
		r.SetHasDarkBackground(true)
	default:
		r.SetHasDarkBackground(r.HasDarkBackground())
	}

	lang := os.Getenv("LC_ALL")
	if lang == "" {
		lang = os.Getenv("LC_CTYPE")
	}
	if lang == "" {
		lang = os.Getenv("LANG")
	}
	unicode := strings.Contains(strings.ToUpper(lang), "UTF")
	if _, force := os.LookupEnv("SINGCTL_ASCII"); force {
		unicode = false
	}
	return Caps{R: r, Unicode: unicode, Color: color}
}

// asciiCaps is a deterministic capability set used by tests: ascii colour
// profile + ascii glyphs, so rendered width never depends on the host locale.
func asciiCaps() Caps {
	r := lipgloss.NewRenderer(os.Stdout)
	r.SetColorProfile(termenv.Ascii)
	return Caps{R: r, Unicode: false, Color: false}
}

// Styles holds every lipgloss.Style the UI uses, built once from a Theme + Caps
// so the padding/border/accent rhythm is identical across every screen. Width
// is applied at render time (see Panel), so Styles never needs rebuilding on
// resize — only on a theme change.
type Styles struct {
	r  *lipgloss.Renderer
	th Theme
	gl Glyphs

	Title  lipgloss.Style // app name / screen title (accent, bold)
	Muted  lipgloss.Style
	Subtle lipgloss.Style
	Err    lipgloss.Style

	Panel       lipgloss.Style // bordered card
	PanelActive lipgloss.Style // bordered card, focused border
	PanelTitle  lipgloss.Style // title row inside a panel
	Key         lipgloss.Style // status-row key label

	SegBox      lipgloss.Style // box around the segmented selector
	SegSelected lipgloss.Style // the active (running) segment
	SegNormal   lipgloss.Style // an inactive segment
	SegDisabled lipgloss.Style // a blocked segment (VPN while Cisco active)

	Help lipgloss.Style
}

// NewStyles builds the style set from the renderer in Caps so all styles share
// one colour profile.
func NewStyles(c Caps, th Theme, gl Glyphs) Styles {
	ns := c.R.NewStyle
	panel := ns().Border(gl.Border).BorderForeground(th.Border).Padding(0, 1)
	seg := ns().Padding(0, 2)
	return Styles{
		r:  c.R,
		th: th,
		gl: gl,

		Title:  ns().Bold(true).Foreground(th.Accent),
		Muted:  ns().Foreground(th.Muted),
		Subtle: ns().Foreground(th.Subtle),
		Err:    ns().Foreground(th.Error),

		Panel:       panel,
		PanelActive: panel.BorderForeground(th.BorderActive),
		PanelTitle:  ns().Bold(true).Foreground(th.Accent),
		Key:         ns().Foreground(th.Muted),

		SegBox:      ns().Border(gl.Border).BorderForeground(th.Border).Padding(0, 1),
		SegSelected: seg.Background(th.Accent).Foreground(th.OnAccent).Bold(true),
		SegNormal:   seg.Foreground(th.Muted),
		SegDisabled: seg.Faint(true).Foreground(th.Subtle),

		Help: ns().Foreground(th.Subtle),
	}
}

// --- small render helpers ---

// colored renders text in a single foreground colour.
func (s Styles) colored(c lipgloss.AdaptiveColor, text string) string {
	return s.r.NewStyle().Foreground(c).Render(text)
}

// dot renders a filled status dot in the given colour.
func (s Styles) dot(c lipgloss.AdaptiveColor) string {
	return s.colored(c, s.gl.DotOn)
}

// rule renders a horizontal divider of the given cell width.
func (s Styles) rule(w int) string {
	if w < 1 {
		w = 1
	}
	ch := "─"
	if !s.gl.unicode() {
		ch = "-"
	}
	return s.Subtle.Render(strings.Repeat(ch, w))
}

// unicode reports whether this glyph set is the Unicode (non-ascii) variant.
func (g Glyphs) unicode() bool { return g.DotOn == "●" }
