// ProxiesScreen.swift
//
// The failover group's live member list, via `Backend.proxyGroup()`/
// `proxySelect(_:)` (PROXY-GROUP/PROXY-SELECT — see Backend.swift). Unlike
// the read-only version this replaces, every member row is clickable: it
// pins the group to that server, and a dedicated "Auto" row restores
// automatic urltest selection. `available == false` (single-server mode, no
// group at all) hides the picker entirely.
//
// State is owned locally and refreshed on a short timer, the same shape as
// AppsScreen's `refreshLoop()`/`mutatingBundleIDs` — not routed through
// LiveStore, since `subList`/`subAdd`-style screen-owned calls are the
// pattern this feature follows (see Backend.swift's doc comment).

import SwiftUI

struct ProxiesScreen: View {
    @Environment(\.backend) private var backend

    @State private var group: ProxyGroup = .empty
    @State private var errorMessage: String?
    /// The tag ("auto" or a member tag) currently being applied via
    /// PROXY-SELECT, or nil when no selection is in flight. Only one
    /// selection is allowed at a time — every row disables while this is set,
    /// and the row matching it shows an inline spinner.
    @State private var pendingTag: String?

    private static let minScaleMs = 300
    /// Mirrors AppsScreen's shared refresh cadence — a live list that changes
    /// on its own (urltest re-probing), not just on user action.
    private static let refreshInterval: UInt64 = 4_000_000_000

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Proxies")

            if let errorMessage {
                Text(errorMessage)
                    .font(.appSecondary)
                    .foregroundStyle(Color.sDanger)
            }

            groupCard
        }
        .padding(Spacing.lg)
        .task { await refreshLoop() }
    }

    private var groupCard: some View {
        Card(title: "Failover group (urltest)") {
            Badge(text: statusBadgeText, tone: .accent)
        } content: {
            if !group.available {
                EmptyState(
                    text: "Single-server mode — no failover group configured.",
                    symbol: "network.slash"
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if group.members.isEmpty {
                EmptyState(text: "No latency data yet. Enable Proxy/VPN mode with the Clash API on.", symbol: "gauge")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List {
                    autoRow
                        .listRowBackground(Color.clear)
                    ForEach(group.members) { member in
                        ProxyMemberRow(
                            member: member,
                            isEffective: member.tag == group.selected,
                            isAuto: group.auto,
                            isPending: pendingTag == member.tag,
                            disabled: pendingTag != nil,
                            maxDelay: maxDelay
                        ) {
                            select(member.tag)
                        }
                        .listRowBackground(Color.clear)
                    }
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .frame(maxHeight: .infinity)
    }

    /// "restore automatic urltest selection", pinned above the member list.
    /// Marked active when `group.auto` is true — auto MODE being in effect,
    /// independent of which member it currently resolves to (that's the
    /// matching member row's own "Active" mark via `isEffective`).
    private var autoRow: some View {
        Button {
            select("auto")
        } label: {
            HStack(spacing: Spacing.md) {
                HStack(spacing: Spacing.sm) {
                    StatusDot(on: group.auto)
                    Image(systemName: "wand.and.stars")
                        .foregroundStyle(Color.sTextDim)
                    Text("Auto")
                        .font(.appBody)
                        .foregroundStyle(Color.sText)
                    if group.auto {
                        Badge(text: "Auto mode", tone: .ok)
                    }
                }
                Spacer()
                if pendingTag == "auto" {
                    ProgressView().controlSize(.small)
                }
            }
            .padding(.vertical, Spacing.xs)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(pendingTag != nil)
    }

    private var maxDelay: Int {
        max(Self.minScaleMs, group.members.map(\.delay).max() ?? 0)
    }

    private var statusBadgeText: String {
        guard group.available else { return "single-server" }
        let tag = group.selected.isEmpty ? "—" : group.selected
        return group.auto ? "auto · \(tag)" : "pinned · \(tag)"
    }

    // MARK: - Actions

    private func refreshLoop() async {
        await load()
        while !Task.isCancelled {
            try? await Task.sleep(nanoseconds: Self.refreshInterval)
            if Task.isCancelled { break }
            await load()
        }
    }

    private func load() async {
        do {
            group = try await backend.proxyGroup()
            errorMessage = nil
        } catch {
            errorMessage = error.localizedDescription
        }
    }

    /// Pins the group to `tag` ("auto" restores automatic selection).
    /// Reflects the result immediately by reloading on success; a failure is
    /// surfaced inline, the way KeysScreen reports its own mutation errors.
    private func select(_ tag: String) {
        guard pendingTag == nil else { return }
        pendingTag = tag
        errorMessage = nil
        Task {
            defer { pendingTag = nil }
            do {
                try await backend.proxySelect(tag)
                await load()
            } catch {
                errorMessage = error.localizedDescription
            }
        }
    }
}

/// One failover-group member: index + name (e.g. "9 · Finland 🇫🇮"), the raw
/// tag kept as a tooltip for debugging, a latency gauge, and a click target
/// that pins the group to this server. Two independent marks distinguish
/// "this is the currently effective server" (`isEffective`) from "the group
/// is in auto mode right now" (`isAuto`) — a pinned member and an
/// auto-selected member can both be the effective one, and conflating them
/// (auto happening to pick proxy-8 vs. the user pinning proxy-8) was the
/// whole ambiguity this feature exists to remove.
private struct ProxyMemberRow: View {
    let member: ProxyGroupMember
    let isEffective: Bool
    let isAuto: Bool
    let isPending: Bool
    let disabled: Bool
    let maxDelay: Int
    let onSelect: () -> Void

    private static let minScaleMs = 300

    var body: some View {
        Button(action: onSelect) {
            HStack(alignment: .center, spacing: Spacing.md) {
                HStack(spacing: Spacing.sm) {
                    if isEffective {
                        StatusDot(on: true)
                    }
                    Text("\(member.index) · \(member.name)")
                        .font(.appBody)
                        .foregroundStyle(Color.sText)
                        .help(member.tag)
                    if isEffective {
                        Badge(text: "Active", tone: .ok)
                    }
                    if isEffective, !isAuto {
                        Badge(text: "Pinned", tone: .accent)
                    }
                }
                .frame(minWidth: 200, alignment: .leading)

                Gauge(value: gaugeValue, in: 0...Double(max(Self.minScaleMs, maxDelay))) {
                    EmptyView()
                } currentValueLabel: {
                    Text(delayLabel)
                        .font(.appBody.monospacedDigit())
                        .foregroundStyle(Color.sTextDim)
                }
                .gaugeStyle(.accessoryLinearCapacity)
                .tint(tint)

                if isPending {
                    ProgressView().controlSize(.small)
                }
            }
            .padding(.vertical, Spacing.xs)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(disabled)
    }

    private var gaugeValue: Double {
        Double(Swift.max(0, member.delay))
    }

    private var delayLabel: String {
        member.delay > 0 ? "\(member.delay) ms" : "timeout"
    }

    /// Thresholds follow the same bands as gui/frontend/src/entities/proxy/
    /// ui/LatencyBar.tsx's delayColorClass.
    private var tint: Color {
        if member.delay <= 0 { return .sDanger }
        if member.delay < 150 { return .sOk }
        if member.delay < 350 { return .sWarn }
        return .sDanger
    }
}
