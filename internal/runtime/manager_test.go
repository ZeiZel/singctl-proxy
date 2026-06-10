package runtime

import (
	"context"
	"errors"
	"testing"

	"singctl/internal/core"
)

func newTestManager(t *testing.T) (*Manager, *core.FakeFactory, *fakeBuilder, *fakeProber, *fakeRoutes) {
	t.Helper()
	ff := core.NewFakeFactory()
	b := &fakeBuilder{}
	p := &fakeProber{iface: "en0"}
	r := &fakeRoutes{}
	return NewManager(ff.Factory(), b, p, r), ff, b, p, r
}

func TestManager_StartProxy_Idempotent_AndRunsCleanupFirst(t *testing.T) {
	m, ff, _, _, r := newTestManager(t)
	ctx := context.Background()

	if err := m.StartProxy(ctx); err != nil {
		t.Fatalf("StartProxy: %v", err)
	}
	if err := m.StartProxy(ctx); err != nil {
		t.Fatalf("StartProxy (2nd): %v", err)
	}
	if r.cleanupCalls != 1 {
		t.Errorf("CleanupOrphans calls = %d, want 1 (idempotent)", r.cleanupCalls)
	}
	if got := ff.BuiltFor("proxy"); len(got) != 1 {
		t.Errorf("proxy cores built = %d, want 1", len(got))
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only", m.State())
	}
	if m.BoundInterface() != "" {
		t.Errorf("proxy-only must be unbound, got %q", m.BoundInterface())
	}
}

func TestManager_EnableVPN_RebindsProxyAndStartsForwarder(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	if err := m.StartProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.StartForwarder(ctx); err != nil {
		t.Fatalf("StartForwarder: %v", err)
	}
	if m.State() != StateVPN {
		t.Fatalf("state = %v, want vpn", m.State())
	}
	if m.BoundInterface() != "en0" {
		t.Errorf("proxy should be bound to en0 in VPN mode, got %q", m.BoundInterface())
	}
	// Proxy was recreated with the bind (two proxy cores: unbound then en0).
	proxies := ff.BuiltFor("proxy")
	if len(proxies) != 2 {
		t.Fatalf("proxy cores built = %d, want 2 (recreated with bind)", len(proxies))
	}
	if string(proxies[0].Config) != `{"proxy":""}` || string(proxies[1].Config) != `{"proxy":"en0"}` {
		t.Errorf("proxy configs = %q,%q; want unbound then en0", proxies[0].Config, proxies[1].Config)
	}
	// First proxy core must have been closed; second running.
	if proxies[0].Running() {
		t.Error("old (unbound) proxy core should be closed after rebind")
	}
	if !proxies[1].Running() {
		t.Error("rebound proxy core should be running")
	}
	if fwds := ff.BuiltFor("forwarder"); len(fwds) != 1 || !fwds[0].Running() {
		t.Errorf("forwarder core: built=%d running=%v, want 1 running", len(fwds), len(fwds) == 1 && fwds[0].Running())
	}
}

func TestManager_EnableVPN_Idempotent(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)
	if err := m.StartForwarder(ctx); err != nil {
		t.Fatalf("double StartForwarder: %v", err)
	}
	if got := ff.BuiltFor("forwarder"); len(got) != 1 {
		t.Errorf("forwarder cores = %d, want 1 (idempotent)", len(got))
	}
}

func TestManager_StartForwarder_StartFailure_RollsBack(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	if err := m.StartProxy(ctx); err != nil {
		t.Fatal(err)
	}
	ff.NewErrOn["forwarder"] = errors.New("tun create failed")

	if err := m.StartForwarder(ctx); err == nil {
		t.Fatal("expected StartForwarder to fail")
	}
	// Rolled back: no forwarder, proxy restored to unbound proxy-only.
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only after rollback", m.State())
	}
	if m.BoundInterface() != "" {
		t.Errorf("proxy should be unbound after rollback, got %q", m.BoundInterface())
	}
	if got := ff.BuiltFor("forwarder"); len(got) != 0 {
		t.Errorf("forwarder cores running = %d, want 0", len(got))
	}
	// A running proxy must remain.
	proxies := ff.BuiltFor("proxy")
	last := proxies[len(proxies)-1]
	if !last.Running() {
		t.Error("a working proxy must remain after rollback")
	}
}

func TestManager_DisableVPN_UnbindsProxy(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)

	if err := m.StopForwarder(ctx); err != nil {
		t.Fatalf("StopForwarder: %v", err)
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only", m.State())
	}
	if m.BoundInterface() != "" {
		t.Errorf("proxy should be unbound after VPN stop, got %q", m.BoundInterface())
	}
	if fwds := ff.BuiltFor("forwarder"); fwds[0].Running() {
		t.Error("forwarder core should be closed after StopForwarder")
	}
}

func TestManager_ForwarderCrash_FallsBackToProxyOnly(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)

	if err := m.NotifyForwarderExited(ctx); err != nil {
		t.Fatalf("NotifyForwarderExited: %v", err)
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only after crash", m.State())
	}
	if m.BoundInterface() != "" {
		t.Errorf("proxy should be unbound after crash fallback, got %q", m.BoundInterface())
	}
	if fwds := ff.BuiltFor("forwarder"); fwds[0].Running() {
		t.Error("crashed forwarder core should be closed")
	}
}

func TestManager_RefreshProxy_RecreatesSameBinding(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)

	if err := m.RefreshProxy(ctx); err != nil {
		t.Fatalf("RefreshProxy: %v", err)
	}
	proxies := ff.BuiltFor("proxy")
	if len(proxies) != 2 {
		t.Fatalf("proxy cores = %d, want 2 (refresh recreates)", len(proxies))
	}
	if proxies[0].Running() {
		t.Error("old proxy core should be closed after refresh")
	}
	if !proxies[1].Running() || string(proxies[1].Config) != `{"proxy":""}` {
		t.Error("refreshed proxy should be running with same (unbound) config")
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only", m.State())
	}
}

func TestManager_Shutdown_ClosesBoth(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)

	if err := m.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if m.State() != StateStopped {
		t.Errorf("state = %v, want stopped", m.State())
	}
	for _, c := range ff.Built {
		if c.Running() {
			t.Errorf("core %q still running after shutdown", c.Label)
		}
	}
}

func TestManager_StartForwarder_RequiresProxy(t *testing.T) {
	m, _, _, _, _ := newTestManager(t)
	if err := m.StartForwarder(context.Background()); err == nil {
		t.Fatal("StartForwarder without a running proxy should error")
	}
}

func TestManager_SuspendForCisco_StopsBoth(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)
	if err := m.SuspendForCisco(ctx); err != nil {
		t.Fatal(err)
	}
	if m.State() != StateSuspended {
		t.Errorf("state = %v, want suspended", m.State())
	}
	for _, c := range ff.Built {
		if c.Running() {
			t.Errorf("core %q still running after suspend (must yield fully to Cisco)", c.Label)
		}
	}
	if m.BoundInterface() != "" {
		t.Error("suspend must unbind the proxy")
	}
}

func TestManager_ResumeProxy_FromSuspended(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	_ = m.StartProxy(ctx)
	_ = m.StartForwarder(ctx)
	_ = m.SuspendForCisco(ctx)
	if err := m.ResumeProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only", m.State())
	}
	if m.BoundInterface() != "" {
		t.Error("resumed proxy must be unbound (rides Cisco)")
	}
	proxies := ff.BuiltFor("proxy")
	if !proxies[len(proxies)-1].Running() {
		t.Error("resumed proxy core should be running")
	}
}

func TestManager_ResumeProxy_NoopWhenNotSuspended(t *testing.T) {
	m, _, _, _, _ := newTestManager(t)
	if err := m.ResumeProxy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.State() != StateStopped {
		t.Errorf("state = %v, want stopped (resume only from suspended)", m.State())
	}
}
