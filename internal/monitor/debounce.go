package monitor

import "singctl/internal/policy"

// Debouncer suppresses Cisco-state flapping: a flip is committed only after
// `threshold` consecutive identical observations. The very first observation is
// committed immediately (initial sync) but reported as not-changed, so startup
// produces no spurious action.
type Debouncer struct {
	threshold int
	committed policy.CiscoState
	pending   policy.CiscoState
	count     int
}

func NewDebouncer(threshold int) *Debouncer {
	if threshold < 1 {
		threshold = 1
	}
	return &Debouncer{threshold: threshold, committed: policy.CiscoUnknown}
}

// Observe feeds one raw observation and returns the committed state and whether
// it just changed.
func (d *Debouncer) Observe(raw policy.CiscoState) (committed policy.CiscoState, changed bool) {
	if d.committed == policy.CiscoUnknown {
		d.committed, d.pending, d.count = raw, raw, 0
		return d.committed, false
	}
	if raw == d.committed {
		d.pending, d.count = raw, 0
		return d.committed, false
	}
	if raw == d.pending {
		d.count++
	} else {
		d.pending, d.count = raw, 1
	}
	if d.count >= d.threshold {
		d.committed = raw
		d.count = 0
		return d.committed, true
	}
	return d.committed, false
}

// Committed returns the current committed state.
func (d *Debouncer) Committed() policy.CiscoState { return d.committed }
