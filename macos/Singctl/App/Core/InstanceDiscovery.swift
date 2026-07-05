// InstanceDiscovery.swift
//
// Mirrors internal/control/instance.go: a running singctl daemon advertises
// itself in ~/.config/singctl/instance.json. Clients (this app included) read
// that file to find the control socket and Clash API address/secret. The file
// is re-read on every call because a daemon restart changes the socket path
// and Clash secret.

import Foundation

/// Mirrors control.Instance (internal/control/instance.go) — the JSON shape a
/// running singctl daemon writes to instance.json.
struct SingctlInstance: Codable, Equatable {
    var pid: Int
    var mode: String
    var logPath: String
    var controlSocket: String
    var clashAPIAddr: String
    var clashSecret: String
    var startedAt: String

    private enum CodingKeys: String, CodingKey {
        case pid
        case mode
        case logPath = "log_path"
        case controlSocket = "control_socket"
        case clashAPIAddr = "clash_api_addr"
        case clashSecret = "clash_secret"
        case startedAt = "started_at"
    }
}

/// Reads and interprets ~/.config/singctl/instance.json, mirroring
/// gui/bridge/daemon.go's Daemon.resolve()/socket(). Every method re-reads the
/// file from disk so a daemon restart (new socket, new Clash secret) is picked
/// up immediately without caching.
enum InstanceDiscovery {

    /// ~/.config/singctl — the daemon's config dir. Matches configDir() in
    /// gui/bridge/daemon.go exactly (HOME-relative, no XDG_CONFIG_HOME override,
    /// since the daemon itself pins HOME for the LaunchDaemon).
    static var configDir: String {
        let home = ProcessInfo.processInfo.environment["HOME"]
            ?? FileManager.default.homeDirectoryForCurrentUser.path
        return (home as NSString).appendingPathComponent(".config/singctl")
    }

    private static var instancePath: String {
        (configDir as NSString).appendingPathComponent("instance.json")
    }

    /// Reads instance.json fresh from disk. Returns nil if absent/unparseable.
    static func readInstance() -> SingctlInstance? {
        guard let data = FileManager.default.contents(atPath: instancePath) else {
            return nil
        }
        return try? JSONDecoder().decode(SingctlInstance.self, from: data)
    }

    /// Probes whether `pid` is a live process, mirroring control.IsAlive: a
    /// signal-0 probe where EPERM (process alive but owned by another user —
    /// the common case for a root LaunchDaemon probed by the per-user app)
    /// still counts as alive; only ESRCH means truly gone.
    static func isAlive(_ pid: Int) -> Bool {
        guard pid > 0 else { return false }
        if kill(pid_t(pid), 0) == 0 {
            return true
        }
        return errno == EPERM
    }

    /// Re-reads instance.json and verifies the advertised PID is still alive.
    /// Returns nil when there is no live daemon (mirrors Daemon.resolve()).
    static func liveInstance() -> SingctlInstance? {
        guard let inst = readInstance(), inst.pid != 0, isAlive(inst.pid) else {
            return nil
        }
        return inst
    }

    /// Convenience bundle of everything a client needs to talk to the daemon
    /// right now: control-socket path plus Clash API address/secret. Returns
    /// nil when no live daemon is found. Always re-resolves from disk.
    struct Endpoint {
        let controlSocket: String
        let clashAPIAddr: String
        let clashSecret: String
        let pid: Int
        let mode: String
        let startedAt: String

        var clashEnabled: Bool { !clashAPIAddr.isEmpty }
    }

    static func currentEndpoint() -> Endpoint? {
        guard let inst = liveInstance(), !inst.controlSocket.isEmpty else {
            return nil
        }
        return Endpoint(
            controlSocket: inst.controlSocket,
            clashAPIAddr: inst.clashAPIAddr,
            clashSecret: inst.clashSecret,
            pid: inst.pid,
            mode: inst.mode,
            startedAt: inst.startedAt
        )
    }
}
