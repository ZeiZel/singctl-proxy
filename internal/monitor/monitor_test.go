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

func boundFunc(b bool) func() bool { return func() bool { return b } }

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
	m := New(det, 2, modeFunc(policy.ModeVPN), nil, out)
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

// TestMonitor_ReleasesStaleBindInProxy: if the proxy somehow holds a physical
// bind, the monitor emits ActUnbindProxy to release it (we no longer bind).
func TestMonitor_ReleasesStaleBindInProxy(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: true, CiscoOwnsDefault: true, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), boundFunc(true), out)

	m.poll(context.Background()) // bound → release
	if len(out) != 1 {
		t.Fatalf("expected 1 unbind event for a stale bind, got %d", len(out))
	}
	if e := <-out; !hasAction(e, policy.ActUnbindProxy) {
		t.Errorf("actions = %v, want unbind-proxy", e.Decision.Actions)
	}
}

// TestMonitor_FullTunnelDoesNotBind: full-tunnel Cisco with an unbound proxy
// must NOT bind — the proxy rides the tunnel (which reaches the internet);
// binding to the physical NIC would break it.
func TestMonitor_FullTunnelDoesNotBind(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: true, CiscoOwnsDefault: true, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), boundFunc(false), out)

	for i := 0; i < 3; i++ {
		m.poll(context.Background())
	}
	if len(out) != 0 {
		t.Fatalf("full-tunnel Cisco must not produce bind events, got %d", len(out))
	}
}

// TestMonitor_SplitTunnelDoesNotBind: split-tunnel Cisco with an unbound proxy
// must not produce any action.
func TestMonitor_SplitTunnelDoesNotBind(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: true, CiscoOwnsDefault: false, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), boundFunc(false), out)

	for i := 0; i < 3; i++ {
		m.poll(context.Background())
	}
	if len(out) != 0 {
		t.Fatalf("split-tunnel Cisco must not produce bind events, got %d", len(out))
	}
}

func TestMonitor_NoEventWhenUnchanged(t *testing.T) {
	out := make(chan Event, 8)
	det := &mockDetector{}
	det.set(types.NetState{CiscoActive: false, PhysicalIface: "en0"})
	m := New(det, 2, modeFunc(policy.ModeProxy), nil, out)
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
	m := New(det, 1, modeFunc(policy.ModeProxy), nil, out)
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
	m := New(det, 1, modeFunc(policy.ModeVPN), nil, out)
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
	m := New(det, 1, modeFunc(policy.ModeProxy), nil, out)

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
