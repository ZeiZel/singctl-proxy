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
	if _, err := json.UnmarshalExtendedContext[option.Options](ctx, data); err != nil {
		t.Fatalf("sing-box rejected generated config:\n%s\nerror: %v", data, err)
	}
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
