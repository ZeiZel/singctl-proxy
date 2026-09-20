// Package app is the composition root's glue: the Executor builds the runtime
// from a pasted link, applies the monitor's policy decisions to the manager
// (auto fail-closed / refresh), and pushes status notes (see internal/notify)
// to a channel headless mode drains and prints. It is testable end-to-end on
// fakes (FakeCore + fake prober/routes).
package app

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"singctl/internal/clashapi"
	"singctl/internal/clashui"
	"singctl/internal/control"
	"singctl/internal/core"
	"singctl/internal/daemon"
	"singctl/internal/firewall"
	"singctl/internal/monitor"
	"singctl/internal/netext"
	"singctl/internal/notify"
	"singctl/internal/policy"
	"singctl/internal/proclist"
	"singctl/internal/procproxy"
	"singctl/internal/protocol"
	"singctl/internal/runtime"
	"singctl/internal/singbox"
	"singctl/internal/sub"
	"singctl/internal/sysproxy"
	"singctl/internal/types"
)

// pollInterval is how often the Clash API is polled for live connections and
// per-server latency.
const pollInterval = 2 * time.Second

// Executor wires the UI/monitor to the runtime.Manager. The manager is built
// lazily on StartProxy (once the link is known).
type Executor struct {
	factory core.Factory
	// registry is the protocol registry every key is parsed and rendered
	// through. Injected at construction (see NewExecutor) — never a
	// package-level singleton, so the composition root (cmd/singctl/main.go)
	// is the one place that decides which protocols are supported.
	registry *protocol.Registry
	prober   runtime.InterfaceProber
	routes   runtime.RouteController
	notes    chan any

	mu    sync.Mutex
	mgr   *runtime.Manager
	links []string // EFFECTIVE share links currently loaded (priority order)

	// subMu guards the two inputs the effective link set is computed from:
	// the keys the user entered by hand, and the subscriptions singctl fetches
	// on their behalf. It is independent of mu and cfgMu and, like them, is
	// never held while another of the three is taken — effectiveLinks()
	// snapshots under subMu and releases before the manager work under mu.
	subMu    sync.Mutex
	manual   []string           // keys the user added themselves
	subs     []sub.Subscription // configured subscriptions + their cached links
	fetcher  sub.Fetcher        // nil in clients that must not hit the network
	saveSubs func([]sub.Subscription) error

	// cfgMu guards all the tunables + the per-process router + the log file.
	// Separate from mu (which guards mgr/links) so the control socket, monitor
	// and UI can read/write config concurrently without racing or deadlocking
	// against LoadLink. Never hold cfgMu and mu at the same time.
	cfgMu          sync.Mutex
	save           func(string) error
	introSeen      func() error
	autostartMode  string             // "" behaves as "off" — see AutostartMode
	saveAutostart  func(string) error // persistence hook for SetAutostartMode (F2 item 2)
	saveSysproxy   func([]byte) error // persistence hook for SysProxySet/-Import (F1b item 1)
	logPath        string
	logLevel       string // sing-box log level ("" → builder default "warn")
	logFile        *os.File
	logWriter      *bufio.Writer
	logFlushCancel context.CancelFunc
	ports          singbox.Ports
	clashAddr      string
	clashSecret    string
	urltest        singbox.URLTestParams
	router         procproxy.Router
	routerBuilt    bool
	launchUser     *procproxy.LaunchUser // real user to drop launched children to (sudo)

	// ctrl is the Executor's OWN netext.Controller, pinned to the proxy's
	// configured local SOCKS port exactly like the router's (see controller()).
	// It is the SOLE writer of the system extension's target set
	// (RecomputeAppTargets) — router_darwin's register/deregister only do
	// PID/bundle bookkeeping and never call AddTarget/RemoveTarget, so there is
	// never more than one writer racing to flush config.json.
	ctrl      netext.Controller
	ctrlBuilt bool

	// sysProxy is the Executor's own sysproxy.Manager for the macOS
	// system-proxy (PAC) toggle — see internal/sysproxy. Built lazily (same
	// pattern as controller()); a freshly built Manager starts in the inert
	// ModeOff config and touches no system state until SysProxySet/-Import is
	// called (see internal/sysproxy's package doc SAFETY note — this Executor
	// never calls those on its own either).
	sysProxy      *sysproxy.Manager
	sysProxyBuilt bool

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

	// fwMu guards the persisted firewall rule set (F6 item 5 in
	// docs/v2-spec.md). Independent of cfgMu/mu/subMu, like every other
	// persisted-state mutex here — never held across manager work.
	fwMu         sync.Mutex
	fwRules      []firewall.Rule
	saveFirewall func([]firewall.Rule) error

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
func (e *Executor) ListProcesses(ctx context.Context) ([]notify.ProcInfo, error) {
	e.listerOnce.Do(func() { e.lister = proclist.NewLister() })
	procs, err := e.lister.List(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]notify.ProcInfo, 0, len(procs))
	for _, p := range procs {
		rows = append(rows, notify.ProcInfo{PID: p.PID, Name: p.Name, Ports: p.PortsString(), Children: p.Children})
	}
	return rows, nil
}

// SetLogPath redirects sing-box logs to a file (keeps them out of the TUI).
func (e *Executor) SetLogPath(path string) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if path != e.logPath {
		e.closeLogLocked()
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

func NewExecutor(f core.Factory, reg *protocol.Registry, p runtime.InterfaceProber, r runtime.RouteController, notes chan any) *Executor {
	e := &Executor{factory: f, registry: reg, prober: p, routes: r, notes: notes, store: newAppStore(proxiedAppsPath)}
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

// SetAutostartSaver registers the persistence hook SetAutostartMode calls
// (best-effort) after changing the mode — the profile.Store-backed
// implementation in cmd/singctl/main.go, mirroring SetSaver.
func (e *Executor) SetAutostartSaver(fn func(string) error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.saveAutostart = fn
}

// AutostartMode returns the persisted autostart mode ("off" by default — F2
// item 2: a fresh install, or one from before F2, must come up idle rather
// than re-deriving an old --vpn flag).
func (e *Executor) AutostartMode() string {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if e.autostartMode == "" {
		return "off"
	}
	return e.autostartMode
}

// SetAutostartMode validates and records mode ("off"|"proxy"|"vpn"),
// persisting it via the saver registered with SetAutostartSaver (best-effort:
// a persistence failure is reported to the caller — typically SETTINGS-SET —
// but never prevents the in-memory value from taking effect for the rest of
// this run). It does NOT itself change what is currently running — see
// ApplyAutostart, which is what the daemon calls once at startup.
func (e *Executor) SetAutostartMode(mode string) error {
	switch mode {
	case "off", "proxy", "vpn":
	default:
		return fmt.Errorf("unknown autostart mode %q (want off, proxy, or vpn)", mode)
	}
	e.cfgMu.Lock()
	e.autostartMode = mode
	save := e.saveAutostart
	e.cfgMu.Unlock()
	if save != nil {
		return save(mode)
	}
	return nil
}

// ApplyAutostart enables the persisted autostart mode (a no-op for the
// default "off"), meant to be called once the daemon is otherwise up — after
// the control socket, monitor and log sink are already live, so a GUI
// attaching mid-startup sees a responsive daemon regardless of whether this
// succeeds (F2 item 2).
//
// It is ALWAYS best-effort at the state level: if enabling the mode fails
// (the reported bug — VPN failing with "no physical interface detected" —
// is exactly this case), it forces a full Stop so the daemon is left
// running in "off" rather than some partially-applied state, and returns the
// original error for the caller to log. It never panics or exits — see F2
// item 3 and cmd/singctl/main.go's runHeadless, which is what makes this
// safe to call unconditionally from a KeepAlive-restarted LaunchDaemon.
func (e *Executor) ApplyAutostart(ctx context.Context) error {
	mode := e.AutostartMode()
	var err error
	switch mode {
	case "proxy":
		err = e.EnableProxy(ctx)
	case "vpn":
		err = e.EnableVPN(ctx)
	default:
		return nil
	}
	if err != nil {
		_ = e.Stop(ctx) // never leave a half-applied mode running — fall back to fully off
		return fmt.Errorf("apply autostart mode %q: %w", mode, err)
	}
	return nil
}

// --- Backend port (see internal/app.Executor's methods) ---

// LoadLink validates+remembers the MANUAL key set and prepares the runtime
// WITHOUT starting anything (the user explicitly enables a mode afterwards).
// Changing the set tears down any previous runtime first.
//
// Manual keys are only one of the two inputs: whatever the configured
// subscriptions last returned is merged in on top (see effectiveLinks). Only
// the manual half is persisted to profile.txt — subscription servers belong to
// the subscription and are re-fetched, never hand-edited.
func (e *Executor) LoadLink(ctx context.Context, raw string) error {
	profiles, err := e.registry.ParseAll([]string{raw})
	if err != nil {
		return err
	}
	manual := make([]string, 0, len(profiles))
	for _, p := range profiles {
		manual = append(manual, p.Raw)
	}
	e.subMu.Lock()
	e.manual = manual
	e.subMu.Unlock()
	return e.applyEffective(ctx)
}

// applyEffective rebuilds the runtime from manual keys + subscription keys. It
// is the single place that swaps the manager, so every mutation path (manual
// edit, subscription refresh, startup restore) converges here.
func (e *Executor) applyEffective(ctx context.Context) error {
	effective := e.effectiveLinks()
	if len(effective) == 0 {
		return e.clearLinks(ctx)
	}
	profiles, err := e.registry.ParseAll(effective)
	if err != nil {
		return err
	}
	e.stopPoller()
	if old := e.manager(); old != nil {
		_ = old.Shutdown(ctx)
	}
	// Snapshot the tunables under cfgMu (never held across mgr work / mu).
	e.cfgMu.Lock()
	builder := firewallConfigBuilder{
		registry: e.registry,
		profiles: profiles,
		logPath:  e.logPath,
		logLevel: e.logLevel,
		ports:    e.ports,
		clashAPI: clashAPIConfig(e.clashAddr, e.clashSecret),
		urltest:  e.urltest,
		firewall: e.FirewallList(),
	}
	save := e.save
	e.cfgMu.Unlock()

	mgr := runtime.NewManager(e.factory, builder, e.prober, e.routes)
	links := make([]string, 0, len(profiles))
	for _, p := range profiles {
		links = append(links, p.Raw)
	}
	e.mu.Lock()
	e.mgr = mgr
	e.links = links
	e.mu.Unlock()
	if save != nil {
		_ = save(strings.Join(e.manualLinks(), "\n"))
	}
	return nil
}

// CurrentLinks returns the raw share links currently loaded (in priority order).
func (e *Executor) CurrentLinks() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, len(e.links))
	copy(out, e.links)
	return out
}

// AddLink appends another server to the set and reloads, preserving the
// running mode so the new server joins the failover group live.
func (e *Executor) AddLink(ctx context.Context, raw string) error {
	if _, err := e.registry.ParseAll([]string{raw}); err != nil {
		return err
	}
	prev := e.StateLabel()
	combined := append(e.manualLinks(), strings.TrimSpace(raw))
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
		return fmt.Errorf("invalid key index: %d", index)
	}
	if owner, owned := e.subscriptionOwning(links[index]); owned {
		return fmt.Errorf("this server comes from subscription %q; remove the subscription instead — a refresh would bring the server straight back", owner)
	}
	manual := e.manualLinks()
	remaining := make([]string, 0, len(manual))
	for _, l := range manual {
		if strings.TrimSpace(l) != strings.TrimSpace(links[index]) {
			remaining = append(remaining, l)
		}
	}
	e.subMu.Lock()
	e.manual = remaining
	e.subMu.Unlock()
	return e.applyPreservingMode(ctx)
}

// RenameLink rewrites the #fragment label of the key at index and reloads.
func (e *Executor) RenameLink(ctx context.Context, index int, name string) error {
	links := e.CurrentLinks()
	if index < 0 || index >= len(links) {
		return fmt.Errorf("invalid key index: %d", index)
	}
	if owner, owned := e.subscriptionOwning(links[index]); owned {
		return fmt.Errorf("this server comes from subscription %q and is named by the panel; a refresh would overwrite the new name", owner)
	}
	renamed, err := e.registry.SetLabel(links[index], name)
	if err != nil {
		return err
	}
	manual := e.manualLinks()
	for i, l := range manual {
		if strings.TrimSpace(l) == strings.TrimSpace(links[index]) {
			manual[i] = renamed
			break
		}
	}
	e.subMu.Lock()
	e.manual = manual
	e.subMu.Unlock()
	return e.applyPreservingMode(ctx)
}

// clashAPIConfig returns the sing-box Clash API config, or nil if disabled.
// Pure (caller holds cfgMu and supplies the snapshot).
func clashAPIConfig(addr, secret string) *singbox.ClashAPI {
	if addr == "" {
		return nil
	}
	return &singbox.ClashAPI{ExternalController: addr, Secret: secret}
}

// firewallConfigBuilder produces sing-box JSON for the two instances,
// threading the persisted firewall rule set (F6 item 5 in docs/v2-spec.md)
// into the proxy config's route rules via singbox.ProxyOpts.Firewall. It
// carries the exact same fields as runtime.ProfileConfigBuilder plus
// firewall, and satisfies runtime.ConfigBuilder structurally (Go interfaces
// need no explicit implements) — so it can stand in for
// runtime.ProfileConfigBuilder in applyEffective without this feature
// touching internal/runtime/configbuilder.go, a file this task owns no
// permission to change (see the FILE OWNERSHIP note for F6). applyLog below
// intentionally duplicates runtime.ProfileConfigBuilder.applyLog's few lines
// for the same reason: that method is unexported in another package.
type firewallConfigBuilder struct {
	registry *protocol.Registry
	profiles []protocol.Profile
	logPath  string
	logLevel string
	ports    singbox.Ports
	clashAPI *singbox.ClashAPI
	urltest  singbox.URLTestParams
	firewall []firewall.Rule
}

func (b firewallConfigBuilder) ProxyConfig(physIface string) ([]byte, error) {
	cfg, err := singbox.GenerateProxyConfigOpts(b.registry, b.profiles, singbox.ProxyOpts{
		PhysIface: physIface,
		Ports:     b.ports,
		ClashAPI:  b.clashAPI,
		URLTest:   b.urltest,
		Firewall:  b.firewall,
	})
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

func (b firewallConfigBuilder) ForwarderConfig() ([]byte, error) {
	cfg, err := singbox.GenerateForwarderConfigSet(b.registry, b.profiles, b.ports)
	if err != nil {
		return nil, err
	}
	b.applyLog(&cfg)
	return singbox.MarshalIndented(cfg)
}

// applyLog mirrors runtime.ProfileConfigBuilder.applyLog exactly (see this
// type's doc comment for why it is duplicated rather than shared).
func (b firewallConfigBuilder) applyLog(cfg *singbox.Config) {
	if cfg.Log == nil {
		return
	}
	if b.logPath != "" {
		cfg.Log.Output = b.logPath
	}
	level := b.logLevel
	if level == "" {
		level = "warn"
	}
	cfg.Log.Level = level
}

// --- Live connections (F6 in docs/v2-spec.md) ---

// Connections builds the CONNECTIONS control command's payload: the live
// Clash API connection table, per-app/per-destination aggregates, and an
// explicit diagnosis when the table is empty (F6 items 1-3) — see
// clashapi.Connections, which is the single place that decides between
// no_mode/api_disabled/api_unreachable/idle/active so this and whatever
// drives the Dashboard's "Enable the Clash API" hint can never disagree.
func (e *Executor) Connections(ctx context.Context) clashapi.Payload {
	running := e.StateLabel() != "off"
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	var client *clashapi.Client
	if addr != "" {
		client = clashapi.NewClient(addr, secret)
	}
	return clashapi.Connections(ctx, client, running, addr != "")
}

// CloseConnection closes one active connection by id (CONNECTION-CLOSE),
// via the Clash API's DELETE /connections/{id}.
func (e *Executor) CloseConnection(ctx context.Context, id string) error {
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	if addr == "" {
		return fmt.Errorf("the Clash API is disabled; enable it in settings to manage connections")
	}
	return clashapi.NewClient(addr, secret).CloseConnection(ctx, id)
}

// --- Firewall (F6 item 5 in docs/v2-spec.md) ---

// SetFirewallSaver registers the persistence hook FirewallAdd/FirewallRemove
// call after a successful mutation — the profile.Store-backed implementation
// in cmd/singctl/main.go, mirroring SetSubscriptionDeps/SetSysProxySaver.
func (e *Executor) SetFirewallSaver(fn func([]firewall.Rule) error) {
	e.fwMu.Lock()
	defer e.fwMu.Unlock()
	e.saveFirewall = fn
}

// RestoreFirewallRules seeds the in-memory rule set from disk at startup
// (mirrors RestoreSubscriptions). It does not itself reload the running
// config — the caller's subsequent LoadLink/Reload picks the seeded rules up
// through applyEffective, exactly like a restored subscription's servers do.
func (e *Executor) RestoreFirewallRules(rules []firewall.Rule) {
	e.fwMu.Lock()
	defer e.fwMu.Unlock()
	e.fwRules = append(e.fwRules[:0], rules...)
}

// FirewallList returns a snapshot of the configured firewall rules
// (FIREWALL-LIST), in the order they were added.
func (e *Executor) FirewallList() []firewall.Rule {
	e.fwMu.Lock()
	defer e.fwMu.Unlock()
	out := make([]firewall.Rule, len(e.fwRules))
	copy(out, e.fwRules)
	return out
}

// FirewallAdd validates rule (assigning it a random id if it arrives without
// one), appends it to the persisted set, and — if a mode is currently
// running — reloads the live config the SAME way a key change does
// (applyPreservingMode: one rebuild of the generated config, one re-enable of
// whatever mode was already running), so the new rule takes effect without
// dropping the connection (F6 item 5). With no mode running the rule is
// simply persisted; it takes effect the next time a mode starts.
func (e *Executor) FirewallAdd(ctx context.Context, rule firewall.Rule) (firewall.Rule, error) {
	if rule.ID == "" {
		rule.ID = randomHex()
	}
	if err := rule.Validate(); err != nil {
		return firewall.Rule{}, err
	}
	e.fwMu.Lock()
	for _, r := range e.fwRules {
		if r.ID == rule.ID {
			e.fwMu.Unlock()
			return firewall.Rule{}, fmt.Errorf("firewall: rule id %q already exists", rule.ID)
		}
	}
	e.fwRules = append(e.fwRules, rule)
	snapshot := make([]firewall.Rule, len(e.fwRules))
	copy(snapshot, e.fwRules)
	save := e.saveFirewall
	e.fwMu.Unlock()

	if save != nil {
		if err := save(snapshot); err != nil {
			return firewall.Rule{}, err
		}
	}
	if e.manager() == nil {
		return rule, nil // nothing running yet — takes effect on the next mode start
	}
	return rule, e.applyPreservingMode(ctx)
}

// FirewallRemove drops the rule with the given id from the set and reloads
// the same way FirewallAdd does (a single reload preserving whatever mode was
// running, or none at all if nothing is running).
func (e *Executor) FirewallRemove(ctx context.Context, id string) error {
	id = strings.TrimSpace(id)
	e.fwMu.Lock()
	kept := make([]firewall.Rule, 0, len(e.fwRules))
	found := false
	for _, r := range e.fwRules {
		if r.ID == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	e.fwRules = kept
	snapshot := make([]firewall.Rule, len(kept))
	copy(snapshot, kept)
	save := e.saveFirewall
	e.fwMu.Unlock()
	if !found {
		return fmt.Errorf("no such firewall rule: %s", id)
	}
	if save != nil {
		if err := save(snapshot); err != nil {
			return err
		}
	}
	if e.manager() == nil {
		return nil
	}
	return e.applyPreservingMode(ctx)
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
	defer e.flushLog()
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
	e.pushNonBlocking(notify.ConnectionsMsg{Rows: clashui.ConnRows(conns)})
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
func (e *Executor) pushNonBlocking(msg any) {
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

// appendLog queues one enriched connection line for the same destination as the
// sing-box log: the log file in TUI/non-interactive mode, or stdout when logs
// are streamed (headless --logs, logPath == ""). File output is deliberately
// buffered: a busy browser can create hundreds of short-lived connections and
// doing one write syscall per line has a measurable energy cost.
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
		e.logFile = f
		e.logWriter = bufio.NewWriterSize(f, 64*1024)
		e.startLogFlusherLocked()
	}
	_, _ = fmt.Fprintln(e.logWriter, line)
	// Keep memory bounded even if the periodic flusher is delayed.
	if e.logWriter.Buffered() >= 32*1024 {
		_ = e.logWriter.Flush()
	}
}

const logFlushInterval = 2 * time.Second

// startLogFlusherLocked starts one low-frequency flusher for the current file.
// cfgMu must be held.
func (e *Executor) startLogFlusherLocked() {
	if e.logFlushCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	e.logFlushCancel = cancel
	go func() {
		ticker := time.NewTicker(logFlushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				e.flushLog()
			}
		}
	}()
}

func (e *Executor) flushLog() {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if e.logWriter != nil {
		_ = e.logWriter.Flush()
	}
}

// CloseLog flushes the final partial chunk and closes the enriched-log sink.
func (e *Executor) CloseLog() {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.closeLogLocked()
}

// closeLogLocked tears down the current writer. cfgMu must be held.
func (e *Executor) closeLogLocked() {
	if e.logFlushCancel != nil {
		e.logFlushCancel()
		e.logFlushCancel = nil
	}
	if e.logWriter != nil {
		_ = e.logWriter.Flush()
		e.logWriter = nil
	}
	if e.logFile != nil {
		_ = e.logFile.Close()
		e.logFile = nil
	}
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
	e.push(ctx, notify.NetStateMsg{Cisco: ns.CiscoActive, PhysIface: ns.PhysicalIface, Bypass: bypass, NetextAvailable: netext.Available()})
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
func (e *Executor) runMode() notify.RunMode {
	m := e.manager()
	if m == nil {
		return notify.RunOff
	}
	switch m.State() {
	case runtime.StateVPN:
		return notify.RunVPN
	case runtime.StateProxyOnly:
		return notify.RunProxy
	default:
		return notify.RunOff
	}
}

// --- per-process proxying ---

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
				e.pushNonBlocking(notify.ConsoleMsg{PID: l.PID, App: l.App, Stream: l.Stream, Text: l.Text})
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

// sysProxyMgr lazily builds the Executor's own sysproxy.Manager — same lazy
// pattern as controller(). Building it runs no `networksetup` command; see
// internal/sysproxy's package doc.
func (e *Executor) sysProxyMgr() *sysproxy.Manager {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	if !e.sysProxyBuilt {
		e.sysProxy = sysproxy.NewManager(sysproxy.New()).WithPortDiagnostics(sysproxy.NewPortDiagnostics())
		e.sysProxyBuilt = true
	}
	return e.sysProxy
}

// SysProxyStatus reports the macOS system-proxy (PAC) toggle's current mode,
// service, PAC URL/reachability, and domain count — see
// internal/sysproxy.Manager.Status.
func (e *Executor) SysProxyStatus() sysproxy.Status {
	return e.sysProxyMgr().Status()
}

// SetSysProxySaver registers the persistence hook SysProxySet/SysProxyImport
// call (best-effort, after a successful Apply/Import) — the profile.Store-
// backed implementation in cmd/singctl/main.go, mirroring SetAutostartSaver.
// F1b item 1 in docs/v2-spec.md.
func (e *Executor) SetSysProxySaver(fn func([]byte) error) {
	e.cfgMu.Lock()
	defer e.cfgMu.Unlock()
	e.saveSysproxy = fn
}

// persistSysProxy saves mgr's just-applied config via the registered saver
// (a no-op if none was registered, e.g. no resolvable config directory). A
// save failure is returned to the caller (typically SYSPROXY-SET/-IMPORT)
// exactly like SetAutostartMode's save failure is — it never undoes the
// in-memory Apply/Import that already succeeded.
func (e *Executor) persistSysProxy(mgr *sysproxy.Manager) error {
	e.cfgMu.Lock()
	save := e.saveSysproxy
	e.cfgMu.Unlock()
	if save == nil {
		return nil
	}
	return save(mgr.ConfigINI())
}

// SysProxySet applies cfg as the system-proxy configuration. Requires root
// (the real NetworkSetup adapter shells out to `networksetup`); the daemon
// has it, which is the whole reason this moved out of the GUI.
//
// A successful Apply is persisted (F1b item 1) so it survives a daemon
// restart — including cfg.Mode == sysproxy.ModeOff, so a user who explicitly
// turns the proxy off does not have it switched back on at the next restore.
func (e *Executor) SysProxySet(cfg sysproxy.Config) error {
	mgr := e.sysProxyMgr()
	if err := mgr.Apply(cfg); err != nil {
		return err
	}
	return e.persistSysProxy(mgr)
}

// RestoreSysProxyConfig restores a previously persisted sysproxy config (the
// INI bytes returned by profile.Store.LoadSysproxyConfig — see
// sysproxy.Config.INI) after a daemon restart (F1b item 2). data == nil/empty
// means nothing was ever saved (a fresh install, or one from before F1b) —
// a no-op, matching the Manager's own inert-by-default ModeOff. Restoration
// is best-effort at the caller's discretion: this returns any parse/apply
// error rather than swallowing it, but cmd/singctl/main.go logs it as a
// warning and continues starting up regardless (F1b item 3), the same rule
// F2 item 3 applies to the autostart mode.
func (e *Executor) RestoreSysProxyConfig(data []byte) error {
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	cfg, err := sysproxy.ParseINI(data)
	if err != nil {
		return fmt.Errorf("parse persisted system-proxy config: %w", err)
	}
	return e.sysProxyMgr().Restore(cfg)
}

// SysProxyConfig returns the currently-applied (or default) system-proxy
// Config — used by SYSPROXY-SET to merge a partial (e.g. mode-only) request
// onto the live config rather than a zero value.
func (e *Executor) SysProxyConfig() sysproxy.Config {
	return e.sysProxyMgr().Config()
}

// SysProxyConfigYAML returns the current system-proxy Config as YAML.
// Retained for the pre-INI wire format; SYSPROXY-CONFIG itself now uses
// SysProxyConfigINI.
func (e *Executor) SysProxyConfigYAML() ([]byte, error) {
	return e.sysProxyMgr().ConfigYAML()
}

// SysProxyConfigINI returns the current system-proxy Config as INI (see
// internal/sysproxy/ini.go) — the SYSPROXY-CONFIG payload.
func (e *Executor) SysProxyConfigINI() []byte {
	return e.sysProxyMgr().ConfigINI()
}

// SysProxyImport decodes and applies an INI Config, a legacy YAML Config, or
// a plain newline-separated domain list (see sysproxy.DecodeImport).
//
// A successful Import is persisted exactly like a successful SysProxySet
// (F1b item 1) — see persistSysProxy.
func (e *Executor) SysProxyImport(data []byte) error {
	mgr := e.sysProxyMgr()
	if err := mgr.Import(data); err != nil {
		return err
	}
	return e.persistSysProxy(mgr)
}

// SysProxyReclaimPort removes singctl's own legacy PAC LaunchAgent that may
// be holding the configured PAC port — only when it verifiably belongs to
// singctl (see sysproxy.Manager.ReclaimPort); refuses otherwise. Never runs
// on its own — only in response to the SYSPROXY-RECLAIM-PORT control
// command, itself only reachable from an explicit user action.
func (e *Executor) SysProxyReclaimPort() error {
	return e.sysProxyMgr().ReclaimPort()
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
		return fmt.Errorf("app %s is not currently running", bundleID)
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
		return fmt.Errorf("per-app routing is not supported on this platform")
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
		return pid, fmt.Errorf("app launched (PID %d), but its bundle ID could not be resolved — extension capture was not activated", pid)
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

// --- Manual proxy selection (multi-server failover group) ---
//
// In multi-server mode the generated config wires a urltest group tagged
// "auto" over every server, plus a selector tagged "proxy" whose members are
// ["auto", "proxy-0", …, "proxy-N"], defaulting to "auto" (see
// internal/singbox/generate.go's GenerateProxyConfigOpts). route.final and
// the DNS detour reference "proxy", so switching the selector's active
// member via the Clash API — PUT /proxies/proxy — changes where traffic
// goes live, with no config rebuild. In single-server mode there is no
// group at all: the lone outbound is tagged "proxy" directly.
const (
	proxyGroupTag = "proxy" // mirrors singbox.proxyTag
	autoGroupTag  = "auto"  // mirrors singbox.autoTag
)

// ProxyGroup reports the multi-server failover group for the UI.
type ProxyGroup struct {
	Available bool          `json:"available"` // false in single-server mode
	Auto      bool          `json:"auto"`      // selector currently on "auto"
	Selected  string        `json:"selected"`  // the EFFECTIVE server tag, resolved through auto
	Members   []ProxyMember `json:"members"`
}

// ProxyMember is one server in the failover group.
type ProxyMember struct {
	Tag   string `json:"tag"`   // "proxy-3"
	Index int    `json:"index"` // 3 — its position in the loaded key list
	Name  string `json:"name"`  // the key's display label, e.g. "France 🇫🇷"
	Delay int    `json:"delay"` // ms; 0 = timeout/unknown
}

// proxyServerTag mirrors internal/singbox's own (unexported) helper of the
// same name: the per-server outbound tag in a multi-server set.
func proxyServerTag(i int) string { return fmt.Sprintf("%s-%d", proxyGroupTag, i) }

// proxyTagIndex parses a "proxy-N" tag back into N. ok is false for anything
// that doesn't match, including the bare "proxy"/"auto" group tags.
func proxyTagIndex(tag string) (int, bool) {
	const prefix = proxyGroupTag + "-"
	if !strings.HasPrefix(tag, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(tag[len(prefix):])
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// memberLabel resolves a member's display label: links[index] parsed through
// the registry, for its Profile.Label. It returns "" (never an error) when
// the index is out of range or the label can't be resolved — the caller
// falls back to the raw tag. The links list and the live Clash-reported tags
// are updated independently and can transiently disagree during a reload;
// this must never panic on that race.
func (e *Executor) memberLabel(links []string, index int) string {
	if index < 0 || index >= len(links) {
		return ""
	}
	p, err := e.registry.Parse(links[index])
	if err != nil {
		return ""
	}
	return p.Label
}

// ProxyGroup reports the failover group for the UI: whether one exists at
// all, whether it's on automatic selection, the effective server (resolved
// through "auto" when applicable), and each member with its resolved display
// name and last-probed latency. Returns Available=false (no error) in
// single-server mode, where selection is meaningless, and also if the
// running instance hasn't (yet) reported a group — e.g. mid-reload.
func (e *Executor) ProxyGroup(ctx context.Context) (ProxyGroup, error) {
	links := e.CurrentLinks()
	if len(links) < 2 {
		return ProxyGroup{Available: false}, nil
	}
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	if addr == "" {
		return ProxyGroup{}, fmt.Errorf("the Clash API is disabled; enable it in settings to select a proxy")
	}
	proxies, err := clashapi.NewClient(addr, secret).Proxies(ctx)
	if err != nil {
		return ProxyGroup{}, fmt.Errorf("clash api: %w", err)
	}
	group, ok := proxies[proxyGroupTag]
	if !ok || len(group.All) == 0 {
		return ProxyGroup{Available: false}, nil
	}

	now := group.Now
	auto := now == autoGroupTag || now == ""
	selected := now
	if auto {
		// The Clash API reports the selector's "now" as "auto"; resolve one
		// more hop through the urltest group's own "now" to get the actually
		// effective server.
		if ag, ok := proxies[autoGroupTag]; ok && ag.Now != "" {
			selected = ag.Now
		}
	}

	members := make([]ProxyMember, 0, len(group.All))
	for _, tag := range group.All {
		if tag == autoGroupTag {
			continue
		}
		idx, _ := proxyTagIndex(tag)
		name := e.memberLabel(links, idx)
		if name == "" {
			name = tag
		}
		members = append(members, ProxyMember{
			Tag:   tag,
			Index: idx,
			Name:  name,
			Delay: proxies[tag].LastDelay(),
		})
	}
	return ProxyGroup{Available: true, Auto: auto, Selected: selected, Members: members}, nil
}

// SelectProxy pins traffic to one member of the failover group, or restores
// automatic selection when tag is "auto". It rejects a tag that is not
// currently a member (listing the valid ones) and single-server mode, where
// there is no group to select within.
func (e *Executor) SelectProxy(ctx context.Context, tag string) error {
	links := e.CurrentLinks()
	if len(links) < 2 {
		return fmt.Errorf("proxy selection needs more than one server loaded")
	}
	tag = strings.TrimSpace(tag)
	valid := make([]string, 0, len(links)+1)
	valid = append(valid, autoGroupTag)
	member := tag == autoGroupTag
	for i := range links {
		t := proxyServerTag(i)
		valid = append(valid, t)
		member = member || tag == t
	}
	if !member {
		return fmt.Errorf("unknown proxy %q; valid members: %s", tag, strings.Join(valid, ", "))
	}
	e.cfgMu.Lock()
	addr, secret := e.clashAddr, e.clashSecret
	e.cfgMu.Unlock()
	if addr == "" {
		return fmt.Errorf("the Clash API is disabled; enable it in settings to select a proxy")
	}
	if err := clashapi.NewClient(addr, secret).SelectOutbound(ctx, proxyGroupTag, tag); err != nil {
		return fmt.Errorf("clash api: %w", err)
	}
	return nil
}

// CurrentSettings returns the live tunables as a notify.Settings (the inverse of
// ApplySettings) so a remote client / SETTINGS-GET can seed its form.
func (e *Executor) CurrentSettings() notify.Settings {
	e.cfgMu.Lock()
	mode := e.autostartMode
	defer e.cfgMu.Unlock()
	if mode == "" {
		mode = "off"
	}
	return notify.Settings{
		SocksPort:        e.ports.Socks,
		ClashEnabled:     e.clashAddr != "",
		ClashAddr:        e.clashAddr,
		URLTestURL:       e.urltest.URL,
		URLTestInterval:  e.urltest.Interval,
		URLTestTolerance: e.urltest.Tolerance,
		SaveProfile:      e.save != nil,
		AutostartMode:    mode,
	}
}

// ApplySettings reloads the running core with edited tunables (port, Clash API,
// urltest) and re-enables the current mode so the changes take effect live.
func (e *Executor) ApplySettings(ctx context.Context, s notify.Settings) error {
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
	if s.AutostartMode != "" { // "" (a client that predates F2) means "leave it alone"
		if err := e.SetAutostartMode(s.AutostartMode); err != nil {
			return err
		}
	}
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
		// Only the manual keys: the child restores subscriptions from
		// subscriptions.json and refetches them itself.
		_ = e.save(strings.Join(e.manualLinks(), "\n"))
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

// StopDaemon is only meaningful for a remote (attached)
// client; the local executor is the process itself, so it has no background
// instance to stop.
func (e *Executor) StopDaemon(context.Context) error {
	return fmt.Errorf("no background process (this is a local run)")
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

func (e *Executor) push(ctx context.Context, msg any) {
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
	e.push(ctx, notify.StatusMsg{Mode: e.runMode(), Note: note})
	// The UI never initiates these (driven by Cisco connect/disconnect), so also
	// surface them in the action log.
	e.pushNonBlocking(notify.ActionMsg{Level: level, Text: note})
}

// coexistNote returns the user-facing message + action-log level for a
// coexistence state (empty for the steady "no Cisco" state).
func coexistNote(s coexistState, physIface string) (string, int) {
	switch s {
	case coexistBypass:
		iface := physIface
		if iface == "" {
			iface = "the physical interface"
		}
		return "Cisco is active — proxy is bypassing Cisco (egress via " + iface + ")", notify.ActOk
	case coexistFallback:
		return "Cisco is active — could not bind the proxy to the physical interface, traffic is riding Cisco (fallback)", notify.ActWarn
	case coexistSuspended:
		return "Cisco is active — our VPN has stopped (fail-closed), we will resume once Cisco disconnects", notify.ActWarn
	case coexistNone:
		return "Cisco disconnected — proxy is back on the default route", notify.ActInfo
	default:
		return "", notify.ActInfo
	}
}
