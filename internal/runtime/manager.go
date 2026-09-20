// Package runtime owns the lifecycle of the two sing-box instances (persistent
// proxy + on-demand TUN-forwarder). It depends only on injected interfaces
// (core.Factory, ConfigBuilder, InterfaceProber, RouteController), so it is
// fully unit-testable with fakes — no real sing-box, no root, no network.
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"singctl/internal/core"
)

// State is the manager's coarse lifecycle state.
type State int

const (
	StateStopped State = iota
	StateProxyOnly
	StateVPN
	StateSuspended // fully stood down while yielding to Cisco (proxy will resume)
)

func (s State) String() string {
	switch s {
	case StateProxyOnly:
		return "proxy-only"
	case StateVPN:
		return "vpn"
	case StateSuspended:
		return "suspended"
	default:
		return "stopped"
	}
}

// DefaultTransitionTimeout bounds how long a mode transition (StartProxy,
// StartForwarder, StopForwarder, BindProxyToPhysical, UnbindProxy,
// RefreshProxy, SuspendForCisco, ResumeProxy) waits for the underlying
// core.Factory/Start/Close to finish before giving up and returning a timeout
// error to the caller (F2 item 4).
//
// sing-box's Start/Close are synchronous and do not honor context
// cancellation, so a timed-out transition does not kill the underlying work —
// it only stops the CALLER (and, critically, anyone else who would otherwise
// be wedged behind the manager's lock — see transition) from being wedged
// behind it forever. Two chained transitions (EnableVPN's StartProxy then
// StartForwarder) must together stay under control.FastDeadline (~15s), which
// is why this is sized well under half of it.
const DefaultTransitionTimeout = 6 * time.Second

// ConfigBuilder produces sing-box JSON for the two instances. physIface is the
// physical interface to bind the proxy's outbounds to; it is non-empty ONLY in
// VPN mode (D3). "" means proxy-only (no bind; rides the default route).
type ConfigBuilder interface {
	ProxyConfig(physIface string) ([]byte, error)
	ForwarderConfig() ([]byte, error)
}

// InterfaceProber discovers the physical default interface to bind to in VPN mode.
type InterfaceProber interface {
	PhysicalDefault() (string, error)
}

// RouteController removes routing-table leftovers (orphan TUN routes) from a
// prior crash. Expanded in stage 5; the manager only needs CleanupOrphans here.
type RouteController interface {
	CleanupOrphans() error
}

// Manager coordinates the proxy and forwarder cores. All methods are safe for
// concurrent use.
//
// Locking model (F2 item 4): mu guards ONLY the small in-memory fields below
// (state, proxy, fwd, bound, gen) and is held for O(1) reads/writes — NEVER
// across a call into factory/Start/Close, which are the slow, syscall-heavy,
// potentially-stuck operations. Every mutating method runs its actual work in
// a background goroutine (see transition) and waits for it up to
// TransitionTimeout; a caller that times out gets an error immediately
// without the work being cancelled (sing-box can't be cancelled), so a
// completion that arrives late checks gen (bumped by Shutdown) and discards
// itself instead of clobbering state a subsequent Shutdown already reset.
// This is what makes a stuck forwarder start recoverable without restarting
// the process: Shutdown never waits on mu for longer than an instant, so
// "MODE off" always works even while an earlier transition is still stuck.
type Manager struct {
	factory core.Factory
	build   ConfigBuilder
	prober  InterfaceProber
	routes  RouteController

	// TransitionTimeout overrides DefaultTransitionTimeout when non-zero. Set
	// it directly before concurrent use begins (tests shrink it to keep
	// timeout cases fast).
	TransitionTimeout time.Duration

	mu    sync.Mutex
	state State
	proxy core.Core
	fwd   core.Core
	bound string // interface the proxy is currently bound to ("" = none)
	gen   uint64 // bumped by Shutdown to invalidate any in-flight transition
}

func NewManager(f core.Factory, b ConfigBuilder, p InterfaceProber, r RouteController) *Manager {
	return &Manager{factory: f, build: b, prober: p, routes: r}
}

func (m *Manager) timeout() time.Duration {
	if m.TransitionTimeout > 0 {
		return m.TransitionTimeout
	}
	return DefaultTransitionTimeout
}

// transition runs work in the background — NOT holding mu for its duration —
// and waits up to the transition timeout for it to finish, so a slow or stuck
// core.Start/Close can never wedge a concurrent Shutdown/Stop (or any other
// transition) behind it. work is handed the generation stamped at the moment
// the transition began; it must check that stamp (via committed, below)
// before writing any result back into the manager's fields, so a completion
// that arrives after a Shutdown has reset the manager is discarded instead of
// clobbering it.
func (m *Manager) transition(label string, work func(gen uint64) error) error {
	m.mu.Lock()
	gen := m.gen
	m.mu.Unlock()

	errCh := make(chan error, 1)
	go func() { errCh <- work(gen) }()

	select {
	case err := <-errCh:
		return err
	case <-time.After(m.timeout()):
		return fmt.Errorf("%s: timed out after %s", label, m.timeout())
	}
}

// committed reports whether gen is still the manager's current generation —
// i.e. no Shutdown has reset the manager since the transition that owns gen
// began. Callers hold mu is NOT required/assumed; committed takes it itself.
func (m *Manager) committed(gen uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.gen == gen
}

// StartProxy starts the persistent proxy (proxy-only mode). It cleans orphan
// routes first and is idempotent.
func (m *Manager) StartProxy(ctx context.Context) error {
	return m.transition("start proxy", func(gen uint64) error {
		m.mu.Lock()
		already := m.proxy != nil
		m.mu.Unlock()
		if already {
			return nil
		}
		if err := m.routes.CleanupOrphans(); err != nil {
			return fmt.Errorf("cleanup orphan routes: %w", err)
		}
		if err := m.dialProxy(ctx, "", gen); err != nil {
			return err
		}
		m.mu.Lock()
		if m.gen == gen {
			m.state = StateProxyOnly
		}
		m.mu.Unlock()
		return nil
	})
}

// StartForwarder enters VPN mode: it rebinds the proxy to the physical interface
// (recreating it — accepted sub-second drop, D3) so its egress escapes our TUN,
// then starts the forwarder. On any failure it rolls back to the prior
// proxy-only state. No-op if already in VPN mode.
func (m *Manager) StartForwarder(ctx context.Context) error {
	return m.transition("start forwarder", func(gen uint64) error {
		m.mu.Lock()
		if m.state == StateVPN {
			m.mu.Unlock()
			return nil
		}
		if m.proxy == nil {
			m.mu.Unlock()
			return fmt.Errorf("start forwarder: proxy not running")
		}
		prevBound := m.bound
		m.mu.Unlock()

		physIface, err := m.prober.PhysicalDefault()
		if err != nil {
			return fmt.Errorf("detect physical interface: %w", err)
		}
		if physIface == "" {
			return fmt.Errorf("start forwarder: no physical interface detected")
		}

		if err := m.recreateProxy(ctx, physIface, gen); err != nil {
			// Proxy is now down; try to restore the previous binding.
			m.rollbackProxy(ctx, prevBound, gen)
			return fmt.Errorf("rebind proxy for VPN: %w", err)
		}

		cfg, err := m.build.ForwarderConfig()
		if err != nil {
			m.rollbackProxy(ctx, prevBound, gen)
			return fmt.Errorf("build forwarder config: %w", err)
		}
		fwd, err := m.factory(ctx, "forwarder", cfg)
		if err != nil {
			m.rollbackProxy(ctx, prevBound, gen)
			return fmt.Errorf("create forwarder core: %w", err)
		}
		if err := fwd.Start(ctx); err != nil {
			_ = fwd.Close()
			m.rollbackProxy(ctx, prevBound, gen)
			return fmt.Errorf("start forwarder core: %w", err)
		}

		if !m.committed(gen) {
			_ = fwd.Close() // superseded by a Shutdown while we were starting
			return fmt.Errorf("start forwarder: superseded")
		}
		m.mu.Lock()
		m.fwd = fwd
		m.state = StateVPN
		m.mu.Unlock()
		return nil
	})
}

// StopForwarder leaves VPN mode: it tears down the forwarder and unbinds the
// proxy (back to riding the default route). No-op if not in VPN mode.
func (m *Manager) StopForwarder(ctx context.Context) error {
	return m.transition("stop forwarder", func(gen uint64) error {
		m.mu.Lock()
		inVPN := m.state == StateVPN
		m.mu.Unlock()
		if !inVPN {
			return nil
		}
		return m.teardownForwarder(ctx, gen)
	})
}

// NotifyForwarderExited handles an unexpected forwarder exit (crash): drop to
// proxy-only with the proxy unbound. No-op if not in VPN mode.
func (m *Manager) NotifyForwarderExited(ctx context.Context) error {
	return m.transition("forwarder exited", func(gen uint64) error {
		m.mu.Lock()
		inVPN := m.state == StateVPN
		m.mu.Unlock()
		if !inVPN {
			return nil
		}
		return m.teardownForwarder(ctx, gen)
	})
}

// teardownForwarder closes the forwarder, scrubs any leftover TUN device and
// routes (so a connecting Cisco gets a clean routing table), and restores an
// unbound proxy. Never holds mu across the (potentially slow) Close/Start
// calls — see the Manager doc comment.
func (m *Manager) teardownForwarder(ctx context.Context, gen uint64) error {
	m.mu.Lock()
	fwd := m.fwd
	m.fwd = nil
	m.mu.Unlock()
	if fwd != nil {
		_ = fwd.Close()
	}
	// Belt-and-suspenders: remove our own orphaned utun/routes immediately so
	// they can't conflict with Cisco's route installation.
	_ = m.routes.CleanupOrphans()
	if err := m.recreateProxy(ctx, "", gen); err != nil {
		m.mu.Lock()
		if m.gen == gen {
			m.state = StateStopped
		}
		m.mu.Unlock()
		return fmt.Errorf("rebind proxy after VPN stop: %w", err)
	}
	m.mu.Lock()
	if m.gen == gen {
		m.state = StateProxyOnly
	}
	m.mu.Unlock()
	return nil
}

// SuspendForCisco fully stands down — closes the forwarder AND the proxy and
// scrubs our routes — so a connecting Cisco gets a completely quiet routing
// table (no teardown/reconnect churn racing its route installation). The manager
// keeps its config builder so ResumeProxy can bring the proxy back.
func (m *Manager) SuspendForCisco(ctx context.Context) error {
	return m.transition("suspend for cisco", func(gen uint64) error {
		m.mu.Lock()
		fwd, proxy := m.fwd, m.proxy
		m.fwd, m.proxy, m.bound = nil, nil, ""
		m.mu.Unlock()
		if fwd != nil {
			_ = fwd.Close()
		}
		if proxy != nil {
			_ = proxy.Close()
		}
		_ = m.routes.CleanupOrphans()
		m.mu.Lock()
		if m.gen == gen {
			m.state = StateSuspended
		}
		m.mu.Unlock()
		return nil
	})
}

// ResumeProxy brings the proxy back (unbound, riding the now-stable default
// route / Cisco) after a Cisco-yield suspension. No-op unless suspended.
func (m *Manager) ResumeProxy(ctx context.Context) error {
	return m.transition("resume proxy", func(gen uint64) error {
		m.mu.Lock()
		ok := m.state == StateSuspended && m.proxy == nil
		m.mu.Unlock()
		if !ok {
			return nil
		}
		if err := m.dialProxy(ctx, "", gen); err != nil {
			return err
		}
		m.mu.Lock()
		if m.gen == gen {
			m.state = StateProxyOnly
		}
		m.mu.Unlock()
		return nil
	})
}

// BindProxyToPhysical rebinds the proxy's egress to physIface while STAYING in
// proxy-only mode, so its upstream dial escapes a third-party VPN (Cisco) that
// owns the default route — the primary coexistence strategy. No-op unless the
// proxy is running in proxy-only mode and the target differs from the current
// bind. On failure it rolls back to the previous (usually unbound) proxy so the
// caller can fall back to riding the default route, and returns the error.
func (m *Manager) BindProxyToPhysical(ctx context.Context, physIface string) error {
	return m.transition("bind proxy to physical", func(gen uint64) error {
		m.mu.Lock()
		if m.state != StateProxyOnly || m.proxy == nil {
			m.mu.Unlock()
			return nil
		}
		if physIface == "" {
			m.mu.Unlock()
			return fmt.Errorf("bind proxy for cisco: no physical interface")
		}
		if m.bound == physIface {
			m.mu.Unlock()
			return nil // already pinned to this NIC
		}
		prevBound := m.bound
		m.mu.Unlock()

		if err := m.recreateProxy(ctx, physIface, gen); err != nil {
			m.rollbackProxy(ctx, prevBound, gen) // restore the working unbound proxy
			return fmt.Errorf("bind proxy to %s: %w", physIface, err)
		}
		return nil
	})
}

// UnbindProxy releases any physical bind and re-dials the proxy over the current
// default route (rule c: Cisco disconnected). No-op unless in proxy-only mode.
func (m *Manager) UnbindProxy(ctx context.Context) error {
	return m.transition("unbind proxy", func(gen uint64) error {
		m.mu.Lock()
		inProxy := m.state == StateProxyOnly
		m.mu.Unlock()
		if !inProxy {
			return nil
		}
		if err := m.recreateProxy(ctx, "", gen); err != nil {
			m.mu.Lock()
			if m.gen == gen {
				m.state = StateStopped
			}
			m.mu.Unlock()
			return fmt.Errorf("unbind proxy: %w", err)
		}
		return nil
	})
}

// RefreshProxy re-dials the proxy upstream by recreating it with the same
// binding (rule c: reconnect after Cisco turns off). Accepts the sub-second
// listener gap (D3/R2).
func (m *Manager) RefreshProxy(ctx context.Context) error {
	return m.transition("refresh proxy", func(gen uint64) error {
		m.mu.Lock()
		if m.proxy == nil {
			m.mu.Unlock()
			return fmt.Errorf("refresh: proxy not running")
		}
		bound := m.bound
		m.mu.Unlock()
		return m.recreateProxy(ctx, bound, gen)
	})
}

// Shutdown closes both cores and returns the manager to StateStopped. Unlike
// every method above it does NOT go through transition: it must never wait
// behind a stuck transition's background goroutine, so "MODE off" (F2 item 4)
// always works even while an earlier StartForwarder/StartProxy is stuck
// inside a slow core.Start. It bumps gen first so that stuck goroutine's
// eventual (possibly never) completion is discarded instead of clobbering
// this shutdown (see transition/committed), then closes whatever cores are
// ALREADY published — never anything the stuck goroutine is still building.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.gen++
	fwd, proxy := m.fwd, m.proxy
	m.fwd, m.proxy = nil, nil
	m.state = StateStopped
	m.mu.Unlock()

	var firstErr error
	if fwd != nil {
		if err := fwd.Close(); err != nil {
			firstErr = err
		}
	}
	if proxy != nil {
		if err := proxy.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// State returns the current lifecycle state.
func (m *Manager) State() State {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// BoundInterface returns the interface the proxy is currently bound to
// ("" = none / proxy-only).
func (m *Manager) BoundInterface() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.bound
}

// --- lock-free (w.r.t. the slow call) helpers ---
//
// dialProxy/recreateProxy/rollbackProxy do their own brief mu locking around
// field reads/writes but NEVER hold mu while calling into build/factory/Start
// — see the Manager doc comment. Each takes the gen the owning transition was
// stamped with and checks it (via committed) before publishing a result, so a
// transition that has already timed out (and whose caller has moved on, or
// whom a subsequent Shutdown has superseded) can't clobber newer state.

// dialProxy builds+starts a proxy core bound to physIface and, if this
// transition is still current, commits it as m.proxy/m.bound.
func (m *Manager) dialProxy(ctx context.Context, physIface string, gen uint64) error {
	cfg, err := m.build.ProxyConfig(physIface)
	if err != nil {
		return fmt.Errorf("build proxy config: %w", err)
	}
	c, err := m.factory(ctx, "proxy", cfg)
	if err != nil {
		return fmt.Errorf("create proxy core: %w", err)
	}
	if err := c.Start(ctx); err != nil {
		_ = c.Close()
		return fmt.Errorf("start proxy core: %w", err)
	}
	if !m.committed(gen) {
		_ = c.Close() // superseded by a Shutdown while we were starting
		return fmt.Errorf("start proxy: superseded")
	}
	m.mu.Lock()
	m.proxy = c
	m.bound = physIface
	m.mu.Unlock()
	return nil
}

// recreateProxy closes the current proxy (if any) and dials a fresh one bound
// to physIface. On failure m.proxy is left nil.
func (m *Manager) recreateProxy(ctx context.Context, physIface string, gen uint64) error {
	m.mu.Lock()
	old := m.proxy
	m.proxy = nil
	m.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return m.dialProxy(ctx, physIface, gen)
}

// rollbackProxy best-effort restores a working proxy-only state after a
// failed VPN enable / bind attempt.
func (m *Manager) rollbackProxy(ctx context.Context, prevBound string, gen uint64) {
	if err := m.recreateProxy(ctx, prevBound, gen); err != nil {
		m.mu.Lock()
		if m.gen == gen {
			m.state = StateStopped
		}
		m.mu.Unlock()
		return
	}
	m.mu.Lock()
	if m.gen == gen {
		m.state = StateProxyOnly
	}
	m.mu.Unlock()
}
