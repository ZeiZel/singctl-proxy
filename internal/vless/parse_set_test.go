package vless

import (
	"errors"
	"testing"
)

const (
	linkA = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@1.1.1.1:443?security=reality&pbk=MLWbCmCus3crtCxy2QAuO1zp74nbDE1zMvO1azp-F0k&sni=a.com&type=grpc&serviceName=g#A"
	linkB = "vless://4ce58870-27d3-489b-87a0-3109db4fb919@2.2.2.2:8443?security=tls&sni=b.com#B"
)

func TestParseLinks_PreservesPriorityOrder(t *testing.T) {
	set, err := ParseLinks([]string{linkA, linkB})
	if err != nil {
		t.Fatalf("ParseLinks: %v", err)
	}
	if set.Len() != 2 {
		t.Fatalf("Len = %d, want 2", set.Len())
	}
	if !set.Multi() {
		t.Error("Multi() should be true for 2 servers")
	}
	if set.Primary().Host != "1.1.1.1" {
		t.Errorf("Primary = %q, want 1.1.1.1 (first = highest priority)", set.Primary().Host)
	}
	if set.Profiles[1].Host != "2.2.2.2" {
		t.Errorf("second = %q, want 2.2.2.2", set.Profiles[1].Host)
	}
}

func TestParseLinks_SplitsBlob(t *testing.T) {
	// One string holding both links separated by a newline.
	set, err := ParseLinks([]string{linkA + "\n" + linkB})
	if err != nil {
		t.Fatalf("ParseLinks blob: %v", err)
	}
	if set.Len() != 2 {
		t.Errorf("Len = %d, want 2 (newline-split blob)", set.Len())
	}
}

func TestParseLinks_Empty(t *testing.T) {
	if _, err := ParseLinks([]string{"  ", ""}); !errors.Is(err, ErrNoLinks) {
		t.Errorf("expected ErrNoLinks, got %v", err)
	}
}

func TestParseLinks_PropagatesParseError(t *testing.T) {
	if _, err := ParseLinks([]string{linkA, "vless://bad"}); err == nil {
		t.Error("expected error from malformed second link")
	}
}

func TestSingleSet(t *testing.T) {
	p, _ := ParseLink(linkA)
	set := SingleSet(p)
	if set.Len() != 1 || set.Multi() {
		t.Errorf("SingleSet should hold exactly one, non-multi profile")
	}
	if set.Primary().Host != "1.1.1.1" {
		t.Errorf("Primary host = %q", set.Primary().Host)
	}
}
