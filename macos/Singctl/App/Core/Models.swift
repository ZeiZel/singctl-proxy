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

    private enum CodingKeys: String, CodingKey {
        case socksPort = "SocksPort"
        case clashEnabled = "ClashEnabled"
        case clashAddr = "ClashAddr"
        case urlTestURL = "URLTestURL"
        case urlTestInterval = "URLTestInterval"
        case urlTestTolerance = "URLTestTolerance"
        case saveProfile = "SaveProfile"
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
