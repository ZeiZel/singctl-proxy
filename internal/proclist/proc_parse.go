package proclist

import (
	"strconv"
	"strings"
)

// parseProcNetPorts parses a /proc/net/tcp{,6} table into an inode→local-port
// map (all socket states). Pure, fixture-tested. Field 1 is HEXIP:HEXPORT and
// field 9 is the socket inode.
func parseProcNetPorts(data []byte) map[uint64]int {
	out := map[uint64]int{}
	for i, line := range strings.Split(string(data), "\n") {
		if i == 0 { // header
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 10 {
			continue
		}
		local := fields[1]
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
		out[inode] = int(port)
	}
	return out
}
