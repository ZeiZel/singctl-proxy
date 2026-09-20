package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

func TestManager_BindProxyToPhysical_CiscoCoexistence(t *testing.T) {
	m, ff, _, _, _ := newTestManager(t)
	ctx := context.Background()
	if err := m.StartProxy(ctx); err != nil {
		t.Fatal(err)
	}
	// Cisco appears while proxy-only: pin egress to the physical NIC (bypass),
	// staying in proxy-only mode (no forwarder).
	if err := m.BindProxyToPhysical(ctx, "en0"); err != nil {
		t.Fatalf("BindProxyToPhysical: %v", err)
	}
	if m.State() != StateProxyOnly {
		t.Errorf("state = %v, want proxy-only (bind must not enter VPN)", m.State())
	}
	if m.BoundInterface() != "en0" {
		t.Errorf("bound = %q, want en0", m.BoundInterface())
	}
	proxies := ff.BuiltFor("proxy")
	if len(proxies) != 2 || string(proxies[1].Config) != `{"proxy":"en0"}` {
		t.Fatalf("want proxy recreated bound to en0, got %d cores (last=%q)", len(proxies), lastCfg(proxies))
	}
	// Idempotent: binding to the same NIC again is a no-op.
	if err := m.BindProxyToPhysical(ctx, "en0"); err != nil {
		t.Fatal(err)
	}
	if got := ff.BuiltFor("proxy"); len(got) != 2 {
		t.Errorf("rebind to same NIC must be a no-op, cores = %d, want 2", len(got))
	}
	// Cisco leaves: unbind + re-dial over the restored default route.
	if err := m.UnbindProxy(ctx); err != nil {
		t.Fatalf("UnbindProxy: %v", err)
	}
	if m.BoundInterface() != "" {
		t.Errorf("after unbind bound = %q, want empty", m.BoundInterface())
	}
	if got := ff.BuiltFor("proxy"); len(got) != 3 || string(got[2].Config) != `{"proxy":""}` {
		t.Errorf("unbind should re-dial unbound, cores = %d (last=%q)", len(got), lastCfg(ff.BuiltFor("proxy")))
	}
}

func TestManager_BindProxyToPhysical_FallbackContract(t *testing.T) {
	ctx := context.Background()

	// No-op (no error) when not in proxy-only mode, so the executor's fallback
	// path never sees a spurious failure before the proxy is up.
	m, _, _, _, _ := newTestManager(t)
	if err := m.BindProxyToPhysical(ctx, "en0"); err != nil {
		t.Fatalf("bind before StartProxy should be a no-op, got %v", err)
	}

	// An empty iface errors (so the caller keeps the unbound proxy) but leaves
	// the running proxy intact.
	m2, _, _, _, _ := newTestManager(t)
	if err := m2.StartProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m2.BindProxyToPhysical(ctx, ""); err == nil {
		t.Error("empty iface should error so the caller falls back to riding the default route")
	}
	if m2.State() != StateProxyOnly || m2.BoundInterface() != "" {
		t.Errorf("rejected bind must leave proxy unbound+running, state=%v bound=%q", m2.State(), m2.BoundInterface())
	}
}

func lastCfg(cores []*core.FakeCore) string {
	if len(cores) == 0 {
		return ""
	}
	return string(cores[len(cores)-1].Config)
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

// TestManager_StartForwarder_TimesOutThenStopStillWorks is F2 item 4's core
// regression test: a wedged forwarder start must not hang the caller forever,
// and — because the manager no longer holds its lock across the slow
// core.Start call — a subsequent Shutdown ("MODE off") must succeed
// immediately instead of queuing behind the still-stuck goroutine.
func TestManager_StartForwarder_TimesOutThenStopStillWorks(t *testing.T) {
	ff := core.NewFakeFactory()
	blocker := &blockingCore{} // stop == nil: Start never returns
	factory := func(ctx context.Context, label string, cfg []byte) (core.Core, error) {
		if label == "forwarder" {
			return blocker, nil
		}
		return ff.Factory()(ctx, label, cfg)
	}
	b := &fakeBuilder{}
	p := &fakeProber{iface: "en0"}
	r := &fakeRoutes{}
	m := NewManager(factory, b, p, r)
	m.TransitionTimeout = 50 * time.Millisecond
	ctx := context.Background()

	if err := m.StartProxy(ctx); err != nil {
		t.Fatalf("StartProxy: %v", err)
	}

	start := time.Now()
	err := m.StartForwarder(ctx)
	if err == nil {
		t.Fatal("StartForwarder against a wedged forwarder core should return an error, not hang")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("error = %q, want a timeout error", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("StartForwarder took %s, want roughly TransitionTimeout (50ms)", elapsed)
	}

	// The manager must be recoverable: a subsequent Stop (Shutdown, what MODE
	// off drives) must not be wedged behind the still-blocked forwarder start.
	done := make(chan error, 1)
	go func() { done <- m.Shutdown(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown after a timed-out transition: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung behind the stuck forwarder start — the lock was held across the slow call")
	}
	if got := m.State(); got != StateStopped {
		t.Errorf("state after Shutdown = %v, want stopped", got)
	}

	// Fully recoverable: a fresh StartProxy afterwards must still work.
	if err := m.StartProxy(ctx); err != nil {
		t.Fatalf("StartProxy after recovery: %v", err)
	}
	if got := m.State(); got != StateProxyOnly {
		t.Errorf("state after post-recovery StartProxy = %v, want proxy-only", got)
	}
}

// TestManager_StopForwarder_TimesOut covers the teardown half of item 4 (a
// stuck Close during VPN -> off must also time out rather than hang).
func TestManager_StopForwarder_TimesOut(t *testing.T) {
	ff := core.NewFakeFactory()
	factory := func(ctx context.Context, label string, cfg []byte) (core.Core, error) {
		if label == "forwarder" {
			return &closeBlockingCore{}, nil
		}
		return ff.Factory()(ctx, label, cfg)
	}
	b := &fakeBuilder{}
	p := &fakeProber{iface: "en0"}
	r := &fakeRoutes{}
	m := NewManager(factory, b, p, r)
	m.TransitionTimeout = 50 * time.Millisecond
	ctx := context.Background()

	if err := m.StartProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.StartForwarder(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.StopForwarder(ctx); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("StopForwarder against a wedged Close = %v, want a timeout error", err)
	}

	done := make(chan error, 1)
	go func() { done <- m.Shutdown(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Shutdown after a timed-out teardown: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown hung behind the stuck forwarder teardown")
	}
}

// closeBlockingCore starts instantly but blocks forever in Close (simulating
// a wedged teardown rather than a wedged start).
type closeBlockingCore struct{}

func (c *closeBlockingCore) Start(ctx context.Context) error { return nil }
func (c *closeBlockingCore) Close() error                    { select {} }
