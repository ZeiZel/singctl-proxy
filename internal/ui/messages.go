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

// internal command results.
type linkLoadedMsg struct{}
type proxyEnabledMsg struct{}
type vpnEnabledMsg struct{}
type stoppedMsg struct{}
type errMsg struct{ err error }
type logsMsg struct{ content string }
type logsTickMsg struct{}
