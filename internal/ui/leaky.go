package ui

import (
	"path/filepath"
	"strings"
)

// leakyEditors are app names whose AI/agent traffic runs in Node-based
// extension-host workers (native fetch/undici). On macOS that traffic ignores
// BOTH --proxy-server (Chromium's network service only) and the proxy env vars,
// so per-app proxying cannot fully route them — only VPN (TUN) mode catches it.
// Matched on the normalised app label (see editorLabel). Kept in sync with
// procproxy.chromiumApps for the editor subset.
var leakyEditors = map[string]bool{
	"cursor": true,
	"code":   true,
	"vscode": true,
	"codium": true,
}

// editorLabel normalises an app name to its lower-cased base (sans .app/.exe),
// mirroring procproxy.appLabel so the same names match here. Pure.
func editorLabel(name string) string {
	if name == "" {
		return ""
	}
	b := strings.ToLower(filepath.Base(name))
	b = strings.TrimSuffix(b, ".app")
	b = strings.TrimSuffix(b, ".exe")
	return b
}

// isLeakyEditor reports whether name is a Node-extension-host editor whose agent
// traffic leaks past per-app proxying on macOS. Pure.
func isLeakyEditor(name string) bool { return leakyEditors[editorLabel(name)] }

// leakyEditorProxied reports whether the UI should nudge the user toward VPN:
// macOS, not already in VPN mode, and at least one currently-proxied app is a
// leaky editor (its extension-host traffic is escaping the proxy). False off
// macOS, where Linux cgroup routing already catches every child process.
func (m Model) leakyEditorProxied() bool {
	if !m.isDarwin || m.mode == RunVPN {
		return false
	}
	for _, a := range m.proxied {
		if isLeakyEditor(a.Name) {
			return true
		}
	}
	return false
}
