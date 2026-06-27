// Package bridge connects the Wails GUI to a running singctl daemon. The GUI is
// unprivileged: it never links sing-box and never touches the kernel. Every
// action goes to the root daemon over its Unix control socket (mode, keys,
// settings, per-process routing) and observability (connections, latency,
// traffic) is read from the daemon's loopback Clash API. This mirrors how the
// terminal UI's remote backend drives a daemon, but emits Wails events instead
// of Bubble Tea messages.
package bridge

import (
	"os"
	"path/filepath"
	"sync"

	"singctl/internal/control"
)

// Daemon resolves and talks to the running singctl instance advertised under the
// user's config dir (~/.config/singctl/instance.json). It is safe for concurrent
// use; the resolved instance is cached and refreshed on demand.
type Daemon struct {
	configDir string

	mu   sync.RWMutex
	inst control.Instance
	live bool
}

// NewDaemon builds a Daemon pointed at the current user's config dir. The GUI
// runs as the normal user, so the dir is simply ~/.config/singctl (the same
// place the daemon advertises itself when launched via the LaunchDaemon/systemd
// unit, which pins HOME to this user).
func NewDaemon() *Daemon {
	return &Daemon{configDir: configDir()}
}

// configDir returns ~/.config/singctl for the user running the GUI.
func configDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		if home = os.Getenv("HOME"); home == "" {
			return ""
		}
	}
	return filepath.Join(home, ".config", "singctl")
}

// resolve re-reads instance.json and verifies the PID is alive, caching the
// result. It returns the instance and whether a live daemon was found.
func (d *Daemon) resolve() (control.Instance, bool) {
	inst, err := control.ReadInstance(d.configDir)
	live := err == nil && inst.PID != 0 && control.IsAlive(inst.PID)
	d.mu.Lock()
	d.inst, d.live = inst, live
	d.mu.Unlock()
	return inst, live
}

// instance returns the cached instance, resolving once if never resolved.
func (d *Daemon) instance() (control.Instance, bool) {
	d.mu.RLock()
	inst, live := d.inst, d.live
	d.mu.RUnlock()
	if inst.ControlSocket == "" {
		return d.resolve()
	}
	return inst, live
}

// socket returns the control-socket path of the live daemon, resolving as
// needed. ok is false when no daemon is running.
func (d *Daemon) socket() (string, bool) {
	inst, live := d.instance()
	if !live || inst.ControlSocket == "" {
		// One more fresh attempt in case the daemon just (re)started.
		inst, live = d.resolve()
	}
	if !live || inst.ControlSocket == "" {
		return "", false
	}
	return inst.ControlSocket, true
}

// request sends a control command to the live daemon, refreshing the instance
// first so a restarted daemon (new socket) is picked up.
func (d *Daemon) request(cmd, arg string) (string, error) {
	sock, ok := d.socket()
	if !ok {
		return "", errNoDaemon
	}
	return control.Request(sock, cmd, arg)
}
