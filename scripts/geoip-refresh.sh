#!/usr/bin/env bash
# Download this month's DB-IP Lite databases (City + ASN) and swap them into
# GEOIP_DIR atomically. The running server notices the new files within a
# minute and reopens them; no restart needed.
#
# Data: DB-IP Lite, CC BY 4.0 — "IP Geolocation by DB-IP" <https://db-ip.com>.
# Free, no account, published monthly as dated files.
#
# Safety: each download is decompressed next to its destination, checked with
# `simple-host geoip-verify` (it must open and answer 8.8.8.8), and only then
# renamed over the live file. A failed or truncated download never goes live;
# the previous month's file keeps serving.
#
#   GEOIP_DIR=/srv/simple-host/geoip bash scripts/geoip-refresh.sh
#
# Installed as deploy/prod/simple-host-geoip-refresh.{service,timer} (monthly).
set -euo pipefail

GEOIP_DIR="${GEOIP_DIR:-/srv/simple-host/geoip}"
SIMPLE_HOST_BIN="${SIMPLE_HOST_BIN:-/usr/local/bin/simple-host}"
BASE_URL="${GEOIP_BASE_URL:-https://download.db-ip.com/free}"

mkdir -p "$GEOIP_DIR"
work=$(mktemp -d "$GEOIP_DIR/.refresh.XXXXXX") # same filesystem, so mv is atomic
trap 'rm -rf "$work"' EXIT

# DB-IP publishes on the 1st; early in a month the new file may not be up yet,
# so fall back to last month's.
months=("$(date -u +%Y-%m)" "$(date -u -d "$(date -u +%Y-%m-01) -1 month" +%Y-%m)")

status=0
for kind in city asn; do
  dest="$GEOIP_DIR/dbip-$kind-lite.mmdb"
  got=""
  for m in "${months[@]}"; do
    url="$BASE_URL/dbip-$kind-lite-$m.mmdb.gz"
    if curl -fsS --retry 3 --max-time 600 -o "$work/$kind.mmdb.gz" "$url"; then
      got="$m"
      break
    fi
    echo "geoip-refresh: $url not available" >&2
  done
  if [ -z "$got" ]; then
    echo "geoip-refresh: FAILED to download $kind; keeping the current file" >&2
    status=1
    continue
  fi

  if ! gunzip -c "$work/$kind.mmdb.gz" > "$work/$kind.mmdb"; then
    echo "geoip-refresh: $kind download is not valid gzip; keeping the current file" >&2
    status=1
    continue
  fi
  if ! "$SIMPLE_HOST_BIN" geoip-verify "$work/$kind.mmdb"; then
    echo "geoip-refresh: $kind ($got) failed verification; keeping the current file" >&2
    status=1
    continue
  fi
  if [ -f "$dest" ] && cmp -s "$work/$kind.mmdb" "$dest"; then
    echo "geoip-refresh: $kind already current ($got)"
    continue
  fi

  chmod 0644 "$work/$kind.mmdb"
  mv -f "$work/$kind.mmdb" "$dest"
  echo "geoip-refresh: installed $dest ($got, $(du -h "$dest" | cut -f1))"
done
exit "$status"
