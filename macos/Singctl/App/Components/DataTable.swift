// DataTable.swift
//
// Reusable table for the list-heavy screens (Connections, Proxies, Apps,
// Keys, Console…): a sticky header row of column labels, an optional filter
// field, and rows you draw yourself via `rowContent`. Callers own filtering —
// pass `searchText` and filter `rows` before handing them in (this keeps
// DataTable generic instead of baking in per-screen match logic).

import SwiftUI

/// - Parameters (generic over `Row: Identifiable`):
///   - columns: Header labels, left-to-right. Purely cosmetic — row layout is
///     up to `rowContent`, so column widths should be aligned by eye/spacers.
///   - rows: Already-filtered/sorted data to render, one `rowContent` call each.
///   - searchText: Optional binding; when non-nil a filter `TextField` is shown
///     above the table and bound to it. Pass `nil` to omit the field entirely.
///   - searchPlaceholder: Placeholder for the filter field.
///   - rowContent: Builds one row's view from a `Row`.
///
/// ```swift
/// DataTable(
///     columns: ["Process", "Source", "Destination", "Chain"],
///     rows: filteredConnections,
///     searchText: $filter
/// ) { row in
///     HStack {
///         Text(row.process).frame(width: 140, alignment: .leading)
///         Text(row.source).frame(width: 160, alignment: .leading)
///         Text(row.dest).frame(maxWidth: .infinity, alignment: .leading)
///         Text(row.chain).foregroundStyle(Color.sTextDim)
///     }
/// }
/// ```
struct DataTable<Row: Identifiable, RowContent: View>: View {
    let columns: [String]
    let rows: [Row]
    var searchText: Binding<String>?
    var searchPlaceholder: String = "Filter…"
    @ViewBuilder let rowContent: (Row) -> RowContent

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.sm) {
            if let searchText {
                HStack(spacing: Spacing.xs) {
                    Image(systemName: "magnifyingglass")
                        .foregroundStyle(Color.sTextFaint)
                    TextField(searchPlaceholder, text: searchText)
                        .textFieldStyle(.plain)
                }
                .padding(.horizontal, Spacing.sm)
                .padding(.vertical, 6)
                .background(Color.sBgSoft)
                .clipShape(RoundedRectangle(cornerRadius: Radius.sm, style: .continuous))
            }

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 0, pinnedViews: [.sectionHeaders]) {
                    SwiftUI.Section {
                        ForEach(rows) { row in
                            rowContent(row)
                                .padding(.horizontal, Spacing.sm)
                                .padding(.vertical, Spacing.sm)
                                .font(.system(.subheadline, design: .monospaced))
                            Divider().overlay(Color.sBorder)
                        }
                        if rows.isEmpty {
                            EmptyState(text: "No rows.")
                        }
                    } header: {
                        HStack {
                            ForEach(columns, id: \.self) { column in
                                Text(column.uppercased())
                                    .font(.caption2.weight(.semibold))
                                    .tracking(0.5)
                                    .foregroundStyle(Color.sTextDim)
                                    .frame(maxWidth: column == columns.last ? .infinity : nil, alignment: .leading)
                                if column != columns.last { Spacer(minLength: Spacing.md) }
                            }
                        }
                        .padding(.horizontal, Spacing.sm)
                        .padding(.vertical, Spacing.xs)
                        .background(Color.sPanelRaised)
                    }
                }
            }
        }
    }
}
