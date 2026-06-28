// App.swift — App Store SKU container app (SCAFFOLD, untested).
//
// A sandboxed SwiftUI app that installs/controls the packet-tunnel VPN via
// NETunnelProviderManager and edits keys/settings stored in the App Group
// container (read by the appex). Does not compile here (no SwiftUI/NE SDK in the
// Go/Linux container). Complete on a Mac. See ../../../../docs/appstore-sku.md.

import SwiftUI
import NetworkExtension

@main
struct SingctlApp: App {
    var body: some Scene {
        WindowGroup {
            ContentView()
        }
    }
}

struct ContentView: View {
    @StateObject private var vpn = VPNController()

    var body: some View {
        VStack(spacing: 16) {
            Text("singctl").font(.title.bold())
            Text(vpn.statusText).foregroundStyle(.secondary)
            Button(vpn.isOn ? "Disconnect" : "Connect") {
                Task { await vpn.toggle() }
            }
            // TODO (Mac): keys editor + settings + traffic chart (Libbox stats),
            // all persisted to the App Group container as config.json.
        }
        .padding(40)
        .task { await vpn.load() }
    }
}

/// Thin wrapper over NETunnelProviderManager: install the VPN profile, start/stop,
/// observe status. SCAFFOLD — error handling/observers to be completed on a Mac.
@MainActor
final class VPNController: ObservableObject {
    @Published var isOn = false
    @Published var statusText = "Not configured"

    private var manager: NETunnelProviderManager?

    func load() async {
        let managers = (try? await NETunnelProviderManager.loadAllFromPreferences()) ?? []
        manager = managers.first ?? makeManager()
        refresh()
    }

    private func makeManager() -> NETunnelProviderManager {
        let manager = NETunnelProviderManager()
        let proto = NETunnelProviderProtocol()
        proto.providerBundleIdentifier = "com.singctl.appstore.tunnel"
        proto.serverAddress = "singctl"
        manager.protocolConfiguration = proto
        manager.localizedDescription = "singctl"
        return manager
    }

    func toggle() async {
        guard let manager else { return }
        do {
            manager.isEnabled = true
            try await manager.saveToPreferences()
            try await manager.loadFromPreferences()
            if isOn {
                manager.connection.stopVPNTunnel()
            } else {
                try manager.connection.startVPNTunnel()
            }
        } catch {
            statusText = "error: \(error.localizedDescription)"
        }
        refresh()
    }

    private func refresh() {
        let status = manager?.connection.status ?? .invalid
        isOn = (status == .connected || status == .connecting)
        statusText = String(describing: status)
    }
}
