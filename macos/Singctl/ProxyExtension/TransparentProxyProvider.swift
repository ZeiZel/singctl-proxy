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

import Dispatch
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

    /// Live file-watch on the shared config.json so the CLI/container app can
    /// change `targets` without the extension being restarted. This is the
    /// provider's own watch (root-owned container); see ContainerApp/main.swift
    /// for why the container app's watcher alone is not sufficient.
    private var configWatchSource: DispatchSourceFileSystemObject?

    // MARK: - Lifecycle

    override func startProxy(options: [String: Any]? = nil,
                            completionHandler: @escaping (Error?) -> Void) {
        loadConfiguration(options: options)
        startWatchingConfigFile()

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
        stopWatchingConfigFile()
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
            // SOCKS5 UDP ASSOCIATE (RFC 1928 §7) — sing-box's SOCKS inbound
            // supports it, so captured UDP (QUIC/HTTP-3, app-originated DNS)
            // relays the same as TCP instead of leaking direct. Per-app DNS
            // resolved via mDNSResponder is not affected either way: those
            // flows belong to mDNSResponder, not the target app, so they
            // never reach here.
            UDPFlowRelay.relay(udp, toSOCKS: socksHost, port: socksPort, log: log)
            return true
        }
        return false
    }

    // MARK: - Matching & config

    /// True when the flow's source app is one we were told to capture. The
    /// signing identifier is the app's bundle ID (covers Electron helper
    /// processes, which share the parent's signing identifier).
    private func shouldCapture(_ flow: NEAppProxyFlow) -> Bool {
        // Per-app capture stays active regardless of Cisco AnyConnect state;
        // coexistence (suspend/resume of the proxy core) is handled by the Go
        // internal/policy layer — do not reintroduce a capture-yield here
        // (decision 2026-07-01).
        let appID = flow.metaData.sourceAppSigningIdentifier
        let captured = !appID.isEmpty && targets.contains(appID)
        // Debug-only: lets us verify on-device that Electron helper processes
        // (which share the parent's signing identifier) are covered.
        os_log("flow sourceAppSigningIdentifier=%{public}@ -> %{public}@",
               log: log, type: .debug, appID, captured ? "captured" : "declined")
        return captured
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
    }

    private func loadConfigFile() {
        guard let url = TransparentProxyProvider.sharedConfigURL,
              let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return }
        apply(obj)
    }

    // MARK: - Config file watch

    /// Start watching the shared config.json for live updates. The provider
    /// runs as root, so this watch (unlike the container app's) sees the file
    /// singctl actually writes to (/var/root/Library/Group Containers/...) and
    /// is the authoritative live-reload channel.
    private func startWatchingConfigFile() {
        guard let url = TransparentProxyProvider.sharedConfigURL else { return }
        openConfigWatch(at: url)
    }

    private func stopWatchingConfigFile() {
        configWatchSource?.cancel()
        configWatchSource = nil
    }

    /// Opens a DispatchSourceFileSystemObject on `url` and re-applies the
    /// config on every write/rename/delete. Atomic writes (write-tmp +
    /// rename-over-target) replace the inode, so on rename/delete we cancel
    /// the stale fd and reopen a fresh one on the (new) file, mirroring
    /// ContainerApp/main.swift's watchConfig.
    private func openConfigWatch(at url: URL) {
        let fd = open(url.path, O_EVTONLY)
        guard fd >= 0 else {
            os_log("cannot watch config %{public}@ (errno %d)", log: log, type: .error, url.path, errno)
            return
        }
        let source = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd, eventMask: [.write, .rename, .delete], queue: .main)
        source.setEventHandler { [weak self] in
            guard let self = self else { return }
            let flags = source.data
            self.loadConfigFile()
            os_log("config.json changed, reapplied (%d target(s))",
                   log: self.log, type: .debug, self.targets.count)
            if flags.contains(.rename) || flags.contains(.delete) {
                self.configWatchSource = nil
                source.cancel()
                DispatchQueue.main.async { [weak self] in self?.openConfigWatch(at: url) }
            }
        }
        source.setCancelHandler { close(fd) }
        source.resume()
        configWatchSource = source
    }

    static var sharedConfigURL: URL? {
        // TODO: replace with the real App Group identifier.
        FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: "group.com.singctl.proxy")?
            .appendingPathComponent("config.json")
    }
}
