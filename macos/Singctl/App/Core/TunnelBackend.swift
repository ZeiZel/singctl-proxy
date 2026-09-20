// TunnelBackend.swift
//
// The App Store `Backend`: drives a `NETunnelProviderManager`-managed
// `NEPacketTunnelProvider` (the `PacketTunnel` appex, bundle id
// `com.singctl.appstore.tunnel`) instead of a root daemon, and persists mode/
// keys/settings to `config.json` in the shared App Group container
// (`group.com.singctl.appstore`) instead of talking to a Unix control socket.
//
// `config.json`'s shape here (`TunnelConfig`: mode + keys + settings) is an
// interim, container-app-owned representation. Per docs/appstore-sku.md, a
// later Go `mobile.BuildConfig(keysJSON, settingsJSON)` shim (reusing
// internal/vless + internal/singbox) is meant to turn this into the actual
// sing-box config JSON the appex's `PacketTunnelProvider.loadConfigJSON()`
// expects — that transform doesn't exist yet, so today the appex simply has
// nothing valid to read (it's a documented stub, see PacketTunnel/
// PacketTunnelProvider.swift). Swift-side plumbing (this file) is complete;
// wiring the Go shim + Libbox is a separate, later step.
//
// `#if APPSTORE` only — compiles to nothing in the Developer-ID build even
// without project.yml's target-level exclusion (belt-and-suspenders, like
// DaemonBackend.swift's `#if !APPSTORE`).

#if APPSTORE
import Foundation

/// The App Store SKU's `Backend`. No control socket, no Clash API: `status()`
/// reads the tunnel's connection state + the locally-stored mode; keys/
/// settings round-trip through the App Group's `config.json`; traffic/
/// latency/connections are stubbed until Libbox stats are wired up.
///
/// Not itself `@MainActor` (unlike `VPNController`, which it owns) so it can
/// be constructed synchronously anywhere, including `AppModel`'s
/// `EnvironmentKey.defaultValue`; every `vpn.*` call below hops to the main
/// actor as needed.
final class TunnelBackend: Backend {
    static let appGroupID = "group.com.singctl.appstore"
    static let providerBundleID = "com.singctl.appstore.tunnel"

    private let vpn = VPNController()

    // MARK: - Status / lifecycle

    func status() async throws -> DaemonStatus {
        await vpn.load()
        let config = TunnelConfigStore.load()
        let running = await vpn.isConnectedOrConnecting
        return DaemonStatus(
            pid: 0,
            mode: running ? config.mode : "off",
            startedAt: "",
            ciscoActive: false,
            proxyBypass: false,
            physIface: "",
            netextSupported: false,
            netextAvailable: false
        )
    }

    func setMode(_ mode: String) async throws {
        var config = TunnelConfigStore.load()
        config.mode = mode
        TunnelConfigStore.save(config)
        if mode.isEmpty || mode == "off" {
            await vpn.stop()
        } else {
            await vpn.load()
            await vpn.start()
        }
    }

    func stop() async throws {
        var config = TunnelConfigStore.load()
        config.mode = "off"
        TunnelConfigStore.save(config)
        await vpn.load()
        await vpn.stop()
    }

    // MARK: - Observability
    //
    // TODO(datapath): Libbox doesn't expose live stats to the container app
    // yet (needs the `mobile/` gomobile shim's command-client bridge — see
    // docs/appstore-sku.md). Stub to empty/zero until that lands.

    func traffic() async throws -> Traffic { .zero }
    func latency() async throws -> Latency { .empty }
    func connections() async throws -> ClashConnections {
        ClashConnections(downloadTotal: 0, uploadTotal: 0, connections: [])
    }

    // TODO(datapath): the F6 CONNECTIONS/CONNECTION-CLOSE control verbs are
    // a root-daemon + Clash API concept (state diagnosis, live table,
    // aggregates) that has no equivalent here yet — Libbox is driven
    // directly by this container app with no Clash API surfaced to it.
    // Report "unsupported" rather than faking a state/payload, same as
    // proxyGroup()/sysProxy* below — ConnectionsScreen itself is Dev-ID only
    // (excluded from this target, see project.yml) so these aren't reachable
    // from any screen today, but the contract stays honest if that changes.

    func connectionsDetail() async throws -> ConnectionsPayload {
        throw TunnelBackendError.unsupported
    }

    func closeConnection(_ id: String) async throws {
        throw TunnelBackendError.unsupported
    }

    // MARK: - Proxy failover group
    //
    // TODO(datapath): this SKU has no daemon and no failover-group concept
    // yet — Libbox is driven directly by this container app with no urltest
    // group exposed to it. Report "unsupported" rather than faking a group
    // or silently accepting a pin that can never take effect.

    func proxyGroup() async throws -> ProxyGroup {
        throw TunnelBackendError.unsupported
    }

    func proxySelect(_ tag: String) async throws {
        throw TunnelBackendError.unsupported
    }

    // MARK: - Keys (VLESS links)

    func keysGet() async throws -> [String] {
        TunnelConfigStore.load().keys
    }

    func keysAdd(_ link: String) async throws {
        var config = TunnelConfigStore.load()
        config.keys.append(link.trimmingCharacters(in: .whitespacesAndNewlines))
        TunnelConfigStore.save(config)
    }

    // TODO(datapath): WireGuard, like subscriptions above, has no datapath
    // here yet (no gomobile Libbox WireGuard support wired into the appex).
    // Report "unsupported" rather than silently accepting a config this
    // build can never actually connect with.
    func keysAddConfig(_ config: String) async throws {
        throw TunnelBackendError.unsupported
    }

    func keysRemove(_ index: Int) async throws {
        var config = TunnelConfigStore.load()
        guard config.keys.indices.contains(index) else { return }
        config.keys.remove(at: index)
        TunnelConfigStore.save(config)
    }

    /// KeysScreen derives a key's display name from its `#fragment` (see
    /// KeysScreen.deriveName); mirror that convention in reverse here so
    /// renaming round-trips the same way the Dev-ID KEYS-RENAME verb does.
    func keysRename(_ index: Int, _ name: String) async throws {
        var config = TunnelConfigStore.load()
        guard config.keys.indices.contains(index) else { return }
        config.keys[index] = Self.settingFragment(
            on: config.keys[index],
            to: name.trimmingCharacters(in: .whitespacesAndNewlines)
        )
        TunnelConfigStore.save(config)
    }

    private static func settingFragment(on link: String, to name: String) -> String {
        let base = link.split(separator: "#", maxSplits: 1, omittingEmptySubsequences: false)
            .first.map(String.init) ?? link
        let escaped = name.addingPercentEncoding(withAllowedCharacters: .urlFragmentAllowed) ?? name
        return "\(base)#\(escaped)"
    }

    // MARK: - Subscriptions
    //
    // TODO(datapath): subscription fetching lives in the daemon's internal/sub
    // package (a real HTTP fetch + parse), which isn't reachable from this
    // sandboxed container app or the appex yet. Report "unsupported" rather
    // than silently no-opping, so the Subscriptions panel can say so instead
    // of looking broken (an empty subAdd that pretended to succeed would just
    // be a silently-stale subscription forever).

    func subList() async throws -> [Subscription] { [] }

    func subAdd(_ url: String) async throws {
        throw TunnelBackendError.unsupported
    }

    func subRemove(_ url: String) async throws {
        throw TunnelBackendError.unsupported
    }

    func subUpdate() async throws -> Int {
        throw TunnelBackendError.unsupported
    }

    // MARK: - Settings

    func settingsGet() async throws -> Settings {
        TunnelConfigStore.load().settings
    }

    func settingsSet(_ settings: Settings) async throws {
        var config = TunnelConfigStore.load()
        config.settings = settings
        TunnelConfigStore.save(config)
    }

    // MARK: - System proxy
    //
    // TODO(datapath): the system-wide PAC + `networksetup` proxy is a root-
    // daemon-only concept (it shells out to `networksetup`, which this
    // sandboxed container app cannot do). Report "unsupported" rather than
    // faking success or a fabricated status, same as the failover-group
    // methods above — SysProxyScreen still renders in this build and simply
    // shows the error inline.

    func sysProxyStatus() async throws -> SysProxyStatus {
        throw TunnelBackendError.unsupported
    }

    func sysProxyConfig() async throws -> String {
        throw TunnelBackendError.unsupported
    }

    func sysProxySet(mode: String) async throws {
        throw TunnelBackendError.unsupported
    }

    func sysProxyImport(_ text: String) async throws {
        throw TunnelBackendError.unsupported
    }

    // MARK: - Firewall
    //
    // TODO(datapath): no generated sing-box config / route-rules concept in
    // this build yet (Libbox is driven directly, with no internal/firewall
    // rendering wired into it) — report "unsupported" rather than silently
    // no-opping or faking an empty rule set, same reasoning as above.

    func firewallList() async throws -> [FirewallRule] {
        throw TunnelBackendError.unsupported
    }

    func firewallAdd(_ rule: FirewallRule) async throws -> FirewallRule {
        throw TunnelBackendError.unsupported
    }

    func firewallRemove(_ id: String) async throws {
        throw TunnelBackendError.unsupported
    }
}

/// Errors specific to the App Store SKU's `Backend` — currently the
/// subscription methods and WireGuard config import, neither of which have
/// a datapath in this build yet (see the MARK: - Subscriptions and
/// keysAddConfig TODOs above).
enum TunnelBackendError: Error, LocalizedError {
    case unsupported

    var errorDescription: String? {
        switch self {
        case .unsupported: return "This feature isn't available in this build yet."
        }
    }
}

// MARK: - App Group config store

/// The container-app-owned state persisted to `config.json` in the App Group
/// container (see the file-level doc comment above for why this isn't yet
/// the real sing-box config the appex expects).
struct TunnelConfig: Codable, Equatable {
    var mode: String = "off"
    var keys: [String] = []
    var settings: Settings = Settings(
        socksPort: 0,
        clashEnabled: false,
        clashAddr: "",
        urlTestURL: "https://www.gstatic.com/generate_204",
        urlTestInterval: "3m",
        urlTestTolerance: 50,
        saveProfile: false
    )
}

/// Reads/writes `TunnelConfig` to the App Group container, tolerating a
/// missing/corrupt file (first launch, or before the container app has ever
/// written anything) by falling back to defaults rather than throwing.
enum TunnelConfigStore {
    private static var configURL: URL? {
        FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: TunnelBackend.appGroupID)?
            .appendingPathComponent("config.json")
    }

    static func load() -> TunnelConfig {
        guard let url = configURL, let data = try? Data(contentsOf: url) else {
            return TunnelConfig()
        }
        return (try? JSONDecoder().decode(TunnelConfig.self, from: data)) ?? TunnelConfig()
    }

    static func save(_ config: TunnelConfig) {
        guard let url = configURL, let data = try? JSONEncoder().encode(config) else { return }
        try? data.write(to: url, options: .atomic)
    }
}
#endif
