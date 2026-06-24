#!/usr/bin/env bash
#
# install-macos.sh — install singctl as a system tool on macOS.
#
#   scripts/install-macos.sh            # install binary + LaunchDaemon
#   scripts/install-macos.sh uninstall  # remove both
#
# Privilege handling is brew-style: we do NOT run the build (or make) as root.
# Only the install touches root-owned paths (/usr/local/bin, /Library/LaunchDaemons),
# so we ask for the sudo password ONCE up front (sudo -v) and run just those
# steps with sudo — everything else stays as your user.
#
# Result: `singctl` becomes a normal command in /usr/local/bin (on macOS's default
# PATH), runnable as `sudo singctl` (it needs root for the TUN device). The
# LaunchDaemon runs the system VPN (singctl --headless --vpn) at boot; the key
# comes from the saved profile, so save one once interactively first (docs/macos.md).
# This is NOT per-process kernel interception (that needs a signed Network
# Extension) — it is a system-wide VPN daemon managed via launchctl / --status /
# --attach / --stop.
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

# Acquire sudo once for the privileged steps. We prefix only those commands with
# $SUDO (empty when already root) — never `sudo make`, never the whole script.
SUDO=""
if [ "$(id -u)" != "0" ]; then
	SUDO="sudo"
	echo "==> для установки в /usr/local/bin и /Library/LaunchDaemons нужен sudo"
	sudo -v || die "не удалось получить права sudo"
fi

if [ "${1:-install}" = "uninstall" ]; then
	echo "==> выгружаю ${LABEL}"
	$SUDO launchctl bootout system "${PLIST_DST}" 2>/dev/null ||
		$SUDO launchctl unload -w "${PLIST_DST}" 2>/dev/null || true
	$SUDO rm -f "${PLIST_DST}" "${BIN_DST}"
	echo "==> singctl удалён (профиль ~/.config/singctl и /var/log/singctl.log сохранены)"
	exit 0
fi

# install — the binary is built by the normal user (make build); only the copy
# into the system paths needs sudo.
BIN_SRC="${REPO_ROOT}/bin/singctl"
[ -x "${BIN_SRC}" ] || die "бинарь не найден: ${BIN_SRC} — сначала выполните 'make build'"
[ -f "${PLIST_SRC}" ] || die "plist не найден: ${PLIST_SRC}"

echo "==> ставлю команду singctl -> ${BIN_DST}"
$SUDO install -m 0755 "${BIN_SRC}" "${BIN_DST}"

echo "==> ставлю LaunchDaemon -> ${PLIST_DST}"
$SUDO install -m 0644 "${PLIST_SRC}" "${PLIST_DST}"
$SUDO chown root:wheel "${PLIST_DST}"

echo "==> загружаю ${LABEL}"
$SUDO launchctl bootout system "${PLIST_DST}" 2>/dev/null || true
$SUDO launchctl bootstrap system "${PLIST_DST}" 2>/dev/null ||
	$SUDO launchctl load -w "${PLIST_DST}"

cat <<'EOF'

singctl установлен как системная команда (/usr/local/bin/singctl).

  Теперь можно запускать из любого места:
      sudo singctl                  # TUI (нужен root для TUN)

  ВАЖНО: сохраните ключ один раз, чтобы демон мог подключиться:
      sudo singctl                  # раздел «Ключи» → добавить ключ → выйти
  Демон берёт ключ из сохранённого профиля (~/.config/singctl).

  Управление:
      launchctl list | grep singctl     # загружен ли демон
      sudo singctl --status             # статус демона
      sudo singctl --attach             # живой лог
      sudo singctl --stop               # остановить инстанс
      tail -f /var/log/singctl.log      # лог демона

  Удалить:
      make uninstall                    # (или scripts/install-macos.sh uninstall)
EOF
