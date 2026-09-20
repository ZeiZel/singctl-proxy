package all

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"singctl/internal/protocol"
)

// vmessConformanceLink builds a minimal valid vmess:// link the same way a
// real client would, so TestConformance below has a realistic sample input
// without depending on vmess's own test-only helpers (unexported, in another
// package's _test.go).
func vmessConformanceLink(t *testing.T) string {
	t.Helper()
	fields := map[string]any{
		"v": "2", "ps": "conformance-node", "add": "example.com", "port": 443,
		"id": "4ce58870-27d3-489b-87a0-3109db4fb919", "aid": 0, "scy": "auto", "net": "tcp",
	}
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal vmess fields: %v", err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}

// wireguardConformanceConfig is a realistic two-peer WireGuard config,
// mirroring internal/protocol/wireguard's own test fixture.
const wireguardConformanceConfig = `[Interface]
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

// TestConformance exercises CheckRegistry against the real, default registry:
// one realistic ConformanceCase per module (mirroring each module's own
// TestConformance), plus the registry-level invariants (every registered
// module has a case; unknown input is refused listing what IS accepted).
//
// CheckRegistry fails if any registered module lacks a case, which is
// exactly the point: a tenth protocol added to Registry() above must get its
// own coverage here or this test starts failing.
func TestConformance(t *testing.T) {
	const vlessUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"
	const tuicUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"

	protocol.CheckRegistry(t, Registry(), []protocol.ConformanceCase{
		{
			Module: "vless",
			Input: "vless://" + vlessUUID + "@193.188.22.147:443?type=grpc&security=reality&" +
				"pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server",
			WantLabel: "test-server",
			Rejects: []string{
				"vless://1.2.3.4:443",
				"vless://not-a-uuid@1.2.3.4:443",
				"trojan://" + vlessUUID + "@1.2.3.4:443",
			},
		},
		{
			Module:    "vmess",
			Input:     vmessConformanceLink(t),
			WantLabel: "conformance-node",
			Rejects: []string{
				"vmess://not-valid-base64!!!",
				"vless://" + vlessUUID + "@1.2.3.4:443",
				"vmess://" + base64.StdEncoding.EncodeToString([]byte("not json")),
			},
		},
		{
			Module:    "trojan",
			Input:     "trojan://s3cr3t-P%40ss@1.2.3.4:443#conformance",
			WantLabel: "conformance",
			Rejects: []string{
				"trojan://1.2.3.4:443",
				"trojan://pw@1.2.3.4:443?security=none",
				"vless://s3cr3t-P%40ss@1.2.3.4:443",
			},
		},
		{
			Module:    "shadowsocks",
			Input:     "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#conformance",
			WantLabel: "conformance",
			Rejects: []string{
				"ss://not-valid-base64-!!!!",
				"ss://OnBhc3N3b3Jk@example.com:8388",
				"vless://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388",
			},
		},
		{
			Module: "hysteria",
			Input: "hysteria://1.2.3.4:443?auth=s3cr3t&upmbps=100&downmbps=50&obfs=xplus&" +
				"peer=example.com&alpn=h3&insecure=1#my-server",
			WantLabel: "my-server",
			Rejects: []string{
				"hysteria://1.2.3.4:443?upmbps=10&downmbps=10",
				"hysteria://1.2.3.4:443?auth=pw",
				"not-a-link-at-all",
			},
		},
		{
			Module: "hysteria2",
			Input: "hysteria2://s3cr3t@1.2.3.4:443?sni=example.com&alpn=h3&insecure=1&" +
				"obfs=salamander&obfs-password=obfspw&up=100&down=50#my-server",
			WantLabel: "my-server",
			Rejects: []string{
				"hysteria2://1.2.3.4:443",
				"hysteria2://pw@:443",
				"not-a-link-at-all",
			},
		},
		{
			Module: "tuic",
			Input: "tuic://" + tuicUUID + ":s3cr3t@1.2.3.4:443?congestion_control=bbr&" +
				"udp_relay_mode=quic&zero_rtt_handshake=1&sni=example.com&alpn=h3&allow_insecure=1#my-server",
			WantLabel: "my-server",
			Rejects: []string{
				"tuic://:pw@1.2.3.4:443",
				"tuic://not-a-uuid:pw@1.2.3.4:443",
				"not-a-link-at-all",
			},
		},
		{
			Module:    "anytls",
			Input:     "anytls://s3cr3t@example.com:8443?sni=example.com&alpn=h2,http%2F1.1&fp=chrome&insecure=1#anytls-server",
			WantLabel: "anytls-server",
			Rejects: []string{
				"anytls://example.com:443",
				"anytls://pw@:443",
				"not-a-link-at-all",
			},
		},
		{
			Module:    "wireguard",
			Input:     wireguardConformanceConfig,
			WantLabel: "vpn.example.com",
			Rejects: []string{
				"vless://" + vlessUUID + "@1.2.3.4:443",
				"https://panel.example.com/sub/token/",
				"[Interface]\nAddress = 10.0.0.2/32\n", // missing PrivateKey
			},
		},
	})
}

// TestRegistry_PanicsNever pins that the real module set wires cleanly (no
// duplicate scheme/name) — Registry() must never panic in practice.
func TestRegistry_PanicsNever(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Registry() panicked: %v", r)
		}
	}()
	r := Registry()
	if len(r.Modules()) != 9 {
		t.Fatalf("Registry() has %d modules, want 9", len(r.Modules()))
	}
}
