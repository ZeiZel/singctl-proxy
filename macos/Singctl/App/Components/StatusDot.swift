// StatusDot.swift
//
// The small colored dot used next to "singctl" in the sidebar header and in
// the menu-bar popover to show whether the daemon is reachable.

import SwiftUI

/// ```swift
/// StatusDot(on: store.daemonRunning)
/// StatusDot(on: store.daemonRunning, size: 10)
/// ```
struct StatusDot: View {
    let on: Bool
    var size: CGFloat = 8

    /// - Parameters:
    ///   - on: `true` draws an `ok`-green glowing dot; `false` draws a dim gray one.
    ///   - size: Diameter in points.
    init(on: Bool, size: CGFloat = 8) {
        self.on = on
        self.size = size
    }

    var body: some View {
        Circle()
            .fill(on ? Color.sOk : Color.sTextFaint)
            .frame(width: size, height: size)
            .shadow(color: on ? Color.sOk.opacity(0.7) : .clear, radius: size * 0.6)
    }
}
