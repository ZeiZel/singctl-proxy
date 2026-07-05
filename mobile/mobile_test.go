package mobile

import (
	"strings"
	"testing"
)

const (
	linkA = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server"
	linkB = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@45.10.20.30:8443?security=tls&sni=fallback.example#fallback"
)

func TestBuildConfig_SingleKey(t *testing.T) {
	configJSON := `{
		"mode": "vpn",
		"keys": ["` + linkA + `"],
		"settings": {
			"SocksPort": 0,
			"ClashEnabled": false,
			"ClashAddr": "",
			"URLTestURL": "https://www.gstatic.com/generate_204",
			"URLTestInterval": "3m",
			"URLTestTolerance": 50,
			"SaveProfile": false
		}
	}`

	out, err := BuildConfig(configJSON)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if !strings.Contains(out, `"type": "tun"`) {
		t.Error("expected a tun inbound in the generated config")
	}
	if !strings.Contains(out, `"type": "vless"`) {
		t.Error("expected a vless outbound in the generated config")
	}
	if strings.Contains(out, `"type": "urltest"`) {
		t.Error("single key must not produce a urltest group")
	}
	if strings.Contains(out, "bind_interface") {
		t.Error("sandboxed tunnel config must never contain bind_interface")
	}
}

func TestBuildConfig_MultiKey(t *testing.T) {
	configJSON := `{
		"mode": "vpn",
		"keys": ["` + linkA + `", "` + linkB + `"],
		"settings": {
			"URLTestURL": "https://www.gstatic.com/generate_204",
			"URLTestInterval": "3m",
			"URLTestTolerance": 50
		}
	}`

	out, err := BuildConfig(configJSON)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if !strings.Contains(out, `"type": "urltest"`) {
		t.Error("expected a urltest failover group for 2 keys")
	}
	// The bootstrap-DNS fix must still be present: the probe host resolves via
	// a second, detour-less DoH server so the group can bootstrap.
	if !strings.Contains(out, "www.gstatic.com") {
		t.Error("expected the urltest probe host in the DNS bootstrap rule")
	}
	if strings.Contains(out, "bind_interface") {
		t.Error("sandboxed tunnel config must never contain bind_interface")
	}
}

func TestBuildConfig_BadKey(t *testing.T) {
	configJSON := `{"mode":"vpn","keys":["not-a-vless-link"],"settings":{}}`
	if _, err := BuildConfig(configJSON); err == nil {
		t.Error("expected an error for a malformed VLESS key")
	}
}

func TestBuildConfig_EmptyKeys(t *testing.T) {
	configJSON := `{"mode":"vpn","keys":[],"settings":{}}`
	_, err := BuildConfig(configJSON)
	if err == nil {
		t.Fatal("expected an error for no keys configured")
	}
	if !strings.Contains(err.Error(), "no keys configured") {
		t.Errorf("error = %q, want it to mention 'no keys configured'", err.Error())
	}
}

func TestBuildConfig_MalformedJSON(t *testing.T) {
	if _, err := BuildConfig("{not json"); err == nil {
		t.Error("expected an error for malformed configJSON")
	}
}

func TestVersion(t *testing.T) {
	if v := Version(); v == "" {
		t.Error("Version() must not be empty")
	}
}
