# singctl — macOS Transparent Proxy System Extension (scaffold)

> **Status: scaffold, not shippable as-is.** This directory is the starting
> point for Variant C of the macOS Cursor-leak plan (`singctl-proxy-dzw`):
> a `NETransparentProxyProvider` System Extension that intercepts the traffic of
> **selected apps at the network layer** and relays it to singctl's local SOCKS
> proxy — catching *every* stack (Chromium, Node/undici, raw sockets), unlike the
> Go-side env/flag levers which only cover part of an app (see `docs/macos.md`).
>
> It **cannot be built, signed, or tested from the Go repo / CI**. Completing it
> requires Xcode, an **Apple Developer Program** membership (Team ID), the
> **Network Extensions** capability, code-signing and **notarization**, plus
> on-device testing. The Swift here is a faithful skeleton of Apple's transparent
> proxy pattern, but the flow-relay and packaging must be finished and verified on
> a Mac. Treat every `// TODO` as a required step, not a nicety.

## Why this exists

On macOS there is no kernel per-PID interception (the Linux cgroup+nftables path
is Linux-only). singctl's per-app proxying therefore relies on two cooperative
levers — `--proxy-server` (Chromium only) and `HTTP_PROXY` env (only processes
that read it). Cursor/VS Code agent traffic is born in Node extension-host
workers whose native `fetch`/undici ignore both, so it leaks. The only
*guaranteed* mechanism today is full VPN/TUN mode (system-wide).

A `NETransparentProxyProvider` is the supported way to get **per-app** capture
without taking over the whole default route: the system hands the provider every
new network flow, the provider keeps the flows whose source app matches a
configured allow-list and relays them to the SOCKS proxy, and lets everything
else go direct.

## Architecture

```
┌─────────────────────┐     OSSystemExtensionRequest      ┌──────────────────────┐
│  Container app       │ ───  activate / deactivate  ───▶  │  System Extension     │
│  (singctl helper)    │                                   │  ProxyExtension       │
│                      │ ──  NETransparentProxyManager ──▶ │  (NETransparentProxy  │
│  writes targets +    │     providerConfiguration         │   Provider subclass)  │
│  socks addr          │                                   │                       │
└─────────────────────┘                                   │  handleNewFlow:       │
          ▲                                                 │   match source app ──▶│ relay → 127.0.0.1:1080
          │ control socket / config file                   │   else return false   │ (singctl SOCKS)
   ┌──────┴───────┐                                         └──────────────────────┘
   │ singctl (Go) │  decides which bundle IDs to capture (the "leaky editors"
   └──────────────┘  set, or any per-app pick) and the SOCKS host:port.
```

- **ProxyExtension/** — the System Extension target (the provider that runs
  privileged, out of process, managed by `nesessionmanager`).
- **ContainerApp/** — the app that *activates* the extension and *configures* the
  `NETransparentProxyManager` (the extension cannot configure itself).

## Integration with singctl (Go side)

The Go binary owns the policy; the extension is a dumb relay. Contract:

1. singctl writes `targets` (bundle IDs) + SOCKS `host:port` into the App Group
   `config.json` (`internal/netext`). The container app reads it and pushes it to
   the manager's `providerConfiguration`; it also **watches the file** and
   re-applies on every change (`ContainerApp/main.swift`), so the CLI can update
   the set live without reinstalling.
2. On macOS, per-app proxying IS the extension. `internal/procproxy`'s
   `darwinRouter` (`router_darwin.go`) implements the platform `Router` over
   `internal/netext`: RoutePID/Launch resolve the app's bundle ID and `AddTarget`;
   Unroute/Kill `RemoveTarget` (refcounted across a bundle's PIDs, so Electron
   helpers are handled). Requires `netext.Available()`; otherwise it returns a
   clear "install/approve the extension" error surfaced in the TUI.
3. The Linux backend (cgroup/nftables) and Windows env fallback are unchanged —
   `procproxy.NewRouter` picks the backend by build tag.

`internal/netext` is the bridge:
- `netext.Controller` + `netext.New(host, port)` — real darwin controller writes
  `config.json` into the App Group container (errors wrapped for the TUI); no-op
  off macOS. `Available()` probes `systemextensionsctl list`.
- `netext.BundleID(path)` / `netext.BundleIDForPID(pid)` (via `defaults`/`ps`);
  pure `BundleInfoPlistPath` is unit-tested; `darwinRouter` is tested via
  `netext.FakeController`.

What remains depends on the signed extension existing (on-device verification,
relay hardening) — see the checklist below and LICENSATION.md.

## Build / sign / notarize (on a Mac with Xcode)

The targets are described by `project.yml` (XcodeGen) so you don't assemble them
by hand. From this directory on a Mac:

```sh
brew install xcodegen
DEVELOPMENT_TEAM=S3UCF4USYC ./build.sh   # xcodegen generate + xcodebuild (team now defaults in build.sh)
```

`build.sh` generates `SingctlProxy.xcodeproj` (container app `com.singctl.proxy`
+ nested extension `com.singctl.proxy.netext`) and builds Release. You still must
have the **Network Extensions** capability and **App Groups** provisioned on your
Apple Developer account. Then:

1. Run the `.app` once; **approve** the system extension in System Settings →
   General → Login Items & Extensions, then allow the proxy configuration.
2. **Notarize**: `xcrun notarytool submit … --wait` then `xcrun stapler staple`
   (system extensions distributed outside the App Store must be notarized).
3. Ship the `.app` alongside singctl.

## Remaining work (the epic)

Done in this repo (Go + scaffold):
- [x] Go-side control layer + `extension present?` probe + config writer with
      wrapped errors (`internal/netext`).
- [x] macOS per-app `Router` backed by the extension (`procproxy/router_darwin.go`),
      bundle-ID resolution + refcount; clear error when the extension is absent.
- [x] Source-app matching by bundle ID (`netext.BundleID` / `BundleIDForPID`).
- [x] Buildable project (`project.yml` + `build.sh`, with optional notarize) and
      container-app activation + **live config.json watch** (`main.swift`,
      `SystemExtensionActivator` with activation-result completion).
- [x] FlowRelay idempotent teardown (no double-close races).

Requires a Mac + Apple Developer signing (cannot be finished/verified here):
- [ ] Harden the SOCKS5 pump under load + add UDP (`NEAppProxyUDPFlow`) support.
- [ ] Confirm `sourceAppSigningIdentifier` covers Electron helper processes on a
      live system (vs `sourceAppAuditToken`).
- [ ] Cisco coexistence: the provider must **yield** when AnyConnect is active,
      mirroring the observe-only policy in `internal/policy`.
- [ ] On-device verification (the `lsof` check in `build.sh`) + signing/
      notarization + signing the CLI with the App Group entitlement
      (see LICENSATION.md).
