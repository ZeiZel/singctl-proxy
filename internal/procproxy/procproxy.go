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
	"syscall"
)

// ErrUnsupportedOnPlatform is returned by AddPID/RemovePID where real per-PID
// interception is not available (everything except Linux).
var ErrUnsupportedOnPlatform = errors.New("per-PID routing is only supported on Linux; use --launch to start a process with proxy env instead")

// Router routes selected processes through the proxy. Implementations are
// platform-specific (see NewRouter).
type Router interface {
	// AddPID routes an already-running process's traffic through the proxy.
	AddPID(ctx context.Context, pid int) error
	// RemovePID stops routing a process.
	RemovePID(ctx context.Context, pid int) error
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

// Config tunes the router. The zero value is filled with defaults by withDefaults.
type Config struct {
	SocksAddr  string // local SOCKS proxy "host:port"
	HTTPAddr   string // local HTTP proxy "host:port"
	Gateway    string // Linux: nexthop the marked traffic is routed via (the TUN address)
	Mark       int    // Linux: fwmark for the routed cgroup
	Table      int    // Linux: policy-routing table id
	CgroupRoot string // Linux: cgroup v2 mount (default /sys/fs/cgroup)
	CgroupName string // Linux: cgroup leaf name
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

// launchWithEnv starts argv with extraEnv appended to the current environment
// and returns the child PID. Process spawning itself lives in the exec adapter.
func launchWithEnv(ctx context.Context, argv, extraEnv []string) (int, error) {
	if len(argv) == 0 {
		return 0, errors.New("empty command")
	}
	return startProcess(ctx, argv, extraEnv)
}

// restartPID reads a process's argv, terminates it, and relaunches it through
// the given launch func (which applies the platform routing). Shared by every
// Router implementation.
func restartPID(ctx context.Context, pid int, launch func(context.Context, []string) (int, error)) (int, error) {
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

// terminate sends SIGTERM to a PID (best-effort).
func terminate(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Signal(syscall.SIGTERM)
}

// envRouter is the fallback used on non-Linux platforms: Launch injects proxy
// env; per-PID routing is unsupported.
type envRouter struct {
	cfg Config
	mu  muList
}

func newEnvRouter(cfg Config) *envRouter { return &envRouter{cfg: cfg.withDefaults()} }

func (r *envRouter) AddPID(context.Context, int) error    { return ErrUnsupportedOnPlatform }
func (r *envRouter) RemovePID(context.Context, int) error { return ErrUnsupportedOnPlatform }
func (r *envRouter) Cleanup() error                       { return nil }
func (r *envRouter) ListRouted() []int                    { return r.mu.list() }

func (r *envRouter) Launch(ctx context.Context, argv []string) (int, error) {
	pid, err := launchWithEnv(ctx, argv, proxyEnv(r.cfg.SocksAddr, r.cfg.HTTPAddr))
	if err != nil {
		return 0, err
	}
	r.mu.add(pid)
	return pid, nil
}

func (r *envRouter) RestartPID(ctx context.Context, pid int) (int, error) {
	return restartPID(ctx, pid, r.Launch)
}
