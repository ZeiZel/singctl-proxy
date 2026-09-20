//go:build integration && singbox

// WireGuard is the first protocol singctl renders as a sing-box ENDPOINT rather
// than an outbound, and it is gated behind the with_wireguard build tag. Both
// facts fail silently if got wrong: a missing tag lets the config decode and
// then refuses to construct, and a misplaced node is simply ignored. This test
// puts the module's real output through the real core.
package core

import (
	"encoding/json"
	"strings"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/protocol/all"
	"singctl/internal/protocol/wireguard"
	"singctl/internal/singbox"
)

const wgSampleConfig = `
[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.66.66.2/32, fd42:42:42::2/128
MTU = 1420
DNS = 1.1.1.1

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = FpCyhws9cxwWoV4xELtfJvjJN+zQVRPISllRWgeopVE=
Endpoint = 198.51.100.7:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
`

func TestDecodeOptions_WireGuardEndpoint(t *testing.T) {
	mod := wireguard.New()

	if !mod.Sniff(wgSampleConfig) {
		t.Fatal("the module does not recognise its own config format")
	}
	p, err := mod.Parse(wgSampleConfig)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Protocol != "wireguard" {
		t.Fatalf("protocol = %q", p.Protocol)
	}
	if mod.Descriptor().Kind != protocol.KindEndpoint {
		t.Fatalf("kind = %v, want endpoint", mod.Descriptor().Kind)
	}

	node, err := mod.RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	nodeJSON, err := json.Marshal(node)
	if err != nil {
		t.Fatal(err)
	}

	// An endpoint lives in its own top-level array, and its tag is usable
	// wherever an outbound tag is — which is what lets it join the failover
	// group. Both halves are asserted here: route.final points at the endpoint.
	cfg := []byte(`{
  "log": {"level": "warn"},
  "inbounds": [{"type":"socks","tag":"in","listen":"127.0.0.1","listen_port":21090}],
  "endpoints": [` + string(nodeJSON) + `],
  "outbounds": [{"type":"direct","tag":"direct"}],
  "route": {"final": "proxy"}
}`)

	decodeOptions(t, cfg)
}

// TestGenerateProxyConfig_WireGuardEndpoint closes the last gap: the module's
// node reaching a REAL generated config through the registry-driven assembly.
// The isolated render test above proves the node is well formed; this proves
// the assembly puts it in the "endpoints" array rather than "outbounds", and
// that route.final resolves to it — an endpoint placed in the wrong array
// decodes fine and is simply never used.
func TestGenerateProxyConfig_WireGuardEndpoint(t *testing.T) {
	reg := all.Registry()

	profiles, err := reg.ParseAll([]string{wgSampleConfig})
	if err != nil {
		t.Fatalf("registry refused a WireGuard config: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("ParseAll produced %d profiles, want 1 — a multi-line config must not be split", len(profiles))
	}
	if profiles[0].Protocol != "wireguard" {
		t.Fatalf("routed to %q", profiles[0].Protocol)
	}

	cfg, err := singbox.GenerateProxyConfigOpts(reg, profiles, singbox.ProxyOpts{
		Ports: singbox.Ports{Socks: freePort(t), HTTP: freePort(t)},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("generated %d endpoints, want 1 — the endpoint went into the wrong array", len(cfg.Endpoints))
	}
	data, err := singbox.MarshalIndented(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"endpoints"`) {
		t.Fatalf("no endpoints array in the generated config:\n%s", data)
	}

	// Decode AND start: starting is what proves the endpoint tag actually
	// resolves for route.final and the DNS detour.
	startInstance(t, data)
}
