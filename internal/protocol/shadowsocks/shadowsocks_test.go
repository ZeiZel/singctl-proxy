package shadowsocks

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

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

func TestParse_OK(t *testing.T) {
	tests := []struct {
		name  string
		link  string
		label string
		want  Params
	}{
		{
			name: "SIP002 padded standard base64",
			// base64("aes-256-gcm:password1") == YWVzLTI1Ni1nY206cGFzc3dvcmQx
			link: "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#server1",
			want: Params{
				Host:     "example.com",
				Port:     8388,
				Password: "password1",
				Method:   "aes-256-gcm",
			},
			label: "server1",
		},
		{
			name: "SIP002 unpadded url-safe base64",
			// base64url-nopad("chacha20-ietf-poly1305:p4ssW0rd!")
			link: "ss://Y2hhY2hhMjAtaWV0Zi1wb2x5MTMwNTpwNHNzVzByZCE@1.2.3.4:9999#chacha",
			want: Params{
				Host:     "1.2.3.4",
				Port:     9999,
				Password: "p4ssW0rd!",
				Method:   "chacha20-ietf-poly1305",
			},
			label: "chacha",
		},
		{
			name: "SIP002 percent-encoded non-base64 userinfo (escaped colon)",
			link: "ss://aes-256-gcm%3Amypassword@example.com:8388#plain",
			want: Params{
				Host:     "example.com",
				Port:     8388,
				Password: "mypassword",
				Method:   "aes-256-gcm",
			},
			label: "plain",
		},
		{
			name: "SIP002 percent-encoded userinfo with literal colon",
			link: "ss://aes-256-gcm:mypassword@example.com:8388#plain2",
			want: Params{
				Host:     "example.com",
				Port:     8388,
				Password: "mypassword",
				Method:   "aes-256-gcm",
			},
			label: "plain2",
		},
		{
			name: "legacy whole-blob base64 without fragment",
			// base64("aes-256-gcm:password@leg.example.com:8388")
			link: "ss://YWVzLTI1Ni1nY206cGFzc3dvcmRAbGVnLmV4YW1wbGUuY29tOjgzODg=",
			want: Params{
				Host:     "leg.example.com",
				Port:     8388,
				Password: "password",
				Method:   "aes-256-gcm",
			},
		},
		{
			name: "legacy whole-blob base64 with fragment",
			link: "ss://YWVzLTI1Ni1nY206cGFzc3dvcmRAbGVnLmV4YW1wbGUuY29tOjgzODg=#My%20Server",
			want: Params{
				Host:     "leg.example.com",
				Port:     8388,
				Password: "password",
				Method:   "aes-256-gcm",
			},
			label: "My Server",
		},
		{
			name: "2022-blake3 method with base64 password containing + and /",
			// base64("2022-blake3-aes-128-gcm:ab+c/d==")
			link: "ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206YWIrYy9kPT0=@example.com:8388#aead2022",
			want: Params{
				Host:     "example.com",
				Port:     8388,
				Password: "ab+c/d==",
				Method:   "2022-blake3-aes-128-gcm",
			},
			label: "aead2022",
		},
		{
			name: "plugin with options",
			// base64("aes-256-gcm:pluginpass")
			link: "ss://YWVzLTI1Ni1nY206cGx1Z2lucGFzcw==@example.com:8388/?plugin=obfs-local%3Bobfs%3Dtls%3Bobfs-host%3Dwww.bing.com#withplugin",
			want: Params{
				Host:          "example.com",
				Port:          8388,
				Password:      "pluginpass",
				Method:        "aes-256-gcm",
				Plugin:        "obfs-local",
				PluginOptions: "obfs=tls;obfs-host=www.bing.com",
			},
			label: "withplugin",
		},
		{
			name: "IPv6 host stored without brackets",
			// base64("aes-256-gcm:ipv6pass")
			link: "ss://YWVzLTI1Ni1nY206aXB2NnBhc3M=@[2001:db8::1]:8388#ipv6",
			want: Params{
				Host:     "2001:db8::1",
				Port:     8388,
				Password: "ipv6pass",
				Method:   "aes-256-gcm",
			},
			label: "ipv6",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().Parse(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Protocol != "shadowsocks" {
				t.Errorf("Protocol = %q, want shadowsocks", got.Protocol)
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
		{
			name:    "SIP002 empty method",
			link:    "ss://OnBhc3N3b3Jk@example.com:8388", // base64(":password")
			wantErr: ErrMissingMethod,
		},
		{
			name:    "SIP002 empty password",
			link:    "ss://bWV0aG9kOg==@example.com:8388", // base64("method:")
			wantErr: ErrMissingPassword,
		},
		{
			name:    "SIP002 missing host",
			link:    "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@:8388#name",
			wantErr: ErrMissingHost,
		},
		{
			name:    "SIP002 missing port",
			link:    "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com#name",
			wantErr: ErrMissingPort,
		},
		{
			name:    "SIP002 invalid port zero",
			link:    "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:0#name",
			wantErr: ErrInvalidPort,
		},
		{
			name:    "legacy undecodable base64",
			link:    "ss://not-valid-base64-!!!!",
			wantErr: ErrInvalidLink,
		},
		{
			name:    "legacy decoded blob missing @",
			link:    "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQ=", // base64("aes-256-gcm:password"), no host@
			wantErr: ErrInvalidLink,
		},
		{
			name:    "wrong scheme",
			link:    "vless://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388",
			wantErr: ErrInvalidLink,
		},
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
	link := "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#old-name"
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
		Module:    "shadowsocks",
		Input:     "ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#conformance",
		WantLabel: "conformance",
		Rejects: []string{
			"ss://not-valid-base64-!!!!",
			"ss://OnBhc3N3b3Jk@example.com:8388", // empty method
			"vless://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388",
		},
	})
}

// TestRenderNode_MatchesGolden asserts RenderNode produces exactly the same
// JSON node internal/singbox's generator produced before the refactor,
// pinned by proxy_shadowsocks.golden.json.
func TestRenderNode_MatchesGolden(t *testing.T) {
	// base64("2022-blake3-aes-128-gcm:ss-pw")
	link := "ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206c3MtcHc=@ss.example.com:8388#ss-test"
	p, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	assertMatchesGoldenOutbound(t, node, "../../singbox/testdata/proxy_shadowsocks.golden.json", 0)
}

// TestRenderNode_NoTLS locks the protocol-specific contract: shadowsocks
// encrypts its own stream, so the marshaled outbound must never contain a
// "tls" key at all.
func TestRenderNode_NoTLS(t *testing.T) {
	p, err := New().Parse("ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	b, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if got := string(b); strings.Contains(got, `"tls"`) {
		t.Errorf("shadowsocks outbound must never emit a tls field, got:\n%s", got)
	}
}

func TestRenderNode_WrongParamsType(t *testing.T) {
	_, err := New().RenderNode(protocol.Profile{Protocol: "shadowsocks", Params: "not-params"}, singbox.RenderOpts{})
	if err == nil {
		t.Fatal("expected an error for a mismatched Params type")
	}
}
