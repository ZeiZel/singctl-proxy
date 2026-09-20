package all

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// This file exercises internal/singbox's assembly logic (DNS bootstrap,
// urltest grouping, bind_interface propagation, route rules, ports, the
// tunnel generator's XHTTP rejection) through the real registry, now that the
// per-protocol switch is gone and every node comes from Registry-dispatched
// RenderNode calls. Each protocol module's own rendering correctness is
// already covered by that module's own tests; what matters here is that
// singbox wires several of them together correctly.

func TestProxyConfig_NoTunInbound(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{PhysIface: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	for i, in := range cfg.Inbounds {
		if _, ok := in.(singbox.TunInbound); ok {
			t.Fatalf("PROXY inbound[%d] is a tun inbound — the persistent proxy must never own a TUN", i)
		}
	}
}

func TestGenerateProxyConfig_EmptyPhysIface_OmitsBind(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s := string(marshal(t, cfg))
	if strings.Contains(s, "bind_interface") {
		t.Error("expected no bind_interface when physIface is empty (proxy-only mode)")
	}
	if strings.Contains(s, "default_interface") {
		t.Error("expected no default_interface when physIface is empty")
	}
}

func TestGenerateProxyConfig_WithPhysIface_SetsBind(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{PhysIface: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	s := string(marshal(t, cfg))
	if n := strings.Count(s, `"bind_interface": "en0"`); n != 2 {
		t.Errorf("expected bind_interface=en0 on both outbounds (2), got %d", n)
	}
	if !strings.Contains(s, `"default_interface": "en0"`) {
		t.Error("expected route.default_interface=en0 in VPN mode")
	}
}

func TestForwarderConfig_RelaysToProxyAndHasTun(t *testing.T) {
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}
	var hasTun bool
	for _, in := range cfg.Inbounds {
		if tun, ok := in.(singbox.TunInbound); ok {
			hasTun = true
			if !tun.AutoRoute {
				t.Error("forwarder tun must have auto_route=true")
			}
			if tun.Address[0] != "198.18.0.1/30" {
				t.Errorf("forwarder tun address = %v, want 198.18.0.1/30 first (D7)", tun.Address)
			}
		}
	}
	if !hasTun {
		t.Fatal("forwarder must contain a tun inbound")
	}
	if !cfg.Route.AutoDetectInterface {
		t.Error("forwarder must set auto_detect_interface to keep its dialer off its own TUN")
	}
	var foundBypass bool
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if c == "193.188.22.147/32" {
				foundBypass = true
			}
		}
	}
	if !foundBypass {
		t.Error("expected ip_cidr bypass rule for literal server IP 193.188.22.147/32 → direct")
	}
}

func TestMarshalStability_Deterministic(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{PhysIface: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	first := marshal(t, cfg)
	for i := 0; i < 50; i++ {
		got := marshal(t, cfg)
		if string(got) != string(first) {
			t.Fatalf("non-deterministic marshal at iteration %d", i)
		}
	}
}

func TestClashAPI_AddsProcessProbeRule(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{
		ClashAPI: &singbox.ClashAPI{ExternalController: "127.0.0.1:9090"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marshal(t, cfg)), `"process_path"`) {
		t.Error("Clash API config must add the process-probe route rule to enable process search")
	}
}

func TestNoClashAPI_OmitsClashAndProbe(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Experimental.ClashAPI != nil {
		t.Error("clash_api must be nil when not requested")
	}
	if strings.Contains(string(marshal(t, cfg)), "process_path") {
		t.Error("no process_path rule expected without Clash API")
	}
}

func TestMulti_BindInterfaceOnEachMember(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, realLink, secondLink), singbox.ProxyOpts{PhysIface: "en0"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(marshal(t, cfg))
	// 2 vless members + 1 direct outbound = 3; the urltest group itself must
	// carry no bind_interface.
	if n := strings.Count(got, `"bind_interface": "en0"`); n != 3 {
		t.Errorf("expected bind_interface=en0 on 2 members + direct (3), got %d", n)
	}
}

// dnsRuleFor returns the DNS server that resolves the given domain, or "".
func dnsRuleFor(cfg singbox.Config, domain string) string {
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

// hasDNSServer reports whether a detour-less DNS server with tag exists.
func hasDNSServer(cfg singbox.Config, tag string) bool {
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

func TestMulti_ProbeHostResolvesViaBootstrap(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, realLink, secondLink), singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != "boot-dns" {
		t.Errorf("probe host www.gstatic.com resolves via %q, want boot-dns", got)
	}
	if !hasDNSServer(cfg, "boot-dns") {
		t.Error("expected a detour-less boot-dns server")
	}
}

func TestMulti_CustomProbeHostResolvesViaBootstrap(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, realLink, secondLink), singbox.ProxyOpts{
		URLTest: singbox.URLTestParams{URL: "https://cp.cloudflare.com/generate_204"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := dnsRuleFor(cfg, "cp.cloudflare.com"); got != "boot-dns" {
		t.Errorf("custom probe host resolves via %q, want boot-dns", got)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != "" {
		t.Errorf("default host should not have a rule for a custom probe URL, got %q", got)
	}
}

func TestMulti_DomainServersResolveViaBootstrap(t *testing.T) {
	const a = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@usa.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=0cf78906&sni=microsoft.com&fp=chrome#usa"
	const b = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@auto.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=085ba77f&sni=microsoft.com&fp=chrome#auto"
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, a, b), singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"usa.cloudpath.live", "auto.cloudpath.live", "www.gstatic.com"} {
		if got := dnsRuleFor(cfg, host); got != "boot-dns" {
			t.Errorf("%s resolves via %q, want boot-dns", host, got)
		}
	}
}

func TestSingle_DomainServerResolvesViaBootstrap(t *testing.T) {
	const link = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@usa.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=0cf78906&sni=microsoft.com&fp=chrome#usa"
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, link)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := dnsRuleFor(cfg, "usa.cloudpath.live"); got != "boot-dns" {
		t.Errorf("single-server domain resolves via %q, want boot-dns", got)
	}
}

func TestSingle_IPServerNoBootstrapRule(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DNS == nil || len(cfg.DNS.Rules) != 0 {
		t.Errorf("IP-server config must have no dns rules, got %v", cfg.DNS.Rules)
	}
	if hasDNSServer(cfg, "boot-dns") {
		t.Error("IP-server config must not add a boot-dns server")
	}
}

func TestMulti_ForwarderLoopGuardPerLiteralIP(t *testing.T) {
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, mustProfiles(t, realLink, secondLink), singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"193.188.22.147/32": false, "45.10.20.30/32": false}
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if _, ok := want[c]; ok {
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

func TestForwarder_DomainServer_NoBypassRule(t *testing.T) {
	link := "vless://4ce58870-27d3-489b-87a0-3109db4fb919@example.com:443?security=tls"
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{mustParse(t, link)}, singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range cfg.Route.Rules {
		if len(r.IPCIDR) > 0 {
			t.Errorf("domain-named server must yield no ip_cidr bypass rule, got %v", r.IPCIDR)
		}
	}
}

func TestForwarder_IPv6Server_Slash128(t *testing.T) {
	link := "vless://4ce58870-27d3-489b-87a0-3109db4fb919@[2001:db8::1]:443?security=tls"
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{mustParse(t, link)}, singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if c == "2001:db8::1/128" {
				found = true
			}
		}
	}
	if !found {
		t.Error("want ip_cidr bypass 2001:db8::1/128 for IPv6 literal server")
	}
}

// TestForwarderRelayTargetMatchesProxySocksInbound guards the single most
// safety-critical invariant: the forwarder must relay to exactly where the
// proxy listens. A mismatch = total outage in VPN mode.
func TestForwarderRelayTargetMatchesProxySocksInbound(t *testing.T) {
	p := mustParse(t, "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=tls")
	proxy, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{p}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	fwd, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{p}, singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}

	var socksIn singbox.SocksInbound
	for _, in := range proxy.Inbounds {
		if s, ok := in.(singbox.SocksInbound); ok {
			socksIn = s
		}
	}
	var socksOut singbox.SocksOutbound
	for _, out := range fwd.Outbounds {
		if s, ok := out.(singbox.SocksOutbound); ok {
			socksOut = s
		}
	}
	if socksIn.Listen != socksOut.Server || socksIn.ListenPort != socksOut.ServerPort {
		t.Errorf("relay target mismatch: forwarder socks-out %s:%d != proxy socks-in %s:%d",
			socksOut.Server, socksOut.ServerPort, socksIn.Listen, socksIn.ListenPort)
	}
}

func TestForwarder_DNSHijackedThroughProxy(t *testing.T) {
	p := mustParse(t, "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=tls")
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{p}, singbox.DefaultPorts())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DNS == nil || len(cfg.DNS.Servers) == 0 {
		t.Fatal("forwarder must have its own DNS block (hijacked DNS is answered locally in 1.12)")
	}
	if cfg.DNS.Servers[0].Detour != "socks-out" {
		t.Errorf("forwarder DNS detour = %q, want socks-out (resolve over the proxy tunnel)", cfg.DNS.Servers[0].Detour)
	}
	if cfg.DNS.Strategy != "ipv4_only" {
		t.Errorf("forwarder DNS strategy = %q, want ipv4_only", cfg.DNS.Strategy)
	}
	var hasHijack, hasPrivate bool
	for _, r := range cfg.Route.Rules {
		if r.Action == "hijack-dns" && r.Protocol == "dns" && reflect.DeepEqual(r.Inbound, []string{"tun-in"}) {
			hasHijack = true
		}
		if r.IPIsPrivate {
			hasPrivate = true
		}
	}
	if !hasHijack {
		t.Error("forwarder must have a tun-scoped {protocol:dns}->hijack-dns rule")
	}
	if !hasPrivate {
		t.Error("forwarder must short-circuit LAN/private traffic with ip_is_private->direct")
	}
}

// --- ports ---

func TestGenerateProxyConfig_CustomPorts(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{
		Ports: singbox.Ports{Socks: 7890, HTTP: 7891},
	})
	if err != nil {
		t.Fatal(err)
	}
	socks := cfg.Inbounds[0].(singbox.SocksInbound)
	httpIn := cfg.Inbounds[1].(singbox.HTTPInbound)
	if socks.ListenPort != 7890 || httpIn.ListenPort != 7891 {
		t.Errorf("inbound ports = %d/%d, want 7890/7891", socks.ListenPort, httpIn.ListenPort)
	}
}

func TestGenerateForwarderConfig_DialsCustomSocks(t *testing.T) {
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.Ports{Socks: 7890})
	if err != nil {
		t.Fatal(err)
	}
	var out *singbox.SocksOutbound
	for _, o := range cfg.Outbounds {
		if s, ok := o.(singbox.SocksOutbound); ok {
			out = &s
			break
		}
	}
	if out == nil {
		t.Fatal("forwarder has no socks outbound")
	}
	if out.ServerPort != 7890 {
		t.Errorf("forwarder dials socks port %d, want 7890", out.ServerPort)
	}
}

func TestPorts_ZeroValueKeepsDefaults(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	socks := cfg.Inbounds[0].(singbox.SocksInbound)
	httpIn := cfg.Inbounds[1].(singbox.HTTPInbound)
	if socks.ListenPort != 1080 || httpIn.ListenPort != 2080 {
		t.Errorf("zero Ports = %d/%d, want defaults 1080/2080", socks.ListenPort, httpIn.ListenPort)
	}
}

// --- VLESS transport/TLS field shapes through the full pipeline ---

func proxyVLESS(t *testing.T, raw string) singbox.VLESSOutbound {
	t.Helper()
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, raw)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	out, ok := cfg.Outbounds[0].(singbox.VLESSOutbound)
	if !ok {
		t.Fatalf("outbound[0] is %T, want VLESSOutbound", cfg.Outbounds[0])
	}
	return out
}

func TestGenerateProxy_WS_EmitsHostHeader(t *testing.T) {
	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?type=ws&security=tls&host=cdn.example.com&path=%2Fws")
	if out.Transport == nil || out.Transport.Type != "ws" {
		t.Fatalf("transport = %+v, want ws", out.Transport)
	}
	if out.Transport.Path != "/ws" {
		t.Errorf("path = %q, want /ws", out.Transport.Path)
	}
	if out.Transport.Headers["Host"] != "cdn.example.com" {
		t.Errorf("ws Host header = %q, want cdn.example.com", out.Transport.Headers["Host"])
	}
}

func TestGenerateProxy_HTTP_EmitsHostArray(t *testing.T) {
	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?type=http&security=tls&host=a.example.com&path=%2Fp")
	if out.Transport == nil || out.Transport.Type != "http" {
		t.Fatalf("transport = %+v, want http", out.Transport)
	}
	if want := []string{"a.example.com"}; !reflect.DeepEqual(out.Transport.Host, want) {
		t.Errorf("http host = %#v, want %#v", out.Transport.Host, want)
	}
}

func TestGenerateProxy_TCP_OmitsTransport(t *testing.T) {
	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?security=tls")
	if out.Transport != nil {
		t.Errorf("tcp transport should be omitted, got %+v", out.Transport)
	}
}

func TestGenerateProxy_Insecure_Propagated(t *testing.T) {
	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	link := "vless://" + uuid + "@1.2.3.4:443?security=tls&allowInsecure=1"
	out := proxyVLESS(t, link)
	if out.TLS == nil || !out.TLS.Insecure {
		t.Fatal("want tls.insecure=true")
	}
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, link)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marshal(t, cfg)), `"insecure": true`) {
		t.Error(`marshaled config should contain "insecure": true`)
	}
}

func TestGenerateProxy_Flow_Propagated(t *testing.T) {
	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	link := "vless://" + uuid + "@1.2.3.4:443?flow=xtls-rprx-vision&security=reality&pbk=k"
	out := proxyVLESS(t, link)
	if out.Flow != "xtls-rprx-vision" {
		t.Errorf("flow = %q, want xtls-rprx-vision", out.Flow)
	}
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, link)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(marshal(t, cfg)), `"flow": "xtls-rprx-vision"`) {
		t.Error(`marshaled config should contain "flow": "xtls-rprx-vision"`)
	}
}

// --- mixed multi-protocol assembly shape (types + tags + urltest members) ---

func TestGenerateProxy_MixedProtocolMulti_Types(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg,
		mustProfiles(t, realLink, hysteria2Link, shadowsocksLink), singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var gotVLESS, gotHy2, gotSS, gotGroup bool
	var tags []string
	for _, ob := range cfg.Outbounds {
		switch o := ob.(type) {
		case singbox.VLESSOutbound:
			gotVLESS = true
			tags = append(tags, o.Tag)
		case singbox.Hysteria2Outbound:
			gotHy2 = true
			tags = append(tags, o.Tag)
		case singbox.ShadowsocksOutbound:
			gotSS = true
			tags = append(tags, o.Tag)
		case singbox.URLTestOutbound:
			gotGroup = true
			if len(o.Outbounds) != 3 {
				t.Errorf("urltest group has %d members, want 3", len(o.Outbounds))
			}
		}
	}
	if !gotVLESS || !gotHy2 || !gotSS {
		t.Errorf("expected vless=%v hysteria2=%v shadowsocks=%v members, all true", gotVLESS, gotHy2, gotSS)
	}
	if !gotGroup {
		t.Fatal("expected a urltest group for the mixed 3-server set")
	}
	if len(tags) != 3 {
		t.Errorf("expected 3 tagged members, got %d: %v", len(tags), tags)
	}
}

func TestMulti_XHTTPAndPlainTCP(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, xhttpRealityLink, realLink), singbox.ProxyOpts{})
	if err != nil {
		t.Fatal(err)
	}
	var gotXHTTP, gotVLESS bool
	var tags []string
	for _, ob := range cfg.Outbounds {
		switch o := ob.(type) {
		case singbox.XHTTPOutbound:
			gotXHTTP = true
			tags = append(tags, o.Tag)
		case singbox.VLESSOutbound:
			gotVLESS = true
			tags = append(tags, o.Tag)
		}
	}
	if !gotXHTTP {
		t.Error("expected one XHTTPOutbound member in the mixed set")
	}
	if !gotVLESS {
		t.Error("expected one plain VLESSOutbound member in the mixed set")
	}
	var group singbox.URLTestOutbound
	var foundGroup bool
	for _, ob := range cfg.Outbounds {
		if g, ok := ob.(singbox.URLTestOutbound); ok {
			group, foundGroup = g, true
		}
	}
	if !foundGroup {
		t.Fatal("expected a urltest group for the multi-server set")
	}
	if len(group.Outbounds) != 2 {
		t.Errorf("urltest group has %d members, want 2", len(group.Outbounds))
	}
	for _, tag := range tags {
		var found bool
		for _, gt := range group.Outbounds {
			if gt == tag {
				found = true
			}
		}
		if !found {
			t.Errorf("urltest group members %v missing tag %q", group.Outbounds, tag)
		}
	}
}

// --- tunnel generator (App Store SKU single-instance VPN) ---

func TestGenerateTunnelConfigSet_Single(t *testing.T) {
	cfg, err := singbox.GenerateTunnelConfigSet(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet: %v", err)
	}
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("expected exactly one inbound (tun), got %d", len(cfg.Inbounds))
	}
	tun, ok := cfg.Inbounds[0].(singbox.TunInbound)
	if !ok || !tun.AutoRoute || !tun.StrictRoute || tun.Stack != "system" {
		t.Errorf("unexpected tun inbound: %+v (ok=%v)", cfg.Inbounds[0], ok)
	}
	if cfg.Experimental != nil {
		t.Error("tunnel config must have no experimental section")
	}
	if !cfg.Route.AutoDetectInterface {
		t.Error("route.auto_detect_interface must be true")
	}
	if cfg.Route.Final != "proxy" {
		t.Errorf("route.final = %q, want proxy", cfg.Route.Final)
	}

	var vlessCount, urltestCount int
	for _, ob := range cfg.Outbounds {
		switch v := ob.(type) {
		case singbox.VLESSOutbound:
			vlessCount++
			if v.Tag != "proxy" {
				t.Errorf("single-server VLESS outbound tag = %q, want proxy", v.Tag)
			}
		case singbox.URLTestOutbound:
			urltestCount++
		}
	}
	if vlessCount != 1 {
		t.Errorf("expected 1 vless outbound, got %d", vlessCount)
	}
	if urltestCount != 0 {
		t.Errorf("expected no urltest group for a single server, got %d", urltestCount)
	}

	out := string(marshal(t, cfg))
	if strings.Contains(out, `"socks"`) {
		t.Error("tunnel config must not contain a socks inbound")
	}
	if strings.Contains(out, "clash_api") {
		t.Error("tunnel config must not contain clash_api")
	}
}

func TestGenerateTunnelConfigSet_Multi(t *testing.T) {
	cfg, err := singbox.GenerateTunnelConfigSet(testReg, mustProfiles(t, realLink, secondLink), singbox.TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet multi: %v", err)
	}
	var group *singbox.URLTestOutbound
	var vlessCount int
	for _, ob := range cfg.Outbounds {
		switch v := ob.(type) {
		case singbox.VLESSOutbound:
			vlessCount++
			if v.BindInterface != "" {
				t.Errorf("bind_interface = %q, want empty (sandbox forbids NIC bind)", v.BindInterface)
			}
		case singbox.URLTestOutbound:
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
	if cfg.Route.Final != "proxy" {
		t.Errorf("route.final = %q, want proxy (the urltest group)", cfg.Route.Final)
	}
	if got := dnsRuleFor(cfg, "www.gstatic.com"); got != "boot-dns" {
		t.Errorf("probe host resolves via %q, want boot-dns", got)
	}
	if !hasDNSServer(cfg, "boot-dns") {
		t.Error("expected a detour-less boot-dns server")
	}
}

func TestGenerateTunnelConfigSet_LiteralIPLoopGuard(t *testing.T) {
	cfg, err := singbox.GenerateTunnelConfigSet(testReg, mustProfiles(t, realLink, secondLink), singbox.TunnelOpts{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"193.188.22.147/32": false, "45.10.20.30/32": false}
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if _, ok := want[c]; ok {
				want[c] = true
			}
		}
	}
	for cidr, ok := range want {
		if !ok {
			t.Errorf("missing loop-guard ip_cidr=%s → direct", cidr)
		}
	}
}

func TestGenerateTunnelConfigSet_NoBindInterface(t *testing.T) {
	for name, profiles := range map[string][]protocol.Profile{
		"single": {mustParse(t, realLink)},
		"multi":  mustProfiles(t, realLink, secondLink),
	} {
		t.Run(name, func(t *testing.T) {
			cfg, err := singbox.GenerateTunnelConfigSet(testReg, profiles, singbox.TunnelOpts{})
			if err != nil {
				t.Fatalf("GenerateTunnelConfigSet: %v", err)
			}
			got := marshal(t, cfg)
			if strings.Contains(string(got), "bind_interface") {
				t.Errorf("tunnel config must never contain bind_interface:\n%s", got)
			}
			if cfg.Route.DefaultInterface != "" {
				t.Errorf("route.default_interface must be empty, got %q", cfg.Route.DefaultInterface)
			}
		})
	}
}

func TestGenerateTunnelConfigSet_RejectsXHTTP(t *testing.T) {
	if _, err := singbox.GenerateTunnelConfigSet(testReg, []protocol.Profile{mustParse(t, xhttpRealityLink)}, singbox.TunnelOpts{}); !errors.Is(err, singbox.ErrXHTTPUnsupportedInTunnel) {
		t.Fatalf("got %v, want ErrXHTTPUnsupportedInTunnel", err)
	}

	// A mixed set is rejected too: the appex has no way to run only part of it.
	mixed := mustProfiles(t,
		"vless://4ce58870-27d3-489b-87a0-3109db4fb919@a.example.com:443?type=tcp&security=tls&sni=a.example.com#tcp",
		"vless://4ce58870-27d3-489b-87a0-3109db4fb919@b.example.com:443?type=xhttp&security=tls&sni=b.example.com&path=%2Fxh#xh",
	)
	if _, err := singbox.GenerateTunnelConfigSet(testReg, mixed, singbox.TunnelOpts{}); !errors.Is(err, singbox.ErrXHTTPUnsupportedInTunnel) {
		t.Fatalf("mixed set: got %v, want ErrXHTTPUnsupportedInTunnel", err)
	}
}

// --- WireGuard: the one KindEndpoint protocol — lands in cfg.Endpoints, not
// cfg.Outbounds, but still joins the same urltest failover group. ---

func TestGenerateProxy_WireGuard_LandsInEndpoints(t *testing.T) {
	p := mustParse(t, wireguardConformanceConfig)
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{p}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected exactly one endpoint, got %d: %+v", len(cfg.Endpoints), cfg.Endpoints)
	}
	ep, ok := cfg.Endpoints[0].(*singbox.WireGuardEndpoint)
	if !ok {
		t.Fatalf("endpoint[0] is %T, want *singbox.WireGuardEndpoint", cfg.Endpoints[0])
	}
	if ep.Tag != "proxy" {
		t.Errorf("single-profile endpoint tag = %q, want proxy", ep.Tag)
	}
	for _, ob := range cfg.Outbounds {
		if _, ok := ob.(*singbox.WireGuardEndpoint); ok {
			t.Error("the WireGuard endpoint must not also appear in outbounds")
		}
	}
	if cfg.Route.Final != "proxy" {
		t.Errorf("route.final = %q, want proxy (the endpoint's own tag)", cfg.Route.Final)
	}
}

// TestGenerateProxy_WireGuard_JoinsFailoverGroup proves an endpoint-kind
// profile can sit in the same urltest failover group as an outbound-kind
// one — adapter.Endpoint embeds adapter.Outbound in sing-box, so this must
// work exactly like a second VLESS server would.
func TestGenerateProxy_WireGuard_JoinsFailoverGroup(t *testing.T) {
	profiles := mustProfiles(t, realLink)
	wg := mustParse(t, wireguardConformanceConfig)
	profiles = append(profiles, wg)

	cfg, err := singbox.GenerateProxyConfigOpts(testReg, profiles, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected exactly one endpoint, got %d", len(cfg.Endpoints))
	}
	ep := cfg.Endpoints[0].(*singbox.WireGuardEndpoint)
	if ep.Tag != "proxy-1" {
		t.Errorf("endpoint tag = %q, want proxy-1 (second member)", ep.Tag)
	}
	var group *singbox.URLTestOutbound
	for _, ob := range cfg.Outbounds {
		if g, ok := ob.(singbox.URLTestOutbound); ok {
			group = &g
		}
	}
	if group == nil {
		t.Fatal("expected a urltest failover group for 2 members")
	}
	var found bool
	for _, tag := range group.Outbounds {
		if tag == ep.Tag {
			found = true
		}
	}
	if !found {
		t.Errorf("urltest group %v does not include the endpoint's tag %q", group.Outbounds, ep.Tag)
	}
}

// TestGenerateTunnel_NonVLESSProtocol covers the App Store SKU generator for a
// non-vless protocol: it must go through the registry and emit the matching
// per-protocol outbound, and it must carry no bind_interface.
func TestGenerateTunnel_NonVLESSProtocol(t *testing.T) {
	cfg, err := singbox.GenerateTunnelConfigSet(testReg, []protocol.Profile{mustParse(t, hysteria2Link)}, singbox.TunnelOpts{})
	if err != nil {
		t.Fatalf("GenerateTunnelConfigSet: %v", err)
	}
	if len(cfg.Outbounds) == 0 {
		t.Fatal("no outbounds generated")
	}
	out, ok := cfg.Outbounds[0].(singbox.Hysteria2Outbound)
	if !ok {
		t.Fatalf("outbound[0] is %T, want Hysteria2Outbound", cfg.Outbounds[0])
	}
	if out.Tag != "proxy" {
		t.Errorf("Tag = %q, want proxy", out.Tag)
	}
	if out.BindInterface != "" {
		t.Errorf("BindInterface = %q, want empty (sandbox forbids NIC bind)", out.BindInterface)
	}
}
