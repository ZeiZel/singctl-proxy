// Package procproxy routes the traffic of selected processes through the local
// proxy. On Linux it does real per-PID interception with cgroup v2 + nftables
// fwmark policy routing (no env needed, works for already-running PIDs). On other
// platforms it falls back to launching a child with proxy environment variables
// injected (env-aware apps only, launched processes only). The privileged Linux
// paths shell out to nft/ip/cgroupfs (mirroring the codebase's netstate style);
// the pure argv/env builders are unit-tested.
package procproxy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Router routes selected processes through the proxy. Implementations are
// platform-specific (see NewRouter). On Linux AddPID does real per-PID
// interception; off Linux it restarts the process in proxy mode (env injection).
type Router interface {
	// AddPID routes an already-running process's traffic through the proxy
	// (Linux), or restarts it in proxy mode (other platforms).
	AddPID(ctx context.Context, pid int) error
	// RemovePID stops routing a process.
	RemovePID(ctx context.Context, pid int) error
	// Unroute stops proxying a process the way the platform allows: on Linux it
	// removes the PID from the cgroup (the process keeps running, direct); on
	// platforms where proxying is env-injection (macOS) it can't be undone, so it
	// terminates the process instead.
	Unroute(ctx context.Context, pid int) error
	// Kill terminates a proxied process (best-effort SIGTERM).
	Kill(ctx context.Context, pid int) error
	// Launch starts argv with its traffic routed through the proxy and returns
	// the child PID.
	Launch(ctx context.Context, argv []string) (int, error)
	// RestartPID reads a running process's command line, terminates it, and
	// relaunches it routed through the proxy. Best-effort: argv is recovered via
	// ps (quoting is not preserved), so it suits simple CLI apps. Returns the new
	// PID.
	RestartPID(ctx context.Context, pid int) (int, error)
	// ListRouted returns the PIDs currently routed.
	ListRouted() []int
	// Cleanup removes any kernel/process state created by the router.
	Cleanup() error
}

// BundleRouter is an optional capability implemented by routers that track
// routed PIDs by application bundle ID (currently only darwinRouter — bundle
// IDs are a macOS/system-extension concept; see router_darwin.go). Callers
// type-assert a Router to this interface to drive whole-app actions (route/
// unroute every PID of one app) without duplicating routing logic: they still
// call the plain AddPID/Unroute for each PID this interface reports.
type BundleRouter interface {
	// PIDsForBundle returns the routed PIDs currently attributed to bundleID.
	PIDsForBundle(bundleID string) []int
	// RoutedBundleIDs returns the bundle IDs with at least one routed PID, sorted.
	RoutedBundleIDs() []string
}

// Config tunes the router. The zero value is filled with defaults by withDefaults.
type Config struct {
	SocksAddr  string // local SOCKS proxy "host:port"
	HTTPAddr   string // local HTTP proxy "host:port"
	Gateway    string // Linux: nexthop the marked traffic is routed via (the TUN address)
	Mark       int    // Linux: fwmark for the routed cgroup
	Table      int    // Linux: policy-routing table id
	CgroupRoot string // Linux: cgroup v2 mount (default /sys/fs/cgroup)
	CgroupName string // Linux: cgroup leaf name

	// LaunchUser, when non-nil, is the unprivileged user that launched children
	// should run as. singctl runs as root (sudo) to manage the TUN/routes, but
	// GUI apps (e.g. Zen) refuse to run as root in a user's session, so when this
	// process is root we drop the child to this user and fix its HOME/USER env.
	// Ignored when the current process is not root (the child already runs as the
	// invoking user) or when Uid is 0.
	LaunchUser *LaunchUser

	// Output, when non-nil, captures launched children's stdout/stderr line by
	// line (instead of inheriting singctl's stdio) so a TUI can stream each
	// proxied app's own logs. When nil the child inherits the current process's
	// stdio (headless --launch is unchanged).
	Output OutputSink
}

// LaunchUser is the real (non-root) user a launched child should run as.
type LaunchUser struct {
	Uid  int
	Gid  int
	Name string
	Home string
}

// sudoEnvVars are the sudo bookkeeping variables stripped from a child's
// environment when we drop privileges, so the app sees a clean user session.
var sudoEnvVars = []string{"SUDO_USER", "SUDO_UID", "SUDO_GID", "SUDO_COMMAND"}

// applyUserEnv rewrites env so a child launched as u sees a coherent user
// session: HOME/USER/LOGNAME point at u and sudo's bookkeeping vars are removed.
// Pure; existing assignments are overwritten in place (order preserved).
func applyUserEnv(env []string, u *LaunchUser) []string {
	if u == nil {
		return env
	}
	overrides := map[string]string{
		"HOME":    u.Home,
		"USER":    u.Name,
		"LOGNAME": u.Name,
	}
	out := make([]string, 0, len(env)+len(overrides))
	seen := map[string]bool{}
next:
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		for _, drop := range sudoEnvVars {
			if key == drop {
				continue next
			}
		}
		if v, ok := overrides[key]; ok {
			out = append(out, key+"="+v)
			seen[key] = true
			continue
		}
		out = append(out, kv)
	}
	for key, v := range overrides {
		if !seen[key] && v != "" {
			out = append(out, key+"="+v)
		}
	}
	return out
}

// Default values mirror the proxy's historical ports and the forwarder TUN
// address (198.18.0.1/30, decision D7).
const (
	defaultSocksAddr  = "127.0.0.1:1080"
	defaultHTTPAddr   = "127.0.0.1:2080"
	defaultGateway    = "198.18.0.1"
	defaultMark       = 0x1c9 // 457
	defaultTable      = 0x1c9
	defaultCgroupRoot = "/sys/fs/cgroup"
	defaultCgroupName = "singctl-proxy"
)

func (c Config) withDefaults() Config {
	if c.SocksAddr == "" {
		c.SocksAddr = defaultSocksAddr
	}
	if c.HTTPAddr == "" {
		c.HTTPAddr = defaultHTTPAddr
	}
	if c.Gateway == "" {
		c.Gateway = defaultGateway
	}
	if c.Mark == 0 {
		c.Mark = defaultMark
	}
	if c.Table == 0 {
		c.Table = defaultTable
	}
	if c.CgroupRoot == "" {
		c.CgroupRoot = defaultCgroupRoot
	}
	if c.CgroupName == "" {
		c.CgroupName = defaultCgroupName
	}
	return c
}

// proxyEnv builds the proxy environment variable assignments for a launched
// child. socks is used for ALL_PROXY (socks5h so DNS resolves remotely), the
// HTTP proxy for HTTP[S]_PROXY. Both upper- and lower-case spellings are set
// because tools disagree on which they read. Pure.
func proxyEnv(socksAddr, httpAddr string) []string {
	httpURL := "http://" + httpAddr
	socksURL := "socks5h://" + socksAddr
	return []string{
		"HTTP_PROXY=" + httpURL,
		"http_proxy=" + httpURL,
		"HTTPS_PROXY=" + httpURL,
		"https_proxy=" + httpURL,
		"ALL_PROXY=" + socksURL,
		"all_proxy=" + socksURL,
	}
}

// chromiumApps are the executable base names (lower-cased, .app/.exe stripped) of
// Chromium/Electron-based apps that honour --proxy-server. These ignore the
// HTTP[S]_PROXY/ALL_PROXY env vars, so env injection alone never routes them;
// passing --proxy-server is the only way to proxy them from the launch path.
var chromiumApps = map[string]bool{
	"cursor":        true,
	"code":          true,
	"vscode":        true,
	"chrome":        true,
	"google chrome": true,
	"chromium":      true,
	"brave":         true,
	"zen":           true,
	"electron":      true,
	"slack":         true,
	"discord":       true,
	"claude":        true, // Claude Desktop (/Applications/Claude.app/Contents/MacOS/Claude)
}

// chromiumProxyArgs returns argv with a --proxy-server flag appended when argv[0]
// names a known Chromium/Electron app and no --proxy-server is already present.
// Otherwise argv is returned unchanged. Pure (no I/O), so it is unit-tested.
// socksAddr is a "host:port" SOCKS address (Config.SocksAddr).
func chromiumProxyArgs(argv []string, socksAddr string) []string {
	if len(argv) == 0 || socksAddr == "" {
		return argv
	}
	if !chromiumApps[appLabel(argv[0])] {
		return argv
	}
	for _, a := range argv[1:] {
		if a == "--proxy-server" || strings.HasPrefix(a, "--proxy-server=") {
			return argv // honour an explicit user-supplied proxy
		}
	}
	out := make([]string, len(argv), len(argv)+1)
	copy(out, argv)
	return append(out, "--proxy-server=socks5://"+socksAddr)
}

// electronEnv returns extra environment for a launched Electron app whose
// extension-host / agent traffic runs on Node's native fetch (undici) — e.g.
// Cursor and VS Code. That traffic ignores BOTH --proxy-server (which only
// steers Chromium's network service) AND the bare HTTP[S]_PROXY env vars (Node's
// fetch/undici does not honour them by default), so it otherwise leaks straight
// past the proxy. NODE_USE_ENV_PROXY=1 makes Node parse HTTP[S]_PROXY/NO_PROXY
// for fetch() — but only on Node >= 22.21 / 24 (bundled in newer Electron); it
// is a harmless no-op on older runtimes. Keyed on the same chromiumApps set as
// chromiumProxyArgs. Returns nil for non-Electron commands. Pure; unit-tested.
func electronEnv(argv []string) []string {
	if len(argv) == 0 || !chromiumApps[appLabel(argv[0])] {
		return nil
	}
	return []string{"NODE_USE_ENV_PROXY=1"}
}

// appLabel derives a short display label from a command token (an executable
// path or argv[0]): filepath.Base, then strip a trailing .app or .exe suffix,
// lower-cased. Pure, so it is unit-tested and shared by the Chromium preset and
// the output sink. Returns "" for an empty input.
func appLabel(arg string) string {
	if arg == "" {
		return ""
	}
	base := strings.ToLower(filepath.Base(arg))
	base = strings.TrimSuffix(base, ".app")
	base = strings.TrimSuffix(base, ".exe")
	return base
}

// launchWithEnv starts argv with extraEnv appended to the current environment
// and returns the child PID. When sink is non-nil the child's stdout/stderr are
// captured line by line (tagged with app); when nil the child inherits the
// current process's stdio. Process spawning itself lives in the exec adapter.
func launchWithEnv(ctx context.Context, argv, extraEnv []string, user *LaunchUser, sink OutputSink) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("empty command")
	}
	return startProcess(ctx, argv, extraEnv, user, sink)
}

// restartPID reads a process's argv, terminates it, and relaunches it through
// the given launch func (which applies the platform routing). Shared by every
// Router implementation.
func restartPID(ctx context.Context, pid int, launch func(context.Context, []string) (int, error)) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("invalid pid %d", pid)
	}
	argv, err := processArgv(ctx, pid)
	if err != nil {
		return 0, fmt.Errorf("read argv of pid %d: %w", pid, err)
	}
	if len(argv) == 0 {
		return 0, fmt.Errorf("pid %d has no command line", pid)
	}
	_ = terminate(pid)
	return launch(ctx, argv)
}

// terminate sends SIGTERM to a single PID (best-effort). pid must be > 0: a
// non-positive pid would signal the whole process group (killing singctl).
func terminate(pid int) error {
	if pid <= 0 {
		return fmt.Errorf("refusing to signal non-positive pid %d", pid)
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// envRouter is the fallback used on non-Linux platforms (e.g. macOS): there is no
// kernel per-PID interception, so routing a selected process means RESTARTING it
// in proxy mode (recover its argv, terminate it, relaunch with proxy env), and
// Launch starts a new child with proxy env.
type envRouter struct {
	cfg Config
	mu  muList
}

func newEnvRouter(cfg Config) *envRouter { return &envRouter{cfg: cfg.withDefaults()} }

// AddPID restarts the process in proxy mode (the only way to route an existing
// process off Linux).
func (r *envRouter) AddPID(ctx context.Context, pid int) error {
	_, err := restartPID(ctx, pid, r.Launch)
	return err
}

// RemovePID is a no-op off Linux: a restarted process can't be cleanly un-routed.
func (r *envRouter) RemovePID(context.Context, int) error { return nil }
func (r *envRouter) Cleanup() error                       { return nil }
func (r *envRouter) ListRouted() []int                    { return r.mu.list() }

// Unroute off Linux can't detach env-proxying, so it terminates the process.
func (r *envRouter) Unroute(_ context.Context, pid int) error {
	r.mu.remove(pid)
	return terminate(pid)
}

// Kill terminates a proxied process.
func (r *envRouter) Kill(_ context.Context, pid int) error {
	r.mu.remove(pid)
	return terminate(pid)
}

func (r *envRouter) Launch(ctx context.Context, argv []string) (int, error) {
	argv = chromiumProxyArgs(argv, r.cfg.SocksAddr)
	env := append(proxyEnv(r.cfg.SocksAddr, r.cfg.HTTPAddr), electronEnv(argv)...)
	pid, err := launchWithEnv(ctx, argv, env, r.cfg.LaunchUser, r.cfg.Output)
	if err != nil {
		return 0, err
	}
	r.mu.add(pid)
	return pid, nil
}

func (r *envRouter) RestartPID(ctx context.Context, pid int) (int, error) {
	return restartPID(ctx, pid, r.Launch)
}
