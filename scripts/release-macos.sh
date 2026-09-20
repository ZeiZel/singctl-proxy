#!/usr/bin/env bash
#
# release-macos.sh — one command that produces a complete, self-consistent macOS
# release: the CLI/daemon binary, the SwiftUI app with its embedded system
# extension, and the signed .pkg/.dmg installers in dist/.
#
# It exists because the pieces are built by three different toolchains that each
# carry their own version number, and nothing used to check they agree — a
# release shipped as "1.5.0" once contained a 1.4.1 app bundle. Every step here
# ends in a verification pass that fails loudly on that class of mistake.
#
#   scripts/release-macos.sh 1.5.0
#   scripts/release-macos.sh 1.5.0 --skip-app   # reuse the app built last time
#   scripts/release-macos.sh 1.5.0 --unsigned-installer
#
# --unsigned-installer leaves the .pkg unsigned. Signing it needs Apple's
# timestamp authority (timestamp.apple.com), which the corporate network here
# blocks — productsign then dies with "The timestamp service is not available
# (-67885)" after the rest of the release has already built fine.
#
# Signing identities are auto-detected from the login keychain; override with
# CODESIGN_IDENTITY / INSTALLER_IDENTITY. Notarization runs only when
# NOTARY_PROFILE (an `xcrun notarytool store-credentials` profile) is set —
# without it the artifacts are signed but not notarized, which Gatekeeper warns
# about on machines other than the build host.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

[ "$(uname -s)" = "Darwin" ] || { echo "error: macOS only" >&2; exit 1; }

VERSION="${1:-}"
[ -n "$VERSION" ] || {
	echo "usage: $0 <version> [--skip-app]   (e.g. $0 1.5.0)" >&2; exit 2; }
shift

SKIP_APP=0
SIGN_INSTALLER=1
for arg in "$@"; do
	case "$arg" in
		--skip-app) SKIP_APP=1 ;;
		--unsigned-installer) SIGN_INSTALLER=0 ;;
		*) echo "error: unknown argument '$arg'" >&2; exit 2 ;;
	esac
done

die() { echo "error: $*" >&2; exit 1; }
step() { echo; echo "════ $* ════"; }

# --- 0. Prerequisites -------------------------------------------------------
step "prerequisites"
command -v go >/dev/null || die "go not found"
if [ "$SKIP_APP" = "0" ]; then
	command -v xcodegen >/dev/null || die "xcodegen not found (brew install xcodegen)"
	command -v xcodebuild >/dev/null || die "xcodebuild not found (install Xcode)"
fi

# Auto-detect the Developer ID identities. `find-identity -p codesigning` hides
# the Installer certificate (it is not a codesigning cert), so query unfiltered.
find_identity() {
	security find-identity -v 2>/dev/null \
		| sed -n "s/.*\"\($1: [^\"]*\)\".*/\1/p" | head -1
}
: "${CODESIGN_IDENTITY:=$(find_identity 'Developer ID Application')}"
if [ "$SIGN_INSTALLER" = "1" ]; then
	: "${INSTALLER_IDENTITY:=$(find_identity 'Developer ID Installer')}"
else
	INSTALLER_IDENTITY=""
fi
[ -n "$CODESIGN_IDENTITY" ] || echo "warning: no Developer ID Application identity — CLI will be unsigned" >&2
[ -n "$INSTALLER_IDENTITY" ] || echo "warning: .pkg will be UNSIGNED (Gatekeeper will object on other machines)" >&2
echo "app/CLI signing : ${CODESIGN_IDENTITY:-<none>}"
echo "installer signing: ${INSTALLER_IDENTITY:-<none>}"
echo "notarization     : ${NOTARY_PROFILE:-<skipped, NOTARY_PROFILE unset>}"

# --- 1. Version -------------------------------------------------------------
step "version -> $VERSION"
"$REPO_ROOT/scripts/set-version.sh" "$VERSION"

# --- 2. CLI / daemon --------------------------------------------------------
# VERSION is passed explicitly: the Makefile otherwise derives it from
# `git describe`, which yields something like "782b7d7-dirty" in an untagged or
# dirty tree and would stamp that into `singctl --version`.
step "build CLI (bin/singctl)"
make build VERSION="$VERSION"

# --- 3. App + embedded system extension ------------------------------------
if [ "$SKIP_APP" = "1" ]; then
	step "skip app build (--skip-app)"
else
	step "build Singctl.app"
	make app-macos
fi

# --- 4. Installers ----------------------------------------------------------
step "build installers (dist/)"
CODESIGN_IDENTITY="$CODESIGN_IDENTITY" \
INSTALLER_IDENTITY="$INSTALLER_IDENTITY" \
NOTARY_PROFILE="${NOTARY_PROFILE:-}" \
	make pkg-macos PKG_VERSION="$VERSION"

# --- 5. Verify --------------------------------------------------------------
# Everything below re-reads the artifacts on disk. It must never trust what the
# build steps printed.
step "verify"
APP="$REPO_ROOT/macos/Singctl/build/Build/Products/Release/Singctl.app"
PKG="$REPO_ROOT/dist/singctl-${VERSION}.pkg"
DMG="$REPO_ROOT/dist/singctl-${VERSION}.dmg"
fail=0
check() { # check <description> <actual> <expected>
	if [ "$2" = "$3" ]; then
		printf '  ok   %-34s %s\n' "$1" "$2"
	else
		printf '  FAIL %-34s %s (want %s)\n' "$1" "$2" "$3"
		fail=1
	fi
}

check "bin/singctl --version" "$("$REPO_ROOT/bin/singctl" --version 2>/dev/null)" "singctl $VERSION"
check "Singctl.app version" \
	"$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP/Contents/Info.plist" 2>/dev/null)" \
	"$VERSION"
check "ProxyExtension version" \
	"$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' \
		"$APP/Contents/Library/SystemExtensions/ProxyExtension.systemextension/Contents/Info.plist" 2>/dev/null)" \
	"$VERSION"
# The icon keys have been lost before to a PlistBuddy rewrite of Info.plist;
# an app that installs without an icon is easy to miss and hard to explain.
check "Singctl.app has an icon" \
	"$([ -f "$APP/Contents/Resources/AppIcon.icns" ] && echo yes || echo no)" "yes"

# The installer is what the user actually runs, so verify what is INSIDE it
# rather than the build outputs it was assembled from — that is exactly how a
# 1.5.0 .pkg containing a 1.4.1 app got shipped. build-installers.sh deletes its
# staging tree on the way out, so expand the finished package.
EXPAND_DIR="$(mktemp -d)"
trap 'rm -rf "$EXPAND_DIR"' EXIT
if pkgutil --expand-full "$PKG" "$EXPAND_DIR/pkg" >/dev/null 2>&1; then
	SHIPPED_CLI="$(find "$EXPAND_DIR/pkg" -path '*usr/local/bin/singctl' -type f 2>/dev/null | head -1)"
	SHIPPED_APP_PLIST="$(find "$EXPAND_DIR/pkg" -path '*Singctl.app/Contents/Info.plist' -type f 2>/dev/null | head -1)"
	check "CLI inside the .pkg" "$("$SHIPPED_CLI" --version 2>/dev/null)" "singctl $VERSION"
	check "app inside the .pkg" \
		"$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$SHIPPED_APP_PLIST" 2>/dev/null)" \
		"$VERSION"
	# Substring, not an anchored match: the outbound type name is embedded in a
	# larger string constant. Counted rather than `grep -q`, because grep -q
	# closes the pipe early, `strings` dies of SIGPIPE and `set -o pipefail`
	# then reports the whole pipeline as failed even on a match.
	xhttp_hits="$(strings "$SHIPPED_CLI" 2>/dev/null | grep -c 'vless-xhttp' || true)"
	check "XHTTP support compiled in" \
		"$([ "${xhttp_hits:-0}" -gt 0 ] && echo yes || echo no)" "yes"

	# Protocol support is gated by sing-box build tags, and getting one wrong is
	# invisible until a user's key fails: without with_quic the hysteria /
	# hysteria2 / tuic outbounds parse fine and then refuse to start; without
	# with_utls REALITY does not work; without with_wireguard the wireguard
	# endpoint parses fine and then refuses to construct. Read the tags back
	# out of the shipped binary rather than trusting what the Makefile was
	# supposed to pass.
	shipped_tags="$(go version -m "$SHIPPED_CLI" 2>/dev/null | sed -n 's/.*build[[:space:]]*-tags=//p' | head -1)"
	for tag in singbox with_utls with_clash_api with_quic with_wireguard with_gvisor; do
		case ",$shipped_tags," in
			*",$tag,"*) present=yes ;;
			*) present=no ;;
		esac
		check "build tag $tag" "$present" "yes"
	done

	# The rules file must actually be inside the package: it is the deliverable
	# a user imports on a fresh machine and hands to colleagues, and shipping
	# without it silently sends people back to the repository to find it.
	shipped_rules="$(find "$EXPAND_DIR/pkg" -path '*usr/local/share/singctl/singctl-proxy-rules.ini' -type f 2>/dev/null | head -1)"
	check "proxy rules shipped" "$([ -n "$shipped_rules" ] && echo yes || echo no)" "yes"
else
	printf '  FAIL %-34s could not expand the package\n' "inspect .pkg contents"
	fail=1
fi

for artifact in "$PKG" "$DMG"; do
	check "$(basename "$artifact") exists" "$([ -f "$artifact" ] && echo yes || echo no)" "yes"
done
if [ -n "$INSTALLER_IDENTITY" ]; then
	check ".pkg signature" \
		"$(pkgutil --check-signature "$PKG" 2>/dev/null | sed -n 's/^   Status: //p')" \
		"signed by a developer certificate issued by Apple for distribution"
fi

[ "$fail" = "0" ] || die "verification failed — do NOT ship these artifacts"

step "done"
echo "  $PKG"
echo "  $DMG"
echo
echo "The .pkg installs the app + CLI and STARTS the daemon (postinstall runs"
echo "launchctl bootstrap). To install without starting it:"
echo "  sudo installer -pkg \"$PKG\" -target /"
echo "  sudo launchctl bootout system /Library/LaunchDaemons/com.singctl.proxy.plist"
