// Package app is the composition root's glue: the Executor builds the runtime
// from a pasted link, implements ui.Backend, applies the monitor's policy
// decisions to the manager (auto fail-closed / refresh), and pushes status to
// the UI. It is testable end-to-end on fakes (FakeCore + fake prober/routes).
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/clashapi"
	"singctl/internal/clashui"
	"singctl/internal/core"
	"singctl/internal/daemon"
	"singctl/internal/monitor"
	"singctl/internal/policy"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/runtime"
	"singctl/internal/singbox"
	"singctl/internal/types"
	"singctl/internal/ui"
	"singctl/internal/vless"
)

// pollInterval is how often the Clash API is polled for live connections and
// per-server latency.
const pollInterval = 2 * time.Second

// Executor wires the UI/monitor to the runtime.Manager. The manager is built
// lazily on StartProxy (once the link is known).
type Executor struct {
	factory core.Factory
	prober  runtime.InterfaceProber
	routes  runtime.RouteController
	notes   chan tea.Msg

	mu    sync.Mutex
	mgr   *runtime.Manager
	links []string // raw VLESS links currently loaded (priority order)

	// cfgMu guards all the tunables + the per-process router + the log file.
	// Separate from mu (which guards mgr/links) so the control socket, monitor
	// and UI can read/write config concurrently without racing or deadlocking
	// against LoadLink. Never hold cfgMu and mu at the same time.
	cfgMu       sync.Mutex
	save        func(string) error
	logPath     string
	logFile     *os.File
	ports       singbox.Ports
	clashAddr   string
	clashSecret string
	urltest     singbox.URLTestParams
	router      procproxy.Router
	routerBuilt bool
	launchUser  *procproxy.LaunchUser // real user to drop launched children to (sudo)

	pollMu     sync.Mutex
	pollCancel context.CancelFunc

	listerOnce sync.Once
	lister     proclist.Lister
}

// ListProcesses enumerates processes with network sockets so the UI can offer a
// picker for per-process routing.
func (e *Executor) ListProcesses(ctx context.Context) ([]ui.ProcInfo, error) {
	e.listerOnce.Do(func() { e.lister = proclist.NewLister() })
	procs, err := e.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]ui.ProcInfo, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, ui.ProcInfo{PID: p.PID, Name: p.Name, Ports: p.PortsString(), Children: p.Children})
	}
	return rows, nil
}

// SetLogPath redirects sing-box logs to a file (keeps them out of the TUI).
func (e *Executor) SetLogPath(path string) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if path != e.logPath && e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
	e.logPath = path
}

// SetClashAPI enables the sing-box Clash API on the given "host:port" with the
// given secret (empty addr disables it). Takes effect on the next LoadLink.
func (e *Executor) SetClashAPI(addr, secret string) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.clashAddr = addr
	e.clashSecret = secret
}

// SetURLTest tunes the multi-server failover group (probe URL / interval /
// tolerance). The zero value uses sensible defaults.
func (e *Executor) SetURLTest(u singbox.URLTestParams) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.urltest = u
}

// SetSocksPort overrides the proxy's local socks port (the http port follows
// at port+1). 0 keeps the defaults (socks 1080, http 2080). Takes effect on
// the next LoadLink.
func (e *Executor) SetSocksPort(port int) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if port == 0 {
		e.ports = singbox.Ports{}
		return
	}
	e.ports = singbox.Ports{Socks: port, HTTP: port + 1}
}

// SetLaunchUser records the real (non-root) user that processes launched/restarted
// through the proxy should run as, so GUI apps don't inherit root under sudo.
// Takes effect on the next router build.
func (e *Executor) SetLaunchUser(u *procproxy.LaunchUser) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.launchUser = u
}

func NewExecutor(f core.Factory, p runtime.InterfaceProber, r runtime.RouteController, notes chan tea.Msg) *Executor {
	return &Executor{factory: f, prober: p, routes: r, notes: notes}
}

// SetSaver registers an optional persistence hook called after a successful
// StartProxy (best-effort).
func (e *Executor) SetSaver(fn func(string) error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.save = fn
}

// --- ui.Backend ---

// LoadLink validates+remembers the link and prepares the runtime WITHOUT
// starting anything (the user explicitly enables a mode afterwards). Changing
// the link tears down any previous runtime first.
func (e *Executor) LoadLink(ctx context.Context, link string) error {
	set, err := vless.ParseLinks([]string{link})
	if err != nil {
		return err
	}
	e.stopPoller()
	if old := e.manager(); old != nil {
		_ = old.Shutdown(ctx)
	}
	// Snapshot the tunables under cfgMu (never held across mgr work / mu).
	e.cfgMu.Lock()
	builder := runtime.ProfileConfigBuilder{
		Profiles: set,
		LogPath:  e.logPath,
		Ports:    e.ports,
		ClashAPI: clashAPIConfig(e.clashAddr, e.clashSecret),
		URLTest:  e.urltest,
	}
	save := e.save
	e.cfgMu.Unlock()

	mgr := runtime.NewManager(e.factory, builder, e.prober, e.routes)
	links := make([]string, 0, set.Len())
	for _, p := range set.Profiles {
		links = append(links, p.Raw)
	}
	e.mu.Lock()
	e.mgr = mgr
	e.links = links
	e.mu.Unlock()
	if save != nil {
		_ = save(link)
	}
	return nil
}

// CurrentLinks returns the raw VLESS links currently loaded (in priority order).
func (e *Executor) CurrentLinks() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.links))
	copy(out, e.links)
	return out
}

// AddLink appends another VLESS server to the set and reloads, preserving the
// running mode so the new server joins the failover group live.
func (e *Executor) AddLink(ctx context.Context, link string) error {
	if _, err := vless.ParseLinks([]string{link}); err != nil {
		return err
	}
	prev := e.StateLabel()
	combined := append(e.CurrentLinks(), strings.TrimSpace(link))
	if err := e.LoadLink(ctx, strings.Join(combined, "\n")); err != nil {
		return err
	}
	switch prev {
	case "vpn":
		return e.EnableVPN(ctx)
	case "proxy", "suspended":
		return e.EnableProxy(ctx)
	}
	return nil
}

// clashAPIConfig returns the sing-box Clash API config, or nil if disabled.
// Pure (caller holds cfgMu and supplies the snapshot).
func clashAPIConfig(addr, secret string) *singbox.ClashAPI {
	if addr == "" {
		return nil
	}
	return &singbox.ClashAPI{ExternalController: addr, Secret: secret}
}

// EnableProxy starts (or switches to) proxy-only mode.
func (e *Executor) EnableProxy(ctx context.Context) error {
	mgr := e.manager()
	if mgr == nil {
		return errNoProxy
	}
	if mgr.State() == runtime.StateVPN {
		if err := mgr.StopForwarder(ctx); err != nil { // VPN -> proxy
			return err
		}
		e.startPoller()
		return nil
	}
	if err := mgr.StartProxy(ctx); err != nil { // off/suspended -> proxy (idempotent)
		return err
	}
	e.startPoller()
	return nil
}

// EnableVPN starts VPN mode (proxy + forwarder), ensuring the proxy is up first.
func (e *Executor) EnableVPN(ctx context.Context) error {
	mgr := e.manager()
	if mgr == nil {
		return errNoProxy
	}
	if err := mgr.StartProxy(ctx); err != nil {
		return err
	}
	if err := mgr.StartForwarder(ctx); err != nil {
		return err
	}
	e.startPoller()
	return nil
}

// Stop shuts everything down (back to OFF), keeping the loaded profile so the
// user can re-enable a mode.
func (e *Executor) Stop(ctx context.Context) error {
	e.stopPoller()
	mgr := e.manager()
	if mgr == nil {
		return nil
	}
	return mgr.Shutdown(ctx)
}

// startPoller launches the Clash API poller (idempotent; no-op when the Clash
// API is disabled). Enriched connection lines are appended to the log sink.
func (e *Executor) startPoller() {
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	if addr == "" {
		return
	}
	e.pollMu.Lock()
	defer e.pollMu.Unlock()
	if e.pollCancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.pollCancel = cancel
	poller := &clashapi.Poller{
		Client:   clashapi.NewClient(addr, secret),
		Interval: pollInterval,
		Resolve:  clashapi.NewProcessResolver(),
		Sink: clashapi.Sink{
			LogLine:     e.appendLog,
			Connections: e.pushConnections,
			Proxies:     e.pushProxies,
		},
	}
	go poller.Run(ctx)
}

// pushConnections converts the Clash API connection list into a UI message and
// sends it non-blocking (a dropped update is harmless — the next tick replaces
// it, and we must never wedge the poller on a quit UI).
func (e *Executor) pushConnections(conns []clashapi.Connection) {
	e.pushNonBlocking(ui.ConnectionsMsg{Rows: clashui.ConnRows(conns)})
}

// pushProxies extracts the failover group's per-server latency and selection
// from the Clash API /proxies map and pushes it to the UI.
func (e *Executor) pushProxies(proxies map[string]clashapi.ProxyState) {
	if msg, ok := clashui.LatencyMsg(proxies); ok {
		e.pushNonBlocking(msg)
	}
}

// pushNonBlocking sends a message to the UI notes channel without blocking; if
// no reader is ready the message is dropped.
func (e *Executor) pushNonBlocking(msg tea.Msg) {
	if e.notes == nil {
		return
	}
	select {
	case e.notes <- msg:
	default:
	}
}

// stopPoller stops the Clash API poller if running.
func (e *Executor) stopPoller() {
	e.pollMu.Lock()
	defer e.pollMu.Unlock()
	if e.pollCancel != nil {
		e.pollCancel()
		e.pollCancel = nil
	}
}

// appendLog writes one enriched connection line to the same destination as the
// sing-box log: the log file in TUI/non-interactive mode, or stdout when logs
// are streamed (headless --logs, logPath == "").
func (e *Executor) appendLog(line string) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if e.logPath == "" {
		fmt.Fprintln(os.Stdout, line)
		return
	}
	if e.logFile == nil {
		f, err := os.OpenFile(e.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		e.logFile = f // kept open for the process lifetime (no per-line fd churn)
	}
	fmt.Fprintln(e.logFile, line)
}

// --- monitor integration ---

// Loop applies monitor events until ctx is cancelled.
func (e *Executor) Loop(ctx context.Context, in <-chan monitor.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-in:
			e.Apply(ctx, ev)
		}
	}
}

// Apply enacts a policy decision on the manager and notifies the UI.
func (e *Executor) Apply(ctx context.Context, ev monitor.Event) {
	mgr := e.manager()
	var stop, refresh bool
	for _, a := range ev.Decision.Actions {
		switch a {
		case policy.ActFailClosed, policy.ActStopForwarder:
			stop = true
		case policy.ActRefreshProxy:
			refresh = true
		}
	}
	if mgr != nil {
		if stop {
			// Yield fully (forwarder + proxy down) and STAY down while Cisco is
			// present: AnyConnect aborts ("can't verify IP forwarding table
			// changes") if any process touches the routing table during its
			// connect. We resume only once Cisco disconnects (the refresh path).
			_ = mgr.SuspendForCisco(ctx)
		}
		if refresh {
			if mgr.State() == runtime.StateSuspended {
				_ = mgr.ResumeProxy(ctx) // Cisco disconnected — safe to come back
			} else {
				_ = mgr.RefreshProxy(ctx)
			}
		}
	}
	if note := noteFor(stop, refresh); note != "" {
		e.push(ctx, ui.StatusMsg{Mode: e.runMode(), Note: note})
		// The UI never initiates these (Cisco auto-suspend / reconnect), so also
		// surface them in the action log: a suspend is a warning, a resume is info.
		e.pushNonBlocking(ui.ActionMsg{Level: actionLevelFor(stop, refresh), Text: note})
	}
	e.PushDisplay(ctx, ev.NetState)
}

// PushDisplay sends the live Cisco/interface status to the UI (used as the
// monitor's per-poll callback).
func (e *Executor) PushDisplay(ctx context.Context, ns types.NetState) {
	e.push(ctx, ui.NetStateMsg{Cisco: ns.CiscoActive, PhysIface: ns.PhysicalIface})
}

// Mode maps the manager state to the policy mode (the monitor's modeFn).
func (e *Executor) Mode() policy.Mode {
	m := e.manager()
	if m != nil && m.State() == runtime.StateVPN {
		return policy.ModeVPN
	}
	return policy.ModeProxy
}

// StateLabel returns a short human label of the current runtime state, for the
// control STATUS command and instance advertisement.
func (e *Executor) StateLabel() string {
	m := e.manager()
	if m == nil {
		return "off"
	}
	switch m.State() {
	case runtime.StateVPN:
		return "vpn"
	case runtime.StateProxyOnly:
		return "proxy"
	case runtime.StateSuspended:
		return "suspended"
	default:
		return "off"
	}
}

// runMode maps the manager state to the UI's running state.
func (e *Executor) runMode() ui.RunMode {
	m := e.manager()
	if m == nil {
		return ui.RunOff
	}
	switch m.State() {
	case runtime.StateVPN:
		return ui.RunVPN
	case runtime.StateProxyOnly:
		return ui.RunProxy
	default:
		return ui.RunOff
	}
}

// --- per-process proxying (ui.Backend) ---

// proxyRunning reports whether the proxy listeners are up (required before
// routing a process through them).
func (e *Executor) proxyRunning() bool {
	m := e.manager()
	return m != nil && m.State() != runtime.StateStopped
}

// procRouter lazily builds the platform per-PID router, pinned to the proxy's
// actual local ports.
func (e *Executor) procRouter() procproxy.Router {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if !e.routerBuilt {
		socks, http := 1080, 2080
		if e.ports.Socks != 0 {
			socks, http = e.ports.Socks, e.ports.HTTP
		}
		e.router = procproxy.NewRouter(procproxy.Config{
			SocksAddr:  fmt.Sprintf("127.0.0.1:%d", socks),
			HTTPAddr:   fmt.Sprintf("127.0.0.1:%d", http),
			LaunchUser: e.launchUser,
			Output: procproxy.SinkFunc(func(l procproxy.OutputLine) {
				e.pushNonBlocking(ui.ConsoleMsg{PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text})
			}),
		})
		e.routerBuilt = true
	}
	return e.router
}

// RoutePID routes an already-running PID's traffic through the proxy.
func (e *Executor) RoutePID(ctx context.Context, pid int) error {
	if !e.proxyRunning() {
		return errProxyNotRunning
	}
	return e.procRouter().AddPID(ctx, pid)
}

// LaunchProxied starts a command with its traffic routed through the proxy.
func (e *Executor) LaunchProxied(ctx context.Context, argv []string) (int, error) {
	if !e.proxyRunning() {
		return 0, errProxyNotRunning
	}
	return e.procRouter().Launch(ctx, argv)
}

// RestartProxied terminates a running PID and relaunches it through the proxy.
func (e *Executor) RestartProxied(ctx context.Context, pid int) (int, error) {
	if !e.proxyRunning() {
		return 0, errProxyNotRunning
	}
	return e.procRouter().RestartPID(ctx, pid)
}

// CurrentSettings returns the live tunables as a ui.Settings (the inverse of
// ApplySettings) so a remote client / SETTINGS-GET can seed its form.
func (e *Executor) CurrentSettings() ui.Settings {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	return ui.Settings{
		SocksPort:        e.ports.Socks,
		ClashEnabled:     e.clashAddr != "",
		ClashAddr:        e.clashAddr,
		URLTestURL:       e.urltest.URL,
		URLTestInterval:  e.urltest.Interval,
		URLTestTolerance: e.urltest.Tolerance,
		SaveProfile:      e.save != nil,
	}
}

// ApplySettings reloads the running core with edited tunables (port, Clash API,
// urltest) and re-enables the current mode so the changes take effect live.
func (e *Executor) ApplySettings(ctx context.Context, s ui.Settings) error {
	e.SetSocksPort(s.SocksPort)
	if s.ClashEnabled {
		e.cfgMu.Lock()
		secret := e.clashSecret
		e.cfgMu.Unlock()
		if secret == "" {
			secret = randomHex()
		}
		e.SetClashAPI(s.ClashAddr, secret)
	} else {
		e.SetClashAPI("", "")
	}
	e.SetURLTest(singbox.URLTestParams{URL: s.URLTestURL, Interval: s.URLTestInterval, Tolerance: s.URLTestTolerance})
	e.resetRouter() // new port → fresh per-process router

	links := e.CurrentLinks()
	if len(links) == 0 {
		return nil
	}
	prev := e.StateLabel()
	if err := e.LoadLink(ctx, strings.Join(links, "\n")); err != nil {
		return err
	}
	switch prev {
	case "vpn":
		return e.EnableVPN(ctx)
	case "proxy", "suspended":
		return e.EnableProxy(ctx)
	}
	return nil
}

// Daemonize re-execs a detached background process that keeps the proxy running
// after this process exits, then the caller (UI) quits. The key is persisted so
// the child loads it from the profile.
func (e *Executor) Daemonize(_ context.Context) error {
	links := e.CurrentLinks()
	if len(links) == 0 {
		return errNoProxy
	}
	mode := "proxy"
	if e.StateLabel() == "vpn" {
		mode = "vpn"
	}
	e.cfgMu.Lock()
	if e.save != nil {
		_ = e.save(strings.Join(links, "\n"))
	}
	cfg := daemon.Config{
		Mode:             mode,
		Port:             e.ports.Socks,
		NoClash:          e.clashAddr == "",
		ClashAddr:        e.clashAddr,
		ClashSecret:      e.clashSecret,
		URLTestURL:       e.urltest.URL,
		URLTestInterval:  e.urltest.Interval,
		URLTestTolerance: e.urltest.Tolerance,
		LogPath:          e.logPath,
	}
	e.cfgMu.Unlock()
	return daemon.Spawn(cfg)
}

// StopDaemon is part of ui.Backend but only meaningful for a remote (attached)
// client; the local executor is the process itself, so it has no background
// instance to stop.
func (e *Executor) StopDaemon(context.Context) error {
	return fmt.Errorf("нет фонового процесса (это локальный запуск)")
}

// resetRouter tears down the per-process router so the next route uses fresh
// settings (e.g. a changed socks port).
func (e *Executor) resetRouter() {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if e.router != nil {
		_ = e.router.Cleanup()
	}
	e.router = nil
	e.routerBuilt = false
}

// randomHex returns a 128-bit hex token (Clash API secret when enabling it from
// the settings UI without one set).
func randomHex() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "singctl-local"
	}
	return hex.EncodeToString(b)
}

// Shutdown tears down both cores and any per-process routing state.
func (e *Executor) Shutdown(ctx context.Context) error {
	e.stopPoller()
	e.cfgMu.Lock()
	if e.router != nil {
		_ = e.router.Cleanup()
		e.router, e.routerBuilt = nil, false
	}
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
	e.cfgMu.Unlock()
	if m := e.manager(); m != nil {
		return m.Shutdown(ctx)
	}
	return nil
}

func (e *Executor) manager() *runtime.Manager {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.mgr
}

func (e *Executor) push(ctx context.Context, msg tea.Msg) {
	if e.notes == nil {
		return
	}
	select {
	case e.notes <- msg:
	case <-ctx.Done():
	}
}

// actionLevelFor maps a Cisco-coexistence transition to an action-log level:
// suspending (yielding to Cisco) is a warning, resuming/reconnecting is info.
func actionLevelFor(stop, refresh bool) int {
	if stop {
		return ui.ActWarn
	}
	if refresh {
		return ui.ActInfo
	}
	return ui.ActInfo
}

func noteFor(stop, refresh bool) string {
	switch {
	case stop:
		return "Cisco активен — VPN и proxy остановлены, вернёмся после отключения Cisco"
	case refresh:
		return "Cisco отключён — переподключаю proxy"
	default:
		return ""
	}
}
