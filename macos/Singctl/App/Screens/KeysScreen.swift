// KeysScreen.swift
//
// CRUD over the loaded server keys, via `Backend` (`DaemonBackend`'s
// KEYS-GET/-ADD/-ADD-CONFIG/-REMOVE/-RENAME verbs in the Developer-ID build,
// `TunnelBackend`'s App-Group `config.json` in the App Store build). A key
// is either:
//   - a share link the daemon's internal/link package accepts — `vless://`,
//     `vmess://`, `trojan://`, `ss://`, `hysteria2://` (`hy2://`),
//     `hysteria://` (`hy://`), `tuic://` or `anytls://` — with the protocol
//     always derived from the link's own scheme, never assumed; or
//   - a WireGuard config (`[Interface]`/`[Peer]` INI text), the one key kind
//     with no scheme to dispatch on. That's why "Add key" below is a
//     Link/Config mode picker rather than a protocol picker: link protocols
//     stay auto-detected, WireGuard is the one place an explicit mode is
//     needed at all (see docs/protocol-modules.md).
// `keysGet()` returns the raw keys (masking is a UI concern, not a wire
// concept — see ControlClient.keysGet's doc comment), so this screen derives
// display name, masked form, and protocol label locally for either kind.

import SwiftUI
import AppKit
import UniformTypeIdentifiers

struct KeysScreen: View {
    @Environment(\.backend) private var backend

    /// One row derived from a raw key returned by KEYS-GET. Either a share
    /// link — `vless://`, `vmess://`, `trojan://`, `ss://`, `hysteria2://`
    /// (`hy2://`), `hysteria://` (`hy://`), `tuic://` or `anytls://`, scheme
    /// always read from the link itself, never assumed — or a WireGuard INI
    /// config, which has no scheme at all.
    private struct KeyRow: Identifiable {
        let index: Int
        let name: String
        let masked: String
        let protocolLabel: String
        /// True when this link appears in some subscription's `links` — the
        /// subscription owns it, so rename/delete are refused (see the
        /// Subscriptions section below).
        let isSubscriptionOwned: Bool
        /// True for a WireGuard config key. An INI file has no field to
        /// hold a display name, and the daemon refuses KEYS-RENAME for one —
        /// so Rename is disabled for these rows too (delete is still fine).
        let isWireGuard: Bool

        var id: Int { index }
    }

    /// "Add key" input mode: a share link (auto-detected protocol) or a
    /// pasted/imported WireGuard config. See the file-level doc comment.
    private enum KeyInputMode: Hashable {
        case link, config
    }

    @State private var keys: [KeyRow] = []
    @State private var keySearch = ""
    @State private var isLoading = false
    @State private var isMutating = false
    @State private var errorMessage: String?

    @State private var inputMode: KeyInputMode = .link
    @State private var newLink = ""
    @State private var newConfig = ""
    @State private var isImportingConfigFile = false

    @State private var renameTarget: KeyRow?
    @State private var renameText = ""

    @State private var deleteTarget: KeyRow?

    // MARK: - Subscriptions state

    @State private var subscriptions: [Subscription] = []
    @State private var isSubMutating = false
    @State private var subErrorMessage: String?
    @State private var subStatusMessage: String?

    @State private var newSubURL = ""

    @State private var removeSubTarget: Subscription?

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Keys")

            if let errorMessage {
                Text(errorMessage)
                    .font(.appSecondary)
                    .foregroundStyle(Color.sDanger)
            }

            addCard
            subscriptionsCard
            loadedCard
        }
        .padding(Spacing.lg)
        .searchable(text: $keySearch, prompt: "Search keys")
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
        .confirmationDialog(
            removeSubTarget.map { "Remove subscription \"\($0.label)\"?" } ?? "Remove subscription?",
            isPresented: removeSubBinding,
            titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) { performRemoveSubscription() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("This also removes every server this subscription owns.")
        }
    }

    // MARK: - Add key

    private var addCard: some View {
        Card(title: "Add key") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SegmentedControl(
                    options: [
                        .init(KeyInputMode.link, "Link"),
                        .init(KeyInputMode.config, "Config"),
                    ],
                    selection: $inputMode,
                    disabled: isMutating
                )
                switch inputMode {
                case .link:
                    linkInput
                case .config:
                    configInput
                }
            }
        }
    }

    /// The default mode: a single-line share link, unchanged from before the
    /// Config mode existed.
    private var linkInput: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            HStack(spacing: Spacing.sm) {
                TextField("Paste a key link…", text: $newLink)
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
            Text("Supported: vless://, vmess://, trojan://, ss://, hysteria2:// (hy2://), hysteria:// (hy://), tuic://, anytls://")
                .font(.appSecondary)
                .foregroundStyle(Color.sTextFaint)
            Text("Multiple keys form an automatic latency-tested failover group (priority follows order).")
                .font(.appSecondary)
                .foregroundStyle(Color.sTextFaint)
        }
    }

    /// The WireGuard mode: a multi-line INI editor plus a file importer.
    /// There is deliberately no protocol dropdown here — WireGuard is the
    /// only protocol this mode adds.
    private var configInput: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            TextEditor(text: $newConfig)
                .font(.system(size: 16, design: .monospaced))
                .scrollContentBackground(.hidden)
                .frame(minHeight: 120, maxHeight: 220)
                .disabled(isMutating)
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
                    isLoading: isImportingConfigFile, disabled: isMutating
                ) {
                    importConfigFile()
                }
                Spacer()
                AppButton(
                    "Add", icon: "plus", isLoading: isMutating,
                    disabled: newConfig.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                ) {
                    addConfigKey()
                }
            }
            Text("For WireGuard configs ([Interface] / [Peer]) — paste one or import a .conf file. Link protocols (vless://, vmess://, …) are auto-detected in Link mode above; there's no protocol picker here on purpose.")
                .font(.appSecondary)
                .foregroundStyle(Color.sTextFaint)
        }
    }

    // MARK: - Subscriptions

    private var subscriptionsCard: some View {
        Card(title: "Subscriptions") {
            HStack(spacing: Spacing.sm) {
                Badge(text: "\(subscriptions.count)", tone: .accent)
                AppButton(
                    "Update all", kind: .ghost, icon: "arrow.clockwise",
                    isLoading: isSubMutating, disabled: subscriptions.isEmpty
                ) {
                    updateAllSubscriptions()
                }
            }
        } content: {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                HStack(spacing: Spacing.sm) {
                    TextField("Paste a subscription URL…", text: $newSubURL)
                        .textFieldStyle(.roundedBorder)
                        .disabled(isSubMutating)
                        .onSubmit { addSubscription() }
                    AppButton(
                        "Add", icon: "plus", isLoading: isSubMutating,
                        disabled: newSubURL.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                    ) {
                        addSubscription()
                    }
                }
                if let subErrorMessage {
                    Text(subErrorMessage)
                        .font(.appSecondary)
                        .foregroundStyle(Color.sDanger)
                }
                if let subStatusMessage {
                    Text(subStatusMessage)
                        .font(.appSecondary)
                        .foregroundStyle(Color.sTextFaint)
                }
                Text("A subscription's servers refresh automatically and can't be renamed or deleted individually — remove the whole subscription instead.")
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextFaint)

                if subscriptions.isEmpty {
                    EmptyState(text: "No subscriptions added.", symbol: "link")
                } else {
                    VStack(alignment: .leading, spacing: Spacing.sm) {
                        ForEach(subscriptions) { subscription in
                            subscriptionRow(subscription)
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private func subscriptionRow(_ subscription: Subscription) -> some View {
        VStack(alignment: .leading, spacing: Spacing.xs) {
            HStack(alignment: .firstTextBaseline, spacing: Spacing.sm) {
                Text(subscription.label)
                    .font(.appBody)
                    .foregroundStyle(Color.sText)
                Badge(text: "\(subscription.links.count) servers", tone: .dim)
                Spacer()
                Text(Self.relativeUpdateLabel(subscription.lastUpdateDate))
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextFaint)
                AppButton("Remove", kind: .danger, disabled: isSubMutating) {
                    removeSubTarget = subscription
                }
            }
            if !subscription.lastError.isEmpty {
                Text("Last refresh failed: \(subscription.lastError)")
                    .font(.appSecondary)
                    .foregroundStyle(Color.sDanger)
            }
            if let usageLine = Self.usageLine(subscription.meta) {
                Text(usageLine)
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextDim)
            }
        }
        .padding(.vertical, Spacing.xs)
    }

    // MARK: - Loaded keys

    private var loadedCard: some View {
        Card(title: "Loaded keys") {
            HStack(spacing: Spacing.sm) {
                Badge(text: "\(filteredKeys.count)", tone: .accent)
                AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: isLoading, disabled: isMutating) {
                    Task { await load() }
                }
                .keyboardShortcut("r", modifiers: .command)
            }
        } content: {
            if filteredKeys.isEmpty {
                EmptyState(
                    text: isLoading ? "Loading keys…" : (keySearch.isEmpty ? "No keys loaded." : "No keys match your search."),
                    symbol: keySearch.isEmpty ? "key" : "magnifyingglass"
                )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    ForEach(filteredKeys) { row in
                        HStack(spacing: Spacing.md) {
                            Text("\(row.index + 1)")
                                .font(.appSecondary)
                                .foregroundStyle(Color.sTextDim)
                                .frame(width: 24, alignment: .leading)
                            HStack(spacing: Spacing.sm) {
                                // The name is arbitrary-length and freely
                                // truncates; the badges are short, fixed
                                // vocabulary ("VLESS", "Hysteria2", …) and
                                // must never be compressed to fit — fixedSize
                                // pins each badge to its own intrinsic width
                                // so the HStack takes the shortfall out of
                                // the name instead (see the file-level bug
                                // this fixes: badges rendering as "VL..").
                                Text(row.name)
                                    .font(.appBody)
                                    .foregroundStyle(Color.sText)
                                    .lineLimit(1)
                                    .truncationMode(.tail)
                                    .frame(minWidth: 60, alignment: .leading)
                                Badge(text: row.protocolLabel, tone: .dim)
                                    .fixedSize()
                                if row.isSubscriptionOwned {
                                    Badge(text: "Subscription", tone: .accent)
                                        .fixedSize()
                                }
                            }
                            Text(row.masked)
                                .font(.system(size: 16, design: .monospaced))
                                .foregroundStyle(Color.sTextDim)
                                .lineLimit(1)
                            Spacer()
                            HStack(spacing: Spacing.xs) {
                                AppButton(
                                    "Rename", kind: .ghost,
                                    disabled: isMutating || row.isSubscriptionOwned || row.isWireGuard
                                ) {
                                    renameTarget = row
                                    renameText = row.name
                                }
                                .help(Self.renameHelp(for: row))
                                AppButton("Delete", kind: .danger, disabled: isMutating || row.isSubscriptionOwned) {
                                    deleteTarget = row
                                }
                                .help(row.isSubscriptionOwned ? Self.subscriptionOwnedHelp : "")
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
        .frame(maxHeight: .infinity)
    }

    private var filteredKeys: [KeyRow] {
        let query = keySearch.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !query.isEmpty else { return keys }
        return keys.filter {
            $0.name.localizedCaseInsensitiveContains(query)
                || $0.protocolLabel.localizedCaseInsensitiveContains(query)
                || $0.masked.localizedCaseInsensitiveContains(query)
        }
    }

    // MARK: - Presentation bindings

    private var renameBinding: Binding<Bool> {
        Binding(get: { renameTarget != nil }, set: { if !$0 { renameTarget = nil } })
    }

    private var deleteBinding: Binding<Bool> {
        Binding(get: { deleteTarget != nil }, set: { if !$0 { deleteTarget = nil } })
    }

    private var removeSubBinding: Binding<Bool> {
        Binding(get: { removeSubTarget != nil }, set: { if !$0 { removeSubTarget = nil } })
    }

    // MARK: - Actions

    /// Loads both KEYS-GET and SUB-LIST, then marks each key row owned by a
    /// subscription when its link appears in some subscription's `links` —
    /// both need to be current for that marking to be correct, so this is
    /// the single load path both the initial `.task` and every mutation
    /// (key or subscription) below call afterward.
    private func load() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let links = try await backend.keysGet()
            let subs = try await backend.subList()
            let ownedLinks = Set(subs.flatMap(\.links))
            keys = links.enumerated().map { index, link in
                KeyRow(
                    index: index,
                    name: Self.deriveName(link, index: index),
                    masked: Self.maskKey(link),
                    protocolLabel: Self.protocolLabel(for: link),
                    isSubscriptionOwned: ownedLinks.contains(link),
                    isWireGuard: Self.isWireGuardConfig(link)
                )
            }
            subscriptions = subs
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

    /// Adds a WireGuard key from the Config editor's text, via
    /// `Backend.keysAddConfig` (KEYS-ADD-CONFIG, not KEYS-ADD — see that
    /// method's doc comment for why a WireGuard config needs its own verb).
    private func addConfigKey() {
        let config = newConfig.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !config.isEmpty else { return }
        isMutating = true
        errorMessage = nil
        Task {
            defer { isMutating = false }
            do {
                try await backend.keysAddConfig(config)
                newConfig = ""
                await load()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }

    /// Opens an `NSOpenPanel` restricted to `.conf`/`.txt`/plain text and
    /// loads the picked file's contents into the Config editor. Reading is
    /// done off the main thread; a failure surfaces inline via
    /// `errorMessage`, the same as an add-key failure.
    private func importConfigFile() {
        let panel = NSOpenPanel()
        panel.title = "Import WireGuard config"
        panel.canChooseFiles = true
        panel.canChooseDirectories = false
        panel.allowsMultipleSelection = false
        var allowedTypes: [UTType] = [.plainText]
        if let confType = UTType(filenameExtension: "conf") { allowedTypes.append(confType) }
        if let txtType = UTType(filenameExtension: "txt") { allowedTypes.append(txtType) }
        panel.allowedContentTypes = allowedTypes
        guard panel.runModal() == .OK, let url = panel.url else { return }

        isImportingConfigFile = true
        errorMessage = nil
        Task {
            defer { isImportingConfigFile = false }
            do {
                let text = try await Task.detached(priority: .userInitiated) {
                    try String(contentsOf: url, encoding: .utf8)
                }.value
                newConfig = text
            } catch {
                errorMessage = "Couldn't read \(url.lastPathComponent): \(error.localizedDescription)"
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

    // MARK: - Subscription actions

    private func addSubscription() {
        let url = newSubURL.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !url.isEmpty else { return }
        isSubMutating = true
        subErrorMessage = nil
        subStatusMessage = nil
        Task {
            defer { isSubMutating = false }
            do {
                try await backend.subAdd(url)
                newSubURL = ""
                await load()
            } catch {
                subErrorMessage = error.localizedDescription
            }
        }
    }

    private func performRemoveSubscription() {
        guard let target = removeSubTarget else { return }
        removeSubTarget = nil
        isSubMutating = true
        subErrorMessage = nil
        subStatusMessage = nil
        Task {
            defer { isSubMutating = false }
            do {
                try await backend.subRemove(target.url)
                await load()
            } catch {
                subErrorMessage = error.localizedDescription
            }
        }
    }

    private func updateAllSubscriptions() {
        isSubMutating = true
        subErrorMessage = nil
        subStatusMessage = nil
        Task {
            defer { isSubMutating = false }
            do {
                let changed = try await backend.subUpdate()
                await load()
                subStatusMessage = changed == 0
                    ? "No subscriptions changed."
                    : "\(changed) subscription\(changed == 1 ? "" : "s") updated."
            } catch {
                subErrorMessage = error.localizedDescription
            }
        }
    }

    // MARK: - Key parsing

    /// True when `key` is WireGuard INI config text rather than a
    /// `scheme://` share link — detected by the presence of an
    /// `[Interface]` section header, mirroring how the daemon itself tells
    /// the two apart (no scheme to dispatch on, so content is all there is).
    private static func isWireGuardConfig(_ key: String) -> Bool {
        key.contains("[Interface]")
    }

    /// Derives a display name from a link's `#fragment`, or falls back to
    /// "Key N" (1-based) when the fragment is absent/empty. Skipped
    /// entirely for a WireGuard config: a `#` there is an INI comment
    /// marker, not a fragment, and taking "everything after the last `#`"
    /// would grab a stray comment line instead of a name.
    private static func deriveName(_ key: String, index: Int) -> String {
        if !isWireGuardConfig(key), let hashIndex = key.lastIndex(of: "#") {
            let name = String(key[key.index(after: hashIndex)...])
            if !name.isEmpty { return name }
        }
        return "Key \(index + 1)"
    }

    /// Masks a raw key for display. A share link keeps its `scheme://`
    /// prefix and `@host:port` tail (before any `#fragment`/`?query`) but
    /// hides the credential/UUID — the scheme is read from the link itself,
    /// never assumed to be `vless://`, so an `ss://`/`hysteria2://`/… key is
    /// never mislabeled, and a link with no recognisable scheme masks with
    /// no prefix at all rather than guessing. A WireGuard config has
    /// neither a scheme nor an `@host:port` to derive, so instead this
    /// shows its `Address =` line (the one non-secret INI field that
    /// identifies "which client is this"), or a fixed placeholder when even
    /// that's absent — never a fabricated `scheme://…@host:port` shape.
    private static func maskKey(_ key: String) -> String {
        if isWireGuardConfig(key) {
            return wireGuardAddress(key) ?? "WireGuard config"
        }
        let prefix = scheme(of: key).map { $0 + "://" } ?? ""
        guard let atIndex = key.firstIndex(of: "@") else {
            return prefix + "••••"
        }
        var tail = String(key[atIndex...])
        if let hashIndex = tail.firstIndex(of: "#") {
            tail = String(tail[..<hashIndex])
        }
        if let queryIndex = tail.firstIndex(of: "?") {
            tail = String(tail[..<queryIndex])
        }
        return prefix + "••••" + tail
    }

    /// Extracts the trimmed `Address = …` value from a WireGuard config's
    /// `[Interface]` section (first match only), or `nil` when absent.
    private static func wireGuardAddress(_ config: String) -> String? {
        for line in config.split(separator: "\n") {
            let trimmedLine = line.trimmingCharacters(in: .whitespaces)
            guard let eqIndex = trimmedLine.firstIndex(of: "=") else { continue }
            let fieldName = trimmedLine[trimmedLine.startIndex..<eqIndex]
                .trimmingCharacters(in: .whitespaces)
            if fieldName.caseInsensitiveCompare("Address") == .orderedSame {
                return trimmedLine[trimmedLine.index(after: eqIndex)...]
                    .trimmingCharacters(in: .whitespaces)
            }
        }
        return nil
    }

    /// Extracts the literal scheme from a raw `scheme://…` link (the part
    /// before `://`), or `nil` when the link has none. Mirrors
    /// internal/link/parse.go's `ParseLink` dispatch
    /// (`strings.Cut(raw, "://")` with a non-empty scheme check) so the UI's
    /// notion of "what scheme is this" never drifts from the daemon's.
    private static func scheme(of link: String) -> String? {
        guard let range = link.range(of: "://"), range.lowerBound != link.startIndex else {
            return nil
        }
        return String(link[link.startIndex..<range.lowerBound])
    }

    /// Badge text for a key row: "WireGuard" for a config key, the protocol
    /// name understood from a link's scheme, or "Unknown" when the scheme is
    /// missing/unrecognised.
    private static func protocolLabel(for key: String) -> String {
        if isWireGuardConfig(key) { return KeyProtocol.displayName(forScheme: "wireguard") }
        guard let scheme = scheme(of: key) else { return "Unknown" }
        return KeyProtocol.displayName(forScheme: scheme)
    }

    // MARK: - Subscription formatting

    /// Tooltip shown on a subscription-owned key row's disabled Rename/Delete
    /// buttons.
    private static let subscriptionOwnedHelp =
        "This server is managed by a subscription — remove the subscription to remove it."

    /// Tooltip shown on a WireGuard key row's disabled Rename button.
    private static let wireGuardRenameHelp =
        "WireGuard keys can't be renamed — a config file has nowhere to put a display name."

    /// Picks the Rename button's tooltip for a row: subscription ownership
    /// takes precedence (it also disables Delete, so it's the more relevant
    /// explanation), then WireGuard's own rename restriction, else empty.
    private static func renameHelp(for row: KeyRow) -> String {
        if row.isSubscriptionOwned { return subscriptionOwnedHelp }
        if row.isWireGuard { return wireGuardRenameHelp }
        return ""
    }

    private static let expiryFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateStyle = .medium
        f.timeStyle = .none
        return f
    }()

    /// "updated 5 min ago" / "never updated", via the system's relative-date
    /// formatter.
    private static func relativeUpdateLabel(_ date: Date?) -> String {
        guard let date else { return "never updated" }
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .short
        return "updated " + formatter.localizedString(for: date, relativeTo: Date())
    }

    /// "12.3 MB of 50.0 GB used · expires Jan 1, 2027", omitting whichever
    /// half the panel didn't report, or `nil` entirely when neither was.
    private static func usageLine(_ meta: SubscriptionMeta) -> String? {
        var parts: [String] = []
        if meta.hasUsage {
            if meta.total > 0 {
                parts.append("\(ByteFormat.bytes(meta.used)) of \(ByteFormat.bytes(meta.total)) used")
            } else {
                parts.append("\(ByteFormat.bytes(meta.used)) used")
            }
        }
        if let expireDate = meta.expireDate {
            parts.append("expires \(expiryFormatter.string(from: expireDate))")
        }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }
}

// MARK: - KeyProtocol

/// Maps a share-link scheme (case-insensitively, aliases included) to the
/// display name shown in the protocol badge. Mirrors the scheme dispatch in
/// internal/link/parse.go's `ParseLink` exactly — every scheme it accepts
/// has an entry here, plus a total "Unknown" fallback for anything else.
/// "wireguard" is a synthetic case: a WireGuard key has no real scheme (see
/// KeysScreen.isWireGuardConfig), `protocolLabel(for:)` just feeds this
/// literal in for that case so the label lookup stays in one place.
private enum KeyProtocol {
    static func displayName(forScheme scheme: String) -> String {
        switch scheme.lowercased() {
        case "vless": return "VLESS"
        case "vmess": return "VMess"
        case "trojan": return "Trojan"
        case "ss": return "Shadowsocks"
        case "hysteria2", "hy2": return "Hysteria2"
        case "hysteria", "hy": return "Hysteria"
        case "tuic": return "TUIC"
        case "anytls": return "AnyTLS"
        case "wireguard", "wg": return "WireGuard"
        default: return "Unknown"
        }
    }
}
