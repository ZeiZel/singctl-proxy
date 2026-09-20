package anytls

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
			name:      "happy path with sni, alpn, insecure",
			link:      "anytls://s3cr3t@example.com:8443?sni=example.com&alpn=h2,http%2F1.1&fp=chrome&insecure=1#anytls-server",
			wantLabel: "anytls-server",
			want: Params{
				Host:     "example.com",
				Port:     8443,
				Password: "s3cr3t",
				TLS: TLS{
					ServerName:  "example.com",
					Fingerprint: "chrome",
					ALPN:        []string{"h2", "http/1.1"},
					Insecure:    true,
				},
			},
		},
		{
			name:      "allowInsecure spelling and true value",
			link:      "anytls://pw@1.2.3.4:443?allowInsecure=true#name2",
			wantLabel: "name2",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				Password: "pw",
				TLS:      TLS{Insecure: true},
			},
		},
		{
			name:      "no params, defaults",
			link:      "anytls://pw@example.com:443",
			wantLabel: "",
			want: Params{
				Host:     "example.com",
				Port:     443,
				Password: "pw",
			},
		},
		{
			name:      "ipv6 host stored without brackets",
			link:      "anytls://pw@[2001:db8::1]:443#ipv6",
			wantLabel: "ipv6",
			want: Params{
				Host:     "2001:db8::1",
				Port:     443,
				Password: "pw",
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
				t.Fatalf("Params is %T, want anytls.Params", got.Params)
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
		{"missing password", "anytls://example.com:443", ErrMissingPassword},
		{"missing host", "anytls://pw@:443", ErrMissingHost},
		{"missing port", "anytls://pw@example.com", ErrMissingPort},
		{"invalid port zero", "anytls://pw@example.com:0", ErrInvalidPort},
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
		Input:     "anytls://s3cr3t@example.com:8443?sni=example.com&alpn=h2,http%2F1.1&fp=chrome&insecure=1#anytls-server",
		WantLabel: "anytls-server",
		Rejects: []string{
			"anytls://example.com:443",
			"anytls://pw@:443",
			"anytls://pw@example.com",
			"not-a-link-at-all",
		},
	})
}

func TestRegistry_Routing(t *testing.T) {
	r, err := protocol.NewRegistry(New())
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Parse("anytls://pw@1.2.3.4:443#x")
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != Name {
		t.Errorf("routed to %q, want %q", p.Protocol, Name)
	}
}

// TestRenderNode_MatchesGolden proves RenderNode produces exactly the same
// outbound JSON as internal/singbox/generate.go's old anytlsOutbound did, by
// comparing against the pre-refactor golden file's outbounds[0].
func TestRenderNode_MatchesGolden(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Label:    "anytls-test",
		Raw:      "anytls://anytls-pw@anytls.example.com:443",
		Params: Params{
			Host:     "anytls.example.com",
			Port:     443,
			Password: "anytls-pw",
			TLS: TLS{
				ServerName: "anytls.example.com",
			},
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

	want := goldenOutbound(t, "../../singbox/testdata/proxy_anytls.golden.json")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("RenderNode output does not match golden\n got: %+v\nwant: %+v", got, want)
	}
}

// TestRenderNode_UTLSFingerprint asserts the utls block is populated when a
// link carries a "fp" fingerprint — not exercised by the golden fixture,
// which has none.
func TestRenderNode_UTLSFingerprint(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Params: Params{
			Host:     "anytls.example.com",
			Port:     443,
			Password: "anytls-pw",
			TLS: TLS{
				ServerName:  "anytls.example.com",
				Fingerprint: "chrome",
			},
		},
	}
	node, err := New().RenderNode(profile, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	out, ok := node.(singbox.AnyTLSOutbound)
	if !ok {
		t.Fatalf("node is %T, want singbox.AnyTLSOutbound", node)
	}
	if out.TLS == nil || out.TLS.UTLS == nil || !out.TLS.UTLS.Enabled || out.TLS.UTLS.Fingerprint != "chrome" {
		t.Errorf("TLS.UTLS = %+v, want enabled fingerprint=chrome", out.TLS)
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
