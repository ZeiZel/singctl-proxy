//go:build darwin

package clashapi

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

const ownerFixture = "p10\ncTCP-A\nPTCP\nn127.0.0.1:50000->127.0.0.1:1080\np20\ncUDP-B\nPUDP\nn127.0.0.1:50000->127.0.0.1:1080\np30\ncIPv6-C\nPTCP\nn[::1]:50000->[::1]:1080\n"

func stableOwnerToken(pid int) (string, bool) { return fmt.Sprint(pid), true }
func ownerConnection(network, ip string) Connection {
	return Connection{Metadata: Metadata{Network: network, SourceIP: ip, SourcePort: "50000", DestinationIP: "192.0.2.1", DestinationPort: "443"}}
}
func TestOwnerInventoryProtocolAndIPv6(t *testing.T) {
	inventoryCalls := 0
	service := ownerInventoryService{ttl: time.Minute, inventory: func(context.Context) ([]byte, error) { inventoryCalls++; return []byte(ownerFixture), nil }, identity: stableOwnerToken}
	conns := []Connection{ownerConnection("tcp", "127.0.0.1"), ownerConnection("udp", "127.0.0.1"), ownerConnection("tcp", "::1")}
	service.enrich(context.Background(), conns)
	for i, want := range []string{"TCP-A", "UDP-B", "IPv6-C"} {
		if conns[i].Metadata.Process != want {
			t.Fatalf("connection %d owner=%q want%q", i, conns[i].Metadata.Process, want)
		}
	}
	second := []Connection{ownerConnection("tcp", "127.0.0.1")}
	service.enrich(context.Background(), second)
	if inventoryCalls != 1 || second[0].Metadata.Process != "TCP-A" {
		t.Fatal("inventory not reused across requests")
	}
	service.at = time.Now().Add(-2 * time.Minute)
	service.enrich(context.Background(), []Connection{ownerConnection("tcp", "127.0.0.1")})
	if inventoryCalls != 2 {
		t.Fatal("expired inventory not refreshed")
	}
}
func TestOwnerInventoryRevalidatesFirstFetch(t *testing.T) {
	calls := 0
	service := ownerInventoryService{ttl: time.Minute, inventory: func(context.Context) ([]byte, error) { return []byte("p10\ncold\nPTCP\nn127.0.0.1:50000\n"), nil }, identity: func(int) (string, bool) {
		calls++
		if calls == 1 {
			return "old", true
		}
		return "reused", true
	}}
	conns := []Connection{ownerConnection("tcp", "127.0.0.1")}
	service.enrich(context.Background(), conns)
	if conns[0].Metadata.Process != "" || calls != 2 {
		t.Fatalf("stale first lookup=%q calls=%d", conns[0].Metadata.Process, calls)
	}
}
func TestOwnerInventoryAmbiguityAndMissingProtocol(t *testing.T) {
	raw := "p1\ncfirst\nPTCP\nn127.0.0.1:50000\np2\ncsecond\nPTCP\nn127.0.0.1:50000\n"
	raw = strings.ReplaceAll(raw, "\\c", "\nc")
	owners := parseOwnerInventoryWithIdentity(raw, stableOwnerToken)
	if owners[ownerKey(ownerConnection("tcp", "127.0.0.1").Metadata)].name != "" {
		t.Fatal("ambiguous owner guessed")
	}
	if len(parseOwnerInventoryWithIdentity("p1\ncname\nn127.0.0.1:50000\n", stableOwnerToken)) != 0 {
		t.Fatal("protocol guessed")
	}
}
func TestOwnerInventoryBound(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < ownerInventoryLimit+10; i++ {
		fmt.Fprintf(&raw, "p%d\ncapp\nPTCP\nn127.0.0.1:%d\n", i+1, i+1)
	}
	if got := len(parseOwnerInventoryWithIdentity(raw.String(), stableOwnerToken)); got != ownerInventoryLimit {
		t.Fatalf("inventory size=%d", got)
	}
}
