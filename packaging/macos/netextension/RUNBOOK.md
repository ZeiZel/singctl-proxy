# Finishing the macOS transparent-proxy extension (bead singctl-proxy-4uy)

The Go side (`internal/netext` + `internal/procproxy/router_darwin.go` + Executor
wiring + tests) and this Swift scaffold are done. The **remainder requires a Mac
with Xcode + an Apple Developer Team ID** and cannot be built/signed/tested in the
Go CI/Linux container. This runbook is the exact sequence to finish it.

Background and Apple setup (App IDs, App Group, capabilities, signing the CLI
with the App Group entitlement) live in [`../../../LICENSATION.md`](../../../LICENSATION.md);
the design rationale (why a NETransparentProxyProvider, what it captures) is in
[`README.md`](README.md) and [`../../../docs/macos.md`](../../../docs/macos.md).

## Prerequisites (Mac)
- Xcode + command-line tools (`xcode-select --install`), `brew install xcodegen`.
- An Apple Developer **Team ID** (defaults to `S3UCF4USYC`, this project's team;
  override with `DEVELOPMENT_TEAM=` for another account); App IDs
  `com.singctl.proxy` (app) and `com.singctl.proxy.netext` (extension) with the
  **Network Extensions** capability, and the App Group `group.com.singctl.proxy`
  on both.
- **Developer ID Network Extension entitlement** — distributing this system
  extension with Developer ID signing requires Apple's approval; request it now
  at https://developer.apple.com/contact/request/network-extension (choose
  Developer ID distribution) — turnaround is days to weeks. Development-signed
  local builds work without it (`systemextensionsctl developer on`).
- A `notarytool` keychain profile (see LICENSATION.md) for stapling.

```sh
make build-netext                                       # build the .app + extension (DEVELOPMENT_TEAM=S3UCF4USYC by default)
CONFIGURATION=Release NOTARY_PROFILE=<profile> \
  make build-netext                                     # build + notarize + staple
```

## Remaining work (in order)

1. **Harden the SOCKS5 TCP pump under load** — `ProxyExtension/FlowRelay.swift`.
   The handshake + bidirectional pump are written but untested. On the Mac:
   add backpressure (don't issue the next `flow.readData` until the upstream
   `send` completes — already chained; verify under bulk transfer), bound
   in-flight buffers, and confirm idempotent `teardown()` under simultaneous
   close from both ends. Test: stream a large download through a captured app.

2. **Add UDP relay (SOCKS5 UDP ASSOCIATE)** — currently `handleNewFlow` declines
   `NEAppProxyUDPFlow` so DNS/QUIC fall back to direct. Implement a UDP path:
   send `UDP ASSOCIATE` to the SOCKS server, open the returned UDP relay socket,
   and for each datagram from `NEAppProxyUDPFlow.readDatagrams` wrap it in the
   SOCKS5 UDP request header (RSV, FRAG=0, ATYP, addr, port, data) and forward;
   unwrap replies back to the flow. Decline only if the server lacks UDP.

3. **Confirm `sourceAppSigningIdentifier` covers Electron helpers live** — verify
   on a running system that capturing `com.todesktop.230313mzl4w4u92` (Cursor)
   etc. also captures its `… Helper (Renderer/GPU/Plugin)` and the Node
   `extension-host` flows (they should share the parent's signing identifier).
   If a helper has a *different* identifier, add it to the target set in
   `internal/netext` (the bundle-ID resolver) — this is the whole point of the
   extension over the env/flag path.

4. **Cisco-yield — intentionally omitted (decision 2026-07-01).** The extension's
   capture-yield (`config.json.ciscoActive` / `TransparentProxyProvider.swift`)
   is **not** being wired up: yielding would silently disable per-app proxying
   for the whole capture session while Cisco is active, which is worse than the
   coexistence behavior already owned by the Go `internal/policy` layer. The
   `ciscoActive` plumbing (`netext.Config` field, `Controller.SetCiscoActive`)
   is being removed from the code rather than wired to the Executor/monitor. See
   [`../../../docs/macos.md`](../../../docs/macos.md) §Сосуществование с Cisco
   for the actual coexistence contract.

5. **On-device verification** — after install + approval (System Settings → Login
   Items & Extensions), route an app and confirm with `lsof -i -nP` /
   `nettop -p <pid>` that its (and its helpers') connections egress via the proxy,
   and that non-targeted apps go direct. Add the check to `build.sh` output.

6. **Code-sign + notarize in the release pipeline** — sign the CLI with the App
   Group entitlement (so it can write `config.json` to the shared container),
   sign the `.app` + embedded extension (hardened runtime), notarize + staple.
   Fold into `.github/workflows/release.yml`'s macOS job behind the existing
   Apple secrets. The extension cannot ship via the Mac App Store (system
   extension) — this is the Developer-ID SKU; the App Store SKU is a separate
   track, see [`../../../docs/appstore-sku.md`](../../../docs/appstore-sku.md).

## Definition of done
- A captured app (incl. its Electron helpers) is fully proxied; non-targets are
  direct; UDP/DNS works; AnyConnect coexistence holds; the signed+notarized
  `.app` installs and the extension is approved on a clean Mac.
