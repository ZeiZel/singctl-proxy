package app

import (
	"os"
	"path/filepath"
)

func defaultWatchdogCooldownFile() string {
	base := ""
	if os.Geteuid() == 0 {
		base = "/var/db/singctl"
	} else if value, err := os.UserCacheDir(); err == nil {
		base = filepath.Join(value, "singctl")
	}
	if base == "" {
		base = filepath.Join(os.TempDir(), "singctl")
	}
	_ = os.MkdirAll(base, 0o700)
	return filepath.Join(base, "watchdog-cooldown")
}
