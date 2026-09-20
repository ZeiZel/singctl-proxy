// Models.swift
//
// Codable wire-format models shared by ControlClient and ClashClient. Field
// names/CodingKeys are chosen to match the Go JSON exactly — see:
//   internal/control/server.go   (DaemonStatus, Traffic)
//   gui/bridge/types.go          (Settings, ProcInfo, Application, ProxiedApp,
//                                 ConsoleLine, ConnRow, LatencyRow, Latency)
//   internal/clashapi/client.go  (Clash* types)
//
// NOTE on ProcInfo: the task sketch assumed `Ports:[Int]` and
// `Children:[ProcInfo]?`, but internal/ui/messages.go's ui.ProcInfo (mirrored
// verbatim by gui/bridge/types.go, confirmed by reading both files) is:
//   type ProcInfo struct { PID int; Name string; Ports string; Children int }
// i.e. Ports is a pre-rendered string like ":80 :443", and Children is a plain
// count of folded helper processes (not a recursive array). This file follows
// the real wire format.

import Foundation

// MARK: - Control-socket payloads (internal/control/server.go)

/// STATUS reply. Mirrors control.Status exactly (snake_case wire keys).
struct DaemonStatus: Codable, Equatable {
    var pid: Int
    var mode: String
    var startedAt: String
    var ciscoActive: Bool
    var proxyBypass: Bool
    var physIface: String
    var netextSupported: Bool
    var netextAvailable: Bool

    private enum CodingKeys: String, CodingKey {
        case pid
        case mode
        case startedAt = "started_at"
        case ciscoActive = "cisco_active"
        case proxyBypass = "proxy_bypass"
        case physIface = "phys_iface"
        case netextSupported = "netext_supported"
        case netextAvailable = "netext_available"
    }

    /// The Go fields are all `omitempty`, so a reply may omit any of them.
    /// Decode with defaults so a partial payload still decodes.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pid = try c.decodeIfPresent(Int.self, forKey: .pid) ?? 0
        mode = try c.decodeIfPresent(String.self, forKey: .mode) ?? ""
        startedAt = try c.decodeIfPresent(String.self, forKey: .startedAt) ?? ""
        ciscoActive = try c.decodeIfPresent(Bool.self, forKey: .ciscoActive) ?? false
        proxyBypass = try c.decodeIfPresent(Bool.self, forKey: .proxyBypass) ?? false
        physIface = try c.decodeIfPresent(String.self, forKey: .physIface) ?? ""
        netextSupported = try c.decodeIfPresent(Bool.self, forKey: .netextSupported) ?? false
        netextAvailable = try c.decodeIfPresent(Bool.self, forKey: .netextAvailable) ?? false
    }

    init(
        pid: Int, mode: String, startedAt: String, ciscoActive: Bool, proxyBypass: Bool,
        physIface: String, netextSupported: Bool, netextAvailable: Bool
    ) {
        self.pid = pid
        self.mode = mode
        self.startedAt = startedAt
        self.ciscoActive = ciscoActive
        self.proxyBypass = proxyBypass
        self.physIface = physIface
        self.netextSupported = netextSupported
        self.netextAvailable = netextAvailable
    }

    static let empty = DaemonStatus(
        pid: 0, mode: "off", startedAt: "", ciscoActive: false, proxyBypass: false,
        physIface: "", netextSupported: false, netextAvailable: false
    )
}

/// TRAFFIC reply: cumulative byte counters (not rates).
struct Traffic: Codable, Equatable {
    var up: Int64
    var down: Int64

    static let zero = Traffic(up: 0, down: 0)
}

/// One server in the failover group, as reported by PROXY-GROUP. `delay` is
/// the last urltest probe result in milliseconds; 0 means timeout/unknown
/// (same convention as `LatencyRow.delay` below).
struct ProxyGroupMember: Codable, Equatable, Identifiable {
    var tag: String
    var index: Int
    var name: String
    var delay: Int

    var id: String { tag }
}

/// PROXY-GROUP reply. `available == false` means single-server mode: there is
/// no failover group configured at all, and callers should hide the picker
/// entirely rather than render an empty one. `selected` is always the
/// EFFECTIVE member's tag, even while `auto == true` — auto mode still
/// resolves to one concrete member, it's just chosen by urltest rather than
/// pinned via PROXY-SELECT.
struct ProxyGroup: Codable, Equatable {
    var available: Bool
    var auto: Bool
    var selected: String
    var members: [ProxyGroupMember]

    static let empty = ProxyGroup(available: false, auto: true, selected: "", members: [])
}

/// SETTINGS-GET/SET payload. PascalCase wire keys — matches the daemon's
/// ui.Settings Go struct verbatim on both ends.
struct Settings: Codable, Equatable {
    var socksPort: Int
    var clashEnabled: Bool
    var clashAddr: String
    var urlTestURL: String
    var urlTestInterval: String
    var urlTestTolerance: Int
    var saveProfile: Bool
    /// "off" | "proxy" | "vpn" — what the daemon should switch to on its own
    /// startup (F2). Added alongside this daemon-side key by another agent's
    /// concurrent work on SETTINGS-GET/SET; `nil` decodes cleanly (via the
    /// synthesized `decodeIfPresent`) against an older daemon build that
    /// doesn't yet send the key, and is simply omitted on encode when unset.
    var autostartMode: String?

    private enum CodingKeys: String, CodingKey {
        case socksPort = "SocksPort"
        case clashEnabled = "ClashEnabled"
        case clashAddr = "ClashAddr"
        case urlTestURL = "URLTestURL"
        case urlTestInterval = "URLTestInterval"
        case urlTestTolerance = "URLTestTolerance"
        case saveProfile = "SaveProfile"
        case autostartMode = "AutostartMode"
    }
}

/// PROC-LIST row. Mirrors ui.ProcInfo (internal/ui/messages.go) as re-exported
/// by gui/bridge/types.go: `Ports` is a pre-rendered string (":80 :443"),
/// `Children` is a count of folded helper processes, not a nested array.
struct ProcInfo: Codable, Equatable, Identifiable {
    var pid: Int
    var name: String
    var ports: String
    var children: Int

    var id: Int { pid }

    private enum CodingKeys: String, CodingKey {
        case pid = "PID"
        case name = "Name"
        case ports = "Ports"
        case children = "Children"
    }

    /// Display label mirroring ui.ProcInfo.Label(): "<name> (+N)" when helper
    /// processes are folded under this row.
    var label: String {
        children > 0 ? "\(name) (+\(children))" : name
    }
}

/// APP-LIST row: a whole running application grouped by macOS bundle ID.
/// Mirrors app.Application (gui/bridge/types.go's Application), camelCase.
struct Application: Codable, Equatable, Identifiable {
    var name: String
    var bundleID: String
    var running: Bool
    var pids: [Int]

    var id: String { bundleID }
}

/// APP-LIST-PROXIED row: an entry in the persistent proxied-apps store —
/// survives daemon restarts/app relaunches, unlike Application above.
struct ProxiedApp: Codable, Equatable, Identifiable {
    var bundleID: String
    var name: String
    var enabled: Bool
    var running: Bool

    var id: String { bundleID }
}

/// CONSOLE-POLL row: one captured stdout/stderr line from a proxied app.
struct ConsoleLine: Codable, Equatable, Identifiable {
    var id: Int
    var pid: Int
    var app: String
    var stream: String
    var text: String
}

/// SYSPROXY-STATUS reply: the system proxy's current applied state (mode,
/// which network service it's applied to, whether the localhost PAC server
/// is answering, and how many domains the active list holds). Wire keys are
/// already camelCase, so no `CodingKeys` remapping is needed. Mirrors the
/// generated-PAC + `networksetup` mechanism the Makefile's `proxy-on`/
/// `proxy-pac`/`proxy-off` targets used to drive by hand — see
/// SysProxyScreen.swift.
struct SysProxyStatus: Codable, Equatable {
    /// "off" | "exclude" | "include".
    var mode: String
    var pacServerUp: Bool
    var service: String
    var pacURL: String
    var domains: Int

    /// The daemon speaks snake_case here (see `sysproxy.Status` in
    /// internal/sysproxy/manager.go), like every other control payload. It also
    /// marks `pac_url` omitempty, so the key is ABSENT — not empty — whenever
    /// the proxy is off, which is the common case on first open. Both facts have
    /// to be handled explicitly or the whole status fails to decode and the
    /// screen shows a decoding error instead of "Off".
    enum CodingKeys: String, CodingKey {
        case mode
        case service
        case pacServerUp = "pac_server_up"
        case pacURL = "pac_url"
        case domains = "domain_count"
    }

    init(mode: String, pacServerUp: Bool, service: String, pacURL: String, domains: Int) {
        self.mode = mode
        self.pacServerUp = pacServerUp
        self.service = service
        self.pacURL = pacURL
        self.domains = domains
    }

    init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        mode = try container.decodeIfPresent(String.self, forKey: .mode) ?? "off"
        service = try container.decodeIfPresent(String.self, forKey: .service) ?? ""
        pacServerUp = try container.decodeIfPresent(Bool.self, forKey: .pacServerUp) ?? false
        pacURL = try container.decodeIfPresent(String.self, forKey: .pacURL) ?? ""
        domains = try container.decodeIfPresent(Int.self, forKey: .domains) ?? 0
    }

    static let empty = SysProxyStatus(mode: "off", pacServerUp: false, service: "", pacURL: "", domains: 0)
}

/// SYSPROXY-SET request body: `{"mode": "off"|"exclude"|"include"}`. The
/// screen only ever drives `mode` explicitly (see SysProxyScreen's mode
/// picker) — everything else in SYSPROXY-STATUS is read-only derived state.
struct SysProxySetRequest: Codable, Equatable {
    var mode: String
}

// MARK: - Local (non-daemon) models

/// A locally-scanned installed application (see InstalledApps.swift), distinct
/// from Application above (which reflects live *running* processes grouped by
/// bundle ID). Mirrors gui/bridge/types.go's InstalledApp.
struct InstalledApp: Codable, Equatable, Identifiable {
    var name: String
    var bundleID: String
    var path: String

    var id: String { bundleID }
}

// MARK: - Clash API models (internal/clashapi/client.go)

/// One connection's metadata from GET /connections. Field names/casing match
/// clashapi.Metadata's JSON tags exactly.
struct ClashMetadata: Codable, Equatable {
    var network: String
    var sourceIP: String
    var destinationIP: String
    var sourcePort: String
    var destinationPort: String
    var host: String
    var process: String
    var processPath: String

    /// Mirrors Metadata.Dest(): host when known, else the destination IP, with
    /// port appended as "host:port".
    var dest: String {
        let h = host.isEmpty ? destinationIP : host
        guard !destinationPort.isEmpty else { return h }
        return joinHostPort(h, destinationPort)
    }

    /// Mirrors Metadata.Source(): "ip:port" of the local client.
    var source: String {
        guard !sourcePort.isEmpty else { return sourceIP }
        return joinHostPort(sourceIP, sourcePort)
    }

    private func joinHostPort(_ host: String, _ port: String) -> String {
        // Mirrors net.JoinHostPort: bracket literal IPv6 hosts.
        if host.contains(":") {
            return "[\(host)]:\(port)"
        }
        return "\(host):\(port)"
    }
}

/// One active connection, from GET /connections.
struct ClashConn: Codable, Equatable, Identifiable {
    var id: String
    var metadata: ClashMetadata
    var upload: Int64
    var download: Int64
    var chains: [String]
    var rule: String
}

/// GET /connections response envelope: cumulative totals plus the live table.
struct ClashConnections: Codable, Equatable {
    var downloadTotal: Int64
    var uploadTotal: Int64
    var connections: [ClashConn]

    /// Converts connections into table rows. Mirrors bridge.connRows (also
    /// duplicated by ClashClient.connRows, kept for source compatibility) —
    /// defined here rather than solely on `ClashClient` so `LiveStore` (kept
    /// in both the Dev-ID and App Store builds) can derive `[ConnRow]` from
    /// whatever `Backend.connections()` returns without depending on
    /// `ClashClient`, which is excluded from the App Store target.
    var connRows: [ConnRow] {
        connections.map { c in
            ConnRow(
                process: c.metadata.process,
                source: c.metadata.source,
                dest: c.metadata.dest,
                network: c.metadata.network,
                chain: c.chains.joined(separator: "→")
            )
        }
    }
}

/// One latency probe result, from ProxyState.History.
struct ClashDelayHistory: Codable, Equatable {
    var time: String?
    var delay: Int

    /// History entries may omit `time` in some sing-box versions; decode
    /// permissively (we only ever read the last entry's delay).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        time = try c.decodeIfPresent(String.self, forKey: .time)
        delay = try c.decodeIfPresent(Int.self, forKey: .delay) ?? 0
    }

    init(time: String?, delay: Int) {
        self.time = time
        self.delay = delay
    }

    private enum CodingKeys: String, CodingKey { case time, delay }
}

/// One proxy or group entry from GET /proxies. Mirrors clashapi.ProxyState.
struct ClashProxy: Codable, Equatable {
    var type: String
    var name: String
    var now: String?
    var all: [String]?
    var history: [ClashDelayHistory]?

    /// Mirrors ProxyState.LastDelay(): the most recent probe's delay, 0 if none.
    var lastDelay: Int { history?.last?.delay ?? 0 }
}

/// GET /proxies response envelope.
struct ClashProxies: Codable, Equatable {
    var proxies: [String: ClashProxy]
}

// MARK: - Derived UI rows (ported from gui/bridge/types.go)

/// One row in the live-connections table. Mirrors bridge.ConnRow.
struct ConnRow: Codable, Equatable, Identifiable {
    var process: String
    var source: String
    var dest: String
    var network: String
    var chain: String

    var id: String { "\(process)|\(source)|\(dest)|\(network)|\(chain)" }
}

/// One server's measured latency within the failover group. Mirrors
/// bridge.LatencyRow.
struct LatencyRow: Codable, Equatable, Identifiable {
    var tag: String
    var delay: Int
    var selected: Bool

    var id: String { tag }
}

/// Per-server latency snapshot + the currently-selected server. Mirrors
/// bridge.Latency.
struct Latency: Codable, Equatable {
    var selected: String
    var rows: [LatencyRow]

    static let empty = Latency(selected: "", rows: [])
}

// MARK: - CONNECTIONS control command (F6 in docs/v2-spec.md)
//
// A distinct, richer command from the Clash API's own GET /connections
// (ClashConnections/ConnRow above, still used as-is for Dashboard's traffic/
// connection-count stats): the daemon's CONNECTIONS control verb wraps that
// same live table with per-app/per-destination aggregates AND an explicit
// diagnosis for why the table might be empty (no mode running / Clash API
// disabled / API unreachable / genuinely idle) — the single source of truth
// ConnectionsScreen's empty state and the Dashboard's traffic empty state
// both read, so the two can never disagree about which is true. Mirrors
// clashapi.Payload/Row/AppTotal/DestTotal (internal/clashapi/connections.go)
// field-for-field.

/// Why the CONNECTIONS table might have nothing to show. Mirrors
/// clashapi.State exactly.
enum ConnectionsState: String, Codable, Equatable {
    /// No proxy/VPN mode is running at all.
    case noMode = "no_mode"
    /// A mode is up but the Clash API is switched off in Settings.
    case apiDisabled = "api_disabled"
    /// A mode is up and the API is enabled, but the request to it failed —
    /// `detail` names the address tried and the error.
    case apiUnreachable = "api_unreachable"
    /// The API answered; there is simply no traffic right now.
    case idle
    /// The API answered with at least one live connection.
    case active
}

/// One live connection row from the CONNECTIONS command. Mirrors
/// clashapi.Row exactly.
struct ConnectionRow: Codable, Equatable, Identifiable {
    var id: String
    var app: String
    var process: String
    var host: String
    var port: String
    var network: String
    var rule: String
    var chain: [String]
    var upload: Int64
    var download: Int64
    /// Raw RFC3339 start time (Go's `time.Time` wire format) — parse with
    /// `WireTime.parse` (declared below), same as Subscription's timestamps.
    var start: String

    private enum CodingKeys: String, CodingKey {
        case id, app, process, host, port, network, rule, chain, upload, download, start
    }

    /// Most of the Go fields are `omitempty`; decode permissively so a
    /// partial row (e.g. no process metadata at all) still decodes instead
    /// of failing the whole payload.
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decodeIfPresent(String.self, forKey: .id) ?? ""
        app = try c.decodeIfPresent(String.self, forKey: .app) ?? ""
        process = try c.decodeIfPresent(String.self, forKey: .process) ?? ""
        host = try c.decodeIfPresent(String.self, forKey: .host) ?? ""
        port = try c.decodeIfPresent(String.self, forKey: .port) ?? ""
        network = try c.decodeIfPresent(String.self, forKey: .network) ?? ""
        rule = try c.decodeIfPresent(String.self, forKey: .rule) ?? ""
        chain = try c.decodeIfPresent([String].self, forKey: .chain) ?? []
        upload = try c.decodeIfPresent(Int64.self, forKey: .upload) ?? 0
        download = try c.decodeIfPresent(Int64.self, forKey: .download) ?? 0
        start = try c.decodeIfPresent(String.self, forKey: .start) ?? ""
    }

    /// Parsed connection start time, or `nil` when unset/Go's zero value.
    var startDate: Date? { WireTime.parse(start) }

    /// Friendly destination label: host with port appended when known —
    /// mirrors ClashMetadata.dest's "host:port" shape.
    var destination: String {
        port.isEmpty ? host : "\(host):\(port)"
    }
}

/// One application's aggregate across every connection currently attributed
/// to it. Mirrors clashapi.AppTotal.
struct ConnectionAppTotal: Codable, Equatable, Identifiable {
    var app: String
    var upload: Int64
    var download: Int64
    var count: Int

    var id: String { app }
}

/// One destination host's aggregate across every connection currently
/// dialing it. Mirrors clashapi.DestTotal.
struct ConnectionDestTotal: Codable, Equatable, Identifiable {
    var host: String
    var upload: Int64
    var download: Int64
    var count: Int

    var id: String { host }
}

/// CONNECTIONS reply: the live table, its per-app/per-destination
/// aggregates, and the explicit `state` diagnosis (F6 item 3). `rows`/
/// `apps`/`dests` may be `null` as well as empty — Swift's synthesized
/// decoding maps both to `nil`, so callers must handle `nil` the same as
/// an empty array (never assume one implies the other means something
/// different). Mirrors clashapi.Payload.
struct ConnectionsPayload: Codable, Equatable {
    var state: ConnectionsState
    var rows: [ConnectionRow]?
    var apps: [ConnectionAppTotal]?
    var dests: [ConnectionDestTotal]?
    var detail: String?

    static let empty = ConnectionsPayload(state: .noMode, rows: nil, apps: nil, dests: nil, detail: nil)
}

// MARK: - Firewall (F6 item 5 in docs/v2-spec.md)

/// One firewall rule: block or allow traffic matching exactly one of
/// domain/cidr/process. Mirrors internal/firewall.Rule exactly. The client
/// pre-validates "exactly one match field set, well-formed CIDR" before
/// sending (see ConnectionsScreen) purely for fast feedback — the daemon
/// (FirewallAdd -> Rule.Validate) remains the authority and re-checks on
/// FIREWALL-ADD.
struct FirewallRule: Codable, Equatable, Identifiable {
    var id: String
    /// "block" | "allow".
    var action: String
    var domain: String?
    var cidr: String?
    var process: String?
}

extension FirewallRule {
    /// The match half of firewall.Rule.MatchDescription (without the action
    /// prefix, which callers usually already show via a separate badge).
    var matchDescription: String {
        if let domain, !domain.isEmpty { return "domain \(domain)" }
        if let cidr, !cidr.isEmpty { return "cidr \(cidr)" }
        if let process, !process.isEmpty { return "process \(process)" }
        return "—"
    }
}

// MARK: - Subscriptions (internal/sub/record.go)

/// Parses Go's RFC3339 `time.Time` wire format. `ControlClient.decode`'s
/// `JSONDecoder()` has no date strategy configured (see DaemonStatus above,
/// which decodes its own string fields manually for the same reason), so
/// Subscription/SubscriptionMeta decode their time fields as raw strings and
/// parse them through here. An empty string or Go's zero-value sentinel
/// ("0001-01-01T00:00:00Z", which `omitempty` does NOT strip for struct-typed
/// fields like `time.Time` — a well-known encoding/json quirk) both mean "not
/// reported", so both parse to `nil`.
enum WireTime {
    private static let formatter: ISO8601DateFormatter = {
        let f = ISO8601DateFormatter()
        f.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return f
    }()
    private static let formatterNoFraction = ISO8601DateFormatter()

    static func parse(_ raw: String) -> Date? {
        guard !raw.isEmpty, !raw.hasPrefix("0001-01-01T00:00:00Z") else { return nil }
        return formatter.date(from: raw) ?? formatterNoFraction.date(from: raw)
    }
}

/// A subscription's optional panel-reported profile info. Mirrors
/// internal/sub/sub.go's `Meta` exactly — every field may be absent/zero when
/// the panel's `subscription-userinfo` header didn't report it.
struct SubscriptionMeta: Codable, Equatable {
    var title: String
    /// `time.Duration` wire value, in nanoseconds (Go's default int64 JSON
    /// encoding for a Duration — no custom Marshaler in internal/sub).
    var updateIntervalNanos: Int64
    var upload: Int64
    var download: Int64
    /// Bytes in the plan; 0 = unknown/unlimited (mirrors Meta.Total's doc).
    var total: Int64
    var expireRaw: String

    private enum CodingKeys: String, CodingKey {
        case title
        case updateIntervalNanos = "update_interval"
        case upload
        case download
        case total
        case expireRaw = "expire"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        updateIntervalNanos = try c.decodeIfPresent(Int64.self, forKey: .updateIntervalNanos) ?? 0
        upload = try c.decodeIfPresent(Int64.self, forKey: .upload) ?? 0
        download = try c.decodeIfPresent(Int64.self, forKey: .download) ?? 0
        total = try c.decodeIfPresent(Int64.self, forKey: .total) ?? 0
        expireRaw = try c.decodeIfPresent(String.self, forKey: .expireRaw) ?? ""
    }

    init(
        title: String, updateIntervalNanos: Int64, upload: Int64, download: Int64,
        total: Int64, expireRaw: String
    ) {
        self.title = title
        self.updateIntervalNanos = updateIntervalNanos
        self.upload = upload
        self.download = download
        self.total = total
        self.expireRaw = expireRaw
    }

    static let empty = SubscriptionMeta(
        title: "", updateIntervalNanos: 0, upload: 0, download: 0, total: 0, expireRaw: ""
    )

    /// Mirrors Meta.HasUsage(): whether the panel sent a usage/quota line
    /// worth showing.
    var hasUsage: Bool { total > 0 || upload > 0 || download > 0 }

    /// Mirrors Meta.Used(): total traffic consumed.
    var used: Int64 { upload + download }

    /// Parsed plan expiry, or `nil` when the panel reported none.
    var expireDate: Date? { WireTime.parse(expireRaw) }
}

/// SUB-LIST row. Mirrors internal/sub/record.go's `Subscription` exactly
/// (snake_case wire keys) — one configured subscription plus the outcome of
/// its last fetch. A subscription OWNS the servers in `links`: they refresh
/// automatically and cannot be renamed/removed individually (KEYS-REMOVE/
/// KEYS-RENAME refuse them — the daemon would just undo it on the next
/// refresh), only the whole subscription can be dropped via SUB-REMOVE.
struct Subscription: Codable, Equatable, Identifiable {
    var url: String
    var title: String
    var addedAtRaw: String
    var lastUpdateRaw: String
    var lastError: String
    /// Share links this subscription currently owns (cached across daemon
    /// restarts — see the Go doc comment on Subscription.Links).
    var links: [String]
    var meta: SubscriptionMeta

    var id: String { url }

    private enum CodingKeys: String, CodingKey {
        case url
        case title
        case addedAtRaw = "added_at"
        case lastUpdateRaw = "last_update"
        case lastError = "last_error"
        case links
        case meta
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        url = try c.decode(String.self, forKey: .url)
        title = try c.decodeIfPresent(String.self, forKey: .title) ?? ""
        addedAtRaw = try c.decodeIfPresent(String.self, forKey: .addedAtRaw) ?? ""
        lastUpdateRaw = try c.decodeIfPresent(String.self, forKey: .lastUpdateRaw) ?? ""
        lastError = try c.decodeIfPresent(String.self, forKey: .lastError) ?? ""
        links = try c.decodeIfPresent([String].self, forKey: .links) ?? []
        meta = try c.decodeIfPresent(SubscriptionMeta.self, forKey: .meta) ?? .empty
    }

    /// Mirrors Subscription.Label(): the panel's title when it sent one, else
    /// the URL's host.
    var label: String {
        if !title.isEmpty { return title }
        if let range = url.range(of: "://") {
            let rest = url[range.upperBound...]
            return String(rest.prefix { $0 != "/" })
        }
        return url
    }

    /// Parsed "when was this subscription last refreshed", or `nil` when it
    /// has never been fetched.
    var lastUpdateDate: Date? { WireTime.parse(lastUpdateRaw) }
}
