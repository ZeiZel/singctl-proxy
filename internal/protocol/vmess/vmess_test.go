package vmess

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/singbox"
)

const vmessUUID = "4ce58870-27d3-489b-87a0-3109db4fb919"

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

// buildVMessLink JSON-encodes fields and base64-encodes the result with enc,
// producing a vmess:// link the way a real generator would.
func buildVMessLink(t *testing.T, fields map[string]any, enc *base64.Encoding) string {
	t.Helper()
	b, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return "vmess://" + enc.EncodeToString(b)
}

func TestParse_OK(t *testing.T) {
	base := map[string]any{
		"v": "2", "ps": "my-vmess", "add": "example.com", "port": 443,
		"id": vmessUUID, "aid": 0, "scy": "auto", "net": "ws", "type": "none",
		"host": "cdn.example.com", "path": "/wspath", "tls": "tls",
		"sni": "example.com", "alpn": "h2,http/1.1", "fp": "chrome",
	}

	tests := []struct {
		name  string
		link  string
		label string
		want  Params
	}{
		{
			name:  "standard padded base64, ws+tls",
			link:  buildVMessLink(t, base, base64.StdEncoding),
			label: "my-vmess",
			want: Params{
				UUID:     vmessUUID,
				Host:     "example.com",
				Port:     443,
				Security: SecurityTLS,
				TLS: TLSParams{
					ServerName:  "example.com",
					Fingerprint: "chrome",
					ALPN:        []string{"h2", "http/1.1"},
				},
				Transport: TransportParams{
					Type:       TransportWS,
					Path:       "/wspath",
					Host:       []string{"cdn.example.com"},
					HeaderType: "none",
				},
				AlterID:    0,
				Encryption: "auto",
			},
		},
		{
			name:  "raw unpadded base64, same payload",
			link:  buildVMessLink(t, base, base64.RawStdEncoding),
			label: "my-vmess",
			want: Params{
				UUID:     vmessUUID,
				Host:     "example.com",
				Port:     443,
				Security: SecurityTLS,
				TLS: TLSParams{
					ServerName:  "example.com",
					Fingerprint: "chrome",
					ALPN:        []string{"h2", "http/1.1"},
				},
				Transport: TransportParams{
					Type:       TransportWS,
					Path:       "/wspath",
					Host:       []string{"cdn.example.com"},
					HeaderType: "none",
				},
				AlterID:    0,
				Encryption: "auto",
			},
		},
		{
			name: "numeric port and aid as JSON strings",
			link: buildVMessLink(t, map[string]any{
				"ps": "str-fields", "add": "1.2.3.4", "port": "8443",
				"id": vmessUUID, "aid": "7", "net": "tcp", "tls": "",
			}, base64.StdEncoding),
			label: "str-fields",
			want: Params{
				UUID:       vmessUUID,
				Host:       "1.2.3.4",
				Port:       8443,
				Security:   SecurityNone,
				Transport:  TransportParams{Type: TransportTCP},
				AlterID:    7,
				Encryption: "auto",
			},
		},
		{
			name: "grpc: path maps to ServiceName",
			link: buildVMessLink(t, map[string]any{
				"ps": "grpc-node", "add": "grpc.example.com", "port": 443,
				"id": vmessUUID, "net": "grpc", "path": "mySvc", "tls": "tls",
				"sni": "grpc.example.com",
			}, base64.StdEncoding),
			label: "grpc-node",
			want: Params{
				UUID:     vmessUUID,
				Host:     "grpc.example.com",
				Port:     443,
				Security: SecurityTLS,
				TLS:      TLSParams{ServerName: "grpc.example.com"},
				Transport: TransportParams{
					Type:        TransportGRPC,
					ServiceName: "mySvc",
				},
				AlterID:    0,
				Encryption: "auto",
			},
		},
		{
			name: "net=h2 maps to TransportHTTP (not xhttp)",
			link: buildVMessLink(t, map[string]any{
				"ps": "h2-node", "add": "h2.example.com", "port": 443,
				"id": vmessUUID, "net": "h2", "path": "/h2path", "tls": "tls",
			}, base64.StdEncoding),
			label: "h2-node",
			want: Params{
				UUID:     vmessUUID,
				Host:     "h2.example.com",
				Port:     443,
				Security: SecurityTLS,
				Transport: TransportParams{
					Type: TransportHTTP,
					Path: "/h2path",
				},
				AlterID:    0,
				Encryption: "auto",
			},
		},
		{
			name: "tls off, default aid/scy, plain tcp",
			link: buildVMessLink(t, map[string]any{
				"ps": "plain", "add": "5.6.7.8", "port": 80, "id": vmessUUID,
			}, base64.StdEncoding),
			label: "plain",
			want: Params{
				UUID:       vmessUUID,
				Host:       "5.6.7.8",
				Port:       80,
				Security:   SecurityNone,
				Transport:  TransportParams{Type: TransportTCP},
				AlterID:    0,
				Encryption: "auto",
			},
		},
		{
			name: "explicit scy overrides default auto",
			link: buildVMessLink(t, map[string]any{
				"ps": "chacha", "add": "5.6.7.8", "port": 80, "id": vmessUUID,
				"scy": "chacha20-poly1305",
			}, base64.StdEncoding),
			label: "chacha",
			want: Params{
				UUID:       vmessUUID,
				Host:       "5.6.7.8",
				Port:       80,
				Security:   SecurityNone,
				Transport:  TransportParams{Type: TransportTCP},
				AlterID:    0,
				Encryption: "chacha20-poly1305",
			},
		},
		{
			name: "ipv6 host stored without brackets (add is a bare IPv6 literal)",
			link: buildVMessLink(t, map[string]any{
				"ps": "v6", "add": "2001:db8::1", "port": 443, "id": vmessUUID,
				"tls": "tls",
			}, base64.StdEncoding),
			label: "v6",
			want: Params{
				UUID:       vmessUUID,
				Host:       "2001:db8::1",
				Port:       443,
				Security:   SecurityTLS,
				Transport:  TransportParams{Type: TransportTCP},
				AlterID:    0,
				Encryption: "auto",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New().Parse(tt.link)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Protocol != "vmess" {
				t.Errorf("Protocol = %q, want vmess", got.Protocol)
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
			name:    "bad base64",
			link:    "vmess://not-valid-base64!!!",
			wantErr: ErrInvalidVMessBase64,
		},
		{
			name:    "bad json",
			link:    "vmess://" + base64.StdEncoding.EncodeToString([]byte("not json")),
			wantErr: ErrInvalidVMessJSON,
		},
		{
			name: "missing id",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": 443,
			}, base64.StdEncoding),
			wantErr: ErrInvalidUUID,
		},
		{
			name: "invalid id",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": 443, "id": "not-a-uuid",
			}, base64.StdEncoding),
			wantErr: ErrInvalidUUID,
		},
		{
			name: "missing add",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "port": 443, "id": vmessUUID,
			}, base64.StdEncoding),
			wantErr: ErrMissingHost,
		},
		{
			name: "missing port",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "id": vmessUUID,
			}, base64.StdEncoding),
			wantErr: ErrMissingPort,
		},
		{
			name: "invalid port (zero)",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": 0, "id": vmessUUID,
			}, base64.StdEncoding),
			wantErr: ErrInvalidPort,
		},
		{
			name: "invalid port (non-numeric string)",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": "abc", "id": vmessUUID,
			}, base64.StdEncoding),
			wantErr: ErrInvalidPort,
		},
		{
			name: "unsupported transport",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": 443, "id": vmessUUID,
				"net": "quicX",
			}, base64.StdEncoding),
			wantErr: ErrUnsupportedTransport,
		},
		{
			name: "invalid aid",
			link: buildVMessLink(t, map[string]any{
				"ps": "x", "add": "1.2.3.4", "port": 443, "id": vmessUUID,
				"aid": "not-a-number",
			}, base64.StdEncoding),
			wantErr: ErrInvalidVMessAlterID,
		},
		{
			name:    "wrong scheme",
			link:    "vless://" + vmessUUID + "@1.2.3.4:443",
			wantErr: ErrNotVMess,
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
	link := buildVMessLink(t, map[string]any{
		"v": "2", "ps": "old-name", "add": "example.com", "port": 443,
		"id": vmessUUID, "aid": 0, "scy": "auto", "net": "ws", "type": "none",
		"host": "cdn.example.com", "path": "/wspath", "tls": "tls",
		"sni": "example.com", "alpn": "h2,http/1.1", "fp": "chrome",
	}, base64.StdEncoding)

	renamed, err := New().SetLabel(link, "New Name RU")
	if err != nil {
		t.Fatalf("SetLabel: %v", err)
	}

	before, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse(before): %v", err)
	}
	after, err := New().Parse(renamed)
	if err != nil {
		t.Fatalf("Parse(after): %v", err)
	}

	if after.Label != "New Name RU" {
		t.Errorf("Label = %q, want %q", after.Label, "New Name RU")
	}
	if !reflect.DeepEqual(before.Params, after.Params) {
		t.Errorf("params changed by rename\n before: %+v\n after: %+v", before.Params, after.Params)
	}
}

func TestSetLabel_PreservesUnpaddedFlavour(t *testing.T) {
	link := buildVMessLink(t, map[string]any{
		"ps": "old", "add": "1.2.3.4", "port": 443, "id": vmessUUID,
	}, base64.RawURLEncoding)

	renamed, err := New().SetLabel(link, "new")
	if err != nil {
		t.Fatalf("SetLabel: %v", err)
	}

	payload := renamed[len("vmess://"):]
	if _, err := base64.RawURLEncoding.DecodeString(payload); err != nil {
		t.Errorf("renamed payload is not RawURLEncoding: %v", err)
	}

	after, err := New().Parse(renamed)
	if err != nil {
		t.Fatalf("Parse(renamed): %v", err)
	}
	if after.Label != "new" {
		t.Errorf("Label = %q, want new", after.Label)
	}
}

func TestSetLabel_Errors(t *testing.T) {
	if _, err := New().SetLabel("vless://x@1.2.3.4:443", "n"); !errors.Is(err, ErrNotVMess) {
		t.Errorf("wrong scheme: got %v, want ErrNotVMess", err)
	}
	if _, err := New().SetLabel("vmess://not-valid-base64!!!", "n"); !errors.Is(err, ErrInvalidVMessBase64) {
		t.Errorf("bad base64: got %v, want ErrInvalidVMessBase64", err)
	}
	if _, err := New().SetLabel("vmess://"+base64.StdEncoding.EncodeToString([]byte("not json")), "n"); !errors.Is(err, ErrInvalidVMessJSON) {
		t.Errorf("bad json: got %v, want ErrInvalidVMessJSON", err)
	}
}

// TestConformance runs the shared invariant suite from internal/protocol.
func TestConformance(t *testing.T) {
	link := buildVMessLink(t, map[string]any{
		"v": "2", "ps": "conformance-node", "add": "example.com", "port": 443,
		"id": vmessUUID, "aid": 0, "scy": "auto", "net": "tcp",
	}, base64.StdEncoding)
	protocol.CheckModule(t, New(), protocol.ConformanceCase{
		Module:    "vmess",
		Input:     link,
		WantLabel: "conformance-node",
		Rejects: []string{
			"vmess://not-valid-base64!!!",
			"vless://" + vmessUUID + "@1.2.3.4:443",
			"vmess://" + base64.StdEncoding.EncodeToString([]byte("not json")),
		},
	})
}

// TestRenderNode_MatchesGolden asserts RenderNode produces exactly the same
// JSON node internal/singbox's generator produced before the refactor,
// pinned by proxy_vmess.golden.json.
func TestRenderNode_MatchesGolden(t *testing.T) {
	link := buildVMessLink(t, map[string]any{
		"ps": "vmess-test", "add": "vmess.example.com", "port": 443,
		"id": vmessUUID, "aid": 0, "scy": "auto", "net": "ws",
		"host": "cdn.example.com", "path": "/vm", "tls": "tls",
		"sni": "vmess.example.com",
	}, base64.StdEncoding)
	p, err := New().Parse(link)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	node, err := New().RenderNode(p, singbox.RenderOpts{Tag: "proxy", ConnectTimeout: "10s"})
	if err != nil {
		t.Fatalf("RenderNode: %v", err)
	}
	assertMatchesGoldenOutbound(t, node, "../../singbox/testdata/proxy_vmess.golden.json", 0)
}

func TestRenderNode_WrongParamsType(t *testing.T) {
	_, err := New().RenderNode(protocol.Profile{Protocol: "vmess", Params: "not-params"}, singbox.RenderOpts{})
	if err == nil {
		t.Fatal("expected an error for a mismatched Params type")
	}
}
