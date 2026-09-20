// AppsScreen.swift
//
// The per-app proxy picker: launch an arbitrary command routed through the
// proxy, launch an installed .app bundle into the persistent proxied-apps
// store, and manage routing/enablement of both currently-running and
// previously-proxied applications. Follows DashboardScreen's environment
// wiring — `LiveStore` for shared live state, `ControlClient` (actor) for
// verb calls.
//
// Native migration: every list is a SwiftUI `Table` (resizable/sortable
// columns, fills available space) rather than the custom `DataTable`. The
// installed-apps filter is a `.searchable` field; empty states use
// `ContentUnavailableView` (via `EmptyState`); the two management tables lay
// out side-by-side when wide and stacked when narrow (`ViewThatFits`), with no
// fixed heights.

import SwiftUI

struct AppsScreen: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.controlClient) private var control
    @ObservedObject private var activator = SystemExtensionActivator.shared

    // MARK: - "Launch app through proxy" (arbitrary command)

    @State private var launchCommand = ""
    @State private var isLaunchingCommand = false
    @State private var launchCommandError: String?

    // MARK: - "Launch an app in proxy" (installed .app bundles)

    @State private var installedApps: [InstalledApp] = []
    @State private var installedSearch = ""
    @State private var installedSort = [KeyPathComparator(\InstalledApp.name)]
    @State private var isLoadingInstalled = false
    @State private var installedError: String?
    @State private var launchingBundleID: String?

    // MARK: - Applications / Currently proxied (shared 4s refresh)

    @State private var runningApps: [Application] = []
    @State private var routedBundleIDs: Set<String> = []
    @State private var proxiedApps: [ProxiedApp] = []
    @State private var appsSort = [KeyPathComparator(\Application.name)]
    @State private var proxiedSort = [KeyPathComparator(\ProxiedApp.name)]
    @State private var listsError: String?
    @State private var mutatingBundleIDs: Set<String> = []

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Apps", subtitle: "Route individual applications or commands through the proxy")

            extensionCard
            launchCommandCard
            installedAppsCard

            if let listsError {
                Text(listsError).font(.appSecondary).foregroundStyle(Color.sDanger)
            }

            managementTables
        }
        .padding(Spacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .searchable(text: $installedSearch, prompt: "Filter installed apps")
        .task { await loadInstalledApps() }
        .task { await refreshLoop() }
    }

    // MARK: - System extension status / activation

    /// Per-app routing needs the ProxyExtension system extension activated;
    /// this card shows where that stands and lets the user (re)request
    /// activation. On MDM-managed Macs the exact failure text here is the
    /// evidence of whether the Team ID is allowlisted.
    private var extensionCard: some View {
        Card(title: "System extension") {
            statusBadge
        } content: {
            VStack(alignment: .leading, spacing: Spacing.md) {
                // No .fixedSize(vertical: true) here: the window's min-size
                // probe proposes ~zero width, and fixedSize would report the
                // one-word-per-line wrapped height (~3000pt) as a hard
                // minimum — locking the window tall and un-shrinkable.
                Text("Per-app routing runs through the ProxyExtension network system extension. Install it once, then approve it in System Settings → General → Login Items & Extensions.")
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextDim)

                HStack(spacing: Spacing.sm) {
                    AppButton(
                        activator.status == .requesting ? "Requesting…" : "Install extension",
                        kind: .primary,
                        icon: "puzzlepiece.extension"
                    ) {
                        activator.activate()
                    }
                    .disabled(activator.status == .requesting)

                    if activator.status == .needsUserApproval {
                        AppButton("Open System Settings", kind: .ghost, icon: "gearshape") {
                            NSWorkspace.shared.open(
                                URL(string: "x-apple.systempreferences:com.apple.LoginItems-Settings.extension")!
                            )
                        }
                    }
                }

                if case .failed(let message) = activator.status {
                    Text(message)
                        .font(.appSecondary)
                        .foregroundStyle(Color.sDanger)
                        .textSelection(.enabled)
                }
            }
        }
    }

    private var statusBadge: Badge {
        switch activator.status {
        case .activated:
            return Badge(text: activator.status.label, tone: .ok, dot: true)
        case .activatedPendingReboot, .needsUserApproval, .requesting:
            return Badge(text: activator.status.label, tone: .warn, dot: true)
        case .failed:
            return Badge(text: "Failed", tone: .danger, dot: true)
        case .idle:
            return Badge(text: activator.status.label, tone: .dim)
        }
    }

    // MARK: - Launch app through proxy

    private var launchCommandCard: some View {
        Card(title: "Launch app through proxy") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                Text("Runs a command with its arguments routed through the proxy, e.g. \"/usr/bin/curl -v https://example.com\".")
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextDim)
                HStack(spacing: Spacing.sm) {
                    TextField("Command and arguments…", text: $launchCommand)
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.large)
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
                    Text(launchCommandError).font(.appSecondary).foregroundStyle(Color.sDanger)
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

    /// Search-filtered (via `.searchable`) then sorted by the active column.
    private var filteredInstalledApps: [InstalledApp] {
        let base = installedSearch.isEmpty
            ? installedApps
            : installedApps.filter {
                $0.name.localizedCaseInsensitiveContains(installedSearch)
                    || $0.bundleID.localizedCaseInsensitiveContains(installedSearch)
            }
        return base.sorted(using: installedSort)
    }

    private var installedAppsCard: some View {
        Card(title: "Launch an app in proxy") {
            AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: isLoadingInstalled) {
                Task { await loadInstalledApps() }
            }
            .keyboardShortcut("r", modifiers: .command)
        } content: {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let installedError {
                    Text(installedError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                if filteredInstalledApps.isEmpty {
                    EmptyState(
                        text: installedSearch.isEmpty ? "No installed applications found." : "No apps match your search.",
                        symbol: "app.dashed"
                    )
                } else {
                    List {
                        ForEach(filteredInstalledApps) { app in
                            HStack(spacing: Spacing.md) {
                                Text(app.name)
                                    .font(.appBody)
                                    .foregroundStyle(Color.sText)
                                    .frame(minWidth: 160, alignment: .leading)
                                Text(app.bundleID)
                                    .font(.system(size: 16, design: .monospaced))
                                    .foregroundStyle(Color.sTextDim)
                                    .lineLimit(1)
                                Spacer()
                                AppButton(
                                    "Launch in proxy", icon: "bolt.fill",
                                    isLoading: launchingBundleID == app.bundleID,
                                    disabled: launchingBundleID != nil
                                ) {
                                    launchInstalled(app)
                                }
                            }
                            .padding(.vertical, Spacing.xs)
                            .listRowBackground(Color.clear)
                            .listRowSeparator(.hidden)
                        }
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
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

    // MARK: - Applications + Currently proxied (adaptive layout)

    private var managementTables: some View {
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: Spacing.md) {
                applicationsCard.frame(minWidth: 360)
                proxiedCard.frame(minWidth: 360)
            }
            VStack(spacing: Spacing.md) {
                applicationsCard
                proxiedCard
            }
        }
    }

    // MARK: - Applications (running, routable)

    private var sortedRunningApps: [Application] { runningApps.sorted(using: appsSort) }

    private var applicationsCard: some View {
        Card(title: "Applications") {
            Badge(text: "\(runningApps.count)", tone: .accent)
        } content: {
            if runningApps.isEmpty {
                EmptyState(text: "No running applications.", symbol: "app.badge")
            } else {
                Table(sortedRunningApps, sortOrder: $appsSort) {
                    TableColumn("App", value: \.name) { app in
                        HStack(spacing: Spacing.sm) {
                            Text(app.name).font(.appBody).foregroundStyle(Color.sText)
                            Badge(text: app.running ? "Running" : "Not running", tone: app.running ? .ok : .dim)
                        }
                    }
                    .width(min: 160, ideal: 200)

                    TableColumn("Bundle ID", value: \.bundleID) { app in
                        Text(app.bundleID)
                            .font(.system(size: 16, design: .monospaced))
                            .foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 140, ideal: 220)

                    TableColumn("PIDs") { app in
                        Text(app.pids.map(String.init).joined(separator: ", "))
                            .font(.appBody)
                            .foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 60, ideal: 90)

                    TableColumn("") { app in
                        let isMutating = mutatingBundleIDs.contains(app.bundleID)
                        if routedBundleIDs.contains(app.bundleID) {
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
                    .width(min: 110, ideal: 130, max: 150)
                }
                .tableStyle(.inset(alternatesRowBackgrounds: false))
                .scrollContentBackground(.hidden)
                .background(.clear)
            }
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

    private var sortedProxiedApps: [ProxiedApp] { proxiedApps.sorted(using: proxiedSort) }

    private var proxiedCard: some View {
        Card(title: "Currently proxied") {
            Badge(text: "\(proxiedApps.count)", tone: .accent)
        } content: {
            if proxiedApps.isEmpty {
                EmptyState(text: "No proxied apps yet.", symbol: "app.dashed")
            } else {
                Table(sortedProxiedApps, sortOrder: $proxiedSort) {
                    TableColumn("App", value: \.name) { app in
                        HStack(spacing: Spacing.sm) {
                            Text(app.name).font(.appBody).foregroundStyle(Color.sText)
                            Badge(text: app.running ? "Running" : "Not running", tone: app.running ? .ok : .dim)
                        }
                    }
                    .width(min: 160, ideal: 200)

                    TableColumn("Bundle ID", value: \.bundleID) { app in
                        Text(app.bundleID)
                            .font(.system(size: 16, design: .monospaced))
                            .foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 140, ideal: 220)

                    TableColumn("Enabled") { app in
                        Toggle("", isOn: enabledBinding(for: app))
                            .labelsHidden()
                            .toggleStyle(.switch)
                            .disabled(mutatingBundleIDs.contains(app.bundleID))
                    }
                    .width(min: 70, ideal: 80, max: 90)

                    TableColumn("") { app in
                        let isMutating = mutatingBundleIDs.contains(app.bundleID)
                        AppButton(
                            "Remove", kind: .danger, icon: "trash",
                            isLoading: isMutating, disabled: isMutating
                        ) {
                            removeProxied(app.bundleID)
                        }
                    }
                    .width(min: 110, ideal: 120, max: 140)
                }
                .tableStyle(.inset(alternatesRowBackgrounds: false))
                .scrollContentBackground(.hidden)
                .background(.clear)
            }
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
