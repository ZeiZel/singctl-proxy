// DashboardScreen.swift
//
// The flagship screen — reference implementation for how a screen wires up
// to `LiveStore` (read-only live state) and `ControlClient` (verb calls) via
// the environment. Wave-2 screens should follow this same shape:
//   @EnvironmentObject private var store: LiveStore
//   @Environment(\.controlClient) private var control
//
// Layout: mode switch -> 4-up stat grid -> traffic chart, with warning cards
// up top when the daemon is offline or the license is invalid.

import SwiftUI
import Charts

struct DashboardScreen: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.controlClient) private var control

    @State private var isApplyingMode = false
    @State private var modeError: String?
    @State private var license: LicenseStatus?

    private let modeOptions: [SegmentedOption<String>] = [
        SegmentedOption("off", "Off"),
        SegmentedOption("proxy", "Proxy"),
        SegmentedOption("vpn", "VPN"),
    ]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                header

                if !store.daemonRunning {
                    Card {
                        Text("The singctl daemon is not running. Start it, then this dashboard connects automatically.")
                            .foregroundStyle(Color.sWarn)
                    }
                }

                if let license, !license.valid {
                    Card {
                        Text(licenseMessage(license))
                            .foregroundStyle(Color.sDanger)
                    }
                }

                modeCard
                statGrid
                trafficCard
            }
            .padding(Spacing.lg)
        }
        .task { license = try? await LicenseService.status() }
    }

    // MARK: - Header

    private var header: some View {
        SectionHeader(title: "Dashboard") {
            HStack(spacing: Spacing.sm) {
                if store.status.ciscoActive {
                    Badge(text: "Cisco active", tone: .warn)
                }
                Badge(
                    text: store.daemonRunning ? "Running" : "Offline",
                    tone: store.daemonRunning ? .ok : .danger
                )
            }
        }
    }

    // MARK: - Mode switch

    private var modeCard: some View {
        Card {
            HStack {
                VStack(alignment: .leading, spacing: Spacing.xs) {
                    Text("MODE").font(.caption2).foregroundStyle(Color.sTextDim)
                    SegmentedControl(options: modeOptions, selection: modeBinding, disabled: isApplyingMode)
                    if let modeError {
                        Text(modeError).font(.caption).foregroundStyle(Color.sDanger)
                    }
                }
                Spacer()
                VStack(alignment: .trailing, spacing: Spacing.xs) {
                    Text("SELECTED NODE").font(.caption2).foregroundStyle(Color.sTextDim)
                    Text(selectedNodeLabel)
                        .font(.subheadline.weight(.semibold))
                        .foregroundStyle(Color.sText)
                }
            }
        }
    }

    private var modeBinding: Binding<String> {
        Binding(
            get: { store.status.mode.isEmpty ? "off" : store.status.mode },
            set: applyMode
        )
    }

    private func applyMode(_ mode: String) {
        isApplyingMode = true
        modeError = nil
        Task {
            defer { isApplyingMode = false }
            do {
                try await control.setMode(mode)
            } catch {
                modeError = error.localizedDescription
            }
        }
    }

    private var selectedNodeLabel: String {
        guard let row = store.latency.rows.first(where: { $0.selected }) else {
            return store.latency.selected.isEmpty ? "—" : store.latency.selected
        }
        return row.delay > 0 ? "\(row.tag) · \(row.delay) ms" : "\(row.tag) · timeout"
    }

    // MARK: - Stat grid

    private var lastSample: (up: Double, down: Double)? { store.trafficSamples.last }

    private var statGrid: some View {
        let columns = [GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible()), GridItem(.flexible())]
        return LazyVGrid(columns: columns, spacing: Spacing.md) {
            Card { StatTile(label: "Upload rate", value: ByteFormat.rate(lastSample?.up ?? 0), tone: .accent) }
            Card { StatTile(label: "Download rate", value: ByteFormat.rate(lastSample?.down ?? 0), tone: .ok) }
            Card { StatTile(label: "Total up", value: ByteFormat.bytes(store.totalUp)) }
            Card { StatTile(label: "Total down", value: ByteFormat.bytes(store.totalDown)) }
        }
    }

    // MARK: - Traffic chart

    private var trafficCard: some View {
        Card(title: "Traffic") {
            Badge(text: "\(store.connections.count) active connections", tone: .accent)
        } content: {
            if store.trafficSamples.isEmpty {
                EmptyState(text: "No traffic yet. Enable the Clash API in Settings to see live traffic here.")
            } else {
                TrafficChart(samples: store.trafficSamples)
            }
        }
    }

    private func licenseMessage(_ status: LicenseStatus) -> String {
        status.reason.isEmpty
            ? "No valid license — open the License section to activate."
            : "No valid license — \(status.reason)"
    }
}

/// Dual-area chart (upload = accent, download = ok) over `LiveStore`'s
/// rolling traffic-sample window.
private struct TrafficChart: View {
    let samples: [(up: Double, down: Double)]

    private struct Point: Identifiable {
        let id: Int
        let up: Double
        let down: Double
    }

    private var points: [Point] {
        samples.enumerated().map { Point(id: $0.offset, up: $0.element.up, down: $0.element.down) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            Chart(points) { point in
                AreaMark(x: .value("t", point.id), y: .value("Upload", point.up))
                    .foregroundStyle(Color.sAccent.opacity(0.25))
                LineMark(x: .value("t", point.id), y: .value("Upload", point.up))
                    .foregroundStyle(Color.sAccent)
                    .interpolationMethod(.monotone)

                AreaMark(x: .value("t", point.id), y: .value("Download", point.down))
                    .foregroundStyle(Color.sOk.opacity(0.2))
                LineMark(x: .value("t", point.id), y: .value("Download", point.down))
                    .foregroundStyle(Color.sOk)
                    .interpolationMethod(.monotone)
            }
            .chartXAxis(.hidden)
            .chartYAxis {
                AxisMarks(position: .leading) { value in
                    AxisGridLine().foregroundStyle(Color.sBorder)
                    if let bytes = value.as(Double.self) {
                        AxisValueLabel(ByteFormat.rate(bytes))
                            .foregroundStyle(Color.sTextFaint)
                    }
                }
            }
            .frame(height: 180)

            HStack(spacing: Spacing.md) {
                legendEntry(color: .sAccent, label: "Upload")
                legendEntry(color: .sOk, label: "Download")
            }
        }
    }

    private func legendEntry(color: Color, label: String) -> some View {
        HStack(spacing: Spacing.xs) {
            Circle().fill(color).frame(width: 8, height: 8)
            Text(label).font(.caption).foregroundStyle(Color.sTextDim)
        }
    }
}
