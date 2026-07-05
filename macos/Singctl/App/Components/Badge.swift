// Badge.swift
//
// Small rounded-pill label for statuses/counts (e.g. "3 active connections",
// "Cisco AnyConnect", "off/proxy/vpn").

import SwiftUI

/// ```swift
/// Badge(text: "12 active connections")
/// Badge(text: "Cisco active", tone: .warn)
/// Badge(text: "Invalid license", tone: .danger)
/// ```
struct Badge: View {
    let text: String
    var tone: Tone = .dim

    /// - Parameters:
    ///   - text: Label text.
    ///   - tone: Fill/foreground tone; background is the tone color at low opacity.
    init(text: String, tone: Tone = .dim) {
        self.text = text
        self.tone = tone
    }

    var body: some View {
        Text(text)
            .font(.caption.weight(.medium))
            .foregroundStyle(tone.color)
            .padding(.horizontal, Spacing.sm)
            .padding(.vertical, 4)
            .background(tone.color.opacity(0.15))
            .clipShape(Capsule())
            .overlay(Capsule().strokeBorder(tone.color.opacity(0.3), lineWidth: 1))
    }
}
