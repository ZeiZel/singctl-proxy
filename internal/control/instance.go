// Package control lets a second invocation of singctl discover an already-running
// instance, stream its logs and control it (stop/status). A running instance
// advertises itself in an instance.json file and listens on a Unix control
// socket; clients read the file to find the socket and log path. It performs only
// local IPC — no sing-box, no os/exec.
package control

import (
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
)

const instanceFile = "instance.json"

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

// WriteInstance writes the advertisement file (0600).
func WriteInstance(dir string, inst Instance) error {
	data, err := json.MarshalIndent(inst, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(InstancePath(dir), data, 0o600)
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
	return p.Signal(syscall.Signal(0)) == nil
}
