//go:build linux

package procproxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// NewRouter returns the real Linux per-PID router (cgroup v2 + nftables fwmark +
// policy routing). It requires root and an active proxy; on Linux the marked
// traffic is routed via the forwarder TUN gateway, so VPN mode must be up.
func NewRouter(cfg Config) Router {
	return &linuxRouter{cfg: cfg.withDefaults()}
}

type linuxRouter struct {
	cfg  Config
	pids muList
	mu   muSetup
}

// cgroupDir is the cgroup v2 leaf directory holding the routed PIDs.
func (r *linuxRouter) cgroupDir() string {
	return filepath.Join(r.cfg.CgroupRoot, r.cfg.CgroupName)
}

func (r *linuxRouter) AddPID(ctx context.Context, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("invalid pid %d", pid)
	}
	if err := r.ensureSetup(ctx); err != nil {
		return err
	}
	if err := r.writeCgroupProcs(pid); err != nil {
		return fmt.Errorf("add pid %d to cgroup: %w", pid, err)
	}
	r.pids.add(pid)
	// Pull in already-running descendants too. cgroup v2 membership is inherited
	// by FUTURE forks automatically, but helper/child processes spawned before
	// this add (e.g. an already-running Cursor's renderer/GPU helpers) are still
	// in their old cgroup, so their traffic would escape the proxy. Sweep the
	// current process tree once and move the whole subtree in. Best-effort: a
	// child may exit mid-sweep, and new forks from now on join ours on their own.
	for _, child := range descendantsOf(pid, readProcPPIDs()) {
		_ = r.writeCgroupProcs(child)
	}
	return nil
}

// writeCgroupProcs moves a single PID into the routed cgroup.
func (r *linuxRouter) writeCgroupProcs(pid int) error {
	return os.WriteFile(filepath.Join(r.cgroupDir(), "cgroup.procs"),
		[]byte(strconv.Itoa(pid)), 0o644)
}

// descendantsOf returns all transitive children of root given a pid→ppid map,
// excluding root itself. Cycle-safe. Pure, so it is unit-tested directly.
func descendantsOf(root int, ppid map[int]int) []int {
	children := map[int][]int{}
	for pid, parent := range ppid {
		children[parent] = append(children[parent], pid)
	}
	var out []int
	seen := map[int]bool{root: true}
	queue := append([]int{}, children[root]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if seen[pid] {
			continue
		}
		seen[pid] = true
		out = append(out, pid)
		queue = append(queue, children[pid]...)
	}
	return out
}

// readProcPPIDs scans /proc for a pid→ppid map of every live process.
func readProcPPIDs() map[int]int {
	out := map[int]int{}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		if parent := statPPID(pid); parent > 0 {
			out[pid] = parent
		}
	}
	return out
}

// statPPID reads the parent PID from /proc/<pid>/stat (field 4). The comm field
// may contain spaces/parens, so we split after the last ')'.
func statPPID(pid int) int {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0
	}
	return parseStatLine(string(data))
}

// parseStatLine extracts the parent PID (field 4) from a /proc/<pid>/stat line.
// The comm field may contain spaces/parens, so split after the last ')'. Pure.
func parseStatLine(s string) int {
	rparen := strings.LastIndexByte(s, ')')
	if rparen < 0 || rparen+2 >= len(s) {
		return 0
	}
	fields := strings.Fields(s[rparen+1:])
	if len(fields) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(fields[1])
	return ppid
}

func (r *linuxRouter) RemovePID(_ context.Context, pid int) error {
	// Moving a PID back to the root cgroup removes it from ours.
	err := os.WriteFile(filepath.Join(r.cfg.CgroupRoot, "cgroup.procs"),
		[]byte(strconv.Itoa(pid)), 0o644)
	r.pids.remove(pid)
	if err != nil {
		return fmt.Errorf("remove pid %d from cgroup: %w", pid, err)
	}
	return nil
}

func (r *linuxRouter) Launch(ctx context.Context, argv []string) (int, error) {
	if err := r.ensureSetup(ctx); err != nil {
		return 0, err
	}
	// Launch without env (real interception handles routing), then move the
	// child into the routed cgroup. Output is still captured (sink) and the
	// Chromium preset still applies, since Chromium/Electron apps ignore the env
	// vars and the cgroup move is orthogonal.
	argv = chromiumProxyArgs(argv, r.cfg.SocksAddr)
	pid, err := launchWithEnv(ctx, argv, nil, r.cfg.LaunchUser, r.cfg.Output)
	if err != nil {
		return 0, err
	}
	if err := r.AddPID(ctx, pid); err != nil {
		return pid, err
	}
	return pid, nil
}

func (r *linuxRouter) RestartPID(ctx context.Context, pid int) (int, error) {
	return restartPID(ctx, pid, r.Launch)
}

func (r *linuxRouter) ListRouted() []int { return r.pids.list() }

// ensureSetup creates the cgroup, nftables marking rule and policy route once.
func (r *linuxRouter) ensureSetup(ctx context.Context) error {
	if r.mu.done() {
		return nil
	}
	if err := os.MkdirAll(r.cgroupDir(), 0o755); err != nil {
		return fmt.Errorf("create cgroup %s: %w", r.cgroupDir(), err)
	}
	steps := setupCommands(r.cfg)
	for _, args := range steps {
		if err := runCommand(ctx, args); err != nil {
			return fmt.Errorf("procproxy setup (%s): %w", strings.Join(args, " "), err)
		}
	}
	r.mu.markDone()
	return nil
}

func (r *linuxRouter) Cleanup() error {
	// Best-effort teardown in reverse; ignore errors from already-absent state.
	for _, args := range teardownCommands(r.cfg) {
		_ = runCommand(context.Background(), args)
	}
	_ = os.Remove(r.cgroupDir())
	return nil
}

// --- pure command builders (unit-tested) ---

// nftTable / nftChain name the table+chain we own.
const (
	nftTable = "singctl"
	nftChain = "out"
)

// markRuleArgs builds the nftables rule that marks the routed cgroup's packets.
// cgroupv2 matching uses the path relative to the cgroup mount.
func markRuleArgs(cfg Config) []string {
	return []string{"nft", "add", "rule", "inet", nftTable, nftChain,
		"socket", "cgroupv2", "level", "1", cfg.CgroupName,
		"meta", "mark", "set", markHex(cfg.Mark)}
}

// setupCommands is the ordered list of privileged commands that install the
// marking + policy route. Pure, so it is unit-tested.
func setupCommands(cfg Config) [][]string {
	return [][]string{
		{"nft", "add", "table", "inet", nftTable},
		{"nft", "add", "chain", "inet", nftTable, nftChain,
			"{ type route hook output priority mangle ; }"},
		markRuleArgs(cfg),
		{"ip", "rule", "add", "fwmark", markHex(cfg.Mark), "table", strconv.Itoa(cfg.Table)},
		{"ip", "route", "add", "default", "via", cfg.Gateway, "table", strconv.Itoa(cfg.Table)},
	}
}

// teardownCommands reverses setupCommands. Pure.
func teardownCommands(cfg Config) [][]string {
	return [][]string{
		{"ip", "route", "flush", "table", strconv.Itoa(cfg.Table)},
		{"ip", "rule", "del", "fwmark", markHex(cfg.Mark), "table", strconv.Itoa(cfg.Table)},
		{"nft", "delete", "table", "inet", nftTable},
	}
}

func markHex(mark int) string { return fmt.Sprintf("0x%x", mark) }
