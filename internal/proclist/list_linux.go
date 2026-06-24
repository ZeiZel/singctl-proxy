//go:build linux

package proclist

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NewLister returns the Linux process lister backed by /proc (no external
// binaries needed).
func NewLister() Lister { return procLister{} }

type procLister struct{}

func (procLister) List(_ context.Context) ([]App, error) {
	// inode → local port for every TCP socket.
	inodePort := map[uint64]int{}
	for _, f := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		if data, err := os.ReadFile(f); err == nil {
			for inode, port := range parseProcNetPorts(data) {
				inodePort[inode] = port
			}
		}
	}

	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	// One pass over /proc: build pid→ppid and pid→name for ALL processes (needed
	// to climb the parent chain past non-networked ancestors), and collect the
	// processes that actually have network sockets.
	ppid := map[int]int{}
	name := map[int]string{}
	var procs []Process
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		nm, parent := commForPID(pid), ppidForPID(pid)
		name[pid] = nm
		ppid[pid] = parent
		ports := portsForPID(pid, inodePort)
		if len(ports) == 0 {
			continue // only processes with network sockets contribute ports
		}
		procs = append(procs, Process{PID: pid, Name: nm, Ports: dedupSortPorts(ports)})
	}
	return groupApps(procs, ppid, name), nil
}

// ppidForPID reads the parent PID from /proc/<pid>/stat (field 4). The comm field
// (2) is wrapped in parens and may contain spaces/parens, so we key off the last
// ')' before splitting. Returns 0 when unavailable.
func ppidForPID(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	return parseStatPPID(string(data))
}

// portsForPID scans /proc/<pid>/fd for socket inodes and maps them to ports.
func portsForPID(pid int, inodePort map[uint64]int) []int {
	fdDir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	fds, err := os.ReadDir(fdDir)
	if err != nil {
		return nil
	}
	var ports []int
	for _, fd := range fds {
		link, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
		if err != nil || !strings.HasPrefix(link, "socket:[") {
			continue
		}
		inode, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(link, "socket:["), "]"), 10, 64)
		if err != nil {
			continue
		}
		if port, ok := inodePort[inode]; ok {
			ports = append(ports, port)
		}
	}
	return ports
}

func commForPID(pid int) string {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "comm"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
