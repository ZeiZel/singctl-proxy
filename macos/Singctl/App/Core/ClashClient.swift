// ClashClient.swift
//
// A tiny read-only HTTP client for sing-box's Clash-compatible API, mirroring
// internal/clashapi/client.go. The daemon exposes this on loopback with a
// random secret when Settings.ClashEnabled is true (see Settings.ClashAddr,
// advertised per-instance as clash_api_addr/clash_secret in instance.json —
// see InstanceDiscovery). Used purely for observability: live connections,
// per-server urltest latency, and active latency probes.

import Foundation

/// Errors surfaced by ClashClient.
enum ClashClientError: Error, LocalizedError {
    case clashDisabled
    case httpStatus(Int, String)
    case badResponse(String)

    var errorDescription: String? {
        switch self {
        case .clashDisabled: return "Clash API is not enabled on the daemon"
        case .httpStatus(let code, let path): return "clash api \(path): status \(code)"
        case .badResponse(let msg): return "bad clash api response: \(msg)"
        }
    }
}

/// Talks to one running sing-box Clash API instance. baseURL is like
/// "http://127.0.0.1:9090"; secret may be empty. Stateless/Sendable — safe to
/// share or recreate per call.
struct ClashClient: Sendable {
    let baseURL: String
    let secret: String
    private let session: URLSession

    /// Builds a client for a "host:port" external_controller address, mirroring
    /// clashapi.NewClient. Pass the values from InstanceDiscovery's Endpoint.
    init(externalController: String, secret: String) {
        self.baseURL = "http://" + externalController
        self.secret = secret
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 5
        self.session = URLSession(configuration: config)
    }

    /// GET /connections: cumulative totals plus the live connection table.
    func connections() async throws -> ClashConnections {
        try await getJSON("/connections")
    }

    /// GET /proxies: proxy/group states (urltest selection + per-node latency).
    func proxies() async throws -> ClashProxies {
        try await getJSON("/proxies")
    }

    /// Actively latency-tests a single outbound tag against testURL, with the
    /// probe bounded by timeoutMs. Mirrors clashapi.Client.Delay.
    func delay(tag: String, url testURL: String, timeoutMs: Int) async throws -> Int {
        var comps = URLComponents()
        comps.queryItems = [
            URLQueryItem(name: "url", value: testURL),
            URLQueryItem(name: "timeout", value: String(timeoutMs)),
        ]
        let query = comps.percentEncodedQuery ?? ""
        let escapedTag = tag.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? tag
        let path = "/proxies/\(escapedTag)/delay?\(query)"
        struct DelayResponse: Decodable { let delay: Int }
        let resp: DelayResponse = try await getJSON(path)
        return resp.delay
    }

    // MARK: - Transport

    private func getJSON<T: Decodable>(_ path: String) async throws -> T {
        guard let url = URL(string: baseURL + path) else {
            throw ClashClientError.badResponse("invalid URL for \(path)")
        }
        var request = URLRequest(url: url)
        request.httpMethod = "GET"
        if !secret.isEmpty {
            request.setValue("Bearer \(secret)", forHTTPHeaderField: "Authorization")
        }
        let (data, response) = try await session.data(for: request)
        guard let http = response as? HTTPURLResponse else {
            throw ClashClientError.badResponse("no HTTP response for \(path)")
        }
        guard http.statusCode == 200 else {
            throw ClashClientError.httpStatus(http.statusCode, path)
        }
        do {
            return try JSONDecoder().decode(T.self, from: data)
        } catch {
            throw ClashClientError.badResponse("\(path): \(error)")
        }
    }
}

// MARK: - Derived UI helpers (ported from gui/bridge/types.go)

extension ClashClient {

    /// Converts Clash connections into table rows. Mirrors bridge.connRows.
    static func connRows(from connections: ClashConnections) -> [ConnRow] {
        connections.connections.map { c in
            ConnRow(
                process: c.metadata.process,
                source: c.metadata.source,
                dest: c.metadata.dest,
                network: c.metadata.network,
                chain: c.chains.joined(separator: "→")
            )
        }
    }

    /// Extracts the failover group's latency + selection from /proxies.
    /// Mirrors bridge.latencyFrom exactly: the group is named "proxy"; returns
    /// nil when absent. A urltest group (has `all`) lists every member with
    /// its own last-probe delay and marks the currently-selected one; a single
    /// server (no `all`) is reported as one synthetic "proxy" row.
    static func latency(from proxies: ClashProxies) -> Latency? {
        guard let group = proxies.proxies["proxy"] else { return nil }
        var rows: [LatencyRow] = []
        var selected: String
        if let all = group.all, !all.isEmpty {
            selected = group.now ?? ""
            for tag in all {
                let member = proxies.proxies[tag]
                rows.append(LatencyRow(tag: tag, delay: member?.lastDelay ?? 0, selected: tag == selected))
            }
        } else {
            selected = "proxy"
            rows = [LatencyRow(tag: "proxy", delay: group.lastDelay, selected: true)]
        }
        return Latency(selected: selected, rows: rows)
    }
}
