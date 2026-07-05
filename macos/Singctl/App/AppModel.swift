// AppModel.swift
//
// Shared-state wiring: how screens reach the data layer in App/Core.
//
//   - `LiveStore` (the 2s poll loop / published live state) is a `@StateObject`
//     owned by `SingctlApp` and injected as an `.environmentObject`. Any screen
//     reads it with `@EnvironmentObject var store: LiveStore`.
//   - `ControlClient` (the actor that issues one-shot verbs like MODE, KEYS-ADD,
//     APP-ROUTE, …) is NOT an ObservableObject (it's an actor with no published
//     state), so it can't ride `.environmentObject`. It's injected instead via
//     the custom `\.controlClient` environment key below. Any screen reads it
//     with `@Environment(\.controlClient) var control`.
//
// Both live for the app's lifetime; there is exactly one of each, shared by
// every screen and the menu-bar extra.

import SwiftUI

private struct ControlClientKey: EnvironmentKey {
    static let defaultValue = ControlClient()
}

extension EnvironmentValues {
    /// The shared `ControlClient` used to issue verbs against the daemon's
    /// control socket (MODE, KEYS-*, APP-*, PROC-*, SETTINGS-*, …). Every
    /// `ControlClient` instance re-discovers the daemon per call, so sharing
    /// one instance vs. creating ad hoc ones is purely a convenience — either
    /// works, but sharing means only one place to swap in a mock for previews.
    var controlClient: ControlClient {
        get { self[ControlClientKey.self] }
        set { self[ControlClientKey.self] = newValue }
    }
}
