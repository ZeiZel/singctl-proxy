// DaemonLauncher.swift
//
// Starts the singctl LaunchDaemon on demand when `InstanceDiscovery` finds no
// live daemon — previously the app had NO way to do this at all, every call
// just failed with a socket error (`ControlClientError.noDaemon`), and the
// only fix was a terminal (`sudo launchctl bootstrap …`, or `make install`
// again). This restores that path from the UI.
//
// The app runs unprivileged, and `/Library/LaunchDaemons/…` is root-owned, so
// bootstrapping it needs an authorization prompt: `NSAppleScript`'s
// `do shell script … with administrator privileges` is the standard way to
// get macOS's native Touch ID/password prompt for a one-off privileged shell
// command, without embedding a full privileged-helper-tool XPC service for
// this one action. `-128` is AppleScript's own "user canceled" error code,
// surfaced here when the user dismisses that prompt — that's a normal
// outcome (see `DaemonLauncherError.cancelled`), not a failure to report.
//
// Developer-ID build only: the App Store SKU has no root LaunchDaemon — it
// drives a sandboxed `NEPacketTunnelProvider` (see TunnelBackend.swift)
// instead, so "start the daemon" isn't a concept there.

#if !APPSTORE
import Foundation
import AppKit

/// Errors from `DaemonLauncher.start()`.
enum DaemonLauncherError: Error, LocalizedError, Equatable {
    /// The LaunchDaemon plist isn't on disk at all — starting it can never
    /// work, so callers should point at the installer instead of offering a
    /// "Start daemon" action (see `DaemonLauncher.isInstalled()`).
    case notInstalled
    /// The user dismissed the administrator-privileges prompt. Treat this as
    /// a normal, silent outcome — NOT an error banner (explicit task
    /// requirement: cancelling a password prompt isn't a bug).
    case cancelled
    case scriptFailed(String)
    /// launchd accepted the request but no live daemon advertised itself in
    /// time — it is most likely exiting on startup and being restarted by
    /// KeepAlive. Carries the most useful line from the daemon's log.
    case didNotStart(String?)

    var errorDescription: String? {
        switch self {
        case .notInstalled:
            return "The singctl daemon isn't installed. Run the installer, then try again."
        case .cancelled:
            return "Cancelled."
        case .scriptFailed(let message):
            return message
        case .didNotStart(let logLine?):
            return "The daemon didn't start: \(logLine)"
        case .didNotStart(nil):
            return "The daemon didn't start — see \(DaemonLauncher.logPath)."
        }
    }
}

/// Bootstraps `/Library/LaunchDaemons/com.singctl.proxy.plist` under an
/// administrator-privileges prompt. See the file-level doc comment for why
/// `NSAppleScript` rather than a privileged helper tool.
enum DaemonLauncher {
    /// Mirrors packaging/macos/com.singctl.proxy.plist's installed location
    /// (scripts/install-macos.sh / `make install` writes the real, per-user-
    /// filled-in plist here).
    static let plistPath = "/Library/LaunchDaemons/com.singctl.proxy.plist"
    static let label = "com.singctl.proxy"
    /// StandardOutPath/StandardErrorPath of the installed plist.
    static let logPath = "/var/log/singctl.log"
    /// How long to wait for the started daemon to advertise instance.json.
    static let startupTimeout: Duration = .seconds(8)

    /// Whether the LaunchDaemon plist exists at all — distinguishes "not
    /// installed" (point at the installer, offer nothing) from "installed
    /// but not currently running" (offer to start it). Cheap: a single
    /// `stat`, safe to call from a view body on every render.
    static func isInstalled() -> Bool {
        FileManager.default.fileExists(atPath: plistPath)
    }

    /// Runs the privileged bootstrap. Throws `.notInstalled` immediately
    /// (without ever prompting) when the plist is missing; throws
    /// `.cancelled` when the user dismisses the prompt; throws
    /// `.scriptFailed` for any other AppleScript/shell failure, and
    /// `.didNotStart` when launchd ran the job but no live daemon showed up
    /// within `startupTimeout`. The blocking
    /// `NSAppleScript` call runs off the main thread on a detached task —
    /// same pattern as `ControlClient.blockingRoundTrip` — since it
    /// synchronously waits on the user answering the system prompt.
    static func start() async throws {
        guard isInstalled() else { throw DaemonLauncherError.notInstalled }
        try await Task.detached(priority: .userInitiated) {
            try Self.runPrivileged()
        }.value
        let deadline = ContinuousClock.now + startupTimeout
        while ContinuousClock.now < deadline {
            if InstanceDiscovery.liveInstance() != nil { return }
            try await Task.sleep(for: .milliseconds(250))
        }
        throw DaemonLauncherError.didNotStart(lastLogError())
    }

    /// The last `error:` line of the daemon log (or its last non-empty line),
    /// so a crash-looping daemon explains itself in the UI instead of the
    /// button silently doing nothing. Only the tail of the file is read.
    static func lastLogError() -> String? {
        guard let handle = FileHandle(forReadingAtPath: logPath) else { return nil }
        defer { try? handle.close() }
        let size = (try? handle.seekToEnd()) ?? 0
        try? handle.seek(toOffset: size > 8192 ? size - 8192 : 0)
        guard let data = try? handle.readToEnd(),
              let text = String(data: data, encoding: .utf8) else { return nil }
        let lines = text.split(whereSeparator: \.isNewline)
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        let line = lines.last { $0.hasPrefix("error:") } ?? lines.last
        return line.map { $0.hasPrefix("error: ") ? String($0.dropFirst(7)) : $0 }
    }

    /// `kickstart -k` (re)starts a job launchd already has loaded — the usual
    /// case: the plist is bootstrapped at install and KeepAlive keeps it
    /// loaded even while the process crash-loops or after it was killed. The
    /// old `bootstrap || load -w` pair failed on a loaded job and then
    /// "succeeded" with an "already loaded" warning, so the button did
    /// nothing. `bootstrap` remains the fallback for a job that isn't loaded
    /// (kickstart fails with "no such process"), and `enable` first clears a
    /// disabled override left by `unload -w`. One privileged shell, one prompt.
    private static func runPrivileged() throws {
        let shellCommand = "launchctl enable system/\(label); "
            + "launchctl kickstart -k system/\(label) || launchctl bootstrap system \(plistPath)"
        let escaped = shellCommand.replacingOccurrences(of: "\"", with: "\\\"")
        let source = "do shell script \"\(escaped)\" with administrator privileges"
        guard let appleScript = NSAppleScript(source: source) else {
            throw DaemonLauncherError.scriptFailed("Couldn't build the privileged start command.")
        }
        var errorInfo: NSDictionary?
        appleScript.executeAndReturnError(&errorInfo)
        guard let errorInfo else { return }
        let code = (errorInfo[NSAppleScript.errorNumber] as? Int) ?? 0
        if code == -128 {
            throw DaemonLauncherError.cancelled
        }
        let message = (errorInfo[NSAppleScript.errorMessage] as? String)
            ?? "Failed to start the daemon (AppleScript error \(code))."
        throw DaemonLauncherError.scriptFailed(message)
    }
}
#endif
