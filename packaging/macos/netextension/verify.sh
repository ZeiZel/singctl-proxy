#!/usr/bin/env bash
# verify.sh — read-only on-device verification battery for the singctl macOS
# transparent-proxy system extension (com.singctl.proxy.netext).
#
# Does NOT change any state (no sudo required except where noted — V2's
# root-owned config copy needs it to read, and this script does not attempt
# to elevate itself). Prints PASS/FAIL/SKIP per check plus a summary, and
# exits non-zero if any check FAILed (SKIPs do not affect the exit code).
#
# Usage:
#   ./verify.sh                      # V1, V2, V4 only
#   ./verify.sh --pid 1234           # also V3, V5 against PID 1234
#   ./verify.sh --pid 1234 --socks 127.0.0.1:1080   # override expected SOCKS addr
#
# See build.sh step 3 and RUNBOOK.md item 5 for the manual version this
# script formalizes.
set -euo pipefail

EXTENSION_BUNDLE_ID="com.singctl.proxy.netext"
APP_GROUP="group.com.singctl.proxy"
TARGET_PID=""
SOCKS_HOST="127.0.0.1"
SOCKS_PORT="1080"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --pid)
      TARGET_PID="${2:-}"
      shift 2
      ;;
    --socks)
      SOCKS_HOST="${2%%:*}"
      SOCKS_PORT="${2##*:}"
      shift 2
      ;;
    -h|--help)
      sed -n '2,20p' "$0"
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      exit 2
      ;;
  esac
done

PASS_COUNT=0
FAIL_COUNT=0
SKIP_COUNT=0

pass() { echo "PASS: $1"; PASS_COUNT=$((PASS_COUNT + 1)); }
fail() { echo "FAIL: $1"; FAIL_COUNT=$((FAIL_COUNT + 1)); }
skip() { echo "SKIP: $1"; SKIP_COUNT=$((SKIP_COUNT + 1)); }

echo "== singctl netext verify.sh =="
echo "expecting extension bundle id: $EXTENSION_BUNDLE_ID"
echo "expecting SOCKS proxy at: $SOCKS_HOST:$SOCKS_PORT"
echo

# --- V1: system extension is activated + enabled -----------------------
echo "-- V1: systemextensionsctl status --"
if ! command -v systemextensionsctl >/dev/null 2>&1; then
  skip "V1 systemextensionsctl not available (not macOS?)"
else
  SYSEXT_LIST="$(systemextensionsctl list 2>&1 || true)"
  if echo "$SYSEXT_LIST" | grep -q "$EXTENSION_BUNDLE_ID"; then
    LINE="$(echo "$SYSEXT_LIST" | grep "$EXTENSION_BUNDLE_ID")"
    if echo "$LINE" | grep -q "activated enabled"; then
      pass "V1 $EXTENSION_BUNDLE_ID is activated enabled"
    else
      fail "V1 $EXTENSION_BUNDLE_ID present but not 'activated enabled': $LINE"
    fi
  else
    fail "V1 $EXTENSION_BUNDLE_ID not found in systemextensionsctl list"
  fi
fi
echo

# --- V2: shared config.json (App Group container) -----------------------
echo "-- V2: shared config.json --"
USER_CONFIG="$HOME/Library/Group Containers/$APP_GROUP/config.json"
ROOT_CONFIG="/var/root/Library/Group Containers/$APP_GROUP/config.json"

print_config() {
  local path="$1"
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$path" <<'PYEOF' 2>/dev/null || cat "$path"
import json, sys
try:
    with open(sys.argv[1]) as f:
        data = json.load(f)
except Exception as e:
    print(f"  (unparseable JSON: {e})")
    sys.exit(0)
targets = data.get("targets", [])
host = data.get("socksHost", "?")
port = data.get("socksPort", "?")
print(f"  targets: {targets}")
print(f"  socks:   {host}:{port}")
PYEOF
  elif command -v plutil >/dev/null 2>&1; then
    plutil -p "$path" 2>/dev/null || cat "$path"
  else
    cat "$path"
  fi
}

if [[ -r "$USER_CONFIG" ]]; then
  pass "V2 user-container config.json found: $USER_CONFIG"
  print_config "$USER_CONFIG"
else
  skip "V2 user-container config.json not found/readable: $USER_CONFIG"
fi

if [[ -r "$ROOT_CONFIG" ]]; then
  pass "V2 root-container config.json found: $ROOT_CONFIG"
  print_config "$ROOT_CONFIG"
else
  skip "V2 root-container config.json not readable: $ROOT_CONFIG (needs root; the extension runs as root and writes/reads this copy — re-run as 'sudo ./verify.sh' to check it)"
fi
echo

# --- V3: (optional) confirm captured PID's TCP connections go to SOCKS --
echo "-- V3: captured process TCP destinations (requires --pid) --"
if [[ -z "$TARGET_PID" ]]; then
  skip "V3 no --pid given"
elif ! command -v lsof >/dev/null 2>&1; then
  skip "V3 lsof not available"
else
  # -a ANDs the -p and -i selectors together; without it lsof ORs them and
  # returns every network fd on the system plus every fd of this PID.
  LSOF_OUT="$(lsof -nP -a -p "$TARGET_PID" -i 2>/dev/null || true)"
  if [[ -z "$LSOF_OUT" ]]; then
    skip "V3 no open network fds for pid $TARGET_PID (process gone, no perms, or idle)"
  else
    # TCP lines look like:
    #   COMMAND PID USER FD TYPE DEVICE SIZE/OFF NODE NAME
    # NODE (field 8) is the protocol ("TCP"); NAME (field 9) is
    # "10.0.0.5:54321->1.2.3.4:443" for an actual connection, or a bare
    # "*:PORT"/"host:PORT" (no "->") for a passive LISTEN socket. Only lines
    # with "->" represent an outbound/established connection to a peer, so
    # LISTEN sockets are excluded rather than counted as "not via SOCKS".
    TCP_LINES="$(echo "$LSOF_OUT" | awk '$8 == "TCP" && $9 ~ /->/')"
    if [[ -z "$TCP_LINES" ]]; then
      skip "V3 no established/outbound TCP connections found for pid $TARGET_PID (only listeners, or none)"
    else
      BAD_LINES=""
      TOTAL=0
      OK=0
      while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        TOTAL=$((TOTAL + 1))
        NAME_FIELD="$(echo "$line" | awk '{print $9}')"
        REMOTE="${NAME_FIELD#*->}"
        # Strip IPv6 brackets ("[::1]:1994" -> "::1]:1994") before splitting
        # off the port at the last colon, then drop the trailing "]" if any.
        REMOTE_NOBRACKET="${REMOTE#\[}"
        REMOTE_PORT="${REMOTE_NOBRACKET##*:}"
        REMOTE_HOST="${REMOTE_NOBRACKET%:*}"
        REMOTE_HOST="${REMOTE_HOST%]}"
        if [[ "$REMOTE_HOST" == "$SOCKS_HOST" && "$REMOTE_PORT" == "$SOCKS_PORT" ]]; then
          OK=$((OK + 1))
        else
          BAD_LINES+="$line"$'\n'
        fi
      done <<< "$TCP_LINES"
      if [[ "$TOTAL" -gt 0 && "$OK" -eq "$TOTAL" ]]; then
        pass "V3 all $TOTAL TCP connection(s) for pid $TARGET_PID go to $SOCKS_HOST:$SOCKS_PORT"
      else
        fail "V3 $((TOTAL - OK))/$TOTAL TCP connection(s) for pid $TARGET_PID do NOT go to $SOCKS_HOST:$SOCKS_PORT:"
        echo "$BAD_LINES" | sed '/^$/d' | sed 's/^/    /'
      fi
    fi
  fi
fi
echo

# --- V4: hint for watching live capture decisions ------------------------
echo "-- V4: live capture-decision log stream (hint only, not run) --"
cat <<EOF
  To watch per-flow capture decisions (sourceAppSigningIdentifier ->
  captured/declined) in real time, run in a separate terminal:

    log stream --predicate 'subsystem == "$EXTENSION_BUNDLE_ID"' --level debug

  (Not executed here — it blocks indefinitely.)
EOF
skip "V4 informational only"
echo

# --- V5: (optional) UDP relay snapshot via nettop ------------------------
echo "-- V5: UDP relay snapshot (requires --pid) --"
if [[ -z "$TARGET_PID" ]]; then
  skip "V5 no --pid given"
elif ! command -v nettop >/dev/null 2>&1; then
  skip "V5 nettop not available"
else
  if NETTOP_OUT="$(nettop -P -p "$TARGET_PID" -m udp -l 1 2>&1)"; then
    if [[ -n "$NETTOP_OUT" ]]; then
      echo "$NETTOP_OUT" | sed 's/^/    /'
      pass "V5 captured one nettop UDP snapshot for pid $TARGET_PID"
    else
      skip "V5 nettop returned no output for pid $TARGET_PID (no UDP activity?)"
    fi
  else
    skip "V5 nettop failed (missing perms? try with sudo): $NETTOP_OUT"
  fi
fi
echo

# --- summary --------------------------------------------------------------
echo "== summary: $PASS_COUNT passed, $FAIL_COUNT failed, $SKIP_COUNT skipped =="
if [[ "$FAIL_COUNT" -gt 0 ]]; then
  exit 1
fi
exit 0
