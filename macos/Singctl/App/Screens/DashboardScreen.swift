// DashboardScreen.swift
//
// The flagship screen — reference implementation for how a screen wires up
// to `LiveStore` (read-only live state) and `Backend` (verb calls) via the
// environment:
//   @EnvironmentObject private var store: LiveStore
//   @Environment(\.backend) private var backend
//
// Layout: warning banners (daemon offline / invalid license) -> mode switch
// -> stat grid -> traffic chart. Status badges live in the toolbar next to
// the native navigation title. The license banner is Developer-ID only (App
// Store apps don't self-license — see LICENSATION.md) — guarded `#if !APPSTORE`.

import SwiftUI
import Charts

struct DashboardScreen: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.backend) private var backend

    @State private var isApplyingMode = false
    @State private var modeError: String?
    #if !APPSTORE
    @State private var license: LicenseStatus?
    #endif

    private let modeOptions: [SegmentedOption<String>] = [
        SegmentedOption("off", "Off"),
        SegmentedOption("proxy", "Proxy"),
        SegmentedOption("vpn", "VPN"),
    ]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.lg) {
                if !store.daemonRunning {
                    GroupBox {
                        Label(
                            "The singctl daemon is not running. Start it, then this dashboard connects automatically.",
                            systemImage: "exclamationmark.triangle.fill"
                        )
                        .foregroundStyle(Color.sWarn)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }

                #if !APPSTORE
                if let license, !license.valid {
                    GroupBox {
                        Label(licenseMessage(license), systemImage: "exclamationmark.octagon.fill")
                            .foregroundStyle(Color.sDanger)
                            .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }
                #endif

                modeSection
                statGrid
                trafficSection
            }
            .padding(Spacing.lg)
        }
        .navigationTitle("Dashboard")
        .toolbar {
            ToolbarItemGroup {
                if store.status.ciscoActive {
                    Label("Cisco active", systemImage: "exclamationmark.triangle.fill")
                        .font(.appSecondary)
                        .foregroundStyle(Color.sWarn)
                }
                HStack(spacing: Spacing.xs) {
                    StatusDot(on: store.daemonRunning)
                    Text(store.daemonRunning ? "Running" : "Offline")
                        .font(.appSecondary)
                        .foregroundStyle(store.daemonRunning ? Color.sOk : Color.sDanger)
                }
            }
        }
        #if !APPSTORE
        .task { license = try? await LicenseService.status() }
        #endif
    }

    // MARK: - Mode switch

    private var modeSection: some View {
        GroupBox("Mode") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                SegmentedControl(options: modeOptions, selection: modeBinding, disabled: isApplyingMode)

                if let modeError {
                    Text(modeError).font(.appBody).foregroundStyle(Color.sDanger)
                }

                LabeledContent("Selected node", value: selectedNodeLabel)
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.top, Spacing.xs)
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
                try await backend.setMode(mode)
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
        GroupBox("Traffic Stats") {
            Grid(alignment: .leading, horizontalSpacing: Spacing.xl, verticalSpacing: Spacing.sm) {
                GridRow {
                    LabeledContent("Upload rate", value: ByteFormat.rate(lastSample?.up ?? 0))
                    LabeledContent("Download rate", value: ByteFormat.rate(lastSample?.down ?? 0))
                }
                GridRow {
                    LabeledContent("Total up", value: ByteFormat.bytes(store.totalUp))
                    LabeledContent("Total down", value: ByteFormat.bytes(store.totalDown))
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.top, Spacing.xs)
        }
    }

    // MARK: - Traffic chart

    private var trafficSection: some View {
        GroupBox("Traffic") {
            VStack(alignment: .leading, spacing: Spacing.sm) {
                Badge(text: "\(store.connections.count) active connections", tone: .accent)

                if store.trafficSamples.isEmpty {
                    EmptyState(text: "No traffic yet. Enable the Clash API in Settings to see live traffic here.")
                } else {
                    TrafficChart(samples: store.trafficSamples)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.top, Spacing.xs)
        }
    }

    #if !APPSTORE
    private func licenseMessage(_ status: LicenseStatus) -> String {
        status.reason.isEmpty
            ? "No valid license — open the License section to activate."
            : "No valid license — \(status.reason)"
    }
    #endif
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
                    .foregroundStyle(
                        LinearGradient(
                            colors: [Color.sAccent.opacity(0.30), .clear],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                    .interpolationMethod(.monotone)
                LineMark(x: .value("t", point.id), y: .value("Upload", point.up))
                    .foregroundStyle(Color.sAccent)
                    .interpolationMethod(.monotone)

                AreaMark(x: .value("t", point.id), y: .value("Download", point.down))
                    .foregroundStyle(
                        LinearGradient(
                            colors: [Color.sOk.opacity(0.30), .clear],
                            startPoint: .top,
                            endPoint: .bottom
                        )
                    )
                    .interpolationMethod(.monotone)
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
                            .font(.appCaption)
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
            Text(label).font(.appSecondary).foregroundStyle(Color.sTextDim)
        }
    }
}
