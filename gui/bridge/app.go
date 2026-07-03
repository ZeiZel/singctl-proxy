package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"singctl/internal/control"
	"singctl/internal/license"
)

// App is the Wails-bound bridge. Each exported method becomes callable from the
// React frontend (Wails generates the TS bindings). Methods are thin adapters
// over the daemon's control socket; live data is pushed via Wails events from
// the pollers in poller.go.
type App struct {
	ctx               context.Context
	daemon            *Daemon
	poll              *poller
	licenseLoopCancel context.CancelFunc
}

// NewApp builds the bridge.
func NewApp() *App {
	return &App{daemon: NewDaemon()}
}

// Startup is called by Wails with the app context; it starts the event pollers
// and, for licensed builds, the license activation-state refresh loop.
func (a *App) Startup(ctx context.Context) {
	a.ctx = ctx
	a.poll = newPoller(a.ctx, a.daemon)
	a.poll.start()
	a.startLicenseLoop()
}

// Shutdown stops the pollers (called by Wails on exit).
func (a *App) Shutdown(context.Context) {
	if a.poll != nil {
		a.poll.stop()
	}
	if a.licenseLoopCancel != nil {
		a.licenseLoopCancel()
	}
}

// startLicenseLoop re-checks the license server once per day while the GUI
// runs (plus once immediately, so a freshly-launched GUI reflects a
// revocation/reactivation without waiting a full day) and persists the result,
// so GetLicense reflects it without the user having to reopen the app. No-op
// in unlicensed builds. Mirrors poller's start/stop lifecycle.
//
// TODO(license): this is a simple wall-clock ticker tied to process uptime
// (like cmd/singctl/main.go's licenseRefreshLoop) — a GUI left running
// continuously re-checks daily; one that's relaunched more often than that
// effectively only gets the startup check, which is the "re-checks once a day
// when reachable" requirement's weakest point for the GUI. Revisit if that
// ever matters in practice (e.g. wire a "reachability changed" hook instead of
// polling).
func (a *App) startLicenseLoop() {
	if !license.Enabled() {
		return
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.licenseLoopCancel = cancel
	go func() {
		refreshLicenseIn(ctx, a.daemon.configDir, time.Now())
		ticker := time.NewTicker(24 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshLicenseIn(ctx, a.daemon.configDir, time.Now())
			}
		}
	}()
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
			st.NetextSupported, st.NetextAvailable = s.NetextSupported, s.NetextAvailable
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

// --- whole-application routing (by bundle ID, executed inside the root daemon) ---

// ListApplications enumerates running applications for the Apps tab's
// whole-app picker, grouped by bundle ID (see app.Application). Empty when
// bundle IDs can't be resolved (only macOS resolves them today).
func (a *App) ListApplications() ([]Application, error) {
	reply, err := a.daemon.request("APP-LIST", "")
	if err != nil {
		return nil, err
	}
	rows := make([]Application, 0)
	if err := json.Unmarshal([]byte(reply), &rows); err != nil {
		return nil, fmt.Errorf("bad app-list reply: %w", err)
	}
	return rows, nil
}

// ListRoutedApps returns the bundle IDs currently routed through the proxy.
func (a *App) ListRoutedApps() ([]string, error) {
	reply, err := a.daemon.request("APP-LIST-ROUTED", "")
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0)
	if err := json.Unmarshal([]byte(reply), &ids); err != nil {
		return nil, fmt.Errorf("bad app-list-routed reply: %w", err)
	}
	return ids, nil
}

// RouteApp routes a whole application (every current PID sharing bundleID, plus
// any future one — see router_darwin.go) through the proxy.
func (a *App) RouteApp(bundleID string) error {
	_, err := a.daemon.request("APP-ROUTE", strings.TrimSpace(bundleID))
	return err
}

// UnrouteApp stops routing a whole application.
func (a *App) UnrouteApp(bundleID string) error {
	_, err := a.daemon.request("APP-UNROUTE", strings.TrimSpace(bundleID))
	return err
}

// --- persistent per-app proxy store (Apps tab: enable/disable/remove) ---
//
// Unlike ListApplications/RouteApp/UnrouteApp above (a live snapshot of
// running processes), the methods below drive the daemon's persistent
// proxied-apps store (internal/app/appstore.go), which survives daemon
// restarts and app relaunches. ListInstalledApps is the odd one out: it never
// touches the daemon at all (a local /Applications scan needs no privilege).

// ListInstalledApps enumerates installed macOS applications (from
// /Applications, /System/Applications and ~/Applications) for the "add an
// app" picker, entirely locally — no control socket round-trip.
func (a *App) ListInstalledApps() ([]InstalledApp, error) {
	return listInstalledApps()
}

// ListProxiedApps returns every app in the persistent proxied-apps store
// (enabled/disabled, with Running reflecting whether it's currently open).
func (a *App) ListProxiedApps() ([]ProxiedApp, error) {
	reply, err := a.daemon.request("APP-LIST-PROXIED", "")
	if err != nil {
		return nil, err
	}
	rows := make([]ProxiedApp, 0)
	if err := json.Unmarshal([]byte(reply), &rows); err != nil {
		return nil, fmt.Errorf("bad app-list-proxied reply: %w", err)
	}
	return rows, nil
}

// LaunchAppBundle launches an application chosen by its .app bundle path
// (e.g. from ListInstalledApps), adding it to the proxied-apps store
// (enabled); returns the child PID.
func (a *App) LaunchAppBundle(appPath string) (int, error) {
	reply, err := a.daemon.request("APP-LAUNCH", strings.TrimSpace(appPath))
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(reply))
}

// SetAppEnabled enables or disables capture of a stored app without removing
// it from the list.
func (a *App) SetAppEnabled(bundleID string, enabled bool) error {
	data, err := json.Marshal(struct {
		BundleID string `json:"bundleID"`
		Enabled  bool   `json:"enabled"`
	}{BundleID: strings.TrimSpace(bundleID), Enabled: enabled})
	if err != nil {
		return err
	}
	_, err = a.daemon.request("APP-SET-ENABLED", string(data))
	return err
}

// RemoveApp deletes an app from the proxied-apps store entirely (unrouting
// any of its PIDs currently proxied).
func (a *App) RemoveApp(bundleID string) error {
	_, err := a.daemon.request("APP-REMOVE", strings.TrimSpace(bundleID))
	return err
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
