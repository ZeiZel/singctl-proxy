package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"singctl/internal/core"
	"singctl/internal/monitor"
	"singctl/internal/netext"
	"singctl/internal/notify"
	"singctl/internal/policy"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/runtime"
	"singctl/internal/types"
)

type fakeProber struct{}

func (fakeProber) PhysicalDefault() (string, error) { return "en0", nil }

type fakeRoutes struct{}

func (fakeRoutes) CleanupOrphans() error { return nil }

const validLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=k&sni=cursor.com&fp=chrome#t"

func newExecutor() (*Executor, chan any) {
	notes := make(chan any, 32)
	ff := core.NewFakeFactory()
	e := NewExecutor(ff.Factory(), fakeProber{}, fakeRoutes{}, notes)
	// The production proxiedAppsPath lives under root-owned /Library — swap in
	// a temp-dir-backed store so the persistent per-app store's tests don't
	// need root (white-box: appStore is unexported, only reachable from tests
	// in this package).
	dir, err := os.MkdirTemp("", "singctl-appstore-test")
	if err == nil {
		e.store = newAppStore(filepath.Join(dir, "proxied-apps.json"))
	}
	return e, notes
}

func TestExecutor_LoadLink_Invalid(t *testing.T) {
	e, _ := newExecutor()
	if err := e.LoadLink(context.Background(), "not-a-link"); err == nil {
		t.Fatal("expected error for invalid link")
	}
}

func TestExecutor_LoadLink_DoesNotAutoStart(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	if err := e.LoadLink(ctx, validLink); err != nil {
		t.Fatal(err)
	}
	if e.manager().State() != runtime.StateStopped {
		t.Errorf("after LoadLink state = %v, want stopped (nothing auto-starts)", e.manager().State())
	}
}

func TestExecutor_EnableProxy_VPN_Switch_Stop(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)

	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if e.manager().State() != runtime.StateProxyOnly {
		t.Errorf("EnableProxy -> %v, want proxy-only", e.manager().State())
	}
	if err := e.EnableVPN(ctx); err != nil {
		t.Fatal(err)
	}
	if e.manager().State() != runtime.StateVPN {
		t.Errorf("EnableVPN -> %v, want vpn", e.manager().State())
	}
	// EnableProxy from VPN drops back to proxy-only.
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	if e.manager().State() != runtime.StateProxyOnly {
		t.Errorf("EnableProxy from VPN -> %v, want proxy-only", e.manager().State())
	}
	if err := e.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if e.manager().State() != runtime.StateStopped {
		t.Errorf("Stop -> %v, want stopped", e.manager().State())
	}
}

func TestExecutor_EnableWithoutLinkErrors(t *testing.T) {
	e, _ := newExecutor()
	if err := e.EnableProxy(context.Background()); err == nil {
		t.Error("EnableProxy without a loaded link must error")
	}
	if err := e.EnableVPN(context.Background()); err == nil {
		t.Error("EnableVPN without a loaded link must error")
	}
}

func TestExecutor_LoadLink_RelinkShutsDownOld(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	_ = e.EnableProxy(ctx)
	other := "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=tls#x"
	if err := e.LoadLink(ctx, other); err != nil {
		t.Fatalf("relink: %v", err)
	}
	if e.manager().State() != runtime.StateStopped {
		t.Errorf("after relink state = %v, want stopped (fresh, not started)", e.manager().State())
	}
}

func TestExecutor_Apply_CiscoUp_Suspends_StaysDownUntilCiscoGone(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	_ = e.EnableVPN(ctx)

	// Cisco appears -> fully suspend (no sing-box activity for AnyConnect).
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: true},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActFailClosed, policy.ActStopForwarder, policy.ActNotify}},
	})
	if e.manager().State() != runtime.StateSuspended {
		t.Fatalf("after Cisco up state = %v, want suspended", e.manager().State())
	}
	// Steady active -> stays suspended (no resume while Cisco present).
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: true},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActNone}},
	})
	if e.manager().State() != runtime.StateSuspended {
		t.Errorf("must stay suspended while Cisco active, got %v", e.manager().State())
	}
	// Cisco gone -> resume proxy.
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: false},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActRefreshProxy}},
	})
	if e.manager().State() != runtime.StateProxyOnly {
		t.Errorf("after Cisco gone state = %v, want proxy-only", e.manager().State())
	}
}

func TestExecutor_Apply_NotifiesUIWithRunMode(t *testing.T) {
	e, notes := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	_ = e.EnableVPN(ctx)
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: true, PhysicalIface: "en0"},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActFailClosed, policy.ActStopForwarder}},
	})
	var sawStatus, sawNet bool
	for len(notes) > 0 {
		switch mm := (<-notes).(type) {
		case notify.StatusMsg:
			sawStatus = true
			if mm.Mode != notify.RunOff {
				t.Errorf("status mode = %v, want OFF (suspended)", mm.Mode)
			}
		case notify.NetStateMsg:
			sawNet = true
			if !mm.Cisco {
				t.Error("net state should report Cisco active")
			}
		}
	}
	if !sawStatus || !sawNet {
		t.Errorf("expected StatusMsg + NetStateMsg (status=%v net=%v)", sawStatus, sawNet)
	}
}

func TestExecutor_Apply_CiscoBypass_BindsPhysical_AndUnbinds(t *testing.T) {
	e, notes := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	_ = e.EnableProxy(ctx) // proxy-only, unbound (rides default route)

	if e.ProxyBoundToPhysical() {
		t.Fatal("proxy should start unbound")
	}

	// Cisco appears while proxy-only -> pin egress to the physical NIC (bypass),
	// staying in proxy-only mode so AnyConnect can still connect.
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: true, PhysicalIface: "en0"},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActBindPhysical}},
	})
	if e.manager().State() != runtime.StateProxyOnly {
		t.Fatalf("bind must stay proxy-only, got %v", e.manager().State())
	}
	if !e.ProxyBoundToPhysical() {
		t.Fatal("proxy egress should be pinned to the physical NIC after bind")
	}
	if cisco, bypass, phys := e.CoexistStatus(); !cisco || !bypass || phys != "en0" {
		t.Errorf("CoexistStatus = (%v,%v,%q), want (true,true,en0)", cisco, bypass, phys)
	}

	// Cisco leaves -> unbind + re-dial over the restored default route.
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{CiscoActive: false, PhysicalIface: "en0"},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActUnbindProxy}},
	})
	if e.ProxyBoundToPhysical() {
		t.Error("proxy should be unbound after Cisco leaves")
	}
	if _, bypass, _ := e.CoexistStatus(); bypass {
		t.Error("bypass should be cleared after Cisco leaves")
	}

	// The bind/unbind transitions must have surfaced a status note to the UI.
	var sawStatus bool
	for len(notes) > 0 {
		if _, ok := (<-notes).(notify.StatusMsg); ok {
			sawStatus = true
		}
	}
	if !sawStatus {
		t.Error("expected a StatusMsg for the coexistence transition")
	}
}

func TestExecutor_Apply_RefreshFromProxyOnly(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	_ = e.EnableProxy(ctx)
	e.Apply(ctx, monitor.Event{
		NetState: types.NetState{},
		Decision: policy.DecisionResult{Actions: []policy.Action{policy.ActRefreshProxy}},
	})
	if e.manager().State() != runtime.StateProxyOnly {
		t.Errorf("refresh from proxy-only -> %v, want proxy-only", e.manager().State())
	}
}

func TestConsoleRing_AppendAndSince(t *testing.T) {
	e, _ := newExecutor()
	for i := 0; i < 3; i++ {
		e.appendConsole(procproxy.OutputLine{PID: 42, App: "zen", Stream: "stdout", Text: "line"})
	}
	all := e.ConsoleSince(0)
	if len(all) != 3 {
		t.Fatalf("ConsoleSince(0) = %d entries, want 3", len(all))
	}
	if all[0].ID != 1 || all[2].ID != 3 || all[0].App != "zen" {
		t.Errorf("entries mis-tagged: %+v", all)
	}
	// Polling from the last seen id returns only newer lines.
	if got := e.ConsoleSince(all[2].ID); len(got) != 0 {
		t.Errorf("ConsoleSince(last) should be empty, got %d", len(got))
	}
	e.appendConsole(procproxy.OutputLine{PID: 42, App: "zen", Stream: "exit", Text: "[exited]"})
	if got := e.ConsoleSince(all[2].ID); len(got) != 1 || got[0].Stream != "exit" {
		t.Errorf("ConsoleSince should return the one new entry, got %+v", got)
	}
}

func TestExecutor_ListRouted(t *testing.T) {
	e, _ := newExecutor()
	// No router built yet → empty (must not lazily build one just to list).
	if got := e.ListRouted(); len(got) != 0 {
		t.Fatalf("ListRouted before any routing = %v, want empty", got)
	}
	// Inject a fake router (white-box) and confirm its PIDs surface.
	fake := &procproxy.FakeRouter{Routed: []int{111, 222}}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = fake, true
	e.cfgMu.Unlock()
	got := e.ListRouted()
	if len(got) != 2 || got[0] != 111 || got[1] != 222 {
		t.Errorf("ListRouted = %v, want [111 222]", got)
	}
}

// fakeAppLister is a proclist.Lister stub for the Application tests: it returns
// a fixed "running process" list without shelling out to lsof/ps.
type fakeAppLister struct{ apps []proclist.App }

func (f fakeAppLister) List(context.Context) ([]proclist.App, error) { return f.apps, nil }

// setFakeLister injects a fake process lister (white-box). It must also mark
// listerOnce as done, otherwise listProcRows's lazy sync.Once init would
// clobber e.lister with the real proclist.NewLister() on first use.
func setFakeLister(e *Executor, apps []proclist.App) {
	e.lister = fakeAppLister{apps: apps}
	e.listerOnce.Do(func() {})
}

// withFakeBundleResolver overrides bundleIDForPID for the duration of a test
// (restored via t.Cleanup), so Application grouping/routing can be tested
// without a real .app process.
func withFakeBundleResolver(t *testing.T, resolve func(pid int) string) {
	t.Helper()
	old := bundleIDForPID
	bundleIDForPID = resolve
	t.Cleanup(func() { bundleIDForPID = old })
}

func TestExecutor_ListApplications_GroupsByBundleID(t *testing.T) {
	e, _ := newExecutor()
	setFakeLister(e, []proclist.App{
		{PID: 100, Name: "Cursor"},
		{PID: 200, Name: "Cursor Helper (Renderer)"}, // same bundle, separate proclist root
		{PID: 300, Name: "sshd"},                     // not a .app -> no bundle id
	})
	withFakeBundleResolver(t, func(pid int) string {
		if pid == 100 || pid == 200 {
			return "com.cursor.app"
		}
		return ""
	})

	apps, err := e.ListApplications(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 1 {
		t.Fatalf("ListApplications = %+v, want 1 app", apps)
	}
	got := apps[0]
	if got.BundleID != "com.cursor.app" || !got.Running || len(got.PIDs) != 2 {
		t.Errorf("ListApplications[0] = %+v, want bundle com.cursor.app, running, 2 PIDs", got)
	}
}

func TestExecutor_RouteApp_NotRunning(t *testing.T) {
	e, _ := newExecutor()
	if err := e.RouteApp(context.Background(), "com.cursor.app"); err != errProxyNotRunning {
		t.Errorf("RouteApp before proxy: got %v, want errProxyNotRunning", err)
	}
}

func TestExecutor_RouteApp_RoutesEveryPIDForTheBundle(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	setFakeLister(e, []proclist.App{
		{PID: 100, Name: "Cursor"},
		{PID: 200, Name: "Cursor Helper"},
	})
	withFakeBundleResolver(t, func(int) string { return "com.cursor.app" })

	fake := &procproxy.FakeRouter{}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = fake, true
	e.cfgMu.Unlock()

	if err := e.RouteApp(ctx, "com.cursor.app"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Added) != 2 {
		t.Errorf("RouteApp Added = %v, want 2 PIDs added (100, 200)", fake.Added)
	}
}

func TestExecutor_RouteApp_NotCurrentlyRunning(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	setFakeLister(e, nil)

	if err := e.RouteApp(ctx, "com.nope"); err == nil {
		t.Error("RouteApp for a bundle with no running PIDs: expected an error")
	}
}

func TestExecutor_UnrouteApp_UsesBundleRouterLookup(t *testing.T) {
	e, _ := newExecutor()
	fake := &procproxy.FakeRouter{
		Routed:       []int{100, 200, 300},
		BundlesByPID: map[int]string{100: "com.cursor.app", 200: "com.cursor.app", 300: "com.other.app"},
	}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = fake, true
	e.cfgMu.Unlock()

	if err := e.UnrouteApp(context.Background(), "com.cursor.app"); err != nil {
		t.Fatal(err)
	}
	if len(fake.Unrouted) != 2 {
		t.Fatalf("Unrouted = %v, want 2 PIDs unrouted (100, 200)", fake.Unrouted)
	}
	for _, pid := range fake.Unrouted {
		if pid != 100 && pid != 200 {
			t.Errorf("UnrouteApp unrouted unexpected pid %d", pid)
		}
	}
}

func TestExecutor_ListRoutedApps(t *testing.T) {
	e, _ := newExecutor()
	if got := e.ListRoutedApps(); len(got) != 0 {
		t.Fatalf("ListRoutedApps before routing = %v, want empty", got)
	}
	fake := &procproxy.FakeRouter{BundlesByPID: map[int]string{100: "com.cursor.app"}}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = fake, true
	e.cfgMu.Unlock()
	got := e.ListRoutedApps()
	if len(got) != 1 || got[0] != "com.cursor.app" {
		t.Errorf("ListRoutedApps = %v, want [com.cursor.app]", got)
	}
}

// injectFakeController swaps the Executor's controller for an in-memory fake
// (white-box), so RecomputeAppTargets can be asserted on without touching the
// real system extension / App Group container.
func injectFakeController(e *Executor, present bool) *netext.FakeController {
	fake := netext.NewFake(present)
	e.cfgMu.Lock()
	e.ctrl, e.ctrlBuilt = fake, true
	e.cfgMu.Unlock()
	return fake
}

func TestExecutor_ListProxiedApps_DerivesRunning(t *testing.T) {
	e, _ := newExecutor()
	_ = e.store.Upsert("com.cursor.app", "Cursor", true)
	_ = e.store.Upsert("com.zen.app", "Zen", false)
	setFakeLister(e, []proclist.App{{PID: 100, Name: "Cursor"}})
	withFakeBundleResolver(t, func(pid int) string {
		if pid == 100 {
			return "com.cursor.app"
		}
		return ""
	})

	apps, err := e.ListProxiedApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 {
		t.Fatalf("ListProxiedApps = %+v, want 2 entries", apps)
	}
	byID := map[string]ProxiedApp{}
	for _, a := range apps {
		byID[a.BundleID] = a
	}
	if !byID["com.cursor.app"].Running {
		t.Error("com.cursor.app has a live PID, want Running=true")
	}
	if byID["com.zen.app"].Running {
		t.Error("com.zen.app has no live PID, want Running=false")
	}
	if !byID["com.cursor.app"].Enabled || byID["com.zen.app"].Enabled {
		t.Errorf("Enabled flags not preserved from the store: %+v", byID)
	}
}

func TestExecutor_SetProxiedAppEnabled_RecomputesTargets(t *testing.T) {
	e, _ := newExecutor()
	fake := injectFakeController(e, true)
	_ = e.store.Upsert("com.cursor.app", "Cursor", true)

	if err := e.SetProxiedAppEnabled("com.cursor.app", false); err != nil {
		t.Fatalf("SetProxiedAppEnabled: %v", err)
	}
	if got := fake.Targets(); len(got) != 0 {
		t.Errorf("Targets after disabling the only app = %v, want empty", got)
	}
	if err := e.SetProxiedAppEnabled("com.cursor.app", true); err != nil {
		t.Fatalf("SetProxiedAppEnabled: %v", err)
	}
	if got := fake.Targets(); len(got) != 1 || got[0] != "com.cursor.app" {
		t.Errorf("Targets after re-enabling = %v, want [com.cursor.app]", got)
	}
}

func TestExecutor_RemoveProxiedApp_UnroutesAndRecomputes(t *testing.T) {
	e, _ := newExecutor()
	injectFakeController(e, true)
	_ = e.store.Upsert("com.cursor.app", "Cursor", true)

	// FakeRouter.RoutedBundleIDs is a static snapshot of BundlesByPID (it
	// doesn't drop entries when Unroute is called, unlike the real
	// darwinRouter's refcounted bookkeeping) — leave it empty here so the
	// post-Remove recompute reflects only the store, and assert the live-PID
	// unroute call separately via Unrouted.
	router := &procproxy.FakeRouter{
		Routed:       []int{100},
		BundlesByPID: map[int]string{100: "com.cursor.app"},
	}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = router, true
	e.cfgMu.Unlock()

	if err := e.RemoveProxiedApp(context.Background(), "com.cursor.app"); err != nil {
		t.Fatalf("RemoveProxiedApp: %v", err)
	}
	if len(router.Unrouted) != 1 || router.Unrouted[0] != 100 {
		t.Errorf("Unrouted = %v, want [100]", router.Unrouted)
	}
	if got := e.store.List(); len(got) != 0 {
		t.Errorf("store after Remove = %v, want empty", got)
	}
}

func TestExecutor_RouteApp_PersistsToStoreAndRecomputes(t *testing.T) {
	e, _ := newExecutor()
	fake := injectFakeController(e, true)
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	setFakeLister(e, []proclist.App{{PID: 100, Name: "Cursor"}})
	withFakeBundleResolver(t, func(int) string { return "com.cursor.app" })
	e.cfgMu.Lock()
	e.router, e.routerBuilt = &procproxy.FakeRouter{}, true
	e.cfgMu.Unlock()

	if err := e.RouteApp(ctx, "com.cursor.app"); err != nil {
		t.Fatal(err)
	}
	got := e.store.List()
	if len(got) != 1 || got[0].BundleID != "com.cursor.app" || got[0].Name != "Cursor" || !got[0].Enabled {
		t.Errorf("store.List() = %+v, want one enabled com.cursor.app named Cursor", got)
	}
	if targets := fake.Targets(); len(targets) != 1 || targets[0] != "com.cursor.app" {
		t.Errorf("controller Targets = %v, want [com.cursor.app]", targets)
	}
}

func TestExecutor_LaunchProxiedApp_PersistsToStore(t *testing.T) {
	e, _ := newExecutor()
	fake := injectFakeController(e, true)
	ctx := context.Background()
	_ = e.LoadLink(ctx, validLink)
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	router := &procproxy.FakeRouter{NextPID: 555}
	e.cfgMu.Lock()
	e.router, e.routerBuilt = router, true
	e.cfgMu.Unlock()
	withFakeBundleResolver(t, func(pid int) string {
		if pid == 555 {
			return "com.cursor.app"
		}
		return ""
	})

	pid, err := e.LaunchProxiedApp(ctx, "/Applications/Cursor.app")
	if err != nil {
		t.Fatal(err)
	}
	if pid != 555 {
		t.Errorf("LaunchProxiedApp pid = %d, want 555", pid)
	}
	got := e.store.List()
	if len(got) != 1 || got[0].BundleID != "com.cursor.app" || !got[0].Enabled {
		t.Errorf("store.List() = %+v, want one enabled com.cursor.app", got)
	}
	if targets := fake.Targets(); len(targets) != 1 || targets[0] != "com.cursor.app" {
		t.Errorf("controller Targets = %v, want [com.cursor.app]", targets)
	}
}

func TestExecutor_RecomputeAppTargets_UnionsStoreAndRouted(t *testing.T) {
	e, _ := newExecutor()
	fake := injectFakeController(e, true)
	_ = e.store.Upsert("com.a", "A", true)                                      // enabled in the store, no live PID
	router := &procproxy.FakeRouter{BundlesByPID: map[int]string{100: "com.b"}} // routed, not store-enabled
	e.cfgMu.Lock()
	e.router, e.routerBuilt = router, true
	e.cfgMu.Unlock()

	if err := e.RecomputeAppTargets(); err != nil {
		t.Fatalf("RecomputeAppTargets: %v", err)
	}
	got := fake.Targets()
	if len(got) != 2 || got[0] != "com.a" || got[1] != "com.b" {
		t.Errorf("Targets = %v, want [com.a com.b] (union of store-enabled and routed)", got)
	}
}

func TestExecutor_Shutdown_ClearsTargetsButKeepsStore(t *testing.T) {
	e, _ := newExecutor()
	fake := injectFakeController(e, true)
	_ = e.store.Upsert("com.a", "A", true)
	if err := e.RecomputeAppTargets(); err != nil {
		t.Fatal(err)
	}
	if got := fake.Targets(); len(got) != 1 {
		t.Fatalf("precondition: Targets = %v, want 1 entry", got)
	}
	if err := e.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if got := fake.Targets(); len(got) != 0 {
		t.Errorf("Targets after Shutdown = %v, want empty", got)
	}
	if got := e.store.EnabledBundleIDs(); len(got) != 1 || got[0] != "com.a" {
		t.Errorf("store should still list com.a enabled after Shutdown: %v", got)
	}
}

func TestExecutor_TrafficSnapshot(t *testing.T) {
	e, _ := newExecutor()
	// Clash API disabled → zero snapshot, no error.
	if tr, err := e.TrafficSnapshot(context.Background()); err != nil || tr.Up != 0 || tr.Down != 0 {
		t.Fatalf("TrafficSnapshot (disabled) = %+v, %v; want zero/nil", tr, err)
	}
	// Point at a stub Clash API returning running totals.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connections" {
			_, _ = w.Write([]byte(`{"downloadTotal":4096,"uploadTotal":1024,"connections":[]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")
	tr, err := e.TrafficSnapshot(context.Background())
	if err != nil {
		t.Fatalf("TrafficSnapshot: %v", err)
	}
	if tr.Up != 1024 || tr.Down != 4096 {
		t.Errorf("TrafficSnapshot = %+v, want up 1024 down 4096", tr)
	}
}
