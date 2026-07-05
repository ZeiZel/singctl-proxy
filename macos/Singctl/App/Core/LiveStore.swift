// LiveStore.swift
//
// The live-data hub for the SwiftUI app: a 2s poll loop mirroring
// gui/bridge/poller.go, but pull-based (published properties instead of Wails
// events). Talks to the injected `Backend` (`DaemonBackend` in the
// Developer-ID build, `TunnelBackend` in the App Store build — see
// Backend.swift) so the same loop drives both build flavors identically.
//
// Console output (`ConsoleScreen`) isn't part of the `Backend` contract — it
// only exists in the Developer-ID build, and isn't something a kept screen
// needs — so it's polled directly over `ControlClient` under `#if !APPSTORE`,
// exactly as before.

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
    /// Only ever populated in the Developer-ID build (see tick() below).
    @Published private(set) var console: [ConsoleLine] = []

    // MARK: - Tuning (mirrors poller.go's pollInterval / rolling-window sizes)

    private let pollInterval: TimeInterval = 2
    private let maxTrafficSamples = 60
    private let maxConsoleLines = 500

    // MARK: - Internals

    private let backend: Backend
    private var loopTask: Task<Void, Never>?

    private var lastUp: Int64 = 0
    private var lastDown: Int64 = 0
    private var haveLastTraffic = false

    #if !APPSTORE
    // Console polling is a Dev-ID-only concern and isn't part of the Backend
    // contract (ConsoleScreen doesn't exist in the App Store build), so it
    // talks straight to ControlClient exactly as before.
    private let control = ControlClient()
    private var lastConsoleID = 0
    #endif

    /// `backend` defaults per build flag so call sites that don't care (e.g.
    /// previews) get a working instance; `SingctlApp` passes its one shared
    /// `Backend` explicitly so LiveStore and the injected `\.backend`
    /// environment value are the same object.
    init(backend: Backend? = nil) {
        #if APPSTORE
        self.backend = backend ?? TunnelBackend()
        #else
        self.backend = backend ?? DaemonBackend()
        #endif
    }

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
    /// effort. `Backend.traffic()`/`.connections()`/`.latency()` each fail
    /// (and are silently skipped via `try?`) exactly when the pre-`Backend`
    /// code used to skip them — see DaemonBackend's doc comments for the
    /// Dev-ID gating this preserves.
    private func tick() async {
        do {
            status = try await backend.status()
            daemonRunning = true
        } catch {
            // No live daemon/tunnel (or it vanished between polls): report
            // absent, keep the last snapshot.
            daemonRunning = false
            haveLastTraffic = false
            return
        }

        #if !APPSTORE
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
        #endif

        // Cumulative byte counters -> per-second rate, guarded against counter
        // resets (e.g. a live core reload restarts the counters) — mirrors
        // poller.go's lastUp/lastDown/haveLast guard exactly.
        if let traffic = try? await backend.traffic() {
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

        if let conns = try? await backend.connections() {
            connections = conns.connRows
            totalUp = conns.uploadTotal
            totalDown = conns.downloadTotal
        }

        if let lat = try? await backend.latency() {
            latency = lat
        }
    }
}
