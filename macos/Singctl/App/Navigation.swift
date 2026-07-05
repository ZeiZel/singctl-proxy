// Navigation.swift
//
// The app's top-level navigation model: one case per sidebar/menu destination.
// Mirrors gui/frontend/src/shared/config's NAV_ITEMS/TPage ordering exactly so
// the native sidebar matches the web GUI.

import SwiftUI

/// One top-level screen, in sidebar display order. `id` is stable (used for
/// `NavigationSplitView` selection persistence).
enum Section: String, CaseIterable, Identifiable, Hashable {
    case dashboard
    case proxies
    case connections
    case apps
    case keys
    case console
    case license
    case settings

    var id: String { rawValue }

    /// Sidebar / navigation title.
    var title: String {
        switch self {
        case .dashboard: return "Dashboard"
        case .proxies: return "Proxies"
        case .connections: return "Connections"
        case .apps: return "Apps"
        case .keys: return "Keys"
        case .console: return "Console"
        case .license: return "License"
        case .settings: return "Settings"
        }
    }

    /// SF Symbol shown next to the title.
    var symbol: String {
        switch self {
        case .dashboard: return "gauge"
        case .proxies: return "bolt.horizontal"
        case .connections: return "list.bullet"
        case .apps: return "app.badge"
        case .keys: return "key"
        case .console: return "terminal"
        case .license: return "checkmark.seal"
        case .settings: return "gearshape"
        }
    }
}
