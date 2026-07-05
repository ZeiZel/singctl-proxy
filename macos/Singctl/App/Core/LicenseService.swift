// LicenseService.swift
//
// Drives license display/mutation through the `singctl` CLI (not the control
// socket — license state is a CLI/profile-store concern, not a live-daemon
// one). Depends on machine-readable flags a parallel workstream is adding to
// the CLI (`--license-status --json`, `--install <token> --email <email>`,
// `--license-remove`); this file assumes that contract per the shared spec.

import Foundation

/// Decoded `singctl --license-status --json` output.
struct LicenseStatus: Codable, Equatable {
    var valid: Bool
    var dev: Bool
    var subject: String
    var expiresAt: Int64
    var daysLeft: Int
    var features: [String]
    var reason: String

    /// Decode leniently: any field the CLI omits defaults rather than failing
    /// the whole decode (mirrors the defensive decoding gui/bridge/license.go
    /// does for a nil Features slice, etc.).
    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        valid = try c.decodeIfPresent(Bool.self, forKey: .valid) ?? false
        dev = try c.decodeIfPresent(Bool.self, forKey: .dev) ?? false
        subject = try c.decodeIfPresent(String.self, forKey: .subject) ?? ""
        expiresAt = try c.decodeIfPresent(Int64.self, forKey: .expiresAt) ?? 0
        daysLeft = try c.decodeIfPresent(Int.self, forKey: .daysLeft) ?? -1
        features = try c.decodeIfPresent([String].self, forKey: .features) ?? []
        reason = try c.decodeIfPresent(String.self, forKey: .reason) ?? ""
    }

    init(
        valid: Bool, dev: Bool, subject: String, expiresAt: Int64, daysLeft: Int,
        features: [String], reason: String
    ) {
        self.valid = valid
        self.dev = dev
        self.subject = subject
        self.expiresAt = expiresAt
        self.daysLeft = daysLeft
        self.features = features
        self.reason = reason
    }

    private enum CodingKeys: String, CodingKey {
        case valid, dev, subject, expiresAt, daysLeft, features, reason
    }
}

enum LicenseServiceError: Error, LocalizedError {
    case cliNotFound(String)
    case processFailed(exitCode: Int32, stderr: String)
    case badOutput(String)

    var errorDescription: String? {
        switch self {
        case .cliNotFound(let path):
            return "singctl CLI not found at \(path)"
        case .processFailed(let code, let stderr):
            let trimmed = stderr.trimmingCharacters(in: .whitespacesAndNewlines)
            return trimmed.isEmpty ? "singctl exited with status \(code)" : trimmed
        case .badOutput(let msg):
            return "unexpected singctl output: \(msg)"
        }
    }
}

enum LicenseService {

    /// Standard install location of the daemon/CLI binary.
    static var cliPath = "/usr/local/bin/singctl"

    /// Runs `singctl --license-status --json` and decodes the result.
    static func status() async throws -> LicenseStatus {
        let result = try await run(["--license-status", "--json"])
        guard let data = result.stdout.data(using: .utf8) else {
            throw LicenseServiceError.badOutput(result.stdout)
        }
        do {
            return try JSONDecoder().decode(LicenseStatus.self, from: data)
        } catch {
            throw LicenseServiceError.badOutput("\(result.stdout) (\(error))")
        }
    }

    /// Runs `singctl --install <token> --email <email>` to activate a license.
    @discardableResult
    static func activate(token: String, email: String) async throws -> String {
        try await run(["--install", token, "--email", email]).stdout
    }

    /// Runs `singctl --license-remove` to delete the stored license.
    @discardableResult
    static func remove() async throws -> String {
        try await run(["--license-remove"]).stdout
    }

    // MARK: - Process execution

    private struct RunResult {
        let stdout: String
        let stderr: String
        let exitCode: Int32
    }

    private static func run(_ arguments: [String]) async throws -> RunResult {
        guard FileManager.default.isExecutableFile(atPath: cliPath) else {
            throw LicenseServiceError.cliNotFound(cliPath)
        }
        return try await withCheckedThrowingContinuation { continuation in
            let process = Process()
            process.executableURL = URL(fileURLWithPath: cliPath)
            process.arguments = arguments

            let stdoutPipe = Pipe()
            let stderrPipe = Pipe()
            process.standardOutput = stdoutPipe
            process.standardError = stderrPipe

            process.terminationHandler = { proc in
                let stdoutData = stdoutPipe.fileHandleForReading.readDataToEndOfFile()
                let stderrData = stderrPipe.fileHandleForReading.readDataToEndOfFile()
                let stdout = String(data: stdoutData, encoding: .utf8) ?? ""
                let stderr = String(data: stderrData, encoding: .utf8) ?? ""
                if proc.terminationStatus != 0 {
                    continuation.resume(
                        throwing: LicenseServiceError.processFailed(
                            exitCode: proc.terminationStatus, stderr: stderr
                        )
                    )
                    return
                }
                continuation.resume(
                    returning: RunResult(stdout: stdout, stderr: stderr, exitCode: proc.terminationStatus)
                )
            }

            do {
                try process.run()
            } catch {
                continuation.resume(throwing: error)
            }
        }
    }
}
