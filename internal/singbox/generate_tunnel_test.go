package singbox

import (
	"strings"
	"testing"

	"singctl/internal/vless"
)

// TestGenerateTunnelConfigSet_Single covers the single-key sandboxed VPN
// config: one VLESS outbound tagged "proxy" directly (no urltest group), a tun
// inbound, no socks inbound, no Clash API/experimental section, and
// route.final pointing straight at the VLESS outbound.
func TestGenerateTunnelConfigSet_Single(t *testing.T) {
	cfg, err := GenerateTunnelConfigSet(vless.SingleSet(mustProfile(t)), TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet: %v", err)
	}

	if len(cfg.Inbounds) != 1 {
		t.Fatalf("expected exactly one inbound (tun), got %d", len(cfg.Inbounds))
	}
	tun, ok := cfg.Inbounds[0].(TunInbound)
	if !ok {
		t.Fatalf("inbound[0] = %T, want TunInbound", cfg.Inbounds[0])
	}
	if tun.Tag != tunTag || !tun.AutoRoute || !tun.StrictRoute || tun.Stack != "system" {
		t.Errorf("unexpected tun inbound: %+v", tun)
	}

	if cfg.Experimental != nil {
		t.Error("tunnel config must have no experimental section")
	}
	if !cfg.Route.AutoDetectInterface {
		t.Error("route.auto_detect_interface must be true")
	}
	if cfg.Route.Final != proxyTag {
		t.Errorf("route.final = %q, want %q", cfg.Route.Final, proxyTag)
	}
	if cfg.Route.DefaultDomainResolver != localDNSTag {
		t.Errorf("route.default_domain_resolver = %q, want %q", cfg.Route.DefaultDomainResolver, localDNSTag)
	}

	// Exactly one VLESS outbound (tagged "proxy") + one direct outbound; no
	// urltest group for a single server.
	var vlessCount, urltestCount int
	for _, ob := range cfg.Outbounds {
		switch v := ob.(type) {
		case VLESSOutbound:
			vlessCount++
			if v.Tag != proxyTag {
				t.Errorf("single-server VLESS outbound tag = %q, want %q", v.Tag, proxyTag)
			}
		case URLTestOutbound:
			urltestCount++
		}
	}
	if vlessCount != 1 {
		t.Errorf("expected 1 vless outbound, got %d", vlessCount)
	}
	if urltestCount != 0 {
		t.Errorf("expected no urltest group for a single server, got %d", urltestCount)
	}

	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(got)
	if strings.Contains(out, `"socks"`) {
		t.Error("tunnel config must not contain a socks inbound")
	}
	if strings.Contains(out, "clash_api") {
		t.Error("tunnel config must not contain clash_api")
	}
}

// TestGenerateTunnelConfigSet_Multi covers the multi-key case: a urltest
// failover group must exist and route.final must point at it, and the urltest
// probe host must resolve via the bootstrap DNS rule (the DoH-detour↔urltest
// deadlock fix), exactly like GenerateProxyConfigOpts.
func TestGenerateTunnelConfigSet_Multi(t *testing.T) {
	cfg, err := GenerateTunnelConfigSet(mustSet(t, realLink, secondLink), TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet multi: %v", err)
	}

	var group *URLTestOutbound
	var vlessCount int
	for i, ob := range cfg.Outbounds {
		switch v := ob.(type) {
		case VLESSOutbound:
			vlessCount++
			want := proxyServerTag(vlessCount - 1)
			if v.Tag != want {
				t.Errorf("outbound[%d] tag = %q, want %q", i, v.Tag, want)
			}
			if v.BindInterface != "" {
				t.Errorf("outbound[%d] bind_interface = %q, want empty (sandbox forbids NIC bind)", i, v.BindInterface)
			}
		case URLTestOutbound:
			g := v
			group = &g
		}
	}
	if vlessCount != 2 {
		t.Fatalf("expected 2 vless outbounds, got %d", vlessCount)
	}
	if group == nil {
		t.Fatal("expected a urltest failover group for 2 servers")
	}
	if group.Tag != proxyTag {
		t.Errorf("urltest group tag = %q, want %q", group.Tag, proxyTag)
	}
	if len(group.Outbounds) != 2 || group.Outbounds[0] != proxyServerTag(0) || group.Outbounds[1] != proxyServerTag(1) {
		t.Errorf("urltest group outbounds = %v, want [%s %s]", group.Outbounds, proxyServerTag(0), proxyServerTag(1))
	}
	if cfg.Route.Final != proxyTag {
		t.Errorf("route.final = %q, want %q (the urltest group)", cfg.Route.Final, proxyTag)
	}

	// Probe-host bootstrap DNS fix: must resolve via the detour-less bootDNSTag
	// server, not the DoH server that detours through the not-yet-healthy group.
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != bootDNSTag {
		t.Errorf("probe host resolves via %q, want %q", got, bootDNSTag)
	}
	if !hasDNSServer(cfg, bootDNSTag) {
		t.Error("expected a detour-less boot-dns server")
	}
}

// TestGenerateTunnelConfigSet_CustomProbeHost ensures the bootstrap rule
// tracks a user-configured probe URL rather than the default host.
func TestGenerateTunnelConfigSet_CustomProbeHost(t *testing.T) {
	cfg, err := GenerateTunnelConfigSet(mustSet(t, realLink, secondLink), TunnelOpts{
		URLTest: URLTestParams{URL: "https://cp.cloudflare.com/generate_204"},
	})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet custom probe: %v", err)
	}
	if got := dnsRuleFor(cfg, "cp.cloudflare.com"); got != bootDNSTag {
		t.Errorf("custom probe host resolves via %q, want %q", got, bootDNSTag)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != "" {
		t.Errorf("default probe host should have no rule when a custom URL is set, got %q", got)
	}
}

// TestGenerateTunnelConfigSet_LiteralIPLoopGuard mirrors
// TestMulti_ForwarderLoopGuardPerLiteralIP: every literal-IP server host gets
// a belt-and-suspenders ip_cidr→direct rule so traffic to it never re-enters
// our own tun.
func TestGenerateTunnelConfigSet_LiteralIPLoopGuard(t *testing.T) {
	cfg, err := GenerateTunnelConfigSet(mustSet(t, realLink, secondLink), TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet: %v", err)
	}
	want := map[string]bool{"193.188.22.147/32": false, "45.10.20.30/32": false}
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if _, ok := want[c]; ok && r.Outbound == directTag {
				want[c] = true
			}
		}
	}
	for cidr, ok := range want {
		if !ok {
			t.Errorf("missing loop-guard ip_cidr=%s → direct", cidr)
		}
	}

	// Rule order: sniff, hijack-dns, ip_is_private, then the loop guards.
	if len(cfg.Route.Rules) < 4 {
		t.Fatalf("expected at least 4 rules, got %d", len(cfg.Route.Rules))
	}
	if cfg.Route.Rules[0].Action != "sniff" {
		t.Errorf("rule[0].action = %q, want sniff", cfg.Route.Rules[0].Action)
	}
	if cfg.Route.Rules[1].Action != "hijack-dns" || cfg.Route.Rules[1].Protocol != "dns" ||
		len(cfg.Route.Rules[1].Inbound) != 1 || cfg.Route.Rules[1].Inbound[0] != tunTag {
		t.Errorf("rule[1] = %+v, want tun-inbound hijack-dns", cfg.Route.Rules[1])
	}
	if !cfg.Route.Rules[2].IPIsPrivate || cfg.Route.Rules[2].Outbound != directTag {
		t.Errorf("rule[2] = %+v, want ip_is_private -> direct", cfg.Route.Rules[2])
	}
}

// TestGenerateTunnelConfigSet_NoBindInterface asserts bind_interface never
// appears anywhere in the marshaled config: the sandboxed appex cannot and
// must not bind sockets to a physical NIC.
func TestGenerateTunnelConfigSet_NoBindInterface(t *testing.T) {
	for name, cfgFn := range map[string]func() (Config, error){
		"single": func() (Config, error) {
			return GenerateTunnelConfigSet(vless.SingleSet(mustProfile(t)), TunnelOpts{})
		},
		"multi": func() (Config, error) {
			return GenerateTunnelConfigSet(mustSet(t, realLink, secondLink), TunnelOpts{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := cfgFn()
			if err != nil {
				t.Fatalf("GenerateTunnelConfigSet: %v", err)
			}
			got, err := MarshalIndented(cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if strings.Contains(string(got), "bind_interface") {
				t.Errorf("tunnel config must never contain bind_interface:\n%s", got)
			}
			if cfg.Route.DefaultInterface != "" {
				t.Errorf("route.default_interface must be empty, got %q", cfg.Route.DefaultInterface)
			}
		})
	}
}
