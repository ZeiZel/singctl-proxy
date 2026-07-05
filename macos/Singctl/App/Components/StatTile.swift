// StatTile.swift
//
// A small "label / big value / optional sub-line" tile, used in dashboard-
// style stat grids. Not itself a `Card` — wrap in one if you want the frosted
// background (Dashboard puts each tile in its own `Card`). Built on native
// typography (`.secondary`/`.tertiary` label colors, system rounded digits)
// rather than hand-picked hex colors.

import SwiftUI

/// ```swift
/// StatTile(label: "Upload rate", value: "1.2 MB/s", tone: .accent)
/// StatTile(label: "Total down", value: "340 MB", sub: "since daemon start")
/// ```
struct StatTile: View {
    let label: String
    let value: String
    var sub: String?
    var tone: Tone = .default

    /// - Parameters:
    ///   - label: Small uppercase caption above the value.
    ///   - value: The headline figure (already formatted, e.g. via `ByteFormat`).
    ///   - sub: Optional secondary line below the value.
    ///   - tone: Color applied to `value` (label/sub are always dim).
    init(label: String, value: String, sub: String? = nil, tone: Tone = .default) {
        self.label = label
        self.value = value
        self.sub = sub
        self.tone = tone
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.xs) {
            Text(label.uppercased())
                .font(.appSecondary)
                .tracking(0.6)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.appTitle)
                .foregroundStyle(tone.color)
                .monospacedDigit()
            if let sub {
                Text(sub)
                    .font(.appCaption)
                    .foregroundStyle(.tertiary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}
