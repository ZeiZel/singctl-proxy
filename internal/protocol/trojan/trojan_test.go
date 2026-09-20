package trojan

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

const trojanPassword = "s3cr3t-P@ss"

// assertMatchesGoldenOutbound marshals node to JSON and compares it
// (semantically, ignoring key order) against outbounds[index] in the golden
// config file at goldenPath — the same golden files internal/singbox's
// pre-refactor generator produced.
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

// trojanPasswordEscaped percent-encodes trojanPassword for embedding in test
// link literals (must match how a real client would emit "s3cr3t-P@ss").
func trojanPasswordEscaped() string {
	return "s3cr3t-P%40ss"
}

func TestParse_OK(t *testing.T) {
	tests := []struct {
		name  string
		link  string
		label string
		want  Params
	}{
		{
			name: "ws+tls happy path",
			link: "trojan://s3cr3t-P%40ss@example.com:443?type=ws&sni=example.com&host=cdn.example.com&path=%2Fwspath&alpn=h2,http%2F1.1&fp=chrome#My%20Server",
			want: Params{
				Password: trojanPassword,
				Host:     "example.com",
				Port:     443,
				Security: SecurityTLS,
				TLS: TLSParams{
					ServerName:  "example.com",
					Fingerprint: "chrome",
					ALPN:        []string{"h2", "http/1.1"},
				},
				Transport: TransportParams{
					Type: TransportWS,
					Path: "/wspath",
					Host: []string{"cdn.example.com"},
				},
			},
			label: "My Server",
		},
		{
			name: "default security is tls, default transport is tcp",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443",
			want: Params{
				Password:  trojanPassword,
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityTLS,
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "explicit security=tls",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443?security=tls",
			want: Params{
				Password:  trojanPassword,
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityTLS,
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "reality with pbk+sid (trojan-over-REALITY)",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443?security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome",
			want: Params{
				Password: trojanPassword,
				Host:     "1.2.3.4",
				Port:     443,
				Security: SecurityReality,
				TLS:      TLSParams{ServerName: "cursor.com", Fingerprint: "chrome"},
				Reality:  RealityParams{Enabled: true, PublicKey: "MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k", ShortID: "4d04"},
				Transport: TransportParams{
					Type: TransportTCP,
				},
			},
		},
		{
			name: "grpc with serviceName",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443?type=grpc&serviceName=mySvc",
			want: Params{
				Password: trojanPassword,
				Host:     "1.2.3.4",
				Port:     443,
				Security: SecurityTLS,
				Transport: TransportParams{
					Type:        TransportGRPC,
					ServiceName: "mySvc",
				},
			},
		},
		{
			name: "http transport with headerType",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443?type=http&headerType=http",
			want: Params{
				Password: trojanPassword,
				Host:     "1.2.3.4",
				Port:     443,
				Security: SecurityTLS,
				Transport: TransportParams{
					Type:       TransportHTTP,
					HeaderType: "http",
				},
			},
		},
		{
			name: "allowInsecure=1",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443?allowInsecure=1",
			want: Params{
				Password:  trojanPassword,
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityTLS,
				TLS:       TLSParams{Insecure: true},
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "ipv6 host stored without brackets",
			link: "trojan://" + trojanPasswordEscaped() + "@[2001:db8::1]:443",
			want: Params{
				Password:  trojanPassword,
				Host:      "2001:db8::1",
				Port:      443,
				Security:  SecurityTLS,
				Transport: TransportParams{Type: TransportTCP},
			},
		},
		{
			name: "percent-encoded name",
			link: "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443#My%20Server%20RU",
			want: Params{
				Password:  trojanPassword,
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityTLS,
				Transport: TransportParams{Type: TransportTCP},
			},
			label: "My Server RU",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().Parse(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Protocol != "trojan" {
				t.Errorf("Protocol = %q, want trojan", got.Protocol)
			}
			if got.Label != tt.label {
				t.Errorf("Label = %q, want %q", got.Label, tt.label)
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
		{"missing password", "trojan://1.2.3.4:443", ErrMissingPassword},
		{"missing host", "trojan://pw@:443", ErrMissingHost},
		{"missing port", "trojan://pw@1.2.3.4", ErrMissingPort},
		{"invalid port zero", "trojan://pw@1.2.3.4:0", ErrInvalidPort},
		{"security=none is a broken key", "trojan://pw@1.2.3.4:443?security=none", ErrTrojanSecurityNone},
		{"unsupported security", "trojan://pw@1.2.3.4:443?security=weird", ErrUnsupportedSecurity},
		{"unsupported transport", "trojan://pw@1.2.3.4:443?type=quicX", ErrUnsupportedTransport},
		{"reality without pbk", "trojan://pw@1.2.3.4:443?security=reality", ErrMissingRealityKey},
		{"wrong scheme", "vless://" + trojanPasswordEscaped() + "@1.2.3.4:443", ErrNotTrojan},
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

func TestSetLabel(t *testing.T) {
	link := "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443#old-name"
	renamed, err := New().SetLabel(link, "new-name")
	if err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	after, err := New().Parse(renamed)
	if err != nil {
		t.Fatalf("Parse(renamed): %v", err)
	}
	if after.Label != "new-name" {
		t.Errorf("Label = %q, want new-name", after.Label)
	}
	before, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse(before): %v", err)
	}
	if !reflect.DeepEqual(before.Params, after.Params) {
		t.Errorf("params changed by rename\n before: %+v\n after: %+v", before.Params, after.Params)
	}
}

// TestConformance runs the shared invariant suite from internal/protocol.
func TestConformance(t *testing.T) {
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    "trojan",
		Input:     "trojan://" + trojanPasswordEscaped() + "@1.2.3.4:443#conformance",
		WantLabel: "conformance",
		Rejects: []string{
			"trojan://1.2.3.4:443",                                // missing password
			"trojan://pw@1.2.3.4:443?security=none",               // trojan is TLS-only
			"vless://" + trojanPasswordEscaped() + "@1.2.3.4:443", // wrong scheme
		},
	})
}

// TestRenderNode_MatchesGolden asserts RenderNode produces exactly the same
// JSON node internal/singbox's generator produced before the refactor,
// pinned by proxy_trojan.golden.json.
func TestRenderNode_MatchesGolden(t *testing.T) {
	link := "trojan://trojan-pw@trojan.example.com:443?sni=trojan.example.com#trojan-test"
	p, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	assertMatchesGoldenOutbound(t, node, "../../singbox/testdata/proxy_trojan.golden.json", 0)
}

func TestRenderNode_WrongParamsType(t *testing.T) {
	_, err := New().RenderNode(protocol.Profile{Protocol: "trojan", Params: "not-params"}, singbox.RenderOpts{})
	if err == nil {
		t.Fatal("expected an error for a mismatched Params type")
	}
}
