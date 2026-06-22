package ui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// zoneConPID is the bubblezone namespace for a per-app console filter chip. pid
// 0 marks the «Все» chip (clears the filter).
func zoneConPID(pid int) string { return "con-pid-" + strconv.Itoa(pid) }

// consoleEntry is one captured stdout/stderr line from a proxied app.
type consoleEntry struct {
	PID    int
	App    string
	Stream string // "stdout" / "stderr" / "exit"
	Text   string
}

// consoleBuf is a reducer-only ring buffer of captured app output. It holds no
// mutex: it is mutated exclusively from the UI's Update (single goroutine), the
// same way the existing log/conn buffers are. apps maps a PID to the latest app
// name seen for it, so renders/filters can label lines even after they scroll.
type consoleBuf struct {
	lines []consoleEntry
	max   int
	apps  map[int]string
}

// newConsoleBuf builds an empty console ring (~2000 lines).
func newConsoleBuf() *consoleBuf {
	return &consoleBuf{max: 2000, apps: map[int]string{}}
}

// append adds one line, trimming the oldest when over capacity, and records the
// app name for the line's PID.
func (c *consoleBuf) append(e consoleEntry) {
	if e.App != "" && e.PID != 0 {
		c.apps[e.PID] = e.App
	}
	c.lines = append(c.lines, e)
	if len(c.lines) > c.max {
		// drop the oldest, keeping the slice bounded.
		c.lines = c.lines[len(c.lines)-c.max:]
	}
}

// pids returns the distinct PIDs present in the buffer, sorted ascending — used
// to build per-app filter chips.
func (c *consoleBuf) pids() []int {
	seen := map[int]struct{}{}
	var out []int
	for _, e := range c.lines {
		if e.PID == 0 {
			continue
		}
		if _, ok := seen[e.PID]; ok {
			continue
		}
		seen[e.PID] = struct{}{}
		out = append(out, e.PID)
	}
	sort.Ints(out)
	return out
}

// appName returns the recorded app name for a PID (falls back to "app").
func (c *consoleBuf) appName(pid int) string {
	if n, ok := c.apps[pid]; ok && n != "" {
		return n
	}
	return "app"
}

// consoleFilterChips renders the clickable per-app filter chips for the expanded
// console pane: an «Все» chip (clears the filter) followed by one chip per app
// present in the buffer. The chip matching the active filter is highlighted. Each
// chip is marked as a bubblezone (zoneConPID) so handleMouse can isolate one app.
func (m Model) consoleFilterChips(w int) string {
	s := m.styles
	chip := func(pid int, label string) string {
		txt := " " + label + " "
		st := s.SegNormal
		if m.consoleFilter == pid {
			st = s.SegSelected
		}
		return m.zm.Mark(zoneConPID(pid), st.Render(txt))
	}
	chips := []string{chip(0, "Все")}
	for _, pid := range m.console.pids() {
		chips = append(chips, chip(pid, m.console.appName(pid)+" "+strconv.Itoa(pid)))
	}
	return s.clampLine(strings.Join(chips, " "), max(w, 1))
}

// render formats the (tail of the) console for a pane/viewport of the given
// width/height. Each line is tagged "[app pid] text"; stderr/exit lines are
// tinted via the theme. filterPID <= 0 shows all apps; otherwise only that PID.
func (c *consoleBuf) render(s Styles, width, height, filterPID int) string {
	if width < 1 {
		width = 1
	}
	var rows []string
	for _, e := range c.lines {
		if filterPID > 0 && e.PID != filterPID {
			continue
		}
		app := e.App
		if app == "" {
			app = c.appName(e.PID)
		}
		tag := fmt.Sprintf("[%s %d]", app, e.PID)
		line := tag + " " + e.Text
		switch e.Stream {
		case "stderr":
			line = s.colored(s.th.Warn, tag) + " " + e.Text
		case "exit":
			line = s.Subtle.Render(line)
		default:
			line = s.colored(s.th.Subtle, tag) + " " + e.Text
		}
		rows = append(rows, line)
	}
	if len(rows) == 0 {
		return s.Subtle.Render("(вывод приложений появится после запуска)")
	}
	if height > 0 && len(rows) > height {
		rows = rows[len(rows)-height:]
	}
	return strings.Join(rows, "\n")
}
