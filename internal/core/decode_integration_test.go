//go:build integration && singbox

// Integration test: verifies the generated configs are accepted by the REAL
// sing-box v1.12 option schema (catches field-name/type mismatches the golden
// unit tests can't). Run with: go get github.com/sagernet/sing-box@v1.12.x &&
// go test -tags "integration singbox" ./internal/core/
package core

import (
	"context"
	"testing"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/json"

	"singctl/internal/singbox"
	"singctl/internal/vless"
)

func decodeOptions(t *testing.T, data []byte) {
	t.Helper()
	ctx := box.Context(context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
		include.DNSTransportRegistry(),
		include.ServiceRegistry(),
	)
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		t.Fatalf("sing-box rejected generated config:\n%s\nerror: %v", data, err)
	}
	// box.New validates cross-references (outbound/DNS-detour tags, transports)
	// and builds the instance — this is what actually fails at "proxy won't
	// start" if the config decodes but can't be constructed. We build but never
	// Start() it (Start would open ports/dial).
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("sing-box failed to construct instance (would fail to start):\n%s\nerror: %v", data, err)
	}
	_ = inst.Close()
}

// startInstance builds AND Start()s the instance, then closes it. Start() is
// where sing-box validates/initializes DNS transports — e.g. it rejects a DoH
// server whose detour is an empty direct outbound ("detour to an empty direct
// outbound makes no sense"). box.New alone does NOT catch that. The caller must
// generate the config on high, unused ports so Start()'s inbound binds don't
// clash with a running proxy. No TUN → no root needed.
func startInstance(t *testing.T, data []byte) {
	t.Helper()
	ctx := box.Context(context.Background(),
		include.InboundRegistry(),
		include.OutboundRegistry(),
		include.EndpointRegistry(),
		include.DNSTransportRegistry(),
		include.ServiceRegistry(),
	)
	opts, err := json.UnmarshalExtendedContext[option.Options](ctx, data)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	inst, err := box.New(box.Options{Context: ctx, Options: opts})
	if err != nil {
		t.Fatalf("box.New: %v\n%s", err, data)
	}
	if err := inst.Start(); err != nil {
		_ = inst.Close()
		t.Fatalf("sing-box failed to START (this is the real 'proxy won't start'):\n%s\nerror: %v", data, err)
	}
	_ = inst.Close()
}

func TestDecodeOptions_GeneratedConfigsAreValidSingbox(t *testing.T) {
	p, err := vless.ParseLink("vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#t")
	if err != nil {
		t.Fatal(err)
	}

	proxyVPN, err := singbox.GenerateProxyConfig(p, "en0")
	if err != nil {
		t.Fatal(err)
	}
	pj, _ := singbox.MarshalIndented(proxyVPN)
	decodeOptions(t, pj)

	proxyOnly, _ := singbox.GenerateProxyConfig(p, "")
	pj2, _ := singbox.MarshalIndented(proxyOnly)
	decodeOptions(t, pj2)

	fwd, err := singbox.GenerateForwarderConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	fj, _ := singbox.MarshalIndented(fwd)
	decodeOptions(t, fj)
}

// TestDecodeOptions_BootstrapDNS validates the domain-server / multi-server
// configs that carry the boot-dns server + DNS rule (the fix for corp-DNS /
// urltest-loop resolution). Golden unit tests only check JSON shape; this
// confirms sing-box actually accepts the DNS rule + boot-dns server.
func TestDecodeOptions_BootstrapDNS(t *testing.T) {
	const a = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@usa.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=0cf78906&sni=microsoft.com&fp=chrome#usa"
	const b = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@auto.cloudpath.live:443?type=tcp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=085ba77f&sni=microsoft.com&fp=chrome#auto"

	// High, unused ports so Start()'s inbound binds don't clash with a running
	// proxy on 1080/2080.
	hiPorts := singbox.Ports{Socks: 21080, HTTP: 21081}

	multi, err := vless.ParseLinks([]string{a, b})
	if err != nil {
		t.Fatal(err)
	}
	mc, err := singbox.GenerateProxyConfigOpts(multi, singbox.ProxyOpts{Ports: hiPorts})
	if err != nil {
		t.Fatal(err)
	}
	mj, _ := singbox.MarshalIndented(mc)
	startInstance(t, mj) // must actually START (boot-dns detour bug shows here)

	single, err := vless.ParseLinks([]string{a})
	if err != nil {
		t.Fatal(err)
	}
	sc, err := singbox.GenerateProxyConfigOpts(single, singbox.ProxyOpts{Ports: hiPorts})
	if err != nil {
		t.Fatal(err)
	}
	sj, _ := singbox.MarshalIndented(sc)
	startInstance(t, sj)
}
