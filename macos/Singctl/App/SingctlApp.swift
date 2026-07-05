// SingctlApp.swift — @main entry point.
//
// One regular windowed app (NavigationSplitView: 8-section sidebar + detail)
// that ALSO exposes a MenuBarExtra for quick glance/mode-toggle access. Shared
// state:
//   - `LiveStore` — the 2s poll loop, injected as `.environmentObject` so any
//     screen can `@EnvironmentObject var store: LiveStore`.
//   - `ControlClient` — the verb-issuing actor, injected via the custom
//     `\.controlClient` environment key (see AppModel.swift) so any screen can
//     `@Environment(\.controlClient) var control`.
//
// Wave-2 screens are stubbed as a generic placeholder; only Dashboard
// (App/Screens/DashboardScreen.swift) is fully built here.

import SwiftUI
import AppKit

@main
struct SingctlApp: App {
    @StateObject private var store = LiveStore()

    var body: some Scene {
        WindowGroup("Singctl", id: "main") {
            RootView()
                .environmentObject(store)
                .appTheme()
                .onAppear { store.start() }
                .onDisappear { store.stop() }
        }
        .windowStyle(.hiddenTitleBar)

        MenuBarExtra("singctl", systemImage: "shield") {
            MenuBarContentView()
                .environmentObject(store)
                .appTheme()
        }
        .menuBarExtraStyle(.window)
    }
}

// MARK: - Root window content

/// The window's root: sidebar (all 8 `Section`s) + detail pane for the
/// selected one.
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

/// The navigation rail: "singctl" wordmark + live `StatusDot` up top, the 8
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

/// Routes to the fully-built Dashboard, or a placeholder for the 7 screens a
/// later wave implements.
private struct DetailView: View {
    let section: Section

    var body: some View {
        Group {
            switch section {
            case .dashboard:   DashboardScreen()
            case .proxies:     ProxiesScreen()
            case .connections: ConnectionsScreen()
            case .apps:        AppsScreen()
            case .keys:        KeysScreen()
            case .console:     ConsoleScreen()
            case .license:     LicenseScreen()
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
    @Environment(\.controlClient) private var control
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
                Task { try? await control.stop() }
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
                try await control.setMode(mode)
            } catch {
                modeError = error.localizedDescription
            }
        }
    }
}
