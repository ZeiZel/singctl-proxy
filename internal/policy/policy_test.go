package policy

import (
	"reflect"
	"testing"
)

func TestDecide_Table(t *testing.T) {
	tests := []struct {
		name string
		in   DecideInput
		want DecisionResult
	}{
		// Rule (a): block VPN while Cisco active.
		{
			"enable VPN blocked when Cisco active",
			DecideInput{Mode: ModeProxy, NewCisco: CiscoActive, Intent: IntentEnableVPN},
			DecisionResult{Actions: []Action{ActShowWarning}, NextMode: ModeProxy},
		},
		{
			"enable VPN allowed when Cisco inactive",
			DecideInput{Mode: ModeProxy, NewCisco: CiscoInactive, Intent: IntentEnableVPN},
			DecisionResult{Actions: []Action{ActStartForwarder}, NextMode: ModeVPN},
		},
		{
			"enable VPN idempotent when already VPN",
			DecideInput{Mode: ModeVPN, NewCisco: CiscoInactive, Intent: IntentEnableVPN},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeVPN},
		},
		// Disable VPN.
		{
			"disable VPN from VPN",
			DecideInput{Mode: ModeVPN, Intent: IntentDisableVPN},
			DecisionResult{Actions: []Action{ActStopForwarder}, NextMode: ModeProxy},
		},
		{
			"disable VPN when already proxy is a no-op",
			DecideInput{Mode: ModeProxy, Intent: IntentDisableVPN},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		// Rule (b): Cisco up while in VPN -> fail-closed teardown.
		{
			"cisco up in VPN fails closed",
			DecideInput{Mode: ModeVPN, PrevCisco: CiscoInactive, NewCisco: CiscoActive},
			DecisionResult{Actions: []Action{ActFailClosed, ActStopForwarder, ActNotify}, NextMode: ModeProxy},
		},
		{
			// Coexistence no longer binds: riding the default route is correct even
			// under full-tunnel Cisco (its tunnel reaches the internet; en0 egress
			// is blocked, so binding would break the proxy).
			"full-tunnel cisco in proxy mode does NOT bind (rides the tunnel)",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoInactive, NewCisco: CiscoActive, CiscoOwnsDefault: true},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		{
			"split-tunnel cisco in proxy mode does NOT bind",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoInactive, NewCisco: CiscoActive, CiscoOwnsDefault: false},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		// Rule (c): if we somehow hold a stale physical bind in proxy mode, release
		// it so egress rides the default route.
		{
			"stale bind in proxy is released",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoActive, NewCisco: CiscoActive, CiscoOwnsDefault: true, ProxyBoundPhys: true},
			DecisionResult{Actions: []Action{ActUnbindProxy}, NextMode: ModeProxy},
		},
		{
			"cisco down in proxy releases any stale bind",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoActive, NewCisco: CiscoInactive, ProxyBoundPhys: true},
			DecisionResult{Actions: []Action{ActUnbindProxy}, NextMode: ModeProxy},
		},
		{
			// Startup with full-tunnel Cisco already connected: no bind, just ride
			// the tunnel (the user's exact scenario).
			"initial unknown->active with full-tunnel does NOT bind",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoUnknown, NewCisco: CiscoActive, CiscoOwnsDefault: true},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		{
			"initial unknown->inactive is inert",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoUnknown, NewCisco: CiscoInactive},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		// Steady state, no edge.
		{
			"cisco steady active in VPN — should not happen but inert",
			DecideInput{Mode: ModeVPN, PrevCisco: CiscoActive, NewCisco: CiscoActive},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeVPN},
		},
		// Switching mode is inert to observations.
		{
			"switching mode inert to cisco edge",
			DecideInput{Mode: ModeSwitching, PrevCisco: CiscoInactive, NewCisco: CiscoActive},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeSwitching},
		},
		// Physical interface change.
		{
			"phys iface change in VPN rebinds",
			DecideInput{Mode: ModeVPN, PrevCisco: CiscoInactive, NewCisco: CiscoInactive, PhysIfaceChanged: true},
			DecisionResult{Actions: []Action{ActRefreshProxy}, NextMode: ModeVPN},
		},
		{
			"phys iface change in proxy is ignored (proxy unbound)",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoInactive, NewCisco: CiscoInactive, PhysIfaceChanged: true},
			DecisionResult{Actions: []Action{ActNone}, NextMode: ModeProxy},
		},
		{
			// A stale bind takes precedence: release it rather than rebind.
			"phys iface change in proxy while bound releases the stale bind",
			DecideInput{Mode: ModeProxy, PrevCisco: CiscoActive, NewCisco: CiscoActive, CiscoOwnsDefault: true, PhysIfaceChanged: true, ProxyBoundPhys: true},
			DecisionResult{Actions: []Action{ActUnbindProxy}, NextMode: ModeProxy},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Decide(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Decide(%+v)\n got: %+v\nwant: %+v", tt.in, got, tt.want)
			}
		})
	}
}

// TestDecide_Deterministic guards against any hidden nondeterminism across the
// full input cross-product.
func TestDecide_Deterministic(t *testing.T) {
	modes := []Mode{ModeProxy, ModeVPN, ModeSwitching}
	states := []CiscoState{CiscoUnknown, CiscoInactive, CiscoActive}
	intents := []UserIntent{IntentNone, IntentEnableVPN, IntentDisableVPN}
	for _, m := range modes {
		for _, pc := range states {
			for _, nc := range states {
				for _, it := range intents {
					for _, pf := range []bool{false, true} {
						for _, pb := range []bool{false, true} {
							for _, od := range []bool{false, true} {
								in := DecideInput{Mode: m, PrevCisco: pc, NewCisco: nc, Intent: it, PhysIfaceChanged: pf, ProxyBoundPhys: pb, CiscoOwnsDefault: od}
								a := Decide(in)
								b := Decide(in)
								if !reflect.DeepEqual(a, b) {
									t.Fatalf("non-deterministic for %+v: %+v vs %+v", in, a, b)
								}
								if len(a.Actions) == 0 {
									t.Fatalf("empty actions for %+v (should be at least ActNone)", in)
								}
							}
						}
					}
				}
			}
		}
	}
}

// TestDecide_NeverStartsForwarderWhileCiscoActive is the safety invariant that
// underpins the whole coexistence guarantee.
func TestDecide_NeverStartsForwarderWhileCiscoActive(t *testing.T) {
	modes := []Mode{ModeProxy, ModeVPN, ModeSwitching}
	intents := []UserIntent{IntentNone, IntentEnableVPN, IntentDisableVPN}
	for _, m := range modes {
		for _, it := range intents {
			for _, pc := range []CiscoState{CiscoUnknown, CiscoInactive, CiscoActive} {
				res := Decide(DecideInput{Mode: m, PrevCisco: pc, NewCisco: CiscoActive, Intent: it})
				for _, a := range res.Actions {
					if a == ActStartForwarder {
						t.Fatalf("Decide started forwarder while Cisco active: in mode=%v intent=%v", m, it)
					}
				}
			}
		}
	}
}
