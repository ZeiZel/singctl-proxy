// LogsScreen.swift
//
// A live, merged tail of every log source that explains "what is this app
// doing right now": the sing-box core log (the proxy's own routing/traffic
// decisions), the root LaunchDaemon's operational log, this app's own os_log
// lines, AND — folded in here, see the F5 note below — the stdout/stderr of
// apps launched *through* the proxy (`LiveStore.console`).
//
// F5 (Console vs Logs): a standalone Console screen used to show that same
// per-app stdout/stderr, but it was empty for most users forever (nothing to
// show until an app is launched from the Apps screen) and the split from
// Logs wasn't self-evident. Both screens are fundamentally the same job —
// "tell me what's happening under the hood" — split only by an
// implementation detail (poll vs. file tail). Folding Console in as a fourth
// source here removes a confusing, often-empty sidebar entry entirely; see
// SingctlApp.swift/Navigation.swift for the removed `.console` case.
//
// Performance (F4): never loads a whole file. `LogsTailReader` tails only
// new bytes since the last poll (a bounded initial window on first read),
// and "Load older" pages further backward on demand. Rendering uses a
// virtualised `List` (AppKit-backed row recycling), not a growing
// `LazyVStack`, so a multi-thousand-line buffer doesn't stall the window.
//
// Chrome (F4): exactly ONE surface — the outer `Card` — with the list's own
// background hidden, instead of a second (or third) nested rounded panel.
//
// Beware `.fixedSize(vertical:true)` on growable `Text` outside a
// `ScrollView`/`List` — it has previously pinned this app's window to a huge
// minimum size. Nothing here uses it.

import SwiftUI
import AppKit
import OSLog

struct LogsScreen: View {
    @EnvironmentObject private var store: LiveStore
    @StateObject private var model = LogsModel()

    private static let allSourcesLabel = "All sources"
    private static let allLevelsLabel = "All levels"

    @State private var selectedSource = LogsScreen.allSourcesLabel
    // Seeded from Settings → Observability's "Default log level filter" (a
    // view preference, not a daemon setting — see AppPreferences.swift).
    @State private var selectedLevel = LogsScreen.resolveInitialLevel()
    @State private var searchText = ""
    @State private var followTail = true

    private static func resolveInitialLevel() -> String {
        guard let raw = AppPreferences.shared.defaultLogLevelFilter,
              let level = LogLevel(rawValue: raw) else {
            return allLevelsLabel
        }
        return level.label
    }

    private var sourceOptions: [String] {
        [Self.allSourcesLabel] + LogLine.Source.allCases.map(\.label)
    }

    private var levelOptions: [String] {
        [Self.allLevelsLabel] + LogLevel.allCases.map(\.label)
    }

    private var filteredLines: [LogLine] {
        model.lines.filter { line in
            (selectedSource == Self.allSourcesLabel || line.source.label == selectedSource)
                && (selectedLevel == Self.allLevelsLabel || line.level.label == selectedLevel)
                && (searchText.isEmpty || line.text.localizedCaseInsensitiveContains(searchText))
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            header
            Card {
                if filteredLines.isEmpty {
                    EmptyState(
                        text: model.lines.isEmpty
                            ? "No logs available yet. The sing-box, daemon, app, and console output will appear here as soon as they're readable."
                            : "No lines match the current filter/search.",
                        symbol: "doc.text.magnifyingglass"
                    )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    terminal
                }
            }
            .frame(maxHeight: .infinity)
        }
        .padding(Spacing.lg)
        .task { model.start(store: store) }
        .onDisappear { model.stop() }
    }

    // MARK: - Header

    private var header: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            SectionHeader(
                title: "Logs",
                subtitle: "The daemon + sing-box's own output, plus stdout/stderr from apps you launch through the proxy (source: console)."
            ) {
                HStack(spacing: Spacing.sm) {
                    Toggle("Follow tail", isOn: $followTail)
                        .toggleStyle(.switch)
                        .controlSize(.small)
                    AppButton(
                        "Load older", kind: .ghost, icon: "arrow.up.doc",
                        isLoading: model.isLoadingOlder, disabled: !model.canLoadOlder
                    ) {
                        model.loadOlder()
                    }
                    AppButton("Copy", kind: .ghost, icon: "doc.on.doc") {
                        copyVisibleLines()
                    }
                    AppButton("Reveal in Finder", kind: .ghost, icon: "folder") {
                        model.revealInFinder()
                    }
                }
            }

            HStack(spacing: Spacing.sm) {
                Picker("Source", selection: $selectedSource) {
                    ForEach(sourceOptions, id: \.self) { source in
                        Text(source).tag(source)
                    }
                }
                .pickerStyle(.menu)
                .frame(maxWidth: 160)

                Picker("Level", selection: $selectedLevel) {
                    ForEach(levelOptions, id: \.self) { level in
                        Text(level).tag(level)
                    }
                }
                .pickerStyle(.menu)
                .frame(maxWidth: 140)

                TextField("Search logs", text: $searchText)
                    .textFieldStyle(.roundedBorder)
                    .frame(maxWidth: 280)

                if !searchText.isEmpty {
                    AppButton("Clear", kind: .ghost) { searchText = "" }
                }
            }
        }
    }

    private func copyVisibleLines() {
        let text = filteredLines.map { "[\($0.source.label)] \($0.text)" }.joined(separator: "\n")
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(text, forType: .string)
    }

    // MARK: - Terminal

    /// A native `List` rather than a `ScrollView` + `LazyVStack`: `List` is
    /// backed by a recycling `NSTableView` on macOS, so it stays responsive
    /// with several thousand rows instead of diffing/laying out the whole
    /// buffer. `.scrollContentBackground(.hidden)` + clear row backgrounds
    /// keep it visually part of the single enclosing `Card` — no second
    /// surface underneath it.
    private var terminal: some View {
        ScrollViewReader { proxy in
            List {
                ForEach(filteredLines) { line in
                    logLineView(line)
                        .id(line.id)
                        .listRowBackground(Color.clear)
                        .listRowSeparator(.hidden)
                        .listRowInsets(EdgeInsets(top: 1, leading: Spacing.sm, bottom: 1, trailing: Spacing.sm))
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .onScrollGeometryChange(for: Bool.self) { geometry in
                geometry.contentOffset.y >= geometry.contentSize.height - geometry.containerSize.height - 40
            } action: { _, isNearBottom in
                // Only ever adopt a user's scroll position; never fights a
                // programmatic scrollTo triggered by followTail == true.
                followTail = isNearBottom
            }
            .onAppear { scrollToBottom(proxy) }
            .onChange(of: filteredLines.last?.id) { _, _ in
                guard followTail else { return }
                scrollToBottom(proxy)
            }
            .onChange(of: selectedSource) { _, _ in scrollToBottom(proxy) }
            .onChange(of: selectedLevel) { _, _ in scrollToBottom(proxy) }
        }
    }

    private func scrollToBottom(_ proxy: ScrollViewProxy) {
        guard let lastID = filteredLines.last?.id else { return }
        proxy.scrollTo(lastID, anchor: .bottom)
    }

    private func logLineView(_ line: LogLine) -> some View {
        HStack(alignment: .top, spacing: Spacing.xs) {
            Text("[\(line.source.label)]")
                .foregroundStyle(line.source.tint)
            Text(line.text)
                .foregroundStyle(Color.sText)
                .textSelection(.enabled)
        }
        .font(.system(size: 16, design: .monospaced))
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

// MARK: - Model

/// One merged log line, tagged with the source it came from and a
/// best-effort severity level.
struct LogLine: Identifiable, Equatable {
    enum Source: CaseIterable, Sendable {
        case singbox, daemon, app, console

        var label: String {
            switch self {
            case .singbox: return "singbox"
            case .daemon: return "daemon"
            case .app: return "app"
            case .console: return "console"
            }
        }

        var tint: Color {
            switch self {
            case .singbox: return .sAccent
            case .daemon: return .sOk
            case .app: return .sTextDim
            case .console: return .sWarn
            }
        }
    }

    /// A stable, forever-unique identity: forward-tailed/live lines get an
    /// ever-increasing id, lines fetched by "Load older" get an
    /// ever-decreasing one — so prepending older history can never collide
    /// with the live tail, regardless of arrival order.
    let id: Int
    let source: Source
    let level: LogLevel
    let text: String
    let date: Date
}

/// Best-effort severity, sniffed from the line's own text — these are raw
/// tailed lines, not structured records, so this is a heuristic, not a
/// parser. Lines with no recognizable tag (most `console`/`app` output) fall
/// back to `.other` rather than being mis-tagged.
enum LogLevel: String, CaseIterable {
    case error, warn, info, debug, trace, other

    var label: String {
        switch self {
        case .error: return "Error"
        case .warn: return "Warn"
        case .info: return "Info"
        case .debug: return "Debug"
        case .trace: return "Trace"
        case .other: return "Other"
        }
    }

    static func detect(in text: String) -> LogLevel {
        let head = text.prefix(24).uppercased()
        if head.contains("FATAL") || head.contains("ERRO") { return .error }
        if head.contains("WARN") { return .warn }
        if head.contains("DEBU") { return .debug }
        if head.contains("TRAC") { return .trace }
        if head.contains("INFO") { return .info }
        return .other
    }
}

/// Owns the SwiftUI-facing rolling log buffer. All blocking file/OSLogStore
/// work is delegated to `LogsTailReader`; console lines are read straight off
/// the already-shared `LiveStore` (it polls CONSOLE-POLL itself — see
/// LiveStore.swift) so no second control-socket connection is opened just
/// for this screen. Everything is merged into ONE timeline here, with a
/// single assigned-once `id`/`date` per line, so filtering/sorting never
/// re-derives either.
@MainActor
final class LogsModel: ObservableObject {
    @Published private(set) var lines: [LogLine] = []
    @Published private(set) var isLoadingOlder = false
    /// Whether at least one file-backed source might still have earlier
    /// content to page in. Starts true (optimistic); a `loadOlder()` call
    /// that finds nothing left on every source flips it off so the button
    /// doesn't invite a pointless tap forever.
    @Published private(set) var canLoadOlder = true

    private let pollInterval: TimeInterval = 2
    /// "The last N lines" per F4 — a count cap, not a time window, so a
    /// `loadOlder()` batch (necessarily "old" by definition) is never evicted
    /// by the very next regular poll the way a time-based cutoff would.
    /// Seeded from Settings → Observability's "Log retention" preference.
    private var maxLines: Int
    private let hardLineCeiling = 20_000

    private var timerTask: Task<Void, Never>?
    private var nextID = 0
    private var previousID = -1
    private var lastConsoleID = 0
    private let reader = LogsTailReader()
    private weak var store: LiveStore?

    init() {
        maxLines = max(500, AppPreferences.shared.logRetentionLines)
    }

    func start(store: LiveStore) {
        self.store = store
        guard timerTask == nil else { return }
        timerTask = Task { [weak self] in
            guard let self else { return }
            while !Task.isCancelled {
                await self.tick()
                guard !Task.isCancelled else { break }
                try? await Task.sleep(nanoseconds: UInt64(self.pollInterval * 1_000_000_000))
            }
        }
    }

    func stop() {
        timerTask?.cancel()
        timerTask = nil
    }

    /// Pages one more chunk of history backward for every file-backed source
    /// (singbox/daemon logs). Console/app-log sources have no file to page
    /// through, so they're untouched here — a tap just prepends whatever the
    /// file tail can still resolve.
    func loadOlder() {
        guard !isLoadingOlder else { return }
        isLoadingOlder = true
        Task { [weak self] in
            guard let self else { return }
            defer { self.isLoadingOlder = false }
            let (older, moreAvailable) = await self.reader.loadOlder()
            self.canLoadOlder = moreAvailable
            guard !older.isEmpty else { return }
            self.prepend(older)
        }
    }

    /// Opens Finder at whichever log directory is available, preferring the
    /// user-readable singctl config dir (where singbox.log lives) since
    /// /var/log needs no special handling but the config dir is the one a
    /// user is more likely to want to browse (key files, instance.json, etc).
    func revealInFinder() {
        Task { [reader] in
            let path = await reader.preferredLogPath()
            guard !Task.isCancelled else { return }
            NSWorkspace.shared.selectFile(path, inFileViewerRootedAtPath: "")
        }
    }

    private func tick() async {
        var batch = await reader.poll()
        if let store {
            for line in store.console where line.id > lastConsoleID {
                lastConsoleID = line.id
                let app = line.app.isEmpty ? "?" : line.app
                batch.append(LogsIncomingLine(
                    source: .console,
                    text: "[\(app):\(line.pid)|\(line.stream)] \(line.text)",
                    date: Date()
                ))
            }
        }
        guard !Task.isCancelled else { return }
        publish(batch)
    }

    private func publish(_ batch: [LogsIncomingLine]) {
        guard !batch.isEmpty else { return }
        let fresh = batch.sorted { $0.date < $1.date }.map { entry -> LogLine in
            defer { nextID += 1 }
            return LogLine(
                id: nextID, source: entry.source, level: .detect(in: entry.text),
                text: entry.text, date: entry.date
            )
        }
        lines.append(contentsOf: fresh)
        if lines.count > maxLines {
            lines.removeFirst(lines.count - maxLines)
        }
    }

    private func prepend(_ batch: [LogsIncomingLine]) {
        guard !batch.isEmpty else { return }
        let sorted = batch.sorted { $0.date < $1.date }.map { entry -> LogLine in
            defer { previousID -= 1 }
            return LogLine(
                id: previousID, source: entry.source, level: .detect(in: entry.text),
                text: entry.text, date: entry.date
            )
        }
        lines.insert(contentsOf: sorted, at: 0)
        // Raise the cap by exactly what was just loaded so the next regular
        // poll's tail-trim can't immediately evict the history the user just
        // asked for — capped so an enthusiastic clicker still bounds memory.
        maxLines = min(hardLineCeiling, maxLines + sorted.count)
    }
}

/// A small, Sendable representation that can cross from the I/O actor back to
/// the main actor without passing OSLogStore or FileHandle objects around.
private struct LogsIncomingLine: Sendable {
    let source: LogLine.Source
    let text: String
    let date: Date
}

/// Serializes file and OSLogStore access away from the main actor. It keeps
/// byte-level pending fragments so a poll split inside a UTF-8 sequence or a
/// line never produces corrupted/duplicate text.
private actor LogsTailReader {
    private struct FileCursor {
        var offset: UInt64 = 0
        /// The earliest byte offset already surfaced for this file — set
        /// once on first read (or on a rotation/gap restart), then only ever
        /// moved further back by `loadOlder()`. Lets "Load older" resume
        /// exactly where the initial tail window stopped.
        var earliestOffset: UInt64 = 0
        var fileNumber: UInt64?
        var pendingBytes = Data()
        var discardUntilNewline = false
    }

    private static let daemonLogPath = "/var/log/singctl.log"
    private let tailWindowBytes: UInt64 = 256 * 1024
    private let olderChunkBytes: UInt64 = 256 * 1024
    private let maxPartialLineBytes = 64 * 1024
    private let maxLinesPerSourcePerPoll = 500
    /// OSLogStore construction/enumeration is disproportionately expensive.
    /// File tails remain responsive, while app logs are refreshed at a human
    /// useful cadence instead of rescanning the store on every file poll.
    private let osLogScanInterval: TimeInterval = 15
    private let initialOSLogWindow: TimeInterval = 5 * 60

    private var fileCursors: [String: FileCursor] = [:]
    private var lastOSLogDate: Date?
    private var nextOSLogScan = Date.distantPast

    func preferredLogPath() -> String {
        InstanceDiscovery.readInstance()?.logPath ?? Self.daemonLogPath
    }

    func poll() -> [LogsIncomingLine] {
        let now = Date()
        var fresh: [LogsIncomingLine] = []

        if let singboxPath = InstanceDiscovery.readInstance()?.logPath {
            fresh.append(contentsOf: tailNewLines(path: singboxPath, source: .singbox, now: now))
        }
        fresh.append(contentsOf: tailNewLines(path: Self.daemonLogPath, source: .daemon, now: now))

        if now >= nextOSLogScan {
            nextOSLogScan = now.addingTimeInterval(osLogScanInterval)
            fresh.append(contentsOf: appLogLines(now: now))
        }
        return fresh
    }

    /// Pages one more `olderChunkBytes` chunk backward from whatever each
    /// file-backed source's earliest-seen offset already is. Returns the
    /// decoded lines (oldest bytes first) plus whether any source still has
    /// unread bytes before its new earliest offset.
    func loadOlder() -> (lines: [LogsIncomingLine], moreAvailable: Bool) {
        var result: [LogsIncomingLine] = []
        var more = false
        let now = Date()

        if let singboxPath = InstanceDiscovery.readInstance()?.logPath {
            let (lines, hasMore) = loadOlderChunk(path: singboxPath, source: .singbox, now: now)
            result.append(contentsOf: lines)
            more = more || hasMore
        }
        let (daemonLines, daemonHasMore) = loadOlderChunk(path: Self.daemonLogPath, source: .daemon, now: now)
        result.append(contentsOf: daemonLines)
        more = more || daemonHasMore

        return (result, more)
    }

    /// Reads newly-appended bytes from `path` since the last poll (or the
    /// last `tailWindowBytes` on first read), splitting only complete byte
    /// lines. Missing/unreadable files are skipped silently — never throw or
    /// crash the poll loop.
    private func tailNewLines(
        path: String,
        source: LogLine.Source,
        now: Date
    ) -> [LogsIncomingLine] {
        guard let handle = FileHandle(forReadingAtPath: path) else { return [] }
        defer { try? handle.close() }

        guard let endOffset = try? handle.seekToEnd() else { return [] }
        let fileNumber = fileNumber(for: path)
        var cursor = fileCursors[path] ?? FileCursor()
        let didRotate = cursor.fileNumber != nil && fileNumber != nil && cursor.fileNumber != fileNumber
        let mustRestart = fileCursors[path] == nil || didRotate || endOffset < cursor.offset
        let grewBeyondWindow = !mustRestart && endOffset - cursor.offset > tailWindowBytes

        let startOffset: UInt64
        if mustRestart || grewBeyondWindow {
            startOffset = endOffset > tailWindowBytes ? endOffset - tailWindowBytes : 0
            cursor.pendingBytes.removeAll(keepingCapacity: true)
            cursor.discardUntilNewline = startOffset > 0
            cursor.earliestOffset = startOffset
        } else {
            startOffset = cursor.offset
        }
        cursor.offset = endOffset
        cursor.fileNumber = fileNumber
        guard endOffset > startOffset else { return [] }

        do {
            try handle.seek(toOffset: startOffset)
        } catch {
            return []
        }
        guard var data = try? handle.read(upToCount: Int(endOffset - startOffset)) else {
            return []
        }

        if cursor.discardUntilNewline {
            guard let newline = data.firstIndex(of: 0x0A) else {
                fileCursors[path] = cursor
                return []
            }
            data = Data(data.dropFirst(newline + 1))
            cursor.discardUntilNewline = false
        }

        cursor.pendingBytes.append(data)
        let parts = cursor.pendingBytes.split(separator: 0x0A, omittingEmptySubsequences: false)
        let hasTrailingNewline = cursor.pendingBytes.last == 0x0A
        let completedParts = hasTrailingNewline ? parts[...] : parts.dropLast()
        cursor.pendingBytes = hasTrailingNewline ? Data() : Data(parts.last ?? Data())
        if cursor.pendingBytes.count > maxPartialLineBytes {
            // A never-terminated line should not be allowed to retain an
            // unbounded amount of RAM. Drop it and resume at its next newline.
            cursor.pendingBytes.removeAll(keepingCapacity: true)
            cursor.discardUntilNewline = true
        }
        fileCursors[path] = cursor

        return completedParts
            .suffix(maxLinesPerSourcePerPoll)
            .compactMap { bytes -> LogsIncomingLine? in
                guard !bytes.isEmpty else { return nil }
                return LogsIncomingLine(
                    source: source,
                    text: String(decoding: bytes, as: UTF8.self),
                    date: now
                )
            }
    }

    /// Reads the `olderChunkBytes` immediately before `path`'s current
    /// `earliestOffset`, moving that cursor back so a second tap keeps
    /// walking further into the file's history. Returns no lines (and
    /// `hasMore == false`) once a source has nothing before its cursor, or
    /// once an unterminated line makes a further step ambiguous.
    private func loadOlderChunk(
        path: String,
        source: LogLine.Source,
        now: Date
    ) -> ([LogsIncomingLine], Bool) {
        guard var cursor = fileCursors[path], cursor.earliestOffset > 0 else { return ([], false) }
        guard let handle = FileHandle(forReadingAtPath: path) else { return ([], false) }
        defer { try? handle.close() }

        let end = cursor.earliestOffset
        let newStart = end > olderChunkBytes ? end - olderChunkBytes : 0

        do {
            try handle.seek(toOffset: newStart)
        } catch {
            return ([], false)
        }
        guard var data = try? handle.read(upToCount: Int(end - newStart)) else { return ([], false) }

        if newStart > 0 {
            guard let newline = data.firstIndex(of: 0x0A) else {
                // No line boundary anywhere in this whole chunk — stop
                // paging this source rather than loop on an unbounded line.
                cursor.earliestOffset = 0
                fileCursors[path] = cursor
                return ([], false)
            }
            data = Data(data.dropFirst(newline + 1))
        }

        let parts = data.split(separator: 0x0A, omittingEmptySubsequences: false)
        let completed = data.last == 0x0A ? parts[...] : parts.dropLast()

        cursor.earliestOffset = newStart
        fileCursors[path] = cursor

        let lines = completed.compactMap { bytes -> LogsIncomingLine? in
            guard !bytes.isEmpty else { return nil }
            return LogsIncomingLine(source: source, text: String(decoding: bytes, as: UTF8.self), date: now)
        }
        return (lines, newStart > 0)
    }

    /// Best-effort read of this app's own os_log entries via `OSLogStore`,
    /// filtered to our subsystem so unrelated system noise doesn't flood the
    /// view. `OSLogStore` can throw (sandboxing/permissions) on some
    /// configurations — degrade gracefully to "no app logs" rather than
    /// crash, since the file-based sources are the must-have.
    private func appLogLines(now: Date) -> [LogsIncomingLine] {
        guard let store = try? OSLogStore(scope: .currentProcessIdentifier) else { return [] }
        let since = lastOSLogDate ?? now.addingTimeInterval(-initialOSLogWindow)
        let position = store.position(date: since)

        guard let entries = try? store.getEntries(at: position) else { return [] }

        var newestDate = since
        var result: [LogsIncomingLine] = []
        for entry in entries {
            guard let logEntry = entry as? OSLogEntryLog, logEntry.date > since else { continue }
            newestDate = max(newestDate, logEntry.date)
            guard logEntry.subsystem == "com.singctl.proxy" else { continue }
            result.append(LogsIncomingLine(
                source: .app,
                text: "[\(logEntry.category)] \(logEntry.composedMessage)",
                date: logEntry.date
            ))
        }
        // Advance even when no matching entry exists, otherwise an empty
        // app-log stream would repeatedly rescan the same five-minute range.
        lastOSLogDate = max(newestDate, now)
        return Array(result.suffix(maxLinesPerSourcePerPoll))
    }

    private func fileNumber(for path: String) -> UInt64? {
        guard let attributes = try? FileManager.default.attributesOfItem(atPath: path),
              let number = attributes[.systemFileNumber] as? NSNumber else {
            return nil
        }
        return number.uint64Value
    }
}
