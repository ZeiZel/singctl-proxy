package singbox

import (
	"strings"
	"testing"

	"singctl/internal/testutil"
	"singctl/internal/vless"
)

const realLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server"

func mustProfile(t *testing.T) vless.ServerProfile {
	t.Helper()
	p, err := vless.ParseLink(realLink)
	if err != nil {
		t.Fatalf("ParseLink: %v", err)
	}
	return p
}

func TestGenerateProxyConfig_Golden(t *testing.T) {
	// physIface == "" is the proxy-only steady state (no bind; rides Cisco).
	cfg, err := GenerateProxyConfig(mustProfile(t), "")
	if err != nil {
		t.Fatalf("GenerateProxyConfig: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/proxy.golden.json", got)
}

func TestGenerateForwarderConfig_Golden(t *testing.T) {
	cfg, err := GenerateForwarderConfig(mustProfile(t))
	if err != nil {
		t.Fatalf("GenerateForwarderConfig: %v", err)
	}
	got, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	testutil.AssertGoldenJSON(t, "testdata/forwarder.golden.json", got)
}

func TestProxyConfig_NoTunInbound_LoopGuard(t *testing.T) {
	cfg, _ := GenerateProxyConfig(mustProfile(t), "en0")
	for i, in := range cfg.Inbounds {
		if _, ok := in.(TunInbound); ok {
			t.Fatalf("PROXY inbound[%d] is a tun inbound — the persistent proxy must never own a TUN", i)
		}
	}
}

func TestGenerateProxyConfig_EmptyPhysIface_OmitsBind(t *testing.T) {
	cfg, _ := GenerateProxyConfig(mustProfile(t), "")
	got, _ := MarshalIndented(cfg)
	s := string(got)
	if strings.Contains(s, "bind_interface") {
		t.Error("expected no bind_interface when physIface is empty (proxy-only mode)")
	}
	if strings.Contains(s, "default_interface") {
		t.Error("expected no default_interface when physIface is empty")
	}
}

func TestGenerateProxyConfig_WithPhysIface_SetsBind(t *testing.T) {
	cfg, _ := GenerateProxyConfig(mustProfile(t), "en0")
	got, _ := MarshalIndented(cfg)
	s := string(got)
	if n := strings.Count(s, `"bind_interface": "en0"`); n != 2 {
		t.Errorf("expected bind_interface=en0 on both outbounds (2), got %d", n)
	}
	if !strings.Contains(s, `"default_interface": "en0"`) {
		t.Error("expected route.default_interface=en0 in VPN mode")
	}
}

func TestForwarderConfig_RelaysToProxyAndHasTun(t *testing.T) {
	cfg, _ := GenerateForwarderConfig(mustProfile(t))

	var hasTun bool
	for _, in := range cfg.Inbounds {
		if tun, ok := in.(TunInbound); ok {
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
	if cfg.Route.Final != socksOutTag {
		t.Errorf("forwarder final = %q, want %q (relay everything to proxy)", cfg.Route.Final, socksOutTag)
	}
	if !cfg.Route.AutoDetectInterface {
		t.Error("forwarder must set auto_detect_interface to keep its dialer off its own TUN")
	}
	// Literal server IP must have a belt-and-suspenders bypass rule.
	var foundBypass bool
	for _, r := range cfg.Route.Rules {
		for _, c := range r.IPCIDR {
			if c == "193.188.22.147/32" && r.Outbound == directTag {
				foundBypass = true
			}
		}
	}
	if !foundBypass {
		t.Error("expected ip_cidr bypass rule for literal server IP 193.188.22.147/32 → direct")
	}
}

func TestRoundTrip_ParseThenGenerate(t *testing.T) {
	p := mustProfile(t)
	proxy, err := GenerateProxyConfig(p, "")
	if err != nil {
		t.Fatalf("proxy: %v", err)
	}
	out, ok := proxy.Outbounds[0].(VLESSOutbound)
	if !ok {
		t.Fatalf("first outbound is not VLESS: %T", proxy.Outbounds[0])
	}
	if out.Server != "193.188.22.147" || out.ServerPort != 443 {
		t.Errorf("server = %s:%d, want 193.188.22.147:443", out.Server, out.ServerPort)
	}
	if out.UUID != p.UUID {
		t.Errorf("uuid = %s, want %s", out.UUID, p.UUID)
	}
	if out.TLS == nil || out.TLS.Reality == nil || out.TLS.Reality.PublicKey != p.Reality.PublicKey {
		t.Error("reality public key not propagated into config")
	}
	if _, err := GenerateForwarderConfig(p); err != nil {
		t.Fatalf("forwarder: %v", err)
	}
}

func TestMarshalStability_Deterministic(t *testing.T) {
	cfg, _ := GenerateProxyConfig(mustProfile(t), "en0")
	first, err := MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		got, err := MarshalIndented(cfg)
		if err != nil {
			t.Fatalf("marshal iter %d: %v", i, err)
		}
		if string(got) != string(first) {
			t.Fatalf("non-deterministic marshal at iteration %d", i)
		}
	}
}
