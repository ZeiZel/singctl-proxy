// AppModel.swift
//
// Shared-state wiring: how screens reach the data layer in App/Core.
//
//   - `LiveStore` (the 2s poll loop / published live state) is a `@StateObject`
//     owned by `SingctlApp` and injected as an `.environmentObject`. Any screen
//     reads it with `@EnvironmentObject var store: LiveStore`.
//   - `Backend` (status/mode/keys/settings/traffic/latency/connections — see
//     Backend.swift) is NOT an ObservableObject (no published state), so it
//     can't ride `.environmentObject`. It's injected instead via the custom
//     `\.backend` environment key below. Any kept screen (Dashboard, Proxies,
//     Keys, Settings) + the menu-bar extra reads it with
//     `@Environment(\.backend) var backend`. The concrete type is
//     `DaemonBackend` in the Developer-ID build (default) and `TunnelBackend`
//     in the App Store build (`#if APPSTORE`) — see Backend.swift,
//     DaemonBackend.swift, TunnelBackend.swift.
//   - `ControlClient` itself is still exposed via `\.controlClient`, but only
//     in the Developer-ID build: the App-Store-forbidden screens (AppsScreen,
//     ConnectionsScreen, ConsoleScreen, LicenseScreen) are excluded from the
//     App Store target entirely and keep talking to it directly, since their
//     verbs (PROC-*/APP-*/CONSOLE-POLL) aren't part of the cross-build
//     `Backend` contract.
//
// All live for the app's lifetime; there is exactly one of each, shared by
// every screen and the menu-bar extra (see SingctlApp.swift).

import SwiftUI

#if !APPSTORE
private struct ControlClientKey: EnvironmentKey {
    static let defaultValue = ControlClient()
}

extension EnvironmentValues {
    /// The shared `ControlClient` used to issue verbs against the daemon's
    /// control socket (MODE, KEYS-*, APP-*, PROC-*, SETTINGS-*, …) — used
    /// directly only by the App-Store-forbidden screens now that the kept
    /// screens go through `\.backend` instead. Every `ControlClient` instance
    /// re-discovers the daemon per call, so sharing one instance vs. creating
    /// ad hoc ones is purely a convenience — either works, but sharing means
    /// only one place to swap in a mock for previews.
    var controlClient: ControlClient {
        get { self[ControlClientKey.self] }
        set { self[ControlClientKey.self] = newValue }
    }
}
#endif

private struct BackendKey: EnvironmentKey {
    static let defaultValue: Backend = {
        #if APPSTORE
        return TunnelBackend()
        #else
        return DaemonBackend()
        #endif
    }()
}

extension EnvironmentValues {
    /// The shared `Backend` the kept screens + menu-bar extra use for
    /// status/mode/keys/settings/traffic/latency/connections. `SingctlApp`
    /// constructs one instance up front (shared with `LiveStore`) and injects
    /// it explicitly via `.environment(\.backend, _:)`; this default exists
    /// so previews/tests that skip that wiring still get a working instance.
    var backend: Backend {
        get { self[BackendKey.self] }
        set { self[BackendKey.self] = newValue }
    }
}
