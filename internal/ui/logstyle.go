package ui

import (
	"strings"

	charmlog "github.com/charmbracelet/log"
	"github.com/charmbracelet/lipgloss"
)

// logLevels maps the level tokens that appear in sing-box log lines to a
// charm/log level. FATAL/PANIC are mapped to Error (never FatalLevel) so the
// renderer can never trigger an os.Exit from inside the TUI.
var logLevels = []struct {
	tok string
	lvl charmlog.Level
}{
	{"ERROR", charmlog.ErrorLevel},
	{"ERRO", charmlog.ErrorLevel},
	{"FATAL", charmlog.ErrorLevel},
	{"PANIC", charmlog.ErrorLevel},
	{"WARN", charmlog.WarnLevel},
	{"DEBUG", charmlog.DebugLevel},
	{"DEBU", charmlog.DebugLevel},
	{"TRACE", charmlog.DebugLevel},
	{"INFO", charmlog.InfoLevel},
}

// detectLogLevel finds the first level token in a log line and returns the
// level plus the line with that token removed (so the badge isn't duplicated).
// ok is false when no level is present (the line is shown verbatim).
func detectLogLevel(line string) (charmlog.Level, string, bool) {
	up := strings.ToUpper(line)
	for _, L := range logLevels {
		if i := strings.Index(up, L.tok); i >= 0 {
			// Drop the token (and an immediately-following "[..]" sing-box marker).
			rest := line[i+len(L.tok):]
			rest = strings.TrimPrefix(rest, "[")
			if j := strings.IndexByte(rest, ']'); j >= 0 && j < 6 {
				rest = rest[j+1:]
			}
			msg := strings.TrimSpace(line[:i] + " " + rest)
			return L.lvl, msg, true
		}
	}
	return charmlog.InfoLevel, line, false
}

// levelBadge renders a fixed-width, colour-coded level tag (INFO/WARN/ERR/DBG)
// plus the style for the message text, so each severity reads at a glance. Colours
// come from the theme (caps-aware) so they stay distinct and degrade gracefully.
func (s Styles) levelBadge(lvl charmlog.Level) (badge string, msg lipgloss.Style) {
	ns := s.r.NewStyle().Bold(true)
	switch lvl {
	case charmlog.ErrorLevel:
		return ns.Foreground(s.th.OnAccent).Background(s.th.Error).Render(" ERR "), s.r.NewStyle().Foreground(s.th.Error)
	case charmlog.WarnLevel:
		return ns.Foreground(s.th.OnAccent).Background(s.th.Warn).Render(" WARN"), s.r.NewStyle().Foreground(s.th.Warn)
	case charmlog.DebugLevel:
		return s.r.NewStyle().Foreground(s.th.Subtle).Render(" DBG "), s.r.NewStyle().Foreground(s.th.Subtle)
	default: // info
		return ns.Foreground(s.th.OnAccent).Background(s.th.Accent).Render(" INFO"), s.r.NewStyle().Foreground(s.th.Text)
	}
}

// styleLogText renders raw sing-box log text: each recognised line gets a
// colour-coded INFO/WARN/ERR/DBG badge + a level-tinted message, so severity is
// obvious at a glance. Colours are theme-based (caps-aware → plain under
// NO_COLOR/ascii). Lines with no recognised level are shown verbatim.
func (m Model) styleLogText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	s := m.styles
	var b strings.Builder
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		if lvl, msg, ok := detectLogLevel(line); ok {
			badge, mst := s.levelBadge(lvl)
			b.WriteString(badge + " " + mst.Render(msg) + "\n")
		} else {
			b.WriteString(s.Subtle.Render(line) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
