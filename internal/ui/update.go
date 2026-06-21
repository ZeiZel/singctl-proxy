package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/policy"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case NetStateMsg:
		m.cisco = msg.Cisco
		m.phys = msg.PhysIface
		return m, listen(m.notes)

	case StatusMsg:
		m.mode = msg.Mode
		m.busy = false
		m.segCursor = int(msg.Mode)
		if msg.Note != "" {
			m.status = msg.Note
		}
		return m, listen(m.notes)

	case ConnectionsMsg:
		m.conns = msg.Rows
		return m, listen(m.notes)

	case LatencyMsg:
		m.latency = msg.Rows
		m.latencySel = msg.Selected
		return m, listen(m.notes)

	case linkLoadedMsg:
		m.loaded = true
		if link := strings.TrimSpace(m.input.Value()); link != "" {
			m.currentLink = link
		}
		m.screen = ScreenDashboard
		m.mode = RunOff
		m.busy = false
		m.segCursor = int(RunOff)
		m.errText = ""
		m.status = "ссылка загружена — выберите режим"
		return m, nil

	case proxyEnabledMsg:
		m.mode = RunProxy
		m.busy = false
		m.errText = ""
		m.segCursor = int(RunProxy)
		m.status = "PROXY запущен"
		return m, nil

	case vpnEnabledMsg:
		m.mode = RunVPN
		m.busy = false
		m.errText = ""
		m.segCursor = int(RunVPN)
		m.status = "VPN запущен"
		return m, nil

	case stoppedMsg:
		m.mode = RunOff
		m.busy = false
		m.errText = ""
		m.segCursor = int(RunOff)
		m.status = "остановлено"
		return m, nil

	case errMsg:
		m.busy = false
		m.status = "" // drop the stale "…" activity line; show only the error
		m.errText = msg.err.Error()
		return m, nil

	case logsMsg:
		m.logs = msg.content
		m.refreshLogViewport()
		return m, nil

	case logsTickMsg:
		if m.showLogs {
			return m, tea.Batch(readLogsCmd(m.logPath), logsTick())
		}
		return m, nil
	}

	if m.screen == ScreenLink {
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd
	}
	return m, nil
}

// relayout recomputes size-dependent state on every WindowSizeMsg: the help
// footer width (drives truncation), the link input width, and the logs viewport
// dimensions. Styles need no rebuild — width is applied at render time.
func (m *Model) relayout() {
	m.help.Width = m.width
	m.input.Width = max(m.width-6, 10)

	bodyH := max(m.height-m.logsChromeHeight(), 1)
	w := max(m.width, 1)
	if !m.vpReady {
		m.vp = viewport.New(w, bodyH)
		m.vpReady = true
	} else {
		m.vp.Width, m.vp.Height = w, bodyH
	}
	m.refreshLogViewport()
}

// refreshLogViewport re-wraps the (tail of the) log buffer to the viewport
// width and auto-follows the tail when the user is already at the bottom.
func (m *Model) refreshLogViewport() {
	if !m.vpReady {
		return
	}
	atBottom := m.vp.AtBottom()
	content := wrap(tailLines(m.logs, 5000), max(m.vp.Width, 1))
	if strings.TrimSpace(content) == "" {
		content = m.styles.Subtle.Render("(логи появятся после запуска режима)")
	}
	m.vp.SetContent(content)
	if atBottom {
		m.vp.GotoBottom()
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A shown modal swallows the next keypress to dismiss itself — this must stay
	// first so any key (including ctrl+c) clears it before other handling.
	if m.modal != "" {
		m.modal = ""
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// Connections overlay: c/esc/q return to the dashboard.
	if m.showConns {
		switch {
		case key.Matches(msg, m.keys.Conns), msg.String() == "esc", msg.String() == "q":
			m.showConns = false
		}
		return m, nil
	}

	// Logs overlay: l/esc/q return to the dashboard; everything else scrolls the
	// viewport (intercept the close keys before viewport sees them).
	if m.showLogs {
		switch {
		case key.Matches(msg, m.keys.Logs), msg.String() == "esc", msg.String() == "q":
			m.showLogs = false
			return m, nil
		case msg.String() == "g":
			m.vp.GotoTop()
			return m, nil
		case msg.String() == "G":
			m.vp.GotoBottom()
			return m, nil
		default:
			var cmd tea.Cmd
			m.vp, cmd = m.vp.Update(msg)
			return m, cmd
		}
	}

	switch m.screen {
	case ScreenLink:
		switch msg.String() {
		case "esc":
			// Cancel editing and go back — unless this is the first-run screen
			// (no profile yet), where esc exits.
			if m.loaded {
				m.screen = ScreenDashboard
				m.input.Blur()
				return m, nil
			}
			return m, tea.Quit
		case "enter":
			link := strings.TrimSpace(m.input.Value())
			if link == "" {
				m.errText = "введите vless:// ссылку"
				return m, nil
			}
			m.errText = ""
			m.busy = true
			m.status = "загрузка ссылки…"
			return m, loadLinkCmd(m.backend, link)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case ScreenDashboard:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Proxy):
			return m.applyMode(RunProxy)
		case key.Matches(msg, m.keys.VPN):
			return m.applyMode(RunVPN)
		case key.Matches(msg, m.keys.Stop):
			return m.applyMode(RunOff)
		case key.Matches(msg, m.keys.Edit):
			// Change link: clear the field and return to input (cancellable with esc).
			// The current link stays visible as the placeholder so the user sees
			// what they are replacing.
			m.input.SetValue("")
			if m.currentLink != "" {
				m.input.Placeholder = m.currentLink
			}
			m.input.Focus()
			m.screen = ScreenLink
			m.errText = ""
			return m, textinput.Blink
		case key.Matches(msg, m.keys.Logs):
			m.showLogs = true
			return m, tea.Batch(readLogsCmd(m.logPath), logsTick())
		case key.Matches(msg, m.keys.Conns):
			m.showConns = true
			return m, nil
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case key.Matches(msg, m.keys.Next), key.Matches(msg, m.keys.Right):
			m.segCursor = (m.segCursor + 1) % 3
			return m, nil
		case key.Matches(msg, m.keys.Prev), key.Matches(msg, m.keys.Left):
			m.segCursor = (m.segCursor + 2) % 3
			return m, nil
		case key.Matches(msg, m.keys.Activate):
			return m.applyMode(RunMode(m.segCursor))
		}
	}
	return m, nil
}

// applyMode is the single entry point for changing the running mode, shared by
// the single-key shortcuts (p/v/s) and the segmented selector (Enter). VPN is
// always routed through requestVPN so the Cisco policy guard is never bypassed.
func (m Model) applyMode(target RunMode) (tea.Model, tea.Cmd) {
	m.segCursor = int(target)
	switch target {
	case RunProxy:
		m.busy = true
		m.status = "запуск PROXY…"
		return m, enableProxyCmd(m.backend)
	case RunVPN:
		return m.requestVPN()
	default: // RunOff
		m.busy = true
		m.status = "остановка…"
		return m, stopCmd(m.backend)
	}
}

// requestVPN delegates the enable decision to the policy engine (the same one
// the monitor uses), so the "block while Cisco active" rule is never duplicated
// in the view.
func (m Model) requestVPN() (tea.Model, tea.Cmd) {
	cisco := policy.CiscoInactive
	if m.cisco {
		cisco = policy.CiscoActive
	}
	res := m.decide(policy.DecideInput{Mode: toPolicyMode(m.mode), NewCisco: cisco, Intent: policy.IntentEnableVPN})
	for _, a := range res.Actions {
		switch a {
		case policy.ActShowWarning:
			m.modal = "Cisco Secure Client активен — VPN-режим заблокирован, чтобы не конфликтовать ни единым пакетом. Отключите Cisco и повторите."
			m.segCursor = int(m.mode) // don't leave the cursor stranded on the blocked VPN segment
			return m, nil
		case policy.ActStartForwarder:
			m.busy = true
			m.status = "запуск VPN…"
			return m, enableVPNCmd(m.backend)
		}
	}
	return m, nil
}

func toPolicyMode(m RunMode) policy.Mode {
	if m == RunVPN {
		return policy.ModeVPN
	}
	return policy.ModeProxy
}
