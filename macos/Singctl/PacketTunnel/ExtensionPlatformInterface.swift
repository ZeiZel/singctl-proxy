// ExtensionPlatformInterface.swift — the Swift-side callback surface Libbox
// (sing-box's gomobile binding) needs from the host platform.
//
// Ground truth for every method name/signature below is sing-box v1.13.12's
// experimental/libbox package, read directly from the pinned module cache
// (not guessed, and NOT the same as upstream sing-box-for-apple's `main`
// branch, which binds against a newer/renamed sing-box and drifts on a few
// names — see the note by each drifted method):
//   - platform.go      → `PlatformInterface` (15 methods)              → `LibboxPlatformInterfaceProtocol`
//   - command_server.go → `CommandServerHandler` (5 methods)           → `LibboxCommandServerHandlerProtocol`
//   - tun_options.go    → `TunOptions` / `RoutePrefix` accessors       → `LibboxTunOptionsProtocol` / `LibboxRoutePrefix`
//   - iterator.go       → `StringIterator`/generic iterators           → `LibboxStringIteratorProtocol` etc.
//
// Why one class implements two protocols: `LibboxNewCommandServer(handler,
// platformInterface)` needs both at construction time, and sing-box's own
// Apple app satisfies that by implementing both on a single object — we do
// the same rather than inventing a second type.
//
// This is deliberately much thinner than sing-box-for-apple's version: no
// XPC/system-extension variant, no per-app routing, no iOS/tvOS branches, no
// WiFi/notification wiring yet — every inapplicable method returns an inert
// default (nil / false / "not supported" error), which is what a sandboxed,
// single-tun-instance App Store appex needs today.

import Foundation
import Network
import NetworkExtension
import Libbox
import os.log

final class ExtensionPlatformInterface: NSObject, LibboxPlatformInterfaceProtocol, LibboxCommandServerHandlerProtocol {
    private let log = OSLog(subsystem: "com.singctl.appstore.tunnel", category: "platform")

    // Weak: the provider owns us (via PacketTunnelProvider.platformInterface),
    // not the other way around.
    private weak var provider: PacketTunnelProvider?

    // Retained so getSystemProxyStatus/setSystemProxyEnabled (called later,
    // from the command-server handler side) can see what openTun configured.
    private var networkSettings: NEPacketTunnelNetworkSettings?
    private var nwMonitor: NWPathMonitor?

    init(provider: PacketTunnelProvider) {
        self.provider = provider
    }

    /// Called from `stopTunnel` so a restarted tunnel doesn't reuse stale
    /// settings/monitor state if this object were ever reused (it isn't
    /// today — PacketTunnelProvider makes a fresh one per startTunnel — but
    /// mirrors sing-box-for-apple's own `reset()` in case that changes).
    func reset() {
        networkSettings = nil
        nwMonitor?.cancel()
        nwMonitor = nil
    }

    // MARK: - LibboxPlatformInterfaceProtocol

    func localDNSTransport() -> (any LibboxLocalDNSTransportProtocol)? {
        // mobile.BuildConfig always emits a config with its own DNS
        // server/resolver settings (see internal/singbox); we never hand off
        // "local" (platform-delegated) DNS resolution.
        nil
    }

    // Note: the Go/ObjC side names these usePlatformAutoDetectInterfaceControl
    // / autoDetectInterfaceControl (see platform.go) — Swift's importer
    // applies its "omit needless words" heuristic to the generated ObjC
    // selectors and requires the shorter names below instead (confirmed by
    // the actual compiler error against the built Libbox.xcframework, not a
    // guess).

    func usePlatformAutoDetectControl() -> Bool {
        // Let sing-tun bind outbound sockets to the default interface itself
        // (control.BindToInterface) instead of routing every socket() through
        // this callback.
        true
    }

    func autoDetectControl(_ fd: Int32) throws {
        // Never actually invoked while usePlatformAutoDetectControl() returns
        // true (see above) — present only because the protocol requires it.
    }

    /// The one method that actually stands up the tunnel: build
    /// `NEPacketTunnelNetworkSettings` from `options`, hand them to the
    /// system, then return the raw TUN file descriptor Libbox will read/write
    /// packets on.
    ///
    /// `options` maps 1:1 to tun_options.go's `TunOptions` interface — scalar
    /// getters (getMTU/getAutoRoute/...) plus `RoutePrefixIterator`s for the
    /// address/route lists. Go's `(int32, error)` return becomes, on the
    /// Swift side of this *reverse* binding (Swift implementing a Go
    /// interface), `throws` + an `UnsafeMutablePointer<Int32>` out-param —
    /// gomobile's ObjC bridge can't return a bare scalar and an error from
    /// the same call, so the scalar goes out via pointer while the error
    /// rides the normal `throws` channel.
    func openTun(_ options: LibboxTunOptionsProtocol?, ret0_: UnsafeMutablePointer<Int32>?) throws {
        guard let options else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 1,
                          userInfo: [NSLocalizedDescriptionKey: "openTun: nil options"])
        }
        guard let ret0_ else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 2,
                          userInfo: [NSLocalizedDescriptionKey: "openTun: nil return pointer"])
        }
        guard let provider else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 3,
                          userInfo: [NSLocalizedDescriptionKey: "openTun: provider deallocated"])
        }

        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")

        // internal/singbox always sets auto_route=true (see
        // generate_tunnel.go) for this SKU's full-device VPN mode, so this
        // branch is the only one exercised in practice — mirrored from
        // sing-box-for-apple's own ExtensionPlatformInterface, which nests
        // address/DNS/route assignment the same way.
        if options.getAutoRoute() {
            settings.mtu = NSNumber(value: options.getMTU())

            if let dnsBox = try? options.getDNSServerAddress() {
                settings.dnsSettings = NEDNSSettings(servers: [dnsBox.value])
            }

            var ipv4Addresses: [String] = []
            var ipv4Masks: [String] = []
            if let it = options.getInet4Address() {
                while it.hasNext() {
                    guard let prefix = it.next() else { break }
                    ipv4Addresses.append(prefix.address())
                    ipv4Masks.append(prefix.mask())
                }
            }
            if !ipv4Addresses.isEmpty {
                let ipv4Settings = NEIPv4Settings(addresses: ipv4Addresses, subnetMasks: ipv4Masks)
                ipv4Settings.includedRoutes = [NEIPv4Route.default()]
                settings.ipv4Settings = ipv4Settings
            }

            var ipv6Addresses: [String] = []
            var ipv6PrefixLengths: [NSNumber] = []
            if let it = options.getInet6Address() {
                while it.hasNext() {
                    guard let prefix = it.next() else { break }
                    ipv6Addresses.append(prefix.address())
                    ipv6PrefixLengths.append(NSNumber(value: prefix.prefix()))
                }
            }
            if !ipv6Addresses.isEmpty {
                let ipv6Settings = NEIPv6Settings(addresses: ipv6Addresses, networkPrefixLengths: ipv6PrefixLengths)
                ipv6Settings.includedRoutes = [NEIPv6Route.default()]
                settings.ipv6Settings = ipv6Settings
            }
        }

        if options.isHTTPProxyEnabled() {
            let proxyServer = NEProxyServer(address: options.getHTTPProxyServer(),
                                             port: Int(options.getHTTPProxyServerPort()))
            let proxySettings = NEProxySettings()
            proxySettings.httpServer = proxyServer
            proxySettings.httpsServer = proxyServer
            settings.proxySettings = proxySettings
        }

        networkSettings = settings

        // setTunnelNetworkSettings is completion-handler-based; openTun is a
        // synchronous throwing callback invoked off Libbox's own goroutine
        // (not the extension's main thread), so blocking here on a semaphore
        // is safe and keeps this method's control flow linear.
        let semaphore = DispatchSemaphore(value: 0)
        var settingsError: Error?
        provider.setTunnelNetworkSettings(settings) { error in
            settingsError = error
            semaphore.signal()
        }
        semaphore.wait()
        if let settingsError {
            os_log("openTun: setTunnelNetworkSettings failed: %{public}@", log: log, type: .error,
                   settingsError.localizedDescription)
            throw settingsError
        }

        // No public API exposes the kernel utun fd behind NEPacketTunnelFlow;
        // this KVC path is what sing-box's own macOS appex uses (see
        // sing-box-for-apple's ExtensionPlatformInterface.swift).
        guard let tunFd = provider.packetFlow.value(forKeyPath: "socket.fileDescriptor") as? Int32 else {
            throw NSError(domain: "ExtensionPlatformInterface", code: 4,
                          userInfo: [NSLocalizedDescriptionKey: "openTun: could not read tunnel fd from packetFlow"])
        }
        ret0_.pointee = tunFd
    }

    func useProcFS() -> Bool {
        false
    }

    func findConnectionOwner(_ ipProtocol: Int32, sourceAddress: String?, sourcePort: Int32,
                              destinationAddress: String?, destinationPort: Int32) throws -> LibboxConnectionOwner {
        // Per-process attribution needs either /proc (Linux/Android, useProcFS
        // == false above) or a privileged root helper (the Developer-ID
        // build's daemon) — neither exists in a sandboxed App Store appex.
        throw NSError(domain: "ExtensionPlatformInterface", code: 5,
                      userInfo: [NSLocalizedDescriptionKey: "findConnectionOwner: not supported in a sandboxed appex"])
    }

    func startDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {
        guard let listener else { return }
        let monitor = NWPathMonitor()
        nwMonitor = monitor
        // Block until the first path update so sing-box's router sees a
        // default interface before StartOrReloadService returns (mirrors
        // sing-box-for-apple's semaphore-gated first update).
        let semaphore = DispatchSemaphore(value: 0)
        var firstUpdate = true
        monitor.pathUpdateHandler = { [weak self] path in
            self?.reportDefaultInterface(listener, path)
            if firstUpdate {
                firstUpdate = false
                semaphore.signal()
            }
        }
        monitor.start(queue: DispatchQueue.global(qos: .utility))
        semaphore.wait()
    }

    private func reportDefaultInterface(_ listener: LibboxInterfaceUpdateListenerProtocol, _ path: Network.NWPath) {
        guard path.status != .unsatisfied, let iface = path.availableInterfaces.first else {
            listener.updateDefaultInterface("", interfaceIndex: -1, isExpensive: false, isConstrained: false)
            return
        }
        listener.updateDefaultInterface(iface.name, interfaceIndex: Int32(iface.index),
                                         isExpensive: path.isExpensive, isConstrained: path.isConstrained)
    }

    func closeDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {
        nwMonitor?.cancel()
        nwMonitor = nil
    }

    func getInterfaces() throws -> LibboxNetworkInterfaceIteratorProtocol {
        // Used by sing-box for outbound-interface enumeration (per-interface
        // routing rules) we don't expose in this SKU yet; an empty iterator
        // is a valid, inert answer.
        EmptyNetworkInterfaceIterator()
    }

    func underNetworkExtension() -> Bool {
        true
    }

    func includeAllNetworks() -> Bool {
        false
    }

    func readWIFIState() -> LibboxWIFIState? {
        nil
    }

    func systemCertificates() -> (any LibboxStringIteratorProtocol)? {
        // nil → sing-box falls back to Go's own system cert pool, which is
        // sufficient for outbound TLS from within the sandboxed appex.
        nil
    }

    func clearDNSCache() {
        // No system-wide DNS cache to invalidate from a sandboxed appex; the
        // sing-box DNS client keeps (and can be told to drop) its own cache
        // internally.
    }

    // Go/ObjC: SendNotification(notification *Notification) error — Swift's
    // importer renames this to send(_:) (same "omit needless words" reason
    // as autoDetectControl above).
    func send(_ notification: LibboxNotification?) throws {
        // No UNUserNotificationCenter wiring yet for the App Store SKU —
        // silently drop rather than fail service startup over it.
    }

    // MARK: - LibboxCommandServerHandlerProtocol

    func serviceStop() throws {
        provider?.cancelTunnelWithError(nil)
    }

    func serviceReload() throws {
        // TunnelBackend today always tears down and restarts the whole
        // tunnel (VPNController.stop() then start()) rather than reloading a
        // running instance in place, so this is never called yet — present
        // only because the protocol requires it.
    }

    func getSystemProxyStatus() throws -> LibboxSystemProxyStatus {
        let status = LibboxSystemProxyStatus()
        guard let proxySettings = networkSettings?.proxySettings, proxySettings.httpServer != nil else {
            return status
        }
        status.available = true
        status.enabled = proxySettings.httpEnabled
        return status
    }

    func setSystemProxyEnabled(_ isEnabled: Bool) throws {
        guard let networkSettings, let proxySettings = networkSettings.proxySettings,
              proxySettings.httpServer != nil else {
            return
        }
        proxySettings.httpEnabled = isEnabled
        proxySettings.httpsEnabled = isEnabled
        networkSettings.proxySettings = proxySettings
        provider?.setTunnelNetworkSettings(networkSettings) { _ in }
    }

    func writeDebugMessage(_ message: String?) {
        guard let message else { return }
        os_log("%{public}@", log: log, type: .debug, message)
    }
}

/// A `LibboxNetworkInterfaceIteratorProtocol` with no elements — see
/// `getInterfaces()` above for why empty is a correct answer here.
private final class EmptyNetworkInterfaceIterator: NSObject, LibboxNetworkInterfaceIteratorProtocol {
    func hasNext() -> Bool { false }
    func next() -> LibboxNetworkInterface? { nil }
}
