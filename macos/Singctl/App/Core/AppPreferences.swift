// AppPreferences.swift
//
// Local, per-machine UI preferences — deliberately NOT part of the daemon's
// `Settings` model (SETTINGS-GET/SET, see Models.swift): launch-at-login,
// menu-bar visibility, and "confirm before switching to VPN" are choices
// about how THIS app behaves on THIS Mac, not something the daemon needs to
// know or a config a headless `singctl` install would ever read. Persisted
// via `UserDefaults`, read directly by whichever view/AppDelegate code needs
// them (`SettingsScreen`'s General group edits them; `AppDelegate` reads
// `showMenuBarItem`; every mode-switch call site reads `confirmBeforeVPN`).
//
// One shared instance (`.shared`) so every reader observes the same state —
// there is exactly one of these for the app's lifetime, same pattern as
// `LiveStore`/`Backend` (see AppModel.swift).

import SwiftUI
import AppKit
import ServiceManagement

@MainActor
final class AppPreferences: ObservableObject {
    static let shared = AppPreferences()

    private enum Keys {
        static let launchAtLogin = "pref.launchAtLogin"
        static let showMenuBarItem = "pref.showMenuBarItem"
        static let confirmBeforeVPN = "pref.confirmBeforeVPN"
        static let defaultLogLevelFilter = "pref.defaultLogLevelFilter"
        static let logRetentionLines = "pref.logRetentionLines"
    }

    private let defaults: UserDefaults

    /// Whether the app is registered as a login item via `SMAppService`.
    /// Setting this calls through to `SMAppService.mainApp` immediately (see
    /// `setLaunchAtLogin`) — don't just assign this property from a UI
    /// `Toggle` binding directly, or the OS registration and the published
    /// value can drift apart.
    @Published private(set) var launchAtLogin: Bool

    /// Whether the `NSStatusItem` menu-bar presence should exist. Read once
    /// at launch and observed live by `AppDelegate` (SingctlApp.swift) so
    /// flipping it in Settings adds/removes the status item immediately,
    /// with no relaunch needed.
    @Published var showMenuBarItem: Bool {
        didSet { defaults.set(showMenuBarItem, forKey: Keys.showMenuBarItem) }
    }

    /// Whether switching the mode to "vpn" should ask for confirmation
    /// first — VPN mode reroutes ALL system traffic, unlike "proxy" mode
    /// which only affects apps explicitly pointed at it. Read by every
    /// mode-switch call site (Dashboard, the menu-bar popover/menu, and
    /// Settings' own VPN toggle in the App Store build) via
    /// `confirmVPNSwitch()`.
    @Published var confirmBeforeVPN: Bool {
        didSet { defaults.set(confirmBeforeVPN, forKey: Keys.confirmBeforeVPN) }
    }

    /// A `LogLevel.rawValue`, or `nil` for "All levels" — the level Logs
    /// pre-selects on open. There is no daemon-side "log level" setting
    /// (sing-box/daemon logs aren't filterable at the source), so this is a
    /// view preference for `LogsScreen`, not something sent to the daemon —
    /// hence living here (Settings' Observability group edits it) rather
    /// than in `Settings`/SETTINGS-SET.
    @Published var defaultLogLevelFilter: String? {
        didSet { defaults.set(defaultLogLevelFilter, forKey: Keys.defaultLogLevelFilter) }
    }

    /// How many lines `LogsModel` keeps in memory before trimming — a view
    /// preference standing in for "log retention" (there is no daemon-side
    /// log rotation/retention control exposed yet either).
    @Published var logRetentionLines: Int {
        didSet { defaults.set(logRetentionLines, forKey: Keys.logRetentionLines) }
    }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        self.launchAtLogin = SMAppService.mainApp.status == .enabled
        self.showMenuBarItem = defaults.object(forKey: Keys.showMenuBarItem) as? Bool ?? true
        self.confirmBeforeVPN = defaults.object(forKey: Keys.confirmBeforeVPN) as? Bool ?? true
        self.defaultLogLevelFilter = defaults.string(forKey: Keys.defaultLogLevelFilter)
        self.logRetentionLines = defaults.object(forKey: Keys.logRetentionLines) as? Int ?? 4_000
    }

    /// Registers/unregisters the app as a login item. Reverts the published
    /// value to the OS's actual status on failure, so the toggle can never
    /// show "on" while the registration silently failed.
    func setLaunchAtLogin(_ on: Bool) {
        do {
            if on {
                try SMAppService.mainApp.register()
            } else {
                try SMAppService.mainApp.unregister()
            }
            launchAtLogin = on
        } catch {
            launchAtLogin = SMAppService.mainApp.status == .enabled
        }
    }

    /// Presents a confirmation alert before switching to VPN mode, unless
    /// the user has turned that off. Returns `true` when it's safe to
    /// proceed (confirmed, or confirmation disabled) — every mode-switch
    /// call site must check this before calling `Backend.setMode("vpn")`.
    func confirmVPNSwitch() -> Bool {
        guard confirmBeforeVPN else { return true }
        let alert = NSAlert()
        alert.messageText = "Switch to VPN mode?"
        alert.informativeText = "VPN mode reroutes all system traffic through singctl, not just apps pointed at the proxy. You can turn this confirmation off in Settings → General."
        alert.alertStyle = .warning
        alert.addButton(withTitle: "Switch to VPN")
        alert.addButton(withTitle: "Cancel")
        return alert.runModal() == .alertFirstButtonReturn
    }
}
