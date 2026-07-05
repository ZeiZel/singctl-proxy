// LiveStore.swift
//
// The live-data hub for the SwiftUI app: a 2s poll loop mirroring
// gui/bridge/poller.go, but pull-based (published properties instead of Wails
// events). Resilient to a missing/restarted daemon — see tick() below.

import Foundation

@MainActor
final class LiveStore: ObservableObject {

    // MARK: - Published state

    @Published private(set) var status: DaemonStatus = .empty
    @Published private(set) var daemonRunning: Bool = false

    /// Rolling window of per-second (up, down) byte rates, most-recent last.
    @Published private(set) var trafficSamples: [(up: Double, down: Double)] = []
    @Published private(set) var totalUp: Int64 = 0
    @Published private(set) var totalDown: Int64 = 0

    @Published private(set) var connections: [ConnRow] = []
    @Published private(set) var latency: Latency = .empty

    /// Rolling window of the last maxConsoleLines captured stdout/stderr lines.
    @Published private(set) var console: [ConsoleLine] = []

    // MARK: - Tuning (mirrors poller.go's pollInterval / rolling-window sizes)

    private let pollInterval: TimeInterval = 2
    private let maxTrafficSamples = 60
    private let maxConsoleLines = 500

    // MARK: - Internals

    private let control = ControlClient()
    private var loopTask: Task<Void, Never>?

    private var lastUp: Int64 = 0
    private var lastDown: Int64 = 0
    private var haveLastTraffic = false
    private var lastConsoleID = 0

    init() {}

    /// Starts the 2s poll loop (idempotent — calling start() while already
    /// running is a no-op).
    func start() {
        guard loopTask == nil else { return }
        loopTask = Task { [weak self] in
            guard let self else { return }
            await self.tick() // immediate first poll so the UI populates without waiting
            while !Task.isCancelled {
                try? await Task.sleep(nanoseconds: UInt64(self.pollInterval * 1_000_000_000))
                if Task.isCancelled { break }
                await self.tick()
            }
        }
    }

    /// Cancels the poll loop. Published state is left as-is (last known data).
    func stop() {
        loopTask?.cancel()
        loopTask = nil
    }

    /// One poll cycle. Mirrors poller.go's tick(): status is always attempted
    /// (so the UI reflects daemon presence/mode); everything else is best-
    /// effort and only attempted while a daemon is live and Clash-enabled.
    private func tick() async {
        guard let endpoint = InstanceDiscovery.currentEndpoint() else {
            daemonRunning = false
            haveLastTraffic = false
            return
        }

        do {
            status = try await control.status()
            daemonRunning = true
        } catch {
            // Daemon vanished (or the socket is stale) between discovery and
            // the STATUS round trip — report absent, keep the last snapshot.
            daemonRunning = false
            haveLastTraffic = false
            return
        }

        // Console output over the control socket — independent of Clash.
        if let lines = try? await control.consolePoll(since: lastConsoleID), !lines.isEmpty {
            for line in lines where line.id > lastConsoleID {
                lastConsoleID = line.id
            }
            console.append(contentsOf: lines)
            if console.count > maxConsoleLines {
                console.removeFirst(console.count - maxConsoleLines)
            }
        }

        guard endpoint.clashEnabled else { return }

        // Cumulative byte counters -> per-second rate, guarded against counter
        // resets (e.g. a live core reload restarts the counters) — mirrors
        // poller.go's lastUp/lastDown/haveLast guard exactly.
        if let traffic = try? await control.traffic() {
            if haveLastTraffic {
                let secs = max(pollInterval, 1)
                let upRate = traffic.up >= lastUp ? Double(traffic.up - lastUp) / secs : 0
                let downRate = traffic.down >= lastDown ? Double(traffic.down - lastDown) / secs : 0
                trafficSamples.append((up: upRate, down: downRate))
                if trafficSamples.count > maxTrafficSamples {
                    trafficSamples.removeFirst(trafficSamples.count - maxTrafficSamples)
                }
            }
            lastUp = traffic.up
            lastDown = traffic.down
            haveLastTraffic = true
        }

        let clash = ClashClient(externalController: endpoint.clashAPIAddr, secret: endpoint.clashSecret)

        if let conns = try? await clash.connections() {
            connections = ClashClient.connRows(from: conns)
            totalUp = conns.uploadTotal
            totalDown = conns.downloadTotal
        }

        if let proxies = try? await clash.proxies(), let lat = ClashClient.latency(from: proxies) {
            latency = lat
        }
    }
}
