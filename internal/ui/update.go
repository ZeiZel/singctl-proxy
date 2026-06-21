package ui

import (
	"strconv"
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
		m.refreshConnViewport()
		return m, listen(m.notes)

	case LatencyMsg:
		m.latency = msg.Rows
		m.latencySel = msg.Selected
		m.refreshConnViewport()
		return m, listen(m.notes)

	case linkAddedMsg:
		m.currentLinks = msg.links
		m.busy = false
		m.errText = ""
		m.input.SetValue("")
		m.status = "ключ добавлен (" + strconv.Itoa(len(msg.links)) + " всего)"
		return m, nil

	case linkLoadedMsg:
		m.loaded = true
		if link := strings.TrimSpace(m.input.Value()); link != "" {
			m.currentLink = link
		}
		m.currentLinks = msg.links
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

	case procResultMsg:
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = ""
		} else {
			m.errText = ""
			m.status = msg.note
			if msg.pid > 0 {
				m.routedPIDs = appendUnique(m.routedPIDs, msg.pid)
			}
		}
		return m, nil

	case procListMsg:
		if msg.err != nil {
			m.procErr = msg.err.Error()
			m.procRows = nil
		} else {
			m.procErr = ""
			m.procRows = msg.rows
		}
		m.procCursor = 0
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

	connH := max(m.height-m.connsChromeHeight(), 1)
	if !m.connVPReady {
		m.connVP = viewport.New(w, connH)
		m.connVPReady = true
	} else {
		m.connVP.Width, m.connVP.Height = w, connH
	}
	m.refreshConnViewport()
}

// refreshConnViewport re-renders the full connections + latency content into the
// connections viewport, auto-following the tail when already at the bottom.
func (m *Model) refreshConnViewport() {
	if !m.connVPReady {
		return
	}
	atBottom := m.connVP.AtBottom()
	m.connVP.SetContent(m.connContent(max(m.connVP.Width, 1)))
	if atBottom {
		m.connVP.GotoBottom()
	}
}

// connContent builds the full connections + latency text for the expanded view.
func (m Model) connContent(w int) string {
	var parts []string
	if lat := m.latencyBody(w); lat != "" {
		parts = append(parts, lat, m.styles.rule(w))
	}
	parts = append(parts, m.connsBody(w, 0))
	return strings.Join(parts, "\n")
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

	// Приложения view: a launch field (запустить приложение в прокси) + a process
	// picker. Tab toggles focus between them; esc closes.
	if m.showProc {
		switch {
		case msg.String() == "esc":
			m.showProc = false
			m.launchInput.Blur()
			m.procInput.Blur()
			return m, nil
		case msg.Type == tea.KeyTab:
			m.appFocus = 1 - m.appFocus
			if m.appFocus == 0 {
				m.launchInput.Focus()
				m.procInput.Blur()
			} else {
				m.procInput.Focus()
				m.launchInput.Blur()
			}
			return m, textinput.Blink
		case msg.String() == "up":
			if m.procCursor > 0 {
				m.procCursor--
			}
			return m, nil
		case msg.String() == "down":
			if n := len(m.filteredProcs()); n > 0 && m.procCursor < n-1 {
				m.procCursor++
			}
			return m, nil
		case msg.Type == tea.KeyCtrlR:
			// Restart the highlighted process in proxy mode (best-effort; the
			// only way to proxy an existing process on macOS).
			fp := m.filteredProcs()
			if len(fp) == 0 {
				return m, nil
			}
			pid := fp[clampIdx(m.procCursor, len(fp))].PID
			m.showProc = false
			m.status = "перезапускаю процесс в proxy-режиме…"
			return m, restartPIDCmd(m.backend, pid)
		case msg.String() == "enter":
			if m.appFocus == 0 { // launch an application by command
				v := strings.TrimSpace(m.launchInput.Value())
				if v == "" {
					return m, nil
				}
				m.launchInput.SetValue("")
				m.showProc = false
				m.status = "запускаю приложение через прокси…"
				return m, launchProcCmd(m.backend, strings.Fields(v))
			}
			return m.submitProc() // route the highlighted/typed PID
		default:
			var cmd tea.Cmd
			if m.appFocus == 0 {
				m.launchInput, cmd = m.launchInput.Update(msg)
			} else {
				m.procInput, cmd = m.procInput.Update(msg)
				m.procCursor = 0 // filter changed — reset the highlight
			}
			return m, cmd
		}
	}

	// Connections overlay: c/esc/q return; everything else scrolls the viewport.
	if m.showConns {
		switch {
		case key.Matches(msg, m.keys.Conns), msg.String() == "esc", msg.String() == "q":
			m.showConns = false
			return m, nil
		case msg.String() == "g":
			m.connVP.GotoTop()
			return m, nil
		case msg.String() == "G":
			m.connVP.GotoBottom()
			return m, nil
		default:
			var cmd tea.Cmd
			m.connVP, cmd = m.connVP.Update(msg)
			return m, cmd
		}
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
			// With keys already loaded, Enter ADDS another (failover); otherwise
			// it loads the first.
			if len(m.currentLinks) > 0 {
				m.status = "добавляю ключ…"
				return m, addLinkCmd(m.backend, link)
			}
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
			return m.openSection(secKeys)
		case key.Matches(msg, m.keys.Logs):
			return m.openSection(secLogs)
		case key.Matches(msg, m.keys.Conns):
			return m.openSection(secConns)
		case key.Matches(msg, m.keys.Proc):
			return m.openSection(secApps)
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case key.Matches(msg, m.keys.Next):
			m.focus = stepFocus(m.focus, +1, m.sectionCount())
			return m, nil
		case key.Matches(msg, m.keys.Prev):
			m.focus = stepFocus(m.focus, -1, m.sectionCount())
			return m, nil
		case key.Matches(msg, m.keys.Right):
			if m.focus < 0 {
				m.segCursor = (m.segCursor + 1) % 3
			} else {
				m.focus = stepFocus(m.focus, +1, m.sectionCount())
			}
			return m, nil
		case key.Matches(msg, m.keys.Left):
			if m.focus < 0 {
				m.segCursor = (m.segCursor + 2) % 3
			} else {
				m.focus = stepFocus(m.focus, -1, m.sectionCount())
			}
			return m, nil
		case key.Matches(msg, m.keys.Activate):
			if m.focus < 0 {
				return m.applyMode(RunMode(m.segCursor))
			}
			return m.openSection(m.focus)
		}
	}
	return m, nil
}

// dashboard section ring indices (the разделы chips, expandable to full screen).
const (
	secConns = iota
	secLogs
	secApps
	secKeys
)

// dashSectionLabels are the expandable sections shown as разделы chips.
var dashSectionLabels = []string{"Соединения", "Логи", "Приложения", "Ключи"}

func (m Model) sectionCount() int { return len(dashSectionLabels) }

// stepFocus advances the focus cursor over the ring [-1, 0, 1, …, n-1], where -1
// means the OFF/PROXY/VPN selector is focused.
func stepFocus(f, d, n int) int {
	total := n + 1
	idx := ((f+1+d)%total + total) % total
	return idx - 1
}

// openSection opens the full-screen view for a разделы chip (also used by the
// direct c/l/x/e shortcuts).
func (m Model) openSection(idx int) (tea.Model, tea.Cmd) {
	switch idx {
	case secConns:
		m.showConns = true
		return m, nil
	case secLogs:
		m.showLogs = true
		return m, tea.Batch(readLogsCmd(m.logPath), logsTick())
	case secApps:
		m.procInput.SetValue("")
		m.launchInput.SetValue("")
		m.appFocus = 0 // launch field focused first
		m.launchInput.Focus()
		m.procInput.Blur()
		m.showProc = true
		m.procCursor = 0
		m.errText = ""
		return m, tea.Batch(textinput.Blink, listProcessesCmd(m.backend))
	case secKeys:
		// Connection-strings screen: keys shown masked, field adds another (the
		// raw key is never echoed as the placeholder, so it stays secret).
		m.input.SetValue("")
		if len(m.currentLinks) > 0 {
			m.input.Placeholder = "vless://… (добавить ключ)"
		} else {
			m.input.Placeholder = "vless://..."
		}
		m.input.Focus()
		m.screen = ScreenLink
		m.errText = ""
		return m, textinput.Blink
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

// filteredProcs returns the process rows matching the current filter (name/PID).
func (m Model) filteredProcs() []ProcInfo {
	q := strings.TrimSpace(m.procInput.Value())
	if q == "" {
		return m.procRows
	}
	lq := strings.ToLower(q)
	var out []ProcInfo
	for _, p := range m.procRows {
		if strings.Contains(strings.ToLower(p.Name), lq) || strings.Contains(strconv.Itoa(p.PID), q) {
			out = append(out, p)
		}
	}
	return out
}

// submitProc routes the process selected in the picker: an explicit PID typed
// into the filter, otherwise the highlighted row.
func (m Model) submitProc() (tea.Model, tea.Cmd) {
	raw := strings.TrimSpace(m.procInput.Value())
	fp := m.filteredProcs()
	m.procInput.SetValue("")
	m.showProc = false

	if pid, err := strconv.Atoi(raw); err == nil && raw != "" {
		m.status = "проксирую процесс…"
		return m, routePIDCmd(m.backend, pid)
	}
	if len(fp) > 0 {
		m.status = "проксирую процесс…"
		return m, routePIDCmd(m.backend, fp[clampIdx(m.procCursor, len(fp))].PID)
	}
	return m, nil
}

func appendUnique(s []int, v int) []int {
	for _, x := range s {
		if x == v {
			return s
		}
	}
	return append(s, v)
}

func clampIdx(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}

func toPolicyMode(m RunMode) policy.Mode {
	if m == RunVPN {
		return policy.ModeVPN
	}
	return policy.ModeProxy
}
