#!/usr/bin/env bash
# list-keys.sh — list licenses via the admin API, optionally filtered by
# activation state, and pretty-print them.
#
# Usage:
#   list-keys.sh [--unactivated|--activated] [--json]
#
# Env:
#   LICENSE_HOST         base URL of the license server (default: http://194.87.104.170)
#   LICENSE_ADMIN_TOKEN  bearer token for /v1/admin/* (required)
set -euo pipefail

usage() {
  echo "usage: $(basename "$0") [--unactivated|--activated] [--json]" >&2
  exit 2
}

state=""
as_json=0
while [ $# -gt 0 ]; do
  case "$1" in
    --activated)
      state="activated"; shift ;;
    --unactivated)
      state="unactivated"; shift ;;
    --json)
      as_json=1; shift ;;
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

url="$LICENSE_HOST/v1/admin/licenses"
if [ -n "$state" ]; then
  url="$url?state=$state"
fi

resp=$(curl -sS -f "$url" -H "Authorization: Bearer $LICENSE_ADMIN_TOKEN")

if [ "$as_json" -eq 1 ]; then
  echo "$resp" | python3 -m json.tool
  exit 0
fi

# NOTE: `python3 -c` (not `python3 -`) so the piped JSON lands on stdin —
# `python3 -` would read the *script itself* from stdin, starving the pipe.
echo "$resp" | python3 -c '
import json, sys

records = json.load(sys.stdin)
cols = ["ID", "SUBJECT", "EMAIL", "DEVICE", "STATUS", "CREATED"]
rows = []
for r in records:
    claims = r.get("claims", {})
    device = r.get("device_id", "")
    if len(device) > 12:
        device = device[:12] + "…"
    rows.append([
        claims.get("id", ""),
        claims.get("sub", ""),
        r.get("email", ""),
        device,
        r.get("status", ""),
        str(r.get("created_at", "")),
    ])

widths = [len(c) for c in cols]
for row in rows:
    for i, cell in enumerate(row):
        widths[i] = max(widths[i], len(cell))

def fmt(row):
    return "  ".join(cell.ljust(widths[i]) for i, cell in enumerate(row))

print(fmt(cols))
for row in rows:
    print(fmt(row))
'
