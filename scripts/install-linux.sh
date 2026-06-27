#!/usr/bin/env bash
#
# install-linux.sh — install singctl as a system tool on Linux (binary + systemd).
#
#   scripts/install-linux.sh            # install binary + singctld.service
#   scripts/install-linux.sh uninstall  # remove both
#
# Privilege handling mirrors the macOS installer: do NOT run the build (or make)
# as root. Only the install touches root-owned paths (/usr/local/bin,
# /etc/systemd/system), so we self-elevate with sudo just for those steps.
#
# The systemd unit runs the system VPN (singctl --headless --vpn) at boot. The
# key comes from the saved profile, so save one once interactively first
# (`sudo singctl` → Ключи → add key → quit). This is the boot-start daemon the
# desktop GUI drives over the control socket.
set -euo pipefail

UNIT="singctld.service"
UNIT_DST="/etc/systemd/system/${UNIT}"
PREFIX="${PREFIX:-/usr/local}"
BIN_DST="${PREFIX}/bin/singctl"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
UNIT_SRC="${REPO_ROOT}/packaging/linux/${UNIT}"

die() { echo "error: $*" >&2; exit 1; }

[ "$(uname -s)" = "Linux" ] || die "this installer is for Linux only"
command -v systemctl >/dev/null 2>&1 || die "systemd (systemctl) is required"

SUDO=""
if [ "$(id -u)" != "0" ]; then
	SUDO="sudo"
	echo "==> установка в ${PREFIX}/bin и /etc/systemd/system требует sudo"
	sudo -v || die "не удалось получить права sudo"
fi

if [ "${1:-install}" = "uninstall" ]; then
	echo "==> останавливаю и выключаю ${UNIT}"
	$SUDO systemctl disable --now "${UNIT}" 2>/dev/null || true
	$SUDO rm -f "${UNIT_DST}" "${BIN_DST}"
	$SUDO systemctl daemon-reload
	echo "==> singctl удалён (профиль ~/.config/singctl сохранён)"
	exit 0
fi

BIN_SRC="${REPO_ROOT}/bin/singctl"
[ -x "${BIN_SRC}" ] || die "бинарь не найден: ${BIN_SRC} — сначала выполните 'make build'"
[ -f "${UNIT_SRC}" ] || die "шаблон unit не найден: ${UNIT_SRC}"

# Pin the daemon to the installing user's config dir (see the unit template).
REAL_USER="${SUDO_USER:-$(id -un)}"
REAL_HOME="$(eval echo "~${REAL_USER}")"
[ -n "${REAL_USER}" ] && [ -d "${REAL_HOME}" ] || die "не удалось определить домашний каталог пользователя ${REAL_USER}"

echo "==> ставлю команду singctl -> ${BIN_DST}"
$SUDO install -d "${PREFIX}/bin"
$SUDO install -m 0755 "${BIN_SRC}" "${BIN_DST}"

echo "==> ставлю ${UNIT} -> ${UNIT_DST} (config: ${REAL_HOME}/.config/singctl)"
sed -e "s|@BIN@|${BIN_DST}|g" -e "s|@USER@|${REAL_USER}|g" -e "s|@HOME@|${REAL_HOME}|g" \
	"${UNIT_SRC}" | $SUDO tee "${UNIT_DST}" >/dev/null

echo "==> включаю и запускаю ${UNIT}"
$SUDO systemctl daemon-reload
$SUDO systemctl enable --now "${UNIT}"

cat <<'EOF'

singctl установлен как системная команда (/usr/local/bin/singctl) + демон systemd.

  ВАЖНО: сохраните ключ один раз, чтобы демон мог подключиться:
      sudo singctl                  # раздел «Ключи» → добавить ключ → выйти
  Демон берёт ключ из сохранённого профиля (~/.config/singctl).

  Управление:
      systemctl status singctld         # статус демона
      sudo singctl --attach             # живой лог
      sudo singctl --stop               # остановить инстанс
      journalctl -u singctld -f         # лог демона

  Удалить:
      make uninstall                    # (или scripts/install-linux.sh uninstall)
EOF
