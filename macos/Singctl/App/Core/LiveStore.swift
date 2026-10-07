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
            return Self(status: 3, traffic: 30, connections: 30, latency: 60)
            #else
            return Self(status: 3, traffic: 30, connections: 30, latency: 60, console: 15)
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

    private(set) var status: DaemonStatus = .empty
    private(set) var daemonRunning: Bool = false
    /// Increments whenever a new daemon session is observed. Console line IDs
    /// are local to a daemon process and can restart at zero after install.
    private(set) var consoleGeneration = 0

    /// Rolling window of per-second (up, down) byte rates, most-recent last.
    private(set) var trafficSamples: [(up: Double, down: Double)] = []
    private(set) var totalUp: Int64 = 0
    private(set) var totalDown: Int64 = 0

    private(set) var connections: [ConnRow] = []
    private(set) var latency: Latency = .empty

    /// Rolling window of the last maxConsoleLines captured stdout/stderr lines.
    /// Only ever populated in the Developer-ID build (see tick() below).
    private(set) var console: [ConsoleLine] = []

    // MARK: - Tuning

    /// Active-mode traffic is sampled every five seconds, making this a
    /// five-minute rolling chart rather than the previous two-minute window.
    private let maxTrafficSamples = 60
    /// LogsModel owns the visible 500-line presentation window. LiveStore
    /// keeps a larger source ring so a delayed consumer can drain lines that
    /// arrived during an occlusion pause without racing the next poll.
    private let maxConsoleLines = 20_000

    // MARK: - Internals

    private let backend: Backend
    #if !APPSTORE
    private let consoleProvider: ((Int) async throws -> [ConsoleLine])?
    #endif
    private var loopTask: Task<Void, Never>?
    private var pollInFlight = false
    private var pollRequested = false
    private var statusGeneration = 0
    #if !APPSTORE
    private var daemonIdentity: String?
    #endif
    /// Status remains live while the main window is hidden; screen-sized
    /// telemetry pauses until AppKit reports the window visible again.
    private(set) var windowVisible = true
    #if !APPSTORE
    private var hiddenConsole: [ConsoleLine] = []
    private let hiddenConsoleCeiling = 20_000
    #endif

    private var nextTrafficPoll = Date.distantPast
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
    init(backend: Backend? = nil, consoleProvider: ((Int) async throws -> [ConsoleLine])? = nil) {
        #if !APPSTORE
        self.consoleProvider = consoleProvider
        #endif
        #if APPSTORE
        self.backend = backend ?? TunnelBackend()
        #else
        self.backend = backend ?? DaemonBackend()
        #endif
    }

    /// Builds an already-populated store for the source-controlled screenshot
    /// harness. This does not start the polling loop: callers provide every
    /// displayed value explicitly, so no daemon, socket, or network access is
    /// involved while a capture is rendered.
    static func previewSnapshot(
        backend: Backend,
        status: DaemonStatus,
        trafficSamples: [(up: Double, down: Double)],
        totalUp: Int64,
        totalDown: Int64,
        connections: [ConnRow],
        latency: Latency
    ) -> LiveStore {
        let store = LiveStore(backend: backend)
        store.status = status
        store.daemonRunning = true
        store.trafficSamples = trafficSamples
        store.totalUp = totalUp
        store.totalDown = totalDown
        store.connections = connections
        store.latency = latency
        return store
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

    /// Reset the traffic baseline after an occlusion pause so the next rate
    /// sample never spans the time the window was hidden.
    func setWindowVisible(_ visible: Bool) {
        guard windowVisible != visible else { return }
        windowVisible = visible
        guard visible else { return }
        #if !APPSTORE
        if !hiddenConsole.isEmpty {
            console.append(contentsOf: hiddenConsole)
            hiddenConsole.removeAll(keepingCapacity: true)
        }
        #endif
        trafficSamples.removeAll(keepingCapacity: true)
        haveLastTraffic = false
        lastTrafficSampleAt = nil
        nextTrafficPoll = .distantPast
        nextLatencyPoll = .distantPast
        objectWillChange.send()
        #if SCREENSHOT_HARNESS
        // The harness drives explicit poll cycles so it can assert ordering
        // around suspended requests without a concurrent refresh task.
        #else
        refreshAfterMutation()
        #endif
    }

    /// Updates the visible mode immediately. Call this before `setMode`; on
    /// success call `refreshAfterMutation()`, and on failure pass the token to
    /// `restoreOptimisticStatus(_:)`.
    @discardableResult
    func optimisticallySetMode(_ mode: String) -> OptimisticStatusChange {
        statusGeneration += 1 // discard an older in-flight STATUS response
        let change = OptimisticStatusChange(previousStatus: status, generation: statusGeneration)
        status.mode = mode
        objectWillChange.send()
        return change
    }

    /// Restores a failed mode change if it is still the newest one.
    func restoreOptimisticStatus(_ change: OptimisticStatusChange) {
        guard change.generation == statusGeneration else { return }
        statusGeneration += 1
        status = change.previousStatus
        objectWillChange.send()
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
        let oldStatus = status
        let oldDaemonRunning = daemonRunning
        let oldSamples = trafficSamples
        let oldTotalUp = totalUp
        let oldTotalDown = totalDown
        let oldLatency = latency
        let oldConsole = console
        var publishedBeforeLatency = false
        defer {
            let samplesChanged = oldSamples.count != trafficSamples.count
                || zip(oldSamples, trafficSamples).contains { $0.up != $1.up || $0.down != $1.down }
            let nonLatencyChanged = oldStatus != status || oldDaemonRunning != daemonRunning || samplesChanged
                || oldTotalUp != totalUp || oldTotalDown != totalDown
                || oldConsole != console
            if nonLatencyChanged && !publishedBeforeLatency {
                objectWillChange.send()
            } else if latency != oldLatency {
                // One notification per serialized poll cycle, even when the
                // latency request finishes after the main telemetry snapshot.
                objectWillChange.send()
            }
        }
        let statusRequestGeneration = statusGeneration
        do {
            let fetchedStatus = try await backend.status()
            // Do not allow a STATUS reply that began before an optimistic
            // mode action to make the segmented control jump backwards.
            guard statusRequestGeneration == statusGeneration else { return }
            #if !APPSTORE
            let fetchedIdentity = fetchedStatus.pid > 0
                ? "\(fetchedStatus.pid):\(fetchedStatus.startedAt)" : nil
            let sessionChanged = daemonIdentity != nil && fetchedIdentity != daemonIdentity
            daemonIdentity = fetchedIdentity
            if sessionChanged {
                consoleGeneration += 1
                lastConsoleID = 0
                console.removeAll(keepingCapacity: true)
                hiddenConsole.removeAll(keepingCapacity: true)
                trafficSamples.removeAll(keepingCapacity: true)
                haveLastTraffic = false
                lastTrafficSampleAt = nil
                latency = .empty
                nextTrafficPoll = .distantPast
                nextLatencyPoll = .distantPast
                nextConsolePoll = .distantPast
            }
            #endif
            if status != fetchedStatus { status = fetchedStatus }
            if !daemonRunning {
                daemonRunning = true
                #if APPSTORE
                consoleGeneration += 1
                #endif
            }
        } catch {
            // No live daemon/tunnel (or it vanished between polls): report
            // absent, keep the last snapshot.
            if daemonRunning { daemonRunning = false }
            #if !APPSTORE
            // Keep the last identity through a transient timeout. A new
            // session is recognized only after a successful STATUS advertises
            // a different PID/start timestamp.
            #endif
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
            let lines: [ConsoleLine]?
            if let consoleProvider {
                lines = try? await consoleProvider(lastConsoleID)
            } else {
                lines = try? await control.consolePoll(since: lastConsoleID)
            }
            if let lines, !lines.isEmpty {
                let freshLines = lines.filter { $0.id > lastConsoleID }
                if !freshLines.isEmpty {
                    lastConsoleID = freshLines.map(\.id).max() ?? lastConsoleID
                    if windowVisible {
                        console.append(contentsOf: freshLines)
                        if console.count > maxConsoleLines {
                            console.removeFirst(console.count - maxConsoleLines)
                        }
                    } else {
                        hiddenConsole.append(contentsOf: freshLines)
                        if hiddenConsole.count > hiddenConsoleCeiling {
                            hiddenConsole.removeFirst(hiddenConsole.count - hiddenConsoleCeiling)
                        }
                    }
                }
            }
        }
        #endif

        // Keep console collection alive for LogsScreen while the window is
        // hidden, but stop the expensive screen telemetry.
        guard windowVisible else { return }

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
                if totalUp != traffic.up { totalUp = traffic.up }
                if totalDown != traffic.down { totalDown = traffic.down }
                lastTrafficSampleAt = now
                haveLastTraffic = true
            }
        }

        // Make status, traffic, and connection changes available immediately;
        // a slow singleton latency probe must not hold up the dashboard.
        let samplesChanged = oldSamples.count != trafficSamples.count
            || zip(oldSamples, trafficSamples).contains { $0.up != $1.up || $0.down != $1.down }
        if oldStatus != status || oldDaemonRunning != daemonRunning || samplesChanged
            || oldTotalUp != totalUp || oldTotalDown != totalDown
            || oldConsole != console {
            objectWillChange.send()
            publishedBeforeLatency = true
        }

        if now >= nextLatencyPoll {
            nextLatencyPoll = now.addingTimeInterval(cadence.latency)
            if let lat = try? await backend.latency() {
                if latency != lat { latency = lat }
            }
        }
    }

    #if SCREENSHOT_HARNESS
    /// Drives one real production poll cycle for the in-memory harness. It is
    /// excluded from shipping targets and never discovers a daemon itself.
    func pollOnceForHarness() async { await poll() }

    func resetDueTimesForHarness() {
        nextTrafficPoll = .distantPast
        nextLatencyPoll = .distantPast
        #if !APPSTORE
        nextConsolePoll = .distantPast
        #endif
    }

    #endif
}
