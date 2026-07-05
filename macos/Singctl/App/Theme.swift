// Theme.swift
//
// The design system's tokens: palette, spacing/radius scale, and small
// formatting helpers shared by every screen. Mirrors gui/frontend's Tailwind
// theme (gui/frontend/tailwind.config.js) exactly so the native app reads as
// the same product as the web GUI. Dark-only — see `.appTheme()` below.

import SwiftUI

// MARK: - Palette

extension Color {

    /// Parses a "#RRGGBB" or "#RRGGBBAA" hex string. Unrecognized input
    /// falls back to opaque black rather than crashing.
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

    // Base surfaces
    static let sBg = Color(hex: "#111318")
    static let sBgSoft = Color(hex: "#171a21")
    static let sPanel = Color(hex: "#1c2029")
    static let sPanelRaised = Color(hex: "#222734")
    static let sBorder = Color(hex: "#2a3040")

    // Accents / semantic tones
    static let sAccent = Color(hex: "#5b8cff")
    static let sAccentSoft = Color(hex: "#7c5bff")
    static let sOk = Color(hex: "#3ecf8e")
    static let sDanger = Color(hex: "#ff5d6c")
    static let sWarn = Color(hex: "#ffb454")

    // Text
    static let sText = Color(hex: "#e6e9ef")
    static let sTextDim = Color(hex: "#9aa3b2")
    static let sTextFaint = Color(hex: "#6b7280")
}

// MARK: - Tone

/// A semantic color used by `Badge`, `StatusDot`, `StatTile`, etc. — keeps
/// call sites free of raw `Color` literals.
enum Tone {
    case `default`, dim, accent, ok, warn, danger

    var color: Color {
        switch self {
        case .default: return .sText
        case .dim: return .sTextDim
        case .accent: return .sAccent
        case .ok: return .sOk
        case .warn: return .sWarn
        case .danger: return .sDanger
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

/// Applies the app-wide dark appearance. Call once at the root scene/view.
struct AppTheme: ViewModifier {
    func body(content: Content) -> some View {
        content
            .preferredColorScheme(.dark)
            .tint(.sAccent)
            .foregroundStyle(Color.sText)
    }
}

extension View {
    /// Enforces the dark palette + accent tint used throughout the app.
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
