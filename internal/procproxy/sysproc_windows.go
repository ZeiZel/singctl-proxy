//go:build windows

package procproxy

import "syscall"

// userSysProcAttr is a no-op on Windows: there is no sudo/root model here, so
// launched children already run as the invoking user.
func userSysProcAttr(int, int) *syscall.SysProcAttr { return nil }
