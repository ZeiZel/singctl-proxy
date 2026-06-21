package main

import "testing"

func TestRequireRoot(t *testing.T) {
	if err := requireRoot("darwin", 0); err != nil {
		t.Errorf("euid 0 (root) must be allowed, got %v", err)
	}
	if err := requireRoot("darwin", 501); err == nil {
		t.Error("non-root euid must be rejected")
	}
	if err := requireRoot("linux", 501); err == nil {
		t.Error("non-root euid must be rejected on linux too")
	}
	// Windows has no euid (os.Geteuid() == -1); the check must not lock the
	// binary out there.
	if err := requireRoot("windows", -1); err != nil {
		t.Errorf("windows must skip the euid check, got %v", err)
	}
}

func TestDecideStartup(t *testing.T) {
	cases := []struct {
		name                           string
		alive, headless, daemon, child bool
		want                           startupAction
	}{
		{"no instance → local", false, false, false, false, actLocal},
		{"alive + TUI → remote TUI", true, false, false, false, actRemoteTUI},
		{"alive + headless → remote headless", true, true, false, false, actRemoteHeadless},
		{"--daemon forces local", true, false, true, false, actLocal},
		{"daemon child forces local", true, false, false, true, actLocal},
		{"no instance + headless → local", false, true, false, false, actLocal},
	}
	for _, tc := range cases {
		if got := decideStartup(tc.alive, tc.headless, tc.daemon, tc.child); got != tc.want {
			t.Errorf("%s: decideStartup = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestModeFromLabel(t *testing.T) {
	if modeFromLabel("vpn") != 2 || modeFromLabel("proxy") != 1 || modeFromLabel("off") != 0 {
		t.Error("modeFromLabel mapping wrong")
	}
}
