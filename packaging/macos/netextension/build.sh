#!/usr/bin/env bash
# Build (and optionally sign) the singctl macOS transparent-proxy system
# extension from this scaffold.
#
# REQUIRES a Mac with Xcode + XcodeGen and an Apple Developer Team ID. This
# CANNOT run in the Go repo's Linux CI/container — it is the macOS-only step that
# finishes Variant C. Notarization (notarytool) + stapling is a further manual
# step once the build and on-device testing pass; see README.md.
set -euo pipefail
cd "$(dirname "$0")"

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "build.sh must run on macOS (needs Xcode + the NetworkExtension SDK)." >&2
  exit 1
fi
: "${DEVELOPMENT_TEAM:?set DEVELOPMENT_TEAM to your Apple Developer Team ID}"

command -v xcodegen >/dev/null 2>&1 || { echo "install xcodegen: brew install xcodegen" >&2; exit 1; }

echo "==> generating SingctlProxy.xcodeproj from project.yml"
xcodegen generate

echo "==> building (Release)"
xcodebuild \
  -project SingctlProxy.xcodeproj \
  -scheme SingctlProxy \
  -configuration Release \
  DEVELOPMENT_TEAM="$DEVELOPMENT_TEAM" \
  build

cat <<'NEXT'

Build succeeded. Remaining manual steps (see README.md):
  1. Run the .app once; approve the system extension in
     System Settings -> General -> Login Items & Extensions.
  2. Verify capture: route Cursor via singctl, then `lsof -nP -p <ext-host PID> -i`
     should show 127.0.0.1:1080 (the SOCKS proxy), not a direct AWS/Cloudflare hit.
  3. Notarize the .app: `xcrun notarytool submit ... --wait` then `xcrun stapler staple`.
NEXT
