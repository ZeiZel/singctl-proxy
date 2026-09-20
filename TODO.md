# TODO / findings

Working notes from the macOS distribution + corporate-Mac testing effort. What
shipped, what's blocked (with evidence), and what's still worth trying. Newest
context at the top of each section.

---

## Done

- **App Store SKU has a real VPN datapath** (was a stub). `internal/singbox.GenerateTunnelConfigSet`
  + `mobile.BuildConfig` (gomobile) + `PacketTunnel` appex via `LibboxNewCommandServer`
  + `Libbox.xcframework` (sing-box 1.13.12, built by `make libbox`). Commit `96f70d6`.
- **App Store build uploaded.** App record "Singctl VPN" (`com.singctl.appstore`,
  Apple ID 6787699422) created in App Store Connect; package validated + uploaded
  via `xcrun altool` (app-specific password). App icon + Info.plist metadata added
  to pass validation (commit `9061bf5`).
- **System-extension activation UI** restored in the app (Apps → "System extension"
  card, plus `--activate-netext` launch flag) so activation and its exact error are
  visible. App icon now ships in the Developer-ID build too. Commit `782b7d7`.
- **Full Developer-ID `Singctl.app` built, notarized, stapled, installed** to
  `/Applications` (Gatekeeper: accepted, Notarized Developer ID). The app itself
  works — VPN mode, SOCKS proxy, UI, CLI. Only per-app Proxy is blocked (see below).

---

## Blocked — with evidence (not code bugs)

### Per-app Proxy (system extension) on the work Mac — MDM allowlist
- Activating `com.singctl.proxy.netext` fails with `OSSystemExtensionErrorDomain
  code 4` ("Extension not found in App bundle"). Misleading name; the real cause
  is the MDM.
- **This Mac is enrolled in ExampleOrganization MDM**: `MDM server: https://awds.example.invalid/DeviceServices/AppleMDM/Processor.aspx`.
  Its SystemExtensions payload has an `AllowedSystemExtensions` allowlist keyed by
  TeamID. Allowed teams: `DE8Y96K9QP` (Cisco), `TZ3UEPFYKD` (Check Point),
  `S2ZMFGQM93` (VMware Workspace ONE). **Our team `S3UCF4USYC` is NOT in it** →
  macOS filters the extension out before it can load.
- Ruled out as causes: duplicate LaunchServices registrations (purged), notarization
  (done), translocation (no), entitlements (`system-extension.install` present),
  bundle integrity (`codesign --verify --deep --strict` passes). The bundle is
  provably correct.
- **The only durable fix is server-side:** ExampleOrganization IT adds our team to the awds allowlist —
  `AllowedSystemExtensions = { "S3UCF4USYC" = ( "com.singctl.proxy.netext" ); }`
  (type `com.apple.networkextension.app-proxy` / `NETransparentProxyProvider`).
  Local circumvention is out of scope: the MDM re-pushes policy on check-in, the
  Cisco/Check Point EDR flags tampering, and profiles are signed (can't be forged
  via interception).

### App Store submission — Individual account
- Build uploads fine, but **submitting for review is blocked by Guideline 5.4**:
  VPN apps require an **Organization** account. Team `S3UCF4USYC` is Individual.
  Needs a D-U-N-S number + Apple's org verification (days–weeks) before submission.
- App Store Connect API access for automation is "reviewed" (not instant) for
  Individual accounts — used an app-specific password for the upload instead.

---

## To try — might work

### 1. VM-for-proxying (host stays on the corporate VPN) — PRIMARY next experiment
Idea: run a VM you fully control, run the apps you want to proxy **inside** the VM,
and route the VM's traffic through your own singctl VPN. The host keeps its normal
Cisco AnyConnect corporate-VPN connection; you don't touch/bypass the MDM at all —
the corporate policy simply doesn't apply inside the guest OS.

- **Key simplification:** inside the VM you do **not** need the blocked system
  extension. The extension only mattered for selectively routing *specific host*
  apps. If everything in the VM should go through the VPN, that's plain
  **whole-system VPN/TUN mode** (or just point apps at the SOCKS proxy) — no
  per-app system extension, so the MDM allowlist is irrelevant.
- **Recommended setup:** a **Linux VM** (lightest — no SIP/sysext concerns), run
  `singctl` in VPN/TUN mode inside it (build `make build-macos` → actually build
  the Linux binary), launch target apps in the guest.
- **Main risk = VM network egress through the corporate host.** Prior testing on
  this Mac showed **bridged networking wedges** (VM boots, L3 dies after first
  contact) because of the Check Point/Cisco packet/socket filters.
  - **Try NAT networking first** — NAT'd guest traffic looks like ordinary host
    egress and is more likely to pass the corporate filters than bridged.
  - No guarantee: if the EDR inspects NAT egress too, the tunnel may not come up.
    This needs an **empirical test** — the first concrete step is just "does the
    guest get working internet at all through the host."
- **Steps:**
  1. Pick/confirm hypervisor on the Mac (UTM / VMware Fusion / Parallels / QEMU?).
  2. Create a Linux guest, NAT networking, verify basic egress.
  3. Build a Linux `singctl`, run it in VPN/TUN mode in the guest with a real VLESS key.
  4. Verify a guest app's external IP is the VLESS exit, not the corporate egress.
- **Policy caveat (non-technical):** the VM is separate, but its traffic still
  physically leaves via the corporate network inside your tunnel, i.e. off the
  corporate inspection path. Fine for testing your app; routing real work traffic
  around corporate controls is a question for ExampleOrganization's acceptable-use/IB policy, not a
  technical one.

### 2. Get the TeamID allowlisted in awds (the sanctioned fix for the work Mac)
- If you have MDM console access: add `S3UCF4USYC` to the SystemExtensions payload
  server-side (see the evidence block above). Then per-app Proxy activates natively.
- If not: file an ExampleOrganization IT/IB request. Have ready: TeamID `S3UCF4USYC`, extension id
  `com.singctl.proxy.netext`, type `NETransparentProxyProvider`
  (`com.apple.networkextension.app-proxy`), and the justification.

### 3. Personal / unmanaged Mac
- No MDM policy there → the per-app system extension activates normally. Fastest way
  to verify per-app Proxy end-to-end with the notarized build we already have.

### 4. App Store: finish the path
- Convert the Apple account to **Organization** (D-U-N-S + verification) to unblock
  submission under Guideline 5.4.
- **Runtime-verify the App Store VPN datapath** before submitting: confirm the appex
  reads `config.json` from the App Group container and routes real traffic through
  sing-box over the tun fd (add a key via the app UI, switch Mode → VPN, check
  external IP). `startTunnel` is implemented but full end-to-end routing wasn't
  runtime-confirmed yet.

---

## Housekeeping
- Stray root-owned `/Applications/Singctl.old.disabled` left over from the notarized
  reinstall — remove with `sudo rm -rf /Applications/Singctl.old.disabled` (cosmetic).
