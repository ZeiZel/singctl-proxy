// SysProxyScreen.swift
//
// The macOS system proxy (a generated PAC + `networksetup`) — previously only
// reachable via `make proxy-on`/`proxy-pac`/`proxy-off` in a terminal, now
// applied by the daemon (root) through the SYSPROXY-* control verbs (see
// Backend.swift's doc comment on that MARK section). Three explicit modes —
// Off / Exclude / Include, mirroring proxy-off / proxy-on / proxy-pac — plus
// a domain-list editor with Import (paste or file).
//
// SAFETY: opening this screen only READS state (`sysProxyStatus`/
// `sysProxyConfig`, in the initial `.task` below). Nothing is ever applied to
// the machine's network configuration without an explicit "Apply mode" or
// "Import" click — this toggle affects the user's whole machine, so the app
// silently changing it on tab-open would be a serious misbehaviour. This is
// why the mode picker below is bound to a LOCAL `selectedMode`, unlike
// DashboardScreen's mode switch (which applies immediately on selection) —
// see `modeCard`.
//
// App Store build: `TunnelBackend` throws "unsupported" for all four SYSPROXY-*
// verbs (no root daemon, no `networksetup` access in the sandbox) rather than
// faking success — this screen still renders (same pattern as ProxiesScreen
// for PROXY-GROUP/-SELECT) and simply surfaces that error inline.

import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct SysProxyScreen: View {
    @Environment(\.backend) private var backend

    // MARK: - Status (read-only)

    @State private var status: SysProxyStatus = .empty
    @State private var isLoadingStatus = true
    @State private var statusError: String?

    // MARK: - Current config (read-only, for display/copy)

    @State private var currentConfigText = ""
    @State private var isLoadingConfig = false
    @State private var configError: String?

    // MARK: - Mode (mutating — requires an explicit "Apply mode" click)

    private let modeOptions: [SegmentedOption<String>] = [
        SegmentedOption("off", "Off"),
        SegmentedOption("exclude", "Exclude"),
        SegmentedOption("include", "Include"),
    ]
    /// Deliberately NOT bound straight to `status.mode` — see the file-level
    /// safety note. Seeded from `status.mode` once `loadStatus()` returns, and
    /// re-seeded after a successful apply.
    @State private var selectedMode = "off"
    @State private var isApplyingMode = false
    @State private var applyError: String?
    @State private var applyMessage: String?

    // MARK: - Import (mutating — requires an explicit "Import" click)

    @State private var importText = ""
    @State private var isImporting = false
    /// Separate from `isImporting`: this covers only the "Import file…"
    /// picker's read (loads text into the editor), not the "Import" button's
    /// SYSPROXY-IMPORT send — the two can't overlap in practice but keeping
    /// distinct flags avoids one action's spinner/disabled state leaking
    /// into the other's button.
    @State private var isImportingFile = false
    @State private var importError: String?
    @State private var importMessage: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "System proxy")
                statusCard
                modeCard
                importCard
                configCard
            }
            .padding(Spacing.lg)
        }
        .task {
            // Read-only on open: SYSPROXY-STATUS + SYSPROXY-CONFIG only.
            // SYSPROXY-SET/-IMPORT are never called from here.
            await loadStatus()
            await loadConfig()
        }
    }

    // MARK: - Status

    private var statusCard: some View {
        Card(title: "Current state") {
            AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: isLoadingStatus) {
                Task { await loadStatus() }
            }
            .keyboardShortcut("r", modifiers: .command)
        } content: {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let statusError {
                    Text(statusError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                Grid(alignment: .topLeading, horizontalSpacing: Spacing.xl, verticalSpacing: Spacing.md) {
                    GridRow {
                        StatTile(label: "Mode", value: Self.modeLabel(status.mode))
                        StatTile(label: "Network service", value: status.service.isEmpty ? "—" : status.service)
                    }
                    GridRow {
                        StatTile(
                            label: "PAC server",
                            value: status.pacServerUp ? "Up" : "Down",
                            tone: status.pacServerUp ? .ok : .danger
                        )
                        StatTile(label: "Domains", value: "\(status.domains)")
                    }
                }
                if !status.pacURL.isEmpty {
                    Text(status.pacURL)
                        .font(.system(size: 16, design: .monospaced))
                        .foregroundStyle(Color.sTextDim)
                        .textSelection(.enabled)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: - Mode

    private var modeCard: some View {
        Card(title: "Mode") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SegmentedControl(options: modeOptions, selection: $selectedMode, disabled: isApplyingMode)
                Text(
                    "Off disables the system proxy. Exclude routes everything except " +
                    "corporate/Russian/local traffic through singctl. Include routes only the " +
                    "domain list below. Selecting a mode does not apply it — click Apply mode."
                )
                .font(.appSecondary)
                .foregroundStyle(Color.sTextFaint)
                HStack(spacing: Spacing.sm) {
                    AppButton("Apply mode", icon: "checkmark.circle", isLoading: isApplyingMode) {
                        applyMode()
                    }
                    if let applyError {
                        Text(applyError).font(.appSecondary).foregroundStyle(Color.sDanger)
                    } else if let applyMessage {
                        Text(applyMessage).font(.appSecondary).foregroundStyle(Color.sOk)
                    }
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: - Import

    private var importCard: some View {
        Card(title: "Import domain list / config") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                TextEditor(text: $importText)
                    .font(.system(size: 16, design: .monospaced))
                    .scrollContentBackground(.hidden)
                    .frame(minHeight: 120, maxHeight: 220)
                    .disabled(isImporting)
                    .padding(Spacing.xs)
                    .background(Color.sBgSoft)
                    .clipShape(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                            .strokeBorder(Color.sBorder, lineWidth: 1)
                    )
                HStack(spacing: Spacing.sm) {
                    AppButton(
                        "Import file…", kind: .ghost, icon: "doc.badge.plus",
                        isLoading: isImportingFile, disabled: isImporting
                    ) {
                        importFile()
                    }
                    Spacer()
                    AppButton(
                        "Import", icon: "square.and.arrow.down", isLoading: isImporting,
                        disabled: importText.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    ) {
                        importPasted()
                    }
                }
                if let importError {
                    Text(importError).font(.appSecondary).foregroundStyle(Color.sDanger)
                } else if let importMessage {
                    Text(importMessage).font(.appSecondary).foregroundStyle(Color.sOk)
                }
                Text(
                    "Paste an INI rules file ([settings]/[proxy]/[direct]), a YAML config, " +
                    "or a plain newline-separated domain list. " +
                    "\"Import file…\" only loads a file into this box — nothing is sent to the " +
                    "daemon until you click Import."
                )
                .font(.appSecondary)
                .foregroundStyle(Color.sTextFaint)
            }
        }
    }

    // MARK: - Current config

    private var configCard: some View {
        Card(title: "Applied config") {
            HStack(spacing: Spacing.xs) {
                AppButton("Copy", kind: .ghost, icon: "doc.on.doc", disabled: currentConfigText.isEmpty) {
                    copyConfig()
                }
                AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: isLoadingConfig) {
                    Task { await loadConfig() }
                }
            }
        } content: {
            if let configError {
                Text(configError).font(.appSecondary).foregroundStyle(Color.sDanger)
            } else if currentConfigText.isEmpty {
                EmptyState(text: isLoadingConfig ? "Loading config…" : "No config applied.", symbol: "doc.text")
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                ScrollView {
                    Text(currentConfigText)
                        .font(.system(size: 16, design: .monospaced))
                        .foregroundStyle(Color.sTextDim)
                        .textSelection(.enabled)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                .frame(maxHeight: 220)
            }
        }
    }

    // MARK: - Status / config actions (read-only)

    private func loadStatus() async {
        isLoadingStatus = true
        defer { isLoadingStatus = false }
        do {
            let fetched = try await backend.sysProxyStatus()
            status = fetched
            selectedMode = fetched.mode.isEmpty ? "off" : fetched.mode
            statusError = nil
        } catch {
            statusError = error.localizedDescription
        }
    }

    private func loadConfig() async {
        isLoadingConfig = true
        defer { isLoadingConfig = false }
        do {
            currentConfigText = try await backend.sysProxyConfig()
            configError = nil
        } catch {
            configError = error.localizedDescription
        }
    }

    // MARK: - Mode action (mutating)

    /// The one place SYSPROXY-SET is ever sent — only from an explicit
    /// "Apply mode" click (see the file-level safety note).
    private func applyMode() {
        isApplyingMode = true
        applyError = nil
        applyMessage = nil
        Task {
            defer { isApplyingMode = false }
            do {
                try await backend.sysProxySet(mode: selectedMode)
                applyMessage = "Applied."
                await loadStatus()
            } catch {
                applyError = error.localizedDescription
            }
        }
    }

    // MARK: - Import actions (mutating)

    /// Sends the pasted/loaded editor text via SYSPROXY-IMPORT — only from an
    /// explicit "Import" click.
    private func importPasted() {
        let text = importText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        isImporting = true
        importError = nil
        importMessage = nil
        Task {
            defer { isImporting = false }
            do {
                try await backend.sysProxyImport(text)
                importMessage = "Imported."
                importText = ""
                await loadStatus()
                await loadConfig()
            } catch {
                importError = error.localizedDescription
            }
        }
    }

    /// Opens an `NSOpenPanel` for INI/YAML/text rules files and loads the picked
    /// file's contents into the import editor — mirrors KeysScreen's
    /// `importConfigFile()` (WireGuard Config mode) exactly, including that
    /// this only loads text for review; the explicit "Import" button above
    /// is what actually sends SYSPROXY-IMPORT.
    private func importFile() {
        let panel = NSOpenPanel()
        panel.title = "Import system proxy config"
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        // INI is the documented rules format singctl ships
        // (packaging/macos/singctl-proxy-rules.ini), so it must be selectable —
        // .plainText alone does NOT cover a .ini file, and the panel would grey
        // the shipped rules file out.
        var allowedTypes: [UTType] = [.plainText, .text, .data]
        if let iniType = UTType(filenameExtension: "ini") { allowedTypes.append(iniType) }
        if let confType = UTType(filenameExtension: "conf") { allowedTypes.append(confType) }
        if let yamlType = UTType(filenameExtension: "yaml") { allowedTypes.append(yamlType) }
        if let ymlType = UTType(filenameExtension: "yml") { allowedTypes.append(ymlType) }
        if let txtType = UTType(filenameExtension: "txt") { allowedTypes.append(txtType) }
        panel.allowedContentTypes = allowedTypes
        guard panel.runModal() == .OK, let url = panel.url else { return }

        isImportingFile = true
        importError = nil
        importMessage = nil
        Task {
            defer { isImportingFile = false }
            do {
                let text = try await Task.detached(priority: .userInitiated) {
                    try String(contentsOf: url, encoding: .utf8)
                }.value
                importText = text
            } catch {
                importError = "Couldn't read \(url.lastPathComponent): \(error.localizedDescription)"
            }
        }
    }

    private func copyConfig() {
        let pasteboard = NSPasteboard.general
        pasteboard.clearContents()
        pasteboard.setString(currentConfigText, forType: .string)
    }

    // MARK: - Formatting

    private static func modeLabel(_ mode: String) -> String {
        switch mode {
        case "off": return "Off"
        case "exclude": return "Exclude"
        case "include": return "Include"
        default: return mode.isEmpty ? "—" : mode
        }
    }
}
