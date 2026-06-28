#!/bin/sh
# Runs after the .deb/.rpm installs. Best-effort: refresh systemd + desktop caches.
# The boot-start service is a per-user template and is NOT auto-enabled here —
# the installing host has no "the user" context. Enable it explicitly:
#     sudo systemctl enable --now singctld@$USER
set -e

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload || true
fi
if command -v update-desktop-database >/dev/null 2>&1; then
	update-desktop-database -q /usr/share/applications || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
	gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor || true
fi

echo "singctl installed."
echo "  GUI:     run 'singctl' from your launcher, or 'singctl-gui'"
echo "  CLI:     sudo singctl            (TUI; needs root for TUN)"
echo "  Service: sudo systemctl enable --now singctld@\$USER   (boot-start VPN)"
echo "  Save a key first: sudo singctl -> Ключи -> add key -> quit"

exit 0
