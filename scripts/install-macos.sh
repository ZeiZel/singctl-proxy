#!/usr/bin/env bash
#
# install-macos.sh — install singctl as a system tool on macOS.
#
#   sudo scripts/install-macos.sh            # install binary + LaunchDaemon
#   sudo scripts/install-macos.sh uninstall  # remove both
#
# Installs the binary to /usr/local/bin/singctl and a LaunchDaemon at
# /Library/LaunchDaemons/com.singctl.proxy.plist that runs the system VPN
# (singctl --headless --vpn) at boot. The VPN key comes from the saved profile,
# so save a key once interactively first (see docs/macos.md). This does NOT do
# per-process kernel interception (that needs a signed Network Extension) — it's
# a system-wide VPN daemon you manage with launchctl / --status / --attach / --stop.
set -euo pipefail

LABEL="com.singctl.proxy"
PLIST_DST="/Library/LaunchDaemons/${LABEL}.plist"
BIN_DST="/usr/local/bin/singctl"

# Resolve repo root from this script's location so it works from anywhere.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
PLIST_SRC="${REPO_ROOT}/packaging/macos/${LABEL}.plist"

die() { echo "error: $*" >&2; exit 1; }

[ "$(uname -s)" = "Darwin" ] || die "this installer is for macOS only"
[ "$(id -u)" = "0" ] || die "run with sudo (need /usr/local/bin + /Library/LaunchDaemons)"

if [ "${1:-install}" = "uninstall" ]; then
    echo "==> unloading ${LABEL}"
    launchctl bootout system "${PLIST_DST}" 2>/dev/null || launchctl unload -w "${PLIST_DST}" 2>/dev/null || true
    rm -f "${PLIST_DST}"
    rm -f "${BIN_DST}"
    echo "==> uninstalled singctl (kept ~/.config/singctl and /var/log/singctl.log)"
    exit 0
fi

# install
BIN_SRC="${REPO_ROOT}/bin/singctl"
[ -x "${BIN_SRC}" ] || die "binary not found at ${BIN_SRC} — run 'make build' first"
[ -f "${PLIST_SRC}" ] || die "plist not found at ${PLIST_SRC}"

echo "==> installing binary -> ${BIN_DST}"
install -m 0755 "${BIN_SRC}" "${BIN_DST}"

echo "==> installing LaunchDaemon -> ${PLIST_DST}"
install -m 0644 "${PLIST_SRC}" "${PLIST_DST}"
chown root:wheel "${PLIST_DST}"

echo "==> loading ${LABEL}"
launchctl bootout system "${PLIST_DST}" 2>/dev/null || true
launchctl bootstrap system "${PLIST_DST}" 2>/dev/null || launchctl load -w "${PLIST_DST}"

cat <<EOF

singctl installed as a system VPN daemon.

  IMPORTANT: save a VPN key once before it can connect:
      sudo singctl            # add a key in the TUI (Ключи), then quit
  The daemon reads the saved profile from your ~/.config/singctl.

  Manage it:
      launchctl list | grep singctl     # is it loaded?
      sudo singctl --status             # daemon status
      sudo singctl --attach             # live-tail its logs
      sudo singctl --stop               # stop the running instance
      tail -f /var/log/singctl.log      # daemon log

  Uninstall:
      sudo scripts/install-macos.sh uninstall
EOF
