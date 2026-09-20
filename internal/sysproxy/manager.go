package sysproxy

import (
	"errors"
	"fmt"
	"sync"
)

// Status is the daemon-facing snapshot the SYSPROXY-STATUS control command
// returns.
type Status struct {
	Mode Mode `json:"mode"`
	// Service is the macOS network service the current config targets.
	Service string `json:"service"`
	// PACURL is the localhost URL the PAC is served from, "" in ModeOff or
	// before Apply has ever run in a non-off mode.
	PACURL string `json:"pac_url,omitempty"`
	// PACServerUp reports whether the PAC server is currently reachable.
	PACServerUp bool `json:"pac_server_up"`
	// DomainCount is len(Proxy) in ModeInclude, len(Direct) in ModeExclude
	// (after normalization/dedup), 0 in ModeOff.
	DomainCount int `json:"domain_count"`
}

// Manager owns the current Config, the injected NetworkSetup port, and the
// in-process PAC server, and is the only thing in this package that actually
// changes macOS proxy state. See the package doc's SAFETY note: a freshly
// constructed Manager has touched nothing — Apply/Import only run when a
// caller explicitly asks.
type Manager struct {
	ns     NetworkSetup
	server *pacServer
	diag   PortDiagnostics

	mu  sync.Mutex
	cfg Config
}

// NewManager returns a Manager backed by ns, starting from DefaultConfig()
// (ModeOff) and with no system state touched yet. It has no PortDiagnostics
// until WithPortDiagnostics is called — see that method's doc for what a
// Manager without one still does (and doesn't do).
func NewManager(ns NetworkSetup) *Manager {
	return &Manager{ns: ns, server: newPACServer(), cfg: DefaultConfig()}
}

// WithPortDiagnostics attaches diag — used to name a pinned PAC port's
// holder on a failed bind (see diagnoseBindError) and to back ReclaimPort.
// Optional: a Manager with none behaves exactly as it did before F1 — a
// bind-failure error carries no pid/path detail, and ReclaimPort refuses
// outright rather than doing anything. Returns m so callers can chain it
// onto NewManager. Not safe to call concurrently with Apply/ReclaimPort.
func (m *Manager) WithPortDiagnostics(diag PortDiagnostics) *Manager {
	m.diag = diag
	return m
}

// Config returns the currently-applied (or, before the first Apply,
// default) config.
func (m *Manager) Config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// ConfigYAML renders Config() as YAML. Retained for callers still on the
// pre-INI wire format; SYSPROXY-CONFIG itself now returns ConfigINI.
func (m *Manager) ConfigYAML() ([]byte, error) {
	return m.Config().YAML()
}

// ConfigINI renders Config() as INI (Config.INI) — the SYSPROXY-CONFIG
// payload.
func (m *Manager) ConfigINI() []byte {
	return m.Config().INI()
}

// Status reports the current mode/service/PAC state — the SYSPROXY-STATUS
// payload.
func (m *Manager) Status() Status {
	cfg := m.Config()
	count := len(normalizeDomains(cfg.Proxy))
	if cfg.Mode == ModeExclude {
		count = len(normalizeDomains(cfg.Direct))
	}
	return Status{
		Mode:        cfg.Mode,
		Service:     cfg.Service,
		PACURL:      m.server.url(),
		PACServerUp: m.server.answers(),
		DomainCount: count,
	}
}

// Apply validates cfg and, only if it's valid, makes it the live system
// proxy configuration: ModeOff disables both the automatic (PAC) and manual
// web proxy on cfg.Service; ModeInclude/ModeExclude generate and serve the
// matching PAC and point cfg.Service's Automatic Proxy Configuration at it.
// An invalid cfg is rejected before anything is touched — neither the
// NetworkSetup port nor the PAC server sees a call, and the previously
// applied config remains live.
func (m *Manager) Apply(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	switch cfg.Mode {
	case ModeOff:
		// All three calls run regardless of order/failure of the others: a
		// half-applied "off" (PAC still pointed at, or a stale bypass list
		// left in place, say, but manual proxy cleared) is exactly the
		// confusing state this exists to prevent, so report whichever fails
		// rather than silently skipping the rest.
		if err := m.ns.DisableAutoProxy(cfg.Service); err != nil {
			return fmt.Errorf("sysproxy: disable auto proxy: %w", err)
		}
		if err := m.ns.DisableWebProxy(cfg.Service); err != nil {
			return fmt.Errorf("sysproxy: disable web proxy: %w", err)
		}
		if err := m.ns.SetBypassDomains(cfg.Service, nil); err != nil {
			return fmt.Errorf("sysproxy: clear bypass domains: %w", err)
		}
	case ModeInclude, ModeExclude:
		pac, err := GeneratePAC(cfg)
		if err != nil {
			return err
		}
		if err := m.server.ensure(cfg.PACPort); err != nil {
			return m.diagnoseBindError(cfg.PACPort, err)
		}
		m.server.setBody([]byte(pac))
		// PAC and the manual proxy fields must not overlap (mirrors
		// `make proxy-on`/`proxy-pac`'s -setwebproxystate/-setsecurewebproxystate
		// off before -setautoproxyurl).
		if err := m.ns.DisableWebProxy(cfg.Service); err != nil {
			return fmt.Errorf("sysproxy: disable manual web proxy: %w", err)
		}
		if err := m.ns.SetAutoProxyURL(cfg.Service, m.server.url()); err != nil {
			return fmt.Errorf("sysproxy: set auto proxy url: %w", err)
		}
		// Belt-and-suspenders on top of the PAC's own DIRECT rules (see
		// NetworkSetup.SetBypassDomains) — mirrors `make proxy-on`/
		// `proxy-pac`'s -setproxybypassdomains. Only the domain/glob-shaped
		// Direct entries apply: macOS bypass lists take hosts/domains, not
		// CIDRs.
		if err := m.ns.SetBypassDomains(cfg.Service, bypassDomainsFor(cfg.Direct)); err != nil {
			return fmt.Errorf("sysproxy: set bypass domains: %w", err)
		}
	}

	m.cfg = cfg
	return nil
}

// Restore re-applies a previously persisted Config after a daemon restart
// (F1b in docs/v2-spec.md) — bringing the PAC server back up and pointing
// `networksetup` at it again, since neither survives the process exiting.
//
// ModeOff is special-cased and does NOT go through Apply: F1b item 4
// requires that a persisted mode = off config "restores nothing and touches
// no system state" — a user who explicitly turned the proxy off must not
// have Apply's ModeOff branch call DisableAutoProxy/DisableWebProxy/
// SetBypassDomains against NetworkSetup on their behalf again next boot. The
// Manager simply adopts cfg as its in-memory state, matching the same
// zero-touch guarantee NewManager's own DefaultConfig gives a fresh Manager.
//
// Any other mode goes through the ordinary Apply path. That is deliberate,
// not a shortcut: Apply's ModeInclude/ModeExclude branch calls
// m.server.ensure(cfg.PACPort), and PACPort == 0 (the default — see F1) means
// "let the OS assign a fresh ephemeral port," so re-Applying the exact
// persisted Config already binds a NEW port this run and calls
// SetAutoProxyURL with the URL that new port actually produced — never the
// one recorded in the stored config (Config.PACPort's doc comment is explicit
// that field is never authoritative; the bound port only ever lives in
// Manager.Status().PACURL). A caller that pinned an explicit PACPort gets the
// same pinned-port behavior Apply always had.
//
// Like Apply, an invalid cfg is rejected before anything is touched.
func (m *Manager) Restore(cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Mode == ModeOff {
		m.mu.Lock()
		m.cfg = cfg
		m.mu.Unlock()
		return nil
	}
	return m.Apply(cfg)
}

// Import decodes a SYSPROXY-IMPORT payload (see DecodeImport: an INI Config,
// a full legacy YAML Config, or a plain domain list that updates only the
// current config's Proxy — see Config.ApplyImport) and Applies the result.
// Like Apply, a payload that decodes to an invalid config touches nothing.
func (m *Manager) Import(data []byte) error {
	imported, full, err := DecodeImport(data)
	if err != nil {
		return err
	}
	return m.Apply(m.Config().ApplyImport(imported, full))
}

// Close shuts down the PAC server, if running. Best-effort; safe to call
// multiple times or never (the daemon's process exit reclaims the socket
// either way).
func (m *Manager) Close() error {
	return m.server.close()
}

// diagnoseBindError enriches a failed PAC-server bind with who holds the
// port and, when that's verifiably singctl's own legacy PAC LaunchAgent,
// exactly how to remove it — F1 item 3 in docs/v2-spec.md. port == 0 (the
// new default — "let the OS pick") never reaches here: an ephemeral bind
// cannot lose a race for a specific port, so bindErr is only ever the result
// of a caller explicitly pinning PACPort.
//
// With no PortDiagnostics attached (m.diag == nil) this just returns bindErr
// unchanged — the plain, pre-F1 "address already in use" error — rather than
// failing to report anything at all.
func (m *Manager) diagnoseBindError(port int, bindErr error) error {
	if m.diag == nil {
		return bindErr
	}
	holder, ok, err := m.diag.HolderOf(port)
	if err != nil || !ok {
		// Couldn't identify the holder (lookup unsupported, raced with the
		// bind, or errored) — the plain bind error is still better than
		// nothing, and is not itself a reason to fail differently.
		return bindErr
	}
	detail := fmt.Sprintf(" (held by pid %d: %s)", holder.PID, holder.Path)
	if present, owned, path, lerr := m.diag.LegacyPACAgent(); lerr == nil && present && owned {
		detail += fmt.Sprintf(
			"; this is singctl's own legacy PAC LaunchAgent (%s) — remove it by running "+
				"`launchctl bootout gui/$(id -u)/com.singctl.pacserver` and deleting that file, "+
				"or send the SYSPROXY-RECLAIM-PORT control command to have singctl do it",
			path,
		)
	}
	return fmt.Errorf("%w%s", bindErr, detail)
}

// ReclaimPort removes singctl's own legacy PAC LaunchAgent — the standalone
// mac-proxy utility's (and singctl's own pre-2.0 `make pac-server` target's)
// com.singctl.pacserver LaunchAgent, which can hold the exact port singctl's
// in-process PAC server wants (F1 item 4 in docs/v2-spec.md). It ONLY
// proceeds when the on-disk plist verifiably matches singctl's own legacy
// shape (IsLegacyPACAgentPlist, via PortDiagnostics.LegacyPACAgent) — never
// for a foreign LaunchAgent, even one occupying the same port. This is never
// called automatically (in particular, never from Apply/diagnoseBindError);
// it only runs in response to an explicit user action — the daemon's
// SYSPROXY-RECLAIM-PORT control command.
func (m *Manager) ReclaimPort() error {
	if m.diag == nil {
		return errors.New("sysproxy: reclaim port: no port diagnostics available")
	}
	present, owned, path, err := m.diag.LegacyPACAgent()
	if err != nil {
		return fmt.Errorf("sysproxy: reclaim port: %w", err)
	}
	if !present {
		return errors.New("sysproxy: reclaim port: no legacy PAC LaunchAgent found — nothing to remove")
	}
	if !owned {
		return fmt.Errorf("sysproxy: reclaim port: %s does not match singctl's own legacy PAC agent shape — refusing to touch a LaunchAgent that isn't verifiably ours", path)
	}
	return m.diag.RemoveLegacyPACAgent()
}

// bypassDomainsFor extracts the domain/glob-shaped entries from a Direct
// rule list for NetworkSetup.SetBypassDomains — CIDR entries are dropped:
// macOS bypass lists take hosts/domains, not CIDRs (see Manager.Apply).
func bypassDomainsFor(direct []string) []string {
	domains, globs, _, _ := classifyRules(direct)
	if len(domains) == 0 && len(globs) == 0 {
		return nil
	}
	out := make([]string, 0, len(domains)+len(globs))
	out = append(out, domains...)
	out = append(out, globs...)
	return out
}
