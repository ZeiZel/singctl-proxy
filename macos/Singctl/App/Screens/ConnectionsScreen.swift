// ConnectionsScreen.swift
//
// Mirrors gui/frontend's Connections page (ConnectionsTable): the live
// connection table from the Clash API, filterable by process/destination/
// chain. Read-only — data comes straight from `LiveStore.connections`, which
// is refreshed by the 2s poll loop.

import SwiftUI

struct ConnectionsScreen: View {
    @EnvironmentObject private var store: LiveStore

    @State private var filter = ""

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: Spacing.md) {
                SectionHeader(title: "Connections", subtitle: "\(store.connections.count) active")
                connectionsCard
            }
            .padding(Spacing.lg)
        }
    }

    private var connectionsCard: some View {
        Card {
            if store.connections.isEmpty {
                EmptyState(text: "No active connections.")
            } else {
                DataTable(
                    columns: ["Process", "Source", "Destination", "Net", "Chain"],
                    rows: filteredRows,
                    searchText: $filter,
                    searchPlaceholder: "Filter by process / host…"
                ) { row in
                    HStack(alignment: .firstTextBaseline, spacing: Spacing.md) {
                        Text(row.process.isEmpty ? "?" : row.process)
                            .font(.subheadline)
                            .foregroundStyle(Color.sText)
                            .frame(width: 140, alignment: .leading)
                        Text(row.source)
                            .frame(width: 160, alignment: .leading)
                        Text(row.dest)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        Text(row.network)
                            .font(.subheadline)
                            .foregroundStyle(Color.sTextDim)
                            .frame(width: 60, alignment: .leading)
                        Text(row.chain)
                            .font(.system(.subheadline, design: .monospaced))
                            .foregroundStyle(Color.sTextDim)
                            .frame(minWidth: 120, alignment: .leading)
                    }
                }
                .frame(minHeight: 320)
            }
        }
    }

    private var filteredRows: [ConnRow] {
        let query = filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else { return store.connections }
        return store.connections.filter {
            $0.process.lowercased().contains(query)
                || $0.dest.lowercased().contains(query)
                || $0.chain.lowercased().contains(query)
        }
    }
}
