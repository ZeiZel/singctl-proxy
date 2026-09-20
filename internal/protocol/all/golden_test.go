package all

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
	"singctl/internal/testutil"
)

// This file pins the full-document byte fidelity of internal/singbox's
// generators through the registry-driven code path: every golden file under
// internal/singbox/testdata/ must still match without being regenerated (see
// docs/protocol-modules.md's acceptance criteria). Each protocol module's own
// test file already proves its RenderNode output matches the corresponding
// golden file's outbounds[N] fragment; what these tests add is the FULL
// config document (log/dns/inbounds/route/experimental wrapper too), built
// the same way production code builds it: Registry() + registry.Parse/
// ParseAll + singbox.Generate*.

// realLink/secondLink mirror the historical internal/link test fixtures byte
// for byte, so the goldens they were pinned against still apply unchanged.
const (
	realLink   = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server"
	secondLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@45.10.20.30:8443?security=tls&sni=fallback.example#fallback"

	hysteria2Link    = "hysteria2://hy2-pw@hy2.example.com:443?sni=hy2.example.com&obfs=salamander&obfs-password=salamander-pw&up=50&down=150#hysteria2-test"
	shadowsocksLink  = "ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206c3MtcHc=@ss.example.com:8388#ss-test" // base64("2022-blake3-aes-128-gcm:ss-pw")
	trojanLink       = "trojan://trojan-pw@trojan.example.com:443?sni=trojan.example.com#trojan-test"
	hysteriaLink     = "hysteria://hy.example.com:443?auth=auth-str&upmbps=100&downmbps=200&obfs=obfs-pw&peer=hy.example.com#hysteria-test"
	tuicLink         = "tuic://4ce58870-27d3-489b-87a0-3109db4fb919:tuic-pw@tuic.example.com:443?congestion_control=bbr&udp_relay_mode=native&zero_rtt_handshake=1&sni=tuic.example.com#tuic-test"
	anytlsLink       = "anytls://anytls-pw@anytls.example.com:443?sni=anytls.example.com#anytls-test"
	xhttpRealityLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=xhttp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome&host=cdn.example.com&path=%2Fxh&mode=stream-one#xhttp-reality"
)

// testReg is the real, default registry — the same one production code
// builds from Registry().
var testReg = Registry()

func mustParse(t *testing.T, raw string) protocol.Profile {
	t.Helper()
	p, err := testReg.Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	return p
}

func mustProfiles(t *testing.T, raws ...string) []protocol.Profile {
	t.Helper()
	ps, err := testReg.ParseAll(raws)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	return ps
}

// vmessLink builds a minimal vmess:// link, base64-JSON-encoding fields the
// same way a real client does. Mirrors vmess's own test fixture
// (vmess.example.com / cdn.example.com / ws / tls), so it renders to exactly
// what proxy_vmess.golden.json pins.
func vmessLink(t *testing.T) string {
	t.Helper()
	fields := map[string]any{
		"ps": "vmess-test", "add": "vmess.example.com", "port": 443,
		"id": "4ce58870-27d3-489b-87a0-3109db4fb919", "aid": 0, "scy": "auto", "net": "ws",
		"host": "cdn.example.com", "path": "/vm", "tls": "tls", "sni": "vmess.example.com",
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal vmess fields: %v", err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

func marshal(t *testing.T, cfg singbox.Config) []byte {
	t.Helper()
	got, err := singbox.MarshalIndented(cfg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return got
}

func TestGenerateProxyConfig_Golden(t *testing.T) {
	// physIface == "" is the proxy-only steady state (no bind; rides Cisco).
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy.golden.json", marshal(t, cfg))
}

func TestGenerateForwarderConfig_Golden(t *testing.T) {
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.DefaultPorts())
	if err != nil {
		t.Fatalf("GenerateForwarderConfigSet: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/forwarder.golden.json", marshal(t, cfg))
}

func TestGenerateProxyConfig_ClashAPI_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, realLink)}, singbox.ProxyOpts{
		ClashAPI: &singbox.ClashAPI{ExternalController: "127.0.0.1:9090", Secret: "test-secret"},
	})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_clashapi.golden.json", marshal(t, cfg))
}

func TestGenerateProxyConfig_Multi_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, mustProfiles(t, realLink, secondLink), singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts multi: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_multi.golden.json", marshal(t, cfg))
}

func TestGenerateForwarder_Multi_Golden(t *testing.T) {
	cfg, err := singbox.GenerateForwarderConfigSet(testReg, mustProfiles(t, realLink, secondLink), singbox.DefaultPorts())
	if err != nil {
		t.Fatalf("GenerateForwarderConfigSet multi: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/forwarder_multi.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_VMess_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, vmessLink(t))}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_vmess.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_Trojan_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, trojanLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_trojan.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_Shadowsocks_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, shadowsocksLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_shadowsocks.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_Hysteria_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, hysteriaLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_hysteria.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_Hysteria2_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, hysteria2Link)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_hysteria2.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_TUIC_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, tuicLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_tuic.golden.json", marshal(t, cfg))
}

func TestGenerateProxy_AnyTLS_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, anytlsLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_anytls.golden.json", marshal(t, cfg))
}

// TestGenerateProxy_MixedProtocolMulti_Golden is the mixed multi-protocol
// urltest set: vless + hysteria2 + shadowsocks all as group members, each
// keeping its own outbound type and tag.
func TestGenerateProxy_MixedProtocolMulti_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg,
		mustProfiles(t, realLink, hysteria2Link, shadowsocksLink), singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_mixed_protocols.golden.json", marshal(t, cfg))
}

func TestGenerateProxyConfig_XHTTP_Reality_Golden(t *testing.T) {
	cfg, err := singbox.GenerateProxyConfigOpts(testReg, []protocol.Profile{mustParse(t, xhttpRealityLink)}, singbox.ProxyOpts{})
	if err != nil {
		t.Fatalf("GenerateProxyConfigOpts: %v", err)
	}
	testutil.AssertGoldenJSON(t, "../../singbox/testdata/proxy_xhttp_reality.golden.json", marshal(t, cfg))
}
