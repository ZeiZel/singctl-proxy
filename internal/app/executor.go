// Package app is the composition root's glue: the Executor builds the runtime
// from a pasted link, implements ui.Backend, applies the monitor's policy
// decisions to the manager (auto fail-closed / refresh), and pushes status to
// the UI. It is testable end-to-end on fakes (FakeCore + fake prober/routes).
package app

import (
	"context"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/core"
	"singctl/internal/monitor"
	"singctl/internal/policy"
	"singctl/internal/runtime"
	"singctl/internal/singbox"
	"singctl/internal/types"
	"singctl/internal/ui"
	"singctl/internal/vless"
)

// Executor wires the UI/monitor to the runtime.Manager. The manager is built
// lazily on StartProxy (once the link is known).
type Executor struct {
	factory core.Factory
	prober  runtime.InterfaceProber
	routes  runtime.RouteController
	notes   chan tea.Msg

	mu      sync.Mutex
	mgr     *runtime.Manager
	save    func(string) error
	logPath string
	ports   singbox.Ports
}

// SetLogPath redirects sing-box logs to a file (keeps them out of the TUI).
func (e *Executor) SetLogPath(path string) { e.logPath = path }

// SetSocksPort overrides the proxy's local socks port (the http port follows
// at port+1). 0 keeps the defaults (socks 1080, http 2080). Takes effect on
// the next LoadLink.
func (e *Executor) SetSocksPort(port int) {
	if port == 0 {
		e.ports = singbox.Ports{}
		return
	}
	e.ports = singbox.Ports{Socks: port, HTTP: port + 1}
}

func NewExecutor(f core.Factory, p runtime.InterfaceProber, r runtime.RouteController, notes chan tea.Msg) *Executor {
	return &Executor{factory: f, prober: p, routes: r, notes: notes}
}

// SetSaver registers an optional persistence hook called after a successful
// StartProxy (best-effort).
func (e *Executor) SetSaver(fn func(string) error) { e.save = fn }

// --- ui.Backend ---

// LoadLink validates+remembers the link and prepares the runtime WITHOUT
// starting anything (the user explicitly enables a mode afterwards). Changing
// the link tears down any previous runtime first.
func (e *Executor) LoadLink(ctx context.Context, link string) error {
	profile, err := vless.ParseLink(link)
	if err != nil {
		return err
	}
	if old := e.manager(); old != nil {
		_ = old.Shutdown(ctx)
	}
	builder := runtime.ProfileConfigBuilder{Profile: profile, LogPath: e.logPath, Ports: e.ports}
	mgr := runtime.NewManager(e.factory, builder, e.prober, e.routes)
	e.mu.Lock()
	e.mgr = mgr
	e.mu.Unlock()
	if e.save != nil {
		_ = e.save(link)
	}
	return nil
}

// EnableProxy starts (or switches to) proxy-only mode.
func (e *Executor) EnableProxy(ctx context.Context) error {
	mgr := e.manager()
	if mgr == nil {
		return errNoProxy
	}
	if mgr.State() == runtime.StateVPN {
		return mgr.StopForwarder(ctx) // VPN -> proxy
	}
	return mgr.StartProxy(ctx) // off/suspended -> proxy (idempotent)
}

// EnableVPN starts VPN mode (proxy + forwarder), ensuring the proxy is up first.
func (e *Executor) EnableVPN(ctx context.Context) error {
	mgr := e.manager()
	if mgr == nil {
		return errNoProxy
	}
	if err := mgr.StartProxy(ctx); err != nil {
		return err
	}
	return mgr.StartForwarder(ctx)
}

// Stop shuts everything down (back to OFF), keeping the loaded profile so the
// user can re-enable a mode.
func (e *Executor) Stop(ctx context.Context) error {
	mgr := e.manager()
	if mgr == nil {
		return nil
	}
	return mgr.Shutdown(ctx)
}

// --- monitor integration ---

// Loop applies monitor events until ctx is cancelled.
func (e *Executor) Loop(ctx context.Context, in <-chan monitor.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-in:
			e.Apply(ctx, ev)
		}
	}
}

// Apply enacts a policy decision on the manager and notifies the UI.
func (e *Executor) Apply(ctx context.Context, ev monitor.Event) {
	mgr := e.manager()
	var stop, refresh bool
	for _, a := range ev.Decision.Actions {
		switch a {
		case policy.ActFailClosed, policy.ActStopForwarder:
			stop = true
		case policy.ActRefreshProxy:
			refresh = true
		}
	}
	if mgr != nil {
		if stop {
			// Yield fully (forwarder + proxy down) and STAY down while Cisco is
			// present: AnyConnect aborts ("can't verify IP forwarding table
			// changes") if any process touches the routing table during its
			// connect. We resume only once Cisco disconnects (the refresh path).
			_ = mgr.SuspendForCisco(ctx)
		}
		if refresh {
			if mgr.State() == runtime.StateSuspended {
				_ = mgr.ResumeProxy(ctx) // Cisco disconnected — safe to come back
			} else {
				_ = mgr.RefreshProxy(ctx)
			}
		}
	}
	if note := noteFor(stop, refresh); note != "" {
		e.push(ctx, ui.StatusMsg{Mode: e.runMode(), Note: note})
	}
	e.PushDisplay(ctx, ev.NetState)
}

// PushDisplay sends the live Cisco/interface status to the UI (used as the
// monitor's per-poll callback).
func (e *Executor) PushDisplay(ctx context.Context, ns types.NetState) {
	e.push(ctx, ui.NetStateMsg{Cisco: ns.CiscoActive, PhysIface: ns.PhysicalIface})
}

// Mode maps the manager state to the policy mode (the monitor's modeFn).
func (e *Executor) Mode() policy.Mode {
	m := e.manager()
	if m != nil && m.State() == runtime.StateVPN {
		return policy.ModeVPN
	}
	return policy.ModeProxy
}

// runMode maps the manager state to the UI's running state.
func (e *Executor) runMode() ui.RunMode {
	m := e.manager()
	if m == nil {
		return ui.RunOff
	}
	switch m.State() {
	case runtime.StateVPN:
		return ui.RunVPN
	case runtime.StateProxyOnly:
		return ui.RunProxy
	default:
		return ui.RunOff
	}
}

// Shutdown tears down both cores.
func (e *Executor) Shutdown(ctx context.Context) error {
	if m := e.manager(); m != nil {
		return m.Shutdown(ctx)
	}
	return nil
}

func (e *Executor) manager() *runtime.Manager {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mgr
}

func (e *Executor) push(ctx context.Context, msg tea.Msg) {
	if e.notes == nil {
		return
	}
	select {
	case e.notes <- msg:
	case <-ctx.Done():
	}
}

func noteFor(stop, refresh bool) string {
	switch {
	case stop:
		return "Cisco активен — VPN и proxy остановлены, вернёмся после отключения Cisco"
	case refresh:
		return "Cisco отключён — переподключаю proxy"
	default:
		return ""
	}
}
