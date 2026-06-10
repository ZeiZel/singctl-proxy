package singbox

import (
	"reflect"
	"strings"
	"testing"

	"singctl/internal/vless"
)

const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"

func mustParse(t *testing.T, link string) vless.ServerProfile {
	t.Helper()
	p, err := vless.ParseLink(link)
	if err != nil {
		t.Fatalf("ParseLink(%q): %v", link, err)
	}
	return p
}

func proxyVLESS(t *testing.T, link string) VLESSOutbound {
	t.Helper()
	cfg, err := GenerateProxyConfig(mustParse(t, link), "")
	if err != nil {
		t.Fatalf("GenerateProxyConfig: %v", err)
	}
	out, ok := cfg.Outbounds[0].(VLESSOutbound)
	if !ok {
		t.Fatalf("outbound[0] is %T, want VLESSOutbound", cfg.Outbounds[0])
	}
	return out
}

func TestGenerateProxy_WS_EmitsHostHeader(t *testing.T) {
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
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?type=http&security=tls&host=a.example.com&path=%2Fp")
	if out.Transport == nil || out.Transport.Type != "http" {
		t.Fatalf("transport = %+v, want http", out.Transport)
	}
	if want := []string{"a.example.com"}; !reflect.DeepEqual(out.Transport.Host, want) {
		t.Errorf("http host = %#v, want %#v", out.Transport.Host, want)
	}
}

func TestGenerateProxy_TCP_OmitsTransport(t *testing.T) {
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?security=tls")
	if out.Transport != nil {
		t.Errorf("tcp transport should be omitted, got %+v", out.Transport)
	}
}

func TestGenerateProxy_PlainTLS_ALPN_NoReality(t *testing.T) {
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&fp=chrome&alpn=h2,http%2F1.1")
	if out.TLS == nil || !out.TLS.Enabled {
		t.Fatal("want tls.enabled=true")
	}
	if out.TLS.Reality != nil {
		t.Error("plain TLS must NOT carry a reality block")
	}
	if out.TLS.UTLS == nil || out.TLS.UTLS.Fingerprint != "chrome" {
		t.Errorf("utls = %+v, want fingerprint chrome", out.TLS.UTLS)
	}
	if want := []string{"h2", "http/1.1"}; !reflect.DeepEqual(out.TLS.ALPN, want) {
		t.Errorf("alpn = %#v, want %#v", out.TLS.ALPN, want)
	}
}

func TestGenerateProxy_None_OmitsTLS(t *testing.T) {
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443")
	if out.TLS != nil {
		t.Errorf("security=none must omit tls, got %+v", out.TLS)
	}
}

func TestGenerateProxy_Insecure_Propagated(t *testing.T) {
	out := proxyVLESS(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=1")
	if out.TLS == nil || !out.TLS.Insecure {
		t.Fatal("want tls.insecure=true")
	}
	cfg, _ := GenerateProxyConfig(mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=1"), "")
	js, _ := MarshalIndented(cfg)
	if !strings.Contains(string(js), `"insecure": true`) {
		t.Error(`marshaled config should contain "insecure": true`)
	}
}

func TestGenerateProxy_Flow_Propagated(t *testing.T) {
	link := "vless://" + uuid + "@1.2.3.4:443?flow=xtls-rprx-vision&security=reality&pbk=k"
	out := proxyVLESS(t, link)
	if out.Flow != "xtls-rprx-vision" {
		t.Errorf("flow = %q, want xtls-rprx-vision", out.Flow)
	}
	cfg, _ := GenerateProxyConfig(mustParse(t, link), "")
	js, _ := MarshalIndented(cfg)
	if !strings.Contains(string(js), `"flow": "xtls-rprx-vision"`) {
		t.Error(`marshaled config should contain "flow": "xtls-rprx-vision"`)
	}
}

func TestForwarder_IPv6Server_Slash128(t *testing.T) {
	cfg, _ := GenerateForwarderConfig(mustParse(t, "vless://"+uuid+"@[2001:db8::1]:443?security=tls"))
	if !forwarderHasCIDR(cfg, "2001:db8::1/128") {
		t.Error("want ip_cidr bypass 2001:db8::1/128 for IPv6 literal server")
	}
}

func TestForwarder_DomainServer_NoBypassRule(t *testing.T) {
	cfg, _ := GenerateForwarderConfig(mustParse(t, "vless://"+uuid+"@example.com:443?security=tls"))
	for _, r := range cfg.Route.Rules {
		if len(r.IPCIDR) > 0 {
			t.Errorf("domain-named server must yield no ip_cidr bypass rule, got %v", r.IPCIDR)
		}
	}
}

// TestForwarderRelayTargetMatchesProxySocksInbound guards the single most
// safety-critical invariant: the forwarder must relay to exactly where the proxy
// listens. A mismatch = total outage in VPN mode.
func TestForwarderRelayTargetMatchesProxySocksInbound(t *testing.T) {
	p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls")
	proxy, _ := GenerateProxyConfig(p, "")
	fwd, _ := GenerateForwarderConfig(p)

	var socksIn SocksInbound
	for _, in := range proxy.Inbounds {
		if s, ok := in.(SocksInbound); ok {
			socksIn = s
		}
	}
	var socksOut SocksOutbound
	for _, out := range fwd.Outbounds {
		if s, ok := out.(SocksOutbound); ok {
			socksOut = s
		}
	}
	if socksIn.Listen != socksOut.Server || socksIn.ListenPort != socksOut.ServerPort {
		t.Errorf("relay target mismatch: forwarder socks-out %s:%d != proxy socks-in %s:%d",
			socksOut.Server, socksOut.ServerPort, socksIn.Listen, socksIn.ListenPort)
	}
}

// TestForwarder_DNSHijackedThroughProxy locks the resolved PLAN open-q 8: the
// forwarder hijacks DNS and resolves it via DoH over the proxy (detour=socks-out),
// plus short-circuits LAN traffic locally (open-q 9).
func TestForwarder_DNSHijackedThroughProxy(t *testing.T) {
	cfg, _ := GenerateForwarderConfig(mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls"))

	if cfg.DNS == nil || len(cfg.DNS.Servers) == 0 {
		t.Fatal("forwarder must have its own DNS block (hijacked DNS is answered locally in 1.12)")
	}
	if cfg.DNS.Servers[0].Detour != socksOutTag {
		t.Errorf("forwarder DNS detour = %q, want %q (resolve over the proxy tunnel)", cfg.DNS.Servers[0].Detour, socksOutTag)
	}
	if cfg.DNS.Strategy != "ipv4_only" {
		t.Errorf("forwarder DNS strategy = %q, want ipv4_only", cfg.DNS.Strategy)
	}

	var hasHijack, hasPrivate bool
	for _, r := range cfg.Route.Rules {
		if r.Action == "hijack-dns" && r.Protocol == "dns" && reflect.DeepEqual(r.Inbound, []string{tunTag}) {
			hasHijack = true
		}
		if r.IPIsPrivate && r.Outbound == directTag {
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

func forwarderHasCIDR(cfg Config, cidr string) bool {
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if c == cidr && r.Outbound == directTag {
				return true
			}
		}
	}
	return false
}
