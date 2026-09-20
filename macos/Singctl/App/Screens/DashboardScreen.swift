// DashboardScreen.swift
//
// The flagship screen — reference implementation for how a screen wires up
// to `LiveStore` (read-only live state) and `Backend` (verb calls) via the
// environment:
//   @EnvironmentObject private var store: LiveStore
//   @Environment(\.backend) private var backend
//
// Layout: warning banner (daemon offline) -> mode switch -> stat grid ->
// traffic chart. Status badges live in the toolbar next to the native title.

import SwiftUI
import Charts

struct DashboardScreen: View {
    @EnvironmentObject private var store: LiveStore
    @Environment(\.backend) private var backend

    @State private var isApplyingMode = false
    @State private var modeError: String?

    /// F6 item 3's fix: the traffic empty state used to say "Enable the
    /// Clash API in Settings" purely from `store.trafficSamples.isEmpty`,
    /// which could show even when Settings already had it on (e.g. no mode
    /// running, or the API unreachable) — the exact contradiction reported.
    /// Polled independently of `LiveStore` from the same CONNECTIONS control
    /// verb ConnectionsScreen reads (see Backend.connectionsDetail's doc
    /// comment), so the two screens read one shared `state` and can no
    /// longer disagree about which is true.
    @State private var connectionsState: ConnectionsPayload = .empty
    /// Set only when `backend.connectionsDetail()` itself throws (the App
    /// Store `TunnelBackend` reports "unsupported" rather than faking a
    /// state — see TunnelBackend.swift) — distinct from any of
    /// `ConnectionsState`'s cases, so this build gets an honest message
    /// instead of being misread as "no mode running."
    @State private var connectionsStateError: String?

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

                modeSection
                statGrid
                trafficSection
            }
            .padding(Spacing.lg)
        }
        .task { await connectionsStatePollLoop() }
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
        // VPN mode reroutes ALL system traffic, not just proxy-aware apps —
        // ask first, unless the user turned that confirmation off in
        // Settings → General (see AppPreferences.swift).
        if mode == "vpn", !AppPreferences.shared.confirmVPNSwitch() { return }
        isApplyingMode = true
        modeError = nil
        let optimisticChange = store.optimisticallySetMode(mode)
        Task { @MainActor in
            defer { isApplyingMode = false }
            do {
                try await backend.setMode(mode)
                store.refreshAfterMutation()
            } catch {
                store.restoreOptimisticStatus(optimisticChange)
                store.refreshAfterMutation()
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
                trafficEmptyState
                    .frame(maxWidth: .infinity, alignment: .leading)
            } else {
                TrafficChart(samples: store.trafficSamples)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }

    /// Driven by `connectionsState.state` — the same CONNECTIONS field
    /// ConnectionsScreen's own empty state reads (see this screen's
    /// `connectionsState` doc comment) — instead of guessing from
    /// `trafficSamples.isEmpty` alone, so this can never again tell the user
    /// to enable a Clash API that Settings already shows enabled.
    @ViewBuilder
    private var trafficEmptyState: some View {
        if let connectionsStateError {
            EmptyState(text: "Live traffic diagnosis isn't available in this build: \(connectionsStateError)")
        } else {
            switch connectionsState.state {
            case .noMode:
                EmptyState(text: "No mode is running. Switch Mode above to Proxy or VPN to start routing traffic.")
            case .apiDisabled:
                EmptyState(text: "The Clash API is disabled. Enable it in Settings → General to see live traffic here.")
            case .apiUnreachable:
                let detail = (connectionsState.detail?.isEmpty == false) ? connectionsState.detail! : "unknown error"
                EmptyState(text: "The Clash API is unreachable (\(detail)). Check the address in Settings → General, or restart the daemon.")
            case .idle:
                EmptyState(text: "The proxy is up and the Clash API is reachable — there is simply no traffic right now.")
            case .active:
                // Rows exist but the traffic sample window hasn't populated
                // yet (just switched modes, or between traffic polls).
                EmptyState(text: "Traffic is flowing; the chart will populate shortly.")
            }
        }
    }

    /// Independent of `LiveStore`'s poll loop — same 5s-ish cadence idea as
    /// its traffic polling, but this screen only needs the `state` field, not
    /// the full row/aggregate payload ConnectionsScreen renders.
    private func connectionsStatePollLoop() async {
        while !Task.isCancelled {
            do {
                connectionsState = try await backend.connectionsDetail()
                connectionsStateError = nil
            } catch {
                connectionsStateError = error.localizedDescription
            }
            try? await Task.sleep(nanoseconds: 5_000_000_000)
        }
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
