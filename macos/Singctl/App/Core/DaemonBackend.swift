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
    func latency() async throws -> Latency {
        let proxies = try await clashClient().proxies()
        guard let lat = ClashClient.latency(from: proxies) else {
            throw ControlClientError.badReply("no failover group in /proxies")
        }
        return lat
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
