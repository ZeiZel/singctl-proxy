// Socks5.swift — pure SOCKS5 (RFC 1928) wire-format helpers: framing only,
// no sockets. Foundation-only (plus libc address parsing) so this is
// unit-testable in isolation and reusable by a future UDP relay without
// pulling in Network/NetworkExtension. Every function is data-in/data-out;
// none of them touch a socket, a flow, or a queue.

import Foundation
#if canImport(Darwin)
import Darwin
#endif

enum Socks5 {
    // MARK: greeting / method selection

    /// VER=5, NMETHODS=1, METHODS=[NOAUTH]. singctl's local SOCKS server
    /// never requires auth, so this is the only greeting we ever send.
    static let greeting = Data([0x05, 0x01, 0x00])

    /// A valid method-choice reply is VER=5, METHOD=NOAUTH(0x00). Anything
    /// else — including the "no acceptable methods" 0xFF — is a hard failure
    /// the caller should treat as fatal, not retry.
    static func parseMethodChoice(_ data: Data) -> Bool {
        guard data.count == 2 else { return false }
        let i = data.startIndex
        return data[i] == 0x05 && data[data.index(after: i)] == 0x00
    }

    // MARK: CONNECT request (RFC 1928 §4)

    /// Builds a CONNECT request with ATYP=DOMAINNAME so the SOCKS server
    /// resolves `host` remotely (socks5h semantics) — this is what lets a
    /// split-DNS/VPN setup resolve names the way the upstream network
    /// expects rather than however the local Network Extension host would.
    /// Returns nil if `host` doesn't fit SOCKS5's one-byte length prefix
    /// (max 255 bytes) — that's a caller bug (a malformed endpoint), not a
    /// wire error, so we fail closed instead of silently truncating.
    static func connectRequest(host: String, port: UInt16) -> Data? {
        let nameBytes = Array(host.utf8)
        guard nameBytes.count <= 255 else { return nil }
        var req = Data([0x05, 0x01, 0x00]) // VER, CMD=CONNECT, RSV
        req.append(0x03) // ATYP=DOMAINNAME
        req.append(UInt8(nameBytes.count))
        req.append(contentsOf: nameBytes)
        req.append(UInt8(port >> 8))
        req.append(UInt8(port & 0xff))
        return req
    }

    // MARK: CONNECT reply (RFC 1928 §6)

    enum ConnectReplyNext: Equatable {
        /// REP != succeeded, or an ATYP we don't understand.
        case failed
        /// Read exactly this many more bytes (bound addr + port), then done.
        case fixed(Int)
        /// ATYP=DOMAINNAME: read one length byte `n`, then (n + 2) more
        /// bytes (name + port).
        case domainLengthByte
    }

    /// `header` is the fixed 4-byte reply prefix: VER, REP, RSV, ATYP.
    static func connectReplyExtraLength(header: Data) -> ConnectReplyNext {
        guard header.count == 4 else { return .failed }
        let i = header.startIndex
        let rep = header[header.index(i, offsetBy: 1)]
        let atyp = header[header.index(i, offsetBy: 3)]
        guard rep == 0x00 else { return .failed }
        switch atyp {
        case 0x01: return .fixed(4 + 2)   // IPv4 + port
        case 0x04: return .fixed(16 + 2)  // IPv6 + port
        case 0x03: return .domainLengthByte
        default: return .failed
        }
    }

    // MARK: UDP ASSOCIATE datagram header (RFC 1928 §7, for the future UDP relay)

    /// Builds the UDP request header — RSV(2)=0x0000, FRAG=0, then the
    /// destination address/port — that must prefix every datagram sent to
    /// the SOCKS server's UDP relay port. IP literals are encoded in their
    /// compact ATYP form (IPv4/IPv6); anything else goes out as DOMAINNAME
    /// for remote resolution. Returns nil if `host` is a domain name longer
    /// than 255 bytes. We don't support fragmentation (FRAG is always 0),
    /// matching sing-box's SOCKS UDP implementation.
    static func udpHeader(host: String, port: UInt16) -> Data? {
        var out = Data([0x00, 0x00, 0x00]) // RSV, RSV, FRAG=0
        if let bytes = ipv4Bytes(host) {
            out.append(0x01)
            out.append(contentsOf: bytes)
        } else if let bytes = ipv6Bytes(host) {
            out.append(0x04)
            out.append(contentsOf: bytes)
        } else {
            let nameBytes = Array(host.utf8)
            guard nameBytes.count <= 255 else { return nil }
            out.append(0x03)
            out.append(UInt8(nameBytes.count))
            out.append(contentsOf: nameBytes)
        }
        out.append(UInt8(port >> 8))
        out.append(UInt8(port & 0xff))
        return out
    }

    /// Parses one inbound UDP relay datagram, stripping the RFC 1928 §7
    /// header and returning the original destination + payload. Rejects
    /// fragmented datagrams (FRAG != 0) since neither sing-box's client nor
    /// this relay reassembles fragments.
    static func parseUDPDatagram(_ data: Data) -> (host: String, port: UInt16, payload: Data)? {
        guard data.count >= 4 else { return nil }
        var idx = data.startIndex
        guard data[idx] == 0x00, data[data.index(idx, offsetBy: 1)] == 0x00 else { return nil }
        let frag = data[data.index(idx, offsetBy: 2)]
        guard frag == 0x00 else { return nil }
        let atyp = data[data.index(idx, offsetBy: 3)]
        idx = data.index(idx, offsetBy: 4)

        let host: String
        switch atyp {
        case 0x01:
            guard data.distance(from: idx, to: data.endIndex) >= 4 + 2 else { return nil }
            let end = data.index(idx, offsetBy: 4)
            guard let s = ipv4String(Array(data[idx..<end])) else { return nil }
            host = s
            idx = end
        case 0x04:
            guard data.distance(from: idx, to: data.endIndex) >= 16 + 2 else { return nil }
            let end = data.index(idx, offsetBy: 16)
            guard let s = ipv6String(Array(data[idx..<end])) else { return nil }
            host = s
            idx = end
        case 0x03:
            guard idx < data.endIndex else { return nil }
            let n = Int(data[idx])
            idx = data.index(idx, offsetBy: 1)
            guard data.distance(from: idx, to: data.endIndex) >= n + 2 else { return nil }
            let end = data.index(idx, offsetBy: n)
            guard let s = String(bytes: data[idx..<end], encoding: .utf8) else { return nil }
            host = s
            idx = end
        default:
            return nil
        }

        guard data.distance(from: idx, to: data.endIndex) >= 2 else { return nil }
        let hi = UInt16(data[idx])
        let lo = UInt16(data[data.index(idx, offsetBy: 1)])
        let port = (hi << 8) | lo
        let payload = Data(data[data.index(idx, offsetBy: 2)...])
        return (host, port, payload)
    }

    // MARK: address literal <-> bytes (libc, not Network.framework)

    private static func ipv4Bytes(_ host: String) -> [UInt8]? {
        var addr = in_addr()
        guard host.withCString({ inet_pton(AF_INET, $0, &addr) }) == 1 else { return nil }
        return withUnsafeBytes(of: &addr.s_addr) { Array($0) }
    }

    private static func ipv6Bytes(_ host: String) -> [UInt8]? {
        var addr = in6_addr()
        guard host.withCString({ inet_pton(AF_INET6, $0, &addr) }) == 1 else { return nil }
        return withUnsafeBytes(of: &addr) { Array($0) }
    }

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
}
