package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// btnSpec describes one clickable action button in the visible action bar. id is
// the bubblezone namespace (so a click maps back to the button); label is the
// Russian caption; key echoes the keyboard shortcut for the on-pill hint; fire is
// the SAME code path the shortcut runs, so a click and a keypress are identical.
type btnSpec struct {
	id    string
	label string
	key   string
	fire  func(Model) (tea.Model, tea.Cmd)
}

// actionButtons returns the ordered action-bar specs. Every fire reuses the
// existing handler (applyMode / openSection / expandPane / daemonize cmd) so no
// primary action is hidden behind a shortcut — the bar mirrors the keymap.
func (m Model) actionButtons() []btnSpec {
	daemon := btnSpec{id: "btn-daemon", label: "Демон", key: "g", fire: func(m Model) (tea.Model, tea.Cmd) {
		m.status = "запуск в фоне…"
		return m, daemonizeCmd(m.backend)
	}}
	if m.attached {
		daemon = btnSpec{id: "btn-daemon", label: "Остановить демон", key: "g", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.status = "останавливаю демон…"
			return m, stopDaemonCmd(m.backend)
		}}
	}

	return []btnSpec{
		{id: "btn-mode-0", label: "ВЫКЛ", key: "s", fire: func(m Model) (tea.Model, tea.Cmd) { return m.applyMode(RunOff) }},
		{id: "btn-mode-1", label: "ПРОКСИ", key: "p", fire: func(m Model) (tea.Model, tea.Cmd) { return m.applyMode(RunProxy) }},
		{id: "btn-mode-2", label: "VPN", key: "v", fire: func(m Model) (tea.Model, tea.Cmd) { return m.applyMode(RunVPN) }},
		{id: "btn-apps", label: "Запустить", key: "x", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.pane = paneApps
			return m.expandSection(secApps)
		}},
		{id: "btn-logs", label: "Логи", key: "l", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.pane = paneSingbox
			return m.expandSection(secLogs)
		}},
		{id: "btn-conns", label: "Соединения", key: "c", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.pane = paneConns
			return m.expandSection(secConns)
		}},
		{id: "btn-console", label: "Консоль", key: "o", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.pane = paneConsole
			return m.expandPane()
		}},
		{id: "btn-settings", label: "Настройки", key: "g", fire: func(m Model) (tea.Model, tea.Cmd) {
			m.pane = paneSettings
			return m.expandSection(secSettings)
		}},
		daemon,
		{id: "btn-stop", label: "Стоп", key: "s", fire: func(m Model) (tea.Model, tea.Cmd) { return m.applyMode(RunOff) }},
		{id: "btn-keys", label: "Ключи", key: "e", fire: func(m Model) (tea.Model, tea.Cmd) { return m.openSection(secKeys) }},
	}
}

// buttonActive reports whether a button should render in its active style: the
// mode buttons highlight the running mode so the bar doubles as a mode indicator.
func (m Model) buttonActive(spec btnSpec) bool {
	switch spec.id {
	case "btn-mode-0":
		return m.mode == RunOff
	case "btn-mode-1":
		return m.mode == RunProxy
	case "btn-mode-2":
		return m.mode == RunVPN
	}
	return false
}

// renderButtonBar lays out the action buttons as clickable pills, wrapping onto
// new rows when the running width is exceeded. Each pill is marked with its
// bubblezone id so handleMouse can hit-test it before the pane grid.
func (m Model) renderButtonBar(width int) string {
	if width < 1 {
		width = 1
	}
	s := m.styles
	specs := m.actionButtons()

	const gap = 1
	var rows []string
	var row []string
	rowW := 0
	for _, spec := range specs {
		st := s.Button
		if m.buttonActive(spec) {
			st = s.ButtonActive
		}
		pill := m.zm.Mark(spec.id, st.Render(spec.label))
		pw := lipgloss.Width(pill)
		if len(row) > 0 && rowW+gap+pw > width {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
			row = nil
			rowW = 0
		}
		if len(row) > 0 {
			row = append(row, " ")
			rowW += gap
		}
		row = append(row, pill)
		rowW += pw
	}
	if len(row) > 0 {
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return strings.Join(rows, "\n")
}
