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
	"system extension is not installed or not approved — build and approve it " +
		"(make app-macos DEVELOPMENT_TEAM=…, then System Settings → Login Items & " +
		"Extensions; see LICENSATION.md for details)")

// darwinRouter is the macOS per-app backend. Unlike the Linux cgroup router or
// the Windows env fallback, it does NO env injection or process restart: it
// drives the NETransparentProxyProvider system extension (internal/netext),
// capturing a target app's WHOLE network stack by bundle ID. Helper/forked
// processes share the parent's signing identifier, so they are captured too —
// no fork tracking needed. Requires the extension to be installed and approved.
//
// The router itself is pure PID<->bundle-ID bookkeeping: it does NOT call
// ctrl.AddTarget/RemoveTarget (ctrl is only consulted via Available()). The
// persistent per-app store's enabled apps can capture a bundle ID the router
// has never seen a PID for (and vice versa — a routed PID whose app isn't
// store-enabled), so internal/app.Executor.RecomputeAppTargets is the single
// place that unions both sources and writes the controller's target set; see
// RoutedBundleIDs, which the executor reads for that union.
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
		return fmt.Errorf("could not determine the app's bundle ID for PID %d (not an .app bundle?)", pid)
	}
	r.register(pid, id)
	return nil
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
		return pid, fmt.Errorf("app launched (PID %d) but bundle ID could not be determined — extension capture not enabled", pid)
	}
	r.register(pid, id)
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

// Unroute drops the PID from the router's bookkeeping (see darwinRouter's doc
// comment — the caller must recompute the controller's target set afterwards
// for this to stop capture, e.g. via Executor.RecomputeAppTargets).
func (r *darwinRouter) Unroute(_ context.Context, pid int) error {
	r.deregister(pid)
	return nil
}

// RemovePID is the same as Unroute on macOS (no cgroup to detach).
func (r *darwinRouter) RemovePID(_ context.Context, pid int) error {
	r.deregister(pid)
	return nil
}

// Kill terminates the process and drops it from the bookkeeping.
func (r *darwinRouter) Kill(_ context.Context, pid int) error {
	r.deregister(pid)
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

// Cleanup clears the router's PID/bundle bookkeeping (it does NOT touch the
// controller's target set — see darwinRouter's doc comment; the caller, e.g.
// Executor.Shutdown, is responsible for pushing the post-cleanup target set,
// typically empty since the daemon is going down).
func (r *darwinRouter) Cleanup() error {
	r.mu.Lock()
	r.byPID = map[int]string{}
	r.refs = map[string]int{}
	r.mu.Unlock()
	return nil
}

// register records pid->id, refcounting the bundle ID so Electron helpers
// sharing an app aren't dropped until the last of them exits. Idempotent.
// Does not touch the controller (see darwinRouter's doc comment).
func (r *darwinRouter) register(pid int, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.byPID[pid]; ok {
		return // idempotent
	}
	r.byPID[pid] = id
	r.refs[id]++
}

// deregister drops pid from the bookkeeping, decrementing the bundle's
// refcount (and dropping it once it reaches zero). Does not touch the
// controller (see darwinRouter's doc comment).
func (r *darwinRouter) deregister(pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	id, ok := r.byPID[pid]
	if !ok {
		return
	}
	delete(r.byPID, pid)
	r.refs[id]--
	if r.refs[id] <= 0 {
		delete(r.refs, id)
	}
}
