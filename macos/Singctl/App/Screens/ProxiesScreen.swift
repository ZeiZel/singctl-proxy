// ProxiesScreen.swift
//
// Mirrors gui/frontend's Proxies page (LatencyList/LatencyBar): the failover
// group's per-server urltest latency, rendered as one bar per server with the
// currently-selected server highlighted. Read-only — data comes straight from
// `LiveStore.latency`, which is refreshed by the 2s poll loop.

import SwiftUI

struct ProxiesScreen: View {
    @EnvironmentObject private var store: LiveStore

    private static let minScaleMs = 300

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "Proxies")
                latencyCard
            }
            .padding(Spacing.lg)
        }
    }

    private var latencyCard: some View {
        Card(title: "Failover group (urltest)") {
            Badge(text: "selected: \(store.latency.selected.isEmpty ? "—" : store.latency.selected)", tone: .accent)
        } content: {
            if store.latency.rows.isEmpty {
                EmptyState(text: "No latency data yet. Enable Proxy/VPN mode with the Clash API on.")
            } else {
                VStack(spacing: 0) {
                    ForEach(store.latency.rows) { row in
                        LatencyBarRow(row: row, max: maxDelay)
                        if row.id != store.latency.rows.last?.id {
                            Divider().overlay(Color.sBorder.opacity(0.5))
                        }
                    }
                }
            }
        }
    }

    private var maxDelay: Int {
        max(Self.minScaleMs, store.latency.rows.map(\.delay).max() ?? 0)
    }
}

/// One server's latency as a labelled bar. Color follows the same thresholds
/// as gui/frontend/src/entities/proxy/ui/LatencyBar.tsx's delayColorClass.
private struct LatencyBarRow: View {
    let row: LatencyRow
    let max: Int

    var body: some View {
        HStack(alignment: .center, spacing: Spacing.md) {
            HStack(spacing: Spacing.sm) {
                if row.selected {
                    StatusDot(on: true)
                }
                Text(row.tag)
                    .foregroundStyle(Color.sText)
            }
            .frame(width: 160, alignment: .leading)

            GeometryReader { geo in
                ZStack(alignment: .leading) {
                    RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                        .fill(Color.sBgSoft)
                    RoundedRectangle(cornerRadius: Radius.sm, style: .continuous)
                        .fill(barColor)
                        .frame(width: geo.size.width * widthFraction)
                }
            }
            .frame(height: 6)

            Text(delayLabel)
                .foregroundStyle(Color.sTextDim)
                .frame(width: 70, alignment: .trailing)
        }
        .padding(.vertical, Spacing.xs)
    }

    private var widthFraction: CGFloat {
        guard row.delay > 0, max > 0 else { return 1 }
        return Swift.min(1, CGFloat(row.delay) / CGFloat(max))
    }

    private var delayLabel: String {
        row.delay > 0 ? "\(row.delay) ms" : "timeout"
    }

    private var barColor: Color {
        if row.delay <= 0 { return .sDanger }
        if row.delay < 150 { return .sOk }
        if row.delay < 350 { return .sWarn }
        return .sDanger
    }
}
