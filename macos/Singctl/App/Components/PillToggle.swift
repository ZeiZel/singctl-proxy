// PillToggle.swift
//
// A labeled switch row for settings-style screens: label (+ optional
// sub-caption) on the left, a native `Toggle` (`.switch` style) on the right.

import SwiftUI

/// ```swift
/// PillToggle("Save profile", isOn: $settings.saveProfile)
/// PillToggle("Clash API", isOn: $settings.clashEnabled, sub: "Exposes a local metrics/connections API")
/// ```
struct PillToggle: View {
    let label: String
    var sub: String?
    @Binding var isOn: Bool

    /// - Parameters:
    ///   - label: Row title.
    ///   - isOn: Bound toggle state.
    ///   - sub: Optional dim caption under the label.
    init(_ label: String, isOn: Binding<Bool>, sub: String? = nil) {
        self.label = label
        self._isOn = isOn
        self.sub = sub
    }

    var body: some View {
        Toggle(isOn: $isOn) {
            VStack(alignment: .leading, spacing: 2) {
                Text(label).foregroundStyle(.primary)
                if let sub {
                    Text(sub).font(.caption).foregroundStyle(.secondary)
                }
            }
        }
        .toggleStyle(.switch)
    }
}
