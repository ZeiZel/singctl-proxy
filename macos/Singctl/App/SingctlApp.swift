// SingctlApp.swift — @main entry point.
//
// One regular windowed app (NavigationSplitView: sidebar + detail) that ALSO
// drives a menu-bar status item for quick glance/mode-toggle access. Shared
// state:
//   - `LiveStore` — the 2s poll loop, injected as `.environmentObject` so any
//     screen can `@EnvironmentObject var store: LiveStore`.
//   - `Backend` — status/mode/keys/settings/traffic/latency/connections,
//     injected via the custom `\.backend` environment key (see
//     AppModel.swift) so any kept screen can `@Environment(\.backend) var
//     backend`. One instance is constructed here and shared with `LiveStore`
//     so both see the same underlying `DaemonBackend`/`TunnelBackend`.
//
// The menu-bar presence is a plain AppKit `NSStatusItem` owned by
// `AppDelegate` rather than a `MenuBarExtra` scene, so a right-click can show
// a native `NSMenu` (mode switch + About/Quit) while a left-click keeps the
// exact same SwiftUI `MenuBarContentView` popover as before. `AppDelegate` is
// handed the SAME `store`/`backend` instances constructed below — see
// `init()`.
//
// The sidebar's `Section` cases (Navigation.swift) already differ per build
// flag; `DetailView` below mirrors that with matching `#if APPSTORE` guards
// so the switch stays exhaustive in both builds.

import SwiftUI
import AppKit
import Combine
import os.log

@main
struct SingctlApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate
    @StateObject private var store: LiveStore
    private let backend: Backend

    init() {
        let backend = SingctlApp.makeBackend()
        self.backend = backend
        let liveStore = LiveStore(backend: backend)
        _store = StateObject(wrappedValue: liveStore)
        // Hand the AppDelegate (already constructed by the adaptor above)
        // the SAME instances rather than letting it create its own, so
        // there's exactly one poll loop and one backend for the whole app.
        appDelegate.store = liveStore
        appDelegate.backend = backend
    }

    private static func makeBackend() -> Backend {
        #if APPSTORE
        TunnelBackend()
        #else
        DaemonBackend()
        #endif
    }

    var body: some Scene {
        WindowGroup("Singctl", id: "main") {
            RootView()
                .environmentObject(store)
                .environment(\.backend, backend)
                .appTheme()
                .onAppear {
                    // NOTE: store.start()/stop() is intentionally NOT tied to
                    // this window's lifecycle anymore — the poll loop now
                    // runs for the whole app's lifetime (started from
                    // AppDelegate.applicationDidFinishLaunching below) since
                    // it also drives the always-present menu-bar item, which
                    // must keep reflecting/applying mode changes even while
                    // this window is closed. LiveStore.start() is idempotent,
                    // so this is a harmless no-op on the common path where
                    // the window is already open at launch.
                    store.start()
                    #if !APPSTORE
                    // Headless/scripted activation: `open -a Singctl --args
                    // --activate-netext` submits the system-extension
                    // activation request without touching the UI (result
                    // still lands in AppsScreen's status card + os_log).
                    if CommandLine.arguments.contains("--activate-netext") {
                        SystemExtensionActivator.shared.activate()
                    }
                    #endif
                }
        }
        .windowStyle(.hiddenTitleBar)
    }
}

// MARK: - Menu-bar status item

/// Owns the app's `NSStatusItem`. Left-click shows the existing SwiftUI
/// `MenuBarContentView` in an `NSPopover` (identical content/behavior to the
/// former `MenuBarExtra`); right-click shows a native `NSMenu` for a fast
/// mode switch plus About/Quit. `store`/`backend` are set by `SingctlApp`
/// right after this delegate is constructed (see `SingctlApp.init()`) so
/// this never creates its own instances.
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate {
    var store: LiveStore!
    var backend: Backend!

    private var statusItem: NSStatusItem?
    private var popover: NSPopover?
    private var prefsCancellable: AnyCancellable?
    private let log = OSLog(subsystem: "com.singctl.proxy", category: "tray")

    func applicationDidFinishLaunching(_ notification: Notification) {
        CrashReporter.shared.start()

        // Observed (not read once): Settings' General group can flip "show
        // menu-bar item" live, and `$showMenuBarItem` emits its current
        // value immediately on subscribe, so this one subscription both
        // creates the item at launch and tears it down/rebuilds it on every
        // later change — no separate one-time creation needed.
        prefsCancellable = AppPreferences.shared.$showMenuBarItem
            .sink { [weak self] show in
                self?.setMenuBarItemVisible(show)
            }

        // Start the poll loop here (app launch), NOT on the window's
        // .onAppear — the menu-bar item is always present even when the
        // window is closed, and its mode toggle/live status read
        // `store.status`/`store.daemonRunning`, so the loop must keep running
        // for the app's lifetime rather than stopping when the window
        // disappears. LiveStore.start() is idempotent so RootView's own
        // .onAppear calling it too (common case: window open at launch) is
        // harmless.
        store.start()
    }

    /// Creates or tears down the `NSStatusItem` to match the "show menu-bar
    /// item" preference. Idempotent in both directions so the initial
    /// `sink` emission and a later manual toggle behave identically.
    private func setMenuBarItemVisible(_ visible: Bool) {
        guard visible else {
            if let statusItem { NSStatusBar.system.removeStatusItem(statusItem) }
            statusItem = nil
            return
        }
        guard statusItem == nil else { return }
        let item = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
        if let button = item.button {
            button.image = NSImage(systemSymbolName: "shield", accessibilityDescription: "singctl")
            button.target = self
            button.action = #selector(statusItemClicked(_:))
            button.sendAction(on: [.leftMouseUp, .rightMouseUp])
        }
        statusItem = item
    }

    @objc private func statusItemClicked(_ sender: NSStatusBarButton) {
        if NSApp.currentEvent?.type == .rightMouseUp {
            showMenu(on: sender)
        } else {
            togglePopover(relativeTo: sender)
        }
    }

    /// Builds the right-click `NSMenu` on demand and shows it immediately by
    /// assigning it to the status item, replaying the click, then clearing
    /// it — so left-clicks keep triggering `statusItemClicked` instead of
    /// always opening a menu.
    private func showMenu(on button: NSStatusBarButton) {
        let currentMode = store.status.mode.isEmpty ? "off" : store.status.mode

        let menu = NSMenu()
        for (mode, title) in [("off", "Off"), ("proxy", "Proxy"), ("vpn", "VPN")] {
            let item = NSMenuItem(title: title, action: #selector(selectMode(_:)), keyEquivalent: "")
            item.target = self
            item.representedObject = mode
            item.state = (mode == currentMode) ? .on : .off
            menu.addItem(item)
        }

        menu.addItem(.separator())

        let about = NSMenuItem(title: "About singctl", action: #selector(showAbout), keyEquivalent: "")
        about.target = self
        menu.addItem(about)

        let quit = NSMenuItem(title: "Quit singctl", action: #selector(quitApp), keyEquivalent: "q")
        quit.target = self
        menu.addItem(quit)

        statusItem?.menu = menu
        button.performClick(nil)
        statusItem?.menu = nil
    }

    @objc private func selectMode(_ sender: NSMenuItem) {
        guard let mode = sender.representedObject as? String else { return }
        if mode == "vpn", !AppPreferences.shared.confirmVPNSwitch() { return }
        os_log("tray menu: applying mode %{public}@", log: log, type: .info, mode)
        let optimisticChange = store.optimisticallySetMode(mode)
        Task { @MainActor in
            do {
                try await backend.setMode(mode)
                store.refreshAfterMutation()
            } catch {
                store.restoreOptimisticStatus(optimisticChange)
                store.refreshAfterMutation()
                os_log(
                    "tray menu: setMode(%{public}@) failed: %{public}@",
                    log: log, type: .error, mode, error.localizedDescription
                )
            }
        }
    }

    @objc private func showAbout() {
        NSApp.activate(ignoringOtherApps: true)
        NSApp.orderFrontStandardAboutPanel(nil)
    }

    @objc private func quitApp() {
        NSApp.terminate(nil)
    }

    private func togglePopover(relativeTo button: NSStatusBarButton) {
        if let popover, popover.isShown {
            popover.performClose(nil)
            return
        }
        let popover = self.popover ?? makePopover()
        self.popover = popover
        popover.show(relativeTo: button.bounds, of: button, preferredEdge: .minY)
    }

    private func makePopover() -> NSPopover {
        let popover = NSPopover()
        popover.behavior = .transient
        popover.contentViewController = NSHostingController(
            rootView: MenuBarContentView()
                .environmentObject(store!)
                .environment(\.backend, backend)
                .appTheme()
        )
        return popover
    }
}

// MARK: - Root window content

/// The window's root: sidebar (all `Section`s for this build) + detail pane
/// for the selected one.
private struct RootView: View {
    @EnvironmentObject private var store: LiveStore
    @StateObject private var navigation = NavigationModel()

    var body: some View {
        NavigationSplitView {
            SidebarView(selection: $navigation.selection)
        } detail: {
            DetailView(section: navigation.selection ?? .dashboard)
        }
        .containerBackground(for: .window) {
            Rectangle().fill(.regularMaterial).overlay(Color.black.opacity(0.22))
        }
        .environmentObject(navigation)
    }
}

/// The navigation rail: "singctl" wordmark + live `StatusDot` up top, the
/// sections in the middle, and a "daemon connected/offline" footer bound to
/// `LiveStore.daemonRunning`.
private struct SidebarView: View {
    @EnvironmentObject private var store: LiveStore
    @Binding var selection: Section?

    #if !APPSTORE
    // "Start daemon" state — see startDaemonControls below. Dev-ID only: the
    // App Store build has no root LaunchDaemon to start (it drives a
    // sandboxed NEPacketTunnelProvider instead).
    @State private var isStartingDaemon = false
    @State private var startDaemonMessage: String?
    #endif

    var body: some View {
        List(selection: $selection) {
            SwiftUI.Section {
                ForEach(Section.allCases) { section in
                    // Explicit HStack instead of `Label` so the sidebar list
                    // style can't substitute its own (larger) row typography;
                    // text is pinned to the 16px `appBody` token.
                    HStack(spacing: Spacing.sm) {
                        Image(systemName: section.symbol)
                            .font(.system(size: 15, weight: .medium))
                            .foregroundStyle(Color.sTextDim)
                            .frame(width: 20)
                        Text(section.title)
                            .font(.appBody)
                    }
                    .tag(section)
                }
            } header: {
                HStack(spacing: Spacing.sm) {
                    StatusDot(on: store.daemonRunning)
                    Text("singctl")
                        .font(.appHeadline)
                        .foregroundStyle(Color.sText)
                }
                .padding(.bottom, Spacing.xs)
                .textCase(nil)
            }
        }
        .listStyle(.sidebar)
        // Ignore the system "Sidebar icon size" setting (Large would blow the
        // rows up past the 16px type scale).
        .environment(\.sidebarRowSize, .medium)
        .navigationSplitViewColumnWidth(min: 180, ideal: 210, max: 260)
        .safeAreaInset(edge: .bottom) {
            VStack(alignment: .leading, spacing: Spacing.xs) {
                Text(store.daemonRunning ? "daemon connected" : "daemon offline")
                    .font(.appSecondary)
                    .foregroundStyle(store.daemonRunning ? Color.sOk : Color.sTextFaint)
                #if !APPSTORE
                if !store.daemonRunning {
                    startDaemonControls
                }
                #endif
                Text("singctl v\(Bundle.main.appVersion)")
                    .font(.appCaption)
                    .foregroundStyle(Color.sTextFaint)
            }
            .padding(Spacing.sm)
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    #if !APPSTORE
    /// Shown only while `!store.daemonRunning`: either a "Start daemon"
    /// button (plist installed — offer to bootstrap it, with an
    /// administrator-privileges prompt) or a plain "not installed" message
    /// pointing at the installer when it isn't (a start attempt with no
    /// plist cannot work, so don't offer one — see DaemonLauncher.swift).
    @ViewBuilder
    private var startDaemonControls: some View {
        if DaemonLauncher.isInstalled() {
            AppButton("Start daemon", kind: .ghost, icon: "bolt.fill", isLoading: isStartingDaemon) {
                startDaemon()
            }
            .help("macOS will ask for an administrator password to start the daemon.")
        } else {
            Text("Daemon not installed — run the installer (see docs/macos.md).")
                .font(.appCaption)
                .foregroundStyle(Color.sDanger)
        }
        if let startDaemonMessage {
            Text(startDaemonMessage)
                .font(.appCaption)
                .foregroundStyle(Color.sDanger)
        }
    }

    /// Bootstraps the LaunchDaemon under an administrator-privileges prompt,
    /// then re-runs discovery (`LiveStore.refreshAfterMutation()` calls
    /// `Backend.status()`, which re-resolves `InstanceDiscovery` from disk on
    /// every call) so the UI recovers without a relaunch. The user cancelling
    /// the prompt is a normal outcome, not an error — see
    /// `DaemonLauncherError.cancelled`'s doc comment.
    private func startDaemon() {
        isStartingDaemon = true
        startDaemonMessage = nil
        Task {
            defer { isStartingDaemon = false }
            do {
                try await DaemonLauncher.start()
                store.refreshAfterMutation()
            } catch DaemonLauncherError.cancelled {
                // User dismissed the password prompt — say nothing.
            } catch {
                startDaemonMessage = error.localizedDescription
            }
        }
    }
    #endif
}

/// Routes to the screen for the selected `Section`. The `#if APPSTORE`
/// guards here mirror `Section`'s own conditional cases exactly (see
/// Navigation.swift) so this switch stays exhaustive in both builds.
private struct DetailView: View {
    let section: Section

    var body: some View {
        Group {
            switch section {
            case .dashboard:   DashboardScreen()
            case .proxies:     ProxiesScreen()
            case .sysProxy:    SysProxyScreen()
            #if !APPSTORE
            case .connections: ConnectionsScreen()
            case .apps:        AppsScreen()
            #endif
            case .keys:        KeysScreen()
            #if !APPSTORE
            case .logs:        LogsScreen()
            #endif
            case .settings:    SettingsScreen()
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
    }
}

// MARK: - Menu-bar popover

/// Compact `MenuBarExtra` content: running status, a mode toggle, and quick
/// actions ("Open window" / "Stop daemon").
private struct MenuBarContentView: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.backend) private var backend
    @Environment(\.openWindow) private var openWindow

    @State private var isApplyingMode = false
    @State private var modeError: String?

    #if !APPSTORE
    @State private var isStartingDaemon = false
    @State private var startDaemonMessage: String?
    #endif

    private let log = OSLog(subsystem: "com.singctl.proxy", category: "tray")

    private let modeOptions: [SegmentedOption<String>] = [
        SegmentedOption("off", "Off"),
        SegmentedOption("proxy", "Proxy"),
        SegmentedOption("vpn", "VPN"),
    ]

    private var modeBinding: Binding<String> {
        Binding(
            get: { store.status.mode.isEmpty ? "off" : store.status.mode },
            set: applyMode
        )
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            HStack(spacing: Spacing.sm) {
                StatusDot(on: store.daemonRunning)
                Text(store.daemonRunning ? "Daemon running" : "Daemon offline")
                    .font(.appSecondary.weight(.medium))
                    .foregroundStyle(Color.sText)
            }

            #if !APPSTORE
            if !store.daemonRunning {
                startDaemonSection
            }
            #endif

            VStack(alignment: .leading, spacing: Spacing.xs) {
                Text("MODE").font(.appCaption).foregroundStyle(Color.sTextDim)
                SegmentedControl(options: modeOptions, selection: modeBinding, disabled: isApplyingMode)
                if let modeError {
                    Text(modeError).font(.appCaption).foregroundStyle(Color.sDanger)
                }
            }

            Divider().overlay(Color.sBorder)

            AppButton("Open window", kind: .ghost, icon: "macwindow") {
                NSApp.activate(ignoringOtherApps: true)
                openWindow(id: "main")
            }
            AppButton("Stop daemon", kind: .danger, icon: "power") {
                Task { try? await backend.stop() }
            }
        }
        .padding(Spacing.md)
        .frame(width: 240)
        .background(.regularMaterial)
    }

    private func applyMode(_ mode: String) {
        if mode == "vpn", !AppPreferences.shared.confirmVPNSwitch() { return }
        isApplyingMode = true
        modeError = nil
        os_log("tray popover: applying mode %{public}@", log: log, type: .info, mode)
        let optimisticChange = store.optimisticallySetMode(mode)
        Task { @MainActor in
            defer { isApplyingMode = false }
            do {
                try await backend.setMode(mode)
                store.refreshAfterMutation()
            } catch {
                store.restoreOptimisticStatus(optimisticChange)
                store.refreshAfterMutation()
                os_log(
                    "tray popover: setMode(%{public}@) failed: %{public}@",
                    log: log, type: .error, mode, error.localizedDescription
                )
                modeError = error.localizedDescription
            }
        }
    }

    #if !APPSTORE
    /// Mirrors `SidebarView.startDaemonControls` for the menu-bar popover —
    /// same "not installed" vs. "offer to start" split, same administrator-
    /// privileges disclosure. See DaemonLauncher.swift.
    @ViewBuilder
    private var startDaemonSection: some View {
        VStack(alignment: .leading, spacing: Spacing.xs) {
            if DaemonLauncher.isInstalled() {
                AppButton("Start daemon", kind: .ghost, icon: "bolt.fill", isLoading: isStartingDaemon) {
                    startDaemon()
                }
                .help("macOS will ask for an administrator password to start the daemon.")
            } else {
                Text("Daemon not installed — run the installer.")
                    .font(.appCaption)
                    .foregroundStyle(Color.sDanger)
            }
            if let startDaemonMessage {
                Text(startDaemonMessage)
                    .font(.appCaption)
                    .foregroundStyle(Color.sDanger)
            }
        }
    }

    private func startDaemon() {
        isStartingDaemon = true
        startDaemonMessage = nil
        os_log("tray popover: starting daemon", log: log, type: .info)
        Task {
            defer { isStartingDaemon = false }
            do {
                try await DaemonLauncher.start()
                store.refreshAfterMutation()
            } catch DaemonLauncherError.cancelled {
                os_log("tray popover: start-daemon prompt cancelled", log: log, type: .info)
            } catch {
                os_log(
                    "tray popover: start-daemon failed: %{public}@",
                    log: log, type: .error, error.localizedDescription
                )
                startDaemonMessage = error.localizedDescription
            }
        }
    }
    #endif
}
