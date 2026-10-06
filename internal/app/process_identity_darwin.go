//go:build darwin

package app

import (
	"fmt"
	"golang.org/x/sys/unix"
)

func processIdentity(pid int) (string, bool) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", false
	}
	return fmt.Sprintf("%d:%d", kp.Proc.P_pid, kp.Proc.P_starttime.Sec*1_000_000_000+int64(kp.Proc.P_starttime.Usec)), true
}
