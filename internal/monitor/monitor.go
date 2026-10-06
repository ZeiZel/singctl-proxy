// Package monitor polls the passive Detector (and reacts to event-source
// signals), debounces Cisco flapping, runs the policy, and emits Events the
// executor applies. The poll loop is driven by injected channels (a ticker and
// an event source), so it is fully testable without real timers.
package monitor

import (
	"context"
	"time"

	"singctl/internal/policy"
	"singctl/internal/types"
)

// Detector produces a passive NetState snapshot (netstate.Detector implements it).
type Detector interface {
	Observe(ctx context.Context) (types.NetState, error)
}

// Event is emitted when the policy decides an action is required.
type Event struct {
	NetState types.NetState
	Decision policy.DecisionResult
}

// Monitor observes network state and emits policy decisions.
type Monitor struct {
	observationFailed bool
	det               Detector
	deb               *Debouncer
	modeFn            func() policy.Mode
	boundFn           func() bool // proxy egress currently pinned to the physical NIC
	out               chan<- Event

	prevCisco policy.CiscoState
	prevPhys  string
	havePhys  bool
	onPoll    func(types.NetState)
}

// SetOnPoll registers a callback invoked with every successful observation
// (even when no action results), so the UI can show live status. Not used by
// the action path; safe to leave unset.
func (m *Monitor) SetOnPoll(fn func(types.NetState)) { m.onPoll = fn }

// New builds a Monitor. threshold is the debounce count; modeFn returns the
// current mode (owned by the runtime); boundFn reports whether the proxy egress
// is currently pinned to the physical NIC (Cisco-coexistence bind), and may be
// nil (treated as always-false); out receives actionable events.
func New(det Detector, threshold int, modeFn func() policy.Mode, boundFn func() bool, out chan<- Event) *Monitor {
	return &Monitor{
		det:       det,
		deb:       NewDebouncer(threshold),
		modeFn:    modeFn,
		boundFn:   boundFn,
		out:       out,
		prevCisco: policy.CiscoUnknown,
	}
}

// Run does one immediate poll, then polls on each tick and each event-source
// signal until ctx is cancelled. tick and events may be nil.
func (m *Monitor) Run(ctx context.Context, tick <-chan time.Time, events <-chan struct{}) error {
	const minPollInterval = 50 * time.Millisecond
	lastPoll := time.Time{}
	retryInterval := minPollInterval
	var confirm *time.Timer
	var confirmC <-chan time.Time
	defer func() {
		if confirm != nil {
			confirm.Stop()
		}
	}()
	poll := func() {
		if wait := time.Until(lastPoll.Add(minPollInterval)); wait > 0 {
			t := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
		lastPoll = time.Now()
		failed := false
		// An observation failure preserves debounce state but must not create a hot retry loop.
		m.observationFailed = false
		pending := m.poll(ctx)
		failed = m.observationFailed
		if failed {
			retryInterval *= 2
			if retryInterval > 2*time.Second {
				retryInterval = 2 * time.Second
			}
		} else {
			retryInterval = minPollInterval
		}
		if pending || m.deb.Pending() {
			if confirm == nil {
				confirm = time.NewTimer(retryInterval)
			} else {
				if !confirm.Stop() {
					select {
					case <-confirm.C:
					default:
					}
				}
				confirm.Reset(retryInterval)
			}
			confirmC = confirm.C
		}
	}
	poll()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case _, ok := <-tick:
			if !ok {
				tick = nil
				continue
			}
			poll()
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			poll()
		case <-confirmC:
			confirmC = nil
			poll()
		}
	}
}

// poll takes one snapshot, debounces, decides, and emits if actionable. A
// Detector error is swallowed (keep previous committed state, skip this tick).
func (m *Monitor) poll(ctx context.Context) bool {
	ns, err := m.det.Observe(ctx)
	if err != nil {
		m.observationFailed = true
		return false
	}
	if m.onPoll != nil {
		m.onPoll(ns)
	}

	raw := policy.CiscoInactive
	if ns.CiscoActive {
		raw = policy.CiscoActive
	}
	committed, _ := m.deb.Observe(raw)

	physChanged := false
	if ns.PhysicalIface != "" {
		if m.havePhys && ns.PhysicalIface != m.prevPhys {
			physChanged = true
		}
		m.prevPhys = ns.PhysicalIface
		m.havePhys = true
	}

	boundPhys := false
	if m.boundFn != nil {
		boundPhys = m.boundFn()
	}
	res := policy.Decide(policy.DecideInput{
		Mode:             m.modeFn(),
		PrevCisco:        m.prevCisco,
		NewCisco:         committed,
		Intent:           policy.IntentNone,
		PhysIfaceChanged: physChanged,
		ProxyBoundPhys:   boundPhys,
		CiscoOwnsDefault: ns.CiscoOwnsDefault,
	})
	m.prevCisco = committed

	if actionable(res) {
		select {
		case m.out <- Event{NetState: ns, Decision: res}:
		case <-ctx.Done():
		}
	}
	return m.deb.Pending()
}

func actionable(r policy.DecisionResult) bool {
	for _, a := range r.Actions {
		if a != policy.ActNone {
			return true
		}
	}
	return false
}
