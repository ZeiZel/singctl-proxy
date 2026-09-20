package sub

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"singctl/internal/protocol"
	"singctl/internal/protocol/all"
)

// mustReadTestdata reads a fixture file, failing the test immediately if it
// is missing — every fixture here is checked in, so a read failure means the
// test itself is broken, not the code under test.
func mustReadTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	return b
}

// assertRoundTrips is the one property every structured-format test cares
// about, per the task's own framing: it proves each emitted link is
// something our OWN protocol parsers accept, by feeding the whole batch
// through the real registry the daemon uses. Byte-for-byte matching what an
// adapter renders would just be pinning this file's own choices; round-
// tripping instead validates the thing that actually matters — a server the
// user can connect to.
func assertRoundTrips(t *testing.T, links []string) []protocol.Profile {
	t.Helper()
	profiles, err := all.Registry().ParseAll(links)
	if err != nil {
		t.Fatalf("round-trip via protocol.Registry.ParseAll: %v\nlinks: %#v", err, links)
	}
	if len(profiles) != len(links) {
		t.Fatalf("ParseAll returned %d profiles for %d links", len(profiles), len(links))
	}
	return profiles
}

// protocolCounts tallies profiles by protocol name, for asserting the
// expected protocol mix survived a conversion without pinning link order.
func protocolCounts(profiles []protocol.Profile) map[protocol.Name]int {
	counts := make(map[protocol.Name]int)
	for _, p := range profiles {
		counts[p.Protocol]++
	}
	return counts
}

func TestParse_SingboxJSON(t *testing.T) {
	body := mustReadTestdata(t, "singbox.json")

	links, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// 8 convertible outbounds: 3 vless (reality+grpc, ws+tls, xhttp+reality),
	// vmess, trojan, shadowsocks, hysteria2, tuic. direct/block/dns/selector/
	// urltest and the wireguard endpoint must all be skipped, not error out.
	const wantCount = 8
	if len(links) != wantCount {
		t.Fatalf("got %d links, want %d\nlinks: %#v", len(links), wantCount, links)
	}

	profiles := assertRoundTrips(t, links)
	want := map[protocol.Name]int{"vless": 3, "vmess": 1, "trojan": 1, "shadowsocks": 1, "hysteria2": 1, "tuic": 1}
	if got := protocolCounts(profiles); !reflect.DeepEqual(got, want) {
		t.Errorf("protocol mix = %#v, want %#v", got, want)
	}
}

// TestParse_SingboxJSON_RealityLink pins the exact vless+reality link the
// sing-box adapter renders for the fixture's "vless-reality-grpc" outbound,
// since that is the shape a caller most needs to trust: reality's pbk/sid,
// grpc's serviceName, and the uuid/host/port all have to land in exactly the
// query params internal/protocol/vless's Parse reads.
func TestParse_SingboxJSON_RealityLink(t *testing.T) {
	got := buildVLESSLink(
		"vless-reality-grpc",
		"4ce58870-27d3-489b-87a0-3109db4fb919",
		"193.188.22.147",
		443,
		"",
		linkTLS{Enabled: true, ServerName: "cursor.com", Fingerprint: "chrome", RealityPub: "MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k", RealityShortID: "4d04"},
		linkTransport{Type: "grpc", ServiceName: "grpc-svc"},
	)
	want := "vless://4ce58870-27d3-489b-87a0-3109db4fb919@193.188.22.147:443?fp=chrome&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&security=reality&serviceName=grpc-svc&sid=4d04&sni=cursor.com&type=grpc#vless-reality-grpc"
	if got != want {
		t.Errorf("vless+reality link mismatch\n got: %s\nwant: %s", got, want)
	}

	// And it must actually parse back through the real registry.
	profile, err := all.Registry().Parse(got)
	if err != nil {
		t.Fatalf("parse rendered reality link: %v", err)
	}
	if profile.Label != "vless-reality-grpc" {
		t.Errorf("Label = %q, want vless-reality-grpc", profile.Label)
	}
}

func TestParse_ClashYAML(t *testing.T) {
	body := mustReadTestdata(t, "clash.yaml")

	links, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// Same 8 convertible protocols as the sing-box fixture; the ssr proxy
	// must be skipped, not error out.
	const wantCount = 8
	if len(links) != wantCount {
		t.Fatalf("got %d links, want %d\nlinks: %#v", len(links), wantCount, links)
	}

	profiles := assertRoundTrips(t, links)
	want := map[protocol.Name]int{"vless": 3, "vmess": 1, "trojan": 1, "shadowsocks": 1, "hysteria2": 1, "tuic": 1}
	if got := protocolCounts(profiles); !reflect.DeepEqual(got, want) {
		t.Errorf("protocol mix = %#v, want %#v", got, want)
	}
}

func TestParse_JSONLinksArray(t *testing.T) {
	body := mustReadTestdata(t, "links-array.json")

	links, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// 4 entries in the fixture, one (ssr://) unsupported: 3 must survive.
	const wantCount = 3
	if len(links) != wantCount {
		t.Fatalf("got %d links, want %d\nlinks: %#v", len(links), wantCount, links)
	}
	for _, l := range links {
		if l == "ssr://not-a-supported-scheme-base64" {
			t.Errorf("unsupported ssr:// entry leaked through: %#v", links)
		}
	}

	profiles := assertRoundTrips(t, links)
	want := map[protocol.Name]int{"vless": 1, "trojan": 1, "shadowsocks": 1}
	if got := protocolCounts(profiles); !reflect.DeepEqual(got, want) {
		t.Errorf("protocol mix = %#v, want %#v", got, want)
	}
}

func TestParse_JSONLinksWrapper(t *testing.T) {
	body := mustReadTestdata(t, "links-wrapper.json")

	links, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("got %d links, want 2\nlinks: %#v", len(links), links)
	}

	profiles := assertRoundTrips(t, links)
	want := map[protocol.Name]int{"vmess": 1, "hysteria2": 1}
	if got := protocolCounts(profiles); !reflect.DeepEqual(got, want) {
		t.Errorf("protocol mix = %#v, want %#v", got, want)
	}
}

// TestParse_JSONLinksObject_SubscriptionKey covers the "subscription" array
// alias to "links", built inline rather than as its own fixture since it is
// a one-line variant of links-wrapper.json's shape.
func TestParse_JSONLinksObject_SubscriptionKey(t *testing.T) {
	body := []byte(`{"subscription":["ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#sub-key-ss"]}`)
	links, err := Parse(body)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := []string{"ss://YWVzLTI1Ni1nY206cGFzc3dvcmQx@example.com:8388#sub-key-ss"}
	if !reflect.DeepEqual(links, want) {
		t.Errorf("links = %#v, want %#v", links, want)
	}
	assertRoundTrips(t, links)
}

func TestParse_StructuredFormats_Errors(t *testing.T) {
	tests := []struct {
		name string
		body []byte
	}{
		{
			name: "JSON object recognised as neither a sing-box config nor a link wrapper",
			body: []byte(`{"foo":"bar","baz":123}`),
		},
		{
			name: "sing-box config whose outbounds are all non-server entries",
			body: []byte(`{"outbounds":[{"type":"direct","tag":"direct"},{"type":"block","tag":"block"}]}`),
		},
		{
			name: "JSON array with no recognised share-link scheme",
			body: []byte(`["ssr://legacy","socks://also-not-supported"]`),
		},
		{
			name: "Clash config whose proxies are all unsupported",
			body: []byte("proxies:\n  - name: legacy\n    type: ssr\n    server: 1.2.3.4\n    port: 1234\n    cipher: aes-256-cfb\n    password: pw\n"),
		},
		{
			name: "invalid JSON object body",
			body: []byte(`{"outbounds": [}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.body)
			if err == nil {
				t.Fatalf("expected an error, got links: %#v", got)
			}
		})
	}
}

// TestParse_HTMLErrorPage documents that the new structured-format detection
// does not weaken the existing HTML-rejection behaviour: an HTML page starts
// with neither '{'/'[' nor a `proxies:` YAML mapping, so it falls straight
// through to the base64/plain-text path (already covered by
// TestParse_Errors in sub_test.go) and is still rejected.
func TestParse_HTMLErrorPage(t *testing.T) {
	body := []byte(`<html><head><title>404</title></head><body>Not Found</body></html>`)
	if _, err := Parse(body); err == nil {
		t.Fatal("expected an error for an HTML error page")
	}
}

// sanity check that our JSON test fixtures are themselves well-formed JSON,
// catching a hand-authoring mistake in a fixture before it manifests as a
// confusing failure in the tests above.
func TestTestdata_JSONFixturesAreValid(t *testing.T) {
	for _, name := range []string{"singbox.json", "links-array.json", "links-wrapper.json"} {
		t.Run(name, func(t *testing.T) {
			if !json.Valid(mustReadTestdata(t, name)) {
				t.Errorf("testdata/%s is not valid JSON", name)
			}
		})
	}
}
