// Package daemon detaches singctl into a background process so the proxy keeps
// running after the controlling utility exits. It re-execs the binary in
// --headless mode with setsid (a new session, no controlling terminal); the
// child is then managed via the control socket (--attach/--stop/--status). The
// VLESS key is intentionally NOT passed on the command line — the child loads it
// from the saved profile, so it never appears in the process table. The exec
// itself lives in the platform adapter files (daemon_darwin.go / daemon_other.go)
// to satisfy the architecture import guard.
package daemon

import (
	"os"
	"strconv"
)

// childEnv marks the re-exec'd child so the parent's --daemon path doesn't
// recurse (the child runs the normal --headless flow).
const childEnv = "SINGCTL_DAEMON_CHILD"

// IsChild reports whether this process is the detached daemon child.
func IsChild() bool { return os.Getenv(childEnv) == "1" }

// Config is the effective runtime configuration to hand the detached child.
type Config struct {
	Mode             string // "proxy" or "vpn"
	Port             int    // 0 = default
	NoClash          bool
	ClashAddr        string
	ClashSecret      string
	URLTestURL       string
	URLTestInterval  string
	URLTestTolerance int
	LogPath          string // child stdout/stderr destination ("" = /dev/null)
}

// BuildArgs renders the child's argv (without the program name). It mirrors the
// parent's effective settings as flags; the key is omitted on purpose. Pure, so
// it is unit-tested.
func BuildArgs(c Config) []string {
	args := []string{"--headless"}
	if c.Mode == "vpn" {
		args = append(args, "--vpn")
	} else {
		args = append(args, "--proxy")
	}
	if c.Port > 0 {
		args = append(args, "--port", strconv.Itoa(c.Port))
	}
	if c.NoClash {
		args = append(args, "--no-clash-api")
	} else {
		if c.ClashAddr != "" {
			args = append(args, "--clash-api", c.ClashAddr)
		}
		if c.ClashSecret != "" {
			args = append(args, "--clash-secret", c.ClashSecret)
		}
	}
	if c.URLTestURL != "" {
		args = append(args, "--urltest-url", c.URLTestURL)
	}
	if c.URLTestInterval != "" {
		args = append(args, "--urltest-interval", c.URLTestInterval)
	}
	if c.URLTestTolerance > 0 {
		args = append(args, "--urltest-tolerance", strconv.Itoa(c.URLTestTolerance))
	}
	return args
}
