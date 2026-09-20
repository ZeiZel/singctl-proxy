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
import AppKit

// MARK: - Palette

extension NSColor {
    /// Parses a "#RRGGBB" or "#RRGGBBAA" hex string. Unrecognized input
    /// falls back to opaque black rather than crashing.
    convenience init(hex: Int) {
        let r = CGFloat((hex >> 16) & 0xFF) / 255
        let g = CGFloat((hex >> 8) & 0xFF) / 255
        let b = CGFloat(hex & 0xFF) / 255
        self.init(srgbRed: r, green: g, blue: b, alpha: 1)
    }
}

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

    /// Builds an appearance-adaptive color from a light-mode and dark-mode
    /// hex value (e.g. `0xECEDF1`). Used to define the soft-indigo pastel
    /// palette below — every token picks its light or dark hex depending on
    /// the window's current `NSAppearance`, the same way system dynamic
    /// colors work, but with our own hand-tuned values instead of AppKit's.
    static func dyn(light: Int, dark: Int) -> Color {
        Color(nsColor: NSColor(name: nil) { appearance in
            appearance.bestMatch(from: [.aqua, .darkAqua]) == .darkAqua
                ? NSColor(hex: dark)
                : NSColor(hex: light)
        })
    }

    // Base surfaces — a calm, mostly-opaque soft-indigo pastel palette.
    // Every token is adaptive (light/dark) via `dyn(light:dark:)`, and none
    // ever bottoms out at pure black/white.
    static let sBg = Color.dyn(light: 0xECEDF1, dark: 0x1E2027)
    static let sBgSoft = Color.dyn(light: 0xF3F4F7, dark: 0x24262E)
    static let sPanel = Color.dyn(light: 0xE3E5EB, dark: 0x171921)
    static let sPanelRaised = Color.dyn(light: 0xF3F4F7, dark: 0x2E323D)
    static let sBorder = Color.dyn(light: 0xDDDFE6, dark: 0x383C48)

    // Accents / semantic tones — a single soft-indigo accent plus muted
    // (not vivid) semantic colors, all adaptive between Light and Dark.
    static let sAccent = Color.dyn(light: 0x5B67D8, dark: 0x7C88E8)
    static let sAccentSoft = Color.sAccent.opacity(0.5)
    static let sOk = Color.dyn(light: 0x3F9E77, dark: 0x6FBF9A)
    static let sWarn = Color.dyn(light: 0xC79350, dark: 0xE0B173)
    static let sDanger = Color.dyn(light: 0xCE6B78, dark: 0xE1808C)

    // Text — soft near-black/near-white, never pure #000/#fff.
    static let sText = Color.dyn(light: 0x23262E, dark: 0xE7E8EC)
    static let sTextDim = Color.dyn(light: 0x61656F, dark: 0x9A9FAC)
    static let sTextFaint = Color.dyn(light: 0x9AA0AC, dark: 0x6C7280)
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
    static let xxl: CGFloat = 48
    /// Inner padding of `Card` (between the card edge and its content).
    static let cardPad: CGFloat = 20
}

/// Corner-radius scale. `.card` mirrors Tailwind's `rounded-card` (12px).
enum Radius {
    static let sm: CGFloat = 6
    static let md: CGFloat = 9
    static let card: CGFloat = 12
}

// MARK: - Typography

/// Shared font tokens. `.controlSize(.large)` doesn't enlarge `Text`, so
/// components apply these explicitly to get a visibly larger, more
/// legible scale than the system defaults. Screens/tables (later wave)
/// reuse these same tokens.
extension Font {
    // Type scale — the single source of truth. TWO sizes only: 20 for
    // headers/titles, 16 for everything else; extra hierarchy comes from
    // WEIGHT. Reuse these tokens across every component/screen; never use
    // raw SwiftUI semantic fonts (they're <16 on macOS) or ad-hoc sizes.
    static let appTitle = Font.system(size: 20, weight: .semibold)     // page / section titles
    static let appHeadline = Font.system(size: 20, weight: .semibold)  // card / block headers
    static let appValue = Font.system(size: 16, weight: .medium)       // numeric / stat values
    static let appBody = Font.system(size: 16)                         // primary text, nav items
    static let appSecondary = Font.system(size: 16)                    // secondary / dim labels (pair with .secondary color)
    static let appCaption = Font.system(size: 16)                      // hints / axis (smallest allowed)
}

// MARK: - Scene modifier

/// Root-level hook for app-wide appearance. This is the ONE place the app's
/// accent tint is set — applying `Color.sAccent` here means every default
/// `.tint`-driven control (prominent buttons, selection, toggles, …) picks
/// up the same soft-indigo accent instead of the raw system blue.
struct AppTheme: ViewModifier {
    func body(content: Content) -> some View {
        content.tint(Color.sAccent)
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

/// Duration formatting for connection age (Connections screen). Coarsest
/// non-zero unit first, mirroring `ByteFormat`'s "one decimal, biggest unit
/// that fits" spirit but for whole seconds/minutes/hours.
enum DurationFormat {
    /// Formats an elapsed interval, e.g. `short(75) == "1m 15s"`.
    static func short(_ seconds: TimeInterval) -> String {
        guard seconds.isFinite, seconds >= 0 else { return "—" }
        let total = Int(seconds)
        let h = total / 3600
        let m = (total % 3600) / 60
        let s = total % 60
        if h > 0 { return String(format: "%dh %02dm", h, m) }
        if m > 0 { return String(format: "%dm %02ds", m, s) }
        return "\(s)s"
    }
}

// MARK: - App version

extension Bundle {
    /// The app's marketing version (CFBundleShortVersionString), e.g. "1.4.1",
    /// shown in the sidebar footer and Settings → About. Falls back to "—" when
    /// the key is somehow absent.
    var appVersion: String {
        infoDictionary?["CFBundleShortVersionString"] as? String ?? "—"
    }
}
