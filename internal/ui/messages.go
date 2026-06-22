package ui

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
	note string
	err  error
	pid  int // PID now routed/launched (0 if none); tracked in routedPIDs
}

// ProcInfo is one process for the per-process routing picker.
type ProcInfo struct {
	PID   int
	Name  string
	Ports string // pre-rendered ":80 :443"
}

type procListMsg struct {
	rows []ProcInfo
	err  error
}

// linkAddedMsg is the result of adding a second key; links is the refreshed set.
type linkAddedMsg struct {
	links []string
}

type settingsAppliedMsg struct{ err error }
type daemonizedMsg struct{ err error }
type daemonStoppedMsg struct{ err error }
type frameMsg struct{} // ~25 fps animation tick (harmonica spring)
