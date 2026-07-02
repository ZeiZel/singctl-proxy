// main.swift — singctl container app (SCAFFOLD).
//
// Minimal AppKit entry point whose job is to activate the system extension and
// keep the transparent-proxy manager configured from the shared config.json that
// the singctl CLI writes (App Group container). In the real product this folds
// into the singctl macOS helper; kept tiny here so the scaffold compiles and
// demonstrates the activation + live-reconfigure flow.
//
// STATUS: skeleton. The activation/approval UX is intentionally minimal — see
// SystemExtensionActivator.requestNeedsUserApproval.

import AppKit
import Dispatch
import Foundation
import os.log

/// Mirror of the Go-side netext.Config contract (config.example.json).
private struct ProxyConfig: Decodable {
    let targets: [String]
    let socksHost: String?
    let socksPort: Int?
}

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let log = OSLog(subsystem: "com.singctl.proxy", category: "app")
    private let activator = SystemExtensionActivator()
    private var configSource: DispatchSourceFileSystemObject?

    // ~/Library/Group Containers/group.com.singctl.proxy/config.json — the file
    // the CLI writes (internal/netext). Kept in one place so it stays in sync.
    private static let appGroup = "group.com.singctl.proxy"
    private var configURL: URL? {
        FileManager.default
            .containerURL(forSecurityApplicationGroupIdentifier: Self.appGroup)?
            .appendingPathComponent("config.json")
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        os_log("activating system extension…", log: log, type: .info)
        activator.activate { [weak self] result in
            switch result {
            case .success:
                self?.applyConfig()      // initial push
                self?.watchConfig()      // re-apply on every CLI write
            case .failure(let error):
                os_log("activation failed: %{public}@", log: self?.log ?? .default,
                       type: .error, error.localizedDescription)
            }
        }
    }

    /// Read targets from the shared config.json and push them to the manager.
    private func applyConfig() {
        guard let url = configURL else {
            os_log("no App Group container — is the app entitled for %{public}@?",
                   log: log, type: .error, Self.appGroup)
            return
        }
        let cfg: ProxyConfig
        do {
            let data = try Data(contentsOf: url)
            cfg = try JSONDecoder().decode(ProxyConfig.self, from: data)
        } catch {
            // Missing/empty config is normal before the CLI writes anything.
            os_log("config not readable yet (%{public}@): %{public}@",
                   log: log, type: .info, url.path, error.localizedDescription)
            return
        }
        activator.configure(targets: cfg.targets,
                            socksHost: cfg.socksHost ?? "127.0.0.1",
                            socksPort: cfg.socksPort ?? 1080) { [weak self] error in
            if let error = error {
                os_log("configure failed: %{public}@", log: self?.log ?? .default,
                       type: .error, error.localizedDescription)
            } else {
                os_log("configured %d target(s)", log: self?.log ?? .default,
                       type: .info, cfg.targets.count)
            }
        }
    }

    /// Watch config.json for writes so the CLI can change the target set live
    /// without relaunching the app or the extension.
    ///
    /// Note: the container app runs in the user session, so `configURL` above
    /// resolves to the *user's* `~/Library/Group Containers/...` — while the
    /// provider (root) and the root daemon see
    /// `/var/root/Library/Group Containers/...`. Those are different files.
    /// The provider's own file-watch (TransparentProxyProvider.startWatchingConfigFile)
    /// is therefore the authoritative live-reload channel; this watcher only
    /// covers configs written from the user session.
    private func watchConfig() {
        guard let url = configURL else { return }
        // Ensure the directory exists so we can open a descriptor on the file.
        try? FileManager.default.createDirectory(at: url.deletingLastPathComponent(),
                                                withIntermediateDirectories: true)
        let fd = open(url.path, O_EVTONLY)
        guard fd >= 0 else {
            os_log("cannot watch %{public}@ (errno %d)", log: log, type: .error, url.path, errno)
            return
        }
        let src = DispatchSource.makeFileSystemObjectSource(
            fileDescriptor: fd, eventMask: [.write, .rename, .delete], queue: .main)
        src.setEventHandler { [weak self] in self?.applyConfig() }
        src.setCancelHandler { close(fd) }
        src.resume()
        configSource = src
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory) // menu-bar/background helper, no dock icon
app.run()
