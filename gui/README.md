# singctl-gui — desktop GUI (Wails: Go + React)

A clash-verge-rev-style desktop app for `singctl`. It is **unprivileged**: it
never links sing-box and never needs root. Every action is sent to the running
**singctl daemon** over its Unix control socket, and live data (status, traffic,
connections, latency, per-app console) is read from the daemon's loopback Clash
API and pushed to the UI as Wails events.

```
React/TS frontend ──Wails bindings──▶ Go bridge ──control socket──▶ singctl daemon (root)
        ▲                                  │  Clash API (loopback)        │
        └──────── Wails events ────────────┘◀─────────────────────────────┘
```

The daemon is the boot-start service installed by `make install`
(LaunchDaemon on macOS, systemd on Linux). The GUI discovers it via
`~/.config/singctl/instance.json`; with no daemon running the UI shows an offline
state.

---

## Prerequisites

| Tool | Version | Notes |
| --- | --- | --- |
| Go | ≥ 1.24 | the repo's toolchain |
| Node.js | ≥ 18 | for the Vite/React frontend |
| Wails CLI | v2 | **Optional** — the `make` targets fall back to `go run github.com/wailsapp/wails/v2/cmd/wails@<version>`. Install it for faster repeat builds: `go install github.com/wailsapp/wails/v2/cmd/wails@latest` (then add `$(go env GOPATH)/bin` to `PATH`). |
| GTK3 + WebKit2GTK | 4.1 | **Linux only** (webview runtime) |

On Debian/Ubuntu:

```sh
sudo apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

> **Ubuntu webkit note.** Ubuntu ships `webkit2gtk-4.1`, not `4.0`, so
> `wails doctor` falsely reports `libwebkit Not Found`. Ignore it and always
> build/dev with **`-tags webkit2_41`** (the `make` targets already do).

macOS needs Xcode command-line tools (`xcode-select --install`); Windows uses the
bundled WebView2 (no extra tag).

---

## Build & run

From the **repo root** (the `make` targets cd into `gui/` for you):

```sh
make gui          # production build  → gui/build/bin/singctl-gui
make gui-dev      # live dev server   → http://localhost:34115 (hot reload)
make gui-test     # go bridge tests + frontend vitest suite
```

Equivalent manual commands (run inside `gui/`; drop `-tags webkit2_41` on macOS,
and replace `wails` with `go run github.com/wailsapp/wails/v2/cmd/wails@v2.12.0`
if the CLI is not installed):

```sh
wails build -tags webkit2_41          # full build (frontend + Go + package)
wails dev   -tags webkit2_41          # dev with hot reload + Go binding regen
go test ./...                         # Go bridge tests
cd frontend && npm install && npm run test   # frontend unit/component tests
```

`wails build`/`wails dev` regenerate the TypeScript bindings in
`frontend/wailsjs/` from the bound Go `bridge.App` type, then build the Vite
frontend and compile the Go binary with the frontend embedded.

---

## Project layout

```
gui/
├── main.go              Wails entrypoint (binds bridge.App, embeds frontend/dist)
├── bridge/              Go service layer (the "backend" of the GUI)
│   ├── app.go           bridge.App — Wails-bound methods (mode/keys/settings/proc…)
│   ├── daemon.go        daemon discovery via instance.json + control client
│   ├── poller.go        polls Clash API + control socket → emits Wails events
│   └── types.go         DTOs + conversions (mask keys, connection/latency rows)
├── wails.json           Wails project config
├── go.mod               module singctl/gui  (replace singctl => ../)
└── frontend/            React + TypeScript + Vite (see below)
```

The GUI is a **nested Go module** (`module singctl/gui`, `replace singctl => ../`)
so it never pulls sing-box into the build, while still importing the pure,
stdlib-only `singctl/internal/control` and `singctl/internal/clashapi` packages.

### Frontend (Feature-Sliced Design)

`frontend/src/` follows strict **FSD** — imports flow downward only
(`app → pages → widgets → features → entities → shared`), and every slice exposes
a public API via its `index.ts`. Aliases: `@` → `src`, `@wails` → `wailsjs`.

```
src/
├── app/         composition root: App layout + LiveProvider + global Tailwind css
├── pages/       dashboard · proxies · connections · apps · keys · console · settings
│                (compose widgets/features only; no api calls, no raw utilities)
├── widgets/     app-sidebar · traffic-chart · connections-table · latency-list
│                console-viewer · process-list · proxied-list  (prop-driven sections)
├── features/    mode-switch · manage-keys · edit-settings · launch-app
│                route-process · stop-daemon  (user actions + minimal state)
├── entities/    daemon (Zustand live store + selectors + StatusBadge)
│                connection · proxy (LatencyBar) · key · process  (domain types)
└── shared/      ui/ (CVA primitives) · lib/ (cn, format) · api/singctl (bridge
                 client + events + types) · config/ (nav)
```

Only `shared/api/singctl` and `entities/daemon` import `@wails`; everything above
them is transport-agnostic and unit-testable with plain props.

---

## Conventions

The frontend follows an adapted subset of the project code standard:

- **Styling: Tailwind + CVA.** Variant-bearing primitives live in `shared/ui`
  (Box, Stack, Card, Text/Heading, Button, Badge, Toggle, Input, Select, Table,
  EmptyState, Icon). Raw utility classes stay **inside** those primitives — pages
  compose named components, they don't sprinkle utilities. Class merging goes
  through the single `cn` helper (`twMerge(clsx(...))`, `shared/lib/cn`);
  conditional classes use the object form (`cn({ "...": cond })`).
- **State.** Shared live daemon data is a single **Zustand** store
  (`entities/daemon/model/liveStore.ts`) populated by `initLive()`, which
  subscribes to the Wails events. Local UI/form state stays in `useState`.
- **Naming.** Types `T*`/`I*`, enums `E*`, component props `<Component>Props`;
  no abbreviations (`index`, `value`, `event`, `error`). Named React imports only
  (no `React.*`). Files stay under ~200 lines.
- **No Storybook**, no E2E in this app; concise comments (rationale lives in code
  structure, not comment walls).

---

## Testing

- Runner: **vitest** + **@testing-library/react** (jsdom), setup in
  `frontend/src/test/setup.ts`; config in `frontend/vite.config.ts` (`test` block).
- Tests are **co-located**: `Component.test.tsx` next to `Component.tsx`, covering
  positive and negative paths.
- Presentational components render with custom props (no Wails needed). Slices
  that import the bridge (`liveStore`, feature hooks) mock it with
  `vi.mock("@/shared/api/singctl")`.

```sh
cd frontend
npm run test         # run once (CI)
npm run test:watch   # watch mode
```

Go bridge tests: `cd gui && go test ./...` (pure conversion/masking logic).

---

## How the GUI drives the daemon

`bridge.App` methods map to control-socket commands; the poller fans the daemon's
state out as Wails events the frontend subscribes to (`shared/api/singctl`):

| Area | Bound methods | Control / source |
| --- | --- | --- |
| Mode | `SetMode` | `MODE off\|proxy\|vpn` |
| Keys | `GetKeys` `AddKey` `RenameKey` `DeleteKey` | `KEYS-*` |
| Settings | `GetSettings` `ApplySettings` | `SETTINGS-GET/SET` |
| Per-app | `ListProcesses` `ListRouted` `RoutePID` `UnroutePID` `KillPID` `RestartPID` `LaunchApp` | `PROC-*` |
| Lifecycle | `GetStatus` `StopDaemon` | `STATUS` / `STOP` |
| Events | — | `status` `traffic` `connections` `latency` `console` |

---

## Troubleshooting

- **`wails doctor` says `libwebkit Not Found` (Linux).** False negative on
  Ubuntu/webkit2gtk-4.1 — build with `-tags webkit2_41`.
- **UI shows "daemon offline".** No live instance at
  `~/.config/singctl/instance.json`. Install/start the service (`make install`)
  and add a key, or run `singctl --daemon`.
- **`npm install` fails behind a proxy.** Retry — transient proxy 502s can drop
  package fetches; re-running fills the cache until it completes.
- **`Cannot find module @rollup/rollup-<os>-<arch>` / esbuild platform error.**
  `node_modules` was installed on a different OS (e.g. a Linux dev container) and
  reused on a shared checkout. `node_modules` holds platform-specific
  rollup/esbuild binaries and cannot be shared across OSes. Reset it:
  ```sh
  make gui-reset      # rm node_modules + package-lock.json + dist
  make gui            # reinstalls the correct binaries for this OS
  ```
  `package-lock.json` is intentionally **not** committed for the same reason.
