package monitor

import (
	"testing"

	"singctl/internal/policy"
)

func TestDebouncer_InitialCommitNoChange(t *testing.T) {
	d := NewDebouncer(2)
	got, changed := d.Observe(policy.CiscoActive)
	if got != policy.CiscoActive || changed {
		t.Fatalf("initial = (%v,%v), want (active,false)", got, changed)
	}
}

func TestDebouncer_SuppressesSingleFlap(t *testing.T) {
	d := NewDebouncer(2)
	d.Observe(policy.CiscoInactive) // initial commit
	if got, changed := d.Observe(policy.CiscoActive); got != policy.CiscoInactive || changed {
		t.Fatalf("single Active = (%v,%v), want (inactive,false) — flap suppressed", got, changed)
	}
	if got, changed := d.Observe(policy.CiscoInactive); got != policy.CiscoInactive || changed {
		t.Fatalf("back to Inactive = (%v,%v), want (inactive,false)", got, changed)
	}
}

func TestDebouncer_CommitsAfterThreshold(t *testing.T) {
	d := NewDebouncer(2)
	d.Observe(policy.CiscoInactive) // initial
	if _, changed := d.Observe(policy.CiscoActive); changed {
		t.Fatal("1st Active should not commit yet")
	}
	if got, changed := d.Observe(policy.CiscoActive); got != policy.CiscoActive || !changed {
		t.Fatalf("2nd Active = (%v,%v), want (active,true)", got, changed)
	}
}

func TestDebouncer_ResetsPendingOnDifferent(t *testing.T) {
	d := NewDebouncer(2)
	d.Observe(policy.CiscoInactive) // initial
	d.Observe(policy.CiscoActive)   // pending active, count 1
	d.Observe(policy.CiscoInactive) // back to committed, reset
	if _, changed := d.Observe(policy.CiscoActive); changed {
		t.Fatal("count should have reset; 1 Active must not commit")
	}
	if _, changed := d.Observe(policy.CiscoActive); !changed {
		t.Fatal("2 consecutive Active should commit")
	}
}
