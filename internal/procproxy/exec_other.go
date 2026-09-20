package procproxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
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
func startProcess(ctx context.Context, argv, extraEnv []string, user *LaunchUser, sink OutputSink) (int, error) {
	bin, err := resolveExecutable(argv[0])
	if err != nil {
		return 0, fmt.Errorf("launch %s: %w", argv[0], err)
	}
	cmd := exec.CommandContext(ctx, bin, argv[1:]...)
	env := append(stripMallocStackLogging(os.Environ()), extraEnv...)

	// Wire stdio BEFORE Start() so that, when we later drop to the real user via
	// SysProcAttr.Credential, the child still inherits the pipe write-end fds.
	// With a sink we capture stdout/stderr line by line and detach stdin (the TUI
	// owns the terminal); without one we inherit the current process's stdio so
	// headless --launch is unchanged.
	var outPipe, errPipe io.ReadCloser
	if sink != nil {
		cmd.Stdin = nil
		if outPipe, err = cmd.StdoutPipe(); err != nil {
			return 0, fmt.Errorf("launch %s: %w", argv[0], err)
		}
		if errPipe, err = cmd.StderrPipe(); err != nil {
			return 0, fmt.Errorf("launch %s: %w", argv[0], err)
		}
	} else {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	}

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
	app := appLabel(bin)

	if sink != nil {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); scanStream(outPipe, sink, pid, app, "out") }()
		go func() { defer wg.Done(); scanStream(errPipe, sink, pid, app, "err") }()
		go func() {
			wg.Wait() // drain both pipes before Wait closes them
			err := cmd.Wait()
			sink.Line(OutputLine{PID: pid, App: app, Stream: "err", Text: exitText(err)})
		}()
	} else {
		go func() { _ = cmd.Wait() }()
	}
	return pid, nil
}

// scanStream reads r line by line and emits each line to sink tagged with pid/app
// and stream ("out"/"err"). The scanner buffer is raised to scannerBufMax so long
// lines (Chromium/Electron logs) are not dropped. Runs in its own goroutine.
func scanStream(r io.Reader, sink OutputSink, pid int, app, stream string) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), scannerBufMax)
	for sc.Scan() {
		sink.Line(OutputLine{PID: pid, App: app, Stream: stream, Text: sc.Text()})
	}
}

// exitText formats the synthetic "process finished" line emitted after Wait.
func exitText(err error) string {
	if err == nil {
		return "[process exited]"
	}
	return fmt.Sprintf("[process exited: %v]", err)
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
		return "", fmt.Errorf("%q not found in $PATH or among applications (specify a full path or pick a running process)", name)
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
				return MacBundleExe(filepath.Join(d, e.Name()))
			}
		}
	}
	return ""
}

// MacBundleExe returns the executable inside <app>/Contents/MacOS — preferring
// the entry matching the bundle name, else the first regular file. Exported so
// callers that already have a chosen .app path (the whole-app launch flow —
// internal/app.Executor.LaunchProxiedApp, gui/bridge's app picker) can resolve
// the inner Mach-O to exec, since the .app directory itself is not executable.
func MacBundleExe(appPath string) string {
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
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = stripMallocStackLogging(os.Environ())
	return cmd.Run()
}

// mallocStackLoggingPrefix is the family of macOS malloc-debugging env vars
// (MallocStackLogging, MallocStackLoggingNoCompact, MallocStackLoggingDirectory,
// ...) that, once set in this process's own environment (Xcode/lldb often set
// them on the daemon's launch context), get inherited by every child it spawns
// and make each one emit "MallocStackLogging: can't turn off malloc stack
// logging" — dominating the daemon's log (F2 item 6). Stripped here rather
// than at the OS level so the daemon's OWN process is unaffected either way.
const mallocStackLoggingPrefix = "MallocStackLogging"

// stripMallocStackLogging returns env with every MallocStackLogging* entry
// removed, preserving order otherwise.
func stripMallocStackLogging(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, mallocStackLoggingPrefix) {
			continue
		}
		out = append(out, kv)
	}
	return out
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
