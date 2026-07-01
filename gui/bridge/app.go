package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"singctl/internal/control"
)

// App is the Wails-bound bridge. Each exported method becomes callable from the
// React frontend (Wails generates the TS bindings). Methods are thin adapters
// over the daemon's control socket; live data is pushed via Wails events from
// the pollers in poller.go.
type App struct {
	ctx    context.Context
	daemon *Daemon
	poll   *poller
}

// NewApp builds the bridge.
func NewApp() *App {
	return &App{daemon: NewDaemon()}
}

// Startup is called by Wails with the app context; it starts the event pollers.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.poll = newPoller(a.ctx, a.daemon)
	a.poll.start()
}

// Shutdown stops the pollers (called by Wails on exit).
func (a *App) Shutdown(context.Context) {
	if a.poll != nil {
		a.poll.stop()
	}
}

// --- status ---

// GetStatus reports whether a daemon is running and its current mode. Safe to
// call when no daemon exists (Running=false).
func (a *App) GetStatus() Status {
	inst, live := a.daemon.resolve()
	if !live {
		return Status{Running: false, Mode: "off"}
	}
	st := Status{
		Running:   true,
		PID:       inst.PID,
		Mode:      inst.Mode,
		StartedAt: inst.StartedAt,
		ClashAPI:  inst.ClashAPIAddr != "",
	}
	// Prefer the live STATUS reply (mode can change after advertisement).
	if reply, err := a.daemon.request("STATUS", ""); err == nil {
		var s control.Status
		if json.Unmarshal([]byte(reply), &s) == nil && s.Mode != "" {
			st.Mode = s.Mode
			st.PID = s.PID
			st.CiscoActive, st.ProxyBypass, st.PhysIface = s.CiscoActive, s.ProxyBypass, s.PhysIface
		}
	}
	return st
}

// --- mode ---

// SetMode switches the daemon between "off", "proxy" and "vpn".
func (a *App) SetMode(mode string) error {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "off", "proxy", "vpn":
		_, err := a.daemon.request("MODE", mode)
		return err
	}
	return fmt.Errorf("unknown mode %q", mode)
}

// --- keys ---

// GetKeys returns the loaded VLESS keys, masked for display.
func (a *App) GetKeys() ([]Key, error) {
	reply, err := a.daemon.request("KEYS-GET", "")
	if err != nil {
		return nil, err
	}
	keys := make([]Key, 0)
	for i, l := range strings.Split(reply, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			keys = append(keys, maskKey(l, i))
		}
	}
	return keys, nil
}

// AddKey appends a VLESS key to the failover group (reloads the core live).
func (a *App) AddKey(link string) error {
	_, err := a.daemon.request("KEYS-ADD", strings.TrimSpace(link))
	return err
}

// RenameKey sets the #fragment label of the key at index.
func (a *App) RenameKey(index int, name string) error {
	_, err := a.daemon.request("KEYS-RENAME", strconv.Itoa(index)+" "+strings.TrimSpace(name))
	return err
}

// DeleteKey removes the key at index (removing the last key stops the proxy).
func (a *App) DeleteKey(index int) error {
	_, err := a.daemon.request("KEYS-REMOVE", strconv.Itoa(index))
	return err
}

// --- settings ---

// GetSettings fetches the daemon's current tunables.
func (a *App) GetSettings() (Settings, error) {
	reply, err := a.daemon.request("SETTINGS-GET", "")
	if err != nil {
		return Settings{}, err
	}
	var s Settings
	if err := json.Unmarshal([]byte(reply), &s); err != nil {
		return Settings{}, fmt.Errorf("bad settings reply: %w", err)
	}
	return s, nil
}

// ApplySettings pushes edited tunables to the daemon (reloads the core live).
func (a *App) ApplySettings(s Settings) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = a.daemon.request("SETTINGS-SET", string(data))
	return err
}

// --- per-process routing (executed inside the root daemon) ---

// ListProcesses enumerates processes with network sockets for the app picker.
func (a *App) ListProcesses() ([]ProcInfo, error) {
	reply, err := a.daemon.request("PROC-LIST", "")
	if err != nil {
		return nil, err
	}
	var rows []ProcInfo
	if err := json.Unmarshal([]byte(reply), &rows); err != nil {
		return nil, fmt.Errorf("bad proc-list reply: %w", err)
	}
	return rows, nil
}

// ListRouted returns the PIDs currently routed through the proxy.
func (a *App) ListRouted() ([]int, error) {
	reply, err := a.daemon.request("PROC-LIST-ROUTED", "")
	if err != nil {
		return nil, err
	}
	var pids []int
	if err := json.Unmarshal([]byte(reply), &pids); err != nil {
		return nil, fmt.Errorf("bad routed reply: %w", err)
	}
	return pids, nil
}

// RoutePID routes an already-running process through the proxy.
func (a *App) RoutePID(pid int) error {
	_, err := a.daemon.request("PROC-ROUTE", strconv.Itoa(pid))
	return err
}

// UnroutePID stops routing a process.
func (a *App) UnroutePID(pid int) error {
	_, err := a.daemon.request("PROC-UNROUTE", strconv.Itoa(pid))
	return err
}

// KillPID terminates a proxied process.
func (a *App) KillPID(pid int) error {
	_, err := a.daemon.request("PROC-KILL", strconv.Itoa(pid))
	return err
}

// RestartPID terminates a process and relaunches it routed; returns the new PID.
func (a *App) RestartPID(pid int) (int, error) {
	reply, err := a.daemon.request("PROC-RESTART", strconv.Itoa(pid))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(reply))
}

// LaunchApp starts a command with its traffic routed through the proxy; returns
// the child PID. argv[0] is the executable, the rest are arguments.
func (a *App) LaunchApp(argv []string) (int, error) {
	data, err := json.Marshal(argv)
	if err != nil {
		return 0, err
	}
	reply, err := a.daemon.request("PROC-LAUNCH", string(data))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(reply))
}

// --- daemon lifecycle ---

// StopDaemon stops the running daemon entirely.
func (a *App) StopDaemon() error {
	_, err := a.daemon.request("STOP", "")
	return err
}
