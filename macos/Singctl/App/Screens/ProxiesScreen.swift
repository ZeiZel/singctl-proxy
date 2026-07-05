// ProxiesScreen.swift
//
// Mirrors gui/frontend's Proxies page (LatencyList/LatencyBar): the failover
// group's per-server urltest latency, rendered as one native `Gauge` per
// server with the currently-selected server highlighted. Read-only — data
// comes straight from `LiveStore.latency`, which is refreshed by the 2s poll
// loop.

import SwiftUI

struct ProxiesScreen: View {
    @EnvironmentObject private var store: LiveStore

    private static let minScaleMs = 300

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Proxies")
            latencyCard
        }
        .padding(Spacing.lg)
    }

    private var latencyCard: some View {
        Card(title: "Failover group (urltest)") {
            Badge(text: "selected: \(store.latency.selected.isEmpty ? "—" : store.latency.selected)", tone: .accent)
        } content: {
            if store.latency.rows.isEmpty {
                EmptyState(text: "No latency data yet. Enable Proxy/VPN mode with the Clash API on.", symbol: "gauge")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                List(store.latency.rows) { row in
                    LatencyGaugeRow(row: row, maxDelay: maxDelay)
                        .listRowBackground(Color.clear)
                }
                .listStyle(.plain)
                .scrollContentBackground(.hidden)
            }
        }
        .frame(maxHeight: .infinity)
    }

    private var maxDelay: Int {
        max(Self.minScaleMs, store.latency.rows.map(\.delay).max() ?? 0)
    }
}

/// One server's latency as a native `Gauge`. Thresholds follow the same
/// bands as gui/frontend/src/entities/proxy/ui/LatencyBar.tsx's
/// delayColorClass.
private struct LatencyGaugeRow: View {
    let row: LatencyRow
    let maxDelay: Int

    var body: some View {
        HStack(alignment: .center, spacing: Spacing.md) {
            HStack(spacing: Spacing.sm) {
                if row.selected {
                    StatusDot(on: true)
                }
                Text(row.tag)
                    .font(.appBody)
                    .foregroundStyle(Color.sText)
                if row.selected {
                    Badge(text: "selected", tone: .ok)
                }
            }
            .frame(minWidth: 160, alignment: .leading)

            Gauge(value: gaugeValue, in: 0...Double(max(Self.minScaleMs, maxDelay))) {
                EmptyView()
            } currentValueLabel: {
                Text(delayLabel)
                    .font(.appBody.monospacedDigit())
                    .foregroundStyle(Color.sTextDim)
            }
            .gaugeStyle(.accessoryLinearCapacity)
            .tint(tint)
        }
        .padding(.vertical, Spacing.xs)
    }

    private static let minScaleMs = 300

    private var gaugeValue: Double {
        Double(Swift.max(0, row.delay))
    }

    private var delayLabel: String {
        row.delay > 0 ? "\(row.delay) ms" : "timeout"
    }

    private var tint: Color {
        if row.delay <= 0 { return .sDanger }
        if row.delay < 150 { return .sOk }
        if row.delay < 350 { return .sWarn }
        return .sDanger
    }
}
