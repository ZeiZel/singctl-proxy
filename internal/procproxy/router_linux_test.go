//go:build linux

package procproxy

import (
	"strings"
	"testing"
)

func joined(cmds [][]string) string {
	var b strings.Builder
	for _, c := range cmds {
		b.WriteString(strings.Join(c, " "))
		b.WriteString("\n")
	}
	return b.String()
}

func TestSetupCommands_MarkAndRoute(t *testing.T) {
	cfg := Config{Mark: 0x1c9, Table: 0x1c9, Gateway: "198.18.0.1", CgroupName: "singctl-proxy"}.withDefaults()
	out := joined(setupCommands(cfg))
	for _, want := range []string{
		"nft add table inet singctl",
		"socket cgroupv2 level 1 singctl-proxy meta mark set 0x1c9",
		"ip rule add fwmark 0x1c9 table 457",
		"ip route add default via 198.18.0.1 table 457",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("setup commands missing %q in:\n%s", want, out)
		}
	}
}

func TestTeardownReversesSetup(t *testing.T) {
	cfg := Config{}.withDefaults()
	out := joined(teardownCommands(cfg))
	for _, want := range []string{
		"ip route flush table 457",
		"ip rule del fwmark 0x1c9 table 457",
		"nft delete table inet singctl",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("teardown commands missing %q in:\n%s", want, out)
		}
	}
}

func TestMarkHex(t *testing.T) {
	if got := markHex(457); got != "0x1c9" {
		t.Errorf("markHex(457) = %q, want 0x1c9", got)
	}
}
