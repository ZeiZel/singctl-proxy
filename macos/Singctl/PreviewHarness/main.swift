// Deterministic, standalone renderer for README and visual-QA captures.
// It is a command-line target, not a launch mode of Singctl.app. The target
// compiles the real App sources with SCREENSHOT_HARNESS, which excludes the
// shipping @main entry point. Nothing here instantiates DaemonBackend, opens a
// control socket, touches proxy settings, or starts the installed service.

import AppKit
import QuartzCore
import SwiftUI

@main
struct SingctlPreviewMain {
    static func main() {
        let options = CaptureOptions(arguments: Array(CommandLine.arguments.dropFirst()))
        let backend = DemoBackend()
        let store = LiveStore.previewSnapshot(
            backend: backend,
            status: DemoBackend.status,
            trafficSamples: DemoBackend.trafficSamples,
            totalUp: 4_821_540_864,
            totalDown: 18_704_842_752,
            connections: DemoBackend.connections.connRows,
            latency: DemoBackend.latency
        )

        let app = NSApplication.shared
        app.setActivationPolicy(.accessory)

        let content = PreviewPresentation {
            RootView(initialSection: options.section)
                .environmentObject(store)
                .environment(\.backend, backend)
                .appTheme()
                .environment(\.controlActiveState, .key)
        }
        .preferredColorScheme(options.colorScheme)

        let hosting = NSHostingView(rootView: content)
        let window = PreviewWindow(
            contentRect: NSRect(origin: .zero, size: options.canvasSize),
            styleMask: [.borderless], backing: .buffered, defer: false
        )
        window.appearance = NSAppearance(named: options.colorScheme == .dark ? .darkAqua : .aqua)
        window.isReleasedWhenClosed = false
        window.contentView = hosting
        window.makeKeyAndOrderFront(nil)
        app.activate(ignoringOtherApps: true)
        window.layoutIfNeeded()
        hosting.wantsLayer = true
        hosting.layer?.contentsScale = 2
        hosting.layoutSubtreeIfNeeded()

        // Wait for the selected screen's normal, read-only loading task to
        // consume its fixed fixture values. This is condition-based rather
        // than a fixed delay, with a one-second diagnostic ceiling.
        let deadline = Date(timeIntervalSinceNow: 1)
        while !backend.isReady(for: options.section), Date() < deadline {
            RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.01))
        }
        // Flush the appearance/material transaction after the view's state is
        // settled; otherwise a dark capture can contain an in-flight light
        // material frame at the top edge.
        window.displayIfNeeded()
        CATransaction.flush()
        RunLoop.current.run(until: Date(timeIntervalSinceNow: 0.03))
        window.displayIfNeeded()
        CATransaction.flush()
        hosting.layoutSubtreeIfNeeded()

        do {
            try FileManager.default.createDirectory(
                at: options.output.deletingLastPathComponent(),
                withIntermediateDirectories: true
            )
            try WindowCapture.png(window: window, output: options.output)
            print(options.output.path)
        } catch {
            fputs("SingctlPreview: \(error.localizedDescription)\n", stderr)
            exit(1)
        }
    }
}

/// A borderless screenshot canvas has no title bar to make it key by default.
/// Let it become key so native controls use their normal active appearance.
private final class PreviewWindow: NSWindow {
    override var canBecomeKey: Bool { true }
}

private struct CaptureOptions {
    let section: Section
    let colorScheme: ColorScheme
    let canvasSize: CGSize
    let output: URL

    init(arguments: [String]) {
        func value(after flag: String) -> String? {
            guard let i = arguments.firstIndex(of: flag), arguments.indices.contains(i + 1) else { return nil }
            return arguments[i + 1]
        }
        switch value(after: "--screen") {
        case "settings": section = .settings
        default: section = .dashboard
        }
        colorScheme = value(after: "--appearance") == "dark" ? .dark : .light
        let narrow = value(after: "--width") == "narrow"
        canvasSize = narrow ? CGSize(width: 980, height: 860) : CGSize(width: 1400, height: 920)
        output = URL(fileURLWithPath: value(after: "--output") ?? "docs/images/singctl-swiftui-dashboard.png")
    }
}

/// Presentation-only backdrop around the actual RootView. It contains no
/// product controls or claims; its gradients and shapes are rendered by
/// SwiftUI as part of the PNG rather than edited into a captured bitmap.
private struct PreviewPresentation<Content: View>: View {
    private let content: Content

    init(@ViewBuilder content: () -> Content) { self.content = content() }

    var body: some View {
        GeometryReader { proxy in
            ZStack {
                LinearGradient(
                    colors: [Color(red: 0.92, green: 0.94, blue: 1.0), Color(red: 0.82, green: 0.87, blue: 0.98)],
                    startPoint: .topLeading, endPoint: .bottomTrailing
                )
                Circle()
                    .fill(Color.white.opacity(0.46))
                    .frame(width: proxy.size.width * 0.62)
                    .blur(radius: 46)
                    .offset(x: -proxy.size.width * 0.28, y: -proxy.size.height * 0.32)
                Circle()
                    .fill(Color.sAccent.opacity(0.19))
                    .frame(width: proxy.size.width * 0.56)
                    .blur(radius: 54)
                    .offset(x: proxy.size.width * 0.30, y: proxy.size.height * 0.34)

                content
                    .frame(width: proxy.size.width - 144, height: proxy.size.height - 144)
                    // RootView normally receives this from the WindowGroup's
                    // `containerBackground(for: .window)`. The standalone
                    // renderer has no SwiftUI Scene, so supply the same
                    // semantic window surface inside its clipped app frame.
                    .background(Color.sBg)
                    .clipShape(RoundedRectangle(cornerRadius: 18, style: .continuous))
                    .overlay {
                        RoundedRectangle(cornerRadius: 18, style: .continuous)
                            .strokeBorder(Color.white.opacity(0.55), lineWidth: 1)
                    }
                    .shadow(color: Color.black.opacity(0.18), radius: 28, y: 14)
            }
        }
    }
}

private enum WindowCapture {
    static func png(window: NSWindow, output: URL) throws {
        // `screencapture -l` asks WindowServer for exactly this process's
        // borderless renderer window. It is needed because hosted SwiftUI
        // materials are compositor-backed and appear black in NSView bitmap
        // APIs on Xcode 26. No desktop pixels or other app windows are read.
        let process = Process()
        process.executableURL = URL(fileURLWithPath: "/usr/sbin/screencapture")
        process.arguments = ["-x", "-l", String(window.windowNumber), output.path]
        try process.run()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw CaptureError.windowServerUnavailable(process.terminationStatus)
        }
    }

    enum CaptureError: LocalizedError {
        case windowServerUnavailable(Int32)

        var errorDescription: String? {
            switch self {
            case let .windowServerUnavailable(status):
                return "Window capture failed (screencapture exit \(status))."
            }
        }
    }
}

/// Fixed, in-memory data for every Backend requirement. Methods are async so
/// the genuine screens exercise their normal read paths, but none performs IO.
private final class DemoBackend: Backend {
    private var didReadSettings = false
    private var didReadSystemProxy = false
    private var didReadConnectionsDetail = false
    static let status = DaemonStatus(
        pid: 4242, mode: "proxy", startedAt: "2026-09-20T09:00:00Z",
        ciscoActive: false, proxyBypass: false, physIface: "en0",
        netextSupported: true, netextAvailable: true
    )
    static let latency = Latency(selected: "Stockholm", rows: [
        LatencyRow(tag: "Stockholm", delay: 42, selected: true),
        LatencyRow(tag: "Helsinki", delay: 56, selected: false),
        LatencyRow(tag: "Frankfurt", delay: 73, selected: false),
    ])
    static let connections = ClashConnections(
        downloadTotal: 18_704_842_752, uploadTotal: 4_821_540_864,
        connections: [
            ClashConn(
                id: "demo-1",
                metadata: ClashMetadata(network: "tcp", sourceIP: "127.0.0.1", destinationIP: "104.18.32.47", sourcePort: "52511", destinationPort: "443", host: "api.github.com", process: "Safari", processPath: "/Applications/Safari.app"),
                upload: 1_220_000, download: 6_430_000, chains: ["Stockholm"], rule: "MATCH"
            ),
        ]
    )
    static let trafficSamples: [(up: Double, down: Double)] = [
        (80_000, 420_000), (120_000, 710_000), (95_000, 610_000), (180_000, 1_020_000),
        (160_000, 840_000), (240_000, 1_320_000), (210_000, 1_080_000), (190_000, 970_000),
    ]
    private let settings = Settings(
        socksPort: 1080, clashEnabled: true, clashAddr: "127.0.0.1:9090",
        urlTestURL: "https://www.gstatic.com/generate_204", urlTestInterval: "3m",
        urlTestTolerance: 50, saveProfile: true, autostartMode: "proxy"
    )

    func status() async throws -> DaemonStatus { Self.status }
    func setMode(_ mode: String) async throws {}
    func stop() async throws {}
    func traffic() async throws -> Traffic { Traffic(up: 4_821_540_864, down: 18_704_842_752) }
    func latency() async throws -> Latency { Self.latency }
    func connections() async throws -> ClashConnections { Self.connections }
    func connectionsDetail() async throws -> ConnectionsPayload {
        didReadConnectionsDetail = true
        return ConnectionsPayload(state: .active, rows: [], apps: [], dests: [], detail: nil)
    }
    func closeConnection(_ id: String) async throws {}
    func proxyGroup() async throws -> ProxyGroup {
        ProxyGroup(available: true, auto: true, selected: "Stockholm", members: [
            ProxyGroupMember(tag: "Stockholm", index: 1, name: "Stockholm", delay: 42),
            ProxyGroupMember(tag: "Helsinki", index: 2, name: "Helsinki", delay: 56),
            ProxyGroupMember(tag: "Frankfurt", index: 3, name: "Frankfurt", delay: 73),
        ])
    }
    func proxySelect(_ tag: String) async throws {}
    func keysGet() async throws -> [String] { [] }
    func keysAdd(_ link: String) async throws {}
    func keysAddConfig(_ config: String) async throws {}
    func keysRemove(_ index: Int) async throws {}
    func keysRename(_ index: Int, _ name: String) async throws {}
    func subList() async throws -> [Subscription] { [] }
    func subAdd(_ url: String) async throws {}
    func subRemove(_ url: String) async throws {}
    func subUpdate() async throws -> Int { 0 }
    func settingsGet() async throws -> Settings {
        didReadSettings = true
        return settings
    }
    func settingsSet(_ settings: Settings) async throws {}
    func sysProxyStatus() async throws -> SysProxyStatus {
        didReadSystemProxy = true
        return SysProxyStatus(mode: "exclude", pacServerUp: true, service: "Wi-Fi", pacURL: "http://127.0.0.1:21080/proxy.pac", domains: 42)
    }
    func sysProxyConfig() async throws -> String { "[settings]\nmode = exclude" }
    func sysProxySet(mode: String) async throws {}
    func sysProxyImport(_ text: String) async throws {}
    func firewallList() async throws -> [FirewallRule] { [] }
    func firewallAdd(_ rule: FirewallRule) async throws -> FirewallRule { rule }
    func firewallRemove(_ id: String) async throws {}

    func isReady(for section: Section) -> Bool {
        switch section {
        case .settings:
            didReadSettings && didReadSystemProxy
        case .dashboard:
            didReadConnectionsDetail
        default:
            true
        }
    }
}
