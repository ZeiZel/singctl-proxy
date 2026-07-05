// AppsScreen.swift
//
// The per-app proxy picker: launch an arbitrary command routed through the
// proxy, launch an installed .app bundle into the persistent proxied-apps
// store, and manage routing/enablement of both currently-running and
// previously-proxied applications. Follows DashboardScreen's environment
// wiring — `LiveStore` for shared live state, `ControlClient` (actor) for
// verb calls.

import SwiftUI

struct AppsScreen: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.controlClient) private var control

    // MARK: - "Launch app through proxy" (arbitrary command)

    @State private var launchCommand = ""
    @State private var isLaunchingCommand = false
    @State private var launchCommandError: String?

    // MARK: - "Launch an app in proxy" (installed .app bundles)

    @State private var installedApps: [InstalledApp] = []
    @State private var installedSearch = ""
    @State private var isLoadingInstalled = false
    @State private var installedError: String?
    @State private var launchingBundleID: String?

    // MARK: - Applications / Currently proxied (shared 4s refresh)

    @State private var runningApps: [Application] = []
    @State private var routedBundleIDs: Set<String> = []
    @State private var proxiedApps: [ProxiedApp] = []
    @State private var listsError: String?
    @State private var mutatingBundleIDs: Set<String> = []

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "Apps", subtitle: "Route individual applications or commands through the proxy")

                launchCommandCard
                installedAppsCard

                if let listsError {
                    Text(listsError).font(.caption).foregroundStyle(Color.sDanger)
                }

                HStack(alignment: .top, spacing: Spacing.md) {
                    applicationsCard
                    proxiedCard
                }
            }
            .padding(Spacing.lg)
        }
        .task { await loadInstalledApps() }
        .task { await refreshLoop() }
    }

    // MARK: - Launch app through proxy

    private var launchCommandCard: some View {
        Card(title: "Launch app through proxy") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                Text("Runs a command with its arguments routed through the proxy, e.g. \"/usr/bin/curl -v https://example.com\".")
                    .font(.caption)
                    .foregroundStyle(Color.sTextDim)
                HStack(spacing: Spacing.sm) {
                    TextField("Command and arguments…", text: $launchCommand)
                        .textFieldStyle(.plain)
                        .padding(.horizontal, Spacing.sm)
                        .padding(.vertical, 8)
                        .background(Color.sBgSoft)
                        .clipShape(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                        .disabled(isLaunchingCommand)
                        .onSubmit { submitLaunchCommand() }
                    AppButton(
                        "Launch", icon: "play.fill",
                        isLoading: isLaunchingCommand,
                        disabled: launchCommandArgv.isEmpty
                    ) {
                        submitLaunchCommand()
                    }
                }
                if let launchCommandError {
                    Text(launchCommandError).font(.caption).foregroundStyle(Color.sDanger)
                }
            }
        }
    }

    private var launchCommandArgv: [String] {
        launchCommand.split(whereSeparator: \.isWhitespace).map(String.init)
    }

    private func submitLaunchCommand() {
        let argv = launchCommandArgv
        guard !argv.isEmpty, !isLaunchingCommand else { return }
        launchCommandError = nil
        isLaunchingCommand = true
        Task {
            defer { isLaunchingCommand = false }
            do {
                _ = try await control.procLaunch(argv: argv)
                launchCommand = ""
            } catch {
                launchCommandError = error.localizedDescription
            }
        }
    }

    // MARK: - Launch an app in proxy (installed apps)

    private var filteredInstalledApps: [InstalledApp] {
        guard !installedSearch.isEmpty else { return installedApps }
        return installedApps.filter {
            $0.name.localizedCaseInsensitiveContains(installedSearch)
                || $0.bundleID.localizedCaseInsensitiveContains(installedSearch)
        }
    }

    private var installedAppsCard: some View {
        Card(title: "Launch an app in proxy") {
            AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: isLoadingInstalled) {
                Task { await loadInstalledApps() }
            }
        } content: {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let installedError {
                    Text(installedError).font(.caption).foregroundStyle(Color.sDanger)
                }
                DataTable(
                    columns: ["App", "Bundle ID", ""],
                    rows: filteredInstalledApps,
                    searchText: $installedSearch,
                    searchPlaceholder: "Filter installed apps…"
                ) { app in
                    HStack {
                        Text(app.name).frame(width: 220, alignment: .leading)
                        Text(app.bundleID)
                            .foregroundStyle(Color.sTextDim)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        AppButton(
                            "Launch in proxy", icon: "bolt.fill",
                            isLoading: launchingBundleID == app.bundleID,
                            disabled: launchingBundleID != nil
                        ) {
                            launchInstalled(app)
                        }
                    }
                }
                .frame(height: 280)
            }
        }
    }

    private func loadInstalledApps() async {
        isLoadingInstalled = true
        installedError = nil
        installedApps = await InstalledApps.scan()
        isLoadingInstalled = false
    }

    private func launchInstalled(_ app: InstalledApp) {
        guard launchingBundleID == nil else { return }
        installedError = nil
        launchingBundleID = app.bundleID
        Task {
            defer { launchingBundleID = nil }
            do {
                _ = try await control.appLaunch(path: app.path)
                await refreshLists()
            } catch {
                installedError = error.localizedDescription
            }
        }
    }

    // MARK: - Applications (running, routable)

    private var applicationsCard: some View {
        Card(title: "Applications") {
            DataTable(
                columns: ["App", "Bundle ID", "PIDs", ""],
                rows: runningApps,
                searchText: nil
            ) { app in
                let isMutating = mutatingBundleIDs.contains(app.bundleID)
                let isRouted = routedBundleIDs.contains(app.bundleID)
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(app.name)
                        Badge(text: app.running ? "Running" : "Not running", tone: app.running ? .ok : .dim)
                    }
                    .frame(width: 170, alignment: .leading)
                    Text(app.bundleID)
                        .foregroundStyle(Color.sTextDim)
                        .frame(width: 200, alignment: .leading)
                    Text(app.pids.map(String.init).joined(separator: ", "))
                        .foregroundStyle(Color.sTextDim)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    if isRouted {
                        AppButton(
                            "Unroute", kind: .danger, icon: "minus.circle",
                            isLoading: isMutating, disabled: isMutating
                        ) {
                            unroute(app.bundleID)
                        }
                    } else {
                        AppButton(
                            "Route", icon: "arrow.triangle.branch",
                            isLoading: isMutating, disabled: isMutating
                        ) {
                            route(app.bundleID)
                        }
                    }
                }
            }
            .frame(height: 320)
        }
        .frame(maxWidth: .infinity)
    }

    private func route(_ bundleID: String) {
        guard !mutatingBundleIDs.contains(bundleID) else { return }
        listsError = nil
        mutatingBundleIDs.insert(bundleID)
        Task {
            defer { mutatingBundleIDs.remove(bundleID) }
            do {
                try await control.appRoute(bundleID)
                await refreshLists()
            } catch {
                listsError = error.localizedDescription
            }
        }
    }

    private func unroute(_ bundleID: String) {
        guard !mutatingBundleIDs.contains(bundleID) else { return }
        listsError = nil
        mutatingBundleIDs.insert(bundleID)
        Task {
            defer { mutatingBundleIDs.remove(bundleID) }
            do {
                try await control.appUnroute(bundleID)
                await refreshLists()
            } catch {
                listsError = error.localizedDescription
            }
        }
    }

    // MARK: - Currently proxied (persistent store)

    private var proxiedCard: some View {
        Card(title: "Currently proxied") {
            Badge(text: "\(proxiedApps.count)", tone: .accent)
        } content: {
            DataTable(
                columns: ["App", "Bundle ID", ""],
                rows: proxiedApps,
                searchText: nil
            ) { app in
                let isMutating = mutatingBundleIDs.contains(app.bundleID)
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(app.name)
                        Badge(text: app.running ? "Running" : "Not running", tone: app.running ? .ok : .dim)
                    }
                    .frame(width: 170, alignment: .leading)
                    Text(app.bundleID)
                        .foregroundStyle(Color.sTextDim)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    PillToggle("Enabled", isOn: enabledBinding(for: app))
                        .disabled(isMutating)
                        .frame(width: 130)
                    AppButton(
                        "Remove", kind: .danger, icon: "trash",
                        isLoading: isMutating, disabled: isMutating
                    ) {
                        removeProxied(app.bundleID)
                    }
                }
            }
            .frame(height: 320)
        }
        .frame(maxWidth: .infinity)
    }

    private func enabledBinding(for app: ProxiedApp) -> Binding<Bool> {
        Binding(
            get: { app.enabled },
            set: { setEnabled(app.bundleID, $0) }
        )
    }

    private func setEnabled(_ bundleID: String, _ enabled: Bool) {
        guard !mutatingBundleIDs.contains(bundleID) else { return }
        listsError = nil
        mutatingBundleIDs.insert(bundleID)
        if let idx = proxiedApps.firstIndex(where: { $0.bundleID == bundleID }) {
            proxiedApps[idx].enabled = enabled // optimistic, corrected by the next refresh
        }
        Task {
            defer { mutatingBundleIDs.remove(bundleID) }
            do {
                try await control.appSetEnabled(bundleID: bundleID, enabled: enabled)
                await refreshLists()
            } catch {
                listsError = error.localizedDescription
                await refreshLists()
            }
        }
    }

    private func removeProxied(_ bundleID: String) {
        guard !mutatingBundleIDs.contains(bundleID) else { return }
        listsError = nil
        mutatingBundleIDs.insert(bundleID)
        Task {
            defer { mutatingBundleIDs.remove(bundleID) }
            do {
                try await control.appRemove(bundleID)
                await refreshLists()
            } catch {
                listsError = error.localizedDescription
            }
        }
    }

    // MARK: - Shared 4s refresh

    private func refreshLoop() async {
        await refreshLists()
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            if Task.isCancelled { break }
            await refreshLists()
        }
    }

    private func refreshLists() async {
        async let appsResult: [Application]? = try? control.appList()
        async let routedResult: [String]? = try? control.appListRouted()
        async let proxiedResult: [ProxiedApp]? = try? control.appListProxied()
        let (apps, routed, proxied) = await (appsResult, routedResult, proxiedResult)
        if let apps { runningApps = apps }
        if let routed { routedBundleIDs = Set(routed) }
        if let proxied { proxiedApps = proxied }
    }
}
