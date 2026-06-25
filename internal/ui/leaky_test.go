package ui

import "testing"

func TestEditorLabelAndIsLeaky(t *testing.T) {
	leaky := map[string]bool{
		"cursor": true,
		"/Applications/Cursor.app/Contents/MacOS/Cursor": true,
		"Cursor.app": true,
		"code":       true,
		"VSCodium":   false, // only "codium" base is listed
		"codium":     true,
		"zen":        false, // Chromium browser, not a Node-extension-host editor
		"chrome":     false,
		"":           false,
		"curl":       false,
	}
	for name, want := range leaky {
		if got := isLeakyEditor(name); got != want {
			t.Errorf("isLeakyEditor(%q) = %v, want %v (label %q)", name, got, want, editorLabel(name))
		}
	}
}

func TestLeakyEditorProxied(t *testing.T) {
	cursor := []proxiedApp{{PID: 1, Name: "Cursor"}}
	plain := []proxiedApp{{PID: 2, Name: "zen"}}

	cases := []struct {
		name      string
		darwin    bool
		mode      RunMode
		proxied   []proxiedApp
		wantNudge bool
	}{
		{"macOS, cursor proxied, proxy mode → nudge", true, RunProxy, cursor, true},
		{"macOS, cursor proxied, already VPN → no nudge", true, RunVPN, cursor, false},
		{"macOS, only zen proxied → no nudge", true, RunProxy, plain, false},
		{"macOS, nothing proxied → no nudge", true, RunProxy, nil, false},
		{"Linux, cursor proxied → no nudge (cgroup catches it)", false, RunProxy, cursor, false},
	}
	for _, c := range cases {
		m := Model{isDarwin: c.darwin, mode: c.mode, proxied: c.proxied}
		if got := m.leakyEditorProxied(); got != c.wantNudge {
			t.Errorf("%s: leakyEditorProxied() = %v, want %v", c.name, got, c.wantNudge)
		}
	}
}
