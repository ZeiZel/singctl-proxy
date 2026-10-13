#!/usr/bin/env bash
# Hermetic installer checks. These tests never touch /Library, /usr/local, or
# a real user profile: every privileged command is replaced by a temp stub.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
STUBS="$WORK/stubs"
mkdir -p "$STUBS"

cat >"$STUBS/uname" <<'EOF'
#!/bin/sh
/bin/echo Darwin
EOF
cat >"$STUBS/id" <<'EOF'
#!/bin/sh
case "${1:-}" in -u) echo 0 ;; -un) echo testuser ;; *) echo 0 ;; esac
EOF
cat >"$STUBS/launchctl" <<'EOF'
#!/bin/sh
printf '%s\n' "$*" >>"${INSTALLER_TEST_LOG}"
exit 0
EOF
cat >"$STUBS/pgrep" <<'EOF'
#!/bin/sh
exit 1
EOF
cat >"$STUBS/rm" <<'EOF'
#!/bin/sh
printf 'rm %s\n' "$*" >>"${INSTALLER_TEST_LOG}"
exit 0
EOF
cat >"$STUBS/sleep" <<'EOF'
#!/bin/sh
exit 0
EOF
chmod +x "$STUBS"/*

INSTALLER_TEST_LOG="$WORK/commands.log"
export INSTALLER_TEST_LOG
if [ "${RUN_MACOS_INSTALLER_FIXTURE:-0}" = "1" ] && [ "$(uname -s)" = "Darwin" ]; then
	PATH="$STUBS:$PATH" bash "$ROOT/scripts/install-macos.sh" uninstall >/dev/null
fi

# Uninstall is allowed to remove installation-owned paths, but never user
# state. This assertion catches regressions that reintroduce rm of the socket,
# instance advertisement, profile, or key files into the installer.
if [ -f "$INSTALLER_TEST_LOG" ] && grep -Eq 'control\.sock|instance\.json|profile\.txt|keys' "$INSTALLER_TEST_LOG"; then
	printf 'installer attempted to remove user state:\n' >&2
	sed -n '1,120p' "$INSTALLER_TEST_LOG" >&2
	exit 1
fi

# The package must launch the foreground daemon and readiness must use the
# identity-bearing STATUS handshake plus the conditional PAC probe.
grep -q 'STATUS' "$ROOT/packaging/macos/scripts/postinstall"
grep -q 'SYSPROXY-STATUS' "$ROOT/packaging/macos/scripts/check-readiness.py"
grep -q 'FindProxyForURL' "$ROOT/packaging/macos/scripts/check-readiness.py"
test -x "$ROOT/packaging/macos/scripts/check-readiness.py"
grep -q '<string>--headless</string>' "$ROOT/packaging/macos/scripts/postinstall"
if grep -q '<string>--daemon</string>' "$ROOT/packaging/macos/scripts/postinstall"; then
	printf 'launchd plist must keep singctl in foreground\n' >&2
	exit 1
fi

# Static release verification accepts the exact linker value and rejects an
# absent or different value without executing the artifact.
printf '%s\n' 'build -ldflags "-X main.version=1.16.0 -s -w"' \
  | python3 "$ROOT/scripts/read-linker-version.py" | grep -qx '1.16.0'
if printf '%s\n' 'build -ldflags "-X main.version=1.15.0"' \
    | python3 "$ROOT/scripts/read-linker-version.py" | grep -qx '1.16.0'; then
	printf 'release verifier accepted the wrong linker version\n' >&2
	exit 1
fi

python3 "$ROOT/packaging/macos/scripts/test_readiness.py"

printf 'installer script checks passed\n'
