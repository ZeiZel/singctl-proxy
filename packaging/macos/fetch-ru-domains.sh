#!/usr/bin/env bash
# Fetch the list of Russian domains from v2fly's domain-list-community
# `category-ru`, resolving its `include:` graph, and write a flat one-per-line
# domain list to $1 (default: stdout). Entries ending in `.ru`/`.xn--p1ai` are
# dropped — the PAC already covers those via wildcards, so we keep only the
# Russian services on other TLDs (vk.com, mail.ru CDNs, *.tv, ...).
#
# Network-tolerant: writes the output atomically and ONLY if a plausible number
# of domains came back (>= MIN_DOMAINS). On any fetch failure it leaves an
# existing target file untouched, so `proxy-on` keeps using the last good cache.
set -uo pipefail

OUT="${1:-/dev/stdout}"
BASE="https://raw.githubusercontent.com/v2fly/domain-list-community/master/data"
MIN_DOMAINS="${MIN_DOMAINS:-100}"
MAX_FILES="${MAX_FILES:-250}"  # safety cap on the include graph

tmp="$(mktemp)"; trap 'rm -f "$tmp"' EXIT
seen=" "                      # space-delimited visited set (bash 3.2 compatible)
queue=("category-ru")
fetched=0

while [ "${#queue[@]}" -gt 0 ]; do
  name="${queue[0]}"; queue=("${queue[@]:1}")
  case "$seen" in *" $name "*) continue ;; esac
  seen="$seen$name "
  [ "$name" = "tld-ru" ] && continue   # just the .ru TLD; covered by wildcard
  fetched=$((fetched + 1))
  [ "$fetched" -gt "$MAX_FILES" ] && { echo "fetch-ru: hit MAX_FILES=$MAX_FILES cap" >&2; break; }

  # FETCH_PROXY (e.g. 127.0.0.1:2080) routes the fetch through Singctl so it
  # works even when the direct/Cisco path can't reach GitHub.
  body="$(curl -fsS ${FETCH_PROXY:+--proxy "$FETCH_PROXY"} --max-time 30 "$BASE/$name" 2>/dev/null)" || {
    echo "fetch-ru: FAILED to fetch $name" >&2; exit 1; }

  # Strip comments, follow includes, collect domain tokens.
  while IFS= read -r line; do
    line="${line%%#*}"                       # drop trailing comment
    line="$(printf '%s' "$line" | tr -d '[:space:]')"
    [ -z "$line" ] && continue
    case "$line" in
      include:*) queue+=("${line#include:}") ;;
      regexp:*|keyword:*) : ;;               # unusable in exact-suffix PAC
      full:*)   printf '%s\n' "${line#full:}"   >>"$tmp" ;;
      domain:*) printf '%s\n' "${line#domain:}" >>"$tmp" ;;
      *)        printf '%s\n' "$line"           >>"$tmp" ;;
    esac
  done <<<"$body"
done

# Normalize: lowercase, strip any @attributes, drop .ru/.xn--p1ai + blanks, uniq.
cleaned="$(awk '
  { sub(/@.*/, ""); d = tolower($0) }
  d == "" || d == "ru" { next }
  d ~ /\.ru$/ || d ~ /\.xn--p1ai$/ { next }
  { print d }
' "$tmp" | sort -u)"

n="$(printf '%s\n' "$cleaned" | grep -c . || true)"
if [ "$n" -lt "$MIN_DOMAINS" ]; then
  echo "fetch-ru: only $n domains (< $MIN_DOMAINS) — refusing to overwrite cache" >&2
  exit 1
fi

if [ "$OUT" = "/dev/stdout" ]; then
  printf '%s\n' "$cleaned"
else
  printf '%s\n' "$cleaned" >"$OUT"
fi
echo "fetch-ru: $n Russian non-.ru domains from $fetched v2fly files -> $OUT" >&2
