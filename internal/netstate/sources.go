// Package netstate passively determines whether Cisco Secure Client is active,
// using ONLY read-only OS observation (interface enumeration, routing table,
// process list). It NEVER invokes Cisco binaries and NEVER mutates state. All
// data sources are injected (CommandRunner) so the package is fully
// table-testable on captured fixtures.
package netstate

import "context"

// CommandRunner runs a read-only OS command and returns its stdout. The real
// implementation (osreal_darwin.go) maps logical names to absolute paths and
// shells out; tests inject a fake keyed on argv.
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// IfaceInfo is the parsed state of one interface from ifconfig.
type IfaceInfo struct {
	Name         string
	Up           bool
	PointToPoint bool
	NoARP        bool
	IPv4         []string
}

// RouteRow is one "default" row parsed from netstat -rn.
type RouteRow struct {
	Gateway     string
	Iface       string
	IsIPGateway bool // gateway is an IP literal (vs a "link#N" scope)
}
