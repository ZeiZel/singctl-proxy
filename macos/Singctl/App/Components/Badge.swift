// Badge.swift
//
// Small rounded-pill label for statuses/counts (e.g. "3 active connections",
// "Cisco AnyConnect", "off/proxy/vpn"). Colored via `Tone`, which now resolves
// to semantic system colors (`.secondary`, `.green`, `.red`, …) rather than
// fixed hex values, so it adapts to Light/Dark automatically.

import SwiftUI

/// ```swift
/// Badge(text: "12 active connections")
/// Badge(text: "Cisco active", tone: .warn)
/// Badge(text: "Running", tone: .ok, dot: true)
/// ```
struct Badge: View {
    let text: String
    var tone: Tone = .dim
    var dot: Bool = false

    /// - Parameters:
    ///   - text: Label text.
    ///   - tone: Fill/foreground tone; background is the tone color at low opacity.
    ///   - dot: Prepends a small glowing status dot in the tone color.
    init(text: String, tone: Tone = .dim, dot: Bool = false) {
        self.text = text
        self.tone = tone
        self.dot = dot
    }

    var body: some View {
        HStack(spacing: 6) {
            if dot {
                Circle()
                    .fill(tone.color)
                    .frame(width: 7, height: 7)
                    .accessibilityHidden(true)
            }
            Text(text)
                .font(.appCaption.weight(.medium))
        }
        .foregroundStyle(tone.color)
        .padding(.horizontal, Spacing.sm)
        .padding(.vertical, 4)
        .background(tone.color.opacity(0.14), in: Capsule())
        .overlay(Capsule().strokeBorder(tone.color.opacity(0.35), lineWidth: 1))
    }
}
