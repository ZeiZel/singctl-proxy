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

// App is one application as shown in the picker: a single "main" process with
// the network activity of all its helper/forked descendants folded in. Apps like
// Cursor/Chrome fork a swarm of helper processes (GPU, renderer, "Helper"); the
// picker collapses that swarm into one row so the target is easy to find, and
// routing the main PID routes the whole tree (the Linux cgroup captures
// descendants; see procproxy).
type App struct {
	PID      int   // the app's main/root process
	Name     string
	Ports    []int // ports of the whole process tree, sorted, deduped
	Children int   // number of helper processes folded under this app (0 = standalone)
}

// PortsString renders the ports as ":80 :443" (empty when none).
func (p Process) PortsString() string { return portsString(p.Ports) }

// PortsString renders the app's ports as ":80 :443" (empty when none).
func (a App) PortsString() string { return portsString(a.Ports) }

func portsString(ports []int) string {
	if len(ports) == 0 {
		return ""
	}
	parts := make([]string, len(ports))
	for i, port := range ports {
		parts[i] = ":" + strconv.Itoa(port)
	}
	return strings.Join(parts, " ")
}

// Lister enumerates applications with network activity. The real implementation
// is platform-specific (see NewLister) and does the grouping; tests exercise the
// pure parsers and groupApps directly.
type Lister interface {
	List(ctx context.Context) ([]App, error)
}

// launcherNames are process names that mark a boundary when walking up the parent
// chain: an app's helpers never climb past their launcher/shell/init.
var launcherNames = map[string]bool{
	"launchd": true, "systemd": true, "init": true, "login": true,
	"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true,
	"tmux": true, "tmux: server": true, "screen": true, "sshd": true,
	"(sd-pam)": true, "containerd-shim": true, "runc": true,
}

// helperParenSuffixes/" Helper" are stripped from a process name to derive its
// "app base", so "Cursor Helper (Renderer)" and "Cursor" compare equal. Tunable.
func appBase(name string) string {
	n := strings.TrimSpace(name)
	// Strip trailing parentheticals: "Foo Helper (GPU)" -> "Foo Helper".
	for {
		if i := strings.LastIndex(n, " ("); i >= 0 && strings.HasSuffix(n, ")") {
			n = strings.TrimSpace(n[:i])
			continue
		}
		break
	}
	n = strings.TrimSpace(strings.TrimSuffix(n, " Helper"))
	return strings.ToLower(n)
}

// sameApp reports whether two process names belong to the same application after
// stripping helper suffixes. Empty bases never match (avoids grouping unnamed
// processes together).
func sameApp(a, b string) bool {
	ba := appBase(a)
	return ba != "" && ba == appBase(b)
}

// appRoot walks up the parent chain from pid to the topmost ancestor still
// belonging to the same application, stopping at pid 0/1, a launcher/shell, or an
// unrelated executable. Robust to cycles via a visited guard.
func appRoot(pid int, ppid map[int]int, name map[int]string) int {
	root := pid
	seen := map[int]bool{pid: true}
	for {
		parent, ok := ppid[root]
		if !ok || parent == 0 || parent == 1 || seen[parent] {
			return root
		}
		if launcherNames[strings.TrimSpace(name[parent])] {
			return root
		}
		if !sameApp(name[root], name[parent]) {
			return root
		}
		seen[parent] = true
		root = parent
	}
}

// groupApps collapses networked processes into one App per application root. ppid
// and name cover ALL processes (not just networked ones) so the walk can climb
// through non-networked ancestors (the main Cursor process often has no socket of
// its own — only its helpers do). Pure; fixture-tested.
func groupApps(procs []Process, ppid map[int]int, name map[int]string) []App {
	type acc struct {
		ports []int
		kids  map[int]bool // distinct contributing PIDs other than the root
		net   bool         // root itself has network activity
	}
	byRoot := map[int]*acc{}
	var order []int
	for _, p := range procs {
		root := appRoot(p.PID, ppid, name)
		a := byRoot[root]
		if a == nil {
			a = &acc{kids: map[int]bool{}}
			byRoot[root] = a
			order = append(order, root)
		}
		a.ports = append(a.ports, p.Ports...)
		if p.PID == root {
			a.net = true
		} else {
			a.kids[p.PID] = true
		}
		// Keep a name for the root even when the root has no socket (so its
		// display name resolves below).
		if _, ok := name[root]; !ok && p.PID == root {
			name[root] = p.Name
		}
	}
	out := make([]App, 0, len(order))
	for _, root := range order {
		a := byRoot[root]
		dispName := strings.TrimSpace(name[root])
		if dispName == "" {
			dispName = "PID " + strconv.Itoa(root)
		}
		out = append(out, App{
			PID:      root,
			Name:     dispName,
			Ports:    dedupSortPorts(a.ports),
			Children: len(a.kids),
		})
	}
	sortApps(out)
	return out
}

func sortApps(as []App) {
	sort.SliceStable(as, func(i, j int) bool {
		ni, nj := strings.ToLower(as[i].Name), strings.ToLower(as[j].Name)
		if ni != nj {
			return ni < nj
		}
		return as[i].PID < as[j].PID
	})
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

// parsePSPpid parses `ps -axww -o pid=,ppid=,comm=` output into pid→ppid and
// pid→name (basename of comm) maps. comm is the executable path and may contain
// spaces, so only the first two whitespace-separated fields are split off; the
// rest of the line is the command path. Pure, fixture-tested.
func parsePSPpid(data []byte) (map[int]int, map[int]string) {
	ppid := map[int]int{}
	name := map[int]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// pid
		i := strings.IndexByte(line, ' ')
		if i < 0 {
			continue
		}
		pid, err := strconv.Atoi(line[:i])
		if err != nil {
			continue
		}
		rest := strings.TrimLeft(line[i:], " ")
		// ppid
		j := strings.IndexByte(rest, ' ')
		if j < 0 {
			continue
		}
		parent, err := strconv.Atoi(rest[:j])
		if err != nil {
			continue
		}
		comm := strings.TrimSpace(rest[j:])
		ppid[pid] = parent
		name[pid] = baseName(comm)
	}
	return ppid, name
}

// parseStatPPID reads the parent PID from a /proc/<pid>/stat line (field 4). The
// comm field (2) is wrapped in parens and may itself contain spaces and parens,
// so we key off the LAST ')' before splitting the remaining space-separated
// fields ([state, ppid, ...]). Pure, fixture-tested.
func parseStatPPID(stat string) int {
	rparen := strings.LastIndexByte(stat, ')')
	if rparen < 0 || rparen+2 >= len(stat) {
		return 0
	}
	fields := strings.Fields(stat[rparen+1:]) // [state, ppid, ...]
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

// baseName returns the last path component of a (possibly space-containing)
// executable path: "/Applications/Cursor.app/Contents/MacOS/Cursor" -> "Cursor".
func baseName(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
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
