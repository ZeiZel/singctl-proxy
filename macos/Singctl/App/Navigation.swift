// Navigation.swift
//
// The app's top-level navigation model: one case per sidebar/menu destination.
// Mirrors gui/frontend/src/shared/config's NAV_ITEMS/TPage ordering exactly so
// the native sidebar matches the web GUI.
//
// The App Store build (`#if APPSTORE`) drops the sections whose features
// aren't legal/possible in the sandbox: per-app routing (`connections`,
// `apps`), console streaming (`console`), and CLI-driven license activation
// (`license`) — see AppsScreen/ConnectionsScreen/ConsoleScreen/LicenseScreen,
// which are excluded from that target entirely (project.yml). The remaining
// four sections (dashboard, proxies, keys, settings) work in both builds via
// `Backend` (see Backend.swift).

import SwiftUI

/// One top-level screen, in sidebar display order. `id` is stable (used for
/// `NavigationSplitView` selection persistence).
enum Section: String, CaseIterable, Identifiable, Hashable {
    case dashboard
    case proxies
    #if !APPSTORE
    case connections
    case apps
    #endif
    case keys
    #if !APPSTORE
    case console
    case license
    #endif
    case settings

    var id: String { rawValue }

    /// Sidebar / navigation title.
    var title: String {
        switch self {
        case .dashboard: return "Dashboard"
        case .proxies: return "Proxies"
        #if !APPSTORE
        case .connections: return "Connections"
        case .apps: return "Apps"
        #endif
        case .keys: return "Keys"
        #if !APPSTORE
        case .console: return "Console"
        case .license: return "License"
        #endif
        case .settings: return "Settings"
        }
    }

    /// SF Symbol shown next to the title.
    var symbol: String {
        switch self {
        case .dashboard: return "gauge"
        case .proxies: return "bolt.horizontal"
        #if !APPSTORE
        case .connections: return "list.bullet"
        case .apps: return "app.badge"
        #endif
        case .keys: return "key"
        #if !APPSTORE
        case .console: return "terminal"
        case .license: return "checkmark.seal"
        #endif
        case .settings: return "gearshape"
        }
    }
}
