#!/usr/bin/env bash
#
# build-appimage.sh — package the singctl GUI as a portable AppImage.
#
# Bundles the GTK/WebKit2 runtime via linuxdeploy + its gtk plugin, so the
# AppImage runs on distros without libwebkit2gtk-4.1 installed. Requires the GUI
# binary already built (make gui) and network access to fetch the linuxdeploy
# tools on first run. FUSE is needed to run the resulting AppImage (CI uses
# APPIMAGE_EXTRACT_AND_RUN=1).
#
#   ARCH=x86_64 ./packaging/linux/appimage/build-appimage.sh
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
ARCH="${ARCH:-$(uname -m)}"
VERSION="${PKG_VERSION:-$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo dev)}"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist}"
TOOL_DIR="${TOOL_DIR:-$REPO_ROOT/dist/.appimagetools}"

GUI_BIN="${GUI_BIN:-$REPO_ROOT/gui/build/bin/singctl-gui}"
CLI_BIN="${CLI_BIN:-$REPO_ROOT/bin/singctl-linux-${GOARCH:-amd64}}"

die() { echo "error: $*" >&2; exit 1; }
[ -x "$GUI_BIN" ] || die "GUI binary not found: $GUI_BIN (run 'make gui' first)"

mkdir -p "$OUT_DIR" "$TOOL_DIR"

# Fetch linuxdeploy + the GTK plugin (cached in TOOL_DIR).
fetch() {
	local url="$1" dst="$2"
	[ -x "$dst" ] && return 0
	echo "==> downloading $(basename "$dst")"
	curl -fsSL "$url" -o "$dst"
	chmod +x "$dst"
}
fetch "https://github.com/linuxdeploy/linuxdeploy/releases/download/continuous/linuxdeploy-${ARCH}.AppImage" "$TOOL_DIR/linuxdeploy"
fetch "https://raw.githubusercontent.com/linuxdeploy/linuxdeploy-plugin-gtk/master/linuxdeploy-plugin-gtk.sh" "$TOOL_DIR/linuxdeploy-plugin-gtk.sh"

# Stage the AppDir.
APPDIR="$OUT_DIR/AppDir"
rm -rf "$APPDIR"
mkdir -p "$APPDIR/usr/bin"
install -m 0755 "$GUI_BIN" "$APPDIR/usr/bin/singctl-gui"
[ -x "$CLI_BIN" ] && install -m 0755 "$CLI_BIN" "$APPDIR/usr/bin/singctl" || true

export APPIMAGE_EXTRACT_AND_RUN=1
export OUTPUT="$OUT_DIR/singctl-${VERSION}-${ARCH}.AppImage"
export DEPLOY_GTK_VERSION=3

"$TOOL_DIR/linuxdeploy" \
	--appdir "$APPDIR" \
	--executable "$APPDIR/usr/bin/singctl-gui" \
	--desktop-file "$REPO_ROOT/packaging/linux/singctl.desktop" \
	--icon-file "$REPO_ROOT/gui/build/appicon.png" \
	--plugin gtk \
	--output appimage

echo "==> built $OUTPUT"
