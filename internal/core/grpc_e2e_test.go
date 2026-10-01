//go:build integration && singbox

package core

import (
	"encoding/json"
	"fmt"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// TestVLESSGRPCLoopback exercises bytes on the wire through the native
// sing-box gRPC transport. The same sing-box instance hosts a VLESS inbound
// and a gRPC VLESS outbound; a SOCKS request must cross both sides before it
// reaches the local HTTP origin. This catches transport option names and
// runtime wiring errors that config decode tests cannot see.
func TestVLESSGRPCLoopback(t *testing.T) {
	target := startTargetServer(t)
	socksPort := freePort(t)
	vlessPort := freePort(t)

	const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"
	const serviceName = "singctl-grpc-loopback"
	rawLink := fmt.Sprintf("vless://%s@127.0.0.1:%d?type=grpc&serviceName=%s#loopback", uuid, vlessPort, serviceName)
	profile, err := integrationReg.Parse(rawLink)
	if err != nil {
		t.Fatalf("parse synthetic gRPC link: %v", err)
	}
	module, ok := integrationReg.Module(protocol.Name("vless"))
	if !ok {
		t.Fatal("vless module is not registered")
	}
	renderer, ok := module.(singbox.Renderer)
	if !ok {
		t.Fatalf("vless module %T does not render sing-box nodes", module)
	}
	node, err := renderer.RenderNode(profile, singbox.RenderOpts{Tag: "vless-grpc"})
	if err != nil {
		t.Fatalf("render synthetic gRPC link: %v", err)
	}
	outboundJSON, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal rendered gRPC outbound: %v", err)
	}
	config := fmt.Appendf(nil, `{
  "log": {"level": "warn"},
  "inbounds": [
    {"type":"socks","tag":"socks-in","listen":"127.0.0.1","listen_port":%d},
    {"type":"vless","tag":"vless-server","listen":"127.0.0.1","listen_port":%d,
     "users":[{"name":"loopback","uuid":%q}],
     "transport":{"type":"grpc","service_name":%q}}
  ],
  "outbounds": [
    %s,
    {"type":"direct","tag":"direct"}
  ],
  "route": {"rules":[
    {"inbound":["vless-server"],"outbound":"direct"},
    {"inbound":["socks-in"],"outbound":"vless-grpc"}
  ],"final":"direct"}
}`, socksPort, vlessPort, uuid, serviceName, outboundJSON)

	startInstanceAlive(t, config)
	if body := getThroughSocks(t, socksPort, "http://"+target+"/grpc"); body != "singctl-xhttp-ok" {
		t.Fatalf("unexpected body through VLESS gRPC loopback: %q", body)
	}
}
