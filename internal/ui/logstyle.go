package ui

import (
	"strings"

	charmlog "github.com/charmbracelet/log"
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

// styleLogText renders raw sing-box log text with charm/log: each line gets a
// colored level badge and its message, honoring the terminal's colour profile
// (so it degrades to plain text under NO_COLOR/ascii). Lines with no recognised
// level are shown verbatim.
func (m Model) styleLogText(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return raw
	}
	var b strings.Builder
	lg := charmlog.New(&b)
	lg.SetReportTimestamp(false)
	lg.SetColorProfile(m.caps.R.ColorProfile())
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString("\n")
			continue
		}
		if lvl, msg, ok := detectLogLevel(line); ok {
			lg.Log(lvl, msg)
		} else {
			b.WriteString(line + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
