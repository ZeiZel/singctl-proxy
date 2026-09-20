// Package sysproxy owns the macOS system-proxy (PAC — Proxy Auto-Config)
// toggle, closing the gap with the neighbouring mac-proxy Makefile/scripts
// (`proxy-on`, `proxy-pac`, `proxy-off`, `proxy-status`, `gen-pac.sh`,
// `gen-exclude-pac.sh`) so singctl is a full replacement for it — including
// the bypass-domain list and secure-web-proxy status those scripts also
// drove, which this package now covers too (see networksetup.go).
//
// Four pieces:
//
//   - PAC generation (pac.go): GeneratePAC renders the include-mode
//     (allowlist-only) or exclude-mode (proxy-everything-except) PAC script
//     from a Config, semantically identical to the original shell
//     generators — same corporate/RFC1918/CGNAT/link-local/.ru/.xn--p1ai/
//     simple-hostname carve-outs, same domain+subdomain suffix matching —
//     except those carve-outs are now DATA (Config.Direct), not code; see
//     DefaultDirectRules. It stays human-readable, since the .pac file it
//     produces is user-inspectable.
//
//   - Config (config.go) + the INI rules format (ini.go): the mode
//     (off/exclude/include), the local proxy the PAC points at, the [proxy]
//     (force-through) and [direct] (never-proxy) rule lists — each a mix of
//     bare domains, glob patterns, and CIDRs — the bypass_plain_hostnames
//     toggle, the macOS network service name, and the localhost PAC-server
//     port. INI (ini.go's doc comment has the full grammar) is the primary
//     serialization SYSPROXY-CONFIG returns and SYSPROXY-IMPORT prefers;
//     legacy YAML and a plain newline-separated domain list (what
//     packaging/macos/proxy-domains.txt is, and what a user will paste) are
//     still accepted on import — see DecodeImport.
//
//   - System application (manager.go, networksetup.go,
//     networksetup_darwin.go): Manager applies a Config against an injected
//     NetworkSetup port, which the real darwin adapter backs with
//     `networksetup(8)` — including `-setproxybypassdomains` (set AND read)
//     and `-getsecurewebproxy`, both previously missing. The injection keeps
//     everything else in this package unit-testable with no `networksetup`
//     binary, no macOS, and no root on the test-running machine.
//
//   - PAC server (pacserver.go): a tiny in-process localhost HTTP server
//     replacing the Makefile's separate LaunchAgent.
//
// SAFETY: this package never changes the system proxy on its own. There are
// no timers, no file watchers, no init-time side effects — Manager.Apply and
// Manager.Import only touch `networksetup` state when a caller explicitly
// invokes them (in practice: the daemon's SYSPROXY-SET / SYSPROXY-IMPORT
// control commands, themselves only reachable from an explicit user action in
// the GUI). A freshly constructed Manager starts in Config.Mode == ModeOff
// and has not run a single `networksetup` command. Manager.Apply also
// validates the whole Config before touching any system state at all — an
// invalid Config (bad mode, bad CIDR, ...) is rejected atomically, never
// half-applied.
package sysproxy
