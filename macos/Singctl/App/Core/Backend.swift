// Backend.swift
//
// The one seam between the kept screens (Dashboard, Proxies, Keys, Settings +
// the menu-bar extra) and the two very different things that can drive them:
//   - Developer-ID build (default, `#if !APPSTORE`): `DaemonBackend`, a thin
//     wrapper over the existing `ControlClient` (Unix control-socket verbs)
//     + `ClashClient` (read-only observability) + `InstanceDiscovery` (daemon
//     lookup) — see DaemonBackend.swift. Behavior is byte-for-byte identical
//     to what LiveStore/screens did before this abstraction existed.
//   - App Store build (`#if APPSTORE`): `TunnelBackend`, which drives a
//     `NETunnelProviderManager`-managed `NEPacketTunnelProvider` instead of a
//     root daemon, and persists mode/keys/settings to the shared App Group
//     container instead of talking to a control socket — see
//     TunnelBackend.swift.
//
// The protocol only covers what the KEPT screens call. Per-app routing
// (PROC-*/APP-*) is an App-Store-forbidden feature whose screens (AppsScreen,
// ConnectionsScreen) are excluded from the App Store target entirely — they
// keep talking to `ControlClient` directly via the `\.controlClient`
// environment key, which is Dev-ID-only (see AppModel.swift). Console
// streaming (CONSOLE-POLL) is instead polled by `LiveStore` itself and
// surfaced as a source filter on LogsScreen (also Dev-ID-only) — see
// LiveStore.swift.
import Foundation

/// Everything the Dashboard/Proxies/Keys/Settings screens + the menu-bar
/// extra need from "the thing that runs the proxy", independent of whether
/// that's a root daemon (Dev-ID) or a sandboxed packet-tunnel appex (App
/// Store). `AnyObject`-constrained so it can be held as a reference type and
/// injected via a SwiftUI environment key without wrapping (see AppModel.swift).
protocol Backend: AnyObject {

    // MARK: - Status / lifecycle

    /// Current daemon/tunnel status (mode, pid, Cisco/netext flags — see
    /// `DaemonStatus`). Dev-ID: STATUS control verb. App Store: derived from
    /// the `NETunnelProviderManager` connection state + the mode stored in
    /// the App Group config.
    func status() async throws -> DaemonStatus

    /// Switches proxy mode ("off" / "proxy" / "vpn"). Dev-ID: MODE control
    /// verb. App Store: persists the mode to the App Group config and
    /// starts/stops the tunnel accordingly.
    func setMode(_ mode: String) async throws

    /// Stops routing entirely. Dev-ID: STOP control verb (stops the whole
    /// daemon). App Store: stops the packet-tunnel session.
    func stop() async throws

    // MARK: - Observability (Dashboard)

    /// Cumulative up/down byte counters. Dev-ID: TRAFFIC control verb (only
    /// available while the Clash API is enabled — see DaemonBackend). App
    /// Store: stubbed to zero for now (Libbox stats are a later step).
    func traffic() async throws -> Traffic

    /// Per-server urltest latency + current selection (Proxies screen).
    /// Dev-ID: derived from the Clash API's `/proxies`. App Store: stubbed
    /// empty for now.
    func latency() async throws -> Latency

    /// Live connection table + cumulative totals (Dashboard's connection
    /// count badge). Dev-ID: the Clash API's `/connections`. App Store:
    /// stubbed empty for now.
    func connections() async throws -> ClashConnections

    /// Live connection table with per-app/per-destination aggregates and an
    /// explicit diagnosis for why it might be empty (F6 items 1-3 in
    /// docs/v2-spec.md) — the single source of truth ConnectionsScreen's
    /// empty state and the Dashboard's traffic empty state both read, so the
    /// two can never disagree about which is true. Distinct from
    /// `connections()` above, which mirrors the Clash API's own
    /// GET /connections envelope. Dev-ID: CONNECTIONS control verb. App
    /// Store: unsupported (no daemon/Clash API in this build).
    func connectionsDetail() async throws -> ConnectionsPayload

    /// Closes one live connection by id. Dev-ID: CONNECTION-CLOSE control
    /// verb (the Clash API's DELETE /connections/{id}). App Store:
    /// unsupported.
    func closeConnection(_ id: String) async throws

    // MARK: - Proxy failover group (Proxies screen)

    /// The failover group's live member list — name, index, per-member
    /// urltest delay, which member is currently effective, and whether
    /// that's because urltest picked it automatically or the user pinned it
    /// via `proxySelect`. `available == false` means single-server mode (no
    /// group at all — see `ProxyGroup`'s doc comment). Dev-ID: PROXY-GROUP
    /// control verb. App Store: unsupported (no failover-group concept in
    /// this build yet — see TunnelBackend).
    func proxyGroup() async throws -> ProxyGroup

    /// Pins the failover group to one member (`tag`), or restores automatic
    /// urltest selection (`tag == "auto"`). Dev-ID: PROXY-SELECT control
    /// verb. App Store: unsupported.
    func proxySelect(_ tag: String) async throws

    // MARK: - Keys (VLESS links)

    func keysGet() async throws -> [String]
    func keysAdd(_ link: String) async throws

    /// Adds a WireGuard key from raw INI config text (KeysScreen's "Config"
    /// input mode) rather than a share link. Dev-ID: KEYS-ADD-CONFIG control
    /// verb (base64 of the config — see `ControlClient.keysAddConfig`'s doc
    /// comment for why a distinct verb/encoding is needed instead of
    /// `keysAdd`). App Store: unsupported (no WireGuard datapath yet).
    func keysAddConfig(_ config: String) async throws

    func keysRemove(_ index: Int) async throws
    func keysRename(_ index: Int, _ name: String) async throws

    // MARK: - Subscriptions
    //
    // A subscription owns the servers it fetches: they refresh automatically
    // and cannot be renamed/removed individually (keysRemove/keysRename
    // refuse a subscription-owned link — see KeysScreen), only the whole
    // subscription can be dropped. Manual keys (above) and subscription
    // servers coexist; manual keys keep priority in the failover group.

    /// Every configured subscription plus the outcome of its last fetch.
    /// Dev-ID: SUB-LIST control verb. App Store: stubbed empty (no fetch
    /// capability yet — see TunnelBackend).
    func subList() async throws -> [Subscription]

    /// Adds a subscription, fetching it immediately; throws if the URL is
    /// unusable. Dev-ID: SUB-ADD control verb. App Store: unsupported.
    func subAdd(_ url: String) async throws

    /// Removes a subscription and every server it owns. Dev-ID: SUB-REMOVE
    /// control verb. App Store: unsupported.
    func subRemove(_ url: String) async throws

    /// Refreshes every subscription's server list now; returns how many
    /// actually changed. Dev-ID: SUB-UPDATE control verb. App Store:
    /// unsupported.
    func subUpdate() async throws -> Int

    // MARK: - Settings

    func settingsGet() async throws -> Settings
    func settingsSet(_ settings: Settings) async throws

    // MARK: - System proxy (macOS PAC + `networksetup`)
    //
    // The system-wide proxy toggle that used to be `make proxy-on`/
    // `proxy-pac`/`proxy-off` (Makefile, generated PAC + `networksetup`) has
    // moved into the daemon, which runs as root and can actually apply it.
    // Dev-ID: SYSPROXY-* control verbs. App Store: unsupported — the sandbox
    // has no root daemon and no `networksetup` access, exactly like
    // `proxyGroup`/`proxySelect` above (see TunnelBackend).
    //
    // SAFETY: `sysProxySet`/`sysProxyImport` change the machine's system-wide
    // network configuration. Callers must only invoke them from an explicit
    // user action (e.g. a button click) — never from a screen's `.task`/
    // `onAppear`, which must stick to `sysProxyStatus`/`sysProxyConfig`.

    /// Current applied state: mode, which network service it's applied to,
    /// whether the localhost PAC server is answering, and the domain count.
    /// Dev-ID: SYSPROXY-STATUS control verb. App Store: unsupported.
    func sysProxyStatus() async throws -> SysProxyStatus

    /// The current config as raw INI text, for display/copy (not decoded —
    /// same "give the UI the exact wire text" idea as `keysGet`'s masking
    /// doc comment). Dev-ID: SYSPROXY-CONFIG control verb. App Store:
    /// unsupported.
    func sysProxyConfig() async throws -> String

    /// Applies a mode ("off"/"exclude"/"include"). Dev-ID: SYSPROXY-SET
    /// control verb, JSON body `{"mode": …}`. App Store: unsupported.
    func sysProxySet(mode: String) async throws

    /// Imports an INI rules file, a YAML config OR a plain domain list — the
    /// daemon tells the two apart from the content. `text` is the raw UTF-8
    /// text (base64-encoded on the wire by the concrete backend, same trick
    /// as `keysAddConfig` — see that method's doc comment for why a
    /// multi-line payload can't go through as a normal single-line
    /// argument). Dev-ID: SYSPROXY-IMPORT control verb. App Store:
    /// unsupported.
    func sysProxyImport(_ text: String) async throws

    // MARK: - Firewall (F6 item 5 in docs/v2-spec.md)
    //
    // A minimal block/allow firewall matched by destination domain,
    // destination CIDR, or source process name, persisted by the daemon and
    // rendered into the generated sing-box config's route rules — a change
    // takes effect on the running proxy immediately (one reload, same as a
    // key change), no restart needed. Dev-ID: FIREWALL-* control verbs. App
    // Store: unsupported — no daemon and no generated sing-box config in
    // this build (see TunnelBackend).

    /// Every configured rule, in the order it was added.
    func firewallList() async throws -> [FirewallRule]

    /// Adds `rule` (assigning it a random id if it arrives without one) and
    /// returns the stored rule with its id filled in. Throws if `rule`
    /// doesn't validate (exactly one of domain/cidr/process, a well-formed
    /// CIDR) — the client pre-validates the same thing before calling this
    /// for fast feedback, but the daemon remains the authority.
    func firewallAdd(_ rule: FirewallRule) async throws -> FirewallRule

    /// Removes the rule with the given id; throws if no such rule exists.
    func firewallRemove(_ id: String) async throws
}
