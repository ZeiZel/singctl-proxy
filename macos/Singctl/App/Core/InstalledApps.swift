// InstalledApps.swift
//
// Native replacement for gui/bridge/installedapps_darwin.go's
// listInstalledApps(): enumerates .app bundles under /Applications,
// /System/Applications and ~/Applications for the "add an app" picker. Unlike
// the Go version (which shells out to `defaults read` per bundle), this reads
// each bundle's Info.plist directly via Bundle(url:) — no process spawn, no
// daemon/control-socket round trip, no root needed.

import Foundation

enum InstalledApps {

    /// The standard macOS locations .app bundles live in. A missing directory
    /// (e.g. no ~/Applications for this user) is simply skipped, not an error.
    static func searchDirectories() -> [URL] {
        var dirs = [
            URL(fileURLWithPath: "/Applications"),
            URL(fileURLWithPath: "/System/Applications"),
        ]
        let home = FileManager.default.homeDirectoryForCurrentUser
        dirs.append(home.appendingPathComponent("Applications"))
        return dirs
    }

    /// Enumerates every .app bundle under the standard directories, resolving
    /// each one's display name and bundle ID from Info.plist. Entries with no
    /// resolvable bundle ID are skipped (routing needs one); bundle IDs are
    /// deduped in case the same app appears under more than one searched
    /// directory. Sorted case-insensitively by name, mirroring the Go version.
    static func scan() async -> [InstalledApp] {
        await Task.detached(priority: .utility) {
            blockingScan()
        }.value
    }

    private static func blockingScan() -> [InstalledApp] {
        let fm = FileManager.default
        var seen = Set<String>()
        var out: [InstalledApp] = []

        for dir in searchDirectories() {
            guard let entries = try? fm.contentsOfDirectory(
                at: dir, includingPropertiesForKeys: nil, options: [.skipsHiddenFiles]
            ) else {
                continue // e.g. dir doesn't exist for this user — not an error
            }
            for entry in entries where entry.pathExtension == "app" {
                guard let bundle = Bundle(url: entry),
                      let bundleID = bundle.bundleIdentifier, !bundleID.isEmpty,
                      !seen.contains(bundleID)
                else { continue }
                seen.insert(bundleID)

                let info = bundle.infoDictionary
                let name = (info?["CFBundleName"] as? String)
                    ?? (info?["CFBundleDisplayName"] as? String)
                    ?? entry.deletingPathExtension().lastPathComponent

                out.append(InstalledApp(name: name, bundleID: bundleID, path: entry.path))
            }
        }

        out.sort { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        return out
    }
}
