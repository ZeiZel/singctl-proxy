// Package notify holds the small set of message and wire types shared between
// the local Executor, the remote attach client, and singctl's headless mode.
// These used to live in the (now-removed) internal/ui Bubble Tea package;
// headless mode still needs RunMode/StatusMsg to print status notes to
// stdout, and the control socket still exchanges Settings/ProcInfo as JSON
// (SETTINGS-GET/SET, PROC-LIST) — see cmd/singctl/main.go's registerControl.
package notify

import "fmt"

// RunMode is the user-facing running state (OFF/PROXY/VPN).
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

// Action-log levels for ActionMsg.
const (
	ActInfo = iota
	ActOk
	ActWarn
	ActErr
)

// NetStateMsg carries the passive Cisco/interface status for display.
type NetStateMsg struct {
	Cisco     bool
	PhysIface string
	// Bypass is true when Cisco is active AND the proxy egress is pinned to the
	// physical NIC to bypass it (the coexistence mode); false means the proxy is
	// riding Cisco (no Cisco, or the fallback).
	Bypass bool
	// NetextAvailable reports whether the macOS transparent-proxy system
	// extension is installed and approved. Always false off darwin.
	NetextAvailable bool
}

// StatusMsg reflects a runtime mode change or notification from the executor
// (e.g. an auto-suspend when Cisco appears, or a reconnect). Headless mode
// prints Note to stdout when non-empty (see cmd/singctl/main.go's runHeadless).
type StatusMsg struct {
	Mode RunMode
	Note string
}

// ConsoleMsg carries one line of stdout/stderr captured from a proxied app.
type ConsoleMsg struct {
	PID    int
	App    string
	Stream string // "stdout" / "stderr" / "exit"
	Text   string
}

// ActionMsg records a user-facing event that was not itself initiated by the
// caller (e.g. a Cisco auto-suspend). Level is one of ActInfo/ActOk/ActWarn/ActErr.
type ActionMsg struct {
	Level int
	Text  string
}

// ConnRow is one live connection, already formatted from the Clash API.
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

// Settings is the user-editable runtime configuration. It is the JSON wire
// format for the control socket's SETTINGS-GET/SET commands (field names
// matter — gui/bridge.Settings mirrors them exactly).
type Settings struct {
	SocksPort        int
	ClashEnabled     bool
	ClashAddr        string
	URLTestURL       string
	URLTestInterval  string
	URLTestTolerance int
	SaveProfile      bool
	// AutostartMode is the persisted mode ("off"|"proxy"|"vpn", default "off")
	// the daemon applies to itself, best-effort, once it is otherwise up (F2
	// item 2) — this is what a fresh install's LaunchDaemon now relies on
	// instead of a hardcoded --vpn flag in its plist.
	AutostartMode string
}

// ProcInfo is one application for the per-process routing picker/wire format
// (PROC-LIST). Helper/forked processes are folded into their main app;
// Children is how many were folded.
type ProcInfo struct {
	PID      int
	Name     string
	Ports    string // pre-rendered ":80 :443"
	Children int    // helper processes folded under this app (0 = standalone)
}

// Label renders the process for a text picker: the app name plus a "+N" badge
// when helper processes are folded under it.
func (p ProcInfo) Label() string {
	if p.Children > 0 {
		return fmt.Sprintf("%s (+%d)", p.Name, p.Children)
	}
	return p.Name
}
