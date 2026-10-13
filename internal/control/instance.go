// Package control lets a second invocation of singctl discover an already-running
// instance, stream its logs and control it (stop/status). A running instance
// advertises itself in an instance.json file and listens on a Unix control
// socket; clients read the file to find the socket and log path. It performs only
// local IPC — no sing-box, no os/exec.
package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const instanceFile = "instance.json"
const lockFile = "instance.lock"

// Instance describes a running singctl process for discovery by attach/stop.
type Instance struct {
	PID           int    `json:"pid"`
	Mode          string `json:"mode"`
	LogPath       string `json:"log_path"`
	ControlSocket string `json:"control_socket"`
	ClashAPIAddr  string `json:"clash_api_addr,omitempty"`
	ClashSecret   string `json:"clash_secret,omitempty"`
	StartedAt     string `json:"started_at"`
}

// InstancePath is the advertisement file path inside the config dir.
func InstancePath(dir string) string { return filepath.Join(dir, instanceFile) }
func LockPath(dir string) string     { return filepath.Join(dir, lockFile) }
func SocketPath(dir string) string   { return filepath.Join(dir, "control.sock") }

// WriteInstance writes the advertisement file (0600).
func WriteInstance(dir string, inst Instance) error {
	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".instance.json-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, InstancePath(dir))
}

// ReadInstance reads the advertisement file.
func ReadInstance(dir string) (Instance, error) {
	var inst Instance
	data, err := os.ReadFile(InstancePath(dir))
	if err != nil {
		return inst, err
	}
	if err := json.Unmarshal(data, &inst); err != nil {
		return inst, err
	}
	return inst, nil
}

// RemoveInstance deletes the advertisement file (best-effort).
func RemoveInstance(dir string) { _ = os.Remove(InstancePath(dir)) }

// IsAlive reports whether a PID is a live process (signal 0 probe).
func IsAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// signal 0 probes existence without delivering a signal. nil means the
	// process is alive and ours; EPERM means it's alive but owned by another
	// user (the common case here: a root LaunchDaemon probed by the per-user
	// GUI/CLI) — still alive. Only ESRCH means it's truly gone.
	err = p.Signal(syscall.Signal(0))
	if err == nil || errors.Is(err, syscall.EPERM) {
		return true
	}
	return false
}

// IsCurrent verifies the advertised process through its control socket. PID
// reuse alone is not sufficient: the socket reply must carry the same PID and
// start token as the advertisement.
func IsCurrent(inst Instance) bool {
	if inst.PID <= 0 || inst.StartedAt == "" || !IsAlive(inst.PID) || inst.ControlSocket == "" {
		return false
	}
	st, err := QueryStatus(inst.ControlSocket)
	return err == nil && st.PID == inst.PID && st.StartedAt == inst.StartedAt
}

// RemoveStaleAdvertisement removes only the advertisement. Socket ownership is
// decided by Server.Start, which probes it before unlinking a stale pathname.
func RemoveStaleAdvertisement(dir string) { _ = os.Remove(InstancePath(dir)) }

// SocketProbe returns nil only for a confirmed missing or refused socket.
// All ambiguous errors preserve the pathname and fail startup safely.
func SocketProbe(path string) error {
	conn, err := net.DialTimeout("unix", path, 500*time.Millisecond)
	if err == nil {
		conn.Close()
		return fmt.Errorf("control socket has a live peer")
	}
	if errors.Is(err, syscall.ENOENT) {
		return nil
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		info, statErr := os.Lstat(path)
		if errors.Is(statErr, os.ErrNotExist) {
			return nil
		}
		if statErr == nil && info.Mode()&os.ModeSocket != 0 {
			return nil
		}
		return fmt.Errorf("refusing to remove non-socket control pathname")
	}
	return fmt.Errorf("cannot establish socket ownership: %w", err)
}

// RecoverState must be called with the lifetime lock held, before any startup
// writes. It only removes advertisements and sockets proven stale.
func RecoverState(dir string) error {
	if err := SocketProbe(SocketPath(dir)); err != nil {
		return err
	}
	for _, path := range []string{SocketPath(dir), InstancePath(dir)} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}
