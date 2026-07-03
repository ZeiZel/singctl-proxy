#!/usr/bin/env bash
#
# build-installers.sh — macOS-only. Signs the Wails .app and produces a notarized
# .pkg (installs GUI + CLI + boot-start LaunchDaemon) and a drag-install .dmg.
#
# Inputs (built beforehand):
#   gui/build/bin/singctl.app       (make gui)         — override with APP_PATH
#                                    (falls back from the legacy singctl-gui.app
#                                    name if APP_PATH is left at its default)
#   bin/singctl                     (make build)       — override with CLI_BIN
#   packaging/macos/netextension/build/Build/Products/Release/SingctlProxy.app
#                                    (make build-netext) — override with NETEXT_APP
#                                    optional: when present it is staged into the
#                                    pkg at /Applications/SingctlProxy.app so the
#                                    installer also delivers the signed per-app
#                                    system extension container.
#
# Signing / notarization are applied only when the matching env vars are set, so
# CI can build UNSIGNED artifacts for PRs and fully signed ones on release:
#   CODESIGN_IDENTITY   "Developer ID Application: … (S3UCF4USYC)"   — sign the .app + CLI
#   INSTALLER_IDENTITY  "Developer ID Installer: … (S3UCF4USYC)"     — sign the .pkg
#   NOTARY_PROFILE      keychain profile for `notarytool` (or use the AC_* trio)
#   AC_APPLE_ID / AC_PASSWORD / AC_TEAM_ID                       — notary creds
set -euo pipefail

[ "$(uname -s)" = "Darwin" ] || { echo "error: macOS only" >&2; exit 1; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${PKG_VERSION:-$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist}"
APP_PATH="${APP_PATH:-$REPO_ROOT/gui/build/bin/singctl-gui.app}"
if [ ! -d "$APP_PATH" ] && [ -d "$REPO_ROOT/gui/build/bin/singctl.app" ]; then
	# The real Wails output is singctl.app; singctl-gui.app is a legacy/expected
	# name that doesn't actually get produced. Fall back transparently.
	APP_PATH="$REPO_ROOT/gui/build/bin/singctl.app"
fi
CLI_BIN="${CLI_BIN:-$REPO_ROOT/bin/singctl}"
NETEXT_APP="${NETEXT_APP:-$REPO_ROOT/packaging/macos/netextension/build/Build/Products/Release/SingctlProxy.app}"
ENTITLEMENTS="$REPO_ROOT/packaging/macos/singctl.entitlements"
CLI_ENTITLEMENTS="$REPO_ROOT/packaging/macos/singctl-cli.entitlements"
PKG_ID="com.singctl.proxy"

die() { echo "error: $*" >&2; exit 1; }
[ -d "$APP_PATH" ] || die "app bundle not found: $APP_PATH (run 'make gui')"
[ -x "$CLI_BIN" ] || die "CLI binary not found: $CLI_BIN (run 'make build')"
mkdir -p "$OUT_DIR"

# 1) Sign the .app (hardened runtime) when an identity is provided.
if [ -n "${CODESIGN_IDENTITY:-}" ]; then
	echo "==> codesign app"
	codesign --force --deep --options runtime --timestamp \
		--entitlements "$ENTITLEMENTS" --sign "$CODESIGN_IDENTITY" "$APP_PATH"
	codesign --verify --strict --verbose=2 "$APP_PATH"
else
	echo "==> skip app signing (CODESIGN_IDENTITY unset)"
fi

# 2) Stage the payload tree (mirrors final install locations).
STAGE="$OUT_DIR/pkgroot"
rm -rf "$STAGE"
mkdir -p "$STAGE/Applications" "$STAGE/usr/local/bin"
cp -R "$APP_PATH" "$STAGE/Applications/"
install -m 0755 "$CLI_BIN" "$STAGE/usr/local/bin/singctl"

# Stage the netextension container app (SingctlProxy.app, built by
# `make build-netext`). It ships Developer ID signed already (it embeds the
# ProxyExtension.systemextension, which must be signed at build time to
# activate), so we never re-sign it here — just verify the signature is
# intact and copy it in as-is. It rides along in the same pkg and gets
# notarized as part of the pkg submission below.
if [ -n "${NETEXT_APP:-}" ] && [ -d "$NETEXT_APP" ]; then
	echo "==> stage netextension app: $NETEXT_APP"
	codesign --verify --strict --verbose=2 "$NETEXT_APP" \
		|| die "netextension app failed codesign verification: $NETEXT_APP"
	cp -R "$NETEXT_APP" "$STAGE/Applications/"
else
	echo "==> skip netextension app (NETEXT_APP not found: ${NETEXT_APP:-unset}); building GUI+CLI-only pkg"
fi

# 2b) Sign the staged CLI with the App Group entitlement so it can read/write
#     the shared Group Container used by the Network Extension (see
#     internal/netext/controller_darwin.go and LICENSATION.md §3).
if [ -n "${CODESIGN_IDENTITY:-}" ]; then
	echo "==> codesign cli"
	codesign --force --options runtime --timestamp \
		--entitlements "$CLI_ENTITLEMENTS" --sign "$CODESIGN_IDENTITY" "$STAGE/usr/local/bin/singctl"
	codesign --verify --strict --verbose=2 "$STAGE/usr/local/bin/singctl"
else
	echo "==> skip cli signing (CODESIGN_IDENTITY unset)"
fi

# 3) Build the component pkg (postinstall installs the LaunchDaemon).
RAW_PKG="$OUT_DIR/singctl-raw.pkg"
PKG="$OUT_DIR/singctl-${VERSION}.pkg"
# Force the app bundles to install to their staged location (/Applications).
# pkgbuild defaults BundleIsRelocatable=true, so the Installer relocates (or
# skips) a bundle to wherever LaunchServices last saw it — which meant
# SingctlProxy.app (the system-extension container) silently failed to install,
# and the extension could never be found/activated. A component plist with
# BundleIsRelocatable=false pins both apps to /Applications.
COMPONENT="$OUT_DIR/component.plist"
pkgbuild --analyze --root "$STAGE" "$COMPONENT"
python3 - "$COMPONENT" <<'PY'
import sys, plistlib
path = sys.argv[1]
with open(path, "rb") as f:
    comps = plistlib.load(f)
for c in comps:
    c["BundleIsRelocatable"] = False
with open(path, "wb") as f:
    plistlib.dump(comps, f)
PY
pkgbuild --root "$STAGE" --component-plist "$COMPONENT" \
	--identifier "$PKG_ID" --version "$VERSION" \
	--scripts "$REPO_ROOT/packaging/macos/scripts" --install-location / "$RAW_PKG"
if [ -n "${INSTALLER_IDENTITY:-}" ]; then
	productsign --sign "$INSTALLER_IDENTITY" "$RAW_PKG" "$PKG"
	rm -f "$RAW_PKG"
else
	mv "$RAW_PKG" "$PKG"
	echo "==> pkg unsigned (INSTALLER_IDENTITY unset)"
fi

# 4) Build the drag-install .dmg (GUI only).
DMG="$OUT_DIR/singctl-${VERSION}.dmg"
DMG_STAGE="$OUT_DIR/dmg"
rm -rf "$DMG_STAGE"; mkdir -p "$DMG_STAGE"
cp -R "$APP_PATH" "$DMG_STAGE/"
ln -s /Applications "$DMG_STAGE/Applications"
hdiutil create -volname "singctl" -srcfolder "$DMG_STAGE" -ov -format UDZO "$DMG"

# 5) Notarize + staple both artifacts when creds are present.
notarize() {
	local artifact="$1"
	if [ -n "${NOTARY_PROFILE:-}" ]; then
		xcrun notarytool submit "$artifact" --keychain-profile "$NOTARY_PROFILE" --wait
	elif [ -n "${AC_APPLE_ID:-}" ] && [ -n "${AC_PASSWORD:-}" ] && [ -n "${AC_TEAM_ID:-}" ]; then
		xcrun notarytool submit "$artifact" --apple-id "$AC_APPLE_ID" \
			--password "$AC_PASSWORD" --team-id "$AC_TEAM_ID" --wait
	else
		echo "==> skip notarization of $(basename "$artifact") (no notary creds)"
		return 0
	fi
	xcrun stapler staple "$artifact"
}
notarize "$PKG"
notarize "$DMG"

rm -rf "$STAGE" "$DMG_STAGE"
echo "==> built:"
echo "    $PKG"
echo "    $DMG"
