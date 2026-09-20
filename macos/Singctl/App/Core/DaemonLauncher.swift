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

    var errorDescription: String? {
        switch self {
        case .notInstalled:
            return "The singctl daemon isn't installed. Run the installer, then try again."
        case .cancelled:
            return "Cancelled."
        case .scriptFailed(let message):
            return message
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
    /// `.scriptFailed` for any other AppleScript/shell failure. The blocking
    /// `NSAppleScript` call runs off the main thread on a detached task —
    /// same pattern as `ControlClient.blockingRoundTrip` — since it
    /// synchronously waits on the user answering the system prompt.
    static func start() async throws {
        guard isInstalled() else { throw DaemonLauncherError.notInstalled }
        try await Task.detached(priority: .userInitiated) {
            try Self.runPrivileged()
        }.value
    }

    /// `launchctl bootstrap system …` is the modern (10.11+) way to load a
    /// LaunchDaemon; `launchctl load -w …` is the legacy fallback the task
    /// explicitly calls for, tried only when bootstrap itself fails (e.g.
    /// already bootstrapped, or an unusual launchd that rejects the
    /// subcommand). Both run inside the SAME privileged shell invocation
    /// (`with administrator privileges` covers the whole string), so this is
    /// one authorization prompt, not two.
    private static func runPrivileged() throws {
        let shellCommand = "launchctl bootstrap system \(plistPath) || launchctl load -w \(plistPath)"
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
