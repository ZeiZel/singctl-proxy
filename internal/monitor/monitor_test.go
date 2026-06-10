package monitor

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"singctl/internal/policy"
	"singctl/internal/types"
)

type mockDetector struct {
	mu  sync.Mutex
	ns  types.NetState
	err error
}

func (m *mockDetector) set(ns types.NetState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ns, m.err = ns, nil
}

func (m *mockDetector) fail(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

func (m *mockDetector) Observe(context.Context) (types.NetState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ns, m.err
}

func modeFunc(m policy.Mode) func() policy.Mode { return func() policy.Mode { return m } }

func hasAction(e Event, a policy.Action) bool {
	for _, x := range e.Decision.Actions {
		if x == a {
			return true
		}
	}
	return false
}

func TestMonitor_RuleB_FailClosedWhenCiscoAppearsInVPN(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeVPN), out)
	ctx := context.Background()

	m.poll(ctx) // initial commit Inactive
	det.set(types.NetState{CiscoActive: true, PhysicalIface: "en0"})
	m.poll(ctx) // 1st Active — debounced, no commit
	if len(out) != 0 {
		t.Fatalf("no event expected before debounce commit, got %d", len(out))
	}
	m.poll(ctx) // 2nd Active — commit, edge Inactive->Active in VPN

	if len(out) != 1 {
		t.Fatalf("expected exactly 1 event, got %d", len(out))
	}
	e := <-out
	if !hasAction(e, policy.ActFailClosed) || !hasAction(e, policy.ActStopForwarder) {
		t.Errorf("rule (b) actions = %v, want fail-closed + stop", e.Decision.Actions)
	}
	if e.Decision.NextMode != policy.ModeProxy {
		t.Errorf("next mode = %v, want proxy", e.Decision.NextMode)
	}
}

func TestMonitor_RuleC_RefreshWhenCiscoLeavesInProxy(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: true, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), out)
	ctx := context.Background()

	m.poll(ctx) // initial commit Active
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m.poll(ctx) // 1st Inactive — debounced
	m.poll(ctx) // 2nd Inactive — commit, edge Active->Inactive in proxy

	if len(out) != 1 {
		t.Fatalf("expected 1 event, got %d", len(out))
	}
	if e := <-out; !hasAction(e, policy.ActRefreshProxy) {
		t.Errorf("rule (c) actions = %v, want refresh-proxy", e.Decision.Actions)
	}
}

func TestMonitor_NoEventWhenUnchanged(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), out)
	for i := 0; i < 5; i++ {
		m.poll(context.Background())
	}
	if len(out) != 0 {
		t.Fatalf("steady state should emit no events, got %d", len(out))
	}
}

func TestMonitor_DetectorErrorSkips(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.fail(errors.New("ifconfig failed"))
	m := New(det, 1, modeFunc(policy.ModeProxy), out)
	m.poll(context.Background()) // must not panic, no event
	if len(out) != 0 {
		t.Fatalf("error poll should emit nothing, got %d", len(out))
	}
	// Recovery: a good observation works afterwards.
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m.poll(context.Background())
}

func TestMonitor_PhysChangeRefreshInVPN(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m := New(det, 1, modeFunc(policy.ModeVPN), out)
	ctx := context.Background()

	m.poll(ctx) // records en0
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en6"})
	m.poll(ctx) // phys changed under VPN

	if len(out) != 1 {
		t.Fatalf("expected 1 event for phys change, got %d", len(out))
	}
	if e := <-out; !hasAction(e, policy.ActRefreshProxy) {
		t.Errorf("phys-change actions = %v, want refresh-proxy", e.Decision.Actions)
	}
}

func TestMonitor_Run_StopsOnContextCancel(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m := New(det, 1, modeFunc(policy.ModeProxy), out)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx, make(chan time.Time), nil) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after context cancel")
	}
}
