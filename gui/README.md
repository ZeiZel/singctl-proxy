# singctl-gui — desktop GUI (Wails: Go + React)

A clash-verge-rev-style desktop app for singctl. It is **unprivileged**: it never
links sing-box and never needs root. Every action is sent to the running
**singctl daemon** over its Unix control socket, and live data (connections,
latency, traffic, per-app console) is read from the daemon's loopback Clash API.

```
React/TS frontend ──Wails bindings──▶ Go bridge ──control socket──▶ singctl daemon (root)
        ▲                                  │  Clash API (loopback)        │
        └──────── Wails events ────────────┘◀─────────────────────────────┘
```

## Layout

- `bridge/` — Go service layer (`App` is the Wails-bound type). Wraps
  `singctl/internal/control` + `singctl/internal/clashapi`; pollers emit Wails
  events (`status`, `traffic`, `connections`, `latency`, `console`).
- `frontend/` — React + TypeScript + Vite. Pages: Dashboard (mode switch +
  traffic chart), Proxies (latency), Connections, Apps (per-process routing),
  Keys, Console, Settings. No charting dependency — the traffic graph is a small
  custom SVG component.
- This is a **nested module** (`module singctl/gui`, `replace singctl => ../`) so
  the GUI never pulls sing-box into the main build, while still importing the
  pure `singctl/internal/*` packages.

## Develop / build

Requires the Wails CLI and (on Linux) GTK3 + WebKit2GTK 4.1 dev libraries:

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@latest
# Debian/Ubuntu: apt install build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev
```

> On Ubuntu (webkit2gtk-4.1) `wails doctor` falsely reports `libwebkit Not Found`
> — ignore it and always pass `-tags webkit2_41`.

```sh
make gui          # build  (cd gui && wails build -tags webkit2_41)
make gui-dev      # live dev server (http://localhost:34115, drives a running daemon)
make gui-test     # go test ./...  (bridge unit tests)
```

The GUI talks to whatever daemon is advertised at `~/.config/singctl/instance.json`
(install one with `make install`). With no daemon running the UI shows an
offline state and prompts to start the service.
