// TransparentProxyProvider.swift — singctl macOS System Extension (SCAFFOLD).
//
// A NETransparentProxyProvider that captures the flows of selected apps and
// relays them to singctl's local SOCKS proxy. This is the only macOS mechanism
// that catches EVERY network stack of an app (Chromium, Node/undici, raw
// sockets) without taking over the whole default route — unlike the Go-side
// --proxy-server / HTTP_PROXY levers, which only cover part of Cursor/VS Code.
//
// STATUS: skeleton. It compiles against the NetworkExtension framework conceptually
// but has not been built/signed/run. See README.md and every `// TODO`.

import Foundation
import NetworkExtension
import os.log

final class TransparentProxyProvider: NETransparentProxyProvider {
    private let log = OSLog(subsystem: "com.singctl.proxy.netext", category: "provider")

    /// Bundle IDs / executable paths whose flows we capture. Loaded from the
    /// provider configuration (set by the container app) and/or config.json.
    private var targets: Set<String> = []

    /// The SOCKS proxy singctl runs locally (default 127.0.0.1:1080).
    private var socksHost = "127.0.0.1"
    private var socksPort: UInt16 = 1080

    /// Cisco-yield: mirrors internal/policy observe-only behaviour. When the Go
    /// side reports AnyConnect active (config.json `ciscoActive`), we capture
    /// nothing so we never fight its routing. SCAFFOLD: untested on device.
    private var ciscoActive = false

    // MARK: - Lifecycle

    override func startProxy(options: [String: Any]? = nil,
                            completionHandler: @escaping (Error?) -> Void) {
        loadConfiguration(options: options)

        // We declare interest in all TCP/UDP to every destination, then decide
        // per-flow in handleNewFlow whether the SOURCE APP matches `targets`.
        // (Transparent proxies filter by flow; the app match is what scopes us.)
        let settings = NETransparentProxyNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        let tcp = NENetworkRule(remoteNetwork: nil, remotePrefix: 0,
                                localNetwork: nil, localPrefix: 0,
                                protocol: .TCP, direction: .outbound)
        let udp = NENetworkRule(remoteNetwork: nil, remotePrefix: 0,
                                localNetwork: nil, localPrefix: 0,
                                protocol: .UDP, direction: .outbound)
        settings.includedNetworkRules = [tcp, udp]

        setTunnelNetworkSettings(settings) { [weak self] error in
            if let error = error {
                os_log("setTunnelNetworkSettings failed: %{public}@",
                       log: self?.log ?? .default, type: .error, error.localizedDescription)
            }
            completionHandler(error)
        }
    }

    override func stopProxy(with reason: NEProviderStopReason,
                           completionHandler: @escaping () -> Void) {
        os_log("stopProxy reason=%d", log: log, type: .info, reason.rawValue)
        completionHandler()
    }

    // MARK: - Flow handling

    /// The system hands us every new flow. We capture (return true) only flows
    /// whose source app is in `targets`; everything else returns false and goes
    /// direct — that is what makes this PER-APP rather than system-wide.
    override func handleNewFlow(_ flow: NEAppProxyFlow) -> Bool {
        guard shouldCapture(flow) else { return false }

        if let tcp = flow as? NEAppProxyTCPFlow {
            FlowRelay.relayTCP(tcp, toSOCKS: socksHost, port: socksPort, log: log)
            return true
        }
        if let udp = flow as? NEAppProxyUDPFlow {
            // TODO: implement UDP relay (SOCKS5 UDP ASSOCIATE). For now decline
            // UDP so DNS/QUIC fall back to direct rather than black-holing.
            _ = udp
            return false
        }
        return false
    }

    // MARK: - Matching & config

    /// True when the flow's source app is one we were told to capture. The
    /// signing identifier is the app's bundle ID (covers Electron helper
    /// processes, which share the parent's signing identifier).
    private func shouldCapture(_ flow: NEAppProxyFlow) -> Bool {
        // Cisco coexistence: yield entirely while AnyConnect is active (the Go
        // side sets ciscoActive in config.json from internal/policy).
        if ciscoActive { return false }
        let appID = flow.metaData.sourceAppSigningIdentifier
        if !appID.isEmpty, targets.contains(appID) { return true }
        return false
    }

    private func loadConfiguration(options: [String: Any]?) {
        // 1) providerConfiguration set by the container app via the manager.
        if let conf = (protocolConfiguration as? NETunnelProviderProtocol)?.providerConfiguration {
            apply(conf)
        }
        // 2) start options can override (e.g. for a quick reconfigure).
        if let options = options { apply(options) }
        // 3) optional config.json in the shared container (live updates).
        loadConfigFile()
        os_log("config: %d target(s), socks=%{public}@:%d",
               log: log, type: .info, targets.count, socksHost, Int(socksPort))
    }

    private func apply(_ dict: [String: Any]) {
        if let t = dict["targets"] as? [String] { targets = Set(t) }
        if let h = dict["socksHost"] as? String, !h.isEmpty { socksHost = h }
        if let p = dict["socksPort"] as? Int, p > 0, p < 65536 { socksPort = UInt16(p) }
        if let cisco = dict["ciscoActive"] as? Bool { ciscoActive = cisco }
    }

    private func loadConfigFile() {
        // TODO: point at the App Group shared container path and watch for changes
        // so singctl can update `targets` without reinstalling the extension.
        guard let url = TransparentProxyProvider.sharedConfigURL,
              let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return }
        apply(obj)
    }

    static var sharedConfigURL: URL? {
        // TODO: replace with the real App Group identifier.
        FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: "group.com.singctl.proxy")?
            .appendingPathComponent("config.json")
    }
}
