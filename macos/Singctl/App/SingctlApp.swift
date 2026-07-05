// SingctlApp.swift — @main entry point.
//
// One regular windowed app (NavigationSplitView: sidebar + detail) that ALSO
// exposes a MenuBarExtra for quick glance/mode-toggle access. Shared state:
//   - `LiveStore` — the 2s poll loop, injected as `.environmentObject` so any
//     screen can `@EnvironmentObject var store: LiveStore`.
//   - `Backend` — status/mode/keys/settings/traffic/latency/connections,
//     injected via the custom `\.backend` environment key (see
//     AppModel.swift) so any kept screen can `@Environment(\.backend) var
//     backend`. One instance is constructed here and shared with `LiveStore`
//     so both see the same underlying `DaemonBackend`/`TunnelBackend`.
//
// The sidebar's `Section` cases (Navigation.swift) already differ per build
// flag; `DetailView` below mirrors that with matching `#if APPSTORE` guards
// so the switch stays exhaustive in both builds.

import SwiftUI
import AppKit

@main
struct SingctlApp: App {
    @StateObject private var store: LiveStore
    private let backend: Backend

    init() {
        let backend = SingctlApp.makeBackend()
        self.backend = backend
        _store = StateObject(wrappedValue: LiveStore(backend: backend))
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

        MenuBarExtra("singctl", systemImage: "shield") {
            MenuBarContentView()
                .environmentObject(store)
                .environment(\.backend, backend)
                .appTheme()
        }
        .menuBarExtraStyle(.window)
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
                    Label(section.title, systemImage: section.symbol).tag(section)
                }
            } header: {
                HStack(spacing: Spacing.sm) {
                    StatusDot(on: store.daemonRunning)
                    Text("singctl")
                        .font(.headline)
                        .foregroundStyle(Color.sText)
                }
                .padding(.bottom, Spacing.xs)
                .textCase(nil)
            }
        }
        .listStyle(.sidebar)
        .navigationSplitViewColumnWidth(min: 180, ideal: 210, max: 260)
        .safeAreaInset(edge: .bottom) {
            Text(store.daemonRunning ? "daemon connected" : "daemon offline")
                .font(.caption)
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
        .background(Color.sBg)
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
                    .font(.subheadline.weight(.medium))
                    .foregroundStyle(Color.sText)
            }

            VStack(alignment: .leading, spacing: Spacing.xs) {
                Text("MODE").font(.caption2).foregroundStyle(Color.sTextDim)
                SegmentedControl(options: modeOptions, selection: modeBinding, disabled: isApplyingMode)
                if let modeError {
                    Text(modeError).font(.caption).foregroundStyle(Color.sDanger)
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
        .background(Color.sPanel)
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
