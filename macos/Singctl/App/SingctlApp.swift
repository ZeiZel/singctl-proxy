// SingctlApp.swift — @main entry point (SCAFFOLD).
//
// Minimal stub so the Singctl target builds. This app folds together what
// used to be two separate binaries (packaging/macos/netextension's
// ContainerApp + the system extension host): one regular windowed app that
// ALSO activates and configures the embedded ProxyExtension system
// extension, plus a menu-bar item for quick access.
//
// STATUS: placeholder only — a later phase wires real UI (App/Core owns the
// data/activation layer). Do not add screens or Core references here.

import SwiftUI

@main
struct SingctlApp: App {
    var body: some Scene {
        WindowGroup {
            Text("Singctl")
        }

        MenuBarExtra("Singctl", systemImage: "shield") {
            Text("Singctl")
        }
    }
}
