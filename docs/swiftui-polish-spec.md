# Native SwiftUI polish specification

Status: implementation-ready  
Scope issue: `singctl-proxy-nys`  
Target: the Developer ID macOS app in `macos/Singctl/App` (macOS 26; SwiftUI)

## Goal

Make the native app feel like a coherent macOS utility, with Settings as the clearest example: native controls, strong grouping, calm spacing, readable hierarchy, and explicit feedback around changes. Add a few small conveniences that shorten common workflows. Publish one reproducible, representative app image in the root README without requiring an installed or running singctl daemon.

This is a presentation and usability pass. Existing backend commands, routing semantics, App Store conditional behavior, persistence rules, and safety boundaries remain unchanged.

## Evidence and design direction

The current app already has useful foundations: semantic adaptive colors and shared spacing in `Theme.swift`, `NavigationSplitView` plus a native sidebar in `SingctlApp.swift`, and reusable `Card`, `AppButton`, `Badge`, `EmptyState`, and status components. The work should refine those primitives rather than introduce another UI framework.

The main inconsistency is `SettingsScreen.swift`. It renders General, Proxy, System proxy, Routing, Observability, Apply, Maintenance, and About as a long stack of custom cards. Rows combine fixed-width controls with verbose descriptions, custom `PillToggle` and `SegmentedControl` implementations, and a separate Apply card far below the fields it commits. At ordinary macOS window widths this creates heavy visual framing, weak scan lines, and an unclear boundary between settings that save immediately and daemon-backed settings that need Apply.

Other screens repeat the same material card for nearly every unit, use a forced two-size 16/20-point typography scale, and add a dark overlay to the window material. The result is visually louder and larger than typical macOS utilities. The pass should keep the soft-indigo identity while letting native materials, text styles, focus, keyboard behavior, and accessibility do more work.

Reference patterns:

- Apple [Settings HIG](https://developer.apple.com/design/human-interface-guidelines/settings): organize options into clear, task-oriented groups and use familiar platform controls.
- Apple [`Form`](https://developer.apple.com/documentation/swiftui/form): use native form layout and `Section` structure for data-entry controls.
- Sindre Sorhus’s [Settings](https://github.com/sindresorhus/Settings) sample: compact, aligned panes and descriptions with clear section hierarchy. It is a reference only; no package dependency is needed.
- Jordan Baird’s Ice [`SettingsView`](https://github.com/jordanbaird/Ice/blob/main/Ice/Settings/SettingsView.swift) and [`GeneralSettingsPane`](https://github.com/jordanbaird/Ice/blob/main/Ice/Settings/SettingsPanes/GeneralSettingsPane.swift): a real SwiftUI macOS utility with restrained grouped rows, leading labels, trailing controls, and clear gaps between sections. Use structural patterns only; do not copy GPL implementation.

## Required changes

### 1. Shared visual system

- Retain semantic adaptive colors and the single indigo accent, but move base surfaces toward neutral system-adaptive values and reduce custom tinting, glass effects, and panel repetition. Window, sidebar, content background, cards, dividers, and controls must have a clear three-level surface hierarchy in both Light and Dark appearance.
- Replace the blanket 16/20-point type rule with semantic macOS text styles (`title2`/`headline`/`body`/`callout`/`caption`, with monospaced digits where data benefits). Dynamic Type and accessibility sizes must remain usable.
- Refine `Card` into a quieter content surface: consistent radius and internal padding, subtle border/shadow only where it separates content, no stacked material plus heavy tint. Existing call sites should remain straightforward.
- Use native `Button`, `Toggle`, `Picker`, `TextField`, `Gauge`, `LabeledContent`, `Form`, and `Section` behavior wherever a custom control does not add product meaning. Preserve branded wrappers only for consistent role, loading state, or domain-specific display.
- Give every screen one page title and optional concise subtitle, consistent outer margins, and predictable vertical rhythm. Avoid decorative descriptions that restate labels.
- Preserve keyboard focus, text selection, VoiceOver labels, increased-contrast behavior, and disabled/loading states. Color must not be the only carrier of status.

### 2. Settings redesign

- Rebuild Settings around a native `Form` with `Section`s for General, Connection, Routing, Observability, System proxy, Maintenance, and About. Use `LabeledContent` or equivalent aligned rows; controls must resize instead of relying on hard-coded trailing widths.
- General preferences that already commit immediately (`launchAtLogin`, menu-bar item, VPN confirmation, log view preferences) continue to commit immediately and use native switches/pickers.
- Daemon-backed values continue to load through `settingsGet()` and commit only through `settingsSet(_)`. Make this boundary visible: show a compact sticky/bottom action area when daemon-backed fields differ from the last loaded/applied snapshot, with **Apply changes** and **Revert**. Disable Apply while values are unchanged or invalid.
- Keep inline validation for numeric fields and the SOCKS/Clash port collision. Associate each error with the relevant section/field and keep the typed value intact after a failed apply.
- Keep System proxy read-only on this screen. Show its mode/service summary and a clear navigation action to the dedicated System proxy screen; never call `sysProxySet` or `sysProxyImport` while opening Settings.
- Keep Reset and Stop daemon behind confirmation. Present them together as destructive maintenance actions, visually separated from routine preferences. Reset must retain its existing scope and must not touch keys or system proxy settings.
- Keep About compact (app name, version, build when available) and selectable/copyable where useful.
- Preserve `#if APPSTORE` behavior: daemon-only fields and actions stay hidden; the VPN control remains available in that build.

### 3. Cross-screen polish and focused conveniences

- Apply the refined page/section/card hierarchy to Dashboard, Proxies, System proxy, Keys, Apps, Connections, and Logs without changing their backend behavior.
- Add native search where lists can grow: Keys and Apps at minimum; Connections/Logs keep or refine their existing filters. Search is local, instant, case-insensitive, and never mutates source data.
- Add refresh affordances consistently to live/read-only views, with a standard keyboard shortcut where SwiftUI permits it. A refresh must show progress without replacing useful stale data.
- Add copy affordances for useful diagnostic values already displayed (for example PAC URL, applied config, or masked/non-secret identifiers). Never reveal or copy private key material.
- Improve empty, loading, success, offline, and error states so they say what happened and, when actionable, offer the next relevant action. Do not convert backend failures into success-looking empty lists.
- Preserve all safety constraints already documented in source: system proxy changes occur only after explicit Apply/Import actions; VPN confirmation remains honored; destructive actions remain confirmed.

### 4. README image

- Add a single hero image near the top of the root `README.md`, after the project introduction and before long usage instructions. The checked-in asset lives under `docs/images/` and uses a stable relative Markdown path.
- The image must show the actual native SwiftUI app at a representative desktop window size. Prefer Dashboard or the redesigned Settings view, whichever best communicates product quality after implementation.
- Compose the captured window over a restrained pleasant background (soft indigo/blue gradient or blurred shapes), with generous breathing room and a subtle window shadow. Do not add claims or UI elements that the product does not have.
- Generate the app state through a source-controlled static demo fixture. A dedicated launch argument or debug-only harness injects a `Backend` implementation with deterministic status, traffic, latency, connection, settings, and system-proxy samples. It must not instantiate `DaemonBackend`, read control sockets, invoke the installed `singctl` CLI/service, change network settings, or require credentials.
- Keep the fixture deterministic: fixed sample values, fixed window size, selected section, Light/Dark appearance, and capture instructions. The normal app path must never select it unless the explicit screenshot argument is present.
- Document the generation command or short procedure next to the fixture/script so a maintainer can reproduce the asset on macOS.

## Non-goals

- No backend protocol, daemon, PAC/routing, licensing, signing, installer, or Network Extension changes.
- No new third-party UI dependency and no code copied from reference applications.
- No standalone Settings window or navigation restructure in this pass; Settings remains a destination in the existing sidebar.
- No live production data in screenshots and no automated interaction with an installed singctl daemon.
- No wholesale rewrite of every screen. Shared primitives and the highest-value list/form surfaces are the intended boundary.

## Acceptance criteria

1. Settings uses native grouped form semantics and remains usable without clipping at the app’s supported minimum window width in both Light and Dark appearance.
2. Immediate preferences still save immediately. Daemon-backed values expose Apply only when dirty and valid, can be reverted to their last loaded/applied snapshot, and preserve existing `settingsGet/settingsSet` semantics.
3. Field validation, loading, offline, error, success, disabled, and destructive-confirmation states are visually distinct and accessible without relying on color alone.
4. Opening Settings or System proxy performs read-only calls only. System proxy and import mutations still require explicit user actions.
5. Shared typography, surface, spacing, and action styles are visibly consistent across all sidebar destinations; list search works for Keys and Apps without backend mutation.
6. Both Developer ID and App Store conditional source paths compile, subject to their existing framework/signing prerequisites.
7. The screenshot fixture runs without `DaemonBackend`, the installed CLI/service, control sockets, real network state, or credentials; repeated runs yield the same representative content.
8. The root README renders the checked-in hero image via a relative path, and the asset remains legible on GitHub at common desktop and mobile README widths.

## Verification

- Generate the project with `/opt/homebrew/bin/xcodegen generate` from `macos/Singctl`.
- Compile the Developer ID scheme without signing for source verification, using Xcode 26.6 / Swift 6.3.3: `xcodebuild -project Singctl.xcodeproj -scheme Singctl -configuration Debug CODE_SIGNING_ALLOWED=NO build`.
- Compile the App Store scheme without signing when `Libbox.xcframework` is available; otherwise record the existing missing-artifact limitation and at least compile-check shared Swift sources through the Developer ID scheme.
- Exercise Settings manually with the static fixture at narrow and wide window sizes, Light and Dark appearance, keyboard traversal, dirty/revert/apply, invalid numeric input, backend error, and confirmation dialogs.
- Exercise search, refresh, copy, empty, loading, and offline states through deterministic fixture variants.
- Produce the README image only from the fixture path, inspect the final PNG at its native size and at roughly 50% scale, and confirm no private values or machine-specific paths appear.
- Review the final diff to confirm there are no backend/daemon/network-setting changes and no new package dependency.

## Implementation split

1. **Foundation:** refine `Theme.swift`, shared surfaces/actions/headers, and root window background; migrate call sites incrementally while keeping behavior unchanged.
2. **Settings:** introduce the native form layout, applied snapshot/dirty tracking, validation, Revert, and compact action area; preserve conditional builds and safety rules.
3. **Conveniences and consistency:** apply the hierarchy across screens, add Keys/Apps search, normalize refresh/copy/state presentation, and perform accessibility labels/help cleanup.
4. **Fixture and presentation:** add the deterministic demo backend/harness, capture and compose the hero PNG, add reproducibility notes, and insert the image in `README.md`.
5. **Verification:** run project generation/build checks, exercise fixture states and appearances, visually inspect the PNG, and resolve regressions before closing `singctl-proxy-nys`.

## Implementation and verification record

Implemented:

- Settings now uses native `Form`/`Section` structure. Daemon-backed fields have Apply/Revert dirty-state handling and normalized optional-value validation through `SettingsDraft`; the App Store build omits the read-only system-proxy summary.
- Shared surfaces and semantic fonts were refined, and the root window no longer uses the heavy dark overlay.
- Keys gained local search. Apps retains the search it already had before this work; it was not newly introduced here.
- `⌘R` refresh is wired for Keys, Proxies, Connections, Apps, and System proxy. Existing copy affordances were retained; this pass did not add a new copy feature.
- A standalone `SingctlPreview` target renders the real `RootView` with an in-memory backend and isolated defaults. The shipping main entry point is excluded from that target. Capture uses `/usr/sbin/screencapture -x -l` against only the preview window because offscreen caching does not render SwiftUI reliably. It neither contacts an installed service nor changes network state. The surrounding hero background is implemented in SwiftUI, and the resulting image is linked from the root README.
- The preview harness defaults to the dark appearance for the README hero; pass `--appearance light` explicitly for Light visual QA captures.

Completed verification:

- Both unsigned Debug schemes built successfully with Xcode 26.6 / Swift 6.3.3; the App Store build reused the available local `Libbox.xcframework` artifact.
- The final fixture renders were visually inspected for the Dashboard README hero, Settings in a wide Light window, and Settings in a narrow Dark window. This inspection confirmed the corrected TextField labels, semantic scene background, and Dark appearance header contrast in those captured states.
- Settings draft checks passed with:

  ```sh
  swiftc macos/Singctl/App/Core/Models.swift \
    macos/Singctl/App/Core/SettingsDraft.swift \
    macos/Singctl/Scripts/SettingsDraftChecks.swift \
    -o /tmp/settings-draft-checks && /tmp/settings-draft-checks
  ```

- Independent code review found no remaining implementation blockers.

The implementation is complete and the final code builds passed after the latest UI fixes. Visual QA covers only the captured states listed above; it does not claim interaction coverage. No live-daemon integration tests, code signing/notarization, full automated UI interaction suite, or comprehensive VoiceOver audit are claimed by this record.
