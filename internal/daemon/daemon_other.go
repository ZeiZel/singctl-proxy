//go:build !windows

package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// Spawn re-execs the current binary detached (new session via setsid, no
// controlling terminal) so it survives the parent's exit. stdout/stderr go to
// the log file (or /dev/null); stdin is detached. It does not wait for the child.
func Spawn(c Config) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate executable: %w", err)
	}
	cmd := exec.Command(self, BuildArgs(c)...)
	// Strip MallocStackLogging* (Xcode/lldb sometimes set it on the parent's
	// launch context): inherited into the re-exec'd daemon, it would print
	// "MallocStackLogging: can't turn off malloc stack logging" and, worse,
	// propagate into every child THAT process spawns (F2 item 6).
	cmd.Env = append(stripMallocStackLoggingEnv(os.Environ()), childEnv+"=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	out, err := daemonOutput(c.LogPath)
	if err != nil {
		return err
	}
	cmd.Stdin = nil
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon: %w", err)
	}
	if out != nil {
		_ = out.Close() // the child holds its own dup'd fd
	}
	return nil
}

// stripMallocStackLoggingEnv returns env with every MallocStackLogging* entry
// removed (see internal/procproxy's identical helper, which strips the same
// family of macOS malloc-debugging vars from launched-app children).
func stripMallocStackLoggingEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		key, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(key, "MallocStackLogging") {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// daemonOutput opens the child's stdio sink: the log file when set, else /dev/null.
func daemonOutput(logPath string) (*os.File, error) {
	path := logPath
	if path == "" {
		path = os.DevNull
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open daemon log %s: %w", path, err)
	}
	return f, nil
}
