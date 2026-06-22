package ui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// action-log severity levels. Kept as plain ints so ActionMsg (messages.go) can
// carry them across packages without importing this file's types.
const (
	ActInfo = iota
	ActOk
	ActWarn
	ActErr
)

// actionEntry is one timestamped event in the action log.
type actionEntry struct {
	At    time.Time
	Level int
	Text  string
}

// actionLog is a reducer-only ring of recent user-facing actions/events (mode
// changes, launches, errors, Cisco auto-suspends). Like consoleBuf it holds no
// mutex — it is mutated only from the UI's Update on the single goroutine.
type actionLog struct {
	lines []actionEntry
	max   int
}

// newActionLog builds an empty action log (~300 entries).
func newActionLog() *actionLog {
	return &actionLog{max: 300}
}

// add records an event at the current time, trimming the oldest when full.
func (a *actionLog) add(level int, text string) {
	a.lines = append(a.lines, actionEntry{At: time.Now(), Level: level, Text: text})
	if len(a.lines) > a.max {
		a.lines = a.lines[len(a.lines)-a.max:]
	}
}

// levelColor maps a level to its theme colour.
func levelColor(th Theme, level int) lipgloss.AdaptiveColor {
	switch level {
	case ActOk:
		return th.Ok
	case ActWarn:
		return th.Warn
	case ActErr:
		return th.Error
	default:
		return th.Accent
	}
}

// levelGlyph returns an ascii-safe tag so severity stays readable under
// NO_COLOR / ascii glyph fallback, paired with the colour above.
func levelGlyph(level int) string {
	switch level {
	case ActOk:
		return "OK "
	case ActWarn:
		return "!! "
	case ActErr:
		return "ERR"
	default:
		return "·· "
	}
}

// render formats the (tail of the) action log for a footer/pane of the given
// width/height: "15:04:05 <dot> <glyph> text", colourised by level. Only the
// HH:MM:SS portion of the timestamp is shown so renders stay deterministic.
func (a *actionLog) render(s Styles, width, height int) string {
	if width < 1 {
		width = 1
	}
	rows := a.lines
	if height > 0 && len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	if len(rows) == 0 {
		return s.Subtle.Render("(действия появятся здесь)")
	}
	var out []string
	for _, e := range rows {
		c := levelColor(s.th, e.Level)
		ts := s.Subtle.Render(e.At.Format("15:04:05"))
		dot := s.dot(c)
		tag := s.colored(c, levelGlyph(e.Level))
		out = append(out, ts+" "+dot+" "+tag+" "+e.Text)
	}
	return strings.Join(out, "\n")
}
