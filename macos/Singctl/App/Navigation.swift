// Navigation.swift
//
// The app's top-level navigation model: one case per sidebar/menu destination.
// Mirrors gui/frontend/src/shared/config's NAV_ITEMS/TPage ordering exactly so
// the native sidebar matches the web GUI.
//
// The App Store build (`#if APPSTORE`) drops the sections whose features
// aren't legal/possible in the sandbox: per-app routing (`connections`,
// `apps`), log file/OSLogStore/console-poll tailing (`logs`) — see
// AppsScreen/ConnectionsScreen/LogsScreen, which are excluded from that
// target entirely (project.yml). The remaining five sections (dashboard,
// proxies, system proxy, keys, settings) work in both builds via `Backend`
// (see Backend.swift) — for `.sysProxy` specifically, "works" means
// `TunnelBackend` reports "unsupported" rather than the tab being hidden,
// same as `.proxies`' PROXY-GROUP/-SELECT in the App Store build.
//
// There used to be a separate `.console` section (stdout/stderr of apps
// launched through the proxy). It was folded into `.logs` as a source filter
// (F5): it was empty for most users forever, and the split from Logs wasn't
// self-evident — see LogsScreen.swift's file-level comment.

import SwiftUI

/// One top-level screen, in sidebar display order. `id` is stable (used for
/// `NavigationSplitView` selection persistence).
enum Section: String, CaseIterable, Identifiable, Hashable {
    case dashboard
    case proxies
    /// The system proxy (PAC + `networksetup`, applied by the daemon) —
    /// present in BOTH builds, same as `.proxies`: `TunnelBackend` reports
    /// "unsupported" for its SYSPROXY-* verbs rather than hiding the tab
    /// (see Backend.swift/TunnelBackend.swift).
    case sysProxy
    #if !APPSTORE
    case connections
    case apps
    #endif
    case keys
    #if !APPSTORE
    case logs
    #endif
    case settings

    var id: String { rawValue }

    /// Sidebar / navigation title.
    var title: String {
        switch self {
        case .dashboard: return "Dashboard"
        case .proxies: return "Proxies"
        case .sysProxy: return "System proxy"
        #if !APPSTORE
        case .connections: return "Connections"
        case .apps: return "Apps"
        #endif
        case .keys: return "Keys"
        #if !APPSTORE
        case .logs: return "Logs"
        #endif
        case .settings: return "Settings"
        }
    }

    /// SF Symbol shown next to the title.
    var symbol: String {
        switch self {
        case .dashboard: return "gauge"
        case .proxies: return "bolt.horizontal"
        case .sysProxy: return "network"
        #if !APPSTORE
        case .connections: return "list.bullet"
        case .apps: return "app.badge"
        #endif
        case .keys: return "key"
        #if !APPSTORE
        case .logs: return "doc.text.magnifyingglass"
        #endif
        case .settings: return "gearshape"
        }
    }
}
