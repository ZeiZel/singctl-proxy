//go:build darwin

package main

import (
	"os"
	"os/exec"
)

// stopSystemDaemon stops the macOS LaunchDaemon. It runs as root and so advertises
// in a different config dir than an interactive sudo run, which makes it
// unreachable over the control socket; `launchctl bootout` stops it (and prevents
// KeepAlive from relaunching it until the next load/boot). Returns false when the
// daemon isn't installed. Needs root — run under sudo.
func stopSystemDaemon() (bool, string) {
	if _, err := os.Stat(launchDaemonPlist); err != nil {
		return false, "" // not installed
	}
	if os.Geteuid() != 0 {
		return true, "системный демон установлен — остановите его под sudo: sudo singctl --stop"
	}
	// bootout returns non-zero if it was already unloaded; that's fine.
	_ = exec.Command("launchctl", "bootout", "system", launchDaemonPlist).Run()
	return true, "singctl: системный демон остановлен (launchctl bootout). " +
		"Чтобы вернуть его: make install (или перезагрузка)."
}
