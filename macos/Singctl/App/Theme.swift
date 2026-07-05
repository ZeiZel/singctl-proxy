// Theme.swift
//
// The design system's tokens: palette, spacing/radius scale, and small
// formatting helpers shared by every screen. Originally mirrored gui/
// frontend's Tailwind theme with a hardcoded dark palette; the `s*` names
// are now backed by semantic/adaptive system colors so the app follows the
// OS's Light/Dark appearance and window vibrancy instead of forcing dark.
// Call sites (`Color.sText`, `Tone.ok`, …) are unchanged — only what they
// resolve to changed.

import SwiftUI

// MARK: - Palette

extension Color {

    /// Parses a "#RRGGBB" or "#RRGGBBAA" hex string. Unrecognized input
    /// falls back to opaque black rather than crashing. Still used by chart
    /// code that wants a fixed color regardless of appearance.
    init(hex: String) {
        var s = hex.trimmingCharacters(in: .whitespacesAndNewlines)
        s.removeAll { $0 == "#" }
        var v: UInt64 = 0
        Scanner(string: s).scanHexInt64(&v)
        let r, g, b, a: UInt64
        switch s.count {
        case 8:
            (r, g, b, a) = ((v >> 24) & 0xFF, (v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF)
        default: // 6 or unrecognized
            (r, g, b, a) = ((v >> 16) & 0xFF, (v >> 8) & 0xFF, v & 0xFF, 0xFF)
        }
        self.init(
            .sRGB,
            red: Double(r) / 255, green: Double(g) / 255, blue: Double(b) / 255,
            opacity: Double(a) / 255
        )
    }

    // Base surfaces — adaptive/translucent rather than opaque dark fills, so
    // window vibrancy shows through. `sBg` is fully transparent (the window's
    // own material provides the background); the others are subtle system
    // surfaces used where a screen still wants a faint recessed/raised panel.
    static let sBg = Color.clear
    static let sBgSoft = Color(nsColor: .controlBackgroundColor)
    static let sPanel = Color(nsColor: .controlBackgroundColor)
    static let sPanelRaised = Color.primary.opacity(0.05)
    static let sBorder = Color(nsColor: .separatorColor)

    // Accents / semantic tones — system accent + standard semantic colors,
    // all of which adapt automatically between Light and Dark.
    static let sAccent = Color.accentColor
    static let sAccentSoft = Color.accentColor
    static let sOk = Color.green
    static let sDanger = Color.red
    static let sWarn = Color.orange

    // Text — system label colors.
    static let sText = Color.primary
    static let sTextDim = Color.secondary
    static let sTextFaint = Color(nsColor: .tertiaryLabelColor)
}

// MARK: - Tone

/// A semantic color used by `Badge`, `StatusDot`, `StatTile`, etc. — keeps
/// call sites free of raw `Color` literals.
enum Tone {
    case `default`, dim, accent, ok, warn, danger

    var color: Color {
        switch self {
        case .default: return .primary
        case .dim: return .secondary
        case .accent: return .accentColor
        case .ok: return .green
        case .warn: return .orange
        case .danger: return .red
        }
    }
}

// MARK: - Metrics

/// Spacing scale, in points. Mirrors the web GUI's `Stack`/`Box` gap tokens
/// (xs/sm/md/lg).
enum Spacing {
    static let xs: CGFloat = 4
    static let sm: CGFloat = 8
    static let md: CGFloat = 16
    static let lg: CGFloat = 24
    static let xl: CGFloat = 32
}

/// Corner-radius scale. `.card` mirrors Tailwind's `rounded-card` (12px).
enum Radius {
    static let sm: CGFloat = 6
    static let md: CGFloat = 9
    static let card: CGFloat = 12
}

// MARK: - Scene modifier

/// Root-level hook for app-wide appearance. The app now follows the system's
/// Light/Dark appearance and native window vibrancy rather than forcing a
/// palette, so this is intentionally a near-noop — kept as a call site so
/// `SingctlApp.swift` doesn't need to change if a future global tweak (e.g.
/// a non-default accent) is needed.
struct AppTheme: ViewModifier {
    func body(content: Content) -> some View {
        content
    }
}

extension View {
    /// Applies the app's root-level appearance hook. See `AppTheme`.
    func appTheme() -> some View { modifier(AppTheme()) }
}

// MARK: - Formatting helpers

/// Byte/rate formatting shared by Dashboard-style stat tiles. Mirrors
/// gui/frontend/src/shared/lib/format.ts's formatBytes/formatRate.
enum ByteFormat {
    private static let units = ["B", "KB", "MB", "GB", "TB", "PB"]

    /// Formats a cumulative byte count, e.g. `formatBytes(1536) == "1.5 KB"`.
    static func bytes(_ value: Int64) -> String {
        var v = Double(value)
        var unitIndex = 0
        while v >= 1024, unitIndex < units.count - 1 {
            v /= 1024
            unitIndex += 1
        }
        let decimals = (unitIndex == 0) ? 0 : 1
        return String(format: "%.\(decimals)f %@", v, units[unitIndex])
    }

    /// Formats a byte-per-second rate, e.g. `formatRate(2048) == "2.0 KB/s"`.
    static func rate(_ value: Double) -> String {
        bytes(Int64(value.rounded())) + "/s"
    }
}
