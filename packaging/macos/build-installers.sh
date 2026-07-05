#!/usr/bin/env bash
#
# build-installers.sh — macOS-only. Verifies the native Singctl.app and CLI
# signatures and produces a notarized .pkg (installs GUI + CLI + boot-start
# LaunchDaemon) and a drag-install .dmg.
#
# Inputs (built beforehand):
#   macos/Singctl/build/Build/Products/Release/Singctl.app
#                                    (make app-macos)   — override with APP_PATH
#                                    The SwiftUI app that HOSTS the embedded
#                                    ProxyExtension.systemextension (transparent
#                                    proxy). It is signed Developer ID by
#                                    xcodebuild at build time (see project.yml:
#                                    manual signing, "singctl proxy DevID" /
#                                    "singctl netext DevID" provisioning
#                                    profiles) — this script only VERIFIES it,
#                                    it never re-signs it (re-signing with
#                                    --deep would blow away the nested
#                                    extension's signature and its NE /
#                                    system-extension entitlements).
#   bin/singctl                     (make build)       — override with CLI_BIN
#
# Signing / notarization are applied only when the matching env vars are set, so
# CI can build UNSIGNED artifacts for PRs and fully signed ones on release:
#   CODESIGN_IDENTITY   "Developer ID Application: … (S3UCF4USYC)"   — sign the CLI
#   INSTALLER_IDENTITY  "Developer ID Installer: … (S3UCF4USYC)"     — sign the .pkg
#   NOTARY_PROFILE      keychain profile for `notarytool` (or use the AC_* trio)
#   AC_APPLE_ID / AC_PASSWORD / AC_TEAM_ID                       — notary creds
set -euo pipefail

[ "$(uname -s)" = "Darwin" ] || { echo "error: macOS only" >&2; exit 1; }

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
VERSION="${PKG_VERSION:-$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist}"
APP_PATH="${APP_PATH:-$REPO_ROOT/macos/Singctl/build/Build/Products/Release/Singctl.app}"
CLI_BIN="${CLI_BIN:-$REPO_ROOT/bin/singctl}"
CLI_ENTITLEMENTS="$REPO_ROOT/packaging/macos/singctl-cli.entitlements"
PKG_ID="com.singctl.proxy"

die() { echo "error: $*" >&2; exit 1; }
[ -d "$APP_PATH" ] || die "app bundle not found: $APP_PATH (run 'make app-macos')"
[ -x "$CLI_BIN" ] || die "CLI binary not found: $CLI_BIN (run 'make build')"
mkdir -p "$OUT_DIR"

# 1) Verify the .app signature. It ships Developer ID signed already (built by
#    xcodebuild via `make app-macos`, embedding the signed
#    ProxyExtension.systemextension) — never re-sign it here.
echo "==> verify app signature: $APP_PATH"
codesign --verify --deep --strict --verbose=2 "$APP_PATH" \
	|| die "app failed codesign verification: $APP_PATH (run 'make app-macos')"

# 2) Stage the payload tree (mirrors final install locations).
STAGE="$OUT_DIR/pkgroot"
rm -rf "$STAGE"
mkdir -p "$STAGE/Applications" "$STAGE/usr/local/bin"
cp -R "$APP_PATH" "$STAGE/Applications/"
install -m 0755 "$CLI_BIN" "$STAGE/usr/local/bin/singctl"

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
# Force the app bundle to install to its staged location (/Applications).
# pkgbuild defaults BundleIsRelocatable=true, so the Installer relocates (or
# skips) a bundle to wherever LaunchServices last saw it — which previously
# meant the system-extension container app could silently fail to install,
# and the extension could never be found/activated. A component plist with
# BundleIsRelocatable=false pins Singctl.app to /Applications.
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
