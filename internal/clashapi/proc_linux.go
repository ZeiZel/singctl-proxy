//go:build linux

package clashapi

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NewProcessResolver returns a function that maps a loopback source port to the
// owning process's command name by reading /proc. It is the fallback for when
// sing-box does not populate the connection's process field. Returns "" when the
// owner cannot be determined.
func NewProcessResolver() func(srcPort int) string {
	return func(srcPort int) string {
		if srcPort <= 0 {
			return ""
		}
		inode := inodeForPort(srcPort)
		if inode == 0 {
			return ""
		}
		pid := pidForInode(inode)
		if pid == 0 {
			return ""
		}
		return commForPID(pid)
	}
}

// inodeForPort returns the socket inode whose local port matches srcPort,
// searching both IPv4 and IPv6 tables.
func inodeForPort(srcPort int) uint64 {
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if inode := parseProcNetTCP(data)[srcPort]; inode != 0 {
			return inode
		}
	}
	return 0
}

// parseProcNetTCP parses a /proc/net/tcp{,6} table into a local-port→inode map.
// Pure (operates on bytes) so it is unit-tested against a fixture.
func parseProcNetTCP(data []byte) map[int]uint64 {
	out := make(map[int]uint64)
	lines := strings.Split(string(data), "\n")
	for i, line := range lines {
		if i == 0 { // header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		local := fields[1] // HEXIP:HEXPORT
		colon := strings.LastIndexByte(local, ':')
		if colon < 0 {
			continue
		}
		port, err := strconv.ParseInt(local[colon+1:], 16, 32)
		if err != nil {
			continue
		}
		inode, err := strconv.ParseUint(fields[9], 10, 64)
		if err != nil || inode == 0 {
			continue
		}
		if _, ok := out[int(port)]; !ok {
			out[int(port)] = inode
		}
	}
	return out
}

// pidForInode scans /proc/<pid>/fd for a symlink to socket:[inode].
func pidForInode(inode uint64) int {
	target := "socket:[" + strconv.FormatUint(inode, 10) + "]"
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil {
			continue
		}
		fds, err := os.ReadDir(filepath.Join("/proc", p.Name(), "fd"))
		if err != nil {
			continue
		}
		for _, fd := range fds {
			link, err := os.Readlink(filepath.Join("/proc", p.Name(), "fd", fd.Name()))
			if err == nil && link == target {
				return pid
			}
		}
	}
	return 0
}

func commForPID(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
