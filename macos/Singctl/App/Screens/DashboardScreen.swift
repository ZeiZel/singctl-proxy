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
            VStack(alignment: .leading, spacing: Spacing.xl) {
                if !store.daemonRunning {
                    Card {
                        Label(
                            "The singctl daemon is not running. Start it, then this dashboard connects automatically.",
                            systemImage: "exclamationmark.triangle.fill"
                        )
                        .font(.appBody)
                        .foregroundStyle(Color.sWarn)
                        .frame(maxWidth: .infinity, alignment: .leading)
                    }
                }

                #if !APPSTORE
                if let license, !license.valid {
                    Card {
                        Label(licenseMessage(license), systemImage: "exclamationmark.octagon.fill")
                            .font(.appBody)
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
            // `sharedBackgroundVisibility(.hidden)` drops the system glass
            // capsule the toolbar would otherwise draw behind each item, so
            // the `Badge` pill is the only chrome around the status.
            if store.status.ciscoActive {
                ToolbarItem {
                    Badge(text: "Cisco active", tone: .warn)
                }
                .sharedBackgroundVisibility(.hidden)
            }
            ToolbarItem {
                Badge(
                    text: store.daemonRunning ? "Running" : "Offline",
                    tone: store.daemonRunning ? .ok : .danger,
                    dot: true
                )
            }
            .sharedBackgroundVisibility(.hidden)
        }
        #if !APPSTORE
        .task { license = try? await LicenseService.status() }
        #endif
    }

    // MARK: - Mode switch

    private var modeSection: some View {
        Card(title: "Mode") {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SegmentedControl(options: modeOptions, selection: modeBinding, disabled: isApplyingMode)

                if let modeError {
                    Text(modeError).font(.appBody).foregroundStyle(Color.sDanger)
                }

                HStack(spacing: Spacing.sm) {
                    Text("Selected node")
                        .font(.appSecondary)
                        .foregroundStyle(Color.sTextDim)
                    Spacer()
                    Text(selectedNodeLabel)
                        .font(.appBody)
                        .foregroundStyle(Color.sText)
                        .monospacedDigit()
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
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
        Card(title: "Traffic Stats") {
            Grid(alignment: .topLeading, horizontalSpacing: Spacing.xl, verticalSpacing: Spacing.md) {
                GridRow {
                    StatTile(label: "Upload rate", value: ByteFormat.rate(lastSample?.up ?? 0), tone: .accent)
                    StatTile(label: "Download rate", value: ByteFormat.rate(lastSample?.down ?? 0), tone: .ok)
                }
                GridRow {
                    StatTile(label: "Total up", value: ByteFormat.bytes(store.totalUp))
                    StatTile(label: "Total down", value: ByteFormat.bytes(store.totalDown))
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
    }

    // MARK: - Traffic chart

    private var trafficSection: some View {
        Card(title: "Traffic") {
            Badge(text: "\(store.connections.count) active connections", tone: .accent)
        } content: {
            if store.trafficSamples.isEmpty {
                EmptyState(text: "No traffic yet. Enable the Clash API in Settings to see live traffic here.")
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                TrafficChart(samples: store.trafficSamples)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
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
                AreaMark(
                    x: .value("t", point.id),
                    yStart: .value("z", 0.0),
                    yEnd: .value("rate", point.up),
                    series: .value("Series", "Upload")
                )
                .foregroundStyle(
                    LinearGradient(
                        colors: [Color.sAccent.opacity(0.30), .clear],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                .interpolationMethod(.catmullRom)
                LineMark(x: .value("t", point.id), y: .value("rate", point.up), series: .value("Series", "Upload"))
                    .foregroundStyle(Color.sAccent)
                    .interpolationMethod(.catmullRom)

                AreaMark(
                    x: .value("t", point.id),
                    yStart: .value("z", 0.0),
                    yEnd: .value("rate", point.down),
                    series: .value("Series", "Download")
                )
                .foregroundStyle(
                    LinearGradient(
                        colors: [Color.sOk.opacity(0.30), .clear],
                        startPoint: .top,
                        endPoint: .bottom
                    )
                )
                .interpolationMethod(.catmullRom)
                LineMark(x: .value("t", point.id), y: .value("rate", point.down), series: .value("Series", "Download"))
                    .foregroundStyle(Color.sOk)
                    .interpolationMethod(.catmullRom)
            }
            .chartYScale(domain: .automatic(includesZero: true))
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
