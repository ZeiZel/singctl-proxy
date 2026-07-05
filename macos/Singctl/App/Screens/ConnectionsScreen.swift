// ConnectionsScreen.swift
//
// Mirrors gui/frontend's Connections page (ConnectionsTable): the live
// connection table from the Clash API, filterable by process/destination/
// chain. Read-only — data comes straight from `LiveStore.connections`, which
// is refreshed by the 2s poll loop.
//
// Native migration: a single SwiftUI `Table` (resizable + sortable columns,
// fills the detail pane) with a `.searchable` filter, replacing the custom
// `DataTable`. `ContentUnavailableView` (via `EmptyState`) covers the empty
// case.

import SwiftUI

struct ConnectionsScreen: View {
    @EnvironmentObject private var store: LiveStore

    @State private var filter = ""
    @State private var sortOrder = [KeyPathComparator(\ConnRow.process)]

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            SectionHeader(title: "Connections", subtitle: "\(store.connections.count) active")

            if store.connections.isEmpty {
                EmptyState(text: "No active connections.", symbol: "network.slash")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else if rows.isEmpty {
                EmptyState(text: "No connections match your search.", symbol: "magnifyingglass")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                table
            }
        }
        .padding(Spacing.lg)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .searchable(text: $filter, prompt: "Filter by process / host / chain")
    }

    private var table: some View {
        Table(rows, sortOrder: $sortOrder) {
            TableColumn("Process", value: \.process) { row in
                Text(row.process.isEmpty ? "?" : row.process)
                    .font(.body)
                    .foregroundStyle(Color.sText)
            }
            .width(min: 100, ideal: 150)

            TableColumn("Source", value: \.source) { row in
                Text(row.source).font(.body).foregroundStyle(Color.sTextDim)
            }
            .width(min: 120, ideal: 170)

            TableColumn("Destination", value: \.dest) { row in
                Text(row.dest).font(.body).foregroundStyle(Color.sText)
            }
            .width(min: 160, ideal: 260)

            TableColumn("Net", value: \.network) { row in
                Text(row.network).font(.body).foregroundStyle(Color.sTextDim)
            }
            .width(min: 44, ideal: 60, max: 90)

            TableColumn("Chain", value: \.chain) { row in
                Text(row.chain)
                    .font(.system(.callout, design: .monospaced))
                    .foregroundStyle(Color.sTextDim)
            }
            .width(min: 100, ideal: 170)
        }
        .tableStyle(.inset)
        .controlSize(.large)
    }

    /// Search-filtered, then sorted by the active column order.
    private var rows: [ConnRow] {
        let query = filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        let base = query.isEmpty
            ? store.connections
            : store.connections.filter {
                $0.process.lowercased().contains(query)
                    || $0.dest.lowercased().contains(query)
                    || $0.chain.lowercased().contains(query)
            }
        return base.sorted(using: sortOrder)
    }
}
