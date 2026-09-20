# App Store SKU (Phase 6) — sandboxed NEPacketTunnelProvider

> Status: **design + scaffold**. This SKU is a separate, reduced-capability build
> that cannot be developed or verified from the Go/Linux container — it is Swift /
> Xcode / NetworkExtension work needing a Mac, an Apple Developer account with the
> **Network Extensions** capability granted, and App Store Connect. Bead
> `singctl-proxy-8zz`.

## Why a separate SKU

The shipping (Developer-ID) product is a **root daemon** (LaunchDaemon/systemd)
that owns a TUN device, manipulates routes, and does per-app routing via
cgroup/nftables (Linux) or a system extension (macOS). The unprivileged Wails GUI
drives it over a Unix control socket. **None of that is allowed inside the App
Store sandbox**: no root, no setuid helpers, no route table edits, no system
extension, no arbitrary Unix sockets to a privileged process. clash-verge-rev hits
the same wall and ships Developer-ID-only.

The App Store-legal way to ship a VPN is a **`NEPacketTunnelProvider`** packaged
as an **app extension (appex)** bundled in a sandboxed container app, driven by
**`NETunnelProviderManager`**. This is exactly how sing-box's own Apple app
(SFI/SFM) ships. We mirror that model.

## Feature delta vs the Developer-ID build

| Capability | Developer-ID (shipping) | App Store SKU |
| --- | --- | --- |
| System-wide VPN (TUN) | ✅ root daemon | ✅ NEPacketTunnelProvider |
| VLESS multi-key failover (urltest) | ✅ | ✅ (same sing-box core) |
| Live connections / traffic / latency | ✅ Clash API | ✅ via Libbox command client |
| **Per-app routing** (cgroup/nft / system extension) | ✅ | ❌ not possible in sandbox |
| Root daemon + control socket + `--attach`/CLI | ✅ | ❌ replaced by NETunnelProviderManager |
| Boot-start service | ✅ | ⚠️ "on-demand" VPN rules only |
| Cisco AnyConnect coexistence (observe-only) | ✅ | ⚠️ limited (no route inspection in sandbox) |

The App Store SKU is **system-wide VPN only** — no per-app proxying.

## Architecture

```
┌────────────────────────────┐      NETunnelProviderManager      ┌────────────────────────────┐
│ Container app (SwiftUI,     │  save config / start / stop / status │ PacketTunnelProvider (appex)│
│ sandboxed) — keys, mode,    │ ───────────────────────────────▶ │  NEPacketTunnelProvider     │
│ status, traffic chart       │                                   │   └─ Libbox (sing-box core) │
│ keys in App Group container │ ◀──── status / stats (IPC) ────── │       runs on packetFlow fd │
└────────────────────────────┘                                   └────────────────────────────┘
```

1. **Go core as a library (`Libbox`).** Reuse sing-box's official
   `github.com/sagernet/sing-box/experimental/libbox`, built to an
   `Libbox.xcframework` via `gomobile bind`. It runs a sing-box instance over the
   tunnel file descriptor the appex provides and exposes a command client for
   status/stats. We add a tiny gomobile-bound Go package (`mobile/`, see below)
   that turns the user's VLESS key(s) + settings into a sing-box config JSON by
   **reusing the existing pure packages** `internal/vless` + `internal/singbox`
   (the same config the daemon builds), so the two SKUs stay byte-compatible.
2. **PacketTunnelProvider appex.** On `startTunnel`, reads the config from the App
   Group container, builds the platform tun options from `NEPacketTunnelFlow`,
   and hands the JSON to Libbox to run. On `stopTunnel`, stops Libbox.
3. **Container app (SwiftUI).** Sandboxed. Manages a `NETunnelProviderManager`
   (install/enable the VPN profile, start/stop, observe status), edits keys +
   settings stored in the App Group container, and renders status/traffic from
   the Libbox command client. (The existing Wails/React UI could later be hosted
   in a `WKWebView` talking to Swift instead of the daemon, but Phase 6 ships a
   native SwiftUI app for the shortest path to review.)

### Reused, unchanged
`internal/vless` (parse keys) and `internal/singbox` (build config JSON) are pure
and already the single source of truth for the config. The `mobile/` package is a
thin gomobile-friendly wrapper: `func BuildConfig(keysJSON, settingsJSON string)
(string, error)`. No sing-box import leaks into them (only `mobile/` + Libbox link
the core).

**Exception: `type=xhttp` keys are not usable in this SKU.** singctl's XHTTP
transport (`type=xhttp`, alias `type=splithttp`; see [`../PLAN.md`](../PLAN.md)
§7) needs the custom `vless-xhttp` outbound registered by `internal/singboxext`
into sing-box's outbound registry. This SKU links the stock, prebuilt
`Libbox.xcframework`, which hardcodes sing-box's own registry and offers no way
to inject a custom outbound type — so `BuildConfig` fails with an explicit error
for a key with `type=xhttp`. The Developer-ID app and the CLI daemon, which build
their own `internal/core/real.go` and pull in `internal/singboxext`, are
unaffected.

## Entitlements & capabilities

- Container app: `com.apple.security.app-sandbox = true`,
  `com.apple.security.network.client = true`,
  `com.apple.developer.networking.networkextension = [packet-tunnel-provider]`,
  App Group `group.com.singctl.appstore`.
- Appex: `com.apple.developer.networking.networkextension = [packet-tunnel-provider]`,
  same App Group, sandbox on.
- The **Network Extensions** capability (packet-tunnel-provider) must be requested
  from and granted by Apple on the developer account before submission.

Scaffold lives in [`../macos/Singctl/`](../macos/Singctl/) — the App Store SKU
is a flagged target (`SingctlAppStore`, `SWIFT_ACTIVE_COMPILATION_CONDITIONS=
APPSTORE`) in the unified `project.yml`, with the `PacketTunnel` appex sources
under `macos/Singctl/PacketTunnel/` (both entitlements, a
`PacketTunnelProvider.swift` skeleton) — all marked SCAFFOLD, to be completed
on a Mac.

## Build / submit runbook (Mac)

1. `go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`.
2. Build the core library:
   `gomobile bind -target=ios,macos -o macos/Singctl/Libbox.xcframework ./mobile`
   (or vendor sing-box's prebuilt Libbox and bind only the `mobile/` shim).
3. `cd macos/Singctl && xcodegen generate`.
4. Open in Xcode (or `xcodebuild archive`), set the Team ID, ensure the Network
   Extensions capability + App Group are configured on both targets.
5. Archive → validate → upload to **App Store Connect** (`xcodebuild -exportArchive`
   with an `app-store` export options plist, or the Organizer).
6. TestFlight internal test → App Store review. In review notes, explain the VPN
   purpose and the absence of per-app routing.

## Task checklist (all Mac-only)

- [ ] `mobile/` gomobile shim reusing `internal/vless` + `internal/singbox`
      (`BuildConfig`) + a thin status/stats bridge over Libbox.
- [ ] `gomobile bind` → `Libbox.xcframework` in CI (macOS runner) or vendored.
- [ ] Complete `PacketTunnelProvider.swift` (start/stop, packetFlow → Libbox tun).
- [ ] SwiftUI container app: keys/mode/status/traffic + `NETunnelProviderManager`.
- [ ] App Group plumbing (keys + settings) shared app ↔ appex.
- [ ] Request the Network Extensions capability from Apple.
- [ ] Provisioning profiles (app + appex), App Store signing.
- [ ] App Store Connect listing, privacy nutrition labels, review notes.
- [ ] CI: a macOS `appstore` release lane (separate from the Developer-ID lane).

## Decision

Build this **after** the Developer-ID self-distribution SKU is shipping. Keep the
GUI↔daemon boundary clean in the main product so this SKU can swap the daemon for
the appex provider without touching the pure config packages.
