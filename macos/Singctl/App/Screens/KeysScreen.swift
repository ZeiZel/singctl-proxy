// KeysScreen.swift
//
// Mirrors gui/frontend's Keys page (KeysManager): CRUD over the loaded VLESS
// keys, via `Backend` (`DaemonBackend`'s KEYS-GET/-ADD/-REMOVE/-RENAME verbs
// in the Developer-ID build, `TunnelBackend`'s App-Group `config.json` in the
// App Store build). `keysGet()` returns raw `vless://` links (masking is a UI
// concern, not a wire concept — see ControlClient.keysGet's doc comment), so
// this screen derives display name + masked form locally, mirroring
// gui/bridge/types.go's maskKey exactly.

import SwiftUI

struct KeysScreen: View {
    @Environment(\.backend) private var backend

    /// One row derived from a raw `vless://` link returned by KEYS-GET.
    private struct KeyRow: Identifiable {
        let index: Int
        let name: String
        let masked: String

        var id: Int { index }
    }

    @State private var keys: [KeyRow] = []
    @State private var isLoading = false
    @State private var isMutating = false
    @State private var errorMessage: String?

    @State private var newLink = ""

    @State private var renameTarget: KeyRow?
    @State private var renameText = ""

    @State private var deleteTarget: KeyRow?

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Keys")

            if let errorMessage {
                Text(errorMessage)
                    .font(.subheadline)
                    .foregroundStyle(Color.sDanger)
            }

            addCard
            loadedCard
        }
        .padding(Spacing.lg)
        .task { await load() }
        .alert("Rename key", isPresented: renameBinding) {
            TextField("Name", text: $renameText)
            Button("Cancel", role: .cancel) {}
            Button("Save") { performRename() }
        } message: {
            Text("Enter a new name for this key.")
        }
        .confirmationDialog(
            deleteTarget.map { "Delete key \"\($0.name)\"?" } ?? "Delete key?",
            isPresented: deleteBinding,
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) { performDelete() }
            Button("Cancel", role: .cancel) {}
        }
    }

    // MARK: - Add key

    private var addCard: some View {
        Card(title: "Add key") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                HStack(spacing: Spacing.sm) {
                    TextField("vless://…", text: $newLink)
                        .textFieldStyle(.roundedBorder)
                        .disabled(isMutating)
                        .onSubmit { addKey() }
                    AppButton(
                        "Add", icon: "plus", isLoading: isMutating,
                        disabled: newLink.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    ) {
                        addKey()
                    }
                }
                Text("Multiple keys form an automatic latency-tested failover group (priority follows order).")
                    .font(.callout)
                    .foregroundStyle(Color.sTextFaint)
            }
        }
    }

    // MARK: - Loaded keys

    private var loadedCard: some View {
        Card(title: "Loaded keys") {
            Badge(text: "\(keys.count)", tone: .accent)
        } content: {
            if keys.isEmpty {
                EmptyState(text: isLoading ? "Loading keys…" : "No keys loaded.", symbol: "key")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                Table(keys) {
                    TableColumn("#") { row in
                        Text("\(row.index + 1)")
                            .font(.body)
                            .foregroundStyle(Color.sTextDim)
                    }
                    .width(32)

                    TableColumn("Name") { row in
                        Text(row.name)
                            .font(.body)
                            .foregroundStyle(Color.sText)
                    }
                    .width(min: 100, ideal: 160)

                    TableColumn("Key") { row in
                        Text(row.masked)
                            .font(.system(.callout, design: .monospaced))
                            .foregroundStyle(Color.sTextDim)
                    }

                    TableColumn("") { row in
                        HStack(spacing: Spacing.xs) {
                            AppButton("Rename", kind: .ghost, disabled: isMutating) {
                                renameTarget = row
                                renameText = row.name
                            }
                            AppButton("Delete", kind: .danger, disabled: isMutating) {
                                deleteTarget = row
                            }
                        }
                    }
                    .width(min: 160, ideal: 180)
                }
                .controlSize(.large)
            }
        }
        .frame(maxHeight: .infinity)
    }

    // MARK: - Presentation bindings

    private var renameBinding: Binding<Bool> {
        Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })
    }

    private var deleteBinding: Binding<Bool> {
        Binding(get: { deleteTarget != nil }, set: { if !$0 { deleteTarget = nil } })
    }

    // MARK: - Actions

    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let links = try await backend.keysGet()
            keys = links.enumerated().map { index, link in
                KeyRow(index: index, name: Self.deriveName(link, index: index), masked: Self.maskKey(link))
            }
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    private func addKey() {
        let link = newLink.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !link.isEmpty else { return }
        isMutating = true
        errorMessage = nil
        Task {
            defer { isMutating = false }
            do {
                try await backend.keysAdd(link)
                newLink = ""
                await load()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func performRename() {
        guard let target = renameTarget else { return }
        renameTarget = nil
        let name = renameText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else { return }
        isMutating = true
        errorMessage = nil
        Task {
            defer { isMutating = false }
            do {
                try await backend.keysRename(target.index, name)
                await load()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    private func performDelete() {
        guard let target = deleteTarget else { return }
        deleteTarget = nil
        isMutating = true
        errorMessage = nil
        Task {
            defer { isMutating = false }
            do {
                try await backend.keysRemove(target.index)
                await load()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    // MARK: - Key parsing (mirrors gui/bridge/types.go's maskKey)

    /// Derives a display name from a VLESS link's `#fragment`, or falls back
    /// to "Key N" (1-based) when the fragment is absent/empty.
    private static func deriveName(_ link: String, index: Int) -> String {
        if let hashIndex = link.lastIndex(of: "#") {
            let name = String(link[link.index(after: hashIndex)...])
            if !name.isEmpty { return name }
        }
        return "Key \(index + 1)"
    }

    /// Masks a raw `vless://` link, keeping the `@host:port` tail (before any
    /// `#fragment`/`?query`) but hiding the credential/UUID.
    private static func maskKey(_ link: String) -> String {
        guard let atIndex = link.firstIndex(of: "@") else {
            return "vless://••••"
        }
        var tail = String(link[atIndex...])
        if let hashIndex = tail.firstIndex(of: "#") {
            tail = String(tail[..<hashIndex])
        }
        if let queryIndex = tail.firstIndex(of: "?") {
            tail = String(tail[..<queryIndex])
        }
        return "vless://••••" + tail
    }
}
