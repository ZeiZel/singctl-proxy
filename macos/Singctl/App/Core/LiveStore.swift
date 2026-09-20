// LiveStore.swift
//
// The live-data hub for the SwiftUI app: an adaptive poll loop mirroring
// gui/bridge/poller.go, but pull-based (published properties instead of Wails
// events). Talks to the injected `Backend` (`DaemonBackend` in the
// Developer-ID build, `TunnelBackend` in the App Store build — see
// Backend.swift) so the same loop drives both build flavors identically.
//
// Console output (per-app stdout/stderr, surfaced as a source filter on
// LogsScreen — see LogsScreen.swift's file-level comment) isn't part of the
// `Backend` contract — it only exists in the Developer-ID build — so it's
// polled directly over `ControlClient` under `#if !APPSTORE`, exactly as
// before.

import Foundation

/// Polling intervals selected from the last confirmed daemon state. Kept
/// independent from `LiveStore` so interval selection can be unit-tested
/// without a backend or MainActor.
struct LivePollingCadence: Equatable {
    let status: TimeInterval
    let traffic: TimeInterval
    let connections: TimeInterval
    let latency: TimeInterval
    #if !APPSTORE
    let console: TimeInterval
    #endif

    /// Active routing needs a responsive status indicator and a useful
    /// traffic chart. When routing is off (or the daemon is absent), costly
    /// observability calls provide little value, so back them off.
    static func forDaemon(running: Bool, mode: String) -> Self {
        guard running else {
            #if APPSTORE
            return Self(status: 15, traffic: 30, connections: 30, latency: 60)
            #else
            return Self(status: 15, traffic: 30, connections: 30, latency: 60, console: 15)
            #endif
        }

        switch mode {
        case "proxy", "vpn":
            #if APPSTORE
            return Self(status: 3, traffic: 5, connections: 10, latency: 30)
            #else
            return Self(status: 3, traffic: 5, connections: 10, latency: 30, console: 5)
            #endif
        default:
            #if APPSTORE
            return Self(status: 10, traffic: 30, connections: 30, latency: 60)
            #else
            return Self(status: 10, traffic: 30, connections: 30, latency: 60, console: 15)
            #endif
        }
    }
}

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

    // MARK: - Tuning

    /// Active-mode traffic is sampled every five seconds, making this a
    /// five-minute rolling chart rather than the previous two-minute window.
    private let maxTrafficSamples = 60
    private let maxConsoleLines = 500

    // MARK: - Internals

    private let backend: Backend
    private var loopTask: Task<Void, Never>?
    private var pollInFlight = false
    private var pollRequested = false
    private var statusGeneration = 0

    private var nextTrafficPoll = Date.distantPast
    private var nextConnectionsPoll = Date.distantPast
    private var nextLatencyPoll = Date.distantPast

    private var lastUp: Int64 = 0
    private var lastDown: Int64 = 0
    private var lastTrafficSampleAt: Date?
    private var haveLastTraffic = false

    #if !APPSTORE
    // Console polling is a Dev-ID-only concern and isn't part of the Backend
    // contract (LogsScreen, which surfaces it, doesn't exist in the App
    // Store build either), so it talks straight to ControlClient exactly as
    // before.
    private let control = ControlClient()
    private var lastConsoleID = 0
    private var nextConsolePoll = Date.distantPast
    #endif

    /// Captures enough state to restore a failed optimistic mode change. The
    /// generation ensures a slower, older action cannot roll back a newer
    /// selection made in another UI surface (dashboard vs. menu bar).
    struct OptimisticStatusChange {
        fileprivate let previousStatus: DaemonStatus
        fileprivate let generation: Int
    }

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

    /// Starts the adaptive poll loop (idempotent — calling start() while
    /// already running is a no-op).
    func start() {
        guard loopTask == nil else { return }
        loopTask = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                await self.poll()
                if Task.isCancelled { break }
                let cadence = LivePollingCadence.forDaemon(
                    running: self.daemonRunning,
                    mode: self.status.mode
                )
                try? await Task.sleep(nanoseconds: UInt64(cadence.status * 1_000_000_000))
            }
        }
    }

    /// Cancels the poll loop. Published state is left as-is (last known data).
    func stop() {
        loopTask?.cancel()
        loopTask = nil
    }

    /// Updates the visible mode immediately. Call this before `setMode`; on
    /// success call `refreshAfterMutation()`, and on failure pass the token to
    /// `restoreOptimisticStatus(_:)`.
    @discardableResult
    func optimisticallySetMode(_ mode: String) -> OptimisticStatusChange {
        statusGeneration += 1 // discard an older in-flight STATUS response
        let change = OptimisticStatusChange(previousStatus: status, generation: statusGeneration)
        status.mode = mode
        return change
    }

    /// Restores a failed mode change if it is still the newest one.
    func restoreOptimisticStatus(_ change: OptimisticStatusChange) {
        guard change.generation == statusGeneration else { return }
        statusGeneration += 1
        status = change.previousStatus
    }

    /// Schedules an authoritative STATUS read without waiting for the next
    /// regular interval. It is safe to call while a poll is suspended: the
    /// request is coalesced and runs once that cycle completes, never in
    /// parallel with it.
    func refreshAfterMutation() {
        statusGeneration += 1
        pollRequested = true
        Task { [weak self] in
            await self?.poll()
        }
    }

    /// Serializes all polling entry points (the loop and mutation refreshes).
    /// A slow network/control-socket request therefore cannot create an
    /// overlapping second cycle, which was a common source of needless work
    /// once views and the menu bar requested refreshes at the same time.
    private func poll() async {
        guard !pollInFlight else {
            pollRequested = true
            return
        }

        pollInFlight = true
        defer { pollInFlight = false }

        repeat {
            pollRequested = false
            await tick()
        } while pollRequested && !Task.isCancelled
    }

    /// One serialized poll cycle. STATUS is always attempted so the menu bar
    /// remains current. The rest is due-time based and best-effort; see
    /// `LivePollingCadence` for the active/idle/offline schedule.
    private func tick() async {
        let statusRequestGeneration = statusGeneration
        do {
            let fetchedStatus = try await backend.status()
            // Do not allow a STATUS reply that began before an optimistic
            // mode action to make the segmented control jump backwards.
            guard statusRequestGeneration == statusGeneration else { return }
            status = fetchedStatus
            daemonRunning = true
        } catch {
            // No live daemon/tunnel (or it vanished between polls): report
            // absent, keep the last snapshot.
            daemonRunning = false
            haveLastTraffic = false
            lastTrafficSampleAt = nil
            return
        }

        let now = Date()
        let cadence = LivePollingCadence.forDaemon(running: daemonRunning, mode: status.mode)

        #if !APPSTORE
        // Console output over the control socket — independent of Clash.
        // Poll it less frequently and append a whole batch in one published
        // mutation, instead of making the UI process it every status tick.
        if now >= nextConsolePoll {
            nextConsolePoll = now.addingTimeInterval(cadence.console)
            if let lines = try? await control.consolePoll(since: lastConsoleID), !lines.isEmpty {
                for line in lines where line.id > lastConsoleID {
                    lastConsoleID = line.id
                }
                console.append(contentsOf: lines)
                if console.count > maxConsoleLines {
                    console.removeFirst(console.count - maxConsoleLines)
                }
            }
        }
        #endif

        // Cumulative byte counters -> per-second rate, guarded against counter
        // resets (e.g. a live core reload restarts the counters). Rate uses
        // actual elapsed time, not a hard-coded polling period.
        if now >= nextTrafficPoll {
            nextTrafficPoll = now.addingTimeInterval(cadence.traffic)
            if let traffic = try? await backend.traffic() {
                if haveLastTraffic {
                    let secs = max(now.timeIntervalSince(lastTrafficSampleAt ?? now), 1)
                    let upRate = traffic.up >= lastUp ? Double(traffic.up - lastUp) / secs : 0
                    let downRate = traffic.down >= lastDown ? Double(traffic.down - lastDown) / secs : 0
                    trafficSamples.append((up: upRate, down: downRate))
                    if trafficSamples.count > maxTrafficSamples {
                        trafficSamples.removeFirst(trafficSamples.count - maxTrafficSamples)
                    }
                }
                lastUp = traffic.up
                lastDown = traffic.down
                lastTrafficSampleAt = now
                haveLastTraffic = true
            }
        }

        if now >= nextConnectionsPoll {
            nextConnectionsPoll = now.addingTimeInterval(cadence.connections)
            if let conns = try? await backend.connections() {
                connections = conns.connRows
                totalUp = conns.uploadTotal
                totalDown = conns.downloadTotal
            }
        }

        if now >= nextLatencyPoll {
            nextLatencyPoll = now.addingTimeInterval(cadence.latency)
            if let lat = try? await backend.latency() {
                latency = lat
            }
        }
    }
}
