// SocksTests.swift — XCTest coverage for Socks5.swift's pure wire-format
// helpers (RFC 1928 framing only, no sockets). This target compiles
// Socks5.swift directly (see project.yml's SocksTests target) rather than
// linking the ProxyExtension system-extension target, since Socks5.swift is
// pure Foundation/libc and doesn't need the NE entitlement or a host app.
//
// Run: xcodegen && xcodebuild test -project SingctlProxy.xcodeproj -scheme SocksTests

import XCTest
import Foundation
// No @testable import needed: Socks5.swift is compiled directly into this
// test bundle's sources (see project.yml's SocksTests target), so `Socks5`
// is already visible in this module.

final class SocksTests: XCTestCase {

    // MARK: - greeting / method choice

    func testGreetingBytes() {
        XCTAssertEqual(Socks5.greeting, Data([0x05, 0x01, 0x00]))
    }

    func testParseMethodChoiceSuccess() {
        XCTAssertTrue(Socks5.parseMethodChoice(Data([0x05, 0x00])))
    }

    func testParseMethodChoiceWrongVersion() {
        XCTAssertFalse(Socks5.parseMethodChoice(Data([0x04, 0x00])))
    }

    func testParseMethodChoiceNoAcceptableMethods() {
        // 0xFF ("no acceptable methods") must be treated as a hard failure,
        // not a retryable condition.
        XCTAssertFalse(Socks5.parseMethodChoice(Data([0x05, 0xFF])))
    }

    func testParseMethodChoiceShortData() {
        XCTAssertFalse(Socks5.parseMethodChoice(Data([0x05])))
    }

    func testParseMethodChoiceEmptyData() {
        XCTAssertFalse(Socks5.parseMethodChoice(Data()))
    }

    func testParseMethodChoiceTooLong() {
        XCTAssertFalse(Socks5.parseMethodChoice(Data([0x05, 0x00, 0x00])))
    }

    // MARK: - CONNECT request

    func testConnectRequestByteLayout() {
        guard let req = Socks5.connectRequest(host: "example.com", port: 443) else {
            return XCTFail("expected non-nil request")
        }
        let name = Array("example.com".utf8)
        var expected = Data([0x05, 0x01, 0x00]) // VER, CMD=CONNECT, RSV
        expected.append(0x03) // ATYP=DOMAINNAME
        expected.append(UInt8(name.count))
        expected.append(contentsOf: name)
        expected.append(0x01) // port hi: 443 = 0x01BB
        expected.append(0xBB) // port lo
        XCTAssertEqual(req, expected)
    }

    func testConnectRequestFieldsRoundTrip() {
        let host = "singctl.internal"
        let port: UInt16 = 8080
        guard let req = Socks5.connectRequest(host: host, port: port) else {
            return XCTFail("expected non-nil request")
        }
        let bytes = Array(req)
        XCTAssertEqual(bytes[0], 0x05) // VER
        XCTAssertEqual(bytes[1], 0x01) // CMD=CONNECT
        XCTAssertEqual(bytes[2], 0x00) // RSV
        XCTAssertEqual(bytes[3], 0x03) // ATYP=DOMAINNAME
        let len = Int(bytes[4])
        XCTAssertEqual(len, host.utf8.count)
        let nameBytes = bytes[5..<(5 + len)]
        XCTAssertEqual(String(decoding: nameBytes, as: UTF8.self), host)
        let portHi = UInt16(bytes[5 + len])
        let portLo = UInt16(bytes[5 + len + 1])
        XCTAssertEqual((portHi << 8) | portLo, port)
        XCTAssertEqual(bytes.count, 5 + len + 2)
    }

    func testConnectRequestHost255BytesOK() {
        let host = String(repeating: "a", count: 255)
        XCTAssertNotNil(Socks5.connectRequest(host: host, port: 80))
    }

    func testConnectRequestHost256BytesNil() {
        let host = String(repeating: "a", count: 256)
        XCTAssertNil(Socks5.connectRequest(host: host, port: 80))
    }

    // MARK: - CONNECT reply

    func testConnectReplyExtraLengthIPv4() {
        let header = Data([0x05, 0x00, 0x00, 0x01])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .fixed(6))
    }

    func testConnectReplyExtraLengthIPv6() {
        let header = Data([0x05, 0x00, 0x00, 0x04])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .fixed(18))
    }

    func testConnectReplyExtraLengthDomain() {
        let header = Data([0x05, 0x00, 0x00, 0x03])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .domainLengthByte)
    }

    func testConnectReplyExtraLengthRepFailure() {
        // REP != 0x00 (e.g. 0x01 = general SOCKS server failure) is a hard
        // failure regardless of ATYP.
        let header = Data([0x05, 0x01, 0x00, 0x01])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .failed)
    }

    func testConnectReplyExtraLengthBadATYP() {
        let header = Data([0x05, 0x00, 0x00, 0x02])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .failed)
    }

    func testConnectReplyExtraLengthShortHeader() {
        let header = Data([0x05, 0x00, 0x00])
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: header), .failed)
    }

    func testConnectReplyExtraLengthEmptyHeader() {
        XCTAssertEqual(Socks5.connectReplyExtraLength(header: Data()), .failed)
    }

    // MARK: - UDP header

    func testUDPHeaderIPv4Literal() {
        guard let header = Socks5.udpHeader(host: "192.168.1.1", port: 53) else {
            return XCTFail("expected non-nil header")
        }
        var expected = Data([0x00, 0x00, 0x00, 0x01]) // RSV, RSV, FRAG, ATYP=IPv4
        expected.append(contentsOf: [192, 168, 1, 1])
        expected.append(0x00) // port hi: 53 = 0x0035
        expected.append(0x35) // port lo
        XCTAssertEqual(header, expected)
    }

    func testUDPHeaderIPv6Literal() {
        guard let header = Socks5.udpHeader(host: "::1", port: 53) else {
            return XCTFail("expected non-nil header")
        }
        let bytes = Array(header)
        XCTAssertEqual(Array(bytes[0...2]), [0x00, 0x00, 0x00])
        XCTAssertEqual(bytes[3], 0x04) // ATYP=IPv6
        XCTAssertEqual(bytes.count, 4 + 16 + 2)
        // ::1 -> 15 zero bytes followed by 0x01
        let addr = bytes[4..<20]
        XCTAssertEqual(Array(addr), [UInt8](repeating: 0, count: 15) + [0x01])
        XCTAssertEqual(bytes[20], 0x00)
        XCTAssertEqual(bytes[21], 0x35)
    }

    func testUDPHeaderHostname() {
        guard let header = Socks5.udpHeader(host: "example.com", port: 443) else {
            return XCTFail("expected non-nil header")
        }
        let bytes = Array(header)
        XCTAssertEqual(bytes[3], 0x03) // ATYP=DOMAINNAME
        let name = Array("example.com".utf8)
        XCTAssertEqual(Int(bytes[4]), name.count)
        XCTAssertEqual(Array(bytes[5..<(5 + name.count)]), name)
        XCTAssertEqual(bytes[5 + name.count], 0x01) // port hi: 443 = 0x01BB
        XCTAssertEqual(bytes[5 + name.count + 1], 0xBB)
    }

    func testUDPHeaderOverlongHostnameNil() {
        let host = String(repeating: "b", count: 256)
        XCTAssertNil(Socks5.udpHeader(host: host, port: 80))
    }

    func testUDPHeaderHostname255BytesOK() {
        let host = String(repeating: "b", count: 255)
        XCTAssertNotNil(Socks5.udpHeader(host: host, port: 80))
    }

    // MARK: - parseUDPDatagram round-trips with udpHeader

    func testParseUDPDatagramRoundTripIPv4() {
        guard let header = Socks5.udpHeader(host: "10.0.0.5", port: 5353) else {
            return XCTFail("expected non-nil header")
        }
        let payload = Data([0xDE, 0xAD, 0xBE, 0xEF])
        guard let parsed = Socks5.parseUDPDatagram(header + payload) else {
            return XCTFail("expected successful parse")
        }
        XCTAssertEqual(parsed.host, "10.0.0.5")
        XCTAssertEqual(parsed.port, 5353)
        XCTAssertEqual(parsed.payload, payload)
    }

    func testParseUDPDatagramRoundTripIPv6() {
        guard let header = Socks5.udpHeader(host: "2001:db8::1", port: 853) else {
            return XCTFail("expected non-nil header")
        }
        let payload = Data([0x01, 0x02, 0x03])
        guard let parsed = Socks5.parseUDPDatagram(header + payload) else {
            return XCTFail("expected successful parse")
        }
        XCTAssertEqual(parsed.host, "2001:db8::1")
        XCTAssertEqual(parsed.port, 853)
        XCTAssertEqual(parsed.payload, payload)
    }

    func testParseUDPDatagramRoundTripHostname() {
        guard let header = Socks5.udpHeader(host: "dns.example.org", port: 53) else {
            return XCTFail("expected non-nil header")
        }
        let payload = Data("query".utf8)
        guard let parsed = Socks5.parseUDPDatagram(header + payload) else {
            return XCTFail("expected successful parse")
        }
        XCTAssertEqual(parsed.host, "dns.example.org")
        XCTAssertEqual(parsed.port, 53)
        XCTAssertEqual(parsed.payload, payload)
    }

    func testParseUDPDatagramRoundTripEmptyPayload() {
        guard let header = Socks5.udpHeader(host: "127.0.0.1", port: 1) else {
            return XCTFail("expected non-nil header")
        }
        guard let parsed = Socks5.parseUDPDatagram(header) else {
            return XCTFail("expected successful parse with empty payload")
        }
        XCTAssertEqual(parsed.host, "127.0.0.1")
        XCTAssertEqual(parsed.port, 1)
        XCTAssertEqual(parsed.payload, Data())
    }

    func testParseUDPDatagramRejectsFragmented() {
        var bytes = Array(Socks5.udpHeader(host: "127.0.0.1", port: 80)!)
        bytes[2] = 0x01 // FRAG != 0
        XCTAssertNil(Socks5.parseUDPDatagram(Data(bytes)))
    }

    func testParseUDPDatagramRejectsShortHeader() {
        // Fewer than 4 bytes total.
        XCTAssertNil(Socks5.parseUDPDatagram(Data([0x00, 0x00, 0x00])))
    }

    func testParseUDPDatagramRejectsTruncatedIPv4() {
        // ATYP says IPv4 (4 + 2 bytes needed) but only 2 are present.
        let truncated = Data([0x00, 0x00, 0x00, 0x01, 0x7F, 0x00])
        XCTAssertNil(Socks5.parseUDPDatagram(truncated))
    }

    func testParseUDPDatagramRejectsTruncatedDomain() {
        // ATYP=DOMAINNAME claims a 10-byte name but only 2 bytes follow.
        var bytes = Data([0x00, 0x00, 0x00, 0x03, 0x0A])
        bytes.append(contentsOf: [0x61, 0x62])
        XCTAssertNil(Socks5.parseUDPDatagram(bytes))
    }

    func testParseUDPDatagramRejectsBadReserved() {
        // RSV bytes must be 0x0000.
        let bytes = Data([0x01, 0x00, 0x00, 0x01, 0x7F, 0x00, 0x00, 0x01, 0x00, 0x50])
        XCTAssertNil(Socks5.parseUDPDatagram(bytes))
    }

    func testParseUDPDatagramRejectsUnknownATYP() {
        let bytes = Data([0x00, 0x00, 0x00, 0x02, 0x00, 0x50])
        XCTAssertNil(Socks5.parseUDPDatagram(bytes))
    }
}
