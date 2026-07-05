// LicenseScreen.swift
//
// License status + activation, driven entirely by `LicenseService` (which
// shells out to the `singctl` CLI — this is a profile-store concern, not a
// live-daemon one, so it does not touch `LiveStore`/`ControlClient`).

import SwiftUI
import AppKit

struct LicenseScreen: View {
    @State private var status: LicenseStatus?
    @State private var isLoadingStatus = true
    @State private var loadError: String?

    @State private var email: String = ""
    @State private var token: String = ""
    @State private var isActivating = false
    @State private var isRemoving = false
    @State private var actionMessage: String?
    @State private var actionIsError = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "License")
                statusCard
                activateCard
            }
            .padding(Spacing.lg)
        }
        .task { await loadStatus() }
    }

    // MARK: - Status card

    private var statusCard: some View {
        Card(title: "Status") {
            statusBadge
        } content: {
            if isLoadingStatus {
                HStack(spacing: Spacing.sm) {
                    ProgressView().controlSize(.small)
                    Text("Checking license…").font(.subheadline).foregroundStyle(Color.sTextDim)
                }
            } else if let loadError {
                Text(loadError).font(.subheadline).foregroundStyle(Color.sDanger)
            } else if let status {
                statusDetails(status)
            } else {
                EmptyState(text: "License status unavailable.")
            }
        }
    }

    @ViewBuilder
    private var statusBadge: some View {
        if let status {
            if status.dev {
                Badge(text: "Dev build", tone: .accent)
            } else if status.valid {
                Badge(text: "Licensed", tone: .ok)
            } else {
                Badge(text: status.reason.isEmpty ? "Unlicensed" : status.reason, tone: .danger)
            }
        }
    }

    @ViewBuilder
    private func statusDetails(_ status: LicenseStatus) -> some View {
        if status.valid {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                detailRow("Subject", status.subject.isEmpty ? "—" : status.subject)
                detailRow("Expires", expiryText(status))
                detailRow("Days left", status.daysLeft >= 0 ? "\(status.daysLeft)" : "—")
                if !status.features.isEmpty {
                    VStack(alignment: .leading, spacing: Spacing.xs) {
                        Text("FEATURES").font(.subheadline).foregroundStyle(Color.sTextDim)
                        HStack {
                            ForEach(status.features, id: \.self) { feature in
                                Badge(text: feature, tone: .accent)
                            }
                        }
                    }
                }
            }
        } else {
            Text(status.reason.isEmpty ? "No valid license." : status.reason)
                .font(.subheadline)
                .foregroundStyle(Color.sDanger)
        }
    }

    private func detailRow(_ label: String, _ value: String) -> some View {
        HStack {
            Text(label).font(.callout).foregroundStyle(Color.sTextDim)
            Spacer()
            Text(value).font(.subheadline.weight(.medium)).foregroundStyle(Color.sText)
        }
    }

    private func expiryText(_ status: LicenseStatus) -> String {
        guard status.expiresAt != 0 else { return "Never" }
        let date = Date(timeIntervalSince1970: TimeInterval(status.expiresAt))
        let formatter = DateFormatter()
        formatter.dateStyle = .medium
        formatter.timeStyle = .none
        return formatter.string(from: date)
    }

    // MARK: - Activate card

    private var activateCard: some View {
        Card(title: "Activate") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                VStack(alignment: .leading, spacing: Spacing.xs) {
                    Text("EMAIL").font(.subheadline).foregroundStyle(Color.sTextDim)
                    TextField("you@example.com", text: $email)
                        .textFieldStyle(.roundedBorder)
                        .controlSize(.large)
                        .disableAutocorrection(true)
                        .disabled(isBusy)
                }

                VStack(alignment: .leading, spacing: Spacing.xs) {
                    HStack {
                        Text("LICENSE TOKEN").font(.subheadline).foregroundStyle(Color.sTextDim)
                        Spacer()
                        AppButton("Import file…", kind: .ghost, icon: "doc.badge.plus", disabled: isBusy) {
                            importFile()
                        }
                    }
                    TextEditor(text: $token)
                        .font(.system(.body, design: .monospaced))
                        .frame(minHeight: 120)
                        .scrollContentBackground(.hidden)
                        .padding(Spacing.xs)
                        .background(Color.sBgSoft)
                        .clipShape(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                        .overlay(
                            RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                                .strokeBorder(Color.sBorder, lineWidth: 1)
                        )
                        .disabled(isBusy)
                }

                if let actionMessage {
                    Text(actionMessage)
                        .font(.callout)
                        .foregroundStyle(actionIsError ? Color.sDanger : Color.sOk)
                }

                HStack(spacing: Spacing.sm) {
                    AppButton(
                        "Activate", icon: "checkmark.seal",
                        isLoading: isActivating, disabled: !canActivate || isRemoving,
                        action: activate
                    )
                    AppButton(
                        "Remove", kind: .danger, icon: "trash",
                        isLoading: isRemoving, disabled: isActivating,
                        action: remove
                    )
                    Spacer()
                }
            }
        }
    }

    private var isBusy: Bool { isActivating || isRemoving }

    private var isEmailValid: Bool {
        email.range(of: #"^\S+@\S+\.\S+$"#, options: .regularExpression) != nil
    }

    private var canActivate: Bool {
        isEmailValid && !token.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !isActivating
    }

    // MARK: - Actions

    private func importFile() {
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = false
        panel.canChooseDirectories = false
        panel.canChooseFiles = true
        panel.prompt = "Import"
        guard panel.runModal() == .OK, let url = panel.url else { return }
        do {
            let text = try String(contentsOf: url, encoding: .utf8)
            token = text.trimmingCharacters(in: .whitespacesAndNewlines)
        } catch {
            actionIsError = true
            actionMessage = "Could not read file: \(error.localizedDescription)"
        }
    }

    private func activate() {
        isActivating = true
        actionMessage = nil
        let trimmedToken = token.trimmingCharacters(in: .whitespacesAndNewlines)
        let trimmedEmail = email.trimmingCharacters(in: .whitespacesAndNewlines)
        Task {
            defer { isActivating = false }
            do {
                _ = try await LicenseService.activate(token: trimmedToken, email: trimmedEmail)
                actionIsError = false
                actionMessage = "License activated."
                token = ""
                await loadStatus()
            } catch {
                actionIsError = true
                actionMessage = error.localizedDescription
            }
        }
    }

    private func remove() {
        isRemoving = true
        actionMessage = nil
        Task {
            defer { isRemoving = false }
            do {
                _ = try await LicenseService.remove()
                actionIsError = false
                actionMessage = "License removed."
                await loadStatus()
            } catch {
                actionIsError = true
                actionMessage = error.localizedDescription
            }
        }
    }

    private func loadStatus() async {
        isLoadingStatus = true
        defer { isLoadingStatus = false }
        do {
            status = try await LicenseService.status()
            loadError = nil
        } catch {
            loadError = error.localizedDescription
        }
    }
}
