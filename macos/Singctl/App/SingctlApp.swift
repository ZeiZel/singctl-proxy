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
                .onAppear { store.start() }
                .onDisappear { store.stop() }
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

    func applicationDidFinishLaunching(_ notification: Notification) {
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
        Task { try? await backend.setMode(mode) }
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
    @State private var selection: Section? = .dashboard

    var body: some View {
        NavigationSplitView {
            SidebarView(selection: $selection)
        } detail: {
            DetailView(section: selection ?? .dashboard)
        }
        .containerBackground(for: .window) {
            Rectangle().fill(.regularMaterial).overlay(Color.black.opacity(0.22))
        }
    }
}

/// The navigation rail: "singctl" wordmark + live `StatusDot` up top, the
/// sections in the middle, and a "daemon connected/offline" footer bound to
/// `LiveStore.daemonRunning`.
private struct SidebarView: View {
    @EnvironmentObject private var store: LiveStore
    @Binding var selection: Section?

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
            Text(store.daemonRunning ? "daemon connected" : "daemon offline")
                .font(.appSecondary)
                .foregroundStyle(store.daemonRunning ? Color.sOk : Color.sTextFaint)
                .padding(Spacing.sm)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
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
            #if !APPSTORE
            case .connections: ConnectionsScreen()
            case .apps:        AppsScreen()
            #endif
            case .keys:        KeysScreen()
            #if !APPSTORE
            case .console:     ConsoleScreen()
            case .license:     LicenseScreen()
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
        isApplyingMode = true
        modeError = nil
        Task {
            defer { isApplyingMode = false }
            do {
                try await backend.setMode(mode)
            } catch {
                modeError = error.localizedDescription
            }
        }
    }
}
