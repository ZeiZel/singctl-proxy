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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"singctl/internal/clashapi"
	"singctl/internal/clashui"
	"singctl/internal/control"
	"singctl/internal/core"
	"singctl/internal/daemon"
	"singctl/internal/monitor"
	"singctl/internal/netext"
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
	introSeen   func() error
	logPath     string
	logLevel    string // sing-box log level ("" → builder default "warn")
	logFile     *os.File
	ports       singbox.Ports
	clashAddr   string
	clashSecret string
	urltest     singbox.URLTestParams
	router      procproxy.Router
	routerBuilt bool
	launchUser  *procproxy.LaunchUser // real user to drop launched children to (sudo)

	// ctrl is the Executor's OWN netext.Controller, pinned to the proxy's
	// configured local SOCKS port exactly like the router's (see controller()).
	// It is the SOLE writer of the system extension's target set
	// (RecomputeAppTargets) — router_darwin's register/deregister only do
	// PID/bundle bookkeeping and never call AddTarget/RemoveTarget, so there is
	// never more than one writer racing to flush config.json.
	ctrl      netext.Controller
	ctrlBuilt bool

	// store is the persistent, PID-independent record of proxied apps (bundle
	// ID, display name, enabled) backing the GUI's Apps tab. Loaded once at
	// construction; every mutation is followed by RecomputeAppTargets so the
	// controller's target set stays in sync with it.
	store *appStore

	pollMu     sync.Mutex
	pollCancel context.CancelFunc

	// coexistMu guards the Cisco-coexistence display state: the last observed
	// Cisco/physical-iface snapshot and the current coexistence mode. It is read
	// by the control STATUS handler and the monitor's boundFn, and written from
	// the (single) monitor apply/display path. Independent of mu/cfgMu.
	coexistMu sync.Mutex
	coexist   coexistState
	lastCisco bool
	lastPhys  string

	// netDiag* holds the last net snapshot we logged a transition for, so
	// PushDisplay (called every poll) only logs when something actually changes.
	netDiagInit bool
	netDiagCd   bool   // last logged CiscoActive
	netDiagOwns bool   // last logged CiscoOwnsDefault
	netDiagPhys string // last logged PhysicalIface
	netDiagDef  string // last logged DefaultRouteIface

	listerOnce sync.Once
	lister     proclist.Lister

	// consoleLog is a ring of recent per-app stdout/stderr lines with monotonic
	// ids, so an attached client can poll the daemon's app output (CONSOLE-POLL).
	consoleMu  sync.Mutex
	consoleLog []ConsoleEntry
	consoleSeq int
}

// consoleRingMax caps the console ring (older lines are dropped).
const consoleRingMax = 2000

// ConsoleEntry is one buffered console line tagged with a monotonic id, returned
// by ConsoleSince and marshalled over the control socket (CONSOLE-POLL).
type ConsoleEntry struct {
	ID     int    `json:"id"`
	PID    int    `json:"pid"`
	App    string `json:"app"`
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// appendConsole records a captured output line in the ring.
func (e *Executor) appendConsole(l procproxy.OutputLine) {
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	e.consoleSeq++
	e.consoleLog = append(e.consoleLog, ConsoleEntry{
		ID: e.consoleSeq, PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text,
	})
	if len(e.consoleLog) > consoleRingMax {
		e.consoleLog = e.consoleLog[len(e.consoleLog)-consoleRingMax:]
	}
}

// ConsoleSince returns buffered console entries newer than id (capped), so a
// poller advances by the last id it has seen.
func (e *Executor) ConsoleSince(id int) []ConsoleEntry {
	e.consoleMu.Lock()
	defer e.consoleMu.Unlock()
	const maxBatch = 500
	out := make([]ConsoleEntry, 0, 32)
	for _, ent := range e.consoleLog {
		if ent.ID > id {
			out = append(out, ent)
		}
	}
	if len(out) > maxBatch {
		out = out[len(out)-maxBatch:]
	}
	return out
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

// SetLogLevel sets the sing-box log level for subsequently built configs
// (""/unset → the builder's quiet "warn" default; "info" restores verbose
// per-connection logging, e.g. for --verbose). Takes effect on the next LoadLink.
func (e *Executor) SetLogLevel(level string) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.logLevel = level
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
	e := &Executor{factory: f, prober: p, routes: r, notes: notes, store: newAppStore(proxiedAppsPath)}
	_ = e.store.Load() // best-effort: a missing/corrupt store just starts empty
	return e
}

// SetSaver registers an optional persistence hook called after a successful
// StartProxy (best-effort).
func (e *Executor) SetSaver(fn func(string) error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.save = fn
}

// SetIntroHook registers the hook that records the first-run intro as seen
// (backed by the profile store). Optional; MarkIntroSeen is a no-op when unset.
func (e *Executor) SetIntroHook(fn func() error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.introSeen = fn
}

// MarkIntroSeen records that the first-run intro animation has played.
func (e *Executor) MarkIntroSeen() error {
	e.cfgMu.Lock()
	fn := e.introSeen
	e.cfgMu.Unlock()
	if fn == nil {
		return nil
	}
	return fn()
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
		LogLevel: e.logLevel,
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

// DeleteLink removes the key at index and reloads the set, preserving the
// running mode. Removing the last key stops the proxy entirely.
func (e *Executor) DeleteLink(ctx context.Context, index int) error {
	links := e.CurrentLinks()
	if index < 0 || index >= len(links) {
		return fmt.Errorf("неверный индекс ключа: %d", index)
	}
	remaining := append(links[:index:index], links[index+1:]...)
	if len(remaining) == 0 {
		// Last key removed: stop the proxy and clear the loaded set + saved profile.
		if err := e.Stop(ctx); err != nil {
			return err
		}
		if old := e.manager(); old != nil {
			_ = old.Shutdown(ctx)
		}
		e.mu.Lock()
		e.mgr = nil
		e.links = nil
		e.mu.Unlock()
		e.cfgMu.Lock()
		save := e.save
		e.cfgMu.Unlock()
		if save != nil {
			_ = save("")
		}
		return nil
	}
	return e.reloadPreservingMode(ctx, remaining)
}

// RenameLink rewrites the #fragment label of the key at index and reloads.
func (e *Executor) RenameLink(ctx context.Context, index int, name string) error {
	links := e.CurrentLinks()
	if index < 0 || index >= len(links) {
		return fmt.Errorf("неверный индекс ключа: %d", index)
	}
	renamed, err := vless.SetName(links[index], name)
	if err != nil {
		return err
	}
	links[index] = renamed
	return e.reloadPreservingMode(ctx, links)
}

// reloadPreservingMode reloads the given link set and re-enables whatever mode
// was running, so key edits take effect live. Shared by Delete/Rename.
func (e *Executor) reloadPreservingMode(ctx context.Context, links []string) error {
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
		f, err := os.OpenFile(e.logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		e.logFile = f // kept open for the process lifetime (no per-line fd churn)
	}
	fmt.Fprintln(e.logFile, line)
}

// diag writes a timestamped "[coexist]" diagnostic line to the same sink as the
// sing-box log, so Cisco-coexistence decisions (detection, bind/unbind, mode)
// are visible alongside connection logs when troubleshooting.
func (e *Executor) diag(format string, args ...any) {
	e.appendLog("[coexist " + time.Now().Format("15:04:05") + "] " + fmt.Sprintf(format, args...))
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
	var stop, refresh, bindPhys, unbind bool
	for _, a := range ev.Decision.Actions {
		switch a {
		case policy.ActFailClosed, policy.ActStopForwarder:
			stop = true
		case policy.ActRefreshProxy:
			refresh = true
		case policy.ActBindPhysical:
			bindPhys = true
		case policy.ActUnbindProxy:
			unbind = true
		}
	}
	ns := ev.NetState
	if stop || bindPhys || unbind || refresh {
		e.diag("decision: stop=%v bind=%v unbind=%v refresh=%v | cisco=%v ownsDefault=%v defRoute=%q phys=%q mode=%s",
			stop, bindPhys, unbind, refresh, ns.CiscoActive, ns.CiscoOwnsDefault, ns.DefaultRouteIface, ns.PhysicalIface, e.StateLabel())
	}
	if mgr != nil {
		if stop {
			// Yield fully (forwarder + proxy down) and STAY down while Cisco is
			// present: AnyConnect aborts ("can't verify IP forwarding table
			// changes") if any process touches the routing table during its
			// connect. This path is now reached only when Cisco appears while we
			// hold our OWN VPN tunnel (rule b); proxy-only coexistence binds
			// instead of suspending. We resume once Cisco disconnects.
			if err := mgr.SuspendForCisco(ctx); err != nil {
				e.diag("suspend-for-cisco FAILED: %v", err)
			} else {
				e.diag("suspended (fail-closed) while Cisco holds the tunnel")
			}
		}
		if bindPhys {
			// Primary coexistence: pin the proxy's egress to the physical NIC so
			// the upstream dial escapes Cisco's default route. Fall back to the
			// unbound proxy (rides Cisco) when no physical NIC can be resolved or
			// the rebind fails — BindProxyToPhysical rolls back to the working
			// proxy on error, so the fallback never leaves us proxy-less.
			iface := ns.PhysicalIface
			if iface == "" {
				iface, _ = e.prober.PhysicalDefault()
			}
			if iface == "" {
				e.diag("bind: no physical NIC resolved — staying unbound (fallback: rides default route)")
			} else if err := mgr.BindProxyToPhysical(ctx, iface); err != nil {
				e.diag("bind to %s FAILED: %v (fallback: rolled back to unbound proxy)", iface, err)
			} else {
				e.diag("bound proxy egress to %s (bypassing Cisco default route)", iface)
			}
		}
		if unbind {
			if err := mgr.UnbindProxy(ctx); err != nil { // Cisco gone — release bind + re-dial
				e.diag("unbind FAILED: %v", err)
			} else {
				e.diag("unbound proxy — re-dialing over the default route")
			}
		}
		if refresh {
			if mgr.State() == runtime.StateSuspended {
				if err := mgr.ResumeProxy(ctx); err != nil { // Cisco disconnected — safe to come back
					e.diag("resume FAILED: %v", err)
				} else {
					e.diag("resumed proxy after Cisco disconnect")
				}
			} else if err := mgr.RefreshProxy(ctx); err != nil {
				e.diag("refresh FAILED: %v", err)
			}
		}
	}
	e.updateCoexist(ctx, mgr, ev.NetState)
	e.PushDisplay(ctx, ev.NetState)
}

// PushDisplay sends the live Cisco/interface status to the UI (used as the
// monitor's per-poll callback). It also records the snapshot + derived
// coexistence state for the control STATUS handler.
func (e *Executor) PushDisplay(ctx context.Context, ns types.NetState) {
	bypass := e.coexistFor(e.manager(), ns) == coexistBypass
	e.coexistMu.Lock()
	e.lastCisco, e.lastPhys = ns.CiscoActive, ns.PhysicalIface
	changed := !e.netDiagInit || e.netDiagCd != ns.CiscoActive || e.netDiagOwns != ns.CiscoOwnsDefault ||
		e.netDiagPhys != ns.PhysicalIface || e.netDiagDef != ns.DefaultRouteIface
	if changed {
		e.netDiagInit = true
		e.netDiagCd, e.netDiagOwns = ns.CiscoActive, ns.CiscoOwnsDefault
		e.netDiagPhys, e.netDiagDef = ns.PhysicalIface, ns.DefaultRouteIface
	}
	e.coexistMu.Unlock()
	if changed {
		tunnel := "none"
		if ns.CiscoActive && ns.CiscoOwnsDefault {
			tunnel = "full-tunnel (Cisco owns default → bind physical)"
		} else if ns.CiscoActive {
			tunnel = "split-tunnel (physical owns default → no bind needed)"
		}
		e.diag("netstate: cisco=%v ownsDefault=%v defRoute=%q phys=%q → %s", ns.CiscoActive, ns.CiscoOwnsDefault, ns.DefaultRouteIface, ns.PhysicalIface, tunnel)
	}
	// netext.Available is cheap off darwin (no exec) and TTL-cached on darwin, so
	// calling it on every poll (this is the monitor's per-tick callback) is fine.
	e.push(ctx, ui.NetStateMsg{Cisco: ns.CiscoActive, PhysIface: ns.PhysicalIface, Bypass: bypass, NetextAvailable: netext.Available()})
}

// ProxyBoundToPhysical reports whether the proxy is running in proxy-only mode
// with its egress pinned to the physical NIC (the Cisco-coexistence bind). It is
// the monitor's boundFn, making the bind/unbind policy decisions level-triggered.
func (e *Executor) ProxyBoundToPhysical() bool {
	m := e.manager()
	return m != nil && m.State() == runtime.StateProxyOnly && m.BoundInterface() != ""
}

// CoexistStatus reports the live Cisco-coexistence state for the control STATUS
// command (surfaced in the CLI --status output and the GUI). bypass is true when
// the proxy egress is pinned to the physical NIC to bypass Cisco.
func (e *Executor) CoexistStatus() (ciscoActive, bypass bool, physIface string) {
	e.coexistMu.Lock()
	defer e.coexistMu.Unlock()
	return e.lastCisco, e.coexist == coexistBypass, e.lastPhys
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
				e.appendConsole(l) // ring for CONSOLE-POLL (attached clients)
				e.pushNonBlocking(ui.ConsoleMsg{PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text})
			}),
		})
		e.routerBuilt = true
	}
	return e.router
}

// controller lazily builds the Executor's own netext.Controller, pinned to
// the same local SOCKS port procRouter uses (built once, so a SetSocksPort
// call before first use takes effect — same lazy-build pattern as
// procRouter). It is the sole writer of the extension's target set; see
// RecomputeAppTargets.
func (e *Executor) controller() netext.Controller {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if !e.ctrlBuilt {
		socks := 1080
		if e.ports.Socks != 0 {
			socks = e.ports.Socks
		}
		e.ctrl = netext.New("127.0.0.1", socks)
		e.ctrlBuilt = true
	}
	return e.ctrl
}

// RoutePID routes an already-running process's traffic through the proxy. The
// platform router decides how (Linux cgroup; macOS system extension by bundle).
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

// RestartProxied terminates a running PID and relaunches it through the proxy
// (Linux); on macOS the extension captures the running app, so it just registers.
func (e *Executor) RestartProxied(ctx context.Context, pid int) (int, error) {
	if !e.proxyRunning() {
		return 0, errProxyNotRunning
	}
	return e.procRouter().RestartPID(ctx, pid)
}

// UnroutePID stops routing a PID (Linux: clean cgroup detach; macOS: stop capture).
func (e *Executor) UnroutePID(ctx context.Context, pid int) error {
	return e.procRouter().Unroute(ctx, pid)
}

// StopProxied terminates a proxied process.
func (e *Executor) StopProxied(ctx context.Context, pid int) error {
	return e.procRouter().Kill(ctx, pid)
}

// ListRouted returns the PIDs currently routed through the proxy. It does not
// build the router on demand: if nothing has been routed yet the result is empty.
// Surfaced over the control socket (PROC-LIST-ROUTED) so an unprivileged GUI can
// show the daemon's "currently proxied" list.
func (e *Executor) ListRouted() []int {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if !e.routerBuilt || e.router == nil {
		return nil
	}
	return e.router.ListRouted()
}

// --- whole-application proxying (GUI Apps tab) ---
//
// The macOS system-extension router captures traffic by application bundle ID
// (sourceAppSigningIdentifier), not by PID: one target covers every process and
// Electron helper of that app, present and future. The methods below let the
// GUI route/unroute a whole app instead of hunting down one PID, while reusing
// the very same per-PID router methods (AddPID/Unroute) that RoutePID/
// UnroutePID already drive — no routing logic is duplicated, only the app <->
// PID(s) bookkeeping is added on top.

// bundleIDForPID resolves a PID's application bundle ID (see
// netext.BundleIDForPID). Indirected through a var so tests can fake bundle
// resolution without a real running .app process; always "" off macOS.
var bundleIDForPID = netext.BundleIDForPID

// Application is one whole application for the GUI's Apps tab: the currently
// running, networked processes grouped by bundle ID (proclist already folds
// helper/forked processes under one root PID per app; this additionally folds
// multiple such roots together when they share a bundle ID). Processes outside
// a .app bundle — or on platforms without the concept — never resolve a bundle
// ID and are simply omitted; they remain reachable via the per-process
// ListProcesses/RoutePID picker.
type Application struct {
	Name     string `json:"name"`
	BundleID string `json:"bundleID"`
	Running  bool   `json:"running"`
	PIDs     []int  `json:"pids"`
}

// ListApplications enumerates running applications for the Apps tab's whole-app
// picker, grouped by bundle ID.
func (e *Executor) ListApplications(ctx context.Context) ([]Application, error) {
	rows, err := e.listProcRows(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[string]*Application{}
	var order []string
	for _, row := range rows {
		id := bundleIDForPID(row.PID)
		if id == "" {
			continue
		}
		a, ok := byID[id]
		if !ok {
			a = &Application{BundleID: id, Name: row.Name, Running: true}
			byID[id] = a
			order = append(order, id)
		}
		a.PIDs = append(a.PIDs, row.PID)
	}
	out := make([]Application, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// listProcRows lazily builds the process lister (shared with ListProcesses) and
// lists the current networked processes.
func (e *Executor) listProcRows(ctx context.Context) ([]proclist.App, error) {
	e.listerOnce.Do(func() { e.lister = proclist.NewLister() })
	return e.lister.List(ctx)
}

// bundlePIDs resolves the currently running PID(s) whose bundle ID is id
// (ordinarily just one — see Application's doc comment on grouping).
func (e *Executor) bundlePIDs(ctx context.Context, id string) ([]int, error) {
	rows, err := e.listProcRows(ctx)
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, row := range rows {
		if bundleIDForPID(row.PID) == id {
			pids = append(pids, row.PID)
		}
	}
	return pids, nil
}

// RouteApp routes a whole running application through the proxy by bundle ID:
// it resolves the app's current PID(s) and adds each through the platform
// router's AddPID. On macOS, AddPID resolves any of them back to the same
// bundle ID and captures it once for every current AND future flow of that app
// (see router_darwin.go's register/refcounting) — the same mechanism RoutePID
// already uses for a single PID. It also persists the app in the proxied-apps
// store (enabled) and recomputes the controller's target set, so it survives
// the PIDs it was just routed by disappearing — see appStore/
// RecomputeAppTargets.
func (e *Executor) RouteApp(ctx context.Context, bundleID string) error {
	if !e.proxyRunning() {
		return errProxyNotRunning
	}
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return fmt.Errorf("empty bundle id")
	}
	rows, err := e.listProcRows(ctx)
	if err != nil {
		return err
	}
	var pids []int
	name := bundleID
	for _, row := range rows {
		if bundleIDForPID(row.PID) == bundleID {
			pids = append(pids, row.PID)
			name = row.Name
		}
	}
	if len(pids) == 0 {
		return fmt.Errorf("приложение %s сейчас не запущено", bundleID)
	}
	r := e.procRouter()
	var firstErr error
	for _, pid := range pids {
		if err := r.AddPID(ctx, pid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return firstErr
	}
	if err := e.store.Upsert(bundleID, name, true); err != nil {
		return err
	}
	return e.RecomputeAppTargets()
}

// UnrouteApp stops routing every PID currently attributed to bundleID. It
// requires a router that tracks routed PIDs by bundle (procproxy.BundleRouter —
// only macOS implements it); elsewhere per-app unrouting isn't meaningful since
// ListApplications never resolves a bundle ID to route in the first place.
func (e *Executor) UnrouteApp(ctx context.Context, bundleID string) error {
	br, ok := e.procRouter().(procproxy.BundleRouter)
	if !ok {
		return fmt.Errorf("маршрутизация по приложениям не поддерживается на этой платформе")
	}
	r := e.procRouter()
	var firstErr error
	for _, pid := range br.PIDsForBundle(bundleID) {
		if err := r.Unroute(ctx, pid); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// ListRoutedApps returns the bundle IDs currently routed through the proxy
// (macOS only — empty elsewhere), for the Apps tab's "currently proxied" list.
// Like ListRouted, it does not build the router on demand.
func (e *Executor) ListRoutedApps() []string {
	e.cfgMu.Lock()
	built, r := e.routerBuilt, e.router
	e.cfgMu.Unlock()
	if !built || r == nil {
		return nil
	}
	if br, ok := r.(procproxy.BundleRouter); ok {
		return br.RoutedBundleIDs()
	}
	return nil
}

// --- persistent per-app proxy store (GUI Apps tab: enable/disable/remove) ---
//
// Unlike RouteApp/UnrouteApp above (which act on whatever PIDs happen to be
// running right now), the methods below drive a PID-independent record of
// "apps the user wants proxied" that survives daemon restarts and app
// relaunches. RecomputeAppTargets is the single place that turns this store's
// enabled set — unioned with anything currently PID-routed (ListRoutedApps) —
// into the controller's target set; every mutation below calls it so the
// system extension's capture list stays in sync.

// RecomputeAppTargets rewrites the system extension's captured-app set to
// union(enabled apps in the persistent store, apps currently routed by
// PID/bundle). It is the ONLY call site that mutates the controller's target
// set (see darwinRouter's doc comment) — callers that change enablement or
// routing must call this afterwards for it to take effect.
func (e *Executor) RecomputeAppTargets() error {
	seen := map[string]bool{}
	for _, id := range e.store.EnabledBundleIDs() {
		seen[id] = true
	}
	for _, id := range e.ListRoutedApps() {
		seen[id] = true
	}
	targets := make([]string, 0, len(seen))
	for id := range seen {
		targets = append(targets, id)
	}
	return e.controller().SetTargets(targets)
}

// ListProxiedApps returns every app in the persistent store with Running
// derived from the current process table (a store entry with no matching PID
// is simply enabled/disabled but not currently open).
func (e *Executor) ListProxiedApps(ctx context.Context) ([]ProxiedApp, error) {
	entries := e.store.List()
	out := make([]ProxiedApp, 0, len(entries))
	for _, a := range entries {
		pids, err := e.bundlePIDs(ctx, a.BundleID)
		if err != nil {
			return nil, err
		}
		a.Running = len(pids) > 0
		out = append(out, a)
	}
	return out, nil
}

// SetProxiedAppEnabled flips an app's enabled flag in the store and
// recomputes the controller's target set accordingly.
func (e *Executor) SetProxiedAppEnabled(bundleID string, enabled bool) error {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return fmt.Errorf("empty bundle id")
	}
	if err := e.store.SetEnabled(bundleID, enabled); err != nil {
		return err
	}
	return e.RecomputeAppTargets()
}

// RemoveProxiedApp drops bundleID from the store entirely: any of its PIDs
// currently routed are unrouted first (so the router's own bookkeeping
// doesn't keep reporting it via RoutedBundleIDs), then the store entry is
// deleted and the target set recomputed.
func (e *Executor) RemoveProxiedApp(ctx context.Context, bundleID string) error {
	bundleID = strings.TrimSpace(bundleID)
	if bundleID == "" {
		return fmt.Errorf("empty bundle id")
	}
	e.unrouteLiveBundle(ctx, bundleID)
	if err := e.store.Remove(bundleID); err != nil {
		return err
	}
	return e.RecomputeAppTargets()
}

// unrouteLiveBundle unroutes every PID currently attributed to bundleID,
// without building the router on demand (mirrors ListRoutedApps — a removal
// of an app that was never routed shouldn't spin up a router just to find
// nothing to do).
func (e *Executor) unrouteLiveBundle(ctx context.Context, bundleID string) {
	e.cfgMu.Lock()
	built, r := e.routerBuilt, e.router
	e.cfgMu.Unlock()
	if !built || r == nil {
		return
	}
	br, ok := r.(procproxy.BundleRouter)
	if !ok {
		return
	}
	for _, pid := range br.PIDsForBundle(bundleID) {
		_ = r.Unroute(ctx, pid)
	}
}

// LaunchProxiedApp launches an application chosen by its .app bundle path
// (e.g. from the GUI's installed-apps picker): the .app directory itself
// isn't executable, so it first resolves the inner Mach-O
// (procproxy.MacBundleExe) and launches that through the same platform router
// path as LaunchProxied. On success it persists the app in the store (enabled)
// and recomputes the controller's target set, then returns the child PID.
func (e *Executor) LaunchProxiedApp(ctx context.Context, appPath string) (int, error) {
	if !e.proxyRunning() {
		return 0, errProxyNotRunning
	}
	appPath = strings.TrimSpace(appPath)
	if appPath == "" {
		return 0, fmt.Errorf("empty app path")
	}
	exe := procproxy.MacBundleExe(appPath)
	if exe == "" {
		exe = appPath // not a .app bundle (already an executable) — try as-is
	}
	pid, err := e.procRouter().Launch(ctx, []string{exe})
	if err != nil {
		return 0, err
	}
	id := netext.BundleID(appPath)
	if id == "" {
		id = netext.BundleID(exe)
	}
	if id == "" {
		id = bundleIDForPID(pid)
	}
	if id == "" {
		return pid, fmt.Errorf("приложение запущено (PID %d), но bundle ID не определён — захват расширением не активирован", pid)
	}
	name := strings.TrimSuffix(filepath.Base(appPath), ".app")
	if err := e.store.Upsert(id, name, true); err != nil {
		return pid, err
	}
	if err := e.RecomputeAppTargets(); err != nil {
		return pid, err
	}
	return pid, nil
}

// TrafficSnapshot returns the cumulative up/down byte counters from the Clash
// API so an attached client can chart throughput by sampling deltas. Returns a
// zero snapshot (no error) when the Clash API is disabled.
func (e *Executor) TrafficSnapshot(ctx context.Context) (control.Traffic, error) {
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	if addr == "" {
		return control.Traffic{}, nil
	}
	up, down, err := clashapi.NewClient(addr, secret).Traffic(ctx)
	if err != nil {
		return control.Traffic{}, err
	}
	return control.Traffic{Up: up, Down: down}, nil
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

// Shutdown tears down both cores and any per-process routing state. It also
// clears the system extension's target set: while the daemon is down nothing
// should be captured, even though the persistent store still lists apps as
// enabled — the next startup's RecomputeAppTargets restores them from the
// store (see NewExecutor/RecomputeAppTargets).
func (e *Executor) Shutdown(ctx context.Context) error {
	e.stopPoller()
	e.cfgMu.Lock()
	if e.router != nil {
		_ = e.router.Cleanup()
		e.router, e.routerBuilt = nil, false
	}
	ctrlBuilt, ctrl := e.ctrlBuilt, e.ctrl
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
	e.cfgMu.Unlock()
	if ctrlBuilt && ctrl != nil {
		_ = ctrl.SetTargets(nil)
	}
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

// coexistState describes how the proxy currently relates to a third-party VPN
// (Cisco AnyConnect). It drives a single, deduplicated user notification on each
// transition (the bind decision is level-triggered, so without dedup the same
// note would repeat every poll).
type coexistState int

const (
	coexistNone      coexistState = iota // no Cisco — normal proxy
	coexistBypass                        // Cisco active, proxy egress pinned to the physical NIC (bypassing)
	coexistFallback                      // Cisco active, couldn't bind — proxy rides Cisco
	coexistSuspended                     // failed closed: Cisco appeared while we held our own VPN tunnel
)

// coexistFor derives the coexistence state from the live runtime + observation.
func (e *Executor) coexistFor(mgr *runtime.Manager, ns types.NetState) coexistState {
	if mgr == nil {
		return coexistNone
	}
	switch mgr.State() {
	case runtime.StateSuspended:
		return coexistSuspended
	case runtime.StateProxyOnly:
		if ns.CiscoActive {
			if mgr.BoundInterface() != "" {
				return coexistBypass
			}
			return coexistFallback
		}
	}
	return coexistNone
}

// updateCoexist recomputes the coexistence state and notifies the UI only when
// it changes, so the action log and status line get one line per transition.
func (e *Executor) updateCoexist(ctx context.Context, mgr *runtime.Manager, ns types.NetState) {
	next := e.coexistFor(mgr, ns)
	e.coexistMu.Lock()
	prev := e.coexist
	e.coexist = next
	e.coexistMu.Unlock()
	if next == prev {
		return
	}
	// Returning to "none" while Cisco is still up means the proxy was just
	// stopped, not that Cisco disconnected — don't claim a Cisco-down event.
	if next == coexistNone && ns.CiscoActive {
		return
	}
	note, level := coexistNote(next, ns.PhysicalIface)
	if note == "" {
		return
	}
	e.push(ctx, ui.StatusMsg{Mode: e.runMode(), Note: note})
	// The UI never initiates these (driven by Cisco connect/disconnect), so also
	// surface them in the action log.
	e.pushNonBlocking(ui.ActionMsg{Level: level, Text: note})
}

// coexistNote returns the user-facing message + action-log level for a
// coexistence state (empty for the steady "no Cisco" state).
func coexistNote(s coexistState, physIface string) (string, int) {
	switch s {
	case coexistBypass:
		iface := physIface
		if iface == "" {
			iface = "физический интерфейс"
		}
		return "Cisco активен — proxy работает в обход Cisco (egress через " + iface + ")", ui.ActOk
	case coexistFallback:
		return "Cisco активен — не удалось привязать proxy к физическому интерфейсу, трафик идёт через Cisco (fallback)", ui.ActWarn
	case coexistSuspended:
		return "Cisco активен — наш VPN остановлен (fail-closed), вернёмся после отключения Cisco", ui.ActWarn
	case coexistNone:
		return "Cisco отключён — proxy вернулся на основной маршрут", ui.ActInfo
	default:
		return "", ui.ActInfo
	}
}
