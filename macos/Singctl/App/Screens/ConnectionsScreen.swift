// ConnectionsScreen.swift
//
// Live connection table + minimal firewall (F6 in docs/v2-spec.md). Talks to
// the daemon's CONNECTIONS/CONNECTION-CLOSE/FIREWALL-* control verbs through
// `Backend` (`@Environment(\.backend)`, same as KeysScreen/ProxiesScreen —
// see Backend.swift), rather than `ControlClient` directly: this screen is
// Dev-ID only today (excluded from the App Store target, project.yml), but
// routing through `Backend` keeps it consistent with every other kept screen
// and costs nothing.
//
// F6's core complaint was "No active connections" no matter why — the table
// was empty whether no mode was running, the Clash API was off, the API was
// unreachable, or the proxy was simply idle, and Dashboard's traffic hint
// could contradict what this screen showed. The daemon's CONNECTIONS verb
// now returns an explicit `state` (see `ConnectionsState`) for exactly this,
// and DashboardScreen's traffic empty state reads the SAME field (its own
// poll loop, same verb) — see DashboardScreen.swift's `trafficEmptyState` —
// so the two screens can no longer disagree.
//
// Firewall lives on this same screen rather than its own section/tab: F6
// groups "diagnose what's happening" and "block/allow what's happening" as
// one feature, the daemon exposes them over the same live snapshot, and a
// user's natural flow is "I see this connection -> I block it" without
// switching screens. Adding a dedicated `.firewall` Section would also touch
// Navigation.swift/SingctlApp.swift's routing beyond this task's file
// ownership (macos/Singctl/App/ only, but scoped to what was asked).
//
// Native: two sortable `Table`s (live connections, wrapped in the app's
// `Card`) plus small read-only summary tables for the daemon's own per-app/
// per-destination aggregates (never re-summed here — see `appTotalsCard`/
// `destTotalsCard`), a `.searchable` text filter, and a firewall `Card` with
// an add-rule form + rule list. Refreshed on a 4s timer, same cadence as
// AppsScreen.refreshLoop().

import SwiftUI
import Network

struct ConnectionsScreen: View {
    @Environment(\.backend) private var backend

    // MARK: - Live connections

    @State private var payload: ConnectionsPayload = .empty
    @State private var filter = ""
    @State private var sortOrder = [KeyPathComparator(\ConnDisplayRow.app)]
    @State private var closingIDs: Set<String> = []
    @State private var connectionsError: String?

    // MARK: - Firewall

    @State private var rules: [FirewallRule] = []
    @State private var rulesError: String?
    @State private var mutatingRuleIDs: Set<String> = []

    @State private var newAction = "block"
    @State private var newMatchKind: FirewallMatchKind = .domain
    @State private var newMatchValue = ""
    @State private var isAddingRule = false
    @State private var addRuleError: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.lg) {
                SectionHeader(title: "Connections", subtitle: subtitle)
                connectionsSection
                summarySection
                firewallSection
            }
            .padding(Spacing.lg)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .searchable(text: $filter, prompt: "Filter by app / host / rule")
        .task { await refreshLoop() }
    }

    // MARK: - Header

    private var subtitle: String {
        switch payload.state {
        case .active: return "\(payload.rows?.count ?? 0) active"
        case .idle: return "idle — no traffic"
        case .apiDisabled: return "Clash API disabled"
        case .apiUnreachable: return "Clash API unreachable"
        case .noMode: return "no mode running"
        }
    }

    // MARK: - Live connections table

    private var connectionsSection: some View {
        Card(title: "Live connections") {
            Badge(text: "\(payload.rows?.count ?? 0) active", tone: badgeTone)
        } content: {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                if let connectionsError {
                    Text(connectionsError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }
                if payload.state != .active {
                    stateEmptyState
                } else if displayRows.isEmpty {
                    EmptyState(text: "No connections match your search.", symbol: "magnifyingglass")
                } else {
                    connectionsTable
                }
            }
        }
    }

    private var badgeTone: Tone {
        switch payload.state {
        case .active: return .ok
        case .idle: return .dim
        case .apiUnreachable: return .danger
        case .noMode, .apiDisabled: return .warn
        }
    }

    /// F6 item 3: distinguish every reason the table can be empty, each with
    /// its own next step, instead of one blanket "No active connections."
    @ViewBuilder
    private var stateEmptyState: some View {
        switch payload.state {
        case .noMode:
            EmptyState(
                text: "No mode is running. Switch to Proxy or VPN on the Dashboard to start routing traffic.",
                symbol: "power"
            )
        case .apiDisabled:
            EmptyState(
                text: "The Clash API is disabled. Enable it in Settings → General to see live connections here.",
                symbol: "switch.2"
            )
        case .apiUnreachable:
            EmptyState(text: apiUnreachableText, symbol: "wifi.exclamationmark")
        case .idle:
            EmptyState(
                text: "The proxy is up and the Clash API is reachable — there is simply no traffic right now.",
                symbol: "tray"
            )
        case .active:
            // Unreachable in practice (the daemon only reports .active when
            // rows is non-empty), kept so the switch stays exhaustive.
            EmptyState(text: "No active connections.", symbol: "network.slash")
        }
    }

    private var apiUnreachableText: String {
        let detail = (payload.detail?.isEmpty == false) ? payload.detail! : "unknown error"
        return "The Clash API is unreachable (\(detail)). Check the address in Settings → General, or restart the daemon."
    }

    private var connectionsTable: some View {
        Table(displayRows, sortOrder: $sortOrder) {
            TableColumn("App", value: \.app) { row in
                VStack(alignment: .leading, spacing: 2) {
                    Text(row.app.isEmpty ? (row.process.isEmpty ? "?" : row.process) : row.app)
                        .font(.appBody)
                        .foregroundStyle(Color.sText)
                    if !row.process.isEmpty, row.process != row.app {
                        Text(row.process).font(.appCaption).foregroundStyle(Color.sTextFaint)
                    }
                }
            }
            .width(min: 110, ideal: 160)

            TableColumn("Destination", value: \.destination) { row in
                Text(row.destination).font(.appBody).foregroundStyle(Color.sText)
            }
            .width(min: 160, ideal: 240)

            TableColumn("Net", value: \.network) { row in
                Text(row.network.isEmpty ? "—" : row.network)
                    .font(.appBody)
                    .foregroundStyle(Color.sTextDim)
            }
            .width(min: 44, ideal: 56, max: 80)

            TableColumn("Rule / Chain", value: \.ruleChain) { row in
                Text(row.ruleChain)
                    .font(.system(size: 16, design: .monospaced))
                    .foregroundStyle(Color.sTextDim)
            }
            .width(min: 120, ideal: 200)

            TableColumn("Up", value: \.upload) { row in
                Text(ByteFormat.bytes(row.upload)).font(.appBody).foregroundStyle(Color.sTextDim)
            }
            .width(min: 70, ideal: 90)

            TableColumn("Down", value: \.download) { row in
                Text(ByteFormat.bytes(row.download)).font(.appBody).foregroundStyle(Color.sTextDim)
            }
            .width(min: 70, ideal: 90)

            TableColumn("Duration", value: \.durationSeconds) { row in
                Text(DurationFormat.short(row.durationSeconds)).font(.appBody).foregroundStyle(Color.sTextDim)
            }
            .width(min: 80, ideal: 100)

            TableColumn("") { row in
                AppButton(
                    "Close", kind: .danger, icon: "xmark.circle",
                    isLoading: closingIDs.contains(row.id), disabled: closingIDs.contains(row.id)
                ) {
                    closeConnection(row.id)
                }
            }
            .width(min: 90, ideal: 100, max: 120)
        }
        .tableStyle(.inset(alternatesRowBackgrounds: false))
        .scrollContentBackground(.hidden)
        .background(.clear)
        .frame(height: 320)
    }

    /// Search-filtered, then sorted by the active column order. Duration is
    /// computed against one shared `now` so every row in a given render
    /// agrees on "as of when."
    private var displayRows: [ConnDisplayRow] {
        let now = Date()
        let all = (payload.rows ?? []).map { row in
            ConnDisplayRow(
                id: row.id,
                app: row.app,
                process: row.process,
                destination: row.destination,
                network: row.network,
                ruleChain: Self.ruleChainText(rule: row.rule, chain: row.chain),
                upload: row.upload,
                download: row.download,
                durationSeconds: now.timeIntervalSince(row.startDate ?? now)
            )
        }
        let query = filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let filtered = query.isEmpty
            ? all
            : all.filter {
                $0.app.lowercased().contains(query)
                    || $0.process.lowercased().contains(query)
                    || $0.destination.lowercased().contains(query)
                    || $0.ruleChain.lowercased().contains(query)
            }
        return filtered.sorted(using: sortOrder)
    }

    private static func ruleChainText(rule: String, chain: [String]) -> String {
        var parts: [String] = []
        if !rule.isEmpty { parts.append(rule) }
        if !chain.isEmpty { parts.append(chain.joined(separator: "→")) }
        return parts.isEmpty ? "—" : parts.joined(separator: " · ")
    }

    private func closeConnection(_ id: String) {
        guard !closingIDs.contains(id) else { return }
        connectionsError = nil
        closingIDs.insert(id)
        Task {
            defer { closingIDs.remove(id) }
            do {
                try await backend.closeConnection(id)
                await refreshConnections()
            } catch {
                connectionsError = error.localizedDescription
            }
        }
    }

    // MARK: - Summary (F6 item 2) — the daemon's own aggregates, never re-summed here

    private var appTotals: [ConnectionAppTotal] { payload.apps ?? [] }
    private var destTotals: [ConnectionDestTotal] { payload.dests ?? [] }

    private var summarySection: some View {
        ViewThatFits(in: .horizontal) {
            HStack(alignment: .top, spacing: Spacing.md) {
                appTotalsCard.frame(minWidth: 320)
                destTotalsCard.frame(minWidth: 320)
            }
            VStack(spacing: Spacing.md) {
                appTotalsCard
                destTotalsCard
            }
        }
    }

    private var appTotalsCard: some View {
        Card(title: "By application") {
            Badge(text: "\(appTotals.count)", tone: .accent)
        } content: {
            if appTotals.isEmpty {
                EmptyState(text: "No per-app traffic yet.", symbol: "app.dashed")
            } else {
                Table(appTotals) {
                    TableColumn("App") { row in
                        Text(row.app).font(.appBody).foregroundStyle(Color.sText)
                    }
                    .width(min: 100, ideal: 150)
                    TableColumn("Conns") { row in
                        Text("\(row.count)").font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 50, ideal: 60, max: 80)
                    TableColumn("Up") { row in
                        Text(ByteFormat.bytes(row.upload)).font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 70, ideal: 90)
                    TableColumn("Down") { row in
                        Text(ByteFormat.bytes(row.download)).font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 70, ideal: 90)
                }
                .tableStyle(.inset(alternatesRowBackgrounds: false))
                .scrollContentBackground(.hidden)
                .background(.clear)
                .frame(height: 180)
            }
        }
        .frame(maxWidth: .infinity)
    }

    private var destTotalsCard: some View {
        Card(title: "By destination") {
            Badge(text: "\(destTotals.count)", tone: .accent)
        } content: {
            if destTotals.isEmpty {
                EmptyState(text: "No per-destination traffic yet.", symbol: "network")
            } else {
                Table(destTotals) {
                    TableColumn("Host") { row in
                        Text(row.host).font(.appBody).foregroundStyle(Color.sText)
                    }
                    .width(min: 120, ideal: 200)
                    TableColumn("Conns") { row in
                        Text("\(row.count)").font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 50, ideal: 60, max: 80)
                    TableColumn("Up") { row in
                        Text(ByteFormat.bytes(row.upload)).font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 70, ideal: 90)
                    TableColumn("Down") { row in
                        Text(ByteFormat.bytes(row.download)).font(.appBody).foregroundStyle(Color.sTextDim)
                    }
                    .width(min: 70, ideal: 90)
                }
                .tableStyle(.inset(alternatesRowBackgrounds: false))
                .scrollContentBackground(.hidden)
                .background(.clear)
                .frame(height: 180)
            }
        }
        .frame(maxWidth: .infinity)
    }

    // MARK: - Firewall (F6 item 5)

    private var firewallSection: some View {
        Card(title: "Firewall") {
            Badge(text: "\(rules.count)", tone: .accent)
        } content: {
            VStack(alignment: .leading, spacing: Spacing.md) {
                Text("Block or allow traffic by destination domain, destination CIDR, or source process. A rule takes effect on the running proxy immediately — no restart needed.")
                    .font(.appSecondary)
                    .foregroundStyle(Color.sTextDim)

                addRuleForm

                if let rulesError {
                    Text(rulesError).font(.appSecondary).foregroundStyle(Color.sDanger)
                }

                if rules.isEmpty {
                    EmptyState(text: "No firewall rules yet.", symbol: "shield")
                } else {
                    rulesList
                }
            }
        }
    }

    private var addRuleForm: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            HStack(spacing: Spacing.sm) {
                SegmentedControl(
                    options: [SegmentedOption("block", "Block"), SegmentedOption("allow", "Allow")],
                    selection: $newAction,
                    disabled: isAddingRule
                )
                .frame(width: 160)

                SegmentedControl(
                    options: [
                        SegmentedOption(FirewallMatchKind.domain, "Domain"),
                        SegmentedOption(FirewallMatchKind.cidr, "CIDR"),
                        SegmentedOption(FirewallMatchKind.process, "Process"),
                    ],
                    selection: $newMatchKind,
                    disabled: isAddingRule
                )
                .frame(width: 240)
            }

            HStack(spacing: Spacing.sm) {
                TextField(matchValuePlaceholder, text: $newMatchValue)
                    .textFieldStyle(.roundedBorder)
                    .controlSize(.large)
                    .disabled(isAddingRule)
                    .onSubmit { submitNewRule() }
                AppButton(
                    "Add rule", icon: "plus",
                    isLoading: isAddingRule, disabled: !matchValueIsValid
                ) {
                    submitNewRule()
                }
            }

            if let addRuleError {
                Text(addRuleError).font(.appSecondary).foregroundStyle(Color.sDanger)
            }
        }
    }

    private var matchValuePlaceholder: String {
        switch newMatchKind {
        case .domain: return "example.com"
        case .cidr: return "10.0.0.0/8"
        case .process: return "codex"
        }
    }

    private var trimmedMatchValue: String {
        newMatchValue.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Client-side pre-validation for fast feedback — the daemon's
    /// firewall.Rule.Validate remains the authority (see FirewallRule's doc
    /// comment).
    private var matchValueIsValid: Bool {
        switch newMatchKind {
        case .domain:
            return !trimmedMatchValue.isEmpty && !trimmedMatchValue.contains(where: \.isWhitespace)
        case .cidr:
            return FirewallValidation.isValidCIDR(trimmedMatchValue)
        case .process:
            return !trimmedMatchValue.isEmpty
        }
    }

    private func submitNewRule() {
        guard matchValueIsValid, !isAddingRule else { return }
        addRuleError = nil
        isAddingRule = true
        var rule = FirewallRule(id: "", action: newAction, domain: nil, cidr: nil, process: nil)
        switch newMatchKind {
        case .domain: rule.domain = trimmedMatchValue
        case .cidr: rule.cidr = trimmedMatchValue
        case .process: rule.process = trimmedMatchValue
        }
        Task {
            defer { isAddingRule = false }
            do {
                _ = try await backend.firewallAdd(rule)
                newMatchValue = ""
                await refreshFirewallRules()
            } catch {
                addRuleError = error.localizedDescription
            }
        }
    }

    private var rulesList: some View {
        List {
            ForEach(rules) { rule in
                HStack(spacing: Spacing.md) {
                    Badge(text: rule.action.capitalized, tone: rule.action == "block" ? .danger : .ok)
                    Text(rule.matchDescription)
                        .font(.system(size: 16, design: .monospaced))
                        .foregroundStyle(Color.sText)
                        .lineLimit(1)
                    Spacer()
                    AppButton(
                        "Remove", kind: .danger, icon: "trash",
                        isLoading: mutatingRuleIDs.contains(rule.id), disabled: mutatingRuleIDs.contains(rule.id)
                    ) {
                        removeRule(rule)
                    }
                }
                .padding(.vertical, Spacing.xs)
                .listRowBackground(Color.clear)
                .listRowSeparator(.hidden)
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .frame(minHeight: 60, maxHeight: 220)
    }

    private func removeRule(_ rule: FirewallRule) {
        guard !mutatingRuleIDs.contains(rule.id) else { return }
        rulesError = nil
        mutatingRuleIDs.insert(rule.id)
        Task {
            defer { mutatingRuleIDs.remove(rule.id) }
            do {
                try await backend.firewallRemove(rule.id)
                await refreshFirewallRules()
            } catch {
                rulesError = error.localizedDescription
            }
        }
    }

    // MARK: - Shared 4s refresh (mirrors AppsScreen.refreshLoop())

    private func refreshLoop() async {
        await refreshAll()
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: 4_000_000_000)
            if Task.isCancelled { break }
            await refreshAll()
        }
    }

    private func refreshAll() async {
        async let connectionsResult: ConnectionsPayload? = try? backend.connectionsDetail()
        async let rulesResult: [FirewallRule]? = try? backend.firewallList()
        let (conns, fwRules) = await (connectionsResult, rulesResult)
        if let conns { payload = conns }
        if let fwRules { rules = fwRules }
    }

    private func refreshConnections() async {
        if let conns = try? await backend.connectionsDetail() {
            payload = conns
        }
    }

    private func refreshFirewallRules() async {
        if let fwRules = try? await backend.firewallList() {
            rules = fwRules
        }
    }
}

// MARK: - Display row (search/sort over a rendered snapshot of ConnectionRow)

private struct ConnDisplayRow: Identifiable, Equatable {
    let id: String
    let app: String
    let process: String
    let destination: String
    let network: String
    let ruleChain: String
    let upload: Int64
    let download: Int64
    let durationSeconds: TimeInterval
}

// MARK: - Firewall add-rule form support

private enum FirewallMatchKind: Hashable {
    case domain, cidr, process
}

/// Client-side CIDR pre-validation, close to Go's net.ParseCIDR: a dotted/
/// colon address, "/", and a prefix length valid for that address family.
/// The daemon's firewall.Rule.Validate (net.ParseCIDR itself) is still the
/// authority — this only exists so a bad value is caught before a round trip.
private enum FirewallValidation {
    static func isValidCIDR(_ value: String) -> Bool {
        let parts = value.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 2, let prefix = Int(parts[1]) else { return false }
        let address = String(parts[0])
        if IPv4Address(address) != nil { return (0...32).contains(prefix) }
        if IPv6Address(address) != nil { return (0...128).contains(prefix) }
        return false
    }
}
