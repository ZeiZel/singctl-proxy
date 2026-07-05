// Backend.swift
//
// The one seam between the kept screens (Dashboard, Proxies, Keys, Settings +
// the menu-bar extra) and the two very different things that can drive them:
//   - Developer-ID build (default, `#if !APPSTORE`): `DaemonBackend`, a thin
//     wrapper over the existing `ControlClient` (Unix control-socket verbs)
//     + `ClashClient` (read-only observability) + `InstanceDiscovery` (daemon
//     lookup) — see DaemonBackend.swift. Behavior is byte-for-byte identical
//     to what LiveStore/screens did before this abstraction existed.
//   - App Store build (`#if APPSTORE`): `TunnelBackend`, which drives a
//     `NETunnelProviderManager`-managed `NEPacketTunnelProvider` instead of a
//     root daemon, and persists mode/keys/settings to the shared App Group
//     container instead of talking to a control socket — see
//     TunnelBackend.swift.
//
// The protocol only covers what the KEPT screens call. Per-app routing
// (PROC-*/APP-*), console streaming (CONSOLE-POLL), and license activation
// are App-Store-forbidden features whose screens (AppsScreen, ConsoleScreen,
// LicenseScreen) are excluded from the App Store target entirely — they keep
// talking to `ControlClient`/`LicenseService` directly via the `\.controlClient`
// environment key, which is Dev-ID-only (see AppModel.swift).
import Foundation

/// Everything the Dashboard/Proxies/Keys/Settings screens + the menu-bar
/// extra need from "the thing that runs the proxy", independent of whether
/// that's a root daemon (Dev-ID) or a sandboxed packet-tunnel appex (App
/// Store). `AnyObject`-constrained so it can be held as a reference type and
/// injected via a SwiftUI environment key without wrapping (see AppModel.swift).
protocol Backend: AnyObject {

    // MARK: - Status / lifecycle

    /// Current daemon/tunnel status (mode, pid, Cisco/netext flags — see
    /// `DaemonStatus`). Dev-ID: STATUS control verb. App Store: derived from
    /// the `NETunnelProviderManager` connection state + the mode stored in
    /// the App Group config.
    func status() async throws -> DaemonStatus

    /// Switches proxy mode ("off" / "proxy" / "vpn"). Dev-ID: MODE control
    /// verb. App Store: persists the mode to the App Group config and
    /// starts/stops the tunnel accordingly.
    func setMode(_ mode: String) async throws

    /// Stops routing entirely. Dev-ID: STOP control verb (stops the whole
    /// daemon). App Store: stops the packet-tunnel session.
    func stop() async throws

    // MARK: - Observability (Dashboard)

    /// Cumulative up/down byte counters. Dev-ID: TRAFFIC control verb (only
    /// available while the Clash API is enabled — see DaemonBackend). App
    /// Store: stubbed to zero for now (Libbox stats are a later step).
    func traffic() async throws -> Traffic

    /// Per-server urltest latency + current selection (Proxies screen).
    /// Dev-ID: derived from the Clash API's `/proxies`. App Store: stubbed
    /// empty for now.
    func latency() async throws -> Latency

    /// Live connection table + cumulative totals (Dashboard's connection
    /// count badge). Dev-ID: the Clash API's `/connections`. App Store:
    /// stubbed empty for now.
    func connections() async throws -> ClashConnections

    // MARK: - Keys (VLESS links)

    func keysGet() async throws -> [String]
    func keysAdd(_ link: String) async throws
    func keysRemove(_ index: Int) async throws
    func keysRename(_ index: Int, _ name: String) async throws

    // MARK: - Settings

    func settingsGet() async throws -> Settings
    func settingsSet(_ settings: Settings) async throws
}
