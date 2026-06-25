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

This wiring is **intentionally not implemented in Go yet** — it would be dead,
untestable code until the extension is signed and installable. Add it (behind a
"is the extension present & approved?" probe) once the extension builds.

## Build / sign / notarize (on a Mac with Xcode)

1. Create an Xcode project with two targets: a macOS **App** (ContainerApp) and a
   **Network Extension** → **Transparent Proxy** (ProxyExtension). Drop these
   sources/plists/entitlements into the matching targets.
2. Set the **Team** on both targets; bundle IDs must nest, e.g.
   `com.singctl.proxy` (app) and `com.singctl.proxy.netext` (extension).
3. Add the **Network Extensions** capability (App Proxy/Transparent Proxy) and
   **System Extension** capability. Enable **App Groups** if you use the shared
   container for `config.json`.
4. Code-sign with a Developer ID, **notarize** the app (system extensions
   distributed outside the App Store must be notarized), and staple.
5. Ship the `.app`; first run prompts the user to **Approve** the system
   extension in System Settings → General → Login Items & Extensions, then to
   allow the proxy configuration.

## Remaining work (the epic)

- [ ] Finish `FlowRelay` — full SOCKS5 handshake + bidirectional pump for TCP;
      add UDP (`NEAppProxyUDPFlow`) support.
- [ ] Source-app matching: confirm `flow.metaData.sourceAppSigningIdentifier`
      (bundle ID) and `sourceAppAuditToken` mapping for helper processes; decide
      whether to capture by bundle ID (captures all Electron helpers) or by path.
- [ ] Container-app activation/approval UX + `NETransparentProxyManager` config.
- [ ] Live config reload (Darwin notification or file watch) so singctl can change
      `targets` without reinstalling.
- [ ] Cisco coexistence: the provider must **yield** when AnyConnect is active,
      mirroring the observe-only policy in `internal/policy` (do not capture while
      Cisco is connecting/up).
- [ ] Go-side wiring + an `extension present?` probe; prefer the extension over
      the env/flag path on macOS when available.
- [ ] Signing/notarization in the release pipeline.
```
