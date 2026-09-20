package protocol

import (
	"errors"
	"strings"
	"testing"
)

func testRegistryWithWG(t *testing.T) *Registry {
	t.Helper()
	vless := linkModule("vless", "vless")
	wg := fakeModule{desc: Descriptor{
		Name: "wireguard", Title: "WireGuard", Kind: KindEndpoint, Input: InputConfig,
	}, sniffFor: "[Interface]"}
	r, err := NewRegistry(vless, wg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseAll_MultipleShareLinks(t *testing.T) {
	r := testRegistryWithWG(t)
	profiles, err := r.ParseAll([]string{"vless://a#one vless://b#two;vless://c#three"})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("got %d profiles, want 3: %+v", len(profiles), profiles)
	}
	for i, want := range []string{"one", "two", "three"} {
		if profiles[i].Label != want {
			t.Errorf("profile %d label = %q, want %q", i, profiles[i].Label, want)
		}
	}
}

func TestParseAll_ConfigAloneIsOneProfile(t *testing.T) {
	r := testRegistryWithWG(t)
	cfg := "[Interface]\nPrivateKey = abc\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = def\n"
	profiles, err := r.ParseAll([]string{cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1: %+v", len(profiles), profiles)
	}
	if profiles[0].Protocol != "wireguard" {
		t.Fatalf("Protocol = %q, want wireguard", profiles[0].Protocol)
	}
	// The config's own internal newlines must survive intact — this is the
	// regression the whole-blob sniff check exists to prevent: splitLinks
	// would have shredded it into one token per line. Only the outer
	// leading/trailing whitespace is trimmed, same as any other input.
	if profiles[0].Raw != strings.TrimSpace(cfg) {
		t.Errorf("Raw = %q, want the config unmangled:\n%q", profiles[0].Raw, strings.TrimSpace(cfg))
	}
	if !strings.Contains(profiles[0].Params.(string), "[Peer]") {
		t.Errorf("config was mangled before reaching Parse: %v", profiles[0].Params)
	}
}

func TestParseAll_ConfigAlongsideNothingElse(t *testing.T) {
	// A WireGuard config pasted as the sole input, with no other keys in the
	// batch — the case the coordinator's spec calls out explicitly.
	r := testRegistryWithWG(t)
	cfg := "[Interface]\nPrivateKey = abc\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = def\n"
	profiles, err := r.ParseAll([]string{"", cfg, "  "})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 1 || profiles[0].Protocol != "wireguard" {
		t.Fatalf("got %+v, want exactly one wireguard profile", profiles)
	}
}

func TestParseAll_MixedConfigAndLinks(t *testing.T) {
	r := testRegistryWithWG(t)
	cfg := "[Interface]\nPrivateKey = abc\nAddress = 10.0.0.2/32\n\n[Peer]\nPublicKey = def\n"
	profiles, err := r.ParseAll([]string{"vless://a#one", cfg, "vless://b#two"})
	if err != nil {
		t.Fatal(err)
	}
	if len(profiles) != 3 {
		t.Fatalf("got %d profiles, want 3: %+v", len(profiles), profiles)
	}
	if profiles[0].Protocol != "vless" || profiles[1].Protocol != "wireguard" || profiles[2].Protocol != "vless" {
		t.Fatalf("got protocols %q, %q, %q", profiles[0].Protocol, profiles[1].Protocol, profiles[2].Protocol)
	}
}

func TestParseAll_Empty(t *testing.T) {
	r := testRegistryWithWG(t)
	if _, err := r.ParseAll(nil); !errors.Is(err, ErrNoProfiles) {
		t.Fatalf("error = %v, want ErrNoProfiles", err)
	}
	if _, err := r.ParseAll([]string{"", "   "}); !errors.Is(err, ErrNoProfiles) {
		t.Fatalf("error = %v, want ErrNoProfiles", err)
	}
}

func TestParseAll_PropagatesParseError(t *testing.T) {
	r := testRegistryWithWG(t)
	if _, err := r.ParseAll([]string{"vless://"}); err == nil {
		t.Fatal("ParseAll succeeded on a malformed link, want an error")
	}
}
