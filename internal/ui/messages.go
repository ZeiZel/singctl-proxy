package ui

import "fmt"

// RunMode is the user-facing running state shown on the dashboard. The user
// chooses it explicitly; nothing runs until they do.
type RunMode int

const (
	RunOff RunMode = iota
	RunProxy
	RunVPN
)

func (m RunMode) String() string {
	switch m {
	case RunProxy:
		return "PROXY"
	case RunVPN:
		return "VPN"
	default:
		return "OFF"
	}
}

// NetStateMsg and StatusMsg are pushed into the UI's notes channel by the
// executor/monitor (exported so other packages can construct them).

// NetStateMsg carries the passive Cisco/interface status for display.
type NetStateMsg struct {
	Cisco     bool
	PhysIface string
}

// StatusMsg reflects a runtime mode change or notification from the executor
// (e.g. an auto-suspend when Cisco appears, or a reconnect).
type StatusMsg struct {
	Mode RunMode
	Note string
}

// ConsoleMsg carries one line of stdout/stderr captured from a proxied app and
// pushed into the UI's notes channel by the executor/remote backend. The UI
// appends it to its console ring (see internal/ui/console.go); nothing is read
// back through the Backend so the reducer stays pure.
type ConsoleMsg struct {
	PID    int
	App    string
	Stream string // "stdout" / "stderr" / "exit"
	Text   string
}

// ActionMsg records a user-facing event the UI did not itself initiate (e.g. a
// Cisco auto-suspend) for the action log. Level is one of ActInfo/ActOk/
// ActWarn/ActErr (see internal/ui/actionlog.go).
type ActionMsg struct {
	Level int
	Text  string
}

// ConnRow is one live connection for the connections view (already formatted by
// the executor from the Clash API, so the UI stays decoupled from clashapi).
type ConnRow struct {
	Process string
	Source  string
	Dest    string
	Network string
	Chain   string
}

// ConnectionsMsg carries the current live connection table.
type ConnectionsMsg struct {
	Rows []ConnRow
}

// LatencyRow is one server's measured latency in the failover group.
type LatencyRow struct {
	Tag      string
	Delay    int // ms; 0 means timed out / unknown
	Selected bool
}

// LatencyMsg carries per-server latencies and the currently-selected server tag.
type LatencyMsg struct {
	Selected string
	Rows     []LatencyRow
}

// internal command results.
type linkLoadedMsg struct{ links []string }
type proxyEnabledMsg struct{}
type vpnEnabledMsg struct{}
type stoppedMsg struct{}
type errMsg struct{ err error }
type logsMsg struct{ content string }
type logsTickMsg struct{}
type procResultMsg struct {
	note   string
	err    error
	pid    int    // PID now routed/launched (0 if none)
	app    string // app name for the proxied list (launch/route)
	remove bool   // true → drop pid from the proxied list (unroute/kill)
}

// ProcInfo is one application for the per-process routing picker. Helper/forked
// processes are folded into their main app; Children is how many were folded.
type ProcInfo struct {
	PID      int
	Name     string
	Ports    string // pre-rendered ":80 :443"
	Children int    // helper processes folded under this app (0 = standalone)
}

// Label is the picker display name: the app name plus a "+N" badge when helper
// processes are folded under it (so the user sees the swarm is collapsed).
func (p ProcInfo) Label() string {
	if p.Children > 0 {
		return fmt.Sprintf("%s (+%d)", p.Name, p.Children)
	}
	return p.Name
}

type procListMsg struct {
	rows []ProcInfo
	err  error
}

// proxiedApp is one application currently routed/launched through the proxy,
// shown in the "Проксируются сейчас" list with per-app unroute/kill actions.
type proxiedApp struct {
	PID  int
	Name string
}

// proxiedLabel renders a proxied app as "name (PID)" (or just the PID).
func proxiedLabel(a proxiedApp) string {
	if a.Name != "" {
		return fmt.Sprintf("%s (PID %d)", a.Name, a.PID)
	}
	return fmt.Sprintf("PID %d", a.PID)
}

// modalKind selects how the popup behaves: an info card dismissed by any key, or
// a confirm card with [Да]/[Нет].
type modalKind int

const (
	modalInfo modalKind = iota
	modalConfirm
)

// pendingKind is the action a confirm modal runs when accepted.
type pendingKind int

const (
	pendNone pendingKind = iota
	pendDeleteKey
	pendKillApp
)

// pendingAction carries the parameters of a deferred confirm action.
type pendingAction struct {
	kind  pendingKind
	index int // key index (pendDeleteKey)
	pid   int // app PID (pendKillApp)
}

// keyInputMode is the sub-mode of the Ключи top input.
type keyInputMode int

const (
	keyModeAdd    keyInputMode = iota // typing a new vless:// link to add
	keyModeRename                     // typing a new name for the focused key
	keyModeEdit                       // editing the focused key's raw link (replace)
)

// appBusyMsg toggles the "proxying in progress" loader (set when a launch/route
// is dispatched, cleared when its result arrives).
type appBusyMsg struct{ busy bool }

// linkAddedMsg is the result of adding a second key; links is the refreshed set.
type linkAddedMsg struct {
	links []string
}

type settingsAppliedMsg struct{ err error }
type daemonizedMsg struct{ err error }
type daemonStoppedMsg struct{ err error }
type frameMsg struct{} // ~25 fps animation tick (harmonica spring)
