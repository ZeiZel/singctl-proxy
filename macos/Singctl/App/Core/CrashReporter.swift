// CrashReporter.swift
//
// Local, privacy-preserving crash diagnostics. MetricKit delivers diagnostics
// after the affected process has terminated, so this intentionally does not
// install an unsafe in-process signal/exception handler.

import Foundation
import MetricKit
import os.log

/// Subscribes to Apple-provided crash diagnostics and keeps a small, local
/// history for support investigations. The persisted format deliberately
/// omits call stacks, exception messages, termination reasons, configuration,
/// network keys, and application logs: any of those can contain user data.
@available(macOS 12.0, *)
final class CrashReporter: NSObject, MXMetricManagerSubscriber {
    static let shared = CrashReporter()

    private let persistence = CrashReportPersistence()
    private var hasStarted = false

    private override init() {
        super.init()
    }

    /// Must be called while the app is running. MetricKit invokes the
    /// subscriber on a background queue when diagnostics become available.
    @MainActor
    func start() {
        guard !hasStarted else { return }
        hasStarted = true
        MXMetricManager.shared.add(self)
    }

    /// MetricKit calls this after a prior app session. Persist only a compact,
    /// explicitly allow-listed summary rather than `JSONRepresentation()`,
    /// whose diagnostic details can include free-form text and call stacks.
    func didReceive(_ payloads: [MXDiagnosticPayload]) {
        let reports = payloads.flatMap { payload in
            (payload.crashDiagnostics ?? []).map(CrashReportSummary.init)
        }

        guard !reports.isEmpty else { return }
        Task {
            await persistence.save(reports)
        }
    }
}

private struct CrashReportSummary: Encodable, Sendable {
    let schemaVersion = 1
    let recordedAt: Date
    let source = "MetricKit crash diagnostic"
    let bundleIdentifier: String
    let applicationVersion: String
    let applicationBuildVersion: String
    let osVersion: String
    let platformArchitecture: String
    let exceptionType: String?
    let exceptionCode: String?
    let signal: String?

    init(_ diagnostic: MXCrashDiagnostic) {
        let metadata = diagnostic.metaData
        recordedAt = Date()
        bundleIdentifier = Bundle.main.bundleIdentifier ?? "unknown"
        applicationVersion = diagnostic.applicationVersion
        applicationBuildVersion = metadata.applicationBuildVersion
        osVersion = metadata.osVersion
        platformArchitecture = metadata.platformArchitecture
        exceptionType = diagnostic.exceptionType?.stringValue
        exceptionCode = diagnostic.exceptionCode?.stringValue
        signal = diagnostic.signal?.stringValue
    }
}

private actor CrashReportPersistence {
    private static let maximumReportCount = 10
    private let logger = Logger(subsystem: "com.singctl.proxy", category: "crash-reports")

    func save(_ reports: [CrashReportSummary]) {
        guard let directory = reportsDirectory() else { return }

        do {
            try FileManager.default.createDirectory(
                at: directory,
                withIntermediateDirectories: true
            )

            let encoder = JSONEncoder()
            encoder.dateEncodingStrategy = .iso8601
            encoder.outputFormatting = [.prettyPrinted, .sortedKeys]

            for report in reports {
                let data = try encoder.encode(report)
                let filename = "crash-\(UUID().uuidString.lowercased()).json"
                try data.write(to: directory.appendingPathComponent(filename), options: .atomic)
            }

            try pruneReports(in: directory)
        } catch {
            // Do not include a file path or diagnostic data in unified logs.
            logger.error("Unable to persist MetricKit crash diagnostic summaries.")
        }
    }

    /// The normal app uses ~/Library/Application Support/Singctl; the
    /// sandboxed App Store SKU resolves this inside its own container.
    private func reportsDirectory() -> URL? {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)
            .first?
            .appendingPathComponent("Singctl", isDirectory: true)
            .appendingPathComponent("Crash Reports", isDirectory: true)
    }

    private func pruneReports(in directory: URL) throws {
        let reportURLs = try FileManager.default.contentsOfDirectory(
            at: directory,
            includingPropertiesForKeys: [.contentModificationDateKey],
            options: [.skipsHiddenFiles]
        )
        .filter { $0.pathExtension == "json" }
        .sorted { lhs, rhs in
            let lhsDate = (try? lhs.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
            let rhsDate = (try? rhs.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
            return lhsDate > rhsDate
        }

        for url in reportURLs.dropFirst(Self.maximumReportCount) {
            try FileManager.default.removeItem(at: url)
        }
    }
}
