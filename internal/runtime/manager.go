// Package runtime owns the lifecycle of the two sing-box instances (persistent
// proxy + on-demand TUN-forwarder). It depends only on injected interfaces
// (core.Factory, ConfigBuilder, InterfaceProber, RouteController), so it is
// fully unit-testable with fakes — no real sing-box, no root, no network.
package runtime

import (
	"context"
	"fmt"
	"sync"

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
type Manager struct {
	factory core.Factory
	build   ConfigBuilder
	prober  InterfaceProber
	routes  RouteController

	mu    sync.Mutex
	state State
	proxy core.Core
	fwd   core.Core
	bound string // interface the proxy is currently bound to ("" = none)
}

func NewManager(f core.Factory, b ConfigBuilder, p InterfaceProber, r RouteController) *Manager {
	return &Manager{factory: f, build: b, prober: p, routes: r}
}

// StartProxy starts the persistent proxy (proxy-only mode). It cleans orphan
// routes first and is idempotent.
func (m *Manager) StartProxy(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxy != nil {
		return nil
	}
	if err := m.routes.CleanupOrphans(); err != nil {
		return fmt.Errorf("cleanup orphan routes: %w", err)
	}
	if err := m.startProxyLocked(ctx, ""); err != nil {
		return err
	}
	m.state = StateProxyOnly
	return nil
}

// StartForwarder enters VPN mode: it rebinds the proxy to the physical interface
// (recreating it — accepted sub-second drop, D3) so its egress escapes our TUN,
// then starts the forwarder. On any failure it rolls back to the prior
// proxy-only state. No-op if already in VPN mode.
func (m *Manager) StartForwarder(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == StateVPN {
		return nil
	}
	if m.proxy == nil {
		return fmt.Errorf("start forwarder: proxy not running")
	}
	physIface, err := m.prober.PhysicalDefault()
	if err != nil {
		return fmt.Errorf("detect physical interface: %w", err)
	}
	if physIface == "" {
		return fmt.Errorf("start forwarder: no physical interface detected")
	}

	prevBound := m.bound
	if err := m.recreateProxyLocked(ctx, physIface); err != nil {
		// Proxy is now down; try to restore the previous binding.
		m.rollbackProxyLocked(ctx, prevBound)
		return fmt.Errorf("rebind proxy for VPN: %w", err)
	}

	cfg, err := m.build.ForwarderConfig()
	if err != nil {
		m.rollbackProxyLocked(ctx, prevBound)
		return fmt.Errorf("build forwarder config: %w", err)
	}
	fwd, err := m.factory(ctx, "forwarder", cfg)
	if err != nil {
		m.rollbackProxyLocked(ctx, prevBound)
		return fmt.Errorf("create forwarder core: %w", err)
	}
	if err := fwd.Start(ctx); err != nil {
		_ = fwd.Close()
		m.rollbackProxyLocked(ctx, prevBound)
		return fmt.Errorf("start forwarder core: %w", err)
	}

	m.fwd = fwd
	m.state = StateVPN
	return nil
}

// StopForwarder leaves VPN mode: it tears down the forwarder and unbinds the
// proxy (back to riding the default route). No-op if not in VPN mode.
func (m *Manager) StopForwarder(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateVPN {
		return nil
	}
	return m.teardownForwarderLocked(ctx)
}

// NotifyForwarderExited handles an unexpected forwarder exit (crash): drop to
// proxy-only with the proxy unbound. No-op if not in VPN mode.
func (m *Manager) NotifyForwarderExited(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateVPN {
		return nil
	}
	return m.teardownForwarderLocked(ctx)
}

// teardownForwarderLocked closes the forwarder, scrubs any leftover TUN device
// and routes (so a connecting Cisco gets a clean routing table), and restores an
// unbound proxy.
func (m *Manager) teardownForwarderLocked(ctx context.Context) error {
	if m.fwd != nil {
		_ = m.fwd.Close()
		m.fwd = nil
	}
	// Belt-and-suspenders: remove our own orphaned utun/routes immediately so
	// they can't conflict with Cisco's route installation.
	_ = m.routes.CleanupOrphans()
	if err := m.recreateProxyLocked(ctx, ""); err != nil {
		m.state = StateStopped
		return fmt.Errorf("rebind proxy after VPN stop: %w", err)
	}
	m.state = StateProxyOnly
	return nil
}

// SuspendForCisco fully stands down — closes the forwarder AND the proxy and
// scrubs our routes — so a connecting Cisco gets a completely quiet routing
// table (no teardown/reconnect churn racing its route installation). The manager
// keeps its config builder so ResumeProxy can bring the proxy back.
func (m *Manager) SuspendForCisco(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fwd != nil {
		_ = m.fwd.Close()
		m.fwd = nil
	}
	if m.proxy != nil {
		_ = m.proxy.Close()
		m.proxy = nil
	}
	m.bound = ""
	_ = m.routes.CleanupOrphans()
	m.state = StateSuspended
	return nil
}

// ResumeProxy brings the proxy back (unbound, riding the now-stable default
// route / Cisco) after a Cisco-yield suspension. No-op unless suspended.
func (m *Manager) ResumeProxy(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateSuspended || m.proxy != nil {
		return nil
	}
	if err := m.startProxyLocked(ctx, ""); err != nil {
		return err
	}
	m.state = StateProxyOnly
	return nil
}

// BindProxyToPhysical rebinds the proxy's egress to physIface while STAYING in
// proxy-only mode, so its upstream dial escapes a third-party VPN (Cisco) that
// owns the default route — the primary coexistence strategy. No-op unless the
// proxy is running in proxy-only mode and the target differs from the current
// bind. On failure it rolls back to the previous (usually unbound) proxy so the
// caller can fall back to riding the default route, and returns the error.
func (m *Manager) BindProxyToPhysical(ctx context.Context, physIface string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateProxyOnly || m.proxy == nil {
		return nil
	}
	if physIface == "" {
		return fmt.Errorf("bind proxy for cisco: no physical interface")
	}
	if m.bound == physIface {
		return nil // already pinned to this NIC
	}
	prevBound := m.bound
	if err := m.recreateProxyLocked(ctx, physIface); err != nil {
		m.rollbackProxyLocked(ctx, prevBound) // restore the working unbound proxy
		return fmt.Errorf("bind proxy to %s: %w", physIface, err)
	}
	return nil
}

// UnbindProxy releases any physical bind and re-dials the proxy over the current
// default route (rule c: Cisco disconnected). No-op unless in proxy-only mode.
func (m *Manager) UnbindProxy(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateProxyOnly {
		return nil
	}
	if err := m.recreateProxyLocked(ctx, ""); err != nil {
		m.state = StateStopped
		return fmt.Errorf("unbind proxy: %w", err)
	}
	return nil
}

// RefreshProxy re-dials the proxy upstream by recreating it with the same
// binding (rule c: reconnect after Cisco turns off). Accepts the sub-second
// listener gap (D3/R2).
func (m *Manager) RefreshProxy(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proxy == nil {
		return fmt.Errorf("refresh: proxy not running")
	}
	return m.recreateProxyLocked(ctx, m.bound)
}

// Shutdown closes both cores. Returns the first error encountered.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var firstErr error
	if m.fwd != nil {
		if err := m.fwd.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		m.fwd = nil
	}
	if m.proxy != nil {
		if err := m.proxy.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		m.proxy = nil
	}
	m.state = StateStopped
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

// --- locked helpers ---

func (m *Manager) startProxyLocked(ctx context.Context, physIface string) error {
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
	m.proxy = c
	m.bound = physIface
	return nil
}

// recreateProxyLocked closes the current proxy and starts a fresh one bound to
// physIface. On failure m.proxy is left nil.
func (m *Manager) recreateProxyLocked(ctx context.Context, physIface string) error {
	if m.proxy != nil {
		_ = m.proxy.Close()
		m.proxy = nil
	}
	return m.startProxyLocked(ctx, physIface)
}

// rollbackProxyLocked best-effort restores a working proxy-only state after a
// failed VPN enable.
func (m *Manager) rollbackProxyLocked(ctx context.Context, prevBound string) {
	if err := m.recreateProxyLocked(ctx, prevBound); err != nil {
		m.state = StateStopped
		return
	}
	m.state = StateProxyOnly
}
