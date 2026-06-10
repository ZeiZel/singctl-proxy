package vless

import (
	"errors"
	"reflect"
	"testing"
)

const realLink = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?type=grpc&security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sid=4d04&sni=cursor.com&fp=chrome#test-server"

func TestParseLink_OK(t *testing.T) {
	tests := []struct {
		name string
		link string
		want ServerProfile
	}{
		{
			name: "reality grpc (real link)",
			link: realLink,
			want: ServerProfile{
				UUID:     "4ce58870-27d3-489b-87a0-3109db4fb919",
				Host:     "193.188.22.147",
				Port:     443,
				Name:     "test-server",
				Security: SecurityReality,
				TLS:      TLSParams{ServerName: "cursor.com", Fingerprint: "chrome"},
				Reality:  RealityParams{Enabled: true, PublicKey: "MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k", ShortID: "4d04"},
				Transport: TransportParams{
					Type: TransportGRPC,
				},
				Raw: realLink,
			},
		},
		{
			name: "tls websocket with host header and path",
			link: "vless://4ce58870-27d3-489b-87a0-3109db4fb919@example.com:8443?type=ws&security=tls&sni=example.com&host=cdn.example.com&path=%2Fwspath&alpn=h2,http%2F1.1#ws",
			want: ServerProfile{
				UUID:     "4ce58870-27d3-489b-87a0-3109db4fb919",
				Host:     "example.com",
				Port:     8443,
				Name:     "ws",
				Security: SecurityTLS,
				TLS:      TLSParams{ServerName: "example.com", ALPN: []string{"h2", "http/1.1"}},
				Transport: TransportParams{
					Type: TransportWS,
					Path: "/wspath",
					Host: []string{"cdn.example.com"},
				},
				Raw: "vless://4ce58870-27d3-489b-87a0-3109db4fb919@example.com:8443?type=ws&security=tls&sni=example.com&host=cdn.example.com&path=%2Fwspath&alpn=h2,http%2F1.1#ws",
			},
		},
		{
			name: "plain tcp no security (defaults)",
			link: "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443",
			want: ServerProfile{
				UUID:      "4ce58870-27d3-489b-87a0-3109db4fb919",
				Host:      "1.2.3.4",
				Port:      443,
				Security:  SecurityNone,
				Transport: TransportParams{Type: TransportTCP},
				Raw:       "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443",
			},
		},
		{
			name: "ipv6 host stored without brackets",
			link: "vless://4ce58870-27d3-489b-87a0-3109db4fb919@[2001:db8::1]:443?security=tls",
			want: ServerProfile{
				UUID:      "4ce58870-27d3-489b-87a0-3109db4fb919",
				Host:      "2001:db8::1",
				Port:      443,
				Security:  SecurityTLS,
				TLS:       TLSParams{},
				Transport: TransportParams{Type: TransportTCP},
				Raw:       "vless://4ce58870-27d3-489b-87a0-3109db4fb919@[2001:db8::1]:443?security=tls",
			},
		},
		{
			name: "percent-encoded name and flow",
			link: "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?flow=xtls-rprx-vision#My%20Server%20RU",
			want: ServerProfile{
				UUID:      "4ce58870-27d3-489b-87a0-3109db4fb919",
				Host:      "1.2.3.4",
				Port:      443,
				Name:      "My Server RU",
				Flow:      "xtls-rprx-vision",
				Security:  SecurityNone,
				Transport: TransportParams{Type: TransportTCP},
				Raw:       "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?flow=xtls-rprx-vision#My%20Server%20RU",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseLink(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("profile mismatch\n got: %+v\nwant: %+v", got, tt.want)
			}
		})
	}
}

func TestParseLink_Errors(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		wantErr error
	}{
		{"empty", "", ErrNotVLESS},
		{"wrong scheme", "vmess://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443", ErrNotVLESS},
		{"missing uuid", "vless://1.2.3.4:443", ErrMissingUUID},
		{"invalid uuid", "vless://not-a-uuid@1.2.3.4:443", ErrInvalidUUID},
		{"missing port", "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4", ErrMissingPort},
		{"invalid port zero", "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:0", ErrInvalidPort},
		{"reality without pbk", "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=reality", ErrMissingRealityKey},
		{"unsupported transport", "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?type=quicX", ErrUnsupportedTransport},
		{"unsupported security", "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.2.3.4:443?security=weird", ErrUnsupportedSecurity},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLink(tt.link)
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("error mismatch: got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}
