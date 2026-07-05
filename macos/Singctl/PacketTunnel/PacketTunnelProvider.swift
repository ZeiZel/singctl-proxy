// PacketTunnelProvider.swift — App Store SKU VPN datapath (STUB, still to be
// wired to a real datapath).
//
// An NEPacketTunnelProvider that will run the sing-box core (via the
// gomobile-built Libbox) over the tunnel. Mirrors sing-box's official Apple
// app. The sing-box config JSON is built by the Go `mobile/` shim from the
// user's VLESS key(s) + settings (reusing internal/vless + internal/singbox)
// and stored in the App Group container by the SwiftUI app (App/). See
// ../../../docs/appstore-sku.md.
//
// Part of the unified Singctl.xcodeproj (macos/Singctl/project.yml), built by
// the PacketTunnel app-extension target embedded in SingctlAppStore.app. This
// compiles today; the real Libbox datapath is a later step (see TODOs below).

import Foundation
import NetworkExtension
import os.log
// import Libbox   // gomobile-built; add Libbox.xcframework to the target first.

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let log = OSLog(subsystem: "com.singctl.appstore.tunnel", category: "tunnel")

    // The running sing-box instance (Libbox handle). Typed `Any?` here only
    // because Libbox is not importable in this scaffold.
    private var boxService: Any?

    override func startTunnel(options: [String: NSObject]? = nil,
                              completionHandler: @escaping (Error?) -> Void) {
        guard let configJSON = loadConfigJSON() else {
            completionHandler(NSError(domain: "singctl", code: 1,
                userInfo: [NSLocalizedDescriptionKey: "no config in App Group container"]))
            return
        }

        // TODO (Mac): set tunnel network settings (addresses, DNS, routes) to match
        // the sing-box tun inbound, then start Libbox over self.packetFlow:
        //
        //   let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        //   settings.ipv4Settings = NEIPv4Settings(addresses: ["198.18.0.1"], subnetMasks: ["255.255.255.252"])
        //   settings.ipv4Settings?.includedRoutes = [NEIPv4Route.default()]
        //   settings.dnsSettings = NEDNSSettings(servers: ["1.1.1.1"])
        //   setTunnelNetworkSettings(settings) { error in
        //       if let error { completionHandler(error); return }
        //       self.boxService = LibboxNewService(configJSON, PlatformInterface(self.packetFlow))
        //       try? (self.boxService as? LibboxBoxService)?.start()
        //       completionHandler(nil)
        //   }
        _ = configJSON
        os_log("startTunnel: scaffold — wire Libbox + setTunnelNetworkSettings", log: log, type: .error)
        completionHandler(NSError(domain: "singctl", code: 2,
            userInfo: [NSLocalizedDescriptionKey: "PacketTunnelProvider is a scaffold"]))
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        os_log("stopTunnel reason=%d", log: log, type: .info, reason.rawValue)
        // try? (boxService as? LibboxBoxService)?.close()
        boxService = nil
        completionHandler()
    }

    /// Reads the sing-box config JSON the SwiftUI app wrote to the App Group
    /// container (built by the Go `mobile.BuildConfig` shim from the user's keys).
    private func loadConfigJSON() -> String? {
        guard let url = FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: "group.com.singctl.appstore")?
            .appendingPathComponent("config.json"),
              let data = try? Data(contentsOf: url) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
