package all

import (
	"strings"
	"testing"
)

// TestParseAll_LabelsWithSpacesAreNotShredded pins a bug that reached a user.
// Splitting a pasted blob on whitespace looks obvious and is wrong: a share
// link's #fragment is a display label chosen by the panel, and real ones carry
// spaces, emoji and punctuation — "#Poland 🇵🇱", "#Auto → [🚀 Optimal]".
// Whitespace splitting turned ONE such link into a valid link plus a handful of
// label shards, and the first shard ("→") reached the parser as a key, so a
// perfectly good subscription failed with "unsupported protocol".
//
// This runs against the REAL registry and real xhttp/reality links, in the
// exact shape the panel that exposed the bug serves.
func TestParseAll_LabelsWithSpacesAreNotShredded(t *testing.T) {
	const blob = "vless://3eab6f7f-6bdf-5989-a197-274dc2414c90@poland.example.com:8443" +
		"?type=xhttp&security=reality&sni=dlcdnet.asus.com" +
		"&pbk=CeDq6SvOWZ67DnrhGI4FBupElyEYWupPfyNbDrtVxDw&sid=65258ade&fp=chrome" +
		"&mode=stream-one&path=/xhttp#Auto → [\U0001F680 Optimal location]\n" +
		"vless://3eab6f7f-6bdf-5989-a197-274dc2414c90@france.example.com:8443" +
		"?type=xhttp&security=reality&sni=dlcdnet.asus.com" +
		"&pbk=0L44itnIvi_KDMKngoQGzN-f6k8TLeM3fIio686tjRM&sid=0d77807d&fp=chrome" +
		"&mode=stream-one&path=/xhttp#France \U0001F1EB\U0001F1F7\n"

	profiles, err := Registry().ParseAll([]string{blob})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	if len(profiles) != 2 {
		t.Fatalf("parsed %d profiles, want 2 — a label with spaces was split into extra keys", len(profiles))
	}
	if got, want := profiles[0].Label, "Auto → [\U0001F680 Optimal location]"; got != want {
		t.Errorf("label 0 = %q, want %q", got, want)
	}
	if got, want := profiles[1].Label, "France \U0001F1EB\U0001F1F7"; got != want {
		t.Errorf("label 1 = %q, want %q", got, want)
	}
	for i, p := range profiles {
		if p.Protocol != "vless" {
			t.Errorf("profile %d protocol = %q", i, p.Protocol)
		}
	}
}

// TestParseAll_SeparatorsBetweenLinks proves the scheme-anchored split accepts
// every separator panels actually use — including a comma, which must NOT be
// treated as a separator INSIDE a link, where it is legal (alpn=h2,http/1.1).
func TestParseAll_SeparatorsBetweenLinks(t *testing.T) {
	const a = "trojan://pw@a.example.com:443?sni=a.example.com&alpn=h2,http/1.1#A one"
	const b = "trojan://pw@b.example.com:443?sni=b.example.com#B two"

	for _, sep := range []string{"\n", " ", ";", ",", "\r\n", "\n\n", " ; "} {
		profiles, err := Registry().ParseAll([]string{a + sep + b})
		if err != nil {
			t.Fatalf("separator %q: %v", sep, err)
		}
		if len(profiles) != 2 {
			t.Fatalf("separator %q: parsed %d profiles, want 2", sep, len(profiles))
		}
		if profiles[0].Label != "A one" || profiles[1].Label != "B two" {
			t.Errorf("separator %q: labels = %q, %q", sep, profiles[0].Label, profiles[1].Label)
		}
		// The comma inside the first link's alpn must have survived.
		if !strings.Contains(profiles[0].Raw, "alpn=h2,http/1.1") {
			t.Errorf("separator %q: the link's own comma was treated as a separator: %q", sep, profiles[0].Raw)
		}
	}
}
