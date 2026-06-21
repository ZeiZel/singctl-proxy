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
