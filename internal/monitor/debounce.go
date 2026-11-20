package monitor

import "singctl/internal/policy"

// Debouncer tracks the confirmed Cisco connection state.
type Debouncer = debouncer[policy.CiscoState]

func NewDebouncer(threshold int) *Debouncer {
	return newDebouncer[policy.CiscoState](threshold)
}

// Consecutive observations confirm a change. The first is a baseline.
type debouncer[T comparable] struct {
	threshold   int
	initialized bool
	committed   T
	pending     T
	count       int
}

func newDebouncer[T comparable](threshold int) *debouncer[T] {
	return &debouncer[T]{threshold: max(1, threshold)}
}

// Observe feeds one raw observation and returns the committed state and whether
// it just changed.
func (d *debouncer[T]) Observe(raw T) (committed T, changed bool) {
	if !d.initialized {
		d.initialized = true
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
func (d *debouncer[T]) Committed() T { return d.committed }

// Pending reports whether a second observation is required to commit a flip.
func (d *debouncer[T]) Pending() bool {
	return d.initialized && d.pending != d.committed
}
