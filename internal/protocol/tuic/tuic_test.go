package tuic

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

const tuicUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"

func TestParse_OK(t *testing.T) {
	tests := []struct {
		name      string
		link      string
		wantLabel string
		want      Params
	}{
		{
			name:      "happy path with all params",
			link:      "tuic://" + tuicUUID + ":s3cr3t@1.2.3.4:443?congestion_control=bbr&udp_relay_mode=quic&zero_rtt_handshake=1&sni=example.com&alpn=h3&allow_insecure=1#my-server",
			wantLabel: "my-server",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				UUID:     tuicUUID,
				Password: "s3cr3t",
				TLS: TLS{
					ServerName: "example.com",
					ALPN:       []string{"h3"},
					Insecure:   true,
				},
				CongestionControl: "bbr",
				UDPRelayMode:      "quic",
				ZeroRTTHandshake:  true,
			},
		},
		{
			name:      "congestion alias, reduce_rtt alias, insecure alias",
			link:      "tuic://" + tuicUUID + ":pw@1.2.3.4:443?congestion=cubic&udp_relay_mode=native&reduce_rtt=true&insecure=true#aliases",
			wantLabel: "aliases",
			want: Params{
				Host:              "1.2.3.4",
				Port:              443,
				UUID:              tuicUUID,
				Password:          "pw",
				TLS:               TLS{Insecure: true},
				CongestionControl: "cubic",
				UDPRelayMode:      "native",
				ZeroRTTHandshake:  true,
			},
		},
		{
			name:      "new_reno congestion control, allowInsecure alias",
			link:      "tuic://" + tuicUUID + ":pw@1.2.3.4:443?congestion_control=new_reno&allowInsecure=1#new-reno",
			wantLabel: "new-reno",
			want: Params{
				Host:              "1.2.3.4",
				Port:              443,
				UUID:              tuicUUID,
				Password:          "pw",
				TLS:               TLS{Insecure: true},
				CongestionControl: "new_reno",
			},
		},
		{
			name:      "ipv6 host stored without brackets",
			link:      "tuic://" + tuicUUID + ":pw@[2001:db8::1]:443#v6",
			wantLabel: "v6",
			want: Params{
				Host:     "2001:db8::1",
				Port:     443,
				UUID:     tuicUUID,
				Password: "pw",
			},
		},
		{
			name:      "percent-encoded password and name",
			link:      "tuic://" + tuicUUID + ":p%40ss%3Aw0rd@1.2.3.4:443#My%20Server%20RU",
			wantLabel: "My Server RU",
			want: Params{
				Host:     "1.2.3.4",
				Port:     443,
				UUID:     tuicUUID,
				Password: "p@ss:w0rd",
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
				t.Fatalf("Params is %T, want tuic.Params", got.Params)
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
		{"missing uuid", "tuic://:pw@1.2.3.4:443", ErrMissingUUID},
		{"bad uuid", "tuic://not-a-uuid:pw@1.2.3.4:443", ErrInvalidUUID},
		{"missing password", "tuic://" + tuicUUID + "@1.2.3.4:443", ErrMissingPassword},
		{"missing host", "tuic://" + tuicUUID + ":pw@:443", ErrMissingHost},
		{"missing port", "tuic://" + tuicUUID + ":pw@1.2.3.4", ErrMissingPort},
		{"invalid port zero", "tuic://" + tuicUUID + ":pw@1.2.3.4:0", ErrInvalidPort},
		{"port overflow", "tuic://" + tuicUUID + ":pw@1.2.3.4:70000", ErrInvalidPort},
		{"bad congestion control", "tuic://" + tuicUUID + ":pw@1.2.3.4:443?congestion_control=reno", ErrUnsupportedCongestionControl},
		{"bad udp relay mode", "tuic://" + tuicUUID + ":pw@1.2.3.4:443?udp_relay_mode=tcp", ErrUnsupportedUDPRelayMode},
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
		Input:     "tuic://" + tuicUUID + ":s3cr3t@1.2.3.4:443?congestion_control=bbr&udp_relay_mode=quic&zero_rtt_handshake=1&sni=example.com&alpn=h3&allow_insecure=1#my-server",
		WantLabel: "my-server",
		Rejects: []string{
			"tuic://:pw@1.2.3.4:443",
			"tuic://not-a-uuid:pw@1.2.3.4:443",
			"tuic://" + tuicUUID + "@1.2.3.4:443",
			"not-a-link-at-all",
		},
	})
}

func TestRegistry_Routing(t *testing.T) {
	r, err := protocol.NewRegistry(New())
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Parse("tuic://" + tuicUUID + ":pw@1.2.3.4:443#x")
	if err != nil {
		t.Fatal(err)
	}
	if p.Protocol != Name {
		t.Errorf("routed to %q, want %q", p.Protocol, Name)
	}
}

// TestRenderNode_MatchesGolden proves RenderNode produces exactly the same
// outbound JSON as internal/singbox/generate.go's old tuicOutbound did, by
// comparing against the pre-refactor golden file's outbounds[0].
func TestRenderNode_MatchesGolden(t *testing.T) {
	profile := protocol.Profile{
		Protocol: Name,
		Label:    "tuic-test",
		Raw:      "tuic://" + tuicUUID + ":tuic-pw@tuic.example.com:443",
		Params: Params{
			Host:     "tuic.example.com",
			Port:     443,
			UUID:     tuicUUID,
			Password: "tuic-pw",
			TLS: TLS{
				ServerName: "tuic.example.com",
			},
			CongestionControl: "bbr",
			UDPRelayMode:      "native",
			ZeroRTTHandshake:  true,
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

	want := goldenOutbound(t, "../../singbox/testdata/proxy_tuic.golden.json")
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
