package wireguard

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

const fullConfig = `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.66.66.2/32, fd66:66::2/128
MTU = 1420
ListenPort = 51820
DNS = 1.1.1.1, 1.0.0.1

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
PresharedKey = FpCyhws9cxwWoV4xELtfJvjJN+zJ4kFyR9d9GkkzWlM=
Endpoint = vpn.example.com:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25

[Peer]
PublicKey = 7voWWqmxb2vJvzOAgj0aFvOtGZ0nkTUUfHPxdVJnKlk=
Endpoint = 198.51.100.7:51821
AllowedIPs = 192.168.1.0/24
`

func TestParse_FullConfig(t *testing.T) {
	p, err := New().Parse(fullConfig)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if p.Protocol != "wireguard" {
		t.Fatalf("Protocol = %q", p.Protocol)
	}
	if p.Label != "vpn.example.com" {
		t.Fatalf("Label = %q, want the first peer's endpoint host", p.Label)
	}
	if p.Raw != fullConfig {
		t.Fatalf("Raw not preserved")
	}

	params, ok := p.Params.(Params)
	if !ok {
		t.Fatalf("Params has type %T, want Params", p.Params)
	}
	want := Params{
		PrivateKey: "uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=",
		Address:    []string{"10.66.66.2/32", "fd66:66::2/128"},
		MTU:        1420,
		ListenPort: 51820,
		DNS:        []string{"1.1.1.1", "1.0.0.1"},
		Peers: []Peer{
			{
				PublicKey:           "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
				PresharedKey:        "FpCyhws9cxwWoV4xELtfJvjJN+zJ4kFyR9d9GkkzWlM=",
				EndpointHost:        "vpn.example.com",
				EndpointPort:        51820,
				AllowedIPs:          []string{"0.0.0.0/0", "::/0"},
				PersistentKeepalive: 25,
			},
			{
				PublicKey:    "7voWWqmxb2vJvzOAgj0aFvOtGZ0nkTUUfHPxdVJnKlk=",
				EndpointHost: "198.51.100.7",
				EndpointPort: 51821,
				AllowedIPs:   []string{"192.168.1.0/24"},
			},
		},
	}
	if !reflect.DeepEqual(params, want) {
		t.Fatalf("Params =\n%+v\nwant\n%+v", params, want)
	}
}

func TestParse_WhitespaceCommentsAndCaseInsensitiveKeys(t *testing.T) {
	const cfg = `
; a leading comment
   [Interface]
privatekey=uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
   ADDRESS   =   10.0.0.2/32
# another comment

  [PEER]
  PUBLICKEY = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
  endpoint = vpn.example.com:51820
`
	p, err := New().Parse(cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	params := p.Params.(Params)
	if params.PrivateKey != "uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=" {
		t.Errorf("PrivateKey = %q", params.PrivateKey)
	}
	if len(params.Address) != 1 || params.Address[0] != "10.0.0.2/32" {
		t.Errorf("Address = %v", params.Address)
	}
	if len(params.Peers) != 1 || params.Peers[0].PublicKey != "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=" {
		t.Errorf("Peers = %+v", params.Peers)
	}
}

func TestParse_IPv6EndpointAndAddress(t *testing.T) {
	const cfg = `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = fd66:66::2/128

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
Endpoint = [2001:db8::1]:51820
`
	p, err := New().Parse(cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	params := p.Params.(Params)
	if len(params.Address) != 1 || params.Address[0] != "fd66:66::2/128" {
		t.Fatalf("Address = %v", params.Address)
	}
	peer := params.Peers[0]
	if peer.EndpointHost != "2001:db8::1" {
		t.Errorf("EndpointHost = %q, want the bracket-stripped IPv6 literal", peer.EndpointHost)
	}
	if peer.EndpointPort != 51820 {
		t.Errorf("EndpointPort = %d", peer.EndpointPort)
	}
}

func TestParse_RejectsMissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		cfg     string
		wantErr string
	}{
		{
			name: "missing PrivateKey",
			cfg: `[Interface]
Address = 10.0.0.2/32

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
`,
			wantErr: "[Interface]: PrivateKey",
		},
		{
			name: "missing Address",
			cfg: `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
`,
			wantErr: "[Interface]: Address",
		},
		{
			name: "missing PublicKey",
			cfg: `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.0.0.2/32

[Peer]
Endpoint = vpn.example.com:51820
`,
			wantErr: "[Peer] #1: PublicKey",
		},
		{
			name:    "no Interface section at all",
			cfg:     "[Peer]\nPublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=\n",
			wantErr: "no [Interface] section",
		},
		{
			name: "no Peer section at all",
			cfg: `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.0.0.2/32
`,
			wantErr: "no [Peer] section",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New().Parse(tc.cfg)
			if err == nil {
				t.Fatalf("Parse succeeded, want an error mentioning %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestSniff(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"real config", fullConfig, true},
		{"config with leading whitespace before the header", "   [Interface]\nPrivateKey = x\n", true},
		{"config with mixed-case header", "[interface]\nPrivateKey = x\n", true},
		{"vless share link", "vless://uuid@host:443?type=tcp#label", false},
		{"subscription URL", "https://panel.example.com/sub/token/", false},
		{"arbitrary text", "just some notes about a server, nothing structured", false},
		{"empty", "", false},
	}
	m := New()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.Sniff(tc.in); got != tc.want {
				t.Errorf("Sniff(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRenderNode_PinnedJSON(t *testing.T) {
	const cfg = `[Interface]
PrivateKey = uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=
Address = 10.66.66.2/32
MTU = 1420

[Peer]
PublicKey = xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=
Endpoint = vpn.example.com:51820
`
	p, err := New().Parse(cfg)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	node, err := New().RenderNode(p, singbox.RenderOpts{
		Tag:            "wg-primary",
		BindInterface:  "en0",
		ConnectTimeout: "10s",
	})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}

	got, err := json.MarshalIndent(node, "", "  ")
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	// Pinned: any change to this JSON is a change to what sing-box actually
	// receives, and must be a deliberate one. Note AllowedIPs defaulting to a
	// full tunnel — the sample config's [Peer] carries none.
	want := `{
  "type": "wireguard",
  "tag": "wg-primary",
  "mtu": 1420,
  "address": [
    "10.66.66.2/32"
  ],
  "private_key": "uIlt6l0MZjOFCUyGSFHK1uZ8gr0od7CIvS9nXqOSlmg=",
  "peers": [
    {
      "address": "vpn.example.com",
      "port": 51820,
      "public_key": "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg=",
      "allowed_ips": [
        "0.0.0.0/0",
        "::/0"
      ]
    }
  ],
  "bind_interface": "en0",
  "connect_timeout": "10s"
}`
	if string(got) != want {
		t.Fatalf("RenderNode JSON =\n%s\nwant\n%s", got, want)
	}
}

func TestRenderNode_AllowedIPsPreservedWhenConfigSetsThem(t *testing.T) {
	p, err := New().Parse(fullConfig)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "wg"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	ep, ok := node.(*singbox.WireGuardEndpoint)
	if !ok {
		t.Fatalf("RenderNode returned %T, want *singbox.WireGuardEndpoint", node)
	}
	if len(ep.Peers) != 2 {
		t.Fatalf("got %d peers, want 2", len(ep.Peers))
	}
	if !reflect.DeepEqual(ep.Peers[0].AllowedIPs, []string{"0.0.0.0/0", "::/0"}) {
		t.Errorf("peer 0 AllowedIPs = %v, want the config's own explicit value", ep.Peers[0].AllowedIPs)
	}
	if !reflect.DeepEqual(ep.Peers[1].AllowedIPs, []string{"192.168.1.0/24"}) {
		t.Errorf("peer 1 AllowedIPs = %v, want the config's own explicit value", ep.Peers[1].AllowedIPs)
	}
}

func TestRenderNode_WrongParamsType(t *testing.T) {
	_, err := New().RenderNode(protocol.Profile{Protocol: "wireguard", Params: "not a Params"}, singbox.RenderOpts{})
	if err == nil {
		t.Fatal("RenderNode succeeded on a mistyped Params, want an error")
	}
}

func TestConformance(t *testing.T) {
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    "wireguard",
		Input:     fullConfig,
		WantLabel: "vpn.example.com",
		Rejects: []string{
			"vless://uuid@host:443?type=tcp#label",
			"https://panel.example.com/sub/token/",
			"[Interface]\nAddress = 10.0.0.2/32\n", // missing PrivateKey
		},
	})
}

func TestRegistry_Dispatch(t *testing.T) {
	r, err := protocol.NewRegistry(New())
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Parse(fullConfig)
	if err != nil {
		t.Fatalf("Parse via registry: %v", err)
	}
	if p.Protocol != "wireguard" {
		t.Fatalf("Protocol = %q", p.Protocol)
	}

	// A pasted share link or subscription URL must never be mistaken for a
	// WireGuard config.
	if _, err := r.Parse("vless://uuid@host:443?type=tcp#label"); err == nil {
		t.Fatal("registry accepted a vless link with no vless module registered, want ErrUnsupported")
	}
}
