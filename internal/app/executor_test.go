package app

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/core"
	"singctl/internal/monitor"
	"singctl/internal/policy"
	"singctl/internal/runtime"
	"singctl/internal/types"
	"singctl/internal/ui"
)

type fakeProber struct{}

func (fakeProber) PhysicalDefault() (string, error) { return "en0", nil }

type fakeRoutes struct{}

func (fakeRoutes) CleanupOrphans() error { return nil }

const validLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=k&sni=cursor.com&fp=chrome#t"

func newExecutor() (*Executor, chan tea.Msg) {
	notes := make(chan tea.Msg, 32)
	ff := core.NewFakeFactory()
	return NewExecutor(ff.Factory(), fakeProber{}, fakeRoutes{}, notes), notes
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
		case ui.StatusMsg:
			sawStatus = true
			if mm.Mode != ui.RunOff {
				t.Errorf("status mode = %v, want OFF (suspended)", mm.Mode)
			}
		case ui.NetStateMsg:
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
