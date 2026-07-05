// ConsoleScreen.swift
//
// A live terminal view over `LiveStore.console` (already streamed by the 2s
// poll loop's CONSOLE-POLL calls — see LiveStore.swift). Purely a reader of
// shared state: no ControlClient verb calls originate here. Filterable by the
// originating app, auto-scrolling to the newest line.

import SwiftUI

struct ConsoleScreen: View {
    @EnvironmentObject private var store: LiveStore

    private static let allAppsLabel = "All apps"

    @State private var selectedApp = ConsoleScreen.allAppsLabel

    private var appOptions: [String] {
        let apps = Set(store.console.map(\.app)).filter { !$0.isEmpty }
        return [Self.allAppsLabel] + apps.sorted()
    }

    private var filteredLines: [ConsoleLine] {
        guard selectedApp != Self.allAppsLabel else { return store.console }
        return store.console.filter { $0.app == selectedApp }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Spacing.md) {
            header
            Card {
                if filteredLines.isEmpty {
                    EmptyState(
                        text: "No console output yet. Launch an app through the proxy to see its stdout/stderr here.",
                        symbol: "terminal"
                    )
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    terminal
                }
            }
            .frame(maxHeight: .infinity)
        }
        .padding(Spacing.lg)
    }

    // MARK: - Header

    private var header: some View {
        SectionHeader(title: "Console", subtitle: "Live stdout/stderr from proxied apps") {
            Picker("App", selection: $selectedApp) {
                ForEach(appOptions, id: \.self) { app in
                    Text(app).tag(app)
                }
            }
            .pickerStyle(.menu)
            .frame(maxWidth: 220)
        }
    }

    // MARK: - Terminal

    private var terminal: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 2) {
                    ForEach(filteredLines) { line in
                        consoleLineView(line).id(line.id)
                    }
                }
                .padding(Spacing.sm)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .background(Color(nsColor: .textBackgroundColor))
            .clipShape(RoundedRectangle(cornerRadius: Radius.md, style: .continuous))
            .frame(maxHeight: .infinity)
            .onAppear { scrollToBottom(proxy) }
            .onChange(of: filteredLines.last?.id) { _, _ in scrollToBottom(proxy) }
            .onChange(of: selectedApp) { _, _ in scrollToBottom(proxy) }
        }
    }

    private func scrollToBottom(_ proxy: ScrollViewProxy) {
        guard let lastID = filteredLines.last?.id else { return }
        withAnimation(.easeOut(duration: 0.15)) {
            proxy.scrollTo(lastID, anchor: .bottom)
        }
    }

    private func consoleLineView(_ line: ConsoleLine) -> some View {
        HStack(alignment: .top, spacing: Spacing.xs) {
            Text("[\(line.app)|\(line.pid)]")
                .foregroundStyle(Color.sTextFaint)
            Text(line.text)
                .foregroundStyle(color(for: line))
                .textSelection(.enabled)
        }
        .font(.system(.callout, design: .monospaced))
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func color(for line: ConsoleLine) -> Color {
        switch line.stream {
        case "stderr": return .sWarn
        case "exit": return .sTextFaint
        default: return .sText
        }
    }
}
