// SectionHeader.swift
//
// A screen/section title row: big title, optional dim subtitle, optional
// trailing accessory (e.g. a refresh button or badge). Use once per screen,
// at the top, outside any `Card`. Uses native title typography and semantic
// label colors (`.primary`/`.secondary`).

import SwiftUI

/// ```swift
/// SectionHeader(title: "Dashboard")
///
/// SectionHeader(title: "Connections", subtitle: "12 active") {
///     AppButton("Refresh", kind: .ghost, icon: "arrow.clockwise") { refresh() }
/// }
/// ```
struct SectionHeader<Accessory: View>: View {
    private let title: String
    private let subtitle: String?
    private let accessory: Accessory

    /// - Parameters:
    ///   - title: Main heading text.
    ///   - subtitle: Optional dim line under the title.
    ///   - accessory: Optional trailing content, vertically centered with the title.
    init(title: String, subtitle: String? = nil, @ViewBuilder accessory: () -> Accessory) {
        self.title = title
        self.subtitle = subtitle
        self.accessory = accessory()
    }

    var body: some View {
        HStack(alignment: .center) {
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(.appTitle)
                    .foregroundStyle(Color.sText)
                if let subtitle {
                    Text(subtitle)
                        .font(.appSecondary)
                        .foregroundStyle(Color.sTextDim)
                }
            }
            Spacer()
            accessory
        }
    }
}

extension SectionHeader where Accessory == EmptyView {
    init(title: String, subtitle: String? = nil) {
        self.init(title: title, subtitle: subtitle, accessory: { EmptyView() })
    }
}
