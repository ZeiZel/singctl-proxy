// SettingsScreen.swift
//
// Daemon settings, read/edited/applied through `ControlClient.settingsGet()`/
// `.settingsSet(_)`. Also hosts the destructive "Stop daemon" action
// (`ControlClient.stop()`), gated behind a confirmation dialog.

import SwiftUI

struct SettingsScreen: View {
    @Environment(\.controlClient) private var control

    @State private var isLoading = true
    @State private var loadError: String?

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

    @State private var isStopping = false
    @State private var showStopConfirm = false
    @State private var stopError: String?

    var body: some View {
        Form {
            errorSection
            if isLoading {
                loadingSection
            } else {
                proxySection
                clashSection
                urlTestSection
                profileSection
                applySection
                dangerSection
            }
        }
        .formStyle(.grouped)
        .navigationTitle("Settings")
        .task { await load() }
        .confirmationDialog(
            "Stop the singctl daemon?", isPresented: $showStopConfirm, titleVisibility: .visible
        ) {
            Button("Stop daemon", role: .destructive) { stopDaemon() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This disconnects the proxy and stops routing traffic until the daemon is started again.")
        }
    }

    // MARK: - Sections

    @ViewBuilder
    private var errorSection: some View {
        if let loadError {
            SwiftUI.Section {
                Text(loadError).foregroundStyle(Color.sDanger)
            }
        }
    }

    private var loadingSection: some View {
        SwiftUI.Section {
            HStack(spacing: Spacing.sm) {
                ProgressView().controlSize(.small)
                Text("Loading settings…").foregroundStyle(.secondary)
            }
        }
    }

    private var proxySection: some View {
        SwiftUI.Section("Proxy") {
            LabeledContent("SOCKS port") {
                TextField("1080", text: $socksPortText)
                    .multilineTextAlignment(.trailing)
                    .controlSize(.large)
                    .frame(width: 120)
            }
        }
    }

    private var clashSection: some View {
        SwiftUI.Section("Clash API") {
            Toggle("Clash API", isOn: $clashEnabled)
            LabeledContent("Clash API address") {
                TextField("127.0.0.1:9090", text: $clashAddr)
                    .multilineTextAlignment(.trailing)
                    .controlSize(.large)
                    .frame(width: 200)
            }
            .disabled(!clashEnabled)
        }
    }

    private var urlTestSection: some View {
        SwiftUI.Section("URL Test") {
            LabeledContent("URLTest URL") {
                TextField("https://www.gstatic.com/generate_204", text: $urlTestURL)
                    .multilineTextAlignment(.trailing)
                    .controlSize(.large)
                    .frame(width: 260)
            }
            LabeledContent("URLTest interval") {
                TextField("3m", text: $urlTestInterval)
                    .multilineTextAlignment(.trailing)
                    .controlSize(.large)
                    .frame(width: 120)
            }
            LabeledContent("URLTest tolerance (ms)") {
                TextField("50", text: $urlTestToleranceText)
                    .multilineTextAlignment(.trailing)
                    .controlSize(.large)
                    .frame(width: 120)
            }
        }
    }

    private var profileSection: some View {
        SwiftUI.Section("Profile") {
            Toggle("Save profile to disk", isOn: $saveProfile)
        }
    }

    private var applySection: some View {
        SwiftUI.Section {
            HStack {
                Button("Apply", action: apply)
                    .buttonStyle(.borderedProminent)
                    .disabled(isApplying)
                if isApplying {
                    ProgressView().controlSize(.small)
                }
                if let applyMessage {
                    Text(applyMessage)
                        .font(.callout)
                        .foregroundStyle(applyIsError ? Color.sDanger : Color.sOk)
                }
            }
        }
    }

    @ViewBuilder
    private var dangerSection: some View {
        SwiftUI.Section {
            if let stopError {
                Text(stopError).font(.callout).foregroundStyle(Color.sDanger)
            }
            Button("Stop daemon", role: .destructive) {
                showStopConfirm = true
            }
            .disabled(isStopping)
        }
    }

    // MARK: - Actions

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let settings = try await control.settingsGet()
            populate(from: settings)
            loadError = nil
        } catch {
            loadError = error.localizedDescription
        }
    }

    private func populate(from settings: Settings) {
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
            saveProfile: saveProfile
        )
    }

    private func apply() {
        guard let newSettings = buildSettings() else {
            applyIsError = true
            applyMessage = "SOCKS port and URLTest tolerance must be whole numbers."
            return
        }
        isApplying = true
        applyMessage = nil
        Task {
            defer { isApplying = false }
            do {
                try await control.settingsSet(newSettings)
                applyIsError = false
                applyMessage = "Applied."
            } catch {
                applyIsError = true
                applyMessage = error.localizedDescription
            }
        }
    }

    private func stopDaemon() {
        isStopping = true
        stopError = nil
        Task {
            defer { isStopping = false }
            do {
                try await control.stop()
            } catch {
                stopError = error.localizedDescription
            }
        }
    }
}
