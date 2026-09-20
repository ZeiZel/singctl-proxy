// Card.swift
//
// The panel every screen builds on. It is a quiet semantic control surface;
// the window provides the material and the card only adds enough separation
// for grouped content.

import SwiftUI

/// A solid container with an optional title row (title + trailing
/// accessory, e.g. a `Badge` or button) above arbitrary content.
///
/// ```swift
/// Card(title: "Traffic") {
///     TrafficChartView(samples: store.trafficSamples)
/// }
///
/// Card(title: "Connections") {
///     Badge(text: "\(count) active", tone: .accent)
/// } content: {
///     ConnectionsTable(rows: rows)
/// }
/// ```
struct Card<Content: View, Accessory: View>: View {
    private let title: String?
    private let accessory: Accessory
    private let content: Content

    /// - Parameters:
    ///   - title: Optional heading shown above `content`.
    ///   - accessory: Optional trailing view next to the title (badge, button, count…).
    ///   - content: The card body.
    init(
        title: String? = nil,
        @ViewBuilder accessory: () -> Accessory,
        @ViewBuilder content: () -> Content
    ) {
        self.title = title
        self.accessory = accessory()
        self.content = content()
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            if title != nil || Accessory.self != EmptyView.self {
                HStack(alignment: .firstTextBaseline) {
                    if let title {
                        Text(title)
                            .font(.appHeadline)
                            .foregroundStyle(Color.sText)
                    }
                    Spacer()
                    accessory
                }
            }
            content
        }
        .padding(Spacing.cardPad)
        .background(Color.sPanel, in: RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
        .clipShape(RoundedRectangle(cornerRadius: Radius.card, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: Radius.card, style: .continuous)
                .strokeBorder(Color.sBorder.opacity(0.7), lineWidth: 1)
        )
    }
}

/// Convenience initializer for the common case of no trailing accessory.
extension Card where Accessory == EmptyView {
    init(title: String? = nil, @ViewBuilder content: () -> Content) {
        self.init(title: title, accessory: { EmptyView() }, content: content)
    }
}
