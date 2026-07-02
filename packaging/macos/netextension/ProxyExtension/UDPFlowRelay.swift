// UDPFlowRelay.swift — bridge a captured NEAppProxyUDPFlow to singctl's SOCKS5
// proxy via UDP ASSOCIATE (RFC 1928 §7). A TCP control connection performs
// the no-auth handshake and the ASSOCIATE request; the server's BND.ADDR/
// BND.PORT reply names a UDP relay endpoint that we open a second
// NWConnection to and pump datagrams through, framed per §7, for as long as
// the control connection stays up (the association is only valid while it
// lives — RFC 1928 §7). Wire framing lives in Socks5.swift; this file owns
// only the I/O and lifecycle.
//
// Per-app DNS resolved through mDNSResponder is NOT capturable here: those
// UDP flows belong to mDNSResponder's process, not the target app, so they
// never match shouldCapture and never reach this relay. This relay only
// covers UDP flows the target app itself originates directly (QUIC/HTTP-3,
// app-level DNS sockets that bypass mDNSResponder, etc.).

import Foundation
import NetworkExtension
import Network
import os.log

enum UDPFlowRelay {
    static func relay(_ flow: NEAppProxyUDPFlow,
                     toSOCKS host: String, port: UInt16,
                     log: OSLog) {
        let conn = NWConnection(host: .init(host), port: .init(rawValue: port)!, using: .tcp)
        let relay = UDPRelay(flow: flow, conn: conn, socksHost: host, socksPort: port, log: log)
        relay.start()
    }
}

/// Errors we surface through closeReadWithError/closeWriteWithError so the OS
/// (and its diagnostics) records *why* a flow died instead of a bare cancel.
private enum UDPRelayError: Error, LocalizedError {
    case shortRead
    case socksMethodRejected
    case socksAssociateFailed
    case malformedBoundAddress
    case handshakeTimedOut
    case idleTimedOut

    var errorDescription: String? {
        switch self {
        case .shortRead: return "SOCKS control message was truncated"
        case .socksMethodRejected: return "SOCKS server rejected the no-auth method"
        case .socksAssociateFailed: return "SOCKS UDP ASSOCIATE request failed"
        case .malformedBoundAddress: return "SOCKS server returned an unparsable bound address"
        case .handshakeTimedOut: return "SOCKS handshake did not complete in time"
        case .idleTimedOut: return "UDP association was idle too long"
        }
    }
}

/// Owns one captured UDP flow + its SOCKS control connection + its SOCKS UDP
/// data connection for the flow's lifetime. `relay` only ever hands out a
/// local `UDPRelay`, and every one of its callbacks captures `[weak self]` —
/// without an external strong reference the relay would deallocate the
/// instant `relay` returns and the flow would black-hole. `active` is that
/// reference: every relay registers itself in `start()` and unregisters in
/// `teardown()`.
private final class UDPRelay {
    private static var active = Set<UDPRelay>()
    private static let activeLock = NSLock()

    private let flow: NEAppProxyUDPFlow
    private let conn: NWConnection // SOCKS control connection (TCP)
    private let socksHost: String
    private let socksPort: UInt16
    private let log: OSLog

    private var udpConn: NWConnection? // SOCKS UDP relay data connection

    // teardown bookkeeping is reachable from several async callbacks (both
    // pumps, both connections' state handlers, the handshake timer, the idle
    // timer). Serialise on a private queue and guard with a flag so the flow
    // and both connections are closed exactly once.
    private let queue = DispatchQueue(label: "com.singctl.proxy.udprelay")
    private var closed = false

    private var handshakeTimer: DispatchSourceTimer?
    private var idleTimer: DispatchSourceTimer?

    init(flow: NEAppProxyUDPFlow, conn: NWConnection, socksHost: String, socksPort: UInt16, log: OSLog) {
        self.flow = flow; self.conn = conn; self.socksHost = socksHost; self.socksPort = socksPort; self.log = log
    }

    func start() {
        Self.activeLock.lock()
        Self.active.insert(self)
        Self.activeLock.unlock()

        // The handshake (greeting through ASSOCIATE reply) must finish
        // quickly or not at all — a wedged SOCKS server would otherwise leak
        // a flow and two connections forever. Cancelled once the UDP data
        // path opens.
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 10)
        timer.setEventHandler { [weak self] in
            guard let self = self else { return }
            os_log("SOCKS UDP ASSOCIATE handshake timed out", log: self.log, type: .error)
            self.teardown(error: UDPRelayError.handshakeTimedOut)
        }
        timer.resume()
        handshakeTimer = timer

        conn.stateUpdateHandler = { [weak self] state in
            guard let self = self else { return }
            switch state {
            case .ready: self.socksHandshake()
            case .failed(let err):
                os_log("SOCKS control connection failed: %{public}@", log: self.log, type: .error, "\(err)")
                self.teardown(error: err)
            case .cancelled:
                // RFC 1928 §7: the association is valid only while this
                // control connection lives. If it goes away for any reason
                // (including a clean cancel we didn't initiate) the relay is
                // done.
                self.teardown()
            default: break
            }
        }
        flow.open(withLocalEndpoint: nil) { [weak self] error in
            guard let self = self else { return }
            if let error = error {
                os_log("flow.open failed: %{public}@", log: self.log, type: .error, error.localizedDescription)
                self.teardown(error: error)
                return
            }
            self.conn.start(queue: .global(qos: .userInitiated))
        }
    }

    // MARK: SOCKS5 handshake + UDP ASSOCIATE (RFC 1928 §7)

    private func socksHandshake() {
        send(Socks5.greeting) { [weak self] in self?.readMethodChoice() }
    }

    private func readMethodChoice() {
        receive(exactly: 2) { [weak self] data in
            guard let self = self else { return }
            guard Socks5.parseMethodChoice(data) else {
                self.teardown(error: UDPRelayError.socksMethodRejected)
                return
            }
            self.sendAssociateRequest()
        }
    }

    /// UDP ASSOCIATE request (RFC 1928 §7): VER=5, CMD=3, RSV=0, then
    /// DST.ADDR/DST.PORT. We don't know (or need) the client's send-from
    /// endpoint in advance, so we ask for 0.0.0.0:0 — standard client
    /// behavior sing-box's SOCKS UDP implementation accepts. Kept local
    /// rather than added to Socks5.swift since it's the only CMD value this
    /// relay ever sends.
    private func associateRequest() -> Data {
        Data([0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0])
    }

    private func sendAssociateRequest() {
        send(associateRequest()) { [weak self] in self?.readAssociateReply() }
    }

    private func readAssociateReply() {
        // Reply header is 4 bytes (VER, REP, RSV, ATYP); same shape as the
        // CONNECT reply, so Socks5.connectReplyExtraLength applies as-is.
        receive(exactly: 4) { [weak self] head in
            guard let self = self else { return }
            switch Socks5.connectReplyExtraLength(header: head) {
            case .failed:
                self.teardown(error: UDPRelayError.socksAssociateFailed)
            case .fixed(let n):
                let atyp = head[head.index(head.startIndex, offsetBy: 3)]
                self.receive(exactly: n) { [weak self] rest in
                    self?.finishAssociate(atyp: atyp, rest: rest, domainLength: nil)
                }
            case .domainLengthByte:
                self.receive(exactly: 1) { [weak self] lenByte in
                    guard let self = self, let n = lenByte.first else {
                        self?.teardown(error: UDPRelayError.shortRead)
                        return
                    }
                    self.receive(exactly: Int(n) + 2) { [weak self] rest in
                        self?.finishAssociate(atyp: 0x03, rest: rest, domainLength: Int(n))
                    }
                }
            }
        }
    }

    /// `rest` is BND.ADDR + BND.PORT (length depends on ATYP; for
    /// DOMAINNAME, `domainLength` gives the name's byte count).
    private func finishAssociate(atyp: UInt8, rest: Data, domainLength: Int?) {
        guard let (host, port) = Self.parseBoundAddress(atyp: atyp, rest: rest, domainLength: domainLength) else {
            teardown(error: UDPRelayError.malformedBoundAddress)
            return
        }
        // A bound address of 0.0.0.0/:: means "same host you connected to" —
        // standard SOCKS5 UDP client behavior.
        let relayHost = (host == "0.0.0.0" || host == "::") ? socksHost : host
        openUDP(host: relayHost, port: port)
    }

    private static func parseBoundAddress(atyp: UInt8, rest: Data, domainLength: Int?) -> (host: String, port: UInt16)? {
        switch atyp {
        case 0x01:
            guard rest.count == 4 + 2 else { return nil }
            guard let host = ipv4String(Array(rest.prefix(4))) else { return nil }
            return (host, portValue(rest, offset: 4))
        case 0x04:
            guard rest.count == 16 + 2 else { return nil }
            guard let host = ipv6String(Array(rest.prefix(16))) else { return nil }
            return (host, portValue(rest, offset: 16))
        case 0x03:
            guard let n = domainLength, rest.count == n + 2 else { return nil }
            guard let host = String(bytes: rest.prefix(n), encoding: .utf8) else { return nil }
            return (host, portValue(rest, offset: n))
        default:
            return nil
        }
    }

    private static func portValue(_ data: Data, offset: Int) -> UInt16 {
        let i = data.index(data.startIndex, offsetBy: offset)
        let hi = UInt16(data[i])
        let lo = UInt16(data[data.index(i, offsetBy: 1)])
        return (hi << 8) | lo
    }

    // Socks5.swift's IP<->bytes helpers are private, so the tiny bit of
    // libc address parsing needed for BND.ADDR is duplicated here.
    private static func ipv4String(_ bytes: [UInt8]) -> String? {
        guard bytes.count == 4 else { return nil }
        var addr = in_addr()
        withUnsafeMutableBytes(of: &addr.s_addr) { $0.copyBytes(from: bytes) }
        var buf = [Int8](repeating: 0, count: Int(INET_ADDRSTRLEN))
        guard inet_ntop(AF_INET, &addr, &buf, socklen_t(INET_ADDRSTRLEN)) != nil else { return nil }
        return String(cString: buf)
    }

    private static func ipv6String(_ bytes: [UInt8]) -> String? {
        guard bytes.count == 16 else { return nil }
        var addr = in6_addr()
        withUnsafeMutableBytes(of: &addr) { $0.copyBytes(from: bytes) }
        var buf = [Int8](repeating: 0, count: Int(INET6_ADDRSTRLEN))
        guard inet_ntop(AF_INET6, &addr, &buf, socklen_t(INET6_ADDRSTRLEN)) != nil else { return nil }
        return String(cString: buf)
    }

    // MARK: UDP data path

    private func openUDP(host: String, port: UInt16) {
        handshakeTimer?.cancel()
        handshakeTimer = nil

        let udp = NWConnection(host: .init(host), port: .init(rawValue: port)!, using: .udp)
        udpConn = udp
        udp.stateUpdateHandler = { [weak self] state in
            guard let self = self else { return }
            if case .failed(let err) = state {
                os_log("SOCKS UDP data connection failed: %{public}@", log: self.log, type: .error, "\(err)")
                self.teardown(error: err)
            }
        }
        udp.start(queue: .global(qos: .userInitiated))

        armIdleTimer()
        pumpFlowToUpstream()
        pumpUpstreamToFlow()
    }

    /// Re-armed on every datagram in either direction; fires only once the
    /// association has been silent for 120s, at which point we tear the
    /// whole relay down (matches the TCP relay's fail-closed philosophy —
    /// there is no direct fallback).
    private func armIdleTimer() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 120)
        timer.setEventHandler { [weak self] in
            guard let self = self else { return }
            os_log("UDP association idle timeout", log: self.log, type: .info)
            self.teardown(error: UDPRelayError.idleTimedOut)
        }
        timer.resume()
        idleTimer = timer
    }

    private func resetIdleTimer() {
        queue.async { [weak self] in
            guard let self = self, !self.closed else { return }
            self.idleTimer?.cancel()
            self.armIdleTimer()
        }
    }

    private func pumpFlowToUpstream() {
        // NEAppProxyUDPFlow's modern readDatagrams (macOS 15+; the pre-15
        // three-array-parameter form is deprecated) hands back datagram/
        // destination pairs using Network.framework's NWEndpoint (an enum),
        // not NetworkExtension's older NWHostEndpoint class that
        // remoteEndpoint/TCPRelay deal in.
        flow.readDatagrams { [weak self] (pairs: [(Data, Network.NWEndpoint)]?, error: Error?) in
            guard let self = self else { return }
            if let error = error {
                os_log("flow readDatagrams: %{public}@", log: self.log, type: .error, error.localizedDescription)
                self.teardown(error: error)
                return
            }
            guard let pairs = pairs, !pairs.isEmpty else {
                // Empty batch: app side is done with this flow.
                self.teardown()
                return
            }
            // Backpressure invariant: the next flow.readDatagrams() is
            // issued only once every datagram in this batch has been sent
            // upstream, mirroring TCPRelay's single-read-in-flight rule.
            self.sendDatagrams(pairs, index: 0)
        }
    }

    private func sendDatagrams(_ pairs: [(Data, Network.NWEndpoint)], index: Int) {
        guard index < pairs.count else {
            self.pumpFlowToUpstream()
            return
        }
        let (payload, endpoint) = pairs[index]
        guard let (host, port) = Self.hostPort(from: endpoint),
              let header = Socks5.udpHeader(host: host, port: port) else {
            // Drop a datagram whose destination we can't frame (a Bonjour
            // endpoint, or a hostname over SOCKS5's 255-byte limit) and keep
            // the association alive for the rest of the batch.
            os_log("dropping outbound UDP datagram: unframeable destination", log: self.log, type: .debug)
            sendDatagrams(pairs, index: index + 1)
            return
        }
        var packet = header
        packet.append(payload)
        guard let udpConn = udpConn else { teardown(); return }
        udpConn.send(content: packet, completion: .contentProcessed { [weak self] err in
            guard let self = self else { return }
            if let err = err { self.teardown(error: err); return }
            self.resetIdleTimer()
            self.sendDatagrams(pairs, index: index + 1)
        })
    }

    /// Network.NWEndpoint -> (hostString, port) for the .hostPort case (the
    /// only case a UDP flow's destination endpoint is ever reported as).
    /// `Socks5.udpHeader` itself sorts out whether the resulting string is an
    /// IPv4/IPv6 literal or a domain name.
    private static func hostPort(from endpoint: Network.NWEndpoint) -> (host: String, port: UInt16)? {
        guard case let .hostPort(host, port) = endpoint else { return nil }
        switch host {
        case .ipv4(let addr): return ("\(addr)", port.rawValue)
        case .ipv6(let addr): return ("\(addr)", port.rawValue)
        case .name(let name, _): return (name, port.rawValue)
        @unknown default: return nil
        }
    }

    private func pumpUpstreamToFlow() {
        guard let udpConn = udpConn else { return }
        // Backpressure invariant: the next receiveMessage() is issued only
        // from flow.writeDatagrams()'s completion (or immediately, for a
        // dropped/unparseable datagram), so at most one relay datagram is
        // ever in flight toward the flow.
        udpConn.receiveMessage { [weak self] data, _, _, error in
            guard let self = self else { return }
            if let error = error {
                os_log("SOCKS UDP data read: %{public}@", log: self.log, type: .error, "\(error)")
                self.teardown(error: error)
                return
            }
            guard let data = data, let parsed = Socks5.parseUDPDatagram(data) else {
                // Malformed/unframeable relay datagram: drop it and keep
                // the association alive rather than tearing the flow down.
                os_log("dropping unparseable UDP relay datagram", log: self.log, type: .debug)
                self.pumpUpstreamToFlow()
                return
            }
            self.resetIdleTimer()
            let sender = NWHostEndpoint(hostname: parsed.host, port: String(parsed.port))
            self.flow.writeDatagrams([parsed.payload], sentBy: [sender]) { [weak self] err in
                guard let self = self else { return }
                if let err = err { self.teardown(error: err); return }
                self.pumpUpstreamToFlow()
            }
        }
    }

    // MARK: control-connection helpers (same pattern as TCPRelay)

    private func send(_ data: Data, then: @escaping () -> Void) {
        conn.send(content: data, completion: .contentProcessed { [weak self] err in
            if let err = err { self?.teardown(error: err); return }
            then()
        })
    }

    /// Receive exactly n bytes (SOCKS control messages are tiny and
    /// fixed-size). A short read — fewer bytes than requested, whether from
    /// an error, an early isComplete, or a nil payload — is treated the same
    /// as any other framing failure: tear the whole relay down.
    private func receive(exactly n: Int, then: @escaping (Data) -> Void) {
        conn.receive(minimumIncompleteLength: n, maximumLength: n) { [weak self] data, _, _, err in
            guard let self = self else { return }
            if let err = err { self.teardown(error: err); return }
            guard let data = data, data.count == n else {
                self.teardown(error: UDPRelayError.shortRead)
                return
            }
            then(data)
        }
    }

    /// Close the flow and both connections exactly once, regardless of which
    /// async callback (or how many) trips it, and drop the relay's slot in
    /// the static registry so it can finally deallocate. Every failure path
    /// — handshake, ASSOCIATE, idle timeout, either connection dying —
    /// routes here with an error, so the flow always ends in a
    /// closeReadWithError/closeWriteWithError and never silently falls back
    /// to direct traffic.
    private func teardown(error: Error? = nil) {
        queue.async { [weak self] in
            guard let self = self, !self.closed else { return }
            self.closed = true
            self.handshakeTimer?.cancel()
            self.handshakeTimer = nil
            self.idleTimer?.cancel()
            self.idleTimer = nil
            self.flow.closeReadWithError(error)
            self.flow.closeWriteWithError(error)
            self.udpConn?.cancel()
            self.conn.cancel()
            Self.activeLock.lock()
            Self.active.remove(self)
            Self.activeLock.unlock()
        }
    }
}

extension UDPRelay: Hashable {
    static func == (lhs: UDPRelay, rhs: UDPRelay) -> Bool { lhs === rhs }
    func hash(into hasher: inout Hasher) { hasher.combine(ObjectIdentifier(self)) }
}
