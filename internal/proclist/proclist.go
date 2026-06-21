// Package proclist enumerates running processes that have network sockets,
// together with their local ports and program name, so the user can pick one to
// route through the proxy. It is platform-split (lsof on macOS, /proc on Linux)
// with pure, fixture-tested parsers; unsupported platforms return an empty list.
package proclist

import (
	"context"
	"sort"
	"strconv"
	"strings"
)

// Process is one process with network activity.
type Process struct {
	PID   int
	Name  string
	Ports []int // local socket ports (listening + connected), sorted, deduped
}

// PortsString renders the ports as ":80 :443" (empty when none).
func (p Process) PortsString() string {
	if len(p.Ports) == 0 {
		return ""
	}
	parts := make([]string, len(p.Ports))
	for i, port := range p.Ports {
		parts[i] = ":" + strconv.Itoa(port)
	}
	return strings.Join(parts, " ")
}

// Lister enumerates processes. The real implementation is platform-specific
// (see NewLister); tests use the pure parsers directly.
type Lister interface {
	List(ctx context.Context) ([]Process, error)
}

// localPortFromName extracts the local port from an lsof NAME field such as
// "127.0.0.1:54321->1.2.3.4:443" or "*:8080". Returns 0 when not parseable.
func localPortFromName(name string) int {
	// Local endpoint is the part before "->".
	local := name
	if i := strings.Index(local, "->"); i >= 0 {
		local = local[:i]
	}
	// Strip a trailing " (LISTEN)" state annotation.
	if i := strings.IndexByte(local, ' '); i >= 0 {
		local = local[:i]
	}
	colon := strings.LastIndexByte(local, ':')
	if colon < 0 {
		return 0
	}
	port, err := strconv.Atoi(local[colon+1:])
	if err != nil {
		return 0
	}
	return port
}

// parseLsofFields parses `lsof -nP -FpcPn -iTCP` field output into a process list.
// The output is a flat record stream: lines begin with a field tag (p=PID,
// c=command, n=name); a new "p" line starts a new process block. Pure.
func parseLsofFields(data []byte) []Process {
	byPID := map[int]*Process{}
	var order []int
	var cur *Process
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		tag, val := line[0], line[1:]
		switch tag {
		case 'p':
			pid, err := strconv.Atoi(val)
			if err != nil {
				cur = nil
				continue
			}
			if existing, ok := byPID[pid]; ok {
				cur = existing
			} else {
				cur = &Process{PID: pid}
				byPID[pid] = cur
				order = append(order, pid)
			}
		case 'c':
			if cur != nil {
				cur.Name = val
			}
		case 'n':
			if cur != nil {
				if port := localPortFromName(val); port != 0 {
					cur.Ports = append(cur.Ports, port)
				}
			}
		}
	}
	out := make([]Process, 0, len(order))
	for _, pid := range order {
		p := byPID[pid]
		p.Ports = dedupSortPorts(p.Ports)
		out = append(out, *p)
	}
	sortByName(out)
	return out
}

func dedupSortPorts(ports []int) []int {
	if len(ports) == 0 {
		return nil
	}
	seen := map[int]bool{}
	out := ports[:0]
	for _, p := range ports {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

// sortByName orders processes by name (case-insensitive), then PID, for a stable
// readable list.
func sortByName(ps []Process) {
	sort.SliceStable(ps, func(i, j int) bool {
		ni, nj := strings.ToLower(ps[i].Name), strings.ToLower(ps[j].Name)
		if ni != nj {
			return ni < nj
		}
		return ps[i].PID < ps[j].PID
	})
}
