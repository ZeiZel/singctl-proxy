// FlowRelay.swift — bridge a captured NEAppProxyTCPFlow to singctl's SOCKS5
// proxy (SCAFFOLD). Opens a TCP connection to the SOCKS server, performs a
// no-auth SOCKS5 CONNECT to the flow's real destination, then pumps bytes in
// both directions until either side closes.
//
// STATUS: skeleton. The SOCKS5 handshake and pump are written out but UNTESTED.
// Harden error handling, backpressure, and teardown before shipping.

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

/// Owns one captured flow + its upstream SOCKS connection for the flow's lifetime.
private final class TCPRelay {
    private let flow: NEAppProxyTCPFlow
    private let conn: NWConnection
    private let dest: NWHostEndpoint
    private let log: OSLog

    init(flow: NEAppProxyTCPFlow, conn: NWConnection, dest: NWHostEndpoint, log: OSLog) {
        self.flow = flow; self.conn = conn; self.dest = dest; self.log = log
    }

    func start() {
        conn.stateUpdateHandler = { [weak self] state in
            guard let self = self else { return }
            switch state {
            case .ready: self.socksHandshake()
            case .failed(let err):
                os_log("upstream failed: %{public}@", log: self.log, type: .error, "\(err)")
                self.teardown()
            default: break
            }
        }
        flow.open(withLocalEndpoint: nil) { [weak self] error in
            guard let self = self else { return }
            if let error = error {
                os_log("flow.open failed: %{public}@", log: self.log, type: .error, error.localizedDescription)
                self.conn.cancel(); return
            }
            self.conn.start(queue: .global(qos: .userInitiated))
        }
    }

    // MARK: SOCKS5 (RFC 1928), no auth, CONNECT.

    private func socksHandshake() {
        // greeting: VER=5, NMETHODS=1, METHOD=0 (no auth)
        send(Data([0x05, 0x01, 0x00])) { [weak self] in self?.readMethodChoice() }
    }

    private func readMethodChoice() {
        receive(exactly: 2) { [weak self] data in
            guard let self = self, data.count == 2, data[0] == 0x05, data[1] == 0x00 else {
                self?.teardown(); return
            }
            self.sendConnectRequest()
        }
    }

    private func sendConnectRequest() {
        var req = Data([0x05, 0x01, 0x00]) // VER, CMD=CONNECT, RSV
        // Send destination as a DOMAINNAME so the SOCKS server resolves it
        // remotely (socks5h semantics) — matches singctl's proxyEnv.
        let hostname = dest.hostname
        let portValue = UInt16(dest.port) ?? 0
        req.append(0x03) // ATYP=DOMAINNAME
        let nameBytes = Array(hostname.utf8)
        req.append(UInt8(nameBytes.count))
        req.append(contentsOf: nameBytes)
        req.append(UInt8(portValue >> 8)); req.append(UInt8(portValue & 0xff))
        send(req) { [weak self] in self?.readConnectReply() }
    }

    private func readConnectReply() {
        // Reply header is 4 bytes; then a bound addr whose length depends on ATYP.
        receive(exactly: 4) { [weak self] head in
            guard let self = self, head.count == 4, head[1] == 0x00 else {
                self?.teardown(); return
            }
            let extra: Int
            switch head[3] {
            case 0x01: extra = 4 + 2           // IPv4 + port
            case 0x04: extra = 16 + 2          // IPv6 + port
            case 0x03: extra = 0               // read len byte below
            default: self.teardown(); return
            }
            if head[3] == 0x03 {
                self.receive(exactly: 1) { lenByte in
                    guard let n = lenByte.first else { self.teardown(); return }
                    self.receive(exactly: Int(n) + 2) { _ in self.pump() }
                }
            } else {
                self.receive(exactly: extra) { _ in self.pump() }
            }
        }
    }

    // MARK: bidirectional pump

    private func pump() {
        pumpFlowToUpstream()
        pumpUpstreamToFlow()
    }

    private func pumpFlowToUpstream() {
        flow.readData { [weak self] data, error in
            guard let self = self else { return }
            if let error = error { os_log("flow read: %{public}@", log: self.log, type: .error, error.localizedDescription); self.teardown(); return }
            guard let data = data, !data.isEmpty else { self.teardown(); return } // EOF
            self.conn.send(content: data, completion: .contentProcessed { err in
                if err != nil { self.teardown(); return }
                self.pumpFlowToUpstream()
            })
        }
    }

    private func pumpUpstreamToFlow() {
        conn.receive(minimumIncompleteLength: 1, maximumLength: 64 * 1024) { [weak self] data, _, isComplete, error in
            guard let self = self else { return }
            if let error = error { os_log("upstream read: %{public}@", log: self.log, type: .error, "\(error)"); self.teardown(); return }
            if let data = data, !data.isEmpty {
                self.flow.write(data) { werr in
                    if werr != nil { self.teardown(); return }
                    self.pumpUpstreamToFlow()
                }
            } else if isComplete {
                self.teardown()
            } else {
                self.pumpUpstreamToFlow()
            }
        }
    }

    // MARK: helpers

    private func send(_ data: Data, then: @escaping () -> Void) {
        conn.send(content: data, completion: .contentProcessed { [weak self] err in
            if err != nil { self?.teardown(); return }
            then()
        })
    }

    /// Receive exactly n bytes (SOCKS control messages are tiny and fixed-size).
    private func receive(exactly n: Int, then: @escaping (Data) -> Void) {
        conn.receive(minimumIncompleteLength: n, maximumLength: n) { [weak self] data, _, _, err in
            if err != nil { self?.teardown(); return }
            then(data ?? Data())
        }
    }

    private func teardown() {
        flow.closeReadWithError(nil)
        flow.closeWriteWithError(nil)
        conn.cancel()
    }
}
