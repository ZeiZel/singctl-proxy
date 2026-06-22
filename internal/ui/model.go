package ui

import (
	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/progress"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/harmonica"
	zone "github.com/lrstanley/bubblezone"

	"singctl/internal/policy"
)

// Screen is the current UI screen.
type Screen int

const (
	ScreenLink Screen = iota
	ScreenDashboard
)

// Model is the Bubble Tea model. All fields are unexported; tests in this
// package set them directly (white-box) and feed messages to Update.
type Model struct {
	screen       Screen
	mode         RunMode // user-chosen running state (OFF until they pick)
	cisco        bool
	phys         string
	width        int
	height       int
	input        textinput.Model
	modal        string
	status       string
	errText      string
	loaded       bool // a profile (link) has been loaded
	showLogs     bool
	logs         string
	logPath      string
	currentLink  string   // the loaded link, shown as the placeholder when editing
	currentLinks []string // all loaded keys (masked) shown on the link screen
	autoMode     RunMode  // mode to enable right after start (RunOff = none)

	showConns  bool         // connections overlay open
	conns      []ConnRow    // live connection table (from the Clash API poller)
	latency    []LatencyRow // per-server failover latencies
	latencySel string       // currently-selected server tag

	showProc    bool            // Приложения view open (launcher + picker)
	procInput   textinput.Model // process filter for the picker
	launchInput textinput.Model // "запустить приложение в прокси" field
	appFocus    int             // 0 = launch field, 1 = process filter
	procRows    []ProcInfo      // processes with network sockets (picker)
	procCursor  int             // highlighted row in the filtered list
	procErr     string          // process-list fetch error
	routedPIDs  []int           // PIDs currently routed/launched through the proxy

	showSettings bool         // Настройки section open
	settings     Settings     // last-applied settings (seeded from the CLI)
	setForm      settingsForm // editing state for the Настройки section

	attached    bool // driving a remote (already-running) instance over the control socket
	attachedPID int

	// presentation
	theme       Theme
	caps        Caps
	glyphs      Glyphs
	styles      Styles
	keys        keyMap
	help        help.Model
	spin        spinner.Model
	busy        bool             // an enable/stop command is in flight (drives the spinner)
	prog        progress.Model   // header activity gauge
	spring      harmonica.Spring // spring easing the gauge toward busy/idle
	animPos     float64          // current gauge fill (0..1)
	animVel     float64          // spring velocity
	vp          viewport.Model   // logs viewport (scroll)
	vpReady     bool
	connVP      viewport.Model // connections viewport (scroll, full list)
	connVPReady bool
	segCursor   int // keyboard cursor on the OFF|PROXY|VPN selector (0..2)
	focus       int // dashboard focus ring: -1 = mode selector, 0..n-1 = разделы chip

	backend Backend
	decide  func(policy.DecideInput) policy.DecisionResult
	notes   <-chan tea.Msg
	zm      *zone.Manager // bubblezone: mouse hit-testing
}

// New builds the initial model on the link-input screen. notes is the channel of
// async messages (NetStateMsg/StatusMsg) from the executor; it may be nil.
func New(backend Backend, notes <-chan tea.Msg) Model {
	caps := DetectCaps()
	return newWithCaps(backend, notes, caps)
}

// newWithCaps is the shared constructor; tests call it with deterministic caps.
func newWithCaps(backend Backend, notes <-chan tea.Msg, caps Caps) Model {
	th := DefaultTheme()
	gl := PickGlyphs(caps.Unicode)
	styles := NewStyles(caps, th, gl)

	ti := textinput.New()
	ti.Placeholder = "vless://..."
	ti.Prompt = gl.Prompt
	ti.PromptStyle = caps.R.NewStyle().Foreground(th.Accent)
	ti.PlaceholderStyle = caps.R.NewStyle().Foreground(th.Subtle)
	ti.Cursor.Style = caps.R.NewStyle().Foreground(th.Accent)
	ti.Focus()
	ti.Width = 48

	pi := textinput.New()
	pi.Placeholder = "фильтр по имени или PID"
	pi.Prompt = gl.Prompt
	pi.PromptStyle = caps.R.NewStyle().Foreground(th.Accent)
	pi.PlaceholderStyle = caps.R.NewStyle().Foreground(th.Subtle)
	pi.Cursor.Style = caps.R.NewStyle().Foreground(th.Accent)
	pi.Width = 48

	li := textinput.New()
	li.Placeholder = "напр.: zen  (Enter — запустить через прокси)"
	li.Prompt = gl.Prompt
	li.PromptStyle = caps.R.NewStyle().Foreground(th.Accent)
	li.PlaceholderStyle = caps.R.NewStyle().Foreground(th.Subtle)
	li.Cursor.Style = caps.R.NewStyle().Foreground(th.Accent)
	li.Width = 48

	keyStyle := caps.R.NewStyle().Foreground(th.Accent)
	descStyle := caps.R.NewStyle().Foreground(th.Muted)
	sepStyle := caps.R.NewStyle().Foreground(th.Subtle)
	hp := help.New()
	hp.Styles.ShortKey = keyStyle
	hp.Styles.FullKey = keyStyle
	hp.Styles.ShortDesc = descStyle
	hp.Styles.FullDesc = descStyle
	hp.Styles.ShortSeparator = sepStyle
	hp.Styles.FullSeparator = sepStyle
	hp.Styles.Ellipsis = sepStyle
	hp.ShortSeparator = "  " + gl.Sep + "  "
	hp.Ellipsis = gl.Ellipsis

	se := textinput.New()
	se.Prompt = gl.Prompt
	se.PromptStyle = caps.R.NewStyle().Foreground(th.Accent)
	se.Cursor.Style = caps.R.NewStyle().Foreground(th.Accent)
	se.Width = 40

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot // braille dots
	if !caps.Unicode {
		sp.Spinner = spinner.Line // ascii-safe |/-\ fallback
	}
	sp.Style = caps.R.NewStyle().Foreground(th.Accent)

	pr := progress.New(progress.WithSolidFill("#7AA2F7"), progress.WithoutPercentage())
	pr.Width = 16
	spring := harmonica.NewSpring(harmonica.FPS(25), 6.0, 0.5)

	return Model{
		screen:      ScreenLink,
		mode:        RunOff,
		focus:       -1, // selector focused by default
		input:       ti,
		procInput:   pi,
		launchInput: li,
		theme:       th,
		caps:        caps,
		glyphs:      gl,
		styles:      styles,
		keys:        defaultKeys(gl),
		help:        hp,
		spin:        sp,
		prog:        pr,
		spring:      spring,
		backend:     backend,
		decide:      policy.Decide,
		notes:       notes,
		setForm:     settingsForm{edit: se},
		zm:          zone.New(),
	}
}

// WithSettings seeds the Настройки section with the running configuration.
func (m Model) WithSettings(s Settings) Model {
	m.settings = s
	return m
}

// WithAttached marks the UI as driving a remote running instance (PID) over the
// control socket — shown as a header badge; changes the Настройки daemon action
// to "Остановить демон".
func (m Model) WithAttached(pid int) Model {
	m.attached = true
	m.attachedPID = pid
	return m
}

// WithDisplayMode sets the shown mode without issuing any command (used on
// attach to reflect the daemon's current mode).
func (m Model) WithDisplayMode(mode RunMode) Model {
	m.mode = mode
	m.segCursor = int(mode)
	return m
}

// WithLoadedProfile starts directly on the dashboard (mode OFF) — used when a
// saved link was loaded at startup, so the input screen is skipped.
func (m Model) WithLoadedProfile() Model {
	m.loaded = true
	m.screen = ScreenDashboard
	m.input.Blur()
	if m.status == "" {
		m.status = "ссылка загружена — выберите режим"
	}
	return m
}

// WithLogPath points the logs view at the sing-box log file.
func (m Model) WithLogPath(p string) Model {
	m.logPath = p
	return m
}

// WithCurrentLink remembers the loaded link so the edit screen can show it as
// the input placeholder.
func (m Model) WithCurrentLink(link string) Model {
	m.currentLink = link
	return m
}

// WithCurrentLinks records all loaded keys so the connection-strings screen can
// show them masked.
func (m Model) WithCurrentLinks(links []string) Model {
	m.currentLinks = links
	return m
}

// WithAutoMode enables the given mode immediately after start (the --proxy /
// --vpn flags). It requires a loaded profile; RunOff is a no-op.
func (m Model) WithAutoMode(mode RunMode) Model {
	m.autoMode = mode
	if mode != RunOff {
		m.busy = true
		m.segCursor = int(mode)
		m.status = "запуск " + mode.String() + "…"
	}
	return m
}

// WithLogsOpen starts with the logs view shown (the --logs flag).
func (m Model) WithLogsOpen() Model {
	m.showLogs = true
	return m
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{textinput.Blink, m.spin.Tick, frameCmd(), listen(m.notes)}
	switch m.autoMode {
	case RunProxy:
		cmds = append(cmds, enableProxyCmd(m.backend))
	case RunVPN:
		cmds = append(cmds, enableVPNCmd(m.backend))
	}
	if m.showLogs {
		cmds = append(cmds, readLogsCmd(m.logPath), logsTick())
	}
	return tea.Batch(cmds...)
}

// --- test/inspection accessors ---

func (m Model) Screen() Screen          { return m.screen }
func (m Model) Mode() RunMode           { return m.mode }
func (m Model) ModalShown() bool        { return m.modal != "" }
func (m Model) CiscoActive() bool       { return m.cisco }
func (m Model) Status() string          { return m.status }
func (m Model) ErrText() string         { return m.errText }
func (m Model) ShowingLogs() bool       { return m.showLogs }
func (m Model) Logs() string            { return m.logs }
func (m Model) ShowingConns() bool      { return m.showConns }
func (m Model) Conns() []ConnRow        { return m.conns }
func (m Model) Latency() []LatencyRow   { return m.latency }
func (m Model) LinkValue() string       { return m.input.Value() }
func (m Model) Busy() bool              { return m.busy }
func (m Model) SegCursor() int          { return m.segCursor }
func (m Model) Focus() int              { return m.focus }
func (m Model) RoutedPIDs() []int       { return m.routedPIDs }
func (m Model) ShowingSettings() bool   { return m.showSettings }
func (m Model) DraftSettings() Settings { return m.setForm.draft }
func (m Model) Attached() bool          { return m.attached }
