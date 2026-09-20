#!/usr/bin/env bash
#
# set-version.sh — write one version number into every place that hardcodes it.
#
# The version lives in more than one file, and they MUST agree:
#   macos/Singctl/App/Info.plist            CFBundleShortVersionString/CFBundleVersion
#   macos/Singctl/ProxyExtension/Info.plist  ditto — macOS refuses to load a system
#                                            extension whose version disagrees with
#                                            its host app
# The CLI's version is NOT stored anywhere: it is stamped at link time from the
# Makefile's VERSION (-X main.version=...), which release-macos.sh passes along.
#
# Editing is done in place with a targeted substitution, NEVER with PlistBuddy:
# PlistBuddy rewrites the whole file, dropping the XML comments AND reordering
# keys (it has silently eaten the CFBundleIconFile/CFBundleIconName keys here
# before, shipping an app with no icon).
#
#   scripts/set-version.sh 1.5.0
#   scripts/set-version.sh --check 1.5.0   # verify only, change nothing
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLISTS=(
	"$REPO_ROOT/macos/Singctl/App/Info.plist"
	"$REPO_ROOT/macos/Singctl/ProxyExtension/Info.plist"
)

CHECK_ONLY=0
if [ "${1:-}" = "--check" ]; then
	CHECK_ONLY=1
	shift
fi

VERSION="${1:-}"
[ -n "$VERSION" ] || { echo "usage: $0 [--check] <version>   (e.g. 1.5.0)" >&2; exit 2; }
[[ "$VERSION" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] || {
	echo "error: version must look like 1.5 or 1.5.0, got '$VERSION'" >&2; exit 2; }

for plist in "${PLISTS[@]}"; do
	[ -f "$plist" ] || { echo "error: missing $plist" >&2; exit 1; }
done

CHECK_ONLY="$CHECK_ONLY" VERSION="$VERSION" python3 - "${PLISTS[@]}" <<'PY'
import os, re, sys

version = os.environ["VERSION"]
check_only = os.environ["CHECK_ONLY"] == "1"
keys = ("CFBundleShortVersionString", "CFBundleVersion")
failed = False

for path in sys.argv[1:]:
    with open(path, encoding="utf-8") as f:
        text = f.read()
    updated = text
    for key in keys:
        # Match the <string> that follows this exact <key>, keeping whatever
        # indentation and line endings the file already uses.
        pattern = re.compile(
            r"(<key>" + re.escape(key) + r"</key>\s*\n\s*<string>)([^<]*)(</string>)"
        )
        match = pattern.search(updated)
        if not match:
            print(f"error: {path}: no <string> found for {key}", file=sys.stderr)
            failed = True
            continue
        if check_only:
            if match.group(2) != version:
                print(f"MISMATCH {path}: {key} = {match.group(2)!r}, want {version!r}",
                      file=sys.stderr)
                failed = True
            continue
        updated = pattern.sub(lambda m: m.group(1) + version + m.group(3), updated, count=1)
    if not check_only and updated != text:
        with open(path, "w", encoding="utf-8") as f:
            f.write(updated)
        print(f"==> {path}: version -> {version}")
    elif not check_only:
        print(f"==> {path}: already {version}")

sys.exit(1 if failed else 0)
PY

if [ "$CHECK_ONLY" = "1" ]; then
	echo "==> bundle versions match $VERSION"
fi
