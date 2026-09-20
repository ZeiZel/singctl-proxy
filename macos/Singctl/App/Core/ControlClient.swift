// ControlClient.swift
//
// A typed async client for the singctl daemon's Unix control socket, mirroring
// internal/control/client.go's Request() protocol exactly:
//   - one AF_UNIX SOCK_STREAM connection per command (the server closes after
//     replying to a single line — see internal/control/server.go's serve()),
//   - request line is "VERB[ ARG]\n" (ARG may itself be JSON),
//   - reply is read until EOF and trimmed of trailing "\r\n",
//   - a reply starting with "ERR " is an error (message after the prefix).
//
// Every public method re-discovers the daemon (via InstanceDiscovery) on each
// call, so a daemon restart (new socket path) is transparently picked up.

import Foundation
import Darwin

/// Errors surfaced by ControlClient. `.noDaemon` covers both "never started"
/// and "was running but the PID/socket is gone" — mirrors bridge's errNoDaemon.
enum ControlClientError: Error, LocalizedError, Equatable {
    case noDaemon
    case connectFailed(String)
    case timeout
    case serverError(String)
    case badReply(String)

    var errorDescription: String? {
        switch self {
        case .noDaemon: return "singctl daemon is not running"
        case .connectFailed(let msg): return "control socket connect failed: \(msg)"
        case .timeout: return "control socket connect timed out"
        case .serverError(let msg): return msg
        case .badReply(let msg): return "unexpected control reply: \(msg)"
        }
    }
}

/// Talks to one running singctl daemon over its Unix control socket. Actor-
/// isolated because the underlying discovery cache in InstanceDiscovery reads
/// from disk on every call and each request opens/closes its own socket file
/// descriptor — serializing callers avoids redundant concurrent instance.json
/// re-reads racing each other for no benefit.
actor ControlClient {

    /// Per-connection timeouts. connectTimeout mirrors control.Request's
    /// net.DialTimeout(3s); ioTimeout mirrors the server's generous
    /// connDeadline (15s — MODE/SETTINGS-SET can trigger a live core reload).
    private let connectTimeout: Int32 = 3
    private let ioTimeout: Int32 = 15

    // MARK: - Status / lifecycle

    func status() async throws -> DaemonStatus {
        try decode(await roundTrip("STATUS"))
    }

    func stop() async throws {
        _ = try await roundTrip("STOP")
    }

    func setMode(_ mode: String) async throws {
        _ = try await roundTrip("MODE", mode)
    }

    // MARK: - Settings

    func settingsGet() async throws -> Settings {
        try decode(await roundTrip("SETTINGS-GET"))
    }

    func settingsSet(_ settings: Settings) async throws {
        let data = try JSONEncoder().encode(settings)
        _ = try await roundTrip("SETTINGS-SET", String(data: data, encoding: .utf8) ?? "")
    }

    // MARK: - Keys (VLESS links)

    /// KEYS-GET's reply is newline-joined raw links, NOT JSON (masking for
    /// display is a UI concern — see maskKey in gui/bridge/types.go).
    func keysGet() async throws -> [String] {
        let reply = try await roundTrip("KEYS-GET")
        return reply
            .split(separator: "\n", omittingEmptySubsequences: true)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
    }

    func keysAdd(_ link: String) async throws {
        _ = try await roundTrip("KEYS-ADD", link.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    /// Adds a WireGuard key from INI config text (`[Interface]`/`[Peer]`
    /// sections) — the one key kind with no `scheme://` to dispatch on, so
    /// it can't go through `keysAdd`'s KEYS-ADD verb. The wire protocol is
    /// strictly line-delimited (this file's header; the server reads a
    /// single line with `bufio.Reader.ReadString('\n')` and cuts the verb
    /// from the argument on the first space — internal/control/server.go's
    /// serve()), so a raw multi-line config would be silently truncated at
    /// its first embedded newline if sent as a normal argument. KEYS-ADD-CONFIG
    /// instead takes the config as standard base64 (with padding) of its raw
    /// UTF-8 bytes, which is always a single line regardless of content;
    /// the daemon decodes it back before parsing the INI. Replies "OK",
    /// same as KEYS-ADD.
    func keysAddConfig(_ config: String) async throws {
        let trimmed = config.trimmingCharacters(in: .whitespacesAndNewlines)
        let encoded = Data(trimmed.utf8).base64EncodedString()
        _ = try await roundTrip("KEYS-ADD-CONFIG", encoded)
    }

    func keysRemove(_ index: Int) async throws {
        _ = try await roundTrip("KEYS-REMOVE", String(index))
    }

    func keysRename(_ index: Int, _ name: String) async throws {
        _ = try await roundTrip("KEYS-RENAME", "\(index) \(name.trimmingCharacters(in: .whitespacesAndNewlines))")
    }

    // MARK: - Subscriptions

    /// SUB-LIST's reply is a JSON array of `Subscription` records (see
    /// internal/sub/record.go).
    func subList() async throws -> [Subscription] {
        try decode(await roundTrip("SUB-LIST"))
    }

    /// SUB-ADD fetches the subscription immediately; the daemon errors (an
    /// "ERR " reply, surfaced as .serverError) if the URL is unusable.
    func subAdd(_ url: String) async throws {
        _ = try await roundTrip("SUB-ADD", url.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    func subRemove(_ url: String) async throws {
        _ = try await roundTrip("SUB-REMOVE", url.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    /// SUB-UPDATE's reply is a bare decimal count of subscriptions whose
    /// server list changed, not JSON.
    func subUpdate() async throws -> Int {
        try decodeBarePID(await roundTrip("SUB-UPDATE"))
    }

    // MARK: - System proxy (macOS PAC + `networksetup`, applied by the daemon)

    func sysProxyStatus() async throws -> SysProxyStatus {
        try decode(await roundTrip("SYSPROXY-STATUS"))
    }

    /// SYSPROXY-CONFIG's reply is raw INI text, NOT JSON — same idea as
    /// KEYS-GET's raw-links reply (see that method's doc comment).
    func sysProxyConfig() async throws -> String {
        try await roundTrip("SYSPROXY-CONFIG")
    }

    func sysProxySet(mode: String) async throws {
        let data = try JSONEncoder().encode(SysProxySetRequest(mode: mode))
        _ = try await roundTrip("SYSPROXY-SET", String(data: data, encoding: .utf8) ?? "")
    }

    /// SYSPROXY-IMPORT takes an INI rules file, a YAML config or a plain
    /// domain list as standard base64 (with padding) of its raw UTF-8 bytes —
    /// the same trick `keysAddConfig` uses, and for the same reason: the
    /// control protocol is one line per request (internal/control/server.go's
    /// serve() cuts the verb from the argument at the first space and reads
    /// one line with `bufio.Reader.ReadString('\n')`), so raw multi-line text
    /// would be silently truncated at its first embedded newline if sent as a
    /// normal argument. Replies "OK".
    func sysProxyImport(_ text: String) async throws {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let encoded = Data(trimmed.utf8).base64EncodedString()
        _ = try await roundTrip("SYSPROXY-IMPORT", encoded)
    }

    // MARK: - Console

    func consolePoll(since: Int) async throws -> [ConsoleLine] {
        try decode(await roundTrip("CONSOLE-POLL", String(since)))
    }

    // MARK: - Traffic

    func traffic() async throws -> Traffic {
        try decode(await roundTrip("TRAFFIC"))
    }

    // MARK: - Connections (F6 in docs/v2-spec.md)

    /// CONNECTIONS' reply is a JSON object — the live connection table, its
    /// per-app/per-destination aggregates, and an explicit `state` diagnosis
    /// (see `ConnectionsPayload`'s doc comment). Distinct from `connections()`
    /// above, which mirrors the Clash API's own GET /connections envelope.
    func connectionsDetail() async throws -> ConnectionsPayload {
        try decode(await roundTrip("CONNECTIONS"))
    }

    /// Closes one live connection by id (the Clash API's DELETE
    /// /connections/{id}). Replies "OK".
    func closeConnection(_ id: String) async throws {
        _ = try await roundTrip("CONNECTION-CLOSE", id)
    }

    // MARK: - Firewall (F6 item 5)

    /// FIREWALL-LIST's reply is a JSON array of `FirewallRule`, in the order
    /// rules were added.
    func firewallList() async throws -> [FirewallRule] {
        try decode(await roundTrip("FIREWALL-LIST"))
    }

    /// FIREWALL-ADD's argument is the JSON-encoded rule (an empty `id` is
    /// assigned one server-side); its reply is the JSON-encoded stored rule,
    /// NOT "OK" — the caller needs the assigned id back. The daemon errors
    /// (an "ERR " reply, surfaced as .serverError) when the rule doesn't
    /// validate (exactly one of domain/cidr/process, a well-formed CIDR).
    func firewallAdd(_ rule: FirewallRule) async throws -> FirewallRule {
        let data = try JSONEncoder().encode(rule)
        return try decode(await roundTrip("FIREWALL-ADD", String(data: data, encoding: .utf8) ?? ""))
    }

    /// Removes the rule with the given id. Replies "OK"; the daemon errors
    /// for an unknown id.
    func firewallRemove(_ id: String) async throws {
        _ = try await roundTrip("FIREWALL-REMOVE", id)
    }

    // MARK: - Proxy failover group

    /// PROXY-GROUP's reply is a JSON object — see `ProxyGroup`'s doc comment
    /// for the shape and what `available`/`auto`/`selected` mean.
    func proxyGroup() async throws -> ProxyGroup {
        try decode(await roundTrip("PROXY-GROUP"))
    }

    /// Pins the failover group to `tag`, or restores automatic urltest
    /// selection when `tag == "auto"`. Replies "OK"; the daemon errors (an
    /// "ERR " reply, surfaced as .serverError) for an unknown tag.
    func proxySelect(_ tag: String) async throws {
        _ = try await roundTrip("PROXY-SELECT", tag)
    }

    // MARK: - Per-process routing

    func procList() async throws -> [ProcInfo] {
        try decode(await roundTrip("PROC-LIST"))
    }

    func procListRouted() async throws -> [Int] {
        try decode(await roundTrip("PROC-LIST-ROUTED"))
    }

    func procRoute(_ pid: Int) async throws {
        _ = try await roundTrip("PROC-ROUTE", String(pid))
    }

    func procUnroute(_ pid: Int) async throws {
        _ = try await roundTrip("PROC-UNROUTE", String(pid))
    }

    func procKill(_ pid: Int) async throws {
        _ = try await roundTrip("PROC-KILL", String(pid))
    }

    /// Terminates `pid` and relaunches it routed; returns the new PID (a bare
    /// decimal reply, not JSON).
    func procRestart(_ pid: Int) async throws -> Int {
        try decodeBarePID(await roundTrip("PROC-RESTART", String(pid)))
    }

    /// Launches argv[0] (with the rest as arguments) routed through the proxy;
    /// returns the child PID (bare decimal reply). The argument is a JSON array.
    func procLaunch(argv: [String]) async throws -> Int {
        let data = try JSONEncoder().encode(argv)
        let reply = try await roundTrip("PROC-LAUNCH", String(data: data, encoding: .utf8) ?? "[]")
        return try decodeBarePID(reply)
    }

    // MARK: - Whole-application routing (live snapshot, by bundle ID)

    func appList() async throws -> [Application] {
        try decode(await roundTrip("APP-LIST"))
    }

    func appListRouted() async throws -> [String] {
        try decode(await roundTrip("APP-LIST-ROUTED"))
    }

    func appRoute(_ bundleID: String) async throws {
        _ = try await roundTrip("APP-ROUTE", bundleID.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    func appUnroute(_ bundleID: String) async throws {
        _ = try await roundTrip("APP-UNROUTE", bundleID.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    // MARK: - Persistent proxied-apps store

    func appListProxied() async throws -> [ProxiedApp] {
        try decode(await roundTrip("APP-LIST-PROXIED"))
    }

    /// Launches an app by its .app bundle path (e.g. from InstalledApps),
    /// adding it to the proxied-apps store; returns the child PID.
    func appLaunch(path: String) async throws -> Int {
        try decodeBarePID(await roundTrip("APP-LAUNCH", path))
    }

    func appSetEnabled(bundleID: String, enabled: Bool) async throws {
        struct Payload: Encodable {
            let bundleID: String
            let enabled: Bool
        }
        let data = try JSONEncoder().encode(Payload(bundleID: bundleID, enabled: enabled))
        _ = try await roundTrip("APP-SET-ENABLED", String(data: data, encoding: .utf8) ?? "")
    }

    func appRemove(_ bundleID: String) async throws {
        _ = try await roundTrip("APP-REMOVE", bundleID.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    // MARK: - Decoding helpers

    private func decode<T: Decodable>(_ reply: String) throws -> T {
        guard let data = reply.data(using: .utf8) else {
            throw ControlClientError.badReply(reply)
        }
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw ControlClientError.badReply("\(reply) (\(error))")
        }
    }

    private func decodeBarePID(_ reply: String) throws -> Int {
        guard let pid = Int(reply.trimmingCharacters(in: .whitespacesAndNewlines)) else {
            throw ControlClientError.badReply(reply)
        }
        return pid
    }

    // MARK: - Wire transport

    /// Sends "VERB[ ARG]\n" over a fresh AF_UNIX connection and returns the
    /// trimmed reply, throwing .serverError for an "ERR " reply.
    private func roundTrip(_ verb: String, _ arg: String? = nil) async throws -> String {
        guard let endpoint = InstanceDiscovery.currentEndpoint() else {
            throw ControlClientError.noDaemon
        }
        let socketPath = endpoint.controlSocket
        let connectTimeout = self.connectTimeout
        let ioTimeout = self.ioTimeout
        let raw = try await Task.detached(priority: .userInitiated) {
            try Self.blockingRoundTrip(
                socketPath: socketPath, verb: verb, arg: arg,
                connectTimeout: connectTimeout, ioTimeout: ioTimeout
            )
        }.value
        var reply = raw
        while reply.hasSuffix("\n") || reply.hasSuffix("\r") {
            reply.removeLast()
        }
        if reply.hasPrefix("ERR ") {
            throw ControlClientError.serverError(String(reply.dropFirst("ERR ".count)))
        }
        return reply
    }

    /// Blocking POSIX socket implementation, run off the actor on a detached
    /// task so it never ties up the cooperative thread pool. One connection
    /// per command — mirrors control.Request exactly.
    private static func blockingRoundTrip(
        socketPath: String, verb: String, arg: String?,
        connectTimeout: Int32, ioTimeout: Int32
    ) throws -> String {
        let fd = socket(AF_UNIX, SOCK_STREAM, 0)
        guard fd >= 0 else {
            throw ControlClientError.connectFailed(lastErrorMessage())
        }
        defer { Darwin.close(fd) }

        // Build the sockaddr_un for socketPath.
        var addr = sockaddr_un()
        addr.sun_family = sa_family_t(AF_UNIX)
        let pathBytes = Array(socketPath.utf8)
        let maxLen = MemoryLayout.size(ofValue: addr.sun_path) - 1
        guard pathBytes.count <= maxLen else {
            throw ControlClientError.connectFailed("control socket path too long: \(socketPath)")
        }
        withUnsafeMutableBytes(of: &addr.sun_path) { raw in
            let buf = raw.bindMemory(to: UInt8.self)
            for i in 0..<buf.count { buf[i] = 0 }
            for (i, b) in pathBytes.enumerated() { buf[i] = b }
        }
        addr.sun_len = UInt8(MemoryLayout<sockaddr_un>.size)

        // Non-blocking connect with a bounded timeout (AF_UNIX connects are
        // normally instant, but a stuck/overloaded daemon shouldn't hang us).
        let flags = fcntl(fd, F_GETFL, 0)
        _ = fcntl(fd, F_SETFL, flags | O_NONBLOCK)

        let connectResult = withUnsafePointer(to: &addr) { ptr -> Int32 in
            ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { sockPtr in
                connect(fd, sockPtr, socklen_t(MemoryLayout<sockaddr_un>.size))
            }
        }
        if connectResult != 0 {
            let err = errno
            if err != EINPROGRESS {
                throw ControlClientError.connectFailed(errorMessage(err))
            }
            var pfd = pollfd(fd: fd, events: Int16(POLLOUT), revents: 0)
            let pollResult = poll(&pfd, 1, connectTimeout * 1000)
            if pollResult == 0 {
                throw ControlClientError.timeout
            }
            if pollResult < 0 {
                throw ControlClientError.connectFailed(lastErrorMessage())
            }
            var soErr: Int32 = 0
            var soErrLen = socklen_t(MemoryLayout<Int32>.size)
            getsockopt(fd, SOL_SOCKET, SO_ERROR, &soErr, &soErrLen)
            if soErr != 0 {
                throw ControlClientError.connectFailed(errorMessage(soErr))
            }
        }
        // Back to blocking mode for send/recv, bounded by SO_*TIMEO below.
        _ = fcntl(fd, F_SETFL, flags)

        var tv = timeval(tv_sec: Int(ioTimeout), tv_usec: 0)
        _ = setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))
        _ = setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, socklen_t(MemoryLayout<timeval>.size))

        var line = verb
        if let arg, !arg.isEmpty {
            line += " " + arg
        }
        line += "\n"
        let sendBytes = Array(line.utf8)
        var sent = 0
        while sent < sendBytes.count {
            let n = sendBytes.withUnsafeBytes { raw -> Int in
                Darwin.send(fd, raw.baseAddress!.advanced(by: sent), sendBytes.count - sent, 0)
            }
            if n <= 0 {
                throw ControlClientError.connectFailed(lastErrorMessage())
            }
            sent += n
        }

        var replyData = Data()
        var buf = [UInt8](repeating: 0, count: 8192)
        while true {
            let n = buf.withUnsafeMutableBytes { raw -> Int in
                Darwin.recv(fd, raw.baseAddress, raw.count, 0)
            }
            if n < 0 {
                throw ControlClientError.connectFailed(lastErrorMessage())
            }
            if n == 0 { break } // EOF: server closed after one reply line
            replyData.append(buf, count: n)
        }

        guard let text = String(data: replyData, encoding: .utf8) else {
            throw ControlClientError.badReply("non-UTF8 reply (\(replyData.count) bytes)")
        }
        return text
    }

    private static func lastErrorMessage() -> String { errorMessage(errno) }

    private static func errorMessage(_ code: Int32) -> String {
        String(cString: strerror(code))
    }
}
