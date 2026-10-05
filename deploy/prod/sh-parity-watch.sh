#!/bin/bash
# Weekly (set 2026-09-26): parity drift between hosted Simple Host and Simple Host Enterprise.
# Fetches each repo's live branch read-only, compares PARITY.md with explicit Host-only storage differences allowed and
# counts rows with status `gap`. Signals the owner if the copies differ or the gap count grew
# since the last run. First run records the baseline silently. State in /var/lib/sh-parity-watch.
set -u
ROOT=$(cd "$(dirname "$0")/../.." && pwd)
COMPARE=${SH_PARITY_COMPARE:-$ROOT/scripts/parity_compare.py}
CHECK_ONLY=${SH_PARITY_CHECK_ONLY:-0}
ST=${SH_PARITY_STATE:-/var/lib/sh-parity-watch}
if [ "$CHECK_ONLY" = 1 ] && [ -z "${SH_PARITY_STATE:-}" ]; then
  ST=$(mktemp -d); trap 'rm -rf "$ST"' EXIT
  cp /var/lib/sh-parity-watch/gaps.last "$ST/gaps.last" 2>/dev/null || true
fi
mkdir -p "$ST/repos"
REPOS="hosted|https://github.com/vineetu/simple-host.git|main
enterprise|https://github.com/vineetu/simple-host-enterprise.git|main"

send() {
  if [ "$CHECK_ONLY" = 1 ]; then echo "check failed: $1"; return; fi
  curl -s -m 20 -X POST http://127.0.0.1:8086/api/v1/rpc -H 'Content-Type: application/json' -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"send\",\"params\":{\"account\":\"${SIGNAL_ACCOUNT:?set SIGNAL_ACCOUNT}\",\"recipient\":[\"${SIGNAL_RECIPIENT:?set SIGNAL_RECIPIENT}\"],\"message\":$(python3 -c 'import json,sys;print(json.dumps(sys.argv[1]))' "$1")}}" >/dev/null
}

fail=""
if [ -n "${SH_PARITY_INPUT_DIR:-}" ]; then
  cp "$SH_PARITY_INPUT_DIR/hosted.PARITY.md" "$ST/hosted.PARITY.md"
  cp "$SH_PARITY_INPUT_DIR/enterprise.PARITY.md" "$ST/enterprise.PARITY.md"
else
while IFS='|' read -r name url branch; do
  g="$ST/repos/$name.git"
  [ -d "$g" ] || git init -q --bare "$g"
  # the hosted live branch may later merge into main: fall back to main if it is gone
  if ! git -C "$g" fetch -q --depth 1 "$url" "$branch" 2>/dev/null; then
    [ "$branch" != main ] && git -C "$g" fetch -q --depth 1 "$url" main 2>/dev/null || { fail="$fail $name(fetch)"; continue; }
  fi
  git -C "$g" show FETCH_HEAD:PARITY.md > "$ST/$name.PARITY.md" 2>/dev/null || { fail="$fail $name(no PARITY.md)"; rm -f "$ST/$name.PARITY.md"; }
done <<< "$REPOS"
fi

if [ -n "$fail" ]; then
  echo "$(date -u +%FT%TZ) error:$fail" >> "$ST/history"
  # message once per distinct failure, not every week
  [ "$(cat "$ST/last-error" 2>/dev/null)" = "$fail" ] || { send "Simple Host parity check could not read:$fail. Nothing compared this week."; echo "$fail" > "$ST/last-error"; }
  exit 1
fi
rm -f "$ST/last-error"

gaps() { grep -E '^\|' "$1" | grep -F '`gap' | awk -F'|' '{gsub(/^ +| +$/,"",$2); print $2}'; }
H="$ST/hosted.PARITY.md"; E="$ST/enterprise.PARITY.md"
same=yes; python3 "$COMPARE" "$H" "$E" || same=no
gaps "$H" | sort > "$ST/gaps.now"
N=$(wc -l < "$ST/gaps.now")
echo "$(date -u +%FT%TZ) identical=$same gaps=$N" >> "$ST/history"

if [ ! -e "$ST/gaps.last" ]; then
  mv "$ST/gaps.now" "$ST/gaps.last"; echo "baseline: equivalent=$same gaps=$N"; [ "$same" = yes ]; exit $?
fi
P=$(wc -l < "$ST/gaps.last")
msg=""
[ "$same" = no ] && msg="PARITY.md differs between hosted and enterprise; copy the newer one across."
if [ "$N" -gt "$P" ]; then
  new=$(comm -13 "$ST/gaps.last" "$ST/gaps.now" | paste -sd ';' | sed 's/;/; /g')
  msg="${msg:+$msg }Parity gaps grew $P -> $N${new:+ (new: $new)}."
fi
mv "$ST/gaps.now" "$ST/gaps.last"
if [ -n "$msg" ]; then send "Simple Host parity: $msg"; echo "sent: $msg"; else echo "ok: equivalent=$same gaps=$N (was $P)"; fi

[ -z "$msg" ]
