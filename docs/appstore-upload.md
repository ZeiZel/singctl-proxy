# App Store upload — sandboxed VPN SKU

How to publish the reduced App Store SKU of singctl (bead `singctl-proxy-8zz`,
architecture in [`appstore-sku.md`](appstore-sku.md)). This is a **different
product** from the Developer-ID build described in
[`apple-distribution.md`](apple-distribution.md): sandboxed, whole-system VPN
only, sold once for $9.99 instead of licensed per-token.

---

## 0. Blockers — resolve before touching Xcode

Do these in order; each gates the next. None of them can be done from this
repo/container — they're Apple-portal and Mac-only.

1. **Convert the Apple Developer account to Organization.** Team `S3UCF4USYC`
   is currently an **Individual** account. App Review Guideline 5.4 requires
   VPN apps (any app declaring `com.apple.developer.networking.networkextension`)
   to come from an **Organization** account with a verified legal entity. This
   needs a **D-U-N-S number** (free, but the lookup/registration can take days
   to weeks — start it first) and Apple's enrollment/verification step
   (developer.apple.com/support/App-Store-Connect-organization). An Individual
   account cannot submit this app regardless of how complete the code is.
2. **Request the Network Extensions capability (`packet-tunnel-provider`) for
   App Store distribution**, granted separately from the Developer-ID Network
   Extension entitlement in `apple-distribution.md` §1.5 — that one is
   Developer-ID-distribution only and does not cover this SKU:
   https://developer.apple.com/contact/request/network-extension → select
   **App Store distribution**. Do this as soon as the org conversion above is
   underway; approval can take days to weeks.
3. **Finish the real VPN datapath first.** As of this doc, `mobile/` (gomobile
   shim over `internal/vless` + `internal/singbox`), the `Libbox.xcframework`
   build, and `PacketTunnelProvider.startTunnel` in
   `macos/Singctl/PacketTunnel/PacketTunnelProvider.swift`
   are **not implemented** — see the checklist in `appstore-sku.md`. The
   target currently compiles and archives (`make appstore`), but the extension
   does not actually run sing-box over the tunnel fd yet. **Do not submit
   until this works end-to-end** (verified: VPN connects, traffic routes
   through a real VLESS key) — an app that installs a VPN profile and passes
   no traffic is both a guaranteed rejection and a bad first impression if it
   somehow slips through.

Only once all three are done does the rest of this doc apply.

---

## 1. What ships vs. what doesn't

| | Developer-ID (`macos/Singctl/`) | App Store SKU (`macos/Singctl` (SingctlAppStore scheme)) |
|---|---|---|
| Whole-system VPN (VLESS via sing-box) | root daemon + TUN | ✅ `NEPacketTunnelProvider` (sandboxed) |
| Dashboard / Proxies / Keys / Settings | ✅ | ✅ |
| Per-app routing | ✅ (system extension, per-app picker) | ❌ not possible in the App Sandbox |
| Connections / Console (live traffic, Clash API) | ✅ | ❌ |
| CLI (`singctl`, control socket, `--attach`) | ✅ | ❌ |
| License server / token activation | ✅ (own or free internal tokens) | ❌ — unlock is the one-time $9.99 App Store purchase |

The App Store build is deliberately a smaller product: a personal VPN client
with your own VLESS server(s), nothing that looks like a corporate MDM or
proxy-management tool. Don't let per-app, Console, or CLI scope creep back in
— that's exactly what makes this SKU reviewable under 5.4 in the first place.

---

## 2. App Store Connect setup

1. **App IDs** (developer.apple.com/account → Identifiers), Organization
   account, both with **App Groups** + **Network Extensions** capabilities:

   | Role | Bundle ID |
   |---|---|
   | Container app | `com.singctl.appstore` |
   | PacketTunnelProvider appex | `com.singctl.appstore.tunnel` |

2. **App Group**: `group.com.singctl.appstore`, enabled on both App IDs above
   (shares keys/settings between the container app and the appex — same
   pattern as `internal/netext` on the Developer-ID side, different group ID).
3. **Certificates**: **Apple Distribution** (App Store, not Developer ID) —
   one cert covers both app and appex.
4. **Provisioning profiles**: two **App Store** profiles (container app,
   appex), each with the Network Extensions + App Group capabilities enabled,
   matching the App IDs above.
5. **Create the app record** in App Store Connect → My Apps → +: bundle ID
   `com.singctl.appstore`, SKU/name of your choosing.
6. **Pricing**: set the price tier to **$9.99** (Pricing and Availability).
   This is a paid-up-front app, not a subscription — no in-app purchase
   plumbing needed.
7. **Privacy nutrition labels** (App Privacy section): declare what the app
   actually does. It only carries the traffic the user routes through their
   own VLESS server — the app itself doesn't collect analytics, doesn't phone
   a license server, doesn't have accounts. Aim to declare **"Data Not
   Collected"** if that's still true once the datapath ships; if any
   telemetry/crash reporting is added, declare it honestly instead.
8. **Privacy policy URL** — required for any app requesting VPN entitlements,
   even a "no data collected" one. Host one page stating there's no
   collection/no accounts and no per-app routing.
9. **Review notes** (App Review Information → Notes): explain plainly —
   *"This is a personal VPN client. Users supply their own VLESS server
   connection keys; the app does not operate or provide server infrastructure.
   There is no per-app routing in this build — it establishes a single
   system-wide VPN tunnel via NEPacketTunnelProvider."* Reviewers testing VPN
   apps usually need a working server to test against — offer a demo VLESS key
   in the notes if you can, or the review will stall on "unable to test."

---

## 3. Build & upload

The App Store SKU is now a flagged target in the **same unified XcodeGen
project** as the Developer-ID app: `macos/Singctl/project.yml` defines the
`SingctlAppStore` target (bundle `com.singctl.appstore`, built from the same
`App/` sources with `SWIFT_ACTIVE_COMPILATION_CONDITIONS=APPSTORE`) and the
`PacketTunnel` app-extension target (bundle `com.singctl.appstore.tunnel`,
sources under `macos/Singctl/PacketTunnel/`). There is no separate
`packaging/macos/appstore/` scaffold anymore — that directory has been
retired in favor of the unified project.

The primary path is the Makefile target:

```sh
make appstore                                 # uses DEVELOPMENT_TEAM=S3UCF4USYC
make appstore DEVELOPMENT_TEAM=<your-team-id>  # override for another account
```

This runs `xcodegen generate`, archives the `SingctlAppStore` scheme, and
exports with `macos/Singctl/ExportOptions-appstore.plist` (method
`app-store`) into `dist/`. See the `appstore` target in the root `Makefile`
for the exact invocation.

Equivalent manual steps, if you want to run them by hand instead:

```sh
cd macos/Singctl
brew install xcodegen   # if not already installed
xcodegen generate       # → Singctl.xcodeproj (includes SingctlAppStore + PacketTunnel)

xcodebuild \
  -scheme SingctlAppStore \
  -configuration Release \
  -derivedDataPath ./build \
  -archivePath build/SingctlAppStore.xcarchive \
  -allowProvisioningUpdates \
  DEVELOPMENT_TEAM=S3UCF4USYC \
  archive

xcodebuild -exportArchive \
  -archivePath build/SingctlAppStore.xcarchive \
  -exportPath ../../dist \
  -exportOptionsPlist ExportOptions-appstore.plist \
  -allowProvisioningUpdates
```

`ExportOptions-appstore.plist` already exists at
`macos/Singctl/ExportOptions-appstore.plist` (method `app-store`, team
`S3UCF4USYC`, automatic signing) — no need to create it. The export produces
a signed App Store Connect upload package under `dist/`.

Equivalent GUI path, if you'd rather not touch the command line: open
`macos/Singctl/Singctl.xcodeproj` in Xcode, select the **SingctlAppStore**
scheme → **Product ▸ Archive** → Organizer opens automatically on the new
archive → **Distribute App ▸ App Store Connect ▸ Upload**.

**Uploading the built package** (if you archived via the command line instead
of the Organizer):

- Xcode Organizer: Window ▸ Organizer ▸ Archives → select the archive →
  **Distribute App** → follow the App Store Connect flow (this both exports
  and uploads in one step, so if you use Organizer you can skip the manual
  `-exportArchive` above entirely).
- **Transporter** (Apple's standalone uploader app, Mac App Store) — drag the
  exported package in, sign in, upload. This is Apple's currently recommended
  path for uploading pre-built packages.
- `xcrun altool --upload-app -f dist/SingctlAppStore.pkg -t macos --apiKey <KEY_ID> --apiIssuer <ISSUER_ID>`
  — still works but is deprecated by Apple in favor of Transporter/Organizer;
  `<KEY_ID>`/`<ISSUER_ID>` come from an App Store Connect API key (Users and
  Access → Keys).

No separate `notarytool` step is needed for App Store builds — App Store
Connect notarizes/validates the binary itself on ingestion (unlike the
Developer-ID `.pkg`/`.dmg` path in `apple-distribution.md`, which notarizes
explicitly with `notarytool`).

---

## 4. Submit for review

1. In App Store Connect, open the app version, attach the uploaded build
   (it appears under "Build" a few minutes to ~1 hour after upload, once
   Apple's ingestion/processing finishes).
2. Answer the **export compliance** question: this app only uses standard
   TLS/HTTPS and the OS's built-in VPN/crypto frameworks (no custom
   cryptography) — answer accordingly (typically qualifies for the standard
   encryption exemption; the exact wording is asked at submission time).
3. Submit for review.
4. **Typical timing**: Apple's stated median is ~24–48 hours, but VPN apps
   often take longer because they get routed to a specialized review queue —
   budget several days, and don't schedule a launch date tightly against it.
5. **Common VPN-app rejection reasons to preempt**:
   - **Guideline 5.4 (org account)** — the #1 reason VPN submissions bounce;
     confirm the account is Organization *before* submitting, not after a
     rejection.
   - **Missing/broken privacy policy URL**, or a privacy policy that doesn't
     match the declared nutrition labels.
   - **Reviewer can't test the VPN** — no working server/key in the review
     notes leads to "unable to complete review" holds; provide one.
   - **Any appearance of proxying/inspecting user traffic beyond what's
     disclosed** — since this SKU has no per-app routing and no telemetry,
     say so explicitly and make sure the actual behavior matches (reviewers
     do test network behavior for VPN apps).
   - **Entitlement mismatch** — Network Extensions entitlement present in the
     build but not yet granted for App Store distribution on the account (see
     §0.2) is an automatic rejection, distinct from the Developer-ID grant.

---

## 5. What this does *not* affect

Publishing this SKU on the App Store is **independent of the Developer-ID
Team ID (`S3UCF4USYC`) being trusted by any corporate MDM allowlist**. The two
builds have different bundle IDs, different code signatures (Apple
Distribution vs. Developer ID Application), and are reviewed/approved through
completely separate Apple processes. Getting the App Store SKU approved does
nothing to get `macos/Singctl/`'s Developer-ID `.app`/`.pkg` allowlisted by an
MDM — that remains a separate conversation with whoever manages the MDM
policy.
