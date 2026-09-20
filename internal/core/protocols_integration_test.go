//go:build integration && singbox

// Every protocol singctl can parse must produce a config the REAL sing-box
// accepts. Golden tests only pin the JSON we intended to write; this pins that
// sing-box agrees — it is what catches a mistyped option field name, which is
// otherwise invisible until a user's key silently fails to start.
package core

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"singctl/internal/protocol/all"
	"singctl/internal/singbox"
)

// integrationReg is the real, default registry — the same one production
// code builds from all.Registry().
var integrationReg = all.Registry()

const testUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"

// ss2022Key is base64 of EXACTLY 16 bytes: the 2022-blake3-aes-128-gcm cipher
// derives its key straight from the password and rejects any other length.
const ss2022Key = "MDEyMzQ1Njc4OWFiY2RlZg=="

// vmessLink builds a vmess:// share link (base64 of a JSON object) so the test
// reads as the config it represents rather than as an opaque blob.
func vmessLink(t *testing.T, extra map[string]any) string {
	t.Helper()
	obj := map[string]any{
		"v": "2", "ps": "vmess-test", "add": "example.com", "port": "443",
		"id": testUUID, "aid": 0, "scy": "auto", "net": "tcp", "tls": "tls",
		"sni": "example.com",
	}
	for k, v := range extra {
		obj[k] = v
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(raw)
}

// ssLink builds a SIP002 shadowsocks link.
func ssLink(method, password, hostPort, name string) string {
	userinfo := base64.RawURLEncoding.EncodeToString([]byte(method + ":" + password))
	return fmt.Sprintf("ss://%s@%s#%s", userinfo, hostPort, name)
}

func TestDecodeOptions_AllProtocols(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"vless-reality-tcp", "vless://" + testUUID + "@example.com:443?security=reality&" +
			"pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#vless"},
		{"vless-ws-tls", "vless://" + testUUID + "@example.com:443?type=ws&security=tls&" +
			"sni=example.com&host=cdn.example.com&path=%2Fws#vless-ws"},
		{"vmess-tcp-tls", vmessLink(t, nil)},
		{"vmess-ws", vmessLink(t, map[string]any{"net": "ws", "path": "/ws", "host": "cdn.example.com"})},
		{"vmess-grpc", vmessLink(t, map[string]any{"net": "grpc", "path": "grpcsvc"})},
		{"trojan-tls", "trojan://s3cr3t@example.com:443?sni=example.com&alpn=h2#trojan"},
		{"trojan-ws", "trojan://s3cr3t@example.com:443?sni=example.com&type=ws&path=%2Ftr#trojan-ws"},
		{"shadowsocks-gcm", ssLink("aes-256-gcm", "correct-horse", "example.com:8388", "ss")},
		{"shadowsocks-2022", ssLink("2022-blake3-aes-128-gcm", ss2022Key, "example.com:8388", "ss2022")},
		{"hysteria2", "hysteria2://s3cr3t@example.com:443?sni=example.com&obfs=salamander&" +
			"obfs-password=obfspw&up=100&down=200#hy2"},
		{"hysteria2-alias", "hy2://s3cr3t@example.com:443?sni=example.com&up=50&down=100#hy2-alias"},
		{"hysteria-v1", "hysteria://example.com:443?auth=s3cr3t&upmbps=100&downmbps=200&peer=example.com#hy1"},
		{"tuic", "tuic://" + testUUID + ":s3cr3t@example.com:443?congestion_control=bbr&" +
			"udp_relay_mode=native&sni=example.com#tuic"},
		{"anytls", "anytls://s3cr3t@example.com:443?sni=example.com#anytls"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profiles, err := integrationReg.ParseAll([]string{tc.raw})
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			proxy, err := singbox.GenerateProxyConfigOpts(integrationReg, profiles, singbox.ProxyOpts{})
			if err != nil {
				t.Fatalf("generate proxy config: %v", err)
			}
			pj, err := singbox.MarshalIndented(proxy)
			if err != nil {
				t.Fatal(err)
			}
			decodeOptions(t, pj)

			// The App Store SKU's single-instance tunnel config must accept the
			// same protocols (only the XHTTP transport is excluded there).
			tun, err := singbox.GenerateTunnelConfigSet(integrationReg, profiles, singbox.TunnelOpts{})
			if err != nil {
				t.Fatalf("generate tunnel config: %v", err)
			}
			tj, err := singbox.MarshalIndented(tun)
			if err != nil {
				t.Fatal(err)
			}
			decodeOptions(t, tj)
		})
	}
}

// TestDecodeOptions_MixedProtocolFailover is the multi-key case a real user
// hits: several servers of DIFFERENT protocols in one urltest group. It must
// not merely decode — it must start, which is where sing-box validates the
// group's members and the DNS detours that reference them.
func TestDecodeOptions_MixedProtocolFailover(t *testing.T) {
	profiles, err := integrationReg.ParseAll([]string{
		"vless://" + testUUID + "@a.example.com:443?security=reality&" +
			"pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#vless",
		"hysteria2://s3cr3t@b.example.com:443?sni=b.example.com&up=100&down=200#hy2",
		ssLink("aes-256-gcm", "correct-horse", "c.example.com:8388", "ss"),
		"tuic://" + testUUID + ":s3cr3t@d.example.com:443?sni=d.example.com#tuic",
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	cfg, err := singbox.GenerateProxyConfigOpts(integrationReg, profiles, singbox.ProxyOpts{
		Ports: singbox.Ports{Socks: freePort(t), HTTP: freePort(t)},
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	data, err := singbox.MarshalIndented(cfg)
	if err != nil {
		t.Fatal(err)
	}
	startInstance(t, data)
}
