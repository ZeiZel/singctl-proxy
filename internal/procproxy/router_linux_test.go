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

func TestDescendantsOf_WholeSubtree(t *testing.T) {
	// 100 -> {200, 300}; 200 -> {201, 202}; 300 -> {301}; 999 unrelated.
	ppid := map[int]int{
		200: 100, 300: 100,
		201: 200, 202: 200,
		301: 300,
		999: 1,
	}
	got := map[int]bool{}
	for _, p := range descendantsOf(100, ppid) {
		got[p] = true
	}
	for _, want := range []int{200, 300, 201, 202, 301} {
		if !got[want] {
			t.Errorf("descendantsOf(100) missing %d; got %v", want, got)
		}
	}
	if got[100] {
		t.Error("descendantsOf must exclude the root itself")
	}
	if got[999] {
		t.Error("descendantsOf included an unrelated process")
	}
}

func TestDescendantsOf_CycleSafe(t *testing.T) {
	// Pathological cycle 1->2->1 must not loop forever.
	ppid := map[int]int{2: 1, 1: 2, 3: 1}
	_ = descendantsOf(1, ppid) // must terminate
}

func TestStatPPID_Parsing(t *testing.T) {
	if got := parseStatLine("4242 (Cursor Helper (GPU)) S 4240 4242 ..."); got != 4240 {
		t.Errorf("ppid = %d, want 4240", got)
	}
}
