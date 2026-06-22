package procproxy

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// This file is the package's only OS-exec adapter (the _other.go suffix marks it
// as the portable shell-out boundary the arch import guard allows). All process
// spawning and command execution lives here so the rest of procproxy stays
// exec-free and unit-testable.

// startProcess starts argv with extraEnv appended to the current environment and
// returns the child PID, reaping it in the background so it never zombies. argv[0]
// is resolved against $PATH and, on macOS, against installed .app bundles, so a
// bare app name ("zen") works even though GUI apps aren't on $PATH.
//
// singctl runs as root (sudo). When user is set and this process is root, the
// child is dropped to that user's uid/gid and its HOME/USER env is fixed, so GUI
// apps like Zen — which refuse to run as root in a user's session — start
// correctly. When not root the child already runs as the invoking user and the
// drop is skipped.
func startProcess(ctx context.Context, argv, extraEnv []string, user *LaunchUser) (int, error) {
	bin, err := resolveExecutable(argv[0])
	if err != nil {
		return 0, fmt.Errorf("launch %s: %w", argv[0], err)
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	env := append(os.Environ(), extraEnv...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr

	if user != nil && user.Uid > 0 && os.Geteuid() == 0 {
		env = applyUserEnv(env, user)
		if user.Home != "" {
			cmd.Dir = user.Home // start in the user's home, not root's cwd
		}
		cmd.SysProcAttr = userSysProcAttr(user.Uid, user.Gid)
	}
	cmd.Env = env

	if err := cmd.Start(); err != nil {
		return 0, fmt.Errorf("launch %s: %w", argv[0], err)
	}
	pid := cmd.Process.Pid
	go func() { _ = cmd.Wait() }()
	return pid, nil
}

// resolveExecutable turns a command token into an executable path: an explicit
// path is used as-is; otherwise $PATH is searched, then (on macOS) the installed
// application bundles, so "zen" resolves to Zen.app's binary.
func resolveExecutable(name string) (string, error) {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name, nil
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	if runtime.GOOS == "darwin" {
		if p := findMacAppExe(name); p != "" {
			return p, nil
		}
		return "", fmt.Errorf("%q не найдено в $PATH и среди приложений (укажите полный путь или выберите запущенный процесс)", name)
	}
	return "", fmt.Errorf("%q: executable file not found in $PATH", name)
}

// findMacAppExe locates the executable inside a macOS .app bundle matching name
// (case-insensitive, with or without the .app suffix). Returns "" when not found.
func findMacAppExe(name string) string {
	dirs := []string{"/Applications", "/System/Applications"}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "Applications"))
	}
	want := strings.ToLower(name)
	if !strings.HasSuffix(want, ".app") {
		want += ".app"
	}
	for _, d := range dirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if strings.ToLower(e.Name()) == want {
				return macBundleExe(filepath.Join(d, e.Name()))
			}
		}
	}
	return ""
}

// macBundleExe returns the executable inside <app>/Contents/MacOS — preferring
// the entry matching the bundle name, else the first regular file.
func macBundleExe(appPath string) string {
	macos := filepath.Join(appPath, "Contents", "MacOS")
	entries, err := os.ReadDir(macos)
	if err != nil {
		return ""
	}
	base := strings.TrimSuffix(filepath.Base(appPath), ".app")
	var first string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if first == "" {
			first = e.Name()
		}
		if strings.EqualFold(e.Name(), base) {
			return filepath.Join(macos, e.Name())
		}
	}
	if first != "" {
		return filepath.Join(macos, first)
	}
	return ""
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
