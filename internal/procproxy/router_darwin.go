//go:build darwin

package procproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync"

	"singctl/internal/netext"
)

// errExtensionUnavailable is returned when per-app proxying is requested on
// macOS but the transparent-proxy system extension isn't installed/approved.
// Surfaced verbatim in the TUI (procErr/errText).
var errExtensionUnavailable = errors.New(
	"системное расширение не установлено или не одобрено — соберите и одобрите его " +
		"(make build-netext DEVELOPMENT_TEAM=…, затем System Settings → Login Items & " +
		"Extensions; подробнее в LICENSATION.md)")

// darwinRouter is the macOS per-app backend. Unlike the Linux cgroup router or
// the Windows env fallback, it does NO env injection or process restart: it
// drives the NETransparentProxyProvider system extension (internal/netext),
// capturing a target app's WHOLE network stack by bundle ID. Helper/forked
// processes share the parent's signing identifier, so they are captured too —
// no fork tracking needed. Requires the extension to be installed and approved.
type darwinRouter struct {
	cfg  Config
	ctrl netext.Controller

	mu    sync.Mutex
	byPID map[int]string // routed pid -> bundle ID
	refs  map[string]int // bundle ID -> number of routed pids (Electron helpers)
}

// NewRouter (darwin) returns the system-extension-backed per-app router.
func NewRouter(cfg Config) Router {
	cfg = cfg.withDefaults()
	host, port := splitSocksAddr(cfg.SocksAddr)
	return newDarwinRouter(cfg, netext.New(host, port))
}

// newDarwinRouter builds the router with an injected controller (a fake in tests).
func newDarwinRouter(cfg Config, ctrl netext.Controller) *darwinRouter {
	return &darwinRouter{
		cfg:   cfg,
		ctrl:  ctrl,
		byPID: map[int]string{},
		refs:  map[string]int{},
	}
}

// splitSocksAddr parses "host:port" into (host, port), falling back to the
// historical 127.0.0.1:1080 on any parse error.
func splitSocksAddr(addr string) (string, int) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "127.0.0.1", 1080
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 {
		return host, 1080
	}
	return host, port
}

// AddPID routes an already-running process: resolve its bundle ID and tell the
// extension to capture that app. Works for running apps — the extension
// intercepts their future flows; no restart needed.
func (r *darwinRouter) AddPID(_ context.Context, pid int) error {
	if !r.ctrl.Available() {
		return errExtensionUnavailable
	}
	id := netext.BundleIDForPID(pid)
	if id == "" {
		return fmt.Errorf("не удалось определить bundle ID приложения для PID %d (не .app-бандл?)", pid)
	}
	return r.register(pid, id)
}

// Launch starts the app (no env injection) and captures it by bundle ID.
func (r *darwinRouter) Launch(ctx context.Context, argv []string) (int, error) {
	if !r.ctrl.Available() {
		return 0, errExtensionUnavailable
	}
	if len(argv) == 0 {
		return 0, errors.New("empty command")
	}
	pid, err := startProcess(ctx, argv, nil, r.cfg.LaunchUser, r.cfg.Output)
	if err != nil {
		return 0, err
	}
	// Resolve from the launch path (reads the .app Info.plist); fall back to the
	// child PID if argv[0] wasn't a bundle path.
	id := netext.BundleID(argv[0])
	if id == "" {
		id = netext.BundleIDForPID(pid)
	}
	if id == "" {
		return pid, fmt.Errorf("приложение запущено (PID %d), но bundle ID не определён — захват расширением не активирован", pid)
	}
	if err := r.register(pid, id); err != nil {
		return pid, err
	}
	return pid, nil
}

// RestartPID: with the extension there is nothing to restart — the app's running
// flows are captured live. Register it and return the same PID.
func (r *darwinRouter) RestartPID(ctx context.Context, pid int) (int, error) {
	if err := r.AddPID(ctx, pid); err != nil {
		return 0, err
	}
	return pid, nil
}

// Unroute stops capturing the app once its last routed PID is gone.
func (r *darwinRouter) Unroute(_ context.Context, pid int) error { return r.deregister(pid) }

// RemovePID is the same as Unroute on macOS (no cgroup to detach).
func (r *darwinRouter) RemovePID(_ context.Context, pid int) error { return r.deregister(pid) }

// Kill terminates the process and stops capturing its app.
func (r *darwinRouter) Kill(_ context.Context, pid int) error {
	_ = r.deregister(pid)
	return terminate(pid)
}

func (r *darwinRouter) ListRouted() []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]int, 0, len(r.byPID))
	for pid := range r.byPID {
		out = append(out, pid)
	}
	sort.Ints(out)
	return out
}

// PIDsForBundle implements procproxy.BundleRouter: it answers straight from the
// existing byPID bookkeeping (populated by register/AddPID), so callers can
// unroute a whole app by delegating to Unroute for each PID it reports — no
// extra process probing (which would fail for a PID that already exited).
func (r *darwinRouter) PIDsForBundle(bundleID string) []int {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []int
	for pid, id := range r.byPID {
		if id == bundleID {
			out = append(out, pid)
		}
	}
	sort.Ints(out)
	return out
}

// RoutedBundleIDs implements procproxy.BundleRouter: the bundle IDs with at
// least one routed PID (i.e. currently captured by the extension), for the
// GUI's "currently proxied" app list.
func (r *darwinRouter) RoutedBundleIDs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.refs))
	for id := range r.refs {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// Cleanup releases every captured target (so a stale config.json doesn't keep
// routing after singctl exits).
func (r *darwinRouter) Cleanup() error {
	r.mu.Lock()
	ids := make([]string, 0, len(r.refs))
	for id := range r.refs {
		ids = append(ids, id)
	}
	r.byPID = map[int]string{}
	r.refs = map[string]int{}
	r.mu.Unlock()

	var firstErr error
	for _, id := range ids {
		if err := r.ctrl.RemoveTarget(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// register records pid->id and adds the target on the first PID of that app.
func (r *darwinRouter) register(pid int, id string) error {
	r.mu.Lock()
	if _, ok := r.byPID[pid]; ok {
		r.mu.Unlock()
		return nil // idempotent
	}
	r.byPID[pid] = id
	r.refs[id]++
	first := r.refs[id] == 1
	r.mu.Unlock()

	if first {
		if err := r.ctrl.AddTarget(id); err != nil {
			// Roll back the bookkeeping so a retry can re-add cleanly.
			r.mu.Lock()
			delete(r.byPID, pid)
			r.refs[id]--
			if r.refs[id] <= 0 {
				delete(r.refs, id)
			}
			r.mu.Unlock()
			return fmt.Errorf("не удалось включить захват приложения %s: %w", id, err)
		}
	}
	return nil
}

// deregister drops pid and removes the target once the app has no routed PIDs.
func (r *darwinRouter) deregister(pid int) error {
	r.mu.Lock()
	id, ok := r.byPID[pid]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	delete(r.byPID, pid)
	r.refs[id]--
	last := r.refs[id] <= 0
	if last {
		delete(r.refs, id)
	}
	r.mu.Unlock()

	if last {
		return r.ctrl.RemoveTarget(id)
	}
	return nil
}
