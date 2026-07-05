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
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "Settings")

                if let loadError {
                    Card {
                        Text(loadError).foregroundStyle(Color.sDanger)
                    }
                }

                if isLoading {
                    Card {
                        HStack(spacing: Spacing.sm) {
                            ProgressView().controlSize(.small)
                            Text("Loading settings…").font(.subheadline).foregroundStyle(Color.sTextDim)
                        }
                    }
                } else {
                    proxyCard
                    clashCard
                    urlTestCard
                    profileCard
                    applyRow
                    dangerCard
                }
            }
            .padding(Spacing.lg)
        }
        .task { await load() }
    }

    // MARK: - Cards

    private var proxyCard: some View {
        Card(title: "Proxy") {
            labeledField("SOCKS port", text: $socksPortText, placeholder: "1080")
        }
    }

    private var clashCard: some View {
        Card(title: "Clash API") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                PillToggle("Clash API", isOn: $clashEnabled, sub: "Exposes a local metrics/connections API")
                labeledField("Clash API address", text: $clashAddr, placeholder: "127.0.0.1:9090")
                    .disabled(!clashEnabled)
                    .opacity(clashEnabled ? 1 : 0.5)
            }
        }
    }

    private var urlTestCard: some View {
        Card(title: "URL Test") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                labeledField("URLTest URL", text: $urlTestURL, placeholder: "https://www.gstatic.com/generate_204")
                labeledField("URLTest interval", text: $urlTestInterval, placeholder: "3m")
                labeledField("URLTest tolerance (ms)", text: $urlTestToleranceText, placeholder: "50")
            }
        }
    }

    private var profileCard: some View {
        Card(title: "Profile") {
            PillToggle("Save profile to disk", isOn: $saveProfile)
        }
    }

    private var applyRow: some View {
        Card {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let applyMessage {
                    Text(applyMessage)
                        .font(.caption)
                        .foregroundStyle(applyIsError ? Color.sDanger : Color.sOk)
                }
                HStack {
                    AppButton("Apply", icon: "checkmark", isLoading: isApplying, disabled: isApplying, action: apply)
                    Spacer()
                }
            }
        }
    }

    private var dangerCard: some View {
        Card(title: "Danger zone") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let stopError {
                    Text(stopError).font(.caption).foregroundStyle(Color.sDanger)
                }
                HStack {
                    AppButton(
                        "Stop daemon", kind: .danger, icon: "stop.circle",
                        isLoading: isStopping, disabled: isStopping
                    ) {
                        showStopConfirm = true
                    }
                    Spacer()
                }
            }
        }
        .confirmationDialog(
            "Stop the singctl daemon?", isPresented: $showStopConfirm, titleVisibility: .visible
        ) {
            Button("Stop daemon", role: .destructive) { stopDaemon() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This disconnects the proxy and stops routing traffic until the daemon is started again.")
        }
    }

    private func labeledField(_ label: String, text: Binding<String>, placeholder: String = "") -> some View {
        VStack(alignment: .leading, spacing: Spacing.xs) {
            Text(label.uppercased()).font(.caption2).foregroundStyle(Color.sTextDim)
            TextField(placeholder, text: text)
                .textFieldStyle(.roundedBorder)
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
