# Spec: singctl 2.0 — stability, observability and settings

Status: approved, in implementation. Written 2026-09-12.

Six defects reported from a live 1.13.1 install. Three are behavioural bugs with
identified root causes, three are gaps in the UI's usefulness. The version is
2.0.0 because one fix deliberately changes what the daemon does at startup.

---

## F1 — The PAC port belongs to singctl alone

**Reported:** `Apply mode` fails with
`sysproxy: pac server listen on 21080: bind: address already in use`.

**Root cause:** the standalone `mac-proxy` utility ships a LaunchAgent with the
*same label* (`com.singctl.pacserver`) on the *same port* (21080). singctl 1.13
serves the PAC in-process, so the two fight over one port, and whoever loses
reports a bind error the user cannot act on.

**Required:**
1. **Own the state.** The PAC mode/config lives only in singctl's own config;
   nothing else may be consulted or written.
2. **Never fail on a busy port.** `pac_port = 0` (the new default) binds an
   ephemeral port and publishes the real URL through `SYSPROXY-STATUS`. A user
   who pins a port explicitly still gets an error, but an actionable one.
3. **Diagnose the conflict.** When a configured port is taken, the error must
   name what holds it (pid + process path via `lsof`-equivalent) and, when that
   is our own legacy LaunchAgent, say exactly how to remove it. Do NOT kill
   someone else's process automatically — a daemon that silently kills whatever
   holds a port is worse than a clear error.
4. Offer an explicit, user-initiated "take over the port" action in the UI that
   boots out the legacy agent *only* when it is verifiably ours (same
   ownership check `preinstall` already performs).

## F1b — The system proxy must survive a daemon restart

**Found while reviewing F1, not reported — and it would have made "singctl
replaces mac-proxy" false.**

The sysproxy config is held in memory only: nothing writes it, nothing restores
it. So after the daemon restarts (reboot, upgrade, crash):

- singctl believes the mode is `off` and serves no PAC;
- macOS still points `autoproxyurl` at the PAC URL applied earlier;
- that URL is dead, so the system proxy is broken — and with `pac_port = 0` the
  port has changed too, so it can never come back on its own.

This was masked until now: mac-proxy's python LaunchAgent kept serving 21080
across restarts. Removing it exposes the gap on the first reboot.

**Required:**
1. Persist the applied sysproxy config next to the other user state
   (`~/.config/singctl`), written by the same store discipline as the profile —
   root daemon, chowned back to the real user.
2. On daemon startup, restore it: bring the PAC server up and re-apply
   `networksetup` so the URL matches the port actually bound this run.
3. Restoration is best-effort and must never prevent the daemon from starting
   (same rule as F2 item 3).
4. A `mode = off` config restores nothing and touches no system state.

## F2 — The daemon must not start a VPN by itself

**Reported:** on first launch VPN turns itself on; switching to proxy/off hangs
the app; it repeats in a loop.

**Root cause, confirmed in the installed plist and the daemon log:**
`ProgramArguments` is `singctl --headless --vpn`, `RunAtLoad=true`,
`KeepAlive=true`, `ThrottleInterval=10`. So the daemon always starts in VPN
mode; on this machine it fails with `enable vpn: start forwarder: no physical
interface detected`; launchd restarts it ten seconds later; forever. The log
shows exactly that cycle.

**Required:**
1. The LaunchDaemon starts the daemon with **no mode** — `off`. Remove `--vpn`
   from the plist the installer writes and from `scripts/install-macos.sh`.
2. A persisted **autostart mode** setting (`off` | `proxy` | `vpn`, default
   `off`), applied by the daemon after it is otherwise up, surfaced in Settings.
   A failure to apply it must leave the daemon running in `off`, never exit.
3. **The daemon must never exit because a mode failed to start.** Combined with
   `KeepAlive`, exiting is what produced the loop. Report the failure, stay up.
4. **Mode switching must not hang.** `MODE` currently calls straight into
   `runtime.Manager`, whose lock is held for the whole of a forwarder start or
   teardown; while that is stuck, every later command blocks behind it.
   Required: bound every mode transition, return a timeout error instead of
   blocking indefinitely, and make a stuck transition recoverable without
   restarting the app.
5. **Per-command control deadlines.** `control.ConnDeadline` is a single
   3-minute value (raised for subscription fetches). That turns a 15-second
   hang into a three-minute one for *every* command. Deadlines must be
   per-command: fast commands (`MODE`, `STATUS`, `KEYS-*`) get a short one,
   subscription and system-proxy commands keep the long one.
6. **Silence the log flood.** `MallocStackLogging: can't turn off malloc stack
   logging` is emitted by every child process the daemon spawns and dominates
   the log. Strip it at the source (do not inherit the variable into children).

## F3 — Settings worth using

**Reported:** the screen is a thin list; asked for parity with mature VPN
clients.

**Required** — grouped, searchable, each with a one-line explanation:
- **General:** autostart mode (F2), launch at login, show menu-bar item, confirm
  before switching to VPN.
- **Proxy:** SOCKS/HTTP ports with conflict detection *before* apply, local
  listen address.
- **System proxy:** default mode, network service selection from the live list,
  PAC port (0 = automatic), link to the rules editor.
- **Routing:** urltest URL/interval/tolerance, per-mode DNS choice.
- **Observability:** Clash API on/off + address, log level, log retention.
- **Maintenance:** open config directory, export a diagnostics bundle (config +
  recent logs, secrets redacted), reset to defaults, uninstall instructions.
- Every destructive or wide-reaching control asks first. Apply stays explicit.

## F4 — Logs: fast, flat, readable

**Reported:** slow to load; nested boxes inside boxes.

**Required:** drop the redundant container chrome (one surface, not three);
tail-load the last N lines and stream the rest lazily rather than loading the
whole file; virtualised list so a large log does not stall the window;
level/source filtering, text search, copy and reveal-in-Finder; follow-tail
toggle that stops fighting the user's scroll.

## F5 — Console vs Logs must be self-evident

**Reported:** Console is empty and the difference from Logs is unclear.

They are genuinely different: **Logs** is the daemon and sing-box; **Console**
is stdout/stderr of applications launched *through* the proxy, which is empty
until such an app is launched. The UI never says this.

**Required:** state the distinction in each screen's subtitle; in Console's
empty state, explain what produces output and link to the Apps screen that
launches one. If, after that, Console still has no independent value, fold it
into Logs as a source filter — a screen that is empty for most users forever is
not worth a sidebar entry.

## F6 — Connections must show what is actually happening

**Reported:** always "No active connections", no apps, no hosts, no traffic.

**Required:**
1. Live connection rows from the Clash API: process/app, destination host, rule
   and outbound chain, up/down bytes, duration. Sortable, filterable, with a
   close-connection action.
2. Aggregate per application and per destination, so "what is using the tunnel"
   is answerable at a glance.
3. **Diagnose emptiness instead of asserting it.** "No active connections" must
   distinguish: the Clash API is unreachable; the API is up but no traffic is
   flowing; no mode is active. The Dashboard currently tells the user to enable
   a Clash API that Settings already shows enabled — that contradiction must be
   impossible.
4. Minimal firewall tooling: block/allow a destination or a process, persisted
   as routing rules, visible and removable in one place.
5. A visible link between a connection and the System proxy rules that routed
   it, so a user can jump from "this went direct" to the rule responsible.

---

## Acceptance

- Every existing test passes; goldens unchanged except where a spec item
  requires a config change, and then deliberately.
- `make test-singbox-decode` and the XHTTP end-to-end suite still pass.
- The daemon, started fresh from the installed plist, comes up in `off`, stays
  up when a mode fails, and never restarts in a loop.
- Mode switching returns within its deadline under a forced forwarder failure.
- A busy PAC port never blocks `Apply mode` with the default settings.
