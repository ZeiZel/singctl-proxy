// FlowRelay.swift — bridge a captured NEAppProxyTCPFlow to singctl's SOCKS5
// proxy. Opens a TCP connection to the SOCKS server, performs a no-auth
// SOCKS5 CONNECT to the flow's real destination, then pumps bytes in both
// directions until each side has cleanly finished (half-close), or until any
// error/timeout forces a full teardown. Wire framing lives in Socks5.swift;
// this file owns only the I/O and lifecycle.

import Foundation
import NetworkExtension
import Network
import os.log

enum FlowRelay {
    static func relayTCP(_ flow: NEAppProxyTCPFlow,
                        toSOCKS host: String, port: UInt16,
                        log: OSLog) {
        guard let endpoint = flow.remoteEndpoint as? NWHostEndpoint else {
            flow.closeReadWithError(nil); flow.closeWriteWithError(nil); return
        }

        let conn = NWConnection(host: .init(host), port: .init(rawValue: port)!, using: .tcp)
        let relay = TCPRelay(flow: flow, conn: conn, dest: endpoint, log: log)
        relay.start()
    }
}

/// Errors we surface through closeReadWithError/closeWriteWithError so the OS
/// (and its diagnostics) records *why* a flow died instead of a bare cancel.
private enum RelayError: Error, LocalizedError {
    case shortRead
    case socksMethodRejected
    case socksConnectFailed
    case hostnameTooLong
    case handshakeTimedOut

    var errorDescription: String? {
        switch self {
        case .shortRead: return "SOCKS control message was truncated"
        case .socksMethodRejected: return "SOCKS server rejected the no-auth method"
        case .socksConnectFailed: return "SOCKS CONNECT request failed"
        case .hostnameTooLong: return "destination hostname exceeds SOCKS5's 255-byte limit"
        case .handshakeTimedOut: return "SOCKS handshake did not complete in time"
        }
    }
}

/// Owns one captured flow + its upstream SOCKS connection for the flow's
/// lifetime. `relayTCP` only ever hands out a local `TCPRelay`, and every one
/// of its callbacks captures `[weak self]` — without an external strong
/// reference the relay would deallocate the instant `relayTCP` returns and
/// the flow would black-hole. `active` is that reference: every relay
/// registers itself in `start()` and unregisters in `teardown()`.
private final class TCPRelay {
    private static var active = Set<TCPRelay>()
    private static let activeLock = NSLock()

    private let flow: NEAppProxyTCPFlow
    private let conn: NWConnection
    private let dest: NWHostEndpoint
    private let log: OSLog

    // teardown/half-close bookkeeping is reachable from several async
    // callbacks (both pumps, the state handler, send/receive errors, the
    // handshake timer). Serialise on a private queue and guard with flags so
    // the flow/connection are closed exactly once.
    private let queue = DispatchQueue(label: "com.singctl.proxy.relay")
    private var closed = false
    private var outboundDone = false // flow -> upstream direction finished
    private var inboundDone = false  // upstream -> flow direction finished

    private var handshakeTimer: DispatchSourceTimer?

    init(flow: NEAppProxyTCPFlow, conn: NWConnection, dest: NWHostEndpoint, log: OSLog) {
        self.flow = flow; self.conn = conn; self.dest = dest; self.log = log
    }

    func start() {
        Self.activeLock.lock()
        Self.active.insert(self)
        Self.activeLock.unlock()

        // The handshake (greeting through CONNECT reply) must finish quickly
        // or not at all — a wedged SOCKS server would otherwise leak a flow
        // and a connection forever. Cancelled once pump() starts.
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 10)
        timer.setEventHandler { [weak self] in
            guard let self = self else { return }
            os_log("SOCKS handshake timed out", log: self.log, type: .error)
            self.teardown(error: RelayError.handshakeTimedOut)
        }
        timer.resume()
        handshakeTimer = timer

        conn.stateUpdateHandler = { [weak self] state in
            guard let self = self else { return }
            switch state {
            case .ready: self.socksHandshake()
            case .failed(let err):
                os_log("upstream failed: %{public}@", log: self.log, type: .error, "\(err)")
                self.teardown(error: err)
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

    // MARK: SOCKS5 (RFC 1928), no auth, CONNECT.

    private func socksHandshake() {
        send(Socks5.greeting) { [weak self] in self?.readMethodChoice() }
    }

    private func readMethodChoice() {
        receive(exactly: 2) { [weak self] data in
            guard let self = self else { return }
            guard Socks5.parseMethodChoice(data) else {
                self.teardown(error: RelayError.socksMethodRejected)
                return
            }
            self.sendConnectRequest()
        }
    }

    private func sendConnectRequest() {
        // remoteHostname (macOS 11+) carries the real hostname the app asked
        // to connect to, giving the SOCKS server proper socks5h remote-DNS
        // semantics; fall back to the resolved endpoint's hostname string
        // (which may already be an IP literal) when it's unavailable.
        let hostname = flow.remoteHostname ?? dest.hostname
        let portValue = UInt16(dest.port) ?? 0
        guard let req = Socks5.connectRequest(host: hostname, port: portValue) else {
            teardown(error: RelayError.hostnameTooLong)
            return
        }
        send(req) { [weak self] in self?.readConnectReply() }
    }

    private func readConnectReply() {
        // Reply header is 4 bytes; then a bound addr whose length depends on ATYP.
        receive(exactly: 4) { [weak self] head in
            guard let self = self else { return }
            switch Socks5.connectReplyExtraLength(header: head) {
            case .failed:
                self.teardown(error: RelayError.socksConnectFailed)
            case .fixed(let n):
                self.receive(exactly: n) { [weak self] _ in self?.pump() }
            case .domainLengthByte:
                self.receive(exactly: 1) { [weak self] lenByte in
                    guard let self = self, let n = lenByte.first else {
                        self?.teardown(error: RelayError.shortRead)
                        return
                    }
                    self.receive(exactly: Int(n) + 2) { [weak self] _ in self?.pump() }
                }
            }
        }
    }

    // MARK: bidirectional pump

    private func pump() {
        handshakeTimer?.cancel()
        handshakeTimer = nil
        pumpFlowToUpstream()
        pumpUpstreamToFlow()
    }

    private func pumpFlowToUpstream() {
        flow.readData { [weak self] data, error in
            guard let self = self else { return }
            if let error = error {
                os_log("flow read: %{public}@", log: self.log, type: .error, error.localizedDescription)
                self.teardown(error: error)
                return
            }
            guard let data = data, !data.isEmpty else {
                // App-side EOF: half-close upstream (send FIN) but keep the
                // reverse direction pumping until it finishes on its own.
                self.conn.send(content: nil, contentContext: .finalMessage, isComplete: true,
                                completion: .contentProcessed { [weak self] err in
                    guard let self = self else { return }
                    if let err = err { self.teardown(error: err); return }
                    self.markOutboundDone()
                })
                return
            }
            // Backpressure invariant: the next flow.readData() is issued only
            // from this write's completion, so at most one flow read's worth
            // of data is ever in flight upstream — no queue to add here.
            self.conn.send(content: data, completion: .contentProcessed { [weak self] err in
                guard let self = self else { return }
                if let err = err { self.teardown(error: err); return }
                self.pumpFlowToUpstream()
            })
        }
    }

    private func pumpUpstreamToFlow() {
        // Backpressure invariant: reads are capped at 64 KiB and the next
        // conn.receive() is issued only from flow.write()'s completion, so
        // — same as the other direction — nothing is buffered here.
        conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, error in
            guard let self = self else { return }
            if let error = error {
                os_log("upstream read: %{public}@", log: self.log, type: .error, "\(error)")
                self.teardown(error: error)
                return
            }
            if let data = data, !data.isEmpty {
                self.flow.write(data) { [weak self] werr in
                    guard let self = self else { return }
                    if let werr = werr { self.teardown(error: werr); return }
                    if isComplete {
                        self.flow.closeWriteWithError(nil)
                        self.markInboundDone()
                    } else {
                        self.pumpUpstreamToFlow()
                    }
                }
            } else if isComplete {
                self.flow.closeWriteWithError(nil)
                self.markInboundDone()
            } else {
                self.pumpUpstreamToFlow()
            }
        }
    }

    // MARK: half-close bookkeeping

    private func markOutboundDone() {
        queue.async { [weak self] in
            guard let self = self else { return }
            self.outboundDone = true
            if self.inboundDone { self.teardown() }
        }
    }

    private func markInboundDone() {
        queue.async { [weak self] in
            guard let self = self else { return }
            self.inboundDone = true
            if self.outboundDone { self.teardown() }
        }
    }

    // MARK: helpers

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
                self.teardown(error: RelayError.shortRead)
                return
            }
            then(data)
        }
    }

    /// Close the flow and upstream connection exactly once, regardless of
    /// which async callback (or how many) trips it, and drop the relay's
    /// slot in the static registry so it can finally deallocate.
    private func teardown(error: Error? = nil) {
        queue.async { [weak self] in
            guard let self = self, !self.closed else { return }
            self.closed = true
            self.handshakeTimer?.cancel()
            self.handshakeTimer = nil
            self.flow.closeReadWithError(error)
            self.flow.closeWriteWithError(error)
            self.conn.cancel()
            Self.activeLock.lock()
            Self.active.remove(self)
            Self.activeLock.unlock()
        }
    }
}

extension TCPRelay: Hashable {
    static func == (lhs: TCPRelay, rhs: TCPRelay) -> Bool { lhs === rhs }
    func hash(into hasher: inout Hasher) { hasher.combine(ObjectIdentifier(self)) }
}
