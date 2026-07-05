// AppButton.swift
//
// The three button styles used across the app: primary (accent-filled),
// danger (red-filled, destructive actions), and ghost (bordered, low-emphasis).

import SwiftUI

/// ```swift
/// AppButton("Add key", icon: "plus") { addKey() }
/// AppButton("Remove", kind: .danger, icon: "trash") { remove(app) }
/// AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise", isLoading: refreshing) { refresh() }
/// ```
struct AppButton: View {
    enum Kind { case primary, danger, ghost }

    let title: String
    var kind: Kind = .primary
    var icon: String?
    var isLoading: Bool = false
    var disabled: Bool = false
    let action: () -> Void

    /// - Parameters:
    ///   - title: Button label.
    ///   - kind: Visual emphasis — `.primary`, `.danger`, or `.ghost`.
    ///   - icon: Optional leading SF Symbol.
    ///   - isLoading: Shows a small spinner instead of the icon and disables the button.
    ///   - disabled: Disables the button (independent of `isLoading`).
    ///   - action: Tap handler.
    init(
        _ title: String, kind: Kind = .primary, icon: String? = nil,
        isLoading: Bool = false, disabled: Bool = false, action: @escaping () -> Void
    ) {
        self.title = title
        self.kind = kind
        self.icon = icon
        self.isLoading = isLoading
        self.disabled = disabled
        self.action = action
    }

    private var isDisabled: Bool { disabled || isLoading }

    var body: some View {
        Button(action: action) {
            HStack(spacing: Spacing.xs) {
                if isLoading {
                    ProgressView().controlSize(.small).tint(foreground)
                } else if let icon {
                    Image(systemName: icon)
                }
                Text(title)
            }
            .font(.subheadline.weight(.semibold))
            .foregroundStyle(foreground)
            .padding(.horizontal, Spacing.md)
            .padding(.vertical, 8)
            .background(background)
            .clipShape(RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Radius.md, style: .continuous)
                    .strokeBorder(border, lineWidth: kind == .ghost ? 1 : 0)
            )
        }
        .buttonStyle(.plain)
        .opacity(isDisabled ? 0.5 : 1)
        .allowsHitTesting(!isDisabled)
    }

    private var foreground: Color {
        switch kind {
        case .primary, .danger: return .white
        case .ghost: return .sText
        }
    }

    private var background: Color {
        switch kind {
        case .primary: return .sAccent
        case .danger: return .sDanger
        case .ghost: return .sPanelRaised
        }
    }

    private var border: Color {
        kind == .ghost ? .sBorder : .clear
    }
}
