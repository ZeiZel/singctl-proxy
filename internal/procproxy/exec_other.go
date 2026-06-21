package procproxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// This file is the package's only OS-exec adapter (the _other.go suffix marks it
// as the portable shell-out boundary the arch import guard allows). All process
// spawning and command execution lives here so the rest of procproxy stays
// exec-free and unit-testable.

// startProcess starts argv with extraEnv appended to the current environment and
// returns the child PID, reaping it in the background so it never zombies.
func startProcess(ctx context.Context, argv, extraEnv []string) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), extraEnv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("launch %s: %w", argv[0], err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// runCommand runs a command to completion (drives nft/ip in the Linux router).
func runCommand(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return exec.CommandContext(ctx, args[0], args[1:]...).Run()
}

// processArgv recovers a running process's command line via ps. Quoting is not
// preserved (best-effort), so it suits simple CLI apps. Works on macOS and Linux.
func processArgv(ctx context.Context, pid int) ([]string, error) {
	out, err := exec.CommandContext(ctx, "ps", "-p", strconv.Itoa(pid), "-o", "args=").Output()
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(out))
	if line == "" {
		return nil, fmt.Errorf("pid %d not found", pid)
	}
	return strings.Fields(line), nil
}
