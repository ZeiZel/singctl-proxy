// EmptyState.swift
//
// Centered placeholder for a table/list/chart with nothing to show yet
// (e.g. "Clash API disabled", "No connections", "No apps added").

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
        VStack(spacing: Spacing.sm) {
            if let symbol {
                Image(systemName: symbol)
                    .font(.system(size: 28))
                    .foregroundStyle(Color.sTextFaint)
            }
            Text(text)
                .font(.subheadline)
                .foregroundStyle(Color.sTextDim)
                .multilineTextAlignment(.center)
        }
        .frame(maxWidth: .infinity, minHeight: 80)
        .padding(Spacing.md)
    }
}
