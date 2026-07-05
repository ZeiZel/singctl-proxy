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
    private let probeInterval: TimeInterval = 10

    // MARK: - Status / lifecycle

    func status() async throws -> DaemonStatus {
        try await control.status()
    }

    func setMode(_ mode: String) async throws {
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
            if let lastProbe, now.timeIntervalSince(lastProbe) < probeInterval {
                if let lastProbedDelay, lastProbedDelay > 0 {
                    lat.rows[idx].delay = lastProbedDelay
                }
            } else {
                lastProbe = now
                let testURL = await probeURL()
                if let delay = try? await client.delay(tag: lat.rows[idx].tag, url: testURL, timeoutMs: 3000),
                   delay > 0 {
                    lastProbedDelay = delay
                    lat.rows[idx].delay = delay
                }
            }
        }

        return lat
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

    // MARK: - Keys (VLESS links)

    func keysGet() async throws -> [String] {
        try await control.keysGet()
    }

    func keysAdd(_ link: String) async throws {
        try await control.keysAdd(link)
    }

    func keysRemove(_ index: Int) async throws {
        try await control.keysRemove(index)
    }

    func keysRename(_ index: Int, _ name: String) async throws {
        try await control.keysRename(index, name)
    }

    // MARK: - Settings

    func settingsGet() async throws -> Settings {
        try await control.settingsGet()
    }

    func settingsSet(_ settings: Settings) async throws {
        try await control.settingsSet(settings)
    }
}
#endif
