// SettingsScreen.swift
// Native grouped form for local preferences and daemon-backed settings.

import SwiftUI
import AppKit

struct SettingsScreen: View {
    @Environment(\.backend) private var backend
    @EnvironmentObject private var navigation: NavigationModel
    @ObservedObject private var prefs = AppPreferences.shared

    @State private var isLoading = true
    @State private var loadError: String?
    @State private var appliedSettings: Settings?
    @State private var autostartMode = "off"
    @State private var socksPortText = ""
    @State private var clashEnabled = false
    @State private var clashAddr = ""
    @State private var urlTestURL = ""
    @State private var urlTestInterval = ""
    @State private var urlTestToleranceText = ""
    @State private var saveProfile = false
    @State private var isApplying = false
    @State private var applyMessage: String?
    @State private var applyIsError = false
    @State private var sysProxyStatus: SysProxyStatus = .empty
    @State private var sysProxyError: String?
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

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            SectionHeader(title: "Settings", subtitle: "App preferences and connection options")
                .padding(.horizontal, Spacing.lg)
                .padding(.top, Spacing.lg)
            if isLoading {
                ProgressView("Loading settings…").frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Form {
                    SwiftUI.Section {
                        Picker("Autostart mode", selection: $autostartMode) {
                            Text("Off").tag("off"); Text("Proxy").tag("proxy"); Text("VPN").tag("vpn")
                        }
                        .disabled(appliedSettings == nil || isBusy)
                        Toggle("Launch singctl at login", isOn: launchAtLoginBinding)
                        Toggle("Show menu-bar item", isOn: $prefs.showMenuBarItem)
                        Toggle("Confirm before switching to VPN", isOn: $prefs.confirmBeforeVPN)
                    } header: { sectionHeader("General", subtitle: "Startup and app preferences") }

                    #if !APPSTORE
                    SwiftUI.Section {
                        field("SOCKS port", text: $socksPortText, prompt: "1080")
                        if let portError { validation(portError) }
                        LabeledContent("Local listen address") { Text("127.0.0.1").foregroundStyle(.secondary) }
                    } header: { sectionHeader("Connection", subtitle: "Local proxy listener") }
                    .disabled(appliedSettings == nil || isApplying || isResetting)
                    #endif

                    SwiftUI.Section {
                        field("URL-test URL", text: $urlTestURL, prompt: "https://www.gstatic.com/generate_204")
                        field("URL-test interval", text: $urlTestInterval, prompt: "3m")
                        field("Tolerance (ms)", text: $urlTestToleranceText, prompt: "50")
                        if let toleranceError { validation(toleranceError) }
                        Toggle("Save profile to disk", isOn: $saveProfile)
                    } header: { sectionHeader("Routing", subtitle: "Health checks and profile persistence") }
                    .disabled(appliedSettings == nil || isApplying || isResetting)

                    #if !APPSTORE
                    SwiftUI.Section {
                        Toggle("Clash API", isOn: $clashEnabled).disabled(appliedSettings == nil || isBusy)
                        field("Clash API address", text: $clashAddr, prompt: "127.0.0.1:9090").disabled(!clashEnabled || appliedSettings == nil || isBusy)
                        if let portConflictMessage { validation(portConflictMessage) }
                        Picker("Default log level filter", selection: logLevelFilterBinding) {
                            Text("All levels").tag(Optional<String>.none)
                            ForEach(LogLevel.allCases, id: \.self) { level in Text(level.label).tag(Optional(level.rawValue)) }
                        }
                        Picker("Log retention", selection: $prefs.logRetentionLines) {
                            Text("1,000 lines").tag(1_000); Text("4,000 lines").tag(4_000)
                            Text("10,000 lines").tag(10_000); Text("20,000 lines").tag(20_000)
                        }
                    } header: { sectionHeader("Observability", subtitle: "Local diagnostics and API access") }
                    #else
                    SwiftUI.Section {
                        Toggle("VPN", isOn: vpnBinding).disabled(isTogglingVPN)
                        if let vpnError { validation(vpnError) }
                    } header: { sectionHeader("Connection", subtitle: "System-wide tunnel") }
                    #endif

                    #if !APPSTORE
                    SwiftUI.Section {
                        if let sysProxyError { validation(sysProxyError) }
                        LabeledContent("Mode") { Text(sysProxyModeLabel).foregroundStyle(.secondary) }
                        LabeledContent("Network service") { Text(sysProxyStatus.service.isEmpty ? "—" : sysProxyStatus.service).foregroundStyle(.secondary) }
                        LabeledContent("PAC port") { Text(pacPortLabel).foregroundStyle(.secondary) }
                        Button("Open System proxy settings", systemImage: "arrow.right.circle") { navigation.selection = .sysProxy }
                    } header: { sectionHeader("System proxy", subtitle: "Read-only status; changes happen on the dedicated screen") }
                    #endif

                    SwiftUI.Section {
                        #if !APPSTORE
                        LabeledContent("Config directory") { Button("Open in Finder", systemImage: "folder") { openConfigDirectory() } }
                        #endif
                        LabeledContent("Reset settings") {
                            Button("Reset…", systemImage: "arrow.counterclockwise", role: .destructive) { showResetConfirm = true }.disabled(isBusy)
                        }
                        #if !APPSTORE
                        if let stopError { validation(stopError) }
                        LabeledContent("Stop daemon") { Button("Stop daemon", systemImage: "power", role: .destructive) { showStopConfirm = true }.disabled(isStopping || isApplying || isResetting) }
                        #endif
                    } header: { sectionHeader("Maintenance", subtitle: "Destructive actions require confirmation") }

                    SwiftUI.Section {
                        LabeledContent("Version") { Text(Bundle.main.appVersion).textSelection(.enabled) }
                        #if !APPSTORE
                        LabeledContent("Build") { Text(Bundle.main.infoDictionary?["CFBundleVersion"] as? String ?? "—").textSelection(.enabled) }
                        #endif
                    } header: { sectionHeader("About") }
                }
                .formStyle(.grouped)
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if !isLoading, isDirty {
                actionBar
            } else if let applyMessage {
                HStack {
                    Label(applyMessage, systemImage: applyIsError ? "xmark.circle.fill" : "checkmark.circle.fill")
                    Spacer()
                    Button("Dismiss") { self.applyMessage = nil }
                }
                .padding(.horizontal, Spacing.md)
                .padding(.vertical, Spacing.sm)
                .foregroundStyle(applyIsError ? Color.sDanger : Color.sOk)
                .background(.regularMaterial)
                .overlay(alignment: .top) { Divider() }
            }
        }
        .safeAreaInset(edge: .top, spacing: 0) {
            if let loadError {
                HStack(spacing: Spacing.sm) {
                    Label(loadError, systemImage: "exclamationmark.triangle.fill")
                    Spacer()
                    Button("Retry") { Task { await load(); await loadSysProxyStatus() } }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(Spacing.md)
                .foregroundStyle(Color.sDanger)
                .background(.regularMaterial)
            }
        }
        .task { await load(); await loadSysProxyStatus() }
        #if !APPSTORE
        .confirmationDialog("Stop the singctl daemon?", isPresented: $showStopConfirm, titleVisibility: .visible) {
            Button("Stop daemon", role: .destructive) { stopDaemon() }; Button("Cancel", role: .cancel) {}
        } message: { Text("This disconnects the proxy and stops routing traffic until the daemon is started again.") }
        #endif
        .confirmationDialog("Reset settings to defaults?", isPresented: $showResetConfirm, titleVisibility: .visible) {
            Button("Reset to defaults", role: .destructive) { resetToDefaults() }; Button("Cancel", role: .cancel) {}
        } message: { Text("This applies default proxy and routing values. Keys and system proxy settings are unchanged.") }
    }

    private func sectionHeader(_ title: String, subtitle: String? = nil) -> some View {
        VStack(alignment: .leading, spacing: 2) { Text(title); if let subtitle { Text(subtitle).font(.caption).foregroundStyle(.secondary) } }
    }
    private func field(_ label: String, text: Binding<String>, prompt: String) -> some View {
        LabeledContent(label) {
            TextField("", text: text, prompt: Text(prompt))
                .labelsHidden()
                .textFieldStyle(.roundedBorder)
                .frame(minWidth: 100, maxWidth: 340)
        }
    }
    private func validation(_ text: String) -> some View {
        Label(text, systemImage: "exclamationmark.triangle.fill").font(.callout).foregroundStyle(Color.sDanger).fixedSize(horizontal: false, vertical: true)
    }
    private var actionBar: some View {
        HStack(spacing: Spacing.sm) {
            Image(systemName: "circle.fill").font(.caption2).foregroundStyle(Color.sAccent); Text("Unsaved daemon settings"); Spacer()
            if applyIsError, let applyMessage { Text(applyMessage).foregroundStyle(Color.sDanger) }
            Button("Revert") { revert() }.disabled(isBusy)
            Button("Apply changes") { apply() }.buttonStyle(.borderedProminent).disabled(!isValid || isBusy)
        }.padding(.horizontal, Spacing.md).padding(.vertical, Spacing.sm).background(.regularMaterial).overlay(alignment: .top) { Divider() }
    }
    private var launchAtLoginBinding: Binding<Bool> { Binding(get: { prefs.launchAtLogin }, set: { prefs.setLaunchAtLogin($0) }) }

    private var isBusy: Bool {
        #if !APPSTORE
        return isApplying || isResetting || isStopping
        #else
        return isApplying || isResetting || isTogglingVPN
        #endif
    }

    #if !APPSTORE
    private var portError: String? {
        guard SettingsDraft.validPort(socksPortText) else {
            return socksPortText.isEmpty ? "Enter a SOCKS port from 1 to 65535." : "SOCKS port must be a whole number from 1 to 65535."
        }
        return nil
    }
    #endif
    private var toleranceError: String? {
        guard SettingsDraft.validTolerance(urlTestToleranceText) else { return "URL-test tolerance must be a non-negative whole number." }
        return nil
    }
    private var portConflictMessage: String? {
        #if !APPSTORE
        guard SettingsDraft.hasPortConflict(socksPort: socksPortText, clashEnabled: clashEnabled, clashAddress: clashAddr),
              let socks = Int(socksPortText.trimmingCharacters(in: .whitespaces)) else { return nil }
        return "SOCKS port and Clash API address use the same port (\(socks)). Choose another port before applying."
        #else
        return nil
        #endif
    }
    private var isValid: Bool {
        #if !APPSTORE
        return portError == nil && toleranceError == nil && portConflictMessage == nil
        #else
        return toleranceError == nil
        #endif
    }
    private var isDirty: Bool {
        guard let appliedSettings, let draft = buildSettings() else { return appliedSettings != nil }
        return SettingsDraft.isDirty(draft: draft, applied: appliedSettings)
    }
    private var logLevelFilterBinding: Binding<String?> { Binding(get: { prefs.defaultLogLevelFilter }, set: { prefs.defaultLogLevelFilter = $0 }) }
    private var sysProxyModeLabel: String { switch sysProxyStatus.mode { case "off": return "Off"; case "exclude": return "Exclude"; case "include": return "Include"; default: return sysProxyStatus.mode.isEmpty ? "—" : sysProxyStatus.mode } }
    private var pacPortLabel: String { guard let url = URL(string: sysProxyStatus.pacURL), let port = url.port else { return "—" }; return String(port) }

    private func load() async {
        isLoading = true; defer { isLoading = false }
        do {
            let settings = try await backend.settingsGet()
            appliedSettings = SettingsDraft.normalized(settings)
            populate(from: appliedSettings!)
            loadError = nil
        } catch {
            appliedSettings = nil
            loadError = error.localizedDescription
        }
        #if APPSTORE
        if let status = try? await backend.status() { vpnOn = !status.mode.isEmpty && status.mode != "off" }
        #endif
    }
    private func loadSysProxyStatus() async {
        #if !APPSTORE
        do { sysProxyStatus = try await backend.sysProxyStatus(); sysProxyError = nil } catch { sysProxyStatus = .empty; sysProxyError = error.localizedDescription }
        #endif
    }
    private func populate(from settings: Settings) {
        autostartMode = settings.autostartMode ?? "off"; socksPortText = String(settings.socksPort); clashEnabled = settings.clashEnabled; clashAddr = settings.clashAddr; urlTestURL = settings.urlTestURL; urlTestInterval = settings.urlTestInterval; urlTestToleranceText = String(settings.urlTestTolerance); saveProfile = settings.saveProfile
    }
    private func buildSettings() -> Settings? {
        guard let tolerance = Int(urlTestToleranceText.trimmingCharacters(in: .whitespaces)) else { return nil }
        #if !APPSTORE
        guard let socks = Int(socksPortText.trimmingCharacters(in: .whitespaces)) else { return nil }
        #else
        let socks = Int(socksPortText.trimmingCharacters(in: .whitespaces)) ?? appliedSettings?.socksPort ?? 1080
        #endif
        return Settings(socksPort: socks, clashEnabled: clashEnabled, clashAddr: clashAddr, urlTestURL: urlTestURL, urlTestInterval: urlTestInterval, urlTestTolerance: tolerance, saveProfile: saveProfile, autostartMode: autostartMode)
    }
    private func revert() { guard let appliedSettings else { return }; populate(from: appliedSettings); applyMessage = nil }
    private func apply() {
        guard !isBusy, isValid, let settings = buildSettings() else { return }
        isApplying = true
        applyMessage = nil
        Task {
            defer { isApplying = false }
            do {
                try await backend.settingsSet(settings)
                appliedSettings = SettingsDraft.normalized(settings)
                applyIsError = false
                applyMessage = "Applied."
            } catch {
                applyIsError = true
                applyMessage = error.localizedDescription
            }
        }
    }
    private func resetToDefaults() {
        guard !isBusy else { return }
        autostartMode = "off"
        socksPortText = "1080"
        clashEnabled = false
        clashAddr = "127.0.0.1:9090"
        urlTestURL = "https://www.gstatic.com/generate_204"
        urlTestInterval = "3m"
        urlTestToleranceText = "50"
        saveProfile = false
        isResetting = true
        Task {
            defer { isResetting = false }
            guard let defaults = buildSettings() else { return }
            do {
                try await backend.settingsSet(defaults)
                appliedSettings = SettingsDraft.normalized(defaults)
                applyIsError = false
                applyMessage = "Reset to defaults."
            } catch {
                applyIsError = true
                applyMessage = error.localizedDescription
            }
        }
    }

    #if !APPSTORE
    private func openConfigDirectory() { NSWorkspace.shared.selectFile(nil, inFileViewerRootedAtPath: InstanceDiscovery.configDir) }
    private func stopDaemon() {
        guard !isBusy else { return }
        isStopping = true
        stopError = nil
        Task {
            defer { isStopping = false }
            do { try await backend.stop() } catch { stopError = error.localizedDescription }
        }
    }
    #else
    private var vpnBinding: Binding<Bool> { Binding(get: { vpnOn }, set: toggleVPN) }
    private func toggleVPN(_ on: Bool) {
        if on, !AppPreferences.shared.confirmVPNSwitch() { return }; isTogglingVPN = true; vpnError = nil
        Task { defer { isTogglingVPN = false }; do { if on { try await backend.setMode("vpn") } else { try await backend.stop() }; vpnOn = on } catch { vpnError = error.localizedDescription } }
    }
    #endif
}
