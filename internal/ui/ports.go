// Package ui is the Bubble Tea terminal interface. Update is a pure reducer:
// it never performs I/O or embeds policy logic — it delegates decisions to the
// injected policy.Decide and drives the runtime through the Backend port via
// tea.Cmds. This keeps the whole UI unit-testable by feeding messages.
package ui

import "context"

// Backend is the runtime the UI drives. LoadLink validates and remembers a link
// WITHOUT starting anything; the user then explicitly enables a mode. Methods
// are blocking calls wrapped in tea.Cmds (see commands.go); results come back as
// messages.
type Backend interface {
	LoadLink(ctx context.Context, link string) error
	// AddLink appends another VLESS key to the set (failover) and reloads,
	// preserving the running mode.
	AddLink(ctx context.Context, link string) error
	// CurrentLinks returns the loaded VLESS links (priority order) for display.
	CurrentLinks() []string
	EnableProxy(ctx context.Context) error
	EnableVPN(ctx context.Context) error
	Stop(ctx context.Context) error
	// RoutePID routes an already-running process's traffic through the proxy
	// (Linux only; an error is surfaced on other platforms).
	RoutePID(ctx context.Context, pid int) error
	// LaunchProxied starts a command with its traffic routed through the proxy
	// and returns the child PID.
	LaunchProxied(ctx context.Context, argv []string) (int, error)
	// ListProcesses enumerates processes with network sockets for the picker.
	ListProcesses(ctx context.Context) ([]ProcInfo, error)
	// RestartProxied terminates a running PID and relaunches it through the
	// proxy (best-effort; the only way to proxy an existing process on macOS).
	RestartProxied(ctx context.Context, pid int) (int, error)
	// ApplySettings reloads the running core with edited settings (port, Clash
	// API, urltest), preserving the current mode.
	ApplySettings(ctx context.Context, s Settings) error
	// Daemonize re-execs a detached background process that keeps the proxy
	// running after this process exits.
	Daemonize(ctx context.Context) error
	// StopDaemon fully stops a running background instance (used by an attached
	// client). On a local run there is no separate daemon.
	StopDaemon(ctx context.Context) error
}
