#!/usr/bin/env bash
# gen-keys.sh — issue N pre-generated, unassigned licenses via the admin API
# and print their tokens (one per line, stdout).
#
# Usage:
#   gen-keys.sh N [--days D] [--subject S]
#
# Env:
#   LICENSE_HOST         base URL of the license server (default: http://194.87.104.170)
#   LICENSE_ADMIN_TOKEN  bearer token for /v1/admin/* (required)
set -euo pipefail

usage() {
  echo "usage: $(basename "$0") N [--days D] [--subject S]" >&2
  exit 2
}

if [ $# -lt 1 ]; then
  usage
fi

count="$1"
shift
case "$count" in
  ''|*[!0-9]*) echo "error: N must be a positive integer, got '$count'" >&2; exit 2 ;;
esac
if [ "$count" -lt 1 ]; then
  echo "error: N must be >= 1" >&2
  exit 2
fi

days=""
subject=""
while [ $# -gt 0 ]; do
  case "$1" in
    --days)
      days="$2"; shift 2 ;;
    --subject)
      subject="$2"; shift 2 ;;
    -h|--help)
      usage ;;
    *)
      echo "error: unknown argument '$1'" >&2
      usage ;;
  esac
done

: "${LICENSE_HOST:=http://194.87.104.170}"
if [ -z "${LICENSE_ADMIN_TOKEN:-}" ]; then
  echo "error: LICENSE_ADMIN_TOKEN is not set" >&2
  exit 1
fi

body=$(python3 - "$count" "$days" "$subject" <<'PY'
import json, sys
count, days, subject = sys.argv[1], sys.argv[2], sys.argv[3]
req = {"count": int(count)}
if days:
    req["days"] = int(days)
if subject:
    req["subject"] = subject
print(json.dumps(req))
PY
)

resp=$(curl -sS -f -X POST "$LICENSE_HOST/v1/admin/issue" \
  -H "Authorization: Bearer $LICENSE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$body")

# NOTE: `python3 -c` (not `python3 -`) so the piped JSON lands on stdin —
# `python3 -` would read the *script itself* from stdin, starving the pipe.
echo "$resp" | python3 -c '
import json, sys
data = json.load(sys.stdin)
if "licenses" in data:
    for lic in data["licenses"]:
        print(lic["token"])
else:
    print(data["token"])
'
