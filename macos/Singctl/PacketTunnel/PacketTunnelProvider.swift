// PacketTunnelProvider.swift — App Store SKU VPN datapath.
//
// An NEPacketTunnelProvider running the sing-box core over the tunnel fd via
// Libbox (sing-box v1.13.12's gomobile binding, built into
// Libbox.xcframework by the Makefile from github.com/sagernet/sing-box/
// experimental/libbox + this repo's ./mobile shim — see project.yml and
// docs/appstore-sku.md).
//
// IMPORTANT — this does NOT use the classic `LibboxNewService`/
// `LibboxBoxService.start()/.close()` shape some older sing-box docs (and
// sing-box-for-apple's own `main` branch, which tracks a newer/renamed
// sing-box) describe. Reading the *pinned* v1.13.12 sources directly
// (experimental/libbox/service.go, command_server.go, setup.go) shows that
// version's actual entry point:
//   - `LibboxSetup(options, &error)`                         — base/working/temp dirs (setup.go)
//   - `LibboxNewCommandServer(handler, platformInterface, &error) -> LibboxCommandServer?`
//     (command_server.go: `NewCommandServer(handler CommandServerHandler,
//     platformInterface PlatformInterface)`) — `handler` and
//     `platformInterface` are the same object (ExtensionPlatformInterface),
//     matching how sing-box's own Apple app does it.
//   - `commandServer.startOrReloadService(configJSON, options:)` is what
//     actually parses the config and starts the sing-box instance (including
//     calling into `PlatformInterface.openTun` — see
//     ExtensionPlatformInterface.swift). `CommandServer.start()` merely opens
//     an *optional* local control socket (command.sock, for a companion app
//     to query live stats over) — nothing in this datapath needs it, so it's
//     deliberately not called (see the open-risk note in the report).
//
// Part of the unified Singctl.xcodeproj (macos/Singctl/project.yml), built by
// the PacketTunnel app-extension target embedded in SingctlAppStore.app.

import Foundation
import NetworkExtension
import Libbox
import os.log

final class PacketTunnelProvider: NEPacketTunnelProvider {
    private let log = OSLog(subsystem: "com.singctl.appstore.tunnel", category: "tunnel")

    private static let appGroupID = "group.com.singctl.appstore"

    // Implements both LibboxPlatformInterfaceProtocol (openTun etc.) and
    // LibboxCommandServerHandlerProtocol (serviceStop etc.) — see
    // ExtensionPlatformInterface.swift for why one object does both.
    private var platformInterface: ExtensionPlatformInterface?
    private var commandServer: LibboxCommandServer?

    override func startTunnel(options: [String: NSObject]? = nil,
                              completionHandler: @escaping (Error?) -> Void) {
        guard let tunnelConfigJSON = loadConfigJSON() else {
            let error = NSError(domain: "singctl", code: 1,
                                userInfo: [NSLocalizedDescriptionKey: "no config in App Group container"])
            os_log("startTunnel: %{public}@", log: log, type: .error, error.localizedDescription)
            completionHandler(error)
            return
        }

        // config.json in the App Group container is TunnelBackend's
        // TunnelConfig (mode/keys/settings), not a sing-box config — turn it
        // into one via the repo-local `mobile` gomobile shim. mobile.BuildConfig
        // returns (string, error) in Go; as a plain top-level function (not a
        // reverse-bound protocol method) that binds to the NSErrorPointer
        // form, not `throws`.
        var buildError: NSError?
        let configJSON = MobileBuildConfig(tunnelConfigJSON, &buildError)
        if let buildError {
            os_log("startTunnel: MobileBuildConfig failed: %{public}@", log: log, type: .error,
                   buildError.localizedDescription)
            completionHandler(buildError)
            return
        }

        guard let containerURL = FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: Self.appGroupID) else {
            let error = NSError(domain: "singctl", code: 2,
                                userInfo: [NSLocalizedDescriptionKey: "no App Group container"])
            os_log("startTunnel: %{public}@", log: log, type: .error, error.localizedDescription)
            completionHandler(error)
            return
        }

        // Setup(options) (setup.go) wants base/working/temp directories to
        // exist; base is the container root itself (also where CommandServer
        // would put command.sock, if we ever start it), working/temp are
        // subdirectories sing-box uses for cache/rule-set storage etc.
        let basePath = containerURL.path
        let workingPath = containerURL.appendingPathComponent("Working").path
        let tempPath = containerURL.appendingPathComponent("Temp").path
        do {
            try FileManager.default.createDirectory(atPath: workingPath, withIntermediateDirectories: true)
            try FileManager.default.createDirectory(atPath: tempPath, withIntermediateDirectories: true)
        } catch {
            os_log("startTunnel: create working/temp dirs failed: %{public}@", log: log, type: .error,
                   error.localizedDescription)
            completionHandler(error)
            return
        }

        let setupOptions = LibboxSetupOptions()
        setupOptions.basePath = basePath
        setupOptions.workingPath = workingPath
        setupOptions.tempPath = tempPath
        setupOptions.logMaxLines = 3000

        var setupError: NSError?
        LibboxSetup(setupOptions, &setupError)
        if let setupError {
            os_log("startTunnel: LibboxSetup failed: %{public}@", log: log, type: .error,
                   setupError.localizedDescription)
            completionHandler(setupError)
            return
        }

        let iface = ExtensionPlatformInterface(provider: self)
        platformInterface = iface

        var serverError: NSError?
        let server = LibboxNewCommandServer(iface, iface, &serverError)
        if let serverError {
            os_log("startTunnel: LibboxNewCommandServer failed: %{public}@", log: log, type: .error,
                   serverError.localizedDescription)
            platformInterface = nil
            completionHandler(serverError)
            return
        }
        guard let server else {
            let error = NSError(domain: "singctl", code: 3,
                                userInfo: [NSLocalizedDescriptionKey: "LibboxNewCommandServer returned nil"])
            os_log("startTunnel: %{public}@", log: log, type: .error, error.localizedDescription)
            platformInterface = nil
            completionHandler(error)
            return
        }
        commandServer = server

        do {
            // This is the call that actually parses configJSON, builds the
            // sing-box instance, and starts it — including the
            // PlatformInterface.openTun round-trip that configures
            // NEPacketTunnelNetworkSettings and hands back the tun fd.
            try server.startOrReloadService(configJSON, options: LibboxOverrideOptions())
        } catch {
            os_log("startTunnel: startOrReloadService failed: %{public}@", log: log, type: .error,
                   error.localizedDescription)
            commandServer = nil
            platformInterface = nil
            completionHandler(error)
            return
        }

        os_log("startTunnel: sing-box started", log: log, type: .info)
        completionHandler(nil)
    }

    override func stopTunnel(with reason: NEProviderStopReason,
                             completionHandler: @escaping () -> Void) {
        os_log("stopTunnel reason=%d", log: log, type: .info, reason.rawValue)
        do {
            try commandServer?.closeService()
        } catch {
            os_log("stopTunnel: closeService failed: %{public}@", log: log, type: .error,
                   error.localizedDescription)
        }
        commandServer?.close()
        commandServer = nil
        platformInterface?.reset()
        platformInterface = nil
        completionHandler()
    }

    // MARK: - Sleep/wake pass-through
    //
    // CommandServer.pause()/.wake() (command_server.go) drive
    // PauseManager.DevicePause()/DeviceWake() on the running box instance —
    // sing-box's own way of quiescing timers/keepalives while macOS suspends
    // the extension, so this is a direct pass-through rather than a no-op.

    override func sleep(completionHandler: @escaping () -> Void) {
        commandServer?.pause()
        completionHandler()
    }

    override func wake() {
        commandServer?.wake()
    }

    override func handleAppMessage(_ messageData: Data, completionHandler: ((Data?) -> Void)? = nil) {
        // No control-channel protocol defined yet between the container app
        // and this appex (TunnelBackend currently always restarts the whole
        // tunnel via VPNController rather than messaging a running one) — ack
        // with nothing so callers don't hang waiting on a reply.
        completionHandler?(nil)
    }

    /// Reads the App Group's config.json (TunnelBackend.TunnelConfig JSON:
    /// mode/keys/settings), written by the SwiftUI app's TunnelConfigStore.
    private func loadConfigJSON() -> String? {
        guard let url = FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: Self.appGroupID)?
            .appendingPathComponent("config.json"),
              let data = try? Data(contentsOf: url) else { return nil }
        return String(data: data, encoding: .utf8)
    }
}
