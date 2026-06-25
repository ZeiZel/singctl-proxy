// main.swift — singctl container app (SCAFFOLD).
//
// Minimal AppKit entry point whose only job is to activate the system extension
// and configure the transparent-proxy manager. In the real product this is
// folded into the singctl macOS helper; kept tiny here so the scaffold compiles
// and demonstrates the activation flow.
//
// STATUS: skeleton. Replace the hard-coded demo targets with the set singctl
// writes (the leaky-editors default, or the user's per-app picks), and add real
// UI for the approval step (see SystemExtensionActivator.requestNeedsUserApproval).

import AppKit
import os.log

final class AppDelegate: NSObject, NSApplicationDelegate {
    private let log = OSLog(subsystem: "com.singctl.proxy", category: "app")
    private let activator = SystemExtensionActivator()

    func applicationDidFinishLaunching(_ notification: Notification) {
        os_log("activating system extension…", log: log, type: .info)
        activator.activate()

        // TODO: gate this on the activation result (.completed) and read the
        // target set from singctl rather than hard-coding. Bundle IDs shown are
        // examples (Cursor, VS Code).
        activator.configure(targets: [
            "com.todesktop.230313mzl4w4u92", // Cursor
            "com.microsoft.VSCode",
        ]) { [weak self] error in
            if let error = error {
                os_log("configure failed: %{public}@", log: self?.log ?? .default,
                       type: .error, error.localizedDescription)
            }
        }
    }
}

let app = NSApplication.shared
let delegate = AppDelegate()
app.delegate = delegate
app.setActivationPolicy(.accessory) // menu-bar/background helper, no dock icon
app.run()
