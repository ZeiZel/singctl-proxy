package hysteria

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
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
			link:      "hysteria://1.2.3.4:443?auth=s3cr3t&upmbps=100&downmbps=50&obfs=xplus&peer=example.com&alpn=h3&insecure=1#my-server",
			wantLabel: "my-server",
			want: Params{
				Host: "1.2.3.4",
				Port: 443,
				TLS: TLS{
					ServerName: "example.com",
					ALPN:       []string{"h3"},
					Insecure:   true,
				},
				UpMbps:   100,
				DownMbps: 50,
				Obfs:     "xplus",
				AuthStr:  "s3cr3t",
			},
		},
		{
			name:      "sni alias for peer, up/down aliases",
			link:      "hysteria://1.2.3.4:443?auth=pw&up=30&down=20&sni=sni.example.com#sni-alias",
			wantLabel: "sni-alias",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				TLS:      TLS{ServerName: "sni.example.com"},
				UpMbps:   30,
				DownMbps: 20,
				AuthStr:  "pw",
			},
		},
		{
			name:      "bandwidth with unit suffix forms",
			link:      "hysteria://1.2.3.4:443?auth=pw&upmbps=100%20mbps&downmbps=50mbps#bw",
			wantLabel: "bw",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				UpMbps:   100,
				DownMbps: 50,
				AuthStr:  "pw",
			},
		},
		{
			name:      "ipv6 host stored without brackets",
			link:      "hysteria://[2001:db8::1]:443?auth=pw&upmbps=10&downmbps=10#v6",
			wantLabel: "v6",
			want: Params{
				Host:     "2001:db8::1",
				Port:     443,
				UpMbps:   10,
				DownMbps: 10,
				AuthStr:  "pw",
			},
		},
		{
			name:      "percent-encoded auth and name",
			link:      "hysteria://1.2.3.4:443?auth=p%40ss%3Aw0rd&upmbps=10&downmbps=10#My%20Server%20RU",
			wantLabel: "My Server RU",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				UpMbps:   10,
				DownMbps: 10,
				AuthStr:  "p@ss:w0rd",
			},
		},
		{
			name:      "hy alias",
			link:      "hy://1.2.3.4:443?auth=pw&upmbps=10&downmbps=10#alias",
			wantLabel: "alias",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				UpMbps:   10,
				DownMbps: 10,
				AuthStr:  "pw",
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
				t.Fatalf("Params is %T, want hysteria.Params", got.Params)
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
		{"missing auth", "hysteria://1.2.3.4:443?upmbps=10&downmbps=10", ErrMissingAuth},
		{"missing upmbps", "hysteria://1.2.3.4:443?auth=pw&downmbps=10", ErrMissingHysteriaMbps},
		{"missing downmbps", "hysteria://1.2.3.4:443?auth=pw&upmbps=10", ErrMissingHysteriaMbps},
		{"zero upmbps", "hysteria://1.2.3.4:443?auth=pw&upmbps=0&downmbps=10", ErrMissingHysteriaMbps},
		{"missing host", "hysteria://:443?auth=pw&upmbps=10&downmbps=10", ErrMissingHost},
		{"missing port", "hysteria://1.2.3.4?auth=pw&upmbps=10&downmbps=10", ErrMissingPort},
		{"invalid port zero", "hysteria://1.2.3.4:0?auth=pw&upmbps=10&downmbps=10", ErrInvalidPort},
		{"port overflow", "hysteria://1.2.3.4:70000?auth=pw&upmbps=10&downmbps=10", ErrInvalidPort},
		{"wrong scheme", "hysteria2://1.2.3.4:443?auth=pw&upmbps=10&downmbps=10", ErrNotHysteria},
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

// TestConformance runs the shared invariant suite, plus alias-scheme routing.
func TestConformance(t *testing.T) {
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    Name,
		Input:     "hysteria://1.2.3.4:443?auth=s3cr3t&upmbps=100&downmbps=50&obfs=xplus&peer=example.com&alpn=h3&insecure=1#my-server",
		WantLabel: "my-server",
		Rejects: []string{
			"hysteria://1.2.3.4:443?upmbps=10&downmbps=10",
			"hysteria://1.2.3.4:443?auth=pw",
			"hysteria://:443?auth=pw&upmbps=10&downmbps=10",
			"hysteria://1.2.3.4?auth=pw&upmbps=10&downmbps=10",
			"not-a-link-at-all",
		},
	})
}

func TestDescriptor_ClaimsBothSchemes(t *testing.T) {
	d := New().Descriptor()
	want := map[string]bool{"hysteria": true, "hy": true}
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
		"hysteria://1.2.3.4:443?auth=pw&upmbps=10&downmbps=10#x",
		"hy://1.2.3.4:443?auth=pw&upmbps=10&downmbps=10#x",
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
// outbound JSON as internal/singbox/generate.go's old hysteriaOutbound did,
// by comparing against the pre-refactor golden file's outbounds[0].
func TestRenderNode_MatchesGolden(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Label:    "hysteria-test",
		Raw:      "hysteria://hy.example.com:443",
		Params: Params{
			Host: "hy.example.com",
			Port: 443,
			TLS: TLS{
				ServerName: "hy.example.com",
			},
			UpMbps:   100,
			DownMbps: 200,
			Obfs:     "obfs-pw",
			AuthStr:  "auth-str",
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

	want := goldenOutbound(t, "../../singbox/testdata/proxy_hysteria.golden.json")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RenderNode output does not match golden\n got: %+v\nwant: %+v", got, want)
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
