package vless

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

// assertMatchesGoldenOutbound marshals node to JSON and compares it
// (semantically, ignoring key order) against outbounds[index] in the golden
// config file at goldenPath — the same golden files internal/singbox's
// pre-refactor generator produced, so this pins byte-for-byte-equivalent
// output across the move.
func assertMatchesGoldenOutbound(t *testing.T, node any, goldenPath string, index int) {
	t.Helper()
	gotJSON, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal node: %v", err)
	}
	var got any
	if err := json.Unmarshal(gotJSON, &got); err != nil {
		t.Fatalf("unmarshal node: %v", err)
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v", goldenPath, err)
	}
	var full map[string]any
	if err := json.Unmarshal(golden, &full); err != nil {
		t.Fatalf("unmarshal golden %s: %v", goldenPath, err)
	}
	outbounds, ok := full["outbounds"].([]any)
	if !ok || index >= len(outbounds) {
		t.Fatalf("golden %s has no outbounds[%d]", goldenPath, index)
	}
	want := outbounds[index]
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rendered node does not match golden %s outbounds[%d]\n got: %s\nwant: %#v", goldenPath, index, gotJSON, want)
	}
}

const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"

func mustParse(t *testing.T, link string) protocol.Profile {
	t.Helper()
	p, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse(%q): %v", link, err)
	}
	return p
}

const realLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server"

func TestParse_OK(t *testing.T) {
	tests := []struct {
		name string
		link string
		want Params
		lbl  string
	}{
		{
			name: "reality grpc (real link)",
			link: realLink,
			want: Params{
				UUID:     uuid,
				Host:     "193.188.22.147",
				Port:     443,
				Security: SecurityReality,
				TLS:      TLSParams{ServerName: "cursor.com", Fingerprint: "chrome"},
				Reality:  RealityParams{Enabled: true, PublicKey: "MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k", ShortID: "4d04"},
				Transport: TransportParams{
					Type: TransportGRPC,
				},
			},
			lbl: "test-server",
		},
		{
			name: "tls websocket with host header and path",
			link: "vless://" + uuid + "@example.com:8443?type=ws&security=tls&sni=example.com&host=cdn.example.com&path=%2Fwspath&alpn=h2,http%2F1.1#ws",
			want: Params{
				UUID:     uuid,
				Host:     "example.com",
				Port:     8443,
				Security: SecurityTLS,
				TLS:      TLSParams{ServerName: "example.com", ALPN: []string{"h2", "http/1.1"}},
				Transport: TransportParams{
					Type: TransportWS,
					Path: "/wspath",
					Host: []string{"cdn.example.com"},
				},
			},
			lbl: "ws",
		},
		{
			name: "plain tcp no security (defaults)",
			link: "vless://" + uuid + "@1.2.3.4:443",
			want: Params{
				UUID:      uuid,
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityNone,
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "ipv6 host stored without brackets",
			link: "vless://" + uuid + "@[2001:db8::1]:443?security=tls",
			want: Params{
				UUID:      uuid,
				Host:      "2001:db8::1",
				Port:      443,
				Security:  SecurityTLS,
				TLS:       TLSParams{},
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "percent-encoded name and flow",
			link: "vless://" + uuid + "@1.2.3.4:443?flow=xtls-rprx-vision#My%20Server%20RU",
			want: Params{
				UUID:      uuid,
				Host:      "1.2.3.4",
				Port:      443,
				Flow:      "xtls-rprx-vision",
				Security:  SecurityNone,
				Transport: TransportParams{Type: TransportTCP},
			},
			lbl: "My Server RU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().Parse(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Protocol != "vless" {
				t.Errorf("Protocol = %q, want vless", got.Protocol)
			}
			if got.Label != tt.lbl {
				t.Errorf("Label = %q, want %q", got.Label, tt.lbl)
			}
			if got.Raw != tt.link {
				t.Errorf("Raw = %q, want %q", got.Raw, tt.link)
			}
			gotParams, ok := got.Params.(Params)
			if !ok {
				t.Fatalf("Params is %T, want Params", got.Params)
			}
			if !reflect.DeepEqual(gotParams, tt.want) {
				t.Errorf("params mismatch\n got: %+v\nwant: %+v", gotParams, tt.want)
			}
		})
	}
}

func TestParse_Errors(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		wantErr error
	}{
		{"empty", "", ErrNotVLESS},
		{"missing uuid", "vless://1.2.3.4:443", ErrMissingUUID},
		{"invalid uuid", "vless://not-a-uuid@1.2.3.4:443", ErrInvalidUUID},
		{"missing port", "vless://" + uuid + "@1.2.3.4", ErrMissingPort},
		{"invalid port zero", "vless://" + uuid + "@1.2.3.4:0", ErrInvalidPort},
		{"reality without pbk", "vless://" + uuid + "@1.2.3.4:443?security=reality", ErrMissingRealityKey},
		{"unsupported transport", "vless://" + uuid + "@1.2.3.4:443?type=quicX", ErrUnsupportedTransport},
		{"unsupported security", "vless://" + uuid + "@1.2.3.4:443?security=weird", ErrUnsupportedSecurity},
		{"missing host", "vless://" + uuid + "@:443", ErrMissingHost},
		{"port overflow", "vless://" + uuid + "@1.2.3.4:70000", ErrInvalidPort},
		{"non-numeric port (rejected by url.Parse)", "vless://" + uuid + "@1.2.3.4:abc", ErrNotVLESS},
		{"unsupported encryption", "vless://" + uuid + "@1.2.3.4:443?encryption=mlkem768x25519plus", ErrUnsupportedEncryption},
		{"wrong scheme", "trojan://" + uuid + "@1.2.3.4:443", ErrNotVLESS},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New().Parse(tt.link)
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error mismatch: got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestParse_FieldDetails(t *testing.T) {
	t.Run("allowInsecure=1 sets Insecure", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=1")
		if !p.Params.(Params).TLS.Insecure {
			t.Error("want Insecure=true")
		}
	})
	t.Run("allowInsecure=true sets Insecure (case-insensitive)", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=TRUE")
		if !p.Params.(Params).TLS.Insecure {
			t.Error("want Insecure=true")
		}
	})
	t.Run("allowInsecure unset => false", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls")
		if p.Params.(Params).TLS.Insecure {
			t.Error("want Insecure=false")
		}
	})
	t.Run("lowercase servicename alias", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=grpc&security=reality&pbk=k&servicename=mysvc")
		if got := p.Params.(Params).Transport.ServiceName; got != "mysvc" {
			t.Errorf("ServiceName = %q, want mysvc", got)
		}
	})
	t.Run("empty alpn yields nil (not empty slice)", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&alpn=")
		if got := p.Params.(Params).TLS.ALPN; got != nil {
			t.Errorf("ALPN = %#v, want nil", got)
		}
	})
	t.Run("multi host is split and trimmed", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=ws&security=tls&host=a.com,%20b.com")
		got := p.Params.(Params).Transport.Host
		if want := []string{"a.com", "b.com"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Host = %#v, want %#v", got, want)
		}
	})
	t.Run("flow is trimmed", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?flow=%20xtls-rprx-vision%20")
		if got := p.Params.(Params).Flow; got != "xtls-rprx-vision" {
			t.Errorf("Flow = %q, want xtls-rprx-vision", got)
		}
	})
}

// TestParse_XHTTP covers the xhttp transport: path/host/mode/extra parsing,
// the legacy splithttp alias, and that extra is stored compacted.
func TestParse_XHTTP(t *testing.T) {
	t.Run("path host mode extra", func(t *testing.T) {
		link := "vless://" + uuid + "@1.2.3.4:443?type=xhttp&security=tls&host=cdn.example.com&path=%2Fxh&mode=stream-one&extra=%7B%22scMaxBufferedPosts%22%3A30%7D"
		p := mustParse(t, link).Params.(Params)
		if p.Transport.Type != TransportXHTTP {
			t.Fatalf("Transport.Type = %q, want xhttp", p.Transport.Type)
		}
		if p.Transport.Path != "/xh" {
			t.Errorf("Path = %q, want /xh", p.Transport.Path)
		}
		if len(p.Transport.Host) != 1 || p.Transport.Host[0] != "cdn.example.com" {
			t.Errorf("Host = %#v, want [cdn.example.com]", p.Transport.Host)
		}
		if p.Transport.Mode != "stream-one" {
			t.Errorf("Mode = %q, want stream-one", p.Transport.Mode)
		}
		if want := `{"scMaxBufferedPosts":30}`; p.Transport.Extra != want {
			t.Errorf("Extra = %q, want %q", p.Transport.Extra, want)
		}
	})

	t.Run("extra is compacted", func(t *testing.T) {
		link := "vless://" + uuid + "@1.2.3.4:443?type=xhttp&extra=%7B%22a%22%3A%201%2C%20%22b%22%3A%20%5B1%2C%202%5D%7D"
		p := mustParse(t, link).Params.(Params)
		if want := `{"a":1,"b":[1,2]}`; p.Transport.Extra != want {
			t.Errorf("Extra = %q, want %q", p.Transport.Extra, want)
		}
	})

	t.Run("empty mode and extra default to zero values", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=xhttp").Params.(Params)
		if p.Transport.Mode != "" {
			t.Errorf("Mode = %q, want empty", p.Transport.Mode)
		}
		if p.Transport.Extra != "" {
			t.Errorf("Extra = %q, want empty", p.Transport.Extra)
		}
	})

	for _, mode := range []string{"", "auto", "packet-up", "stream-up", "stream-one"} {
		mode := mode
		t.Run("valid mode "+mode, func(t *testing.T) {
			link := "vless://" + uuid + "@1.2.3.4:443?type=xhttp&mode=" + mode
			p := mustParse(t, link).Params.(Params)
			if p.Transport.Mode != mode {
				t.Errorf("Mode = %q, want %q", p.Transport.Mode, mode)
			}
		})
	}

	t.Run("mode is lowercased and trimmed", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=xhttp&mode=%20Stream-Up%20").Params.(Params)
		if p.Transport.Mode != "stream-up" {
			t.Errorf("Mode = %q, want stream-up", p.Transport.Mode)
		}
	})

	t.Run("splithttp alias normalizes to xhttp", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=splithttp&mode=auto").Params.(Params)
		if p.Transport.Type != TransportXHTTP {
			t.Errorf("Transport.Type = %q, want xhttp", p.Transport.Type)
		}
		if p.Transport.Mode != "auto" {
			t.Errorf("Mode = %q, want auto", p.Transport.Mode)
		}
	})

	t.Run("stray mode on non-xhttp transport is ignored, not rejected", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=ws&mode=not-a-real-mode&path=%2Fws").Params.(Params)
		if p.Transport.Mode != "" {
			t.Errorf("Mode = %q, want empty (mode is xhttp-only)", p.Transport.Mode)
		}
	})
}

func TestParse_XHTTP_Errors(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		wantErr error
	}{
		{"unsupported mode", "vless://" + uuid + "@1.2.3.4:443?type=xhttp&mode=bogus", ErrUnsupportedXHTTPMode},
		{"invalid extra json", "vless://" + uuid + "@1.2.3.4:443?type=xhttp&extra=%7Bnot-json", ErrInvalidXHTTPExtra},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New().Parse(tt.link)
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestSetLabel(t *testing.T) {
	renamed, err := New().SetLabel(realLink, "New Name RU")
	if err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	after, err := New().Parse(renamed)
	if err != nil {
		t.Fatalf("Parse(renamed): %v", err)
	}
	if after.Label != "New Name RU" {
		t.Errorf("Label = %q, want %q", after.Label, "New Name RU")
	}
	if !reflect.DeepEqual(after.Params, mustParse(t, realLink).Params) {
		t.Error("SetLabel altered the endpoint beyond its label")
	}
}

// TestConformance runs the shared invariant suite from internal/protocol.
func TestConformance(t *testing.T) {
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    "vless",
		Input:     realLink,
		WantLabel: "test-server",
		Rejects: []string{
			"vless://1.2.3.4:443",                               // missing uuid
			"vless://not-a-uuid@1.2.3.4:443",                    // invalid uuid
			"trojan://" + uuid + "@1.2.3.4:443",                 // wrong scheme
			"vless://" + uuid + "@1.2.3.4:443?security=reality", // reality without pbk
		},
	})
}

// TestRenderNode_VLESS_MatchesGolden asserts RenderNode produces exactly the
// same JSON node as internal/singbox's generator did before the refactor,
// pinned by the existing golden file (proxy.golden.json's outbounds[0]).
func TestRenderNode_VLESS_MatchesGolden(t *testing.T) {
	p := mustParse(t, realLink)
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	assertMatchesGoldenOutbound(t, node, "../../singbox/testdata/proxy.golden.json", 0)
}

// TestRenderNode_XHTTP_MatchesGolden covers the XHTTP branch, pinned against
// proxy_xhttp_reality.golden.json.
func TestRenderNode_XHTTP_MatchesGolden(t *testing.T) {
	const xhttpRealityLink = "vless://" + uuid + "@193.188.22.147:443?type=xhttp&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome&host=cdn.example.com&path=%2Fxh&mode=stream-one#xhttp-reality"
	p := mustParse(t, xhttpRealityLink)
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	if out, ok := node.(singbox.XHTTPOutbound); !ok || out.Type != "vless-xhttp" {
		t.Fatalf("node = %#v, want XHTTPOutbound with type vless-xhttp", node)
	}
	assertMatchesGoldenOutbound(t, node, "../../singbox/testdata/proxy_xhttp_reality.golden.json", 0)
}

func TestRenderNode_WrongParamsType(t *testing.T) {
	_, err := New().RenderNode(protocol.Profile{Protocol: "vless", Params: "not-params"}, singbox.RenderOpts{})
	if err == nil {
		t.Fatal("expected an error for a mismatched Params type")
	}
}
