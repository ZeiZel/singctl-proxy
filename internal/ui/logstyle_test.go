package ui

import (
	"strings"
	"testing"

	charmlog "github.com/charmbracelet/log"
)

func TestDetectLogLevel(t *testing.T) {
	cases := []struct {
		line string
		lvl  charmlog.Level
		ok   bool
	}{
		{"2026-06-24 10:00:00 INFO router: started", charmlog.InfoLevel, true},
		{"2026-06-24 10:00:00 ERROR connect failed", charmlog.ErrorLevel, true},
		{"FATAL boom", charmlog.ErrorLevel, true}, // FATAL never maps to FatalLevel
		{"2026 WARN something", charmlog.WarnLevel, true},
		{"a plain line", charmlog.InfoLevel, false},
	}
	for _, c := range cases {
		lvl, _, ok := detectLogLevel(c.line)
		if ok != c.ok || (ok && lvl != c.lvl) {
			t.Errorf("detectLogLevel(%q) = (%v,%v), want (%v,%v)", c.line, lvl, ok, c.lvl, c.ok)
		}
	}
}

func TestStyleLogText_KeepsMessagesNoColorProfile(t *testing.T) {
	m := newWithCaps(&fakeBackend{}, nil, asciiCaps())
	out := m.styleLogText("2026-06-24 10:00:00 INFO router: up\nplain tail line")
	if !strings.Contains(out, "router: up") || !strings.Contains(out, "plain tail line") {
		t.Errorf("styled log lost its message text:\n%s", out)
	}
	if !strings.Contains(strings.ToUpper(out), "INFO") {
		t.Errorf("styled log should carry a level badge:\n%s", out)
	}
}
