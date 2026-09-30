#!/usr/bin/env bash
# Upgrades Simple Host on this droplet, keeping its addresses.
#
#   /opt/simple-host-setup/upgrade.sh                      the release https://simple-host.app/setup pins
#   /opt/simple-host-setup/upgrade.sh --commit C --sha256 S   a given installer commit and its sha256
#
# The installer is also the upgrade, but run without --host and --content it
# would rewrite /opt/simple-host/.env with both empty. This reads them from
# that file and passes them back. The installer is fetched by commit and run
# only if its sha256 matches, in its own session so a dropped SSH connection
# does not stop it.
set -uo pipefail

SETUP_JS_URL=${SETUP_JS_URL:-https://simple-host.app/setup/setup.js}
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-host}
LOG_DIR=${LOG_DIR:-/root}
DOWNLOAD_TIMEOUT_SECONDS=${DOWNLOAD_TIMEOUT_SECONDS:-60}
DOWNLOAD_RETRIES=${DOWNLOAD_RETRIES:-3}

COMMIT=""; SHA256=""
while [ $# -gt 0 ]; do
  case "$1" in
    --commit) COMMIT=${2:-}; shift 2 ;;
    --sha256) SHA256=${2:-}; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done

if [ "$(id -u)" -ne 0 ]; then echo "Run as root: sudo $0"; exit 1; fi

env_value() { sed -n "s/^$1=//p" "$INSTALL_DIR/.env" 2>/dev/null | tail -n 1; }
HOST=$(env_value SITE_DOMAIN); CONTENT=$(env_value CONTENT_HOST)
if [ -z "$HOST" ] || [ -z "$CONTENT" ]; then
  echo "SITE_DOMAIN and CONTENT_HOST are not both set in $INSTALL_DIR/.env, so there is nothing to upgrade here."
  echo "Set Simple Host up first: /opt/simple-host-setup/first-login.sh"
  exit 1
fi

if [ -z "$COMMIT" ] && [ -z "$SHA256" ]; then
  js=$(curl -fsSL --retry "$DOWNLOAD_RETRIES" --max-time "$DOWNLOAD_TIMEOUT_SECONDS" "$SETUP_JS_URL") || { echo "Could not read $SETUP_JS_URL"; exit 1; }
  COMMIT=$(printf '%s\n' "$js" | sed -n "s/^ *var INSTALLER_COMMIT = '\([0-9a-f]\{40\}\)';.*/\1/p")
  SHA256=$(printf '%s\n' "$js" | sed -n "s/^ *var INSTALLER_SHA256 = '\([0-9a-f]\{64\}\)';.*/\1/p")
  RELEASE=$(printf '%s\n' "$js" | sed -n "s/^ *var INSTALLER_RELEASE = '\([^']*\)';.*/\1/p")
  echo "simple-host.app/setup pins ${RELEASE:-an unnamed release} (commit ${COMMIT:0:12})."
fi
if ! printf '%s' "$COMMIT" | grep -Eq '^[0-9a-f]{40}$' || ! printf '%s' "$SHA256" | grep -Eq '^[0-9a-f]{64}$'; then
  echo "Need a 40-character commit and a 64-character sha256 (give both, or neither)."
  exit 2
fi
INSTALLER_URL=${INSTALLER_URL:-https://raw.githubusercontent.com/vineetu/simple-host/$COMMIT/deploy/install/install.sh}

TMPF=$(mktemp)
trap 'rm -f "$TMPF"' EXIT
if ! curl -fsSL --retry "$DOWNLOAD_RETRIES" --max-time "$DOWNLOAD_TIMEOUT_SECONDS" "$INSTALLER_URL" -o "$TMPF"; then
  echo "Could not download the installer."; exit 1
fi
if ! printf '%s  %s\n' "$SHA256" "$TMPF" | sha256sum -c --quiet - ; then
  echo "The installer does not match the sha256, so it was not run."; exit 1
fi

LOG="$LOG_DIR/simple-host-upgrade-$(date +%Y%m%d-%H%M%S).log"
install -m 600 /dev/null "$LOG"
exec 8<"$LOG"
echo "Upgrading $HOST. If this connection drops, the upgrade goes on; its log is $LOG"
echo
# shellcheck disable=SC2016
nohup setsid -w bash -c 'trap "" HUP INT; bash "$1" --host "$2" --content "$3" >>"$4" 2>&1 </dev/null; s=$?; sed -i "/admin_api_key/d" "$4"; rm -f "$1"; exit $s' \
  _ "$TMPF" "$HOST" "$CONTENT" "$LOG" </dev/null >/dev/null 2>&1 8<&- &
pid=$!
TMPF=""
trap 'echo; echo "The upgrade goes on in the background. Follow it with: tail -f $LOG"; exit 130' INT
tail -n +1 -f --pid="$pid" /dev/fd/8
wait "$pid"; status=$?
if [ "$status" -ne 0 ]; then
  echo "The upgrade did not finish. The output is in $LOG."
  exit 1
fi
echo "Upgraded. The admin key is unchanged: grep ^ADMIN_API_KEY= $INSTALL_DIR/.env"
