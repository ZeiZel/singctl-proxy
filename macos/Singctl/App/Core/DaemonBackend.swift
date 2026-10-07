// DaemonBackend.swift
//
// The Developer-ID `Backend`: delegates every method to the existing
// `ControlClient` (Unix control-socket verbs) + `ClashClient` (read-only
// Clash-API observability) + `InstanceDiscovery` (daemon lookup), exactly
// mirroring what LiveStore/screens did directly before `Backend` existed —
// see each method's doc comment for the specific behavior being preserved.
//
// `#if !APPSTORE` only: `ControlClient`/`ClashClient`/`InstanceDiscovery` are
// excluded from the App Store target's source list (project.yml), so this
// whole file compiles to nothing there even without that exclusion — the
// guard is belt-and-suspenders.

#if !APPSTORE
import Foundation

/// Wraps `ControlClient`/`ClashClient`/`InstanceDiscovery` for the
/// Developer-ID build. One instance is shared app-wide (see SingctlApp.swift),
/// though — like `ControlClient` itself — it holds no meaningful state, so
/// creating more than one is harmless.
final class DaemonBackend: Backend {
    private let control = ControlClient()

    /// Throttle state for the single-server latency probe in `latency()` —
    /// see that method's doc comment. Both are only ever touched from
    /// `latency()`, which LiveStore's poll loop calls serially (never
    /// concurrently), so plain (non-actor-isolated) storage is safe here.
    private var lastProbe: Date?
    private var lastProbedDelay: Int?
    private var probeFailureCount = 0
    private var probeRetryAfter = Date.distantPast
    private var daemonIdentity: String?
    // A single-server /delay probe is useful feedback, but unlike urltest
    // groups it has no reason to run at the live status cadence. Keep a
    // failed or successful probe out of the hot path for 30 seconds.
    private let probeInterval: TimeInterval = 30

    // MARK: - Status / lifecycle

    func status() async throws -> DaemonStatus {
        let fetched = try await control.status()
        let identity = fetched.pid > 0 ? "\(fetched.pid):\(fetched.startedAt)" : nil
        if daemonIdentity != nil && identity != daemonIdentity {
            resetLatencyProbe()
        }
        daemonIdentity = identity
        return fetched
    }

    func setMode(_ mode: String) async throws {
        resetLatencyProbe()
        try await control.setMode(mode)
    }

    func stop() async throws {
        try await control.stop()
    }

    // MARK: - Observability

    /// Mirrors the pre-`Backend` LiveStore.tick(): TRAFFIC was only ever
    /// queried while the daemon is live AND the Clash API is enabled (the two
    /// were gated by the same `endpoint.clashEnabled` check), so preserve
    /// that gate here even though the TRAFFIC verb itself doesn't need Clash.
    func traffic() async throws -> Traffic {
        guard let endpoint = InstanceDiscovery.currentEndpoint(), endpoint.clashEnabled else {
            throw ControlClientError.noDaemon
        }
        return try await control.traffic()
    }

    /// Mirrors LiveStore.tick()'s latency handling exactly: only produces a
    /// value when both the Clash API call succeeds AND a failover group
    /// ("proxy") is present, so a transient/absent group leaves the
    /// last-known `LiveStore.latency` untouched (the caller wraps this in
    /// `try?`) instead of resetting it to `.empty`.
    ///
    /// A single VLESS key configures `proxy` as a standalone outbound rather
    /// than a urltest group, so sing-box never auto-probes it: its Clash
    /// `history` stays empty and `ClashClient.latency(from:)` reports a
    /// `delay` of 0 ("timeout") forever, even though the proxy works fine.
    /// Fix: when the selected row comes back unprobed, actively hit Clash's
    /// `/proxies/{tag}/delay` (`ClashClient.delay`) and splice the result
    /// into that row. Throttled to once per `probeInterval` (~10s) — NOT on
    /// every 2s LiveStore tick — reusing the last probed delay in between so
    /// the UI doesn't flicker back to "timeout" while waiting for the next
    /// probe window.
    func latency() async throws -> Latency {
        let client = try clashClient()
        let proxies = try await client.proxies()
        guard var lat = ClashClient.latency(from: proxies) else {
            throw ControlClientError.badReply("no failover group in /proxies")
        }

        if let idx = lat.rows.firstIndex(where: { $0.selected }), lat.rows[idx].delay <= 0 {
            let now = Date()
            if now < probeRetryAfter {
                if (lat.rows[idx].error ?? "").isEmpty,
                   let lastProbedDelay, lastProbedDelay > 0 {
                    lat.rows[idx].delay = lastProbedDelay
                }
            } else {
                lastProbe = now
                let testURL = await probeURL()
                do {
                    let delay = try await client.delay(tag: lat.rows[idx].tag, url: testURL, timeoutMs: 3000)
                    if delay > 0 {
                        lastProbedDelay = delay
                        probeFailureCount = 0
                        probeRetryAfter = now.addingTimeInterval(probeInterval)
                        lat.rows[idx].delay = delay
                        lat.rows[idx].error = nil
                    }
                } catch {
                    lastProbedDelay = nil
                    probeFailureCount = min(probeFailureCount + 1, 4)
                    let backoff = min(300.0, probeInterval * pow(2.0, Double(probeFailureCount - 1)))
                    probeRetryAfter = now.addingTimeInterval(backoff)
                    lat.rows[idx].delay = 0
                    // sing-box records typed REALITY failures in proxy history;
                    // refetch it so the UI can show the actionable server rejection.
                    if let refreshed = try? await client.proxies(),
                       let refreshedLat = ClashClient.latency(from: refreshed),
                       let refreshedRow = refreshedLat.rows.first(where: { $0.tag == lat.rows[idx].tag }) {
                        lat.rows[idx].error = refreshedRow.error
                    }
                }
            }
        }

        return lat
    }

    private func resetLatencyProbe() {
        lastProbe = nil
        lastProbedDelay = nil
        probeFailureCount = 0
        probeRetryAfter = .distantPast
    }

    /// The URL to actively probe with, mirroring Settings.URLTestURL when
    /// available (falls back to sing-box's own default probe target).
    private func probeURL() async -> String {
        if let settings = try? await control.settingsGet(), !settings.urlTestURL.isEmpty {
            return settings.urlTestURL
        }
        return "http://www.gstatic.com/generate_204"
    }

    func connections() async throws -> ClashConnections {
        try await clashClient().connections()
    }

    func connectionsDetail() async throws -> ConnectionsPayload {
        try await control.connectionsDetail()
    }

    func closeConnection(_ id: String) async throws {
        try await control.closeConnection(id)
    }

    /// Builds a fresh `ClashClient` from the live daemon endpoint, throwing
    /// `.noDaemon` when there is none or the Clash API is disabled — mirrors
    /// the `guard endpoint.clashEnabled else { return }` that used to gate
    /// all of traffic/connections/latency together in LiveStore.tick().
    private func clashClient() throws -> ClashClient {
        guard let endpoint = InstanceDiscovery.currentEndpoint(), endpoint.clashEnabled else {
            throw ControlClientError.noDaemon
        }
        return ClashClient(externalController: endpoint.clashAPIAddr, secret: endpoint.clashSecret)
    }

    // MARK: - Proxy failover group

    func proxyGroup() async throws -> ProxyGroup {
        try await control.proxyGroup()
    }

    func proxySelect(_ tag: String) async throws {
        resetLatencyProbe()
        try await control.proxySelect(tag)
    }

    // MARK: - Keys (VLESS links)

    func keysGet() async throws -> [String] {
        try await control.keysGet()
    }

    func keysAdd(_ link: String) async throws {
        resetLatencyProbe()
        try await control.keysAdd(link)
    }

    func keysAddConfig(_ config: String) async throws {
        resetLatencyProbe()
        try await control.keysAddConfig(config)
    }

    func keysRemove(_ index: Int) async throws {
        resetLatencyProbe()
        try await control.keysRemove(index)
    }

    func keysRename(_ index: Int, _ name: String) async throws {
        resetLatencyProbe()
        try await control.keysRename(index, name)
    }

    // MARK: - Subscriptions

    func subList() async throws -> [Subscription] {
        try await control.subList()
    }

    func subAdd(_ url: String) async throws {
        resetLatencyProbe()
        try await control.subAdd(url)
    }

    func subRemove(_ url: String) async throws {
        resetLatencyProbe()
        try await control.subRemove(url)
    }

    func subUpdate() async throws -> Int {
        resetLatencyProbe()
        return try await control.subUpdate()
    }

    // MARK: - Settings

    func settingsGet() async throws -> Settings {
        try await control.settingsGet()
    }

    func settingsSet(_ settings: Settings) async throws {
        try await control.settingsSet(settings)
    }

    // MARK: - System proxy

    func sysProxyStatus() async throws -> SysProxyStatus {
        try await control.sysProxyStatus()
    }

    func sysProxyConfig() async throws -> String {
        try await control.sysProxyConfig()
    }

    func sysProxySet(mode: String) async throws {
        try await control.sysProxySet(mode: mode)
    }

    func sysProxyImport(_ text: String) async throws {
        try await control.sysProxyImport(text)
    }

    // MARK: - Firewall

    func firewallList() async throws -> [FirewallRule] {
        try await control.firewallList()
    }

    func firewallAdd(_ rule: FirewallRule) async throws -> FirewallRule {
        try await control.firewallAdd(rule)
    }

    func firewallRemove(_ id: String) async throws {
        try await control.firewallRemove(id)
    }
}
#endif
