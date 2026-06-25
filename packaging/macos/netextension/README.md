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

1. singctl writes `targets` (array of bundle IDs and/or executable paths) and the
   SOCKS `host:port` into the manager's `providerConfiguration` (via the
   container app) **and/or** a JSON file the provider re-reads on demand — see
   `config.example.json`.
2. When the user routes Cursor (or any app) on macOS *and the extension is
   installed*, singctl adds that bundle ID to `targets` instead of (or in
   addition to) the env/flag launch path. The "leaky editors" set in
   `internal/ui/leaky.go` is the natural default allow-list.
3. Unroute removes the bundle ID; an empty `targets` list disables capture.

**This wiring is implemented in Go** — see `internal/netext`:
- `netext.Controller` (port) + `netext.New(socks, port)` — a real darwin
  controller that writes `config.json` into the App Group container, and a no-op
  controller off macOS. `Available()` probes `systemextensionsctl list` for the
  approved extension.
- `netext.BundleID(path)` / `netext.BundleIDForPID(pid)` resolve the bundle ID to
  capture (via `defaults read` / `ps`); `BundleInfoPlistPath` is pure & tested.
- The Executor (`internal/app/executor.go`) calls `captureExtension` /
  `releaseExtension` from RoutePID/LaunchProxied/RestartProxied/UnroutePID/
  StopProxied. All of it is a **safe no-op until the extension is installed and
  approved** (`Available()==false`), so routing still falls back to the
  procproxy env/flag path. Unit-tested with `netext.FakeController`.

What remains on the Go side is only what depends on the signed extension existing
(end-to-end verification, live config-reload signalling).

## Build / sign / notarize (on a Mac with Xcode)

The targets are described by `project.yml` (XcodeGen) so you don't assemble them
by hand. From this directory on a Mac:

```sh
brew install xcodegen
DEVELOPMENT_TEAM=<YOUR_TEAM_ID> ./build.sh   # xcodegen generate + xcodebuild
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
- [x] Go-side control layer + `extension present?` probe + config writer
      (`internal/netext`), wired into the Executor (no-op until installed).
- [x] Source-app matching by bundle ID + bundle-ID resolution from path/PID
      (`netext.BundleID` / `BundleIDForPID`).
- [x] Buildable project (`project.yml` + `build.sh`) and container-app activation
      flow (`SystemExtensionActivator` + `main.swift`).

Requires a Mac + Apple Developer signing (cannot be finished/verified here):
- [ ] Finish `FlowRelay` — harden the SOCKS5 handshake + bidirectional pump for
      TCP; add UDP (`NEAppProxyUDPFlow`) support.
- [ ] Confirm `sourceAppSigningIdentifier` covers Electron helper processes on a
      live system (vs `sourceAppAuditToken`).
- [ ] Live config reload (Darwin notification / file watch) so singctl can change
      `targets` without reinstalling.
- [ ] Cisco coexistence: the provider must **yield** when AnyConnect is active,
      mirroring the observe-only policy in `internal/policy`.
- [ ] On-device verification (the `lsof` check in `build.sh`) + signing/
      notarization in the release pipeline.
```
