// VPNController.swift
//
// Thin wrapper over `NETunnelProviderManager` for the App Store SKU's
// packet-tunnel provider (`com.singctl.appstore.tunnel`, embedded as the
// `PacketTunnel` appex — see macos/Singctl/PacketTunnel/). Ported from the
// early scaffold at packaging/macos/appstore/ContainerApp/App.swift's
// `VPNController`; `TunnelBackend` (App/Core/TunnelBackend.swift) drives this
// to start/stop the tunnel and to report connection state for
// `Backend.status()`.
//
// `#if APPSTORE` only — `NETunnelProviderManager`/`NetworkExtension` App-Proxy
// APIs have no equivalent role in the Developer-ID build (which owns a TUN
// device directly from its root daemon).

#if APPSTORE
import Foundation
import NetworkExtension

/// Installs/enables the VPN profile on first use, then starts/stops it and
/// reports its connection state. Holds no state of its own beyond the loaded
/// `NETunnelProviderManager` — `load()` always re-resolves from the system's
/// saved preferences, so multiple `VPNController` instances stay in sync.
@MainActor
final class VPNController {
    private var manager: NETunnelProviderManager?

    /// Nonisolated so `TunnelBackend` (not itself `@MainActor`) can construct
    /// one synchronously (e.g. as a stored-property default, including from
    /// `AppModel`'s `EnvironmentKey.defaultValue`); every actual method below
    /// still hops to the main actor as usual.
    nonisolated init() {}

    /// True once the tunnel is connected or actively connecting.
    var isConnectedOrConnecting: Bool {
        let status = manager?.connection.status ?? .invalid
        return status == .connected || status == .connecting
    }

    /// (Re)loads the on-disk VPN configuration from system preferences,
    /// creating an in-memory one (not yet saved) if none exists yet. Cheap
    /// enough to call before every read (see `TunnelBackend.status()`).
    func load() async {
        let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        manager = managers.first ?? Self.makeManager()
    }

    private static func makeManager() -> NETunnelProviderManager {
        let manager = NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = TunnelBackend.providerBundleID
        proto.serverAddress = "singctl"
        manager.protocolConfiguration = proto
        manager.localizedDescription = "singctl"
        return manager
    }

    /// Enables + saves the profile (installing it on first use, prompting
    /// the user if needed) and starts the tunnel. Best-effort: on failure the
    /// tunnel simply doesn't start, and the next `status()` poll reports
    /// whatever state `NETunnelProviderManager` ends up in.
    func start() async {
        guard let manager else { return }
        do {
            manager.isEnabled = true
            try await manager.saveToPreferences()
            try await manager.loadFromPreferences()
            try manager.connection.startVPNTunnel()
        } catch {
            // Surfaced indirectly: the caller re-reads `status()` afterward.
        }
    }

    func stop() {
        manager?.connection.stopVPNTunnel()
    }
}
#endif
