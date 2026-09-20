// SettingsScreen.swift
//
// Restructured (F3) from a thin, single-list Form into labelled Cards, each
// row carrying a one-line explanation of what it does — the density mature
// clients (Clash Verge, Mihomo Party, Stash, Outline) use, built from THIS
// app's own tokens/components (Card, Badge, AppButton, SectionHeader,
// SegmentedControl, PillToggle, StatTile) instead of native `Form`/`Section`
// chrome, matching every other screen (Dashboard, SysProxy, …).
//
// Groups: General, Proxy, System proxy, Routing, Observability, Maintenance.
// Daemon-backed fields (Proxy/Routing/Clash) still apply only on an explicit
// "Apply" click via `Backend.settingsGet()`/`.settingsSet(_)`. Local-only UI
// preferences (launch at login, menu-bar item, VPN confirmation, Logs'
// default level filter/retention — see AppPreferences.swift) commit
// immediately, same as a normal macOS preference pane.
//
// Developer-ID build: also hosts the SOCKS port + Clash API fields (daemon-
// only concepts — there is no SOCKS listener or Clash API in the App Store
// build's sandboxed tunnel) and the destructive "Stop daemon" action
// (`Backend.stop()`), gated behind a confirmation dialog.
//
// App Store build: those daemon-only bits are hidden (`#if !APPSTORE`); in
// their place, a "VPN" toggle drives the tunnel on/off via `Backend`.

import SwiftUI
import AppKit

struct SettingsScreen: View {
    @Environment(\.backend) private var backend
    @EnvironmentObject private var navigation: NavigationModel
    @ObservedObject private var prefs = AppPreferences.shared

    @State private var isLoading = true
    @State private var loadError: String?

    // MARK: - Daemon-backed form state (Apply-gated)

    @State private var autostartMode: String = "off"
    @State private var socksPortText: String = ""
    @State private var clashEnabled = false
    @State private var clashAddr: String = ""
    @State private var urlTestURL: String = ""
    @State private var urlTestInterval: String = ""
    @State private var urlTestToleranceText: String = ""
    @State private var saveProfile = false

    @State private var isApplying = false
    @State private var applyMessage: String?
    @State private var applyIsError = false

    // MARK: - System proxy summary (read-only; mutation lives on SysProxyScreen)

    @State private var sysProxyStatus: SysProxyStatus = .empty
    @State private var sysProxyError: String?

    // MARK: - Maintenance

    @State private var showResetConfirm = false
    @State private var isResetting = false

    #if !APPSTORE
    @State private var isStopping = false
    @State private var showStopConfirm = false
    @State private var stopError: String?
    #else
    @State private var vpnOn = false
    @State private var isTogglingVPN = false
    @State private var vpnError: String?
    #endif

    private let autostartOptions: [SegmentedOption<String>] = [
        SegmentedOption("off", "Off"), SegmentedOption("proxy", "Proxy"), SegmentedOption("vpn", "VPN"),
    ]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "Settings", subtitle: "Grouped by what each control affects — Apply is explicit for anything the daemon needs to act on.")

                if let loadError {
                    Card { Text(loadError).foregroundStyle(Color.sDanger) }
                }

                if isLoading {
                    Card {
                        HStack(spacing: Spacing.sm) {
                            ProgressView().controlSize(.small)
                            Text("Loading settings…").foregroundStyle(.secondary)
                        }
                    }
                } else {
                    generalCard
                    #if !APPSTORE
                    proxyCard
                    #endif
                    systemProxyCard
                    routingCard
                    #if !APPSTORE
                    observabilityCard
                    #endif
                    applyCard
                    maintenanceCard
                    aboutCard
                }
            }
            .padding(Spacing.lg)
        }
        .navigationTitle("Settings")
        .task {
            await load()
            await loadSysProxyStatus()
        }
        #if !APPSTORE
        .confirmationDialog(
            "Stop the singctl daemon?", isPresented: $showStopConfirm, titleVisibility: .visible
        ) {
            Button("Stop daemon", role: .destructive) { stopDaemon() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This disconnects the proxy and stops routing traffic until the daemon is started again.")
        }
        #endif
        .confirmationDialog(
            "Reset settings to defaults?", isPresented: $showResetConfirm, titleVisibility: .visible
        ) {
            Button("Reset to defaults", role: .destructive) { resetToDefaults() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Replaces the SOCKS port, Clash API, and URL-test fields below with their defaults, then applies them. This does not touch keys or the system proxy.")
        }
    }

    // MARK: - General

    private var generalCard: some View {
        Card(title: "General") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SettingsRow(
                    label: "Autostart mode",
                    sub: "What the daemon switches to on its own, the moment it starts (e.g. after a reboot)."
                ) {
                    SegmentedControl(options: autostartOptions, selection: $autostartMode)
                        .frame(maxWidth: 260)
                }
                Divider().overlay(Color.sBorder)
                PillToggle(
                    "Launch singctl at login", isOn: launchAtLoginBinding,
                    sub: "Starts the app automatically when you log in to this Mac."
                )
                PillToggle(
                    "Show menu-bar item", isOn: $prefs.showMenuBarItem,
                    sub: "Keeps a quick-glance status item and mode switch in the menu bar."
                )
                PillToggle(
                    "Confirm before switching to VPN", isOn: $prefs.confirmBeforeVPN,
                    sub: "Ask first — VPN mode reroutes ALL system traffic, not just proxy-aware apps."
                )
            }
        }
    }

    private var launchAtLoginBinding: Binding<Bool> {
        Binding(get: { prefs.launchAtLogin }, set: { prefs.setLaunchAtLogin($0) })
    }

    // MARK: - Proxy

    #if !APPSTORE
    private var proxyCard: some View {
        Card(title: "Proxy") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SettingsRow(
                    label: "SOCKS port",
                    sub: "Local SOCKS5 listener; the HTTP proxy port follows automatically from it."
                ) {
                    TextField("1080", text: $socksPortText)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.large)
                        .frame(width: 100)
                }
                if let conflict = portConflictMessage {
                    Text(conflict).font(.appCaption).foregroundStyle(Color.sWarn)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "Local listen address", sub: "SOCKS and HTTP always listen on localhost only — not configurable.") {
                    Text("127.0.0.1").font(.appBody).foregroundStyle(.secondary)
                }
            }
        }
    }

    /// Client-side conflict check limited to what this form can know for
    /// certain: whether the SOCKS port the user just typed collides with the
    /// Clash API address entered below. Deliberately does NOT attempt a
    /// live bind()-test against the port — the daemon (root) may already be
    /// the one holding it, which would make every unchanged value look like
    /// a false "conflict".
    private var portConflictMessage: String? {
        guard let socksPort = Int(socksPortText.trimmingCharacters(in: .whitespaces)) else { return nil }
        guard clashEnabled, let clashPort = clashAddr.split(separator: ":").last, let clashPortInt = Int(clashPort) else {
            return nil
        }
        guard socksPort == clashPortInt else { return nil }
        return "SOCKS port and Clash API address use the same port (\(socksPort)) — Apply will fail until one changes."
    }
    #endif

    // MARK: - System proxy (read-only summary; mutation lives on SysProxyScreen)

    private var systemProxyCard: some View {
        Card(title: "System proxy") {
            AppButton("Open System proxy screen", kind: .ghost, icon: "arrow.right.circle") {
                navigation.selection = .sysProxy
            }
        } content: {
            VStack(alignment: .leading, spacing: Spacing.md) {
                if let sysProxyError {
                    Text(sysProxyError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                SettingsRow(label: "Default mode", sub: "Currently applied — off / exclude / include. Changed from the System proxy screen.") {
                    Text(sysProxyModeLabel).font(.appBody).foregroundStyle(.secondary)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "Network service", sub: "The macOS network service singctl's PAC is applied to.") {
                    Text(sysProxyStatus.service.isEmpty ? "—" : sysProxyStatus.service)
                        .font(.appBody).foregroundStyle(.secondary)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "PAC port", sub: "0 = automatic (an ephemeral port chosen at runtime, published via status).") {
                    Text(pacPortLabel).font(.appBody).foregroundStyle(.secondary)
                }
            }
        }
    }

    private var sysProxyModeLabel: String {
        switch sysProxyStatus.mode {
        case "off": return "Off"
        case "exclude": return "Exclude"
        case "include": return "Include"
        default: return sysProxyStatus.mode.isEmpty ? "—" : sysProxyStatus.mode
        }
    }

    private var pacPortLabel: String {
        guard !sysProxyStatus.pacURL.isEmpty, let url = URL(string: sysProxyStatus.pacURL), let port = url.port else {
            return "—"
        }
        return String(port)
    }

    // MARK: - Routing

    private var routingCard: some View {
        Card(title: "Routing") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SettingsRow(label: "URL-test URL", sub: "Probed periodically to measure each server's latency for failover selection.") {
                    TextField("https://www.gstatic.com/generate_204", text: $urlTestURL)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.large)
                        .frame(width: 260)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "URL-test interval", sub: "How often the probe above runs, e.g. \"3m\".") {
                    TextField("3m", text: $urlTestInterval)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.large)
                        .frame(width: 100)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "URL-test tolerance (ms)", sub: "How much slower a server can be than the fastest before failover switches to it.") {
                    TextField("50", text: $urlTestToleranceText)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.large)
                        .frame(width: 100)
                }
                Divider().overlay(Color.sBorder)
                PillToggle(
                    "Save profile to disk", isOn: $saveProfile,
                    sub: "Persists the current server/config profile so it survives a daemon restart."
                )
            }
        }
    }

    // MARK: - Observability

    #if !APPSTORE
    private var observabilityCard: some View {
        Card(title: "Observability") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                PillToggle(
                    "Clash API", isOn: $clashEnabled,
                    sub: "Exposes a local metrics/connections API used by the Dashboard and Connections screen."
                )
                SettingsRow(label: "Clash API address", sub: "Where that API listens, e.g. 127.0.0.1:9090.") {
                    TextField("127.0.0.1:9090", text: $clashAddr)
                        .multilineTextAlignment(.trailing)
                        .controlSize(.large)
                        .frame(width: 180)
                        .disabled(!clashEnabled)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "Default log level filter", sub: "The level Logs pre-selects when opened. A view preference — not sent to the daemon.") {
                    Picker("", selection: logLevelFilterBinding) {
                        Text("All levels").tag(Optional<String>.none)
                        ForEach(LogLevel.allCases, id: \.self) { level in
                            Text(level.label).tag(Optional(level.rawValue))
                        }
                    }
                    .labelsHidden()
                    .pickerStyle(.menu)
                    .frame(width: 140)
                }
                Divider().overlay(Color.sBorder)
                SettingsRow(label: "Log retention", sub: "How many lines Logs keeps in memory before trimming the oldest.") {
                    Picker("", selection: $prefs.logRetentionLines) {
                        Text("1,000 lines").tag(1_000)
                        Text("4,000 lines").tag(4_000)
                        Text("10,000 lines").tag(10_000)
                        Text("20,000 lines").tag(20_000)
                    }
                    .labelsHidden()
                    .pickerStyle(.menu)
                    .frame(width: 140)
                }
            }
        }
    }

    private var logLevelFilterBinding: Binding<String?> {
        Binding(get: { prefs.defaultLogLevelFilter }, set: { prefs.defaultLogLevelFilter = $0 })
    }
    #endif

    // MARK: - Apply (daemon-backed fields only: Proxy/Routing/Observability)

    private var applyCard: some View {
        Card {
            HStack(spacing: Spacing.sm) {
                AppButton("Apply", isLoading: isApplying) { apply() }
                Text("Applies the Proxy, Routing, and Observability fields above.")
                    .font(.appCaption)
                    .foregroundStyle(.secondary)
                Spacer()
                if let applyMessage {
                    Text(applyMessage)
                        .font(.appSecondary)
                        .foregroundStyle(applyIsError ? Color.sDanger : Color.sOk)
                }
            }
        }
    }

    // MARK: - Maintenance

    private var maintenanceCard: some View {
        Card(title: "Maintenance") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                #if !APPSTORE
                SettingsRow(label: "Config directory", sub: "Where instance.json, keys, and singbox.log live.") {
                    AppButton("Open in Finder", kind: .ghost, icon: "folder") { openConfigDirectory() }
                }
                Divider().overlay(Color.sBorder)
                #endif
                SettingsRow(label: "Reset to defaults", sub: "Restores SOCKS/Clash/URL-test fields above to sane defaults, then applies them.") {
                    AppButton("Reset…", kind: .ghost, icon: "arrow.counterclockwise", isLoading: isResetting) {
                        showResetConfirm = true
                    }
                }
                #if !APPSTORE
                Divider().overlay(Color.sBorder)
                if let stopError {
                    Text(stopError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                SettingsRow(label: "Stop daemon", sub: "Disconnects the proxy and stops routing traffic until started again.") {
                    AppButton("Stop daemon", kind: .danger, icon: "power", isLoading: isStopping) {
                        showStopConfirm = true
                    }
                }
                #else
                Divider().overlay(Color.sBorder)
                if let vpnError {
                    Text(vpnError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                SettingsRow(label: "VPN", sub: "Routes all system traffic through singctl.") {
                    Toggle("", isOn: vpnBinding).labelsHidden().toggleStyle(.switch).disabled(isTogglingVPN)
                }
                #endif
            }
        }
    }

    #if APPSTORE
    private var vpnBinding: Binding<Bool> {
        Binding(get: { vpnOn }, set: toggleVPN)
    }
    #endif

    private var aboutCard: some View {
        Card(title: "About") {
            SettingsRow(label: "Version", sub: "singctl's app version.") {
                Text(Bundle.main.appVersion).font(.appBody).foregroundStyle(.secondary)
            }
        }
    }

    // MARK: - Actions

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let settings = try await backend.settingsGet()
            populate(from: settings)
            loadError = nil
        } catch {
            loadError = error.localizedDescription
        }
        #if APPSTORE
        if let status = try? await backend.status() {
            vpnOn = !status.mode.isEmpty && status.mode != "off"
        }
        #endif
    }

    private func loadSysProxyStatus() async {
        do {
            sysProxyStatus = try await backend.sysProxyStatus()
            sysProxyError = nil
        } catch {
            sysProxyStatus = .empty
            sysProxyError = error.localizedDescription
        }
    }

    private func populate(from settings: Settings) {
        autostartMode = settings.autostartMode ?? "off"
        socksPortText = String(settings.socksPort)
        clashEnabled = settings.clashEnabled
        clashAddr = settings.clashAddr
        urlTestURL = settings.urlTestURL
        urlTestInterval = settings.urlTestInterval
        urlTestToleranceText = String(settings.urlTestTolerance)
        saveProfile = settings.saveProfile
    }

    private func buildSettings() -> Settings? {
        guard let socksPort = Int(socksPortText.trimmingCharacters(in: .whitespaces)),
              let tolerance = Int(urlTestToleranceText.trimmingCharacters(in: .whitespaces))
        else {
            return nil
        }
        return Settings(
            socksPort: socksPort,
            clashEnabled: clashEnabled,
            clashAddr: clashAddr,
            urlTestURL: urlTestURL,
            urlTestInterval: urlTestInterval,
            urlTestTolerance: tolerance,
            saveProfile: saveProfile,
            autostartMode: autostartMode
        )
    }

    private func apply() {
        guard portConflictMessageIfAny() == nil else {
            applyIsError = true
            applyMessage = portConflictMessageIfAny()
            return
        }
        guard let newSettings = buildSettings() else {
            applyIsError = true
            applyMessage = "SOCKS port and URL-test tolerance must be whole numbers."
            return
        }
        isApplying = true
        applyMessage = nil
        Task {
            defer { isApplying = false }
            do {
                try await backend.settingsSet(newSettings)
                applyIsError = false
                applyMessage = "Applied."
            } catch {
                applyIsError = true
                applyMessage = error.localizedDescription
            }
        }
    }

    /// `portConflictMessage` is `#if !APPSTORE`-only (it's the Proxy card's
    /// concern); this indirection lets `apply()` call it unconditionally
    /// without spreading `#if` into the action itself.
    private func portConflictMessageIfAny() -> String? {
        #if !APPSTORE
        return portConflictMessage
        #else
        return nil
        #endif
    }

    private func resetToDefaults() {
        autostartMode = "off"
        socksPortText = "1080"
        clashEnabled = false
        clashAddr = "127.0.0.1:9090"
        urlTestURL = "https://www.gstatic.com/generate_204"
        urlTestInterval = "3m"
        urlTestToleranceText = "50"
        saveProfile = false
        isResetting = true
        applyMessage = nil
        Task {
            defer { isResetting = false }
            guard let defaults = buildSettings() else { return }
            do {
                try await backend.settingsSet(defaults)
                applyIsError = false
                applyMessage = "Reset to defaults."
            } catch {
                applyIsError = true
                applyMessage = error.localizedDescription
            }
        }
    }

    #if !APPSTORE
    private func openConfigDirectory() {
        NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: InstanceDiscovery.configDir)
    }

    private func stopDaemon() {
        isStopping = true
        stopError = nil
        Task {
            defer { isStopping = false }
            do {
                try await backend.stop()
            } catch {
                stopError = error.localizedDescription
            }
        }
    }
    #else
    private func toggleVPN(_ on: Bool) {
        if on, !AppPreferences.shared.confirmVPNSwitch() { return }
        isTogglingVPN = true
        vpnError = nil
        Task {
            defer { isTogglingVPN = false }
            do {
                if on {
                    try await backend.setMode("vpn")
                } else {
                    try await backend.stop()
                }
                vpnOn = on
            } catch {
                vpnError = error.localizedDescription
            }
        }
    }
    #endif
}

// MARK: - Row layout

/// A settings row: label + one-line explanation on the left, an arbitrary
/// trailing control on the right — the shared shape every group below uses
/// for its non-toggle rows (`PillToggle` already has its own matching
/// label+sub+switch layout for boolean rows).
private struct SettingsRow<Content: View>: View {
    private let label: String
    private let sub: String?
    private let content: Content

    init(label: String, sub: String? = nil, @ViewBuilder content: () -> Content) {
        self.label = label
        self.sub = sub
        self.content = content()
    }

    var body: some View {
        HStack(alignment: .top, spacing: Spacing.md) {
            VStack(alignment: .leading, spacing: 2) {
                Text(label).font(.appBody).foregroundStyle(.primary)
                if let sub {
                    Text(sub).font(.appCaption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: Spacing.md)
            content
        }
    }
}
