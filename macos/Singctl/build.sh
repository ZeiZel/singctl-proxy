#!/usr/bin/env bash
# Build (and optionally notarize) Singctl.app — the SwiftUI macOS app that
# hosts the embedded ProxyExtension transparent-proxy system extension.
#
# REQUIRES a Mac with Xcode + XcodeGen and an Apple Developer Team ID. This
# CANNOT run in the Go repo's Linux CI/container. Full Apple setup (App IDs,
# capabilities, entitlements, signing the CLI) is in ../../../LICENSATION.md.
#
# Env:
#   DEVELOPMENT_TEAM   Apple Developer Team ID (default: S3UCF4USYC; override for
#                      forks/other accounts).
#   CONFIGURATION      build config (default: Release).
#   NOTARY_PROFILE     (optional) notarytool keychain profile; if set, the built
#                      .app is zipped, submitted to notarytool, and stapled.
set -euo pipefail
cd "$(dirname "$0")"

CONFIGURATION="${CONFIGURATION:-Release}"
DEVELOPMENT_TEAM="${DEVELOPMENT_TEAM:-S3UCF4USYC}"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build.sh must run on macOS (needs Xcode + the NetworkExtension SDK)." >&2
  exit 1
fi
echo "==> using DEVELOPMENT_TEAM=$DEVELOPMENT_TEAM (override with DEVELOPMENT_TEAM=<id> to use a different Apple Developer Team ID)"

if ! command -v xcodegen >/dev/null 2>&1; then
  echo "==> xcodegen not found; installing via Homebrew"
  brew install xcodegen || { brew tap xcodegen/xcodegen && brew install xcodegen; }
fi
command -v xcodebuild >/dev/null 2>&1 || { echo "Xcode command line tools required (xcode-select --install)." >&2; exit 1; }

echo "==> generating Singctl.xcodeproj from project.yml"
xcodegen generate

echo "==> building ($CONFIGURATION)"
DERIVED="$(pwd)/build"
# Manual Developer ID signing (identity + provisioning profiles set per target
# in project.yml). A plain build then yields a Developer-ID-signed .app that is
# ready to notarize — no archive/export nor device registration needed. The
# Developer ID provisioning profiles (which carry the *-systemextension NE
# entitlement) must be installed in ~/Library/MobileDevice/Provisioning Profiles.
build_once() {
  xcodebuild \
    -scheme Singctl \
    -configuration "$CONFIGURATION" \
    -derivedDataPath "$DERIVED" \
    -allowProvisioningUpdates \
    DEVELOPMENT_TEAM="$DEVELOPMENT_TEAM" \
    OTHER_CODE_SIGN_FLAGS="--timestamp" \
    CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO \
    build
}

# OTHER_CODE_SIGN_FLAGS=--timestamp: notarization requires a secure timestamp;
# a plain build otherwise signs with --timestamp=none.
# CODE_SIGN_INJECT_BASE_ENTITLEMENTS=NO: stops Xcode injecting the debug
# get-task-allow entitlement, which notarization rejects for distribution.
if ! build_once; then
  echo "==> build failed; if the error mentions a stale *.cstemp codesign" >&2
  echo "    file, retry once with a clean derivedData dir:" >&2
  echo "    rm -rf build && ./build.sh" >&2
  exit 1
fi

APP="$DERIVED/Build/Products/$CONFIGURATION/Singctl.app"
echo "==> built: $APP (embeds ProxyExtension.systemextension)"

if [[ -n "${NOTARY_PROFILE:-}" ]]; then
  if [[ ! -d "$APP" ]]; then
    echo "cannot notarize: $APP not found" >&2; exit 1
  fi
  ZIP="$DERIVED/Singctl.zip"
  echo "==> notarizing via profile '$NOTARY_PROFILE'"
  /usr/bin/ditto -c -k --keepParent "$APP" "$ZIP"
  xcrun notarytool submit "$ZIP" --keychain-profile "$NOTARY_PROFILE" --wait
  xcrun stapler staple "$APP"
  echo "==> notarized + stapled"
fi

cat <<NEXT

Build succeeded. Remaining steps (see LICENSATION.md):
  1. Run "$APP" once; approve the system extension in
     System Settings -> General -> Login Items & Extensions.
  2. Sign the singctl CLI with the App Group entitlement so it can write the
     shared config.json (see LICENSATION.md §3).
  3. Verify capture: isolate an app in singctl, then 'lsof -nP -p <PID> -i'
     should show 127.0.0.1:1080 (the SOCKS proxy), not a direct AWS/Cloudflare hit.
  4. Notarize (if not done above): re-run with NOTARY_PROFILE=<profile> set.
NEXT
