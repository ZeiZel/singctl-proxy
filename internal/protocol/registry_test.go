package protocol

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// fakeModule is a minimal link module: enough to exercise the registry and the
// conformance harness without depending on any real protocol package.
type fakeModule struct {
	desc     Descriptor
	relabel  bool
	sniffFor string
}

func (f fakeModule) Descriptor() Descriptor { return f.desc }

func (f fakeModule) Parse(raw string) (Profile, error) {
	raw = strings.TrimSpace(raw)
	if f.desc.Input == InputLink {
		scheme, rest, found := strings.Cut(raw, "://")
		if !found || !contains(f.desc.Schemes, strings.ToLower(scheme)) {
			return Profile{}, fmt.Errorf("fake: not a %s link", f.desc.Name)
		}
		if rest == "" {
			return Profile{}, fmt.Errorf("fake: empty body")
		}
		label := ""
		if _, frag, ok := strings.Cut(rest, "#"); ok {
			label = frag
		}
		return Profile{Protocol: f.desc.Name, Label: label, Raw: raw, Params: rest}, nil
	}
	if !strings.Contains(raw, f.sniffFor) {
		return Profile{}, fmt.Errorf("fake: not a %s config", f.desc.Name)
	}
	return Profile{Protocol: f.desc.Name, Label: "cfg", Raw: raw, Params: raw}, nil
}

func (f fakeModule) Sniff(raw string) bool {
	return f.desc.Input == InputConfig && strings.Contains(raw, f.sniffFor)
}

// relabelModule adds the optional Relabeler capability.
type relabelModule struct{ fakeModule }

func (r relabelModule) SetLabel(raw, label string) (string, error) {
	base, _, _ := strings.Cut(strings.TrimSpace(raw), "#")
	if label == "" {
		return base, nil
	}
	return base + "#" + label, nil
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func linkModule(name Name, schemes ...string) fakeModule {
	return fakeModule{desc: Descriptor{
		Name: name, Title: string(name), Kind: KindOutbound, Input: InputLink, Schemes: schemes,
	}}
}

func TestNewRegistry_RejectsBadWiring(t *testing.T) {
	cfgMod := fakeModule{desc: Descriptor{
		Name: "wg", Title: "WireGuard", Kind: KindEndpoint, Input: InputConfig,
	}, sniffFor: "[Interface]"}

	for _, tc := range []struct {
		name    string
		modules []Module
		wantErr string
	}{
		{"no modules", nil, "no protocol modules"},
		{
			"duplicate name",
			[]Module{linkModule("a", "a"), linkModule("a", "b")},
			"duplicate module name",
		},
		{
			// Two modules claiming one scheme cannot be resolved sanely at
			// runtime, so it must fail at wiring time instead.
			"duplicate scheme",
			[]Module{linkModule("a", "x"), linkModule("b", "x")},
			"claimed by both",
		},
		{
			"link module without schemes",
			[]Module{fakeModule{desc: Descriptor{Name: "a", Title: "A", Input: InputLink}}},
			"declares no schemes",
		},
		{
			// A config module the registry cannot sniff would be unreachable.
			"config module without Sniffer",
			[]Module{struct{ Module }{Module: cfgMod}},
			"must implement Sniffer",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRegistry(tc.modules...)
			if err == nil {
				t.Fatalf("NewRegistry succeeded, want an error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestRegistry_Dispatch(t *testing.T) {
	vless := linkModule("vless", "vless")
	hy2 := linkModule("hysteria2", "hysteria2", "hy2")
	wg := fakeModule{desc: Descriptor{
		Name: "wireguard", Title: "WireGuard", Kind: KindEndpoint, Input: InputConfig,
	}, sniffFor: "[Interface]"}

	r, err := NewRegistry(vless, hy2, wg)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("by scheme", func(t *testing.T) {
		p, err := r.Parse("vless://body#label")
		if err != nil {
			t.Fatal(err)
		}
		if p.Protocol != "vless" || p.Label != "label" {
			t.Fatalf("got %+v", p)
		}
	})

	t.Run("scheme alias reaches the same module", func(t *testing.T) {
		p, err := r.Parse("hy2://body")
		if err != nil {
			t.Fatal(err)
		}
		if p.Protocol != "hysteria2" {
			t.Fatalf("alias routed to %q", p.Protocol)
		}
	})

	t.Run("config input is sniffed", func(t *testing.T) {
		p, err := r.Parse("[Interface]\nPrivateKey = abc\n")
		if err != nil {
			t.Fatal(err)
		}
		if p.Protocol != "wireguard" {
			t.Fatalf("config routed to %q", p.Protocol)
		}
	})

	t.Run("unknown scheme is not offered to config sniffers", func(t *testing.T) {
		// A pasted subscription URL must be reported as an unsupported scheme,
		// not misread as a config blob.
		_, err := r.Parse("https://panel.example.com/sub/token/")
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("error = %v, want ErrUnsupported", err)
		}
		if !strings.Contains(err.Error(), "vless://") {
			t.Errorf("error should list accepted schemes, got: %v", err)
		}
	})

	t.Run("schemes are reported sorted and complete", func(t *testing.T) {
		got := strings.Join(r.Schemes(), ",")
		if got != "hy2,hysteria2,vless" {
			t.Fatalf("Schemes() = %q", got)
		}
	})
}

func TestRegistry_SetLabel(t *testing.T) {
	plain := linkModule("plain", "plain")
	renameable := relabelModule{linkModule("named", "named")}
	r, err := NewRegistry(plain, renameable)
	if err != nil {
		t.Fatal(err)
	}

	got, err := r.SetLabel("named://body#old", "new")
	if err != nil {
		t.Fatal(err)
	}
	if got != "named://body#new" {
		t.Fatalf("SetLabel = %q", got)
	}

	// A module that cannot be renamed must say so, rather than silently
	// returning the input unchanged and leaving the UI showing the old name.
	if _, err := r.SetLabel("plain://body", "new"); !errors.Is(err, ErrCannotRelabel) {
		t.Fatalf("error = %v, want ErrCannotRelabel", err)
	}
}

// TestConformanceHarness_SelfCheck exercises the harness itself against fakes,
// so a bug in the harness cannot silently pass every real module later.
func TestConformanceHarness_SelfCheck(t *testing.T) {
	r, err := NewRegistry(relabelModule{linkModule("named", "named")})
	if err != nil {
		t.Fatal(err)
	}
	CheckRegistry(t, r, []ConformanceCase{{
		Module:    "named",
		Input:     "named://body#label",
		WantLabel: "label",
		Rejects:   []string{"named://"},
	}})
}

// TestConformanceHarness_CatchesBadModule proves the harness actually fails on
// a module that violates the contract — a harness that never fails is worse
// than no harness, because it looks like coverage.
func TestConformanceHarness_CatchesBadModule(t *testing.T) {
	bad := brokenModule{linkModule("broken", "broken")}
	rec := &recordingTB{}
	CheckModule(rec, bad, ConformanceCase{Module: "broken", Input: "broken://body#x"})
	if len(rec.errs) == 0 {
		t.Fatal("harness accepted a module that drops Raw and mislabels its protocol")
	}
}

// brokenModule violates two invariants: it reports the wrong protocol and
// throws away Raw.
type brokenModule struct{ fakeModule }

func (brokenModule) Parse(raw string) (Profile, error) {
	return Profile{Protocol: "something-else", Params: "x"}, nil
}

type recordingTB struct{ errs []string }

func (r *recordingTB) Helper() {}
func (r *recordingTB) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}
func (r *recordingTB) Fatalf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
	panic(errSkipRest)
}

var errSkipRest = errors.New("fatal")
