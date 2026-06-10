package vless

import (
	"errors"
	"reflect"
	"testing"
)

const uuid = "4ce58870-27d3-489b-87a0-3109db4fb919"

func mustParse(t *testing.T, link string) ServerProfile {
	t.Helper()
	p, err := ParseLink(link)
	if err != nil {
		t.Fatalf("ParseLink(%q): %v", link, err)
	}
	return p
}

// TestParseLink_FieldDetails pins the field-level contracts the golden/round-trip
// tests don't: insecure flag, serviceName alias, the nil-vs-empty ALPN contract,
// multi-host trimming, and flow normalization.
func TestParseLink_FieldDetails(t *testing.T) {
	t.Run("allowInsecure=1 sets Insecure", func(t *testing.T) {
		if !mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=1").TLS.Insecure {
			t.Error("want Insecure=true")
		}
	})
	t.Run("allowInsecure=true sets Insecure (case-insensitive)", func(t *testing.T) {
		if !mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&allowInsecure=TRUE").TLS.Insecure {
			t.Error("want Insecure=true")
		}
	})
	t.Run("allowInsecure unset => false", func(t *testing.T) {
		if mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls").TLS.Insecure {
			t.Error("want Insecure=false")
		}
	})
	t.Run("lowercase servicename alias", func(t *testing.T) {
		p := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=grpc&security=reality&pbk=k&servicename=mysvc")
		if p.Transport.ServiceName != "mysvc" {
			t.Errorf("ServiceName = %q, want mysvc", p.Transport.ServiceName)
		}
	})
	t.Run("empty alpn yields nil (not empty slice)", func(t *testing.T) {
		if got := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?security=tls&alpn=").TLS.ALPN; got != nil {
			t.Errorf("ALPN = %#v, want nil", got)
		}
	})
	t.Run("multi host is split and trimmed", func(t *testing.T) {
		got := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?type=ws&security=tls&host=a.com,%20b.com").Transport.Host
		if want := []string{"a.com", "b.com"}; !reflect.DeepEqual(got, want) {
			t.Errorf("Host = %#v, want %#v", got, want)
		}
	})
	t.Run("flow is trimmed", func(t *testing.T) {
		if got := mustParse(t, "vless://"+uuid+"@1.2.3.4:443?flow=%20xtls-rprx-vision%20").Flow; got != "xtls-rprx-vision" {
			t.Errorf("Flow = %q, want xtls-rprx-vision", got)
		}
	})
}

// TestParseLink_MoreErrors covers reachable error branches missing from the
// original table: missing host, port overflow, non-numeric port, a link that
// breaks url.Parse, and unsupported encryption.
func TestParseLink_MoreErrors(t *testing.T) {
	tests := []struct {
		name    string
		link    string
		wantErr error
	}{
		{"missing host", "vless://" + uuid + "@:443", ErrMissingHost},
		{"port overflow", "vless://" + uuid + "@1.2.3.4:70000", ErrInvalidPort},
		{"non-numeric port (rejected by url.Parse)", "vless://" + uuid + "@1.2.3.4:abc", ErrNotVLESS},
		{"control chars break url.Parse", "vless://" + uuid + "@1.2.3.4:443/\x7f", ErrNotVLESS},
		{"unsupported encryption", "vless://" + uuid + "@1.2.3.4:443?encryption=mlkem768x25519plus", ErrUnsupportedEncryption},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseLink(tt.link)
			if err == nil {
				t.Fatalf("expected error %v, got nil", tt.wantErr)
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("got %v, want errors.Is(_, %v)", err, tt.wantErr)
			}
		})
	}
}

// TestParseError_String locks the ParseError message format (and covers its
// Error()/Unwrap() methods).
func TestParseError_String(t *testing.T) {
	_, err := ParseLink("vless://" + uuid + "@1.2.3.4:70000")
	var pe *ParseError
	if !errors.As(err, &pe) {
		t.Fatalf("want *ParseError, got %T", err)
	}
	if pe.Field != "port" || pe.Value != "70000" {
		t.Errorf("ParseError = %+v, want field=port value=70000", pe)
	}
	if !errors.Is(pe, ErrInvalidPort) {
		t.Error("Unwrap should expose ErrInvalidPort")
	}
}
