// AppButton.swift
//
// The three button styles used across the app: primary (accent-filled),
// danger (destructive actions), and ghost (bordered, low-emphasis). Wraps
// native macOS button styles (`.borderedProminent` / `.bordered`) rather than
// hand-drawn fills/borders.

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
        Group {
            switch kind {
            case .primary:
                button.buttonStyle(.borderedProminent)
            case .ghost:
                button.buttonStyle(.bordered)
            case .danger:
                button.buttonStyle(.bordered).tint(.red)
            }
        }
        .controlSize(.regular)
        .disabled(isDisabled)
    }

    private var button: some View {
        Button(role: kind == .danger ? .destructive : nil, action: action) {
            labelContent
        }
    }

    @ViewBuilder
    private var labelContent: some View {
        if isLoading {
            HStack(spacing: Spacing.xs) {
                ProgressView().controlSize(.small)
                Text(title)
            }
        } else if let icon {
            Label(title, systemImage: icon)
        } else {
            Text(title)
        }
    }
}
