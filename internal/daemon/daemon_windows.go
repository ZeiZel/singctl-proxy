//go:build windows

package daemon

import "errors"

// Spawn is unsupported on Windows (no setsid). This stub imports no os/exec so it
// stays clear of the OS-adapter import rule.
func Spawn(Config) error {
	return errors.New("daemon mode is not supported on Windows")
}
