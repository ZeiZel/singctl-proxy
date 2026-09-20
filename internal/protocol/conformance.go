package protocol

import (
	"errors"
	"strings"
)

// TB is the slice of *testing.T this package needs. Declaring it here rather
// than importing "testing" keeps the test harness usable from any package's
// tests without dragging test flags into the shipping binary.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
	Fatalf(format string, args ...any)
}

// ConformanceCase is one worked example for a module: an input it must accept,
// and what parsing it must produce.
type ConformanceCase struct {
	Module Name
	// Input is a valid key for this module.
	Input string
	// WantLabel is the display name Parse must extract. Leave empty to skip.
	WantLabel string
	// Rejects are inputs this module must refuse. They reach the module only
	// when it claims them, so they should share its scheme (or config shape).
	Rejects []string
}

// CheckModule asserts the invariants EVERY module must satisfy, whatever it
// speaks. It exists so that registering a ninth protocol is covered the moment
// it is registered, rather than depending on whoever adds it remembering to
// write the same six assertions again.
func CheckModule(t TB, m Module, c ConformanceCase) {
	t.Helper()
	d := m.Descriptor()

	// --- the descriptor must be self-consistent ---
	if d.Name == "" {
		t.Fatalf("descriptor has no Name")
	}
	if d.Title == "" {
		t.Errorf("%s: descriptor has no Title (the UI shows it)", d.Name)
	}
	switch d.Input {
	case InputLink:
		if len(d.Schemes) == 0 {
			t.Fatalf("%s: link module declares no schemes", d.Name)
		}
		for _, s := range d.Schemes {
			if s != strings.ToLower(s) || strings.Contains(s, "://") {
				t.Errorf("%s: scheme %q must be bare and lowercase", d.Name, s)
			}
		}
	case InputConfig:
		if _, ok := m.(Sniffer); !ok {
			t.Fatalf("%s: config module must implement Sniffer", d.Name)
		}
		if len(d.Schemes) != 0 {
			t.Errorf("%s: config module must not claim schemes: %v", d.Name, d.Schemes)
		}
	}

	// --- parsing a known-good input ---
	p, err := m.Parse(c.Input)
	if err != nil {
		t.Fatalf("%s: Parse(valid input) failed: %v", d.Name, err)
	}
	if p.Protocol != d.Name {
		t.Errorf("%s: Parse set Protocol=%q, want the descriptor's name", d.Name, p.Protocol)
	}
	if strings.TrimSpace(p.Raw) == "" {
		t.Errorf("%s: Parse must keep Raw — the UI and re-parse depend on it", d.Name)
	}
	if p.Params == nil {
		t.Errorf("%s: Parse produced no Params", d.Name)
	}
	if c.WantLabel != "" && p.Label != c.WantLabel {
		t.Errorf("%s: Label = %q, want %q", d.Name, p.Label, c.WantLabel)
	}

	// --- parsing is pure: the same input must give the same result ---
	again, err := m.Parse(c.Input)
	if err != nil {
		t.Fatalf("%s: Parse is not deterministic — second call failed: %v", d.Name, err)
	}
	if again.Protocol != p.Protocol || again.Label != p.Label || again.Raw != p.Raw {
		t.Errorf("%s: Parse is not deterministic across calls", d.Name)
	}

	// --- rejections must be errors, never zero-value successes ---
	for _, bad := range c.Rejects {
		if _, err := m.Parse(bad); err == nil {
			t.Errorf("%s: Parse(%q) succeeded, want an error", d.Name, redact(bad))
		}
	}

	// --- optional capability: relabeling must round-trip ---
	if rl, ok := m.(Relabeler); ok {
		renamed, err := rl.SetLabel(c.Input, "renamed-by-conformance")
		if err != nil {
			t.Errorf("%s: SetLabel failed: %v", d.Name, err)
			return
		}
		after, err := m.Parse(renamed)
		if err != nil {
			t.Errorf("%s: SetLabel produced input its own Parse rejects: %v", d.Name, err)
			return
		}
		if after.Label != "renamed-by-conformance" {
			t.Errorf("%s: after SetLabel, Label = %q, want the new name", d.Name, after.Label)
		}
		// Renaming must not disturb anything else about the endpoint.
		if after.Protocol != p.Protocol {
			t.Errorf("%s: SetLabel changed the protocol", d.Name)
		}
	}
}

// CheckRegistry asserts the registry-level invariants across a whole module
// set, then runs CheckModule for every case supplied.
func CheckRegistry(t TB, r *Registry, cases []ConformanceCase) {
	t.Helper()

	covered := make(map[Name]bool, len(cases))
	for _, c := range cases {
		m, ok := r.Module(c.Module)
		if !ok {
			t.Fatalf("conformance case names %q, which is not registered", c.Module)
		}
		CheckModule(t, m, c)
		covered[c.Module] = true

		// The registry must route the same input to the same module.
		p, err := r.Parse(c.Input)
		if err != nil {
			t.Errorf("%s: registry refused input its own module accepts: %v", c.Module, err)
			continue
		}
		if p.Protocol != c.Module {
			t.Errorf("registry routed %q to %q, want %q", redact(c.Input), p.Protocol, c.Module)
		}
	}

	// Every registered module needs a case: an unexercised module is an
	// untested one, and this is the check that makes adding a protocol
	// automatically demand its own coverage.
	for _, m := range r.Modules() {
		if name := m.Descriptor().Name; !covered[name] {
			t.Errorf("module %q has no conformance case", name)
		}
	}

	// Unknown input must be refused with a message that lists what IS accepted.
	_, err := r.Parse("ftp://example.com")
	if !errors.Is(err, ErrUnsupported) {
		t.Errorf("Parse(unknown scheme) error = %v, want ErrUnsupported", err)
	}
	if _, err := r.Parse(""); !errors.Is(err, ErrUnsupported) {
		t.Errorf("Parse(empty) error = %v, want ErrUnsupported", err)
	}
}

// redact keeps credentials out of test output: keys carry UUIDs and passwords,
// and a failing test should not paste them into CI logs.
func redact(raw string) string {
	if scheme, _, found := strings.Cut(raw, "://"); found {
		return scheme + "://…"
	}
	if len(raw) > 16 {
		return raw[:16] + "…"
	}
	return raw
}
