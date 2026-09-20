package hysteria2

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

func TestParse_OK(t *testing.T) {
	tests := []struct {
		name      string
		link      string
		wantLabel string
		want      Params
	}{
		{
			name:      "happy path with all params",
			link:      "hysteria2://s3cr3t@1.2.3.4:443?sni=example.com&alpn=h3&insecure=1&obfs=salamander&obfs-password=obfspw&up=100&down=50#my-server",
			wantLabel: "my-server",
			want: Params{
				Password: "s3cr3t",
				Host:     "1.2.3.4",
				Port:     443,
				TLS: TLS{
					ServerName: "example.com",
					ALPN:       []string{"h3"},
					Insecure:   true,
				},
				UpMbps:       100,
				DownMbps:     50,
				ObfsType:     "salamander",
				ObfsPassword: "obfspw",
			},
		},
		{
			name:      "peer alias for sni, allowInsecure alias",
			link:      "hysteria2://pw@host.example:8443?peer=peer.example.com&allowInsecure=true#peer-alias",
			wantLabel: "peer-alias",
			want: Params{
				Password: "pw",
				Host:     "host.example",
				Port:     8443,
				TLS: TLS{
					ServerName: "peer.example.com",
					Insecure:   true,
				},
			},
		},
		{
			name:      "user:pass userinfo — whole thing is the password",
			link:      "hysteria2://user:pass@1.2.3.4:443#userpass",
			wantLabel: "userpass",
			want: Params{
				Password: "user:pass",
				Host:     "1.2.3.4",
				Port:     443,
			},
		},
		{
			name:      "bandwidth with space-separated unit suffix",
			link:      "hysteria2://pw@1.2.3.4:443?up=100%20mbps&down=50%20mbps#bw-space",
			wantLabel: "bw-space",
			want: Params{
				Password: "pw",
				Host:     "1.2.3.4",
				Port:     443,
				UpMbps:   100,
				DownMbps: 50,
			},
		},
		{
			name:      "bandwidth with attached unit suffix, upmbps/downmbps aliases",
			link:      "hysteria2://pw@1.2.3.4:443?upmbps=200mbps&downmbps=80mbps#bw-attached",
			wantLabel: "bw-attached",
			want: Params{
				Password: "pw",
				Host:     "1.2.3.4",
				Port:     443,
				UpMbps:   200,
				DownMbps: 80,
			},
		},
		{
			name:      "ipv6 host stored without brackets",
			link:      "hysteria2://pw@[2001:db8::1]:443#v6",
			wantLabel: "v6",
			want: Params{
				Password: "pw",
				Host:     "2001:db8::1",
				Port:     443,
			},
		},
		{
			name:      "percent-encoded password and name",
			link:      "hysteria2://p%40ss%3Aw0rd@1.2.3.4:443#My%20Server%20RU",
			wantLabel: "My Server RU",
			want: Params{
				Password: "p@ss:w0rd",
				Host:     "1.2.3.4",
				Port:     443,
			},
		},
		{
			name:      "hy2 alias",
			link:      "hy2://pw@1.2.3.4:443?obfs=salamander&obfs-password=op#alias",
			wantLabel: "alias",
			want: Params{
				Password:     "pw",
				Host:         "1.2.3.4",
				Port:         443,
				ObfsType:     "salamander",
				ObfsPassword: "op",
			},
		},
	}

	m := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.Parse(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Protocol != Name {
				t.Errorf("Protocol = %q, want %q", got.Protocol, Name)
			}
			if got.Label != tt.wantLabel {
				t.Errorf("Label = %q, want %q", got.Label, tt.wantLabel)
			}
			params, ok := got.Params.(Params)
			if !ok {
				t.Fatalf("Params is %T, want hysteria2.Params", got.Params)
			}
			if !reflect.DeepEqual(params, tt.want) {
				t.Errorf("params mismatch\n got: %+v\nwant: %+v", params, tt.want)
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
		{"missing password", "hysteria2://1.2.3.4:443", ErrMissingPassword},
		{"missing host", "hysteria2://pw@:443", ErrMissingHost},
		{"missing port", "hysteria2://pw@1.2.3.4", ErrMissingPort},
		{"invalid port zero", "hysteria2://pw@1.2.3.4:0", ErrInvalidPort},
		{"port overflow", "hysteria2://pw@1.2.3.4:70000", ErrInvalidPort},
		{"bad obfs type", "hysteria2://pw@1.2.3.4:443?obfs=xor", ErrUnsupportedObfs},
		{"not a hysteria2 link", "hysteria2X://pw@1.2.3.4:443", ErrNotHysteria2},
	}

	m := New()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.Parse(tt.link)
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error mismatch: got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

func TestConformance(t *testing.T) {
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    Name,
		Input:     "hysteria2://s3cr3t@1.2.3.4:443?sni=example.com&alpn=h3&insecure=1&obfs=salamander&obfs-password=obfspw&up=100&down=50#my-server",
		WantLabel: "my-server",
		Rejects: []string{
			"hysteria2://1.2.3.4:443",
			"hysteria2://pw@:443",
			"hysteria2://pw@1.2.3.4:443?obfs=xor",
			"not-a-link-at-all",
		},
	})
}

func TestDescriptor_ClaimsBothSchemes(t *testing.T) {
	d := New().Descriptor()
	want := map[string]bool{"hysteria2": true, "hy2": true}
	if len(d.Schemes) != len(want) {
		t.Fatalf("Schemes = %v, want exactly %v", d.Schemes, want)
	}
	for _, s := range d.Schemes {
		if !want[s] {
			t.Errorf("unexpected scheme %q", s)
		}
	}
}

func TestRegistry_AliasRouting(t *testing.T) {
	r, err := protocol.NewRegistry(New())
	if err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{
		"hysteria2://pw@1.2.3.4:443#x",
		"hy2://pw@1.2.3.4:443#x",
	} {
		p, err := r.Parse(link)
		if err != nil {
			t.Fatalf("Parse(%q): %v", link, err)
		}
		if p.Protocol != Name {
			t.Errorf("Parse(%q) routed to %q, want %q", link, p.Protocol, Name)
		}
	}
}

// TestRenderNode_MatchesGolden proves RenderNode produces exactly the same
// outbound JSON as internal/singbox/generate.go's old hysteria2Outbound did,
// including the nested obfs block, by comparing against the pre-refactor
// golden file's outbounds[0].
func TestRenderNode_MatchesGolden(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Label:    "hysteria2-test",
		Raw:      "hysteria2://hy2-pw@hy2.example.com:443",
		Params: Params{
			Host:     "hy2.example.com",
			Port:     443,
			Password: "hy2-pw",
			TLS: TLS{
				ServerName: "hy2.example.com",
			},
			UpMbps:       50,
			DownMbps:     150,
			ObfsType:     "salamander",
			ObfsPassword: "salamander-pw",
		},
	}

	node, err := New().RenderNode(profile, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	gotBytes, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal RenderNode output: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(gotBytes, &got); err != nil {
		t.Fatalf("unmarshal got: %v", err)
	}

	want := goldenOutbound(t, "../../singbox/testdata/proxy_hysteria2.golden.json")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RenderNode output does not match golden\n got: %+v\nwant: %+v", got, want)
	}
}

// TestRenderNode_NoObfsType_OmitsObfsBlock asserts the nested obfs object is
// omitted entirely (not emitted as a null/empty object) when the link carries
// no obfuscation.
func TestRenderNode_NoObfsType_OmitsObfsBlock(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Params: Params{
			Host:     "hy2.example.com",
			Port:     443,
			Password: "hy2-pw",
			TLS:      TLS{ServerName: "hy2.example.com"},
		},
	}
	node, err := New().RenderNode(profile, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	out, ok := node.(singbox.Hysteria2Outbound)
	if !ok {
		t.Fatalf("node is %T, want singbox.Hysteria2Outbound", node)
	}
	if out.Obfs != nil {
		t.Errorf("Obfs = %+v, want nil when ObfsType is empty", out.Obfs)
	}
	gotBytes, err := json.Marshal(node)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(gotBytes), `"obfs"`) {
		t.Errorf("obfs key must be entirely absent when ObfsType is empty, got:\n%s", gotBytes)
	}
}

// goldenOutbound reads a singbox golden config file and returns the first
// entry of its "outbounds" array, decoded generically so the comparison does
// not depend on which Go struct produced it.
func goldenOutbound(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v", path, err)
	}
	var cfg struct {
		Outbounds []map[string]any `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal golden %s: %v", path, err)
	}
	if len(cfg.Outbounds) == 0 {
		t.Fatalf("golden %s has no outbounds", path)
	}
	return cfg.Outbounds[0]
}
