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
	det    Detector
	deb    *Debouncer
	modeFn func() policy.Mode
	out    chan<- Event

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
// current mode (owned by the runtime); out receives actionable events.
func New(det Detector, threshold int, modeFn func() policy.Mode, out chan<- Event) *Monitor {
	return &Monitor{
		det:       det,
		deb:       NewDebouncer(threshold),
		modeFn:    modeFn,
		out:       out,
		prevCisco: policy.CiscoUnknown,
	}
}

// Run does one immediate poll, then polls on each tick and each event-source
// signal until ctx is cancelled. tick and events may be nil.
func (m *Monitor) Run(ctx context.Context, tick <-chan time.Time, events <-chan struct{}) error {
	m.poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick:
			m.poll(ctx)
		case <-events:
			m.poll(ctx)
		}
	}
}

// poll takes one snapshot, debounces, decides, and emits if actionable. A
// Detector error is swallowed (keep previous committed state, skip this tick).
func (m *Monitor) poll(ctx context.Context) {
	ns, err := m.det.Observe(ctx)
	if err != nil {
		return
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

	res := policy.Decide(policy.DecideInput{
		Mode:             m.modeFn(),
		PrevCisco:        m.prevCisco,
		NewCisco:         committed,
		Intent:           policy.IntentNone,
		PhysIfaceChanged: physChanged,
	})
	m.prevCisco = committed

	if actionable(res) {
		select {
		case m.out <- Event{NetState: ns, Decision: res}:
		case <-ctx.Done():
		}
	}
}

func actionable(r policy.DecisionResult) bool {
	for _, a := range r.Actions {
		if a != policy.ActNone {
			return true
		}
	}
	return false
}
