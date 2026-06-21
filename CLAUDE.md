# singctl — Project Instructions for AI Agents

`singctl` is a terminal-UI VLESS proxy / VPN client built on an **embedded
sing-box core** (v1.13.12). It runs a local SOCKS/HTTP proxy and can flip a
system-wide VPN (TUN) mode on and off, while **passively coexisting** with
Cisco Secure Client (AnyConnect). The primary use case is routing the traffic
of selected applications through a corporate VPN so they get outbound access.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:7510c1e2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Session Completion

**When ending a work session**, you MUST complete ALL steps below. Work is NOT complete until `git push` succeeds.

**MANDATORY WORKFLOW:**

1. **File issues for remaining work** - Create issues for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **PUSH TO REMOTE** - This is MANDATORY:
   ```bash
   git pull --rebase
   git push
   git status  # MUST show "up to date with origin"
   ```
5. **Clean up** - Clear stashes, prune remote branches
6. **Verify** - All changes committed AND pushed
7. **Hand off** - Provide context for next session

**CRITICAL RULES:**
- Work is NOT complete until `git push` succeeds
- NEVER stop before pushing - that leaves work stranded locally
- NEVER say "ready to push when you are" - YOU must push
- If push fails, resolve and retry until it succeeds
<!-- END BEADS INTEGRATION -->

## MANDATORY: track every task in beads

**Every change to this project MUST be tracked as a bead.** This is a hard
project rule, not a suggestion:

1. `bd init` once per clone (already done; the Dolt DB lives under `.beads/`).
2. **Before** starting work, `bd create "<title>"` (or claim an existing one)
   and set it `--status in_progress`.
3. Reference the bead ID in the commit message body (e.g. `singctl-proxy-sol`).
4. **Definition of Done** for any bead:
   - `go test ./...` green (hermetic, runs on the stub/fake core),
   - `go build -tags singbox ./...` compiles against the real sing-box,
   - golden config diffs reviewed deliberately,
   - the bead closed (`bd close <id>`).

Do not use TodoWrite / TaskCreate / markdown TODO lists — beads is the single
source of truth.

## Build & Test

```bash
go build ./...                      # default build (stub core, no sing-box link)
go test ./...                       # hermetic unit + golden suite (FakeCore)
go build -tags singbox ./...        # shipping build: links the real sing-box core
go test ./internal/singbox -update  # regenerate golden config files (review diff!)
make build                          # version-stamped binary (see Makefile)
```

The default build/test path **never imports sing-box** — it links a stub core,
so the whole suite is fast and CGO-free. Only `-tags singbox` pulls in the real
core (`internal/core/real.go`).

`singctl` must run as **root** (TUN device + route management). The TUI shows a
preflight error otherwise.

## Architecture Overview

Hexagonal: a pure decision/config core surrounded by injected I/O ports, with
the only sing-box dependency isolated behind a build tag.

```
cmd/singctl            entry point: flags/env/profile precedence, wiring,
                       headless vs TUI, embedded man page (singctl.1)
internal/vless         parse vless:// links -> ServerProfile / ProfileSet (pure)
internal/singbox       build sing-box JSON config as Go structs (pure; NO sing-box
                       import). Contract = JSON, verified by testdata/*.golden.json
internal/core          Core/Factory abstraction over a sing-box Box:
                         real.go  (//go:build singbox) — the only sing-box import
                         stub.go  / fakecore.go — used by default builds & tests
internal/runtime       Manager: two-instance lifecycle (PROXY + TUN forwarder),
                       state machine, ConfigBuilder / InterfaceProber /
                       RouteController ports
internal/app           Executor: composition root; implements ui.Backend; applies
                       monitor policy decisions to the Manager
internal/ui            Bubble Tea TUI (pure reducer); async data via notes channel
internal/netstate      passive, read-only network/Cisco state detection
internal/monitor       debounced event loop over netstate
internal/policy        pure Cisco-coexistence decision engine (no I/O)
internal/profile       persist the key(s) under the real user's ~/.config/singctl
internal/clashapi      sing-box Clash API client + connection poller (observability)
internal/procproxy     per-process routing (Linux cgroup/nftables) + env-launch
                       fallback (other OSes); also PID restart-in-proxy
internal/proclist      enumerate processes with sockets (pid/port/name) for the
                       per-process picker (lsof on macOS, /proc on Linux)
internal/control       instance advertisement (instance.json) + Unix control
                       socket; lets a second invocation attach to logs and
                       stop/status a running instance
```

### Two sing-box instances
- **PROXY** (persistent): SOCKS `127.0.0.1:1080` + HTTP `127.0.0.1:2080`. In VPN
  mode its outbounds get `bind_interface`=physical NIC so egress escapes our own
  TUN; in proxy-only mode they ride the default route.
- **TUN forwarder** (on-demand, VPN mode only): owns the default route via
  `auto_route`, relays everything to the PROXY over SOCKS, hijacks DNS at the TUN
  edge. Subnet `198.18.0.0/30` (avoids Cisco's 172.18/16).

### Key invariants (do not break)
- **`internal/singbox` stays pure** and never imports sing-box. Any config change
  = edit the structs + regenerate goldens (`-update`) and review the diff.
- **The only sing-box import is `internal/core/real.go` behind `//go:build
  singbox`.** New runtime dependencies must be reachable from the default
  (non-singbox) build via interfaces + fakes (see `core.Factory`,
  `runtime.InterfaceProber`, `runtime.RouteController`).
- **UI is a pure reducer.** Async data arrives via the `notes chan tea.Msg`;
  blocking backend calls are wrapped in `tea.Cmd` (`internal/ui/commands.go`).
  `ui.Backend` is the UI↔runtime port — extending it means updating the fakes in
  every `internal/ui/*_test.go`.
- **Cisco coexistence is observe-only.** Never mutate AnyConnect's routes; yield
  (suspend) while it is connecting/active and resume after it disconnects.
- **Platform-specific code is build-tag / file-suffix split** (`*_linux.go`,
  `*_darwin.go`, `*_other.go`), each with a pure-Go fake for tests.

## Features

- **Multiple VLESS keys with automatic failover.** Pass several `--key` flags (or
  newline-separated `SINGCTL_KEY`/`SINGCTL_KEYS`); a sing-box `urltest` group
  latency-tests them and selects the fastest reachable server (priority follows
  input order). Single-key configs are byte-identical to before.
- **Enriched connection logging.** sing-box's Clash API (loopback only, random
  secret, on by default) is polled for live connections; each is logged with the
  **source process**, source ip:port, full destination host/ip:port, network and
  outbound chain, and shown in a live TUI connections view.
- **Per-process proxying by PID.** On Linux, route an already-running PID's
  traffic through the proxy via cgroup v2 + nftables fwmark (no env needed). On
  other OSes, `--launch -- <cmd>` spawns a child with proxy env injected.
- **Process picker.** The TUI process action (`x`) lists processes that have
  network sockets (pid / local ports / program name) so the target is easy to
  find; type to filter, ↑/↓ to choose, Enter to route, `^R` to restart.
- **Restart a PID in proxy mode.** `--restart-pid` / the picker's `^R` recover a
  process's argv (via `ps`), terminate it, and relaunch it routed through the
  proxy — the only way to proxy an already-running process on macOS (best-effort).
- **Attach / control a running instance.** A running instance advertises itself
  (`~/.config/singctl/instance.json`) and serves a Unix control socket, so a
  second invocation can `--attach` (live-tail its logs), `--status`, or `--stop`
  it — start it `--headless` in one tab and control it from another. These
  control commands run without root.
- **Masked keys + add a second key.** The connection-strings screen shows loaded
  keys masked (bullets + the `#name` label only) with a field to add another key
  (joins the failover group live); the raw key is never echoed.

## Conventions & Patterns

- Pure packages (`vless`, `singbox`, `policy`) do no I/O and are unit-tested
  directly; everything with I/O is behind an interface with an injectable fake.
- Golden-file testing for all sing-box JSON; never hand-edit goldens — regenerate.
- User-facing TUI/log strings are in Russian (match existing copy).
- Run under `sudo`; resolve the real user via `SUDO_USER` for file ownership.
