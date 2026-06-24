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

# The daemon runs as root (launchd), but it must use the INSTALLING USER's
# ~/.config/singctl — same place the interactive `sudo singctl` looks — otherwise
# the client can't find/attach to the daemon and tries to bind its ports again
# ("address already in use"). We pin SUDO_USER/HOME in the plist so the root
# daemon resolves to that user's config dir (instance.json, profile, socket, log).
REAL_USER="${SUDO_USER:-$(id -un)}"
REAL_HOME="$(eval echo "~${REAL_USER}")"
[ -n "${REAL_USER}" ] && [ -d "${REAL_HOME}" ] || die "не удалось определить домашний каталог пользователя ${REAL_USER}"

echo "==> ставлю команду singctl -> ${BIN_DST}"
$SUDO install -m 0755 "${BIN_SRC}" "${BIN_DST}"

echo "==> ставлю LaunchDaemon -> ${PLIST_DST} (config: ${REAL_HOME}/.config/singctl)"
$SUDO tee "${PLIST_DST}" >/dev/null <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>${LABEL}</string>
    <key>ProgramArguments</key>
    <array>
        <string>${BIN_DST}</string>
        <string>--headless</string>
        <string>--vpn</string>
    </array>
    <key>EnvironmentVariables</key>
    <dict>
        <key>SUDO_USER</key>
        <string>${REAL_USER}</string>
        <key>HOME</key>
        <string>${REAL_HOME}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>10</integer>
    <key>StandardOutPath</key>
    <string>/var/log/singctl.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/singctl.log</string>
</dict>
</plist>
PLIST
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
