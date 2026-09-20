package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"singctl/internal/clashapi"
	"singctl/internal/core"
	"singctl/internal/firewall"
	"singctl/internal/monitor"
	"singctl/internal/netext"
	"singctl/internal/notify"
	"singctl/internal/policy"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/protocol/all"
	"singctl/internal/runtime"
	"singctl/internal/types"
)

// testRegistry is the real, default protocol registry — the same one
// production code builds from all.Registry().
var testRegistry = all.Registry()

type fakeProber struct{}

func (fakeProber) PhysicalDefault() (string, error) { return "en0", nil }

type fakeRoutes struct{}

func (fakeRoutes) CleanupOrphans() error { return nil }

const validLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=k&sni=cursor.com&fp=chrome#t"

func TestExecutor_ConnectionLogIsFlushedOnClose(t *testing.T) {
	e := NewExecutor(core.NewFakeFactory().Factory(), testRegistry, fakeProber{}, fakeRoutes{}, nil)
	path := filepath.Join(t.TempDir(), "singbox.log")
	e.SetLogPath(path)

	e.appendLog("first")
	e.appendLog("second")
	e.CloseLog()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "first\nsecond\n" {
		t.Fatalf("flushed log = %q", got)
	}
}

func newExecutor() (*Executor, chan any) {
	notes := make(chan any, 32)
	ff := core.NewFakeFactory()
	e := NewExecutor(ff.Factory(), testRegistry, fakeProber{}, fakeRoutes{}, notes)
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

// TestExecutor_AutostartMode_DefaultAndPersistence covers F2 item 2's wiring:
// the default is "off", SetAutostartMode validates + persists via the
// registered saver, and it round-trips through CurrentSettings/ApplySettings
// exactly like the other tunables (SETTINGS-GET/SETTINGS-SET).
func TestExecutor_AutostartMode_DefaultAndPersistence(t *testing.T) {
	e, _ := newExecutor()
	if got := e.AutostartMode(); got != "off" {
		t.Errorf("default autostart mode = %q, want off", got)
	}
	if got := e.CurrentSettings().AutostartMode; got != "off" {
		t.Errorf("CurrentSettings().AutostartMode = %q, want off", got)
	}

	var saved string
	e.SetAutostartSaver(func(m string) error { saved = m; return nil })
	if err := e.SetAutostartMode("vpn"); err != nil {
		t.Fatalf("SetAutostartMode: %v", err)
	}
	if e.AutostartMode() != "vpn" || saved != "vpn" {
		t.Errorf("AutostartMode()=%q saved=%q, want vpn/vpn", e.AutostartMode(), saved)
	}
	if err := e.SetAutostartMode("bogus"); err == nil {
		t.Error("an unknown autostart mode must be rejected")
	}

	// SETTINGS-SET (ApplySettings) with an empty AutostartMode ("" — a client
	// that predates F2) must leave the persisted value alone.
	if err := e.ApplySettings(context.Background(), notify.Settings{}); err != nil {
		t.Fatal(err)
	}
	if e.AutostartMode() != "vpn" {
		t.Errorf("ApplySettings with empty AutostartMode changed it to %q, want unchanged (vpn)", e.AutostartMode())
	}
	// A non-empty AutostartMode in SETTINGS-SET does update it.
	if err := e.ApplySettings(context.Background(), notify.Settings{AutostartMode: "proxy"}); err != nil {
		t.Fatal(err)
	}
	if e.AutostartMode() != "proxy" {
		t.Errorf("ApplySettings did not apply AutostartMode: got %q, want proxy", e.AutostartMode())
	}
}

// TestExecutor_ApplyAutostart is F2 items 2+3's core regression test: a
// failing autostart mode must never be fatal, and must leave the manager
// fully in "off" — never some partially-applied state — so the daemon (a
// LaunchDaemon with KeepAlive) stays up and a later MODE command can retry.
func TestExecutor_ApplyAutostart(t *testing.T) {
	cases := []struct {
		name         string
		mode         string // "" = default (off), never explicitly set
		forwarderErr error  // injects a StartForwarder failure (the reported "no physical interface" class of bug)
		wantState    runtime.State
		wantErr      bool
	}{
		{name: "default off does nothing", mode: "", wantState: runtime.StateStopped},
		{name: "explicit off does nothing", mode: "off", wantState: runtime.StateStopped},
		{name: "proxy applies", mode: "proxy", wantState: runtime.StateProxyOnly},
		{name: "vpn applies", mode: "vpn", wantState: runtime.StateVPN},
		{
			name: "failing vpn leaves off, no fatal", mode: "vpn",
			forwarderErr: fmt.Errorf("start forwarder: no physical interface detected"),
			wantState:    runtime.StateStopped, wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			notes := make(chan any, 32)
			ff := core.NewFakeFactory()
			if tc.forwarderErr != nil {
				ff.NewErrOn["forwarder"] = tc.forwarderErr
			}
			e := NewExecutor(ff.Factory(), testRegistry, fakeProber{}, fakeRoutes{}, notes)
			ctx := context.Background()
			if err := e.LoadLink(ctx, validLink); err != nil {
				t.Fatal(err)
			}
			if tc.mode != "" {
				if err := e.SetAutostartMode(tc.mode); err != nil {
					t.Fatalf("SetAutostartMode: %v", err)
				}
			}

			err := e.ApplyAutostart(ctx)
			if tc.wantErr && err == nil {
				t.Fatal("ApplyAutostart should have returned the underlying failure (for the caller to log)")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ApplyAutostart: %v", err)
			}
			if got := e.manager().State(); got != tc.wantState {
				t.Errorf("manager state after ApplyAutostart = %v, want %v", got, tc.wantState)
			}
		})
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

// multiLink builds a distinct vless share link (unique host, so it isn't
// deduped) labeled via its #fragment.
func multiLink(host, label string) string {
	return "vless://4ce58870-27d3-489b-87a0-3109db4fb919@" + host + ":443?security=tls#" + label
}

func loadMultiServer(t *testing.T, e *Executor, labels ...string) {
	t.Helper()
	links := make([]string, len(labels))
	for i, l := range labels {
		links[i] = multiLink(fmt.Sprintf("192.0.2.%d", i+1), l)
	}
	if err := e.LoadLink(context.Background(), strings.Join(links, "\n")); err != nil {
		t.Fatalf("LoadLink(%d servers): %v", len(labels), err)
	}
}

func TestExecutor_ProxyGroup_SingleServer_Unavailable(t *testing.T) {
	e, _ := newExecutor()
	loadMultiServer(t, e, "Solo")
	got, err := e.ProxyGroup(context.Background())
	if err != nil {
		t.Fatalf("ProxyGroup: %v", err)
	}
	if got.Available {
		t.Errorf("ProxyGroup single-server = %+v, want Available=false", got)
	}
}

func TestExecutor_ProxyGroup_NoLinksLoaded_Unavailable(t *testing.T) {
	e, _ := newExecutor()
	got, err := e.ProxyGroup(context.Background())
	if err != nil || got.Available {
		t.Errorf("ProxyGroup with nothing loaded = %+v, %v; want Available=false, nil", got, err)
	}
}

// clashProxiesServer stubs GET /proxies with the given JSON body and records
// every PUT /proxies/{group} it receives.
func clashProxiesServer(t *testing.T, proxiesJSON string, puts *[]struct{ Group, Name string }) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/proxies":
			_, _ = w.Write([]byte(proxiesJSON))
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/proxies/"):
			var body struct {
				Name string `json:"name"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if puts != nil {
				*puts = append(*puts, struct{ Group, Name string }{
					Group: strings.TrimPrefix(r.URL.Path, "/proxies/"), Name: body.Name,
				})
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestExecutor_ProxyGroup_ResolvesNamesAndEffectiveSelection(t *testing.T) {
	e, _ := newExecutor()
	loadMultiServer(t, e, "France 🇫🇷", "Poland", "Germany")

	// "proxy" is on auto; "auto" itself has settled on proxy-1 — Selected must
	// resolve through that second hop. proxy-2 carries no history entry, so
	// its delay is 0 (unknown), not a zero-value crash.
	const proxiesJSON = `{"proxies":{
		"proxy":{"type":"Selector","now":"auto","all":["auto","proxy-0","proxy-1","proxy-2"]},
		"auto":{"type":"URLTest","now":"proxy-1","all":["proxy-0","proxy-1","proxy-2"]},
		"proxy-0":{"history":[{"delay":120}]},
		"proxy-1":{"history":[{"delay":45}]}
	}}`
	srv := clashProxiesServer(t, proxiesJSON, nil)
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")

	got, err := e.ProxyGroup(context.Background())
	if err != nil {
		t.Fatalf("ProxyGroup: %v", err)
	}
	if !got.Available || !got.Auto || got.Selected != "proxy-1" {
		t.Fatalf("ProxyGroup = %+v, want Available+Auto=true, Selected=proxy-1", got)
	}
	if len(got.Members) != 3 {
		t.Fatalf("Members = %+v, want 3 entries", got.Members)
	}
	byTag := map[string]ProxyMember{}
	for _, m := range got.Members {
		byTag[m.Tag] = m
	}
	if m := byTag["proxy-0"]; m.Name != "France 🇫🇷" || m.Index != 0 || m.Delay != 120 {
		t.Errorf("proxy-0 = %+v, want name=France 🇫🇷 index=0 delay=120", m)
	}
	if m := byTag["proxy-1"]; m.Name != "Poland" || m.Index != 1 || m.Delay != 45 {
		t.Errorf("proxy-1 = %+v, want name=Poland index=1 delay=45", m)
	}
	if m := byTag["proxy-2"]; m.Name != "Germany" || m.Index != 2 || m.Delay != 0 {
		t.Errorf("proxy-2 = %+v, want name=Germany index=2 delay=0 (no history)", m)
	}
}

func TestExecutor_ProxyGroup_NameFallback_TagIndexOutOfRange(t *testing.T) {
	e, _ := newExecutor()
	// Only 2 servers loaded, but the live Clash report (racing a reload)
	// still carries a "proxy-2" member — the resolver must fall back to the
	// raw tag rather than panic on the out-of-range index.
	loadMultiServer(t, e, "France", "Poland")

	const proxiesJSON = `{"proxies":{
		"proxy":{"type":"Selector","now":"proxy-0","all":["auto","proxy-0","proxy-2"]},
		"auto":{"type":"URLTest","now":"proxy-0"},
		"proxy-0":{"history":[{"delay":10}]}
	}}`
	srv := clashProxiesServer(t, proxiesJSON, nil)
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")

	got, err := e.ProxyGroup(context.Background())
	if err != nil {
		t.Fatalf("ProxyGroup: %v", err)
	}
	if got.Auto {
		t.Errorf("Auto = true, want false (selector pinned to proxy-0)")
	}
	if got.Selected != "proxy-0" {
		t.Errorf("Selected = %q, want proxy-0", got.Selected)
	}
	byTag := map[string]ProxyMember{}
	for _, m := range got.Members {
		byTag[m.Tag] = m
	}
	if m := byTag["proxy-2"]; m.Name != "proxy-2" {
		t.Errorf("proxy-2 name = %q, want fallback to the raw tag %q", m.Name, "proxy-2")
	}
}

func TestExecutor_SelectProxy_SingleServer(t *testing.T) {
	e, _ := newExecutor()
	loadMultiServer(t, e, "Solo")
	if err := e.SelectProxy(context.Background(), "auto"); err == nil {
		t.Error("SelectProxy in single-server mode: expected an error")
	}
}

func TestExecutor_SelectProxy_RejectsUnknownTag(t *testing.T) {
	e, _ := newExecutor()
	loadMultiServer(t, e, "France", "Poland", "Germany")
	err := e.SelectProxy(context.Background(), "proxy-9")
	if err == nil {
		t.Fatal("SelectProxy(unknown tag): expected an error")
	}
	if !strings.Contains(err.Error(), "proxy-0") || !strings.Contains(err.Error(), "auto") {
		t.Errorf("error %q should list valid members (auto, proxy-0, …)", err)
	}
}

func TestExecutor_SelectProxy_IssuesCorrectClashCall(t *testing.T) {
	e, _ := newExecutor()
	loadMultiServer(t, e, "France", "Poland", "Germany")
	var puts []struct{ Group, Name string }
	srv := clashProxiesServer(t, `{"proxies":{}}`, &puts)
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")

	if err := e.SelectProxy(context.Background(), "auto"); err != nil {
		t.Fatalf("SelectProxy(auto): %v", err)
	}
	if err := e.SelectProxy(context.Background(), "proxy-1"); err != nil {
		t.Fatalf("SelectProxy(proxy-1): %v", err)
	}
	if len(puts) != 2 {
		t.Fatalf("PUT calls = %+v, want 2", puts)
	}
	if puts[0].Group != "proxy" || puts[0].Name != "auto" {
		t.Errorf("call 1 = %+v, want PUT /proxies/proxy {name:auto}", puts[0])
	}
	if puts[1].Group != "proxy" || puts[1].Name != "proxy-1" {
		t.Errorf("call 2 = %+v, want PUT /proxies/proxy {name:proxy-1}", puts[1])
	}
}

// --- Connections (F6 in docs/v2-spec.md) ---

func TestExecutor_Connections_NoModeRunning(t *testing.T) {
	e, _ := newExecutor()
	got := e.Connections(context.Background())
	if got.State != clashapi.StateNoMode {
		t.Errorf("State = %q, want %q", got.State, clashapi.StateNoMode)
	}
}

func TestExecutor_Connections_APIDisabled(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	if err := e.LoadLink(ctx, validLink); err != nil {
		t.Fatal(err)
	}
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	got := e.Connections(ctx)
	if got.State != clashapi.StateAPIDisabled {
		t.Errorf("State = %q, want %q", got.State, clashapi.StateAPIDisabled)
	}
}

func TestExecutor_Connections_Active(t *testing.T) {
	e, _ := newExecutor()
	ctx := context.Background()
	if err := e.LoadLink(ctx, validLink); err != nil {
		t.Fatal(err)
	}
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/connections" {
			_, _ = w.Write([]byte(`{"downloadTotal":0,"uploadTotal":0,"connections":[
				{"id":"c1","metadata":{"process":"codex","host":"api.openai.com","destinationPort":"443"},
				 "upload":1,"download":2,"chains":["proxy"],"rule":"final"}
			]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")

	got := e.Connections(ctx)
	if got.State != clashapi.StateActive {
		t.Fatalf("State = %q, want %q", got.State, clashapi.StateActive)
	}
	if len(got.Rows) != 1 || got.Rows[0].App != "codex" {
		t.Errorf("Rows = %+v, want one codex row", got.Rows)
	}
	if len(got.Apps) != 1 || got.Apps[0].App != "codex" {
		t.Errorf("Apps = %+v, want one codex total", got.Apps)
	}
}

func TestExecutor_CloseConnection_APIDisabled(t *testing.T) {
	e, _ := newExecutor()
	if err := e.CloseConnection(context.Background(), "c1"); err == nil {
		t.Fatal("expected error when the Clash API is disabled")
	}
}

func TestExecutor_CloseConnection_CallsDeleteEndpoint(t *testing.T) {
	e, _ := newExecutor()
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	e.SetClashAPI(strings.TrimPrefix(srv.URL, "http://"), "")

	if err := e.CloseConnection(context.Background(), "c1"); err != nil {
		t.Fatalf("CloseConnection: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/connections/c1" {
		t.Errorf("got %s %s, want DELETE /connections/c1", gotMethod, gotPath)
	}
}

// --- Firewall (F6 item 5 in docs/v2-spec.md) ---

func TestExecutor_FirewallAdd_RejectsInvalidRule(t *testing.T) {
	e, _ := newExecutor()
	if _, err := e.FirewallAdd(context.Background(), firewall.Rule{Action: firewall.ActionBlock}); err == nil {
		t.Fatal("expected a validation error for a rule matching nothing")
	}
	if len(e.FirewallList()) != 0 {
		t.Errorf("an invalid rule must not be added: %+v", e.FirewallList())
	}
}

func TestExecutor_Firewall_PersistenceRoundTrip(t *testing.T) {
	e, _ := newExecutor()
	var saved []firewall.Rule
	e.SetFirewallSaver(func(rules []firewall.Rule) error {
		saved = append([]firewall.Rule(nil), rules...)
		return nil
	})

	added, err := e.FirewallAdd(context.Background(), firewall.Rule{Action: firewall.ActionBlock, CIDR: "10.0.0.0/8"})
	if err != nil {
		t.Fatalf("FirewallAdd: %v", err)
	}
	if added.ID == "" {
		t.Error("FirewallAdd did not assign an id")
	}
	if len(saved) != 1 || saved[0].ID != added.ID {
		t.Fatalf("saver not called with the new rule set: %+v", saved)
	}

	// A fresh executor (simulating a restart) restores from what got "persisted".
	e2, _ := newExecutor()
	e2.RestoreFirewallRules(saved)
	got := e2.FirewallList()
	if len(got) != 1 || got[0].CIDR != "10.0.0.0/8" {
		t.Fatalf("RestoreFirewallRules did not seed the rule set: %+v", got)
	}

	if err := e.FirewallRemove(context.Background(), added.ID); err != nil {
		t.Fatalf("FirewallRemove: %v", err)
	}
	if len(saved) != 0 {
		t.Errorf("saver after remove = %+v, want empty", saved)
	}
	if len(e.FirewallList()) != 0 {
		t.Errorf("FirewallList after remove = %+v, want empty", e.FirewallList())
	}
	if err := e.FirewallRemove(context.Background(), added.ID); err == nil {
		t.Error("removing an already-removed rule should error")
	}
}

func TestExecutor_FirewallAdd_NoModeRunning_PersistsWithoutReload(t *testing.T) {
	ff := core.NewFakeFactory()
	e := NewExecutor(ff.Factory(), testRegistry, fakeProber{}, fakeRoutes{}, nil)
	ctx := context.Background()
	if err := e.LoadLink(ctx, validLink); err != nil {
		t.Fatal(err)
	}
	if _, err := e.FirewallAdd(ctx, firewall.Rule{Action: firewall.ActionBlock, Process: "malware"}); err != nil {
		t.Fatalf("FirewallAdd: %v", err)
	}
	if len(ff.BuiltFor("proxy")) != 0 {
		t.Errorf("FirewallAdd built a proxy core while nothing was running")
	}
	if len(e.FirewallList()) != 1 {
		t.Errorf("FirewallList() = %+v, want 1 rule", e.FirewallList())
	}
}

// TestExecutor_FirewallAdd_WhileRunning_TriggersExactlyOneReload is the F6
// item 5 requirement: adding a rule while a mode is running must reuse the
// existing "reload preserving mode" path (applyPreservingMode) — one rebuild
// of the generated config, one re-enable of the mode that was running — never
// a second, separate reload.
func TestExecutor_FirewallAdd_WhileRunning_TriggersExactlyOneReload(t *testing.T) {
	ff := core.NewFakeFactory()
	e := NewExecutor(ff.Factory(), testRegistry, fakeProber{}, fakeRoutes{}, nil)
	ctx := context.Background()
	if err := e.LoadLink(ctx, validLink); err != nil {
		t.Fatal(err)
	}
	if err := e.EnableProxy(ctx); err != nil {
		t.Fatal(err)
	}
	before := len(ff.BuiltFor("proxy"))
	if before != 1 {
		t.Fatalf("proxy builds before FirewallAdd = %d, want 1", before)
	}

	added, err := e.FirewallAdd(ctx, firewall.Rule{Action: firewall.ActionBlock, Domain: "ads.example.com"})
	if err != nil {
		t.Fatalf("FirewallAdd: %v", err)
	}
	if added.ID == "" {
		t.Error("FirewallAdd did not assign an id")
	}

	built := ff.BuiltFor("proxy")
	if len(built) != before+1 {
		t.Fatalf("proxy builds after FirewallAdd = %d, want %d (exactly one reload)", len(built), before+1)
	}
	latest := built[len(built)-1]
	if !strings.Contains(string(latest.Config), "ads.example.com") {
		t.Errorf("reloaded config does not carry the new firewall rule:\n%s", latest.Config)
	}
	if e.manager().State() != runtime.StateProxyOnly {
		t.Errorf("mode not preserved across the reload: state = %v", e.manager().State())
	}

	// Removing while running must likewise trigger exactly one more reload.
	if err := e.FirewallRemove(ctx, added.ID); err != nil {
		t.Fatalf("FirewallRemove: %v", err)
	}
	built = ff.BuiltFor("proxy")
	if len(built) != before+2 {
		t.Fatalf("proxy builds after FirewallRemove = %d, want %d (exactly one more reload)", len(built), before+2)
	}
	if strings.Contains(string(built[len(built)-1].Config), "ads.example.com") {
		t.Errorf("reloaded config after FirewallRemove still carries the removed rule:\n%s", built[len(built)-1].Config)
	}
}
