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
	if err := r.ensureSetup(ctx); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(r.cgroupDir(), "cgroup.procs"),
		[]byte(strconv.Itoa(pid)), 0o644); err != nil {
		return fmt.Errorf("add pid %d to cgroup: %w", pid, err)
	}
	r.pids.add(pid)
	return nil
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
	// child into the routed cgroup.
	pid, err := launchWithEnv(ctx, argv, nil)
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
