package singbox

import (
	"strings"
	"testing"

	"singctl/internal/testutil"
	"singctl/internal/vless"
)

// secondLink is a distinct fallback server (plain TLS, different host/port).
const secondLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@45.10.20.30:8443?security=tls&sni=fallback.example#fallback"

func mustSet(t *testing.T, raws ...string) vless.ProfileSet {
	t.Helper()
	set, err := vless.ParseLinks(raws)
	if err != nil {
		t.Fatalf("ParseLinks: %v", err)
	}
	return set
}

func TestGenerateProxyConfig_ClashAPI_Golden(t *testing.T) {
	cfg, err := GenerateProxyConfigOpts(vless.SingleSet(mustProfile(t)), ProxyOpts{
		ClashAPI: &ClashAPI{ExternalController: "127.0.0.1:9090", Secret: "test-secret"},
	})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/proxy_clashapi.golden.json", got)
}

func TestGenerateProxyConfig_Multi_Golden(t *testing.T) {
	cfg, err := GenerateProxyConfigOpts(mustSet(t, realLink, secondLink), ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts multi: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/proxy_multi.golden.json", got)
}

func TestGenerateForwarder_Multi_Golden(t *testing.T) {
	cfg, err := GenerateForwarderConfigSet(mustSet(t, realLink, secondLink), DefaultPorts())
	if err != nil {
		t.Fatalf("GenerateForwarderConfigSet multi: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/forwarder_multi.golden.json", got)
}

func TestClashAPI_AddsProcessProbeRule(t *testing.T) {
	cfg, _ := GenerateProxyConfigOpts(vless.SingleSet(mustProfile(t)), ProxyOpts{
		ClashAPI: &ClashAPI{ExternalController: "127.0.0.1:9090"},
	})
	var found bool
	for _, r := range cfg.Route.Rules {
		for _, p := range r.ProcessPath {
			if p == processProbePath {
				found = true
			}
		}
	}
	if !found {
		t.Error("Clash API config must add the process-probe route rule to enable process search")
	}
}

func TestNoClashAPI_OmitsClashAndProbe(t *testing.T) {
	cfg, _ := GenerateProxyConfigOpts(vless.SingleSet(mustProfile(t)), ProxyOpts{})
	if cfg.Experimental.ClashAPI != nil {
		t.Error("clash_api must be nil when not requested")
	}
	got, _ := MarshalIndented(cfg)
	if strings.Contains(string(got), "process_path") {
		t.Error("no process_path rule expected without Clash API")
	}
}

func TestMulti_BindInterfaceOnEachMember(t *testing.T) {
	cfg, _ := GenerateProxyConfigOpts(mustSet(t, realLink, secondLink), ProxyOpts{PhysIface: "en0"})
	got, _ := MarshalIndented(cfg)
	// Both vless members must carry bind_interface (the group must not).
	if n := strings.Count(string(got), `"bind_interface": "en0"`); n != 3 {
		// 2 vless members + 1 direct outbound = 3
		t.Errorf("expected bind_interface=en0 on 2 members + direct (3), got %d", n)
	}
	if cfg.Route.Final != proxyTag {
		t.Errorf("route.final = %q, want %q (the urltest group)", cfg.Route.Final, proxyTag)
	}
}

// dnsRuleFor returns the DNS server that resolves the given domain, or "".
func dnsRuleFor(cfg Config, domain string) string {
	if cfg.DNS == nil {
		return ""
	}
	for _, r := range cfg.DNS.Rules {
		for _, d := range r.Domain {
			if d == domain {
				return r.Server
			}
		}
	}
	return ""
}

// hasDNSServer reports whether a DNS server with the given tag exists. The
// bootstrap DoH server must be detour-less (a `detour: direct` to an empty
// direct outbound is rejected by sing-box at start), so we also assert no detour.
func hasDNSServer(cfg Config, tag string) bool {
	if cfg.DNS == nil {
		return false
	}
	for _, s := range cfg.DNS.Servers {
		if s.Tag == tag && s.Detour == "" {
			return true
		}
	}
	return false
}

// TestMulti_ProbeHostResolvesViaBootstrap locks in the bootstrap fix: in
// multi-server mode the urltest probe host must resolve via the bootstrap DoH
// (detour=direct), not via the DoH server that detours through the urltest
// group — otherwise the group's health check can never bootstrap.
func TestMulti_ProbeHostResolvesViaBootstrap(t *testing.T) {
	cfg, err := GenerateProxyConfigOpts(mustSet(t, realLink, secondLink), ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts multi: %v", err)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != bootDNSTag {
		t.Errorf("probe host www.gstatic.com resolves via %q, want %q", got, bootDNSTag)
	}
	if !hasDNSServer(cfg, bootDNSTag) {
		t.Errorf("expected a detour-less %q DNS server", bootDNSTag)
	}
}

// TestMulti_CustomProbeHostResolvesViaBootstrap ensures the bootstrap rule tracks
// a user-configured probe URL rather than the default host.
func TestMulti_CustomProbeHostResolvesViaBootstrap(t *testing.T) {
	cfg, _ := GenerateProxyConfigOpts(mustSet(t, realLink, secondLink), ProxyOpts{
		URLTest: URLTestParams{URL: "https://cp.cloudflare.com/generate_204"},
	})
	if got := dnsRuleFor(cfg, "cp.cloudflare.com"); got != bootDNSTag {
		t.Errorf("custom probe host resolves via %q, want %q", got, bootDNSTag)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != "" {
		t.Errorf("default host should not have a rule for a custom probe URL, got %q", got)
	}
}

// TestMulti_DomainServersResolveViaBootstrap is the regression for the real-world
// break: when the VLESS servers are addressed by domain (Reality servers like
// usa.cloudpath.live), their hostnames MUST resolve via the bootstrap DoH over
// "direct" — else they loop through the proxy (lands on 1.1.1.1, TLS fails) or
// hit the corporate resolver under Cisco ("lookup ...: i/o timeout").
func TestMulti_DomainServersResolveViaBootstrap(t *testing.T) {
	const a = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@usa.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=0cf78906&sni=microsoft.com&fp=chrome#usa"
	const b = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@auto.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=085ba77f&sni=microsoft.com&fp=chrome#auto"
	cfg, err := GenerateProxyConfigOpts(mustSet(t, a, b), ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts multi domains: %v", err)
	}
	for _, host := range []string{"usa.cloudpath.live", "auto.cloudpath.live", "www.gstatic.com"} {
		if got := dnsRuleFor(cfg, host); got != bootDNSTag {
			t.Errorf("%s resolves via %q, want %q", host, got, bootDNSTag)
		}
	}
	if !hasDNSServer(cfg, bootDNSTag) {
		t.Errorf("expected a detour-less %q DNS server", bootDNSTag)
	}
}

// TestSingle_DomainServerResolvesViaBootstrap ensures the fix also covers the
// single-server case: a domain-addressed server must resolve via the bootstrap
// DoH, not the corporate/system resolver.
func TestSingle_DomainServerResolvesViaBootstrap(t *testing.T) {
	const link = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@usa.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=0cf78906&sni=microsoft.com&fp=chrome#usa"
	cfg, err := GenerateProxyConfigOpts(mustSet(t, link), ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts single domain: %v", err)
	}
	if got := dnsRuleFor(cfg, "usa.cloudpath.live"); got != bootDNSTag {
		t.Errorf("single-server domain resolves via %q, want %q", got, bootDNSTag)
	}
	if !hasDNSServer(cfg, bootDNSTag) {
		t.Errorf("expected a detour-less %q DNS server", bootDNSTag)
	}
}

// TestSingle_IPServerNoBootstrapRule guards the historical single-server config
// with an IP-addressed server: no hostname to resolve, so no bootstrap rule and
// no boot-dns server — the config stays byte-identical to the golden.
func TestSingle_IPServerNoBootstrapRule(t *testing.T) {
	cfg, _ := GenerateProxyConfigOpts(vless.SingleSet(mustProfile(t)), ProxyOpts{})
	if cfg.DNS == nil || len(cfg.DNS.Rules) != 0 {
		t.Errorf("IP-server config must have no dns rules, got %v", cfg.DNS.Rules)
	}
	if hasDNSServer(cfg, bootDNSTag) {
		t.Error("IP-server config must not add a boot-dns server")
	}
}

func TestMulti_ForwarderLoopGuardPerLiteralIP(t *testing.T) {
	cfg, _ := GenerateForwarderConfigSet(mustSet(t, realLink, secondLink), DefaultPorts())
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
			t.Errorf("missing forwarder loop-guard ip_cidr=%s → direct", cidr)
		}
	}
}
