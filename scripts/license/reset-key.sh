#!/usr/bin/env bash
# reset-key.sh — clear a license's device binding via the admin API so it can
# be activated on a different device.
#
# Usage:
#   reset-key.sh <id>
#
# Env:
#   LICENSE_HOST         base URL of the license server (default: http://194.87.104.170)
#   LICENSE_ADMIN_TOKEN  bearer token for /v1/admin/* (required)
set -euo pipefail

usage() {
  echo "usage: $(basename "$0") <id>" >&2
  exit 2
}

if [ $# -ne 1 ]; then
  usage
fi
id="$1"

: "${LICENSE_HOST:=http://194.87.104.170}"
if [ -z "${LICENSE_ADMIN_TOKEN:-}" ]; then
  echo "error: LICENSE_ADMIN_TOKEN is not set" >&2
  exit 1
fi

body=$(python3 - "$id" <<'PY'
import json, sys
print(json.dumps({"id": sys.argv[1]}))
PY
)

curl -sS -f -X POST "$LICENSE_HOST/v1/admin/reset" \
  -H "Authorization: Bearer $LICENSE_ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d "$body"
echo
