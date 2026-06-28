# App Store SKU scaffold (SCAFFOLD — Mac/Xcode only)

Sandboxed container app + `NEPacketTunnelProvider` appex for the **App Store**
SKU (bead `singctl-proxy-8zz`). This is a starting skeleton only: it is **not
built, signed, or tested** — it cannot be from the Go/Linux container. The full
architecture, build/submit runbook, and task checklist are in
[`../../../docs/appstore-sku.md`](../../../docs/appstore-sku.md).

Files:
- `project.yml` — XcodeGen spec (container app + packet-tunnel appex).
- `ContainerApp/` — sandboxed SwiftUI app skeleton + entitlements + Info.plist.
- `PacketTunnelProvider/` — appex skeleton (`NEPacketTunnelProvider` + Libbox) +
  entitlements + Info.plist.
- `Libbox.xcframework` — produced by `gomobile bind` of the `mobile/` shim
  (not committed; see the runbook).

Generate the project on a Mac: `brew install xcodegen && xcodegen generate`.
Requires the Apple **Network Extensions** capability (packet-tunnel-provider) and
the App Group `group.com.singctl.appstore` on both targets.
