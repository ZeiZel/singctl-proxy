package ui

import (
	"fmt"
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

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case frameMsg:
		// Spring the header activity gauge toward 1 while busy, 0 when idle.
		target := 0.0
		if m.busy {
			target = 1.0
		}
		m.animPos, m.animVel = m.spring.Update(m.animPos, m.animVel, target)
		return m, frameCmd()

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

	case ConsoleMsg:
		m.console.append(consoleEntry{PID: msg.PID, App: msg.App, Stream: msg.Stream, Text: msg.Text})
		m.refreshConsoleViewport()
		return m, listen(m.notes)

	case ActionMsg:
		m.actions.add(msg.Level, msg.Text)
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
		m.keyReveal = false
		if m.keyCursor >= len(m.currentLinks) {
			m.keyCursor = max(len(m.currentLinks)-1, 0)
		}
		if len(m.currentLinks) == 0 { // last key removed → focus the input
			m.keyFocus = 0
			m.input.Focus()
		}
		m.status = "ключи обновлены (" + strconv.Itoa(len(msg.links)) + " всего)"
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
		m.actions.add(ActOk, "PROXY запущен")
		return m, nil

	case vpnEnabledMsg:
		m.mode = RunVPN
		m.busy = false
		m.errText = ""
		m.segCursor = int(RunVPN)
		m.status = "VPN запущен"
		m.actions.add(ActOk, "VPN запущен")
		return m, nil

	case stoppedMsg:
		m.mode = RunOff
		m.busy = false
		m.errText = ""
		m.segCursor = int(RunOff)
		m.status = "остановлено"
		m.actions.add(ActOk, "остановлено")
		return m, nil

	case errMsg:
		m.busy = false
		m.status = "" // drop the stale "…" activity line; show only the error
		m.errText = msg.err.Error()
		m.actions.add(ActErr, msg.err.Error())
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
		m.procBusy = false
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = ""
			m.actions.add(ActErr, msg.err.Error())
		} else {
			m.errText = ""
			m.status = msg.note
			m.actions.add(ActOk, msg.note)
			switch {
			case msg.remove && msg.pid != 0:
				m.proxied = removeProxied(m.proxied, msg.pid)
				if m.proxiedCur >= len(m.proxied) {
					m.proxiedCur = max(len(m.proxied)-1, 0)
				}
			case msg.pid > 0:
				m.proxied = upsertProxied(m.proxied, proxiedApp{PID: msg.pid, Name: msg.app})
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

	case settingsAppliedMsg:
		m.busy = false
		if msg.err != nil {
			m.errText = msg.err.Error()
			m.status = ""
		} else {
			m.errText = ""
			m.status = "настройки применены"
		}
		return m, nil

	case daemonizedMsg:
		if msg.err != nil {
			m.busy = false
			m.errText = msg.err.Error()
			m.status = ""
			return m, nil
		}
		// The detached child now owns the proxy; quit the TUI (deferred Shutdown
		// stops this process's cores).
		return m, tea.Quit

	case daemonStoppedMsg:
		if msg.err != nil {
			m.busy = false
			m.errText = msg.err.Error()
			m.status = ""
			return m, nil
		}
		// The background instance is gone; nothing left to control — quit.
		return m, tea.Quit
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
	content := wrap(m.styleLogText(tailLines(m.logs, 2000)), max(m.vp.Width, 1))
	if strings.TrimSpace(content) == "" {
		content = m.styles.Subtle.Render("(логи появятся после запуска режима)")
	}
	m.vp.SetContent(content)
	if atBottom {
		m.vp.GotoBottom()
	}
}

// handleMouse routes mouse events: wheel scrolls the active scrollable overlay;
// a left click on a bubblezone-marked region (разделы chip, mode segment, picker
// row, settings row) acts on it.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	// Wheel: forward to the active viewport (it handles scrolling natively).
	if msg.Button == tea.MouseButtonWheelUp || msg.Button == tea.MouseButtonWheelDown {
		var cmd tea.Cmd
		switch {
		case m.showLogs:
			m.vp, cmd = m.vp.Update(msg)
		case m.showConns:
			m.connVP, cmd = m.connVP.Update(msg)
		case m.showConsole && m.consoleVPReady:
			m.consoleVP, cmd = m.consoleVP.Update(msg)
		}
		return m, cmd
	}
	if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
		return m, nil
	}
	if m.modal != "" { // click anywhere dismisses (cancels a confirm)
		m.modal = ""
		m.pending = pendingAction{}
		return m, nil
	}
	switch {
	case m.showProc:
		for i := range m.filteredProcs() {
			if m.zm.Get(zoneProc(i)).InBounds(msg) {
				m.appFocus = 1
				m.procInput.Focus()
				m.launchInput.Blur()
				m.procCursor = i
				return m, nil
			}
		}
		for i := range m.proxied {
			if m.zm.Get(zoneProxied(i)).InBounds(msg) {
				m.appFocus = 2
				m.procInput.Blur()
				m.launchInput.Blur()
				m.proxiedCur = i
				return m, nil
			}
		}
	case m.screen == ScreenLink:
		for i := range m.currentLinks {
			if m.zm.Get(zoneKey(i)).InBounds(msg) {
				m.keyFocus = 1
				m.input.Blur()
				m.keyCursor = i
				return m, nil
			}
		}
	case m.showSettings:
		for i := range m.settingsFieldsFor() {
			if m.zm.Get(zoneSetting(i)).InBounds(msg) {
				m.setForm.focus = i
				m.setForm.editing = false
				return m, nil
			}
		}
	case m.showConsole:
		// Per-app filter chips isolate one app's output.
		if m.zm.Get(zoneConPID(0)).InBounds(msg) {
			m.consoleFilter = 0
			m.refreshConsoleViewport()
			return m, nil
		}
		for _, pid := range m.console.pids() {
			if m.zm.Get(zoneConPID(pid)).InBounds(msg) {
				m.consoleFilter = pid
				m.refreshConsoleViewport()
				return m, nil
			}
		}
	case !m.showLogs && !m.showConns && m.screen == ScreenDashboard:
		// OFF/PROXY/VPN selector segments.
		for i := 0; i < 3; i++ {
			if m.zm.Get(zoneMode(i)).InBounds(msg) {
				return m.applyMode(RunMode(i))
			}
		}
		// Sidebar nav: click an already-selected item to open it; click another
		// to select it (a second click then opens — mirrors the keyboard).
		for i := 0; i < navCount; i++ {
			if m.zm.Get(zoneNav(i)).InBounds(msg) {
				if i == m.section {
					return m.openNav(i)
				}
				m.section = i
				return m, nil
			}
		}
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// A shown modal captures the next keypress. An info modal dismisses on any
	// key; a confirm modal runs its pending action on Да (y/Enter) and cancels on
	// Нет (n/Esc). This stays first so any key clears it before other handling.
	if m.modal != "" {
		if m.modalKind == modalConfirm {
			switch msg.String() {
			case "y", "д", "enter":
				return m.runPending()
			default: // n / esc / anything → cancel
				m.modal = ""
				m.pending = pendingAction{}
				return m, nil
			}
		}
		m.modal = ""
		return m, nil
	}
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	// Приложения view: three focus zones — 0 launch field, 1 picker (filter +
	// list), 2 the proxied-apps list. Tab cycles them; ↑/↓ drive the focused zone;
	// esc closes.
	if m.showProc {
		switch {
		case msg.String() == "esc":
			m.showProc = false
			m.launchInput.Blur()
			m.procInput.Blur()
			return m, nil
		case msg.Type == tea.KeyTab:
			m.appFocus = (m.appFocus + 1) % 3
			m.syncAppFocus()
			return m, textinput.Blink
		case msg.Type == tea.KeyShiftTab:
			m.appFocus = (m.appFocus + 2) % 3
			m.syncAppFocus()
			return m, textinput.Blink
		case msg.String() == "up":
			if m.appFocus == 2 {
				if m.proxiedCur > 0 {
					m.proxiedCur--
				}
			} else if m.procCursor > 0 {
				m.procCursor--
			}
			return m, nil
		case msg.String() == "down":
			if m.appFocus == 2 {
				if m.proxiedCur < len(m.proxied)-1 {
					m.proxiedCur++
				}
			} else if n := len(m.filteredProcs()); n > 0 && m.procCursor < n-1 {
				m.procCursor++
			}
			return m, nil
		case msg.String() == "u" && m.appFocus == 2:
			// Unroute the selected proxied app (Linux: clean detach; macOS: kill).
			if pid := m.selectedProxiedPID(); pid != 0 {
				m.procBusy = true
				m.status = "отключаю проксирование…"
				return m, unroutePIDCmd(m.backend, pid)
			}
			return m, nil
		case (msg.String() == "k" || msg.Type == tea.KeyCtrlR) && m.appFocus == 2:
			// Kill the selected proxied app — confirm first.
			if pid := m.selectedProxiedPID(); pid != 0 {
				m.modal = fmt.Sprintf("Завершить приложение (PID %d)? Процесс будет остановлен.", pid)
				m.modalKind = modalConfirm
				m.pending = pendingAction{kind: pendKillApp, pid: pid}
			}
			return m, nil
		case msg.Type == tea.KeyCtrlR && m.appFocus != 2:
			// Restart the highlighted picker process in proxy mode (best-effort;
			// the only way to proxy an existing process on macOS).
			fp := m.filteredProcs()
			if len(fp) == 0 {
				return m, nil
			}
			row := fp[clampIdx(m.procCursor, len(fp))]
			m.procBusy = true
			m.status = "перезапускаю процесс в proxy-режиме…"
			return m, restartPIDCmd(m.backend, row.PID, row.Name)
		case msg.String() == "enter":
			if m.appFocus == 0 {
				if v := strings.TrimSpace(m.launchInput.Value()); v != "" {
					m.launchInput.SetValue("")
					m.procBusy = true
					m.status = "запускаю приложение через прокси…"
					return m, launchProcCmd(m.backend, strings.Fields(v))
				}
				// empty launch field → fall through and route the highlighted app
			}
			if m.appFocus == 2 {
				return m, nil // the proxied list acts via u/k, not Enter
			}
			return m.submitProc() // route the highlighted/typed PID
		default:
			var cmd tea.Cmd
			switch m.appFocus {
			case 0:
				m.launchInput, cmd = m.launchInput.Update(msg)
			case 1:
				m.procInput, cmd = m.procInput.Update(msg)
				m.procCursor = 0 // filter changed — reset the highlight
			}
			return m, cmd
		}
	}

	// Console overlay (Консоль приложений): esc/q return; g/G jump; else scroll.
	if m.showConsole {
		switch {
		case msg.String() == "esc", msg.String() == "q":
			m.showConsole = false
			return m, nil
		case msg.String() == "g":
			if m.consoleVPReady {
				m.consoleVP.GotoTop()
			}
			return m, nil
		case msg.String() == "G":
			if m.consoleVPReady {
				m.consoleVP.GotoBottom()
			}
			return m, nil
		default:
			if m.consoleVPReady {
				var cmd tea.Cmd
				m.consoleVP, cmd = m.consoleVP.Update(msg)
				return m, cmd
			}
			return m, nil
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

	// Настройки section: edit fields, toggle, apply / daemonize.
	if m.showSettings {
		return m.handleSettingsKey(msg)
	}

	switch m.screen {
	case ScreenLink:
		return m.handleKeysKey(msg)

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
			return m.openNav(navKeys)
		case key.Matches(msg, m.keys.Logs):
			return m.openNav(navLogs)
		case key.Matches(msg, m.keys.Conns):
			return m.openNav(navConns)
		case key.Matches(msg, m.keys.Proc):
			return m.openNav(navApps)
		case key.Matches(msg, m.keys.Settings):
			return m.openNav(navSettings)
		case key.Matches(msg, m.keys.Expand): // 'o' → console
			return m.openNav(navConsole)
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			return m, nil
		case key.Matches(msg, m.keys.Jump):
			n := int(msg.Runes[0] - '1') // '1'..'7' → 0..6
			if n >= 0 && n < navCount {
				return m.openNav(n)
			}
			return m, nil
		case key.Matches(msg, m.keys.Down), key.Matches(msg, m.keys.Next):
			m.section = stepNav(m.section, +1)
			return m, nil
		case key.Matches(msg, m.keys.Up), key.Matches(msg, m.keys.Prev):
			m.section = stepNav(m.section, -1)
			return m, nil
		case key.Matches(msg, m.keys.Right):
			// On the Режим home, → advances the OFF/PROXY/VPN cursor; on any other
			// section, → opens it (like Enter).
			if m.section == navMode {
				m.segCursor = (m.segCursor + 1) % 3
				return m, nil
			}
			return m.openNav(m.section)
		case key.Matches(msg, m.keys.Left):
			if m.section == navMode {
				m.segCursor = (m.segCursor + 2) % 3
			}
			return m, nil
		case key.Matches(msg, m.keys.Activate):
			if m.section == navMode {
				return m.applyMode(RunMode(m.segCursor))
			}
			return m.openNav(m.section)
		}
	}
	return m, nil
}

// dashboard section indices, reused by openSection and the nav→section mapping.
const (
	secConns = iota
	secLogs
	secApps
	secKeys
	secSettings
)

// openSection opens the full-screen overlay for a section (also used by openNav
// and the direct c/l/x/e/g shortcuts).
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
		m.proxiedCur = 0
		m.errText = ""
		return m, tea.Batch(textinput.Blink, listProcessesCmd(m.backend))
	case secKeys:
		// Keys manager: a top add field + a navigable masked list (the raw key is
		// never echoed as the placeholder, so it stays secret).
		m.input.SetValue("")
		if len(m.currentLinks) > 0 {
			m.input.Placeholder = "vless://… (добавить ключ)"
		} else {
			m.input.Placeholder = "vless://..."
		}
		m.keyFocus = 0
		m.keyMode = keyModeAdd
		m.keyCursor = 0
		m.keyReveal = false
		m.input.Focus()
		m.screen = ScreenLink
		m.errText = ""
		return m, textinput.Blink
	case secSettings:
		m.setForm.focus = 0
		m.setForm.editing = false
		m.setForm.draft = m.settings // edit a working copy
		m.setForm.edit.Blur()
		m.showSettings = true
		m.errText = ""
		return m, nil
	}
	return m, nil
}

// handleKeysKey drives the Ключи screen: a top input (add / rename / edit) and a
// navigable list of loaded keys. keyFocus 0 = input, 1 = list. List actions:
// Enter reveal, n rename, e edit, d delete (confirm).
func (m Model) handleKeysKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.keyFocus == 1 { // the keys list
		switch msg.String() {
		case "esc", "q":
			if m.loaded {
				m.screen = ScreenDashboard
				return m, nil
			}
			return m, tea.Quit
		case "tab", "shift+tab":
			m.keyFocus = 0
			m.input.Focus()
			return m, textinput.Blink
		case "up", "k":
			if m.keyCursor > 0 {
				m.keyCursor--
			} else {
				m.keyFocus = 0
				m.input.Focus()
				return m, textinput.Blink
			}
			return m, nil
		case "down", "j":
			if m.keyCursor < len(m.currentLinks)-1 {
				m.keyCursor++
			}
			return m, nil
		case "enter", " ":
			m.keyReveal = !m.keyReveal
			return m, nil
		case "n": // rename / add name
			if m.keyCursor < len(m.currentLinks) {
				m.keyMode = keyModeRename
				m.keyEditIndex = m.keyCursor
				m.keyFocus = 0
				m.input.SetValue(linkName(m.currentLinks[m.keyCursor]))
				m.input.CursorEnd()
				m.input.Focus()
				return m, textinput.Blink
			}
			return m, nil
		case "e": // edit the raw link
			if m.keyCursor < len(m.currentLinks) {
				m.keyMode = keyModeEdit
				m.keyEditIndex = m.keyCursor
				m.keyFocus = 0
				m.input.SetValue(m.currentLinks[m.keyCursor])
				m.input.CursorEnd()
				m.input.Focus()
				return m, textinput.Blink
			}
			return m, nil
		case "d": // delete (confirm)
			if m.keyCursor < len(m.currentLinks) {
				m.modal = fmt.Sprintf("Удалить ключ %d? Это действие необратимо.", m.keyCursor+1)
				m.modalKind = modalConfirm
				m.pending = pendingAction{kind: pendDeleteKey, index: m.keyCursor}
			}
			return m, nil
		}
		return m, nil
	}

	// keyFocus 0: the top input.
	switch msg.String() {
	case "esc":
		if m.keyMode != keyModeAdd { // cancel rename/edit back to add
			m.keyMode = keyModeAdd
			m.input.SetValue("")
			return m, nil
		}
		if m.loaded {
			m.screen = ScreenDashboard
			m.input.Blur()
			return m, nil
		}
		return m, tea.Quit
	case "tab", "down":
		if len(m.currentLinks) > 0 {
			m.keyFocus = 1
			m.input.Blur()
		}
		return m, nil
	case "enter":
		val := strings.TrimSpace(m.input.Value())
		switch m.keyMode {
		case keyModeRename:
			idx := m.keyEditIndex
			m.keyMode = keyModeAdd
			m.input.SetValue("")
			m.status = "переименовываю ключ…"
			return m, renameLinkCmd(m.backend, idx, val)
		case keyModeEdit:
			if val == "" {
				m.errText = "введите vless:// ссылку"
				return m, nil
			}
			idx := m.keyEditIndex
			m.keyMode = keyModeAdd
			m.input.SetValue("")
			m.busy = true
			m.status = "сохраняю ключ…"
			return m, replaceLinkCmd(m.backend, idx, val)
		default: // keyModeAdd
			if val == "" {
				m.errText = "введите vless:// ссылку"
				return m, nil
			}
			m.errText = ""
			m.busy = true
			if len(m.currentLinks) > 0 {
				m.status = "добавляю ключ…"
				return m, addLinkCmd(m.backend, val)
			}
			m.status = "загрузка ссылки…"
			return m, loadLinkCmd(m.backend, val)
		}
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// handleSettingsKey drives the Настройки section: ↑/↓ move focus, Enter toggles
// a boolean / edits a value / triggers an action; while editing, Enter commits
// and Esc cancels.
func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	f := &m.setForm
	if f.editing {
		switch msg.String() {
		case "enter":
			f.draft.setField(f.focus, f.edit.Value())
			f.editing = false
			f.edit.Blur()
			return m, nil
		case "esc":
			f.editing = false
			f.edit.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			f.edit, cmd = f.edit.Update(msg)
			return m, cmd
		}
	}

	fields := m.settingsFieldsFor()
	switch msg.String() {
	case "esc", "q":
		m.showSettings = false
		return m, nil
	case "up", "k":
		if f.focus > 0 {
			f.focus--
		}
		return m, nil
	case "down", "j":
		if f.focus < len(fields)-1 {
			f.focus++
		}
		return m, nil
	case "enter", " ":
		fld := fields[clampIdx(f.focus, len(fields))]
		switch fld.kind {
		case sfToggle:
			f.draft.toggle(f.focus)
			return m, nil
		case sfInt, sfText:
			f.editing = true
			f.edit.SetValue(f.draft.editable(f.focus))
			f.edit.CursorEnd()
			f.edit.Focus()
			return m, textinput.Blink
		case sfAction:
			switch fld.action {
			case "daemon":
				m.busy = true
				m.status = "запуск в фоне…"
				return m, daemonizeCmd(m.backend)
			case "stopdaemon":
				m.busy = true
				m.status = "останавливаю демон…"
				return m, stopDaemonCmd(m.backend)
			default: // apply
				m.settings = f.draft
				m.showSettings = false
				m.busy = true
				m.status = "применяю настройки…"
				return m, applySettingsCmd(m.backend, f.draft)
			}
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
		m.actions.add(ActInfo, "переключение в PROXY…")
		return m, enableProxyCmd(m.backend)
	case RunVPN:
		m.actions.add(ActInfo, "переключение в VPN…")
		return m.requestVPN()
	default: // RunOff
		m.busy = true
		m.status = "остановка…"
		m.actions.add(ActInfo, "остановка…")
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
			m.modalKind = modalInfo // dismiss on any key (not a confirm)
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

	if pid, err := strconv.Atoi(raw); err == nil && pid > 0 {
		m.procBusy = true
		m.status = "проксирую процесс…"
		return m, routePIDCmd(m.backend, pid, "PID "+raw)
	}
	if len(fp) > 0 {
		row := fp[clampIdx(m.procCursor, len(fp))]
		m.procBusy = true
		m.status = "проксирую процесс…"
		return m, routePIDCmd(m.backend, row.PID, row.Name)
	}
	return m, nil
}

// runPending executes the action a confirm modal was guarding, then clears it.
func (m Model) runPending() (tea.Model, tea.Cmd) {
	p := m.pending
	m.modal = ""
	m.pending = pendingAction{}
	switch p.kind {
	case pendDeleteKey:
		m.status = "удаляю ключ…"
		if m.keyCursor >= p.index && m.keyCursor > 0 {
			m.keyCursor--
		}
		m.keyReveal = false
		return m, deleteLinkCmd(m.backend, p.index)
	case pendKillApp:
		m.procBusy = true
		m.status = "завершаю приложение…"
		return m, stopProxiedCmd(m.backend, p.pid)
	}
	return m, nil
}

// syncAppFocus points the textinputs at the focused zone (launch/picker), and
// blurs both when the proxied-apps list (zone 2) is focused.
func (m *Model) syncAppFocus() {
	switch m.appFocus {
	case 0:
		m.launchInput.Focus()
		m.procInput.Blur()
	case 1:
		m.procInput.Focus()
		m.launchInput.Blur()
	default:
		m.launchInput.Blur()
		m.procInput.Blur()
	}
}

// selectedProxiedPID is the PID of the highlighted proxied app (0 if none).
func (m Model) selectedProxiedPID() int {
	if len(m.proxied) == 0 {
		return 0
	}
	return m.proxied[clampIdx(m.proxiedCur, len(m.proxied))].PID
}

// upsertProxied adds or updates an app in the proxied list (dedup by PID).
func upsertProxied(s []proxiedApp, a proxiedApp) []proxiedApp {
	for i, x := range s {
		if x.PID == a.PID {
			if a.Name != "" {
				s[i].Name = a.Name
			}
			return s
		}
	}
	return append(s, a)
}

// removeProxied drops the app with pid from the proxied list.
func removeProxied(s []proxiedApp, pid int) []proxiedApp {
	out := s[:0]
	for _, x := range s {
		if x.PID != pid {
			out = append(out, x)
		}
	}
	return out
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
