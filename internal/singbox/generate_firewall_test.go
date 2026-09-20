package singbox

import (
	"strings"
	"testing"

	"singctl/internal/firewall"
	"singctl/internal/protocol"
	"singctl/internal/testutil"
)

// fakeModule is a minimal protocol.Module + Renderer so these tests can drive
// GenerateProxyConfigOpts without depending on internal/protocol/all (which
// itself imports this package — internal/singbox cannot import it back
// without a cycle; see internal/protocol/all/golden_test.go for the
// equivalent full-registry harness the real protocol modules use).
type fakeModule struct{}

func (fakeModule) Descriptor() protocol.Descriptor {
	return protocol.Descriptor{Name: "fake", Title: "Fake", Kind: protocol.KindOutbound, Input: protocol.InputLink, Schemes: []string{"fake"}}
}

func (fakeModule) Parse(raw string) (protocol.Profile, error) {
	return protocol.Profile{Protocol: "fake", Label: "fake-test", Raw: raw}, nil
}

func (fakeModule) RenderNode(p protocol.Profile, o RenderOpts) (any, error) {
	return DirectOutbound{Type: "direct", Tag: o.Tag}, nil
}

func fakeRegistry(t *testing.T) *protocol.Registry {
	t.Helper()
	reg, err := protocol.NewRegistry(fakeModule{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

func fakeProfile() protocol.Profile {
	return protocol.Profile{Protocol: "fake", Label: "fake-test", Raw: "fake://server"}
}

// TestGenerateProxyConfigOpts_NoFirewall_AddsNothing pins that an empty (nil)
// firewall rule set adds no "block" outbound and no extra route rules — the
// byte-fidelity requirement in docs/v2-spec.md's F6: existing golden files
// must never change just because the firewall feature exists.
func TestGenerateProxyConfigOpts_NoFirewall_AddsNothing(t *testing.T) {
	reg := fakeRegistry(t)
	withNil, err := GenerateProxyConfigOpts(reg, []protocol.Profile{fakeProfile()}, ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	withEmpty, err := GenerateProxyConfigOpts(reg, []protocol.Profile{fakeProfile()}, ProxyOpts{Firewall: []firewall.Rule{}})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	gotNil, err := MarshalIndented(withNil)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	gotEmpty, err := MarshalIndented(withEmpty)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(gotNil) != string(gotEmpty) {
		t.Fatalf("nil vs empty firewall rule set produced different configs:\n--- nil ---\n%s\n--- empty ---\n%s", gotNil, gotEmpty)
	}
	for _, unwanted := range []string{`"block"`, `domain_suffix`} {
		if strings.Contains(string(gotNil), unwanted) {
			t.Errorf("config with no firewall rules unexpectedly contains %q:\n%s", unwanted, gotNil)
		}
	}
	testutil.AssertGoldenJSON(t, "testdata/proxy_no_firewall.golden.json", gotNil)
}

// TestGenerateProxyConfigOpts_Firewall pins the rule-bearing case: one block
// rule per match kind (domain, CIDR, process) plus one allow rule, rendered
// as route rules ahead of the private-IP/ru-domain rules, with a single
// "block" outbound appended once (F6 item 5).
func TestGenerateProxyConfigOpts_Firewall(t *testing.T) {
	reg := fakeRegistry(t)
	rules := []firewall.Rule{
		{ID: "1", Action: firewall.ActionBlock, Domain: "ads.example.com"},
		{ID: "2", Action: firewall.ActionBlock, CIDR: "203.0.113.0/24"},
		{ID: "3", Action: firewall.ActionBlock, Process: "malware"},
		{ID: "4", Action: firewall.ActionAllow, Process: "trusted-app"},
	}
	cfg, err := GenerateProxyConfigOpts(reg, []protocol.Profile{fakeProfile()}, ProxyOpts{Firewall: rules})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/proxy_firewall.golden.json", got)
}

// TestFirewallRouteRules_Empty pins that an empty/nil rule set produces no
// outbounds and no rules at all (unit-level, independent of the full config).
func TestFirewallRouteRules_Empty(t *testing.T) {
	outbounds, rules := firewallRouteRules(nil)
	if outbounds != nil || rules != nil {
		t.Errorf("firewallRouteRules(nil) = (%v, %v), want (nil, nil)", outbounds, rules)
	}
}

// TestFirewallRouteRules_AllowOnly pins that a rule set with only "allow"
// rules never emits the "block" outbound.
func TestFirewallRouteRules_AllowOnly(t *testing.T) {
	outbounds, rules := firewallRouteRules([]firewall.Rule{
		{ID: "1", Action: firewall.ActionAllow, Domain: "example.com"},
	})
	if len(outbounds) != 0 {
		t.Errorf("firewallRouteRules with only allow rules added outbounds: %v", outbounds)
	}
	if len(rules) != 1 || rules[0].Outbound != directTag {
		t.Errorf("firewallRouteRules allow rule = %+v, want one rule routed to %q", rules, directTag)
	}
}
