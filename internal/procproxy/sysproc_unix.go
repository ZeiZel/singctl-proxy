//go:build !windows

package procproxy

import "syscall"

// userSysProcAttr returns SysProcAttr that makes the child run as uid/gid instead
// of inheriting root, in a new session (setsid) so it survives the parent and
// isn't tied to our process group. Supplementary groups are reset to just the
// primary gid so the child doesn't keep root's groups.
func userSysProcAttr(uid, gid int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Credential: &syscall.Credential{
			Uid:    uint32(uid),
			Gid:    uint32(gid),
			Groups: []uint32{uint32(gid)},
		},
		Setsid: true,
	}
}
