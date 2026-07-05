// EmptyState.swift
//
// Centered placeholder for a table/list/chart with nothing to show yet
// (e.g. "Clash API disabled", "No connections", "No apps added"). Built on
// the native `ContentUnavailableView` so it matches the system's standard
// empty-state presentation.

import SwiftUI

/// ```swift
/// EmptyState(text: "Enable the Clash API in Settings to see live traffic.")
/// EmptyState(text: "No proxied apps yet.", symbol: "app.dashed")
/// ```
struct EmptyState: View {
    let text: String
    var symbol: String?

    /// - Parameters:
    ///   - text: Message shown to the user.
    ///   - symbol: Optional SF Symbol shown above the text.
    init(text: String, symbol: String? = nil) {
        self.text = text
        self.symbol = symbol
    }

    var body: some View {
        Group {
            if let symbol {
                ContentUnavailableView(text, systemImage: symbol)
            } else {
                ContentUnavailableView {
                    Text(text)
                }
            }
        }
        .frame(maxWidth: .infinity, minHeight: 80)
        .padding(Spacing.md)
    }
}
