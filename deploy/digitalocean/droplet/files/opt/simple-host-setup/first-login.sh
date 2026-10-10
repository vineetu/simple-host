#!/usr/bin/env bash
# Simple Host 1-Click: first-login setup.
#
# Hooked from /root/.bashrc by 001_onboot, so it runs the first time someone
# logs in as root. It asks for the dashboard address and the sites address,
# checks that DNS points at this droplet, then runs the Simple Host installer
# (deploy/install/install.sh), fetched by commit and checked against its
# sha256 exactly as https://simple-host.app/setup does.
#
# The installer runs in its own session (setsid, HUP ignored), so a dropped
# SSH connection does not stop it; this session only follows its log. When it
# succeeds, the detached run writes $DONE_FILE and removes the hook from
# .bashrc. Stopped or failed, the hook stays and setup runs again at the next
# login.
#
# Nothing instance-specific is in the image: the admin key, the database
# password and the certificates are all made by the installer, here.
set -uo pipefail

# The installer this image pins. Written at image build time from the Packer
# variables (template.json); the defaults below are the same release.
PINS_FILE=${PINS_FILE:-/opt/simple-host-setup/installer.env}
INSTALLER_RELEASE=v0.7.17
INSTALLER_COMMIT=1870fe6df49a3c78080e247defda9bff8672c815
INSTALLER_SHA256=501f3c5544b3f51592aa1b636c113cd40ad118ed7dae4b77eb2d4569f275d6b4
if [ -r "$PINS_FILE" ]; then
  # shellcheck source=/dev/null
  . "$PINS_FILE"
fi
INSTALLER_URL=${INSTALLER_URL:-https://raw.githubusercontent.com/vineetu/simple-host/$INSTALLER_COMMIT/deploy/install/install.sh}

# Times and limits. Each can be set in the environment.
DNS_WAIT_SECONDS=${DNS_WAIT_SECONDS:-300}        # how long "check again" keeps trying
DNS_POLL_SECONDS=${DNS_POLL_SECONDS:-10}         # pause between DNS checks while waiting
DNS_RESOLVER=${DNS_RESOLVER:-1.1.1.1}            # asked directly, so a stale local cache does not mislead
DNS_QUERY_TIMEOUT_SECONDS=${DNS_QUERY_TIMEOUT_SECONDS:-3}
METADATA_BASE=${METADATA_BASE:-http://169.254.169.254/metadata/v1}
METADATA_TIMEOUT_SECONDS=${METADATA_TIMEOUT_SECONDS:-3}
DOWNLOAD_TIMEOUT_SECONDS=${DOWNLOAD_TIMEOUT_SECONDS:-60}
DOWNLOAD_RETRIES=${DOWNLOAD_RETRIES:-3}
HOST_MAX_LENGTH=${HOST_MAX_LENGTH:-253}

# Where things live. Overridable for tests.
BASHRC=${BASHRC:-/root/.bashrc}
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-host}
DONE_FILE=${DONE_FILE:-/opt/simple-host-setup/.done}
LOCK_FILE=${LOCK_FILE:-/run/simple-host-setup.lock}
LOG_DIR=${LOG_DIR:-/root}
SELF=${SELF:-/opt/simple-host-setup/first-login.sh}
HOOK_BEGIN='# >>> simple-host first-login >>>'
HOOK_END='# <<< simple-host first-login <<<'

remove_hook() {
  [ -f "$BASHRC" ] || return 0
  sed -i "\|^${HOOK_BEGIN}\$|,\|^${HOOK_END}\$|d" "$BASHRC"
}

# --apply INSTALLER LOG HOST CONTENT: the detached run. It outlives the SSH
# session, so everything that must happen after a successful install is here.
if [ "${1:-}" = "--apply" ]; then
  f=$2; log=$3; host=$4; content=$5
  trap '' HUP INT
  trap 'rm -f "$f"' EXIT
  bash "$f" --host "$host" --content "$content" >>"$log" 2>&1 </dev/null
  status=$?
  if [ "$status" -eq 0 ]; then
    # The key lives in $INSTALL_DIR/.env; it is not copied anywhere else.
    sed -i '/admin_api_key/d' "$log"
    printf 'host=%s\ncontent=%s\nrelease=%s\n' "$host" "$content" "$INSTALLER_RELEASE" > "$DONE_FILE"
    remove_hook
  fi
  exit "$status"
fi

if [ -t 1 ]; then B=$'\e[1m'; Y=$'\e[33m'; G=$'\e[32m'; R=$'\e[31m'; N=$'\e[0m'; else B=; Y=; G=; R=; N=; fi

TMPF=""
trap 'rm -f "$TMPF"' EXIT
stopped() {
  echo
  echo "Setup stopped. It runs again at your next login, or now with: $SELF"
  exit 130
}
trap stopped INT

if [ "$(id -u)" -ne 0 ]; then
  echo "Run setup as root: sudo $SELF"
  exit 1
fi

# One setup at a time. The lock is inherited by the detached install, so it
# is held until the install ends even if this session goes away. The file
# holds the current log's path for the session that finds it taken.
exec 9>>"$LOCK_FILE"
if ! flock -n 9; then
  echo "Setup is already running in another session."
  running_log=$(tail -n 1 "$LOCK_FILE" 2>/dev/null)
  if [ -n "$running_log" ]; then echo "Follow it with: tail -f $running_log"; fi
  exit 0
fi

# Set up already (the marker is written only after the installer succeeded).
if [ -f "$DONE_FILE" ]; then
  done_host=$(sed -n 's/^host=//p' "$DONE_FILE")
  echo "Simple Host is already set up: https://$done_host/admin"
  echo "Admin key: grep ^ADMIN_API_KEY= $INSTALL_DIR/.env"
  remove_hook
  exit 0
fi

# ask PROMPT DEFAULT -> REPLY. Stops cleanly when input ends.
ask() {
  local prompt=$1 default=${2:-}
  if [ -n "$default" ]; then prompt="$prompt [$default]"; fi
  printf '%s: ' "$prompt"
  if ! read -r REPLY; then
    echo
    echo "No input. Run $SELF when you are ready."
    exit 1
  fi
  REPLY=$(printf '%s' "$REPLY" | tr -d '[:space:]')
  if [ -z "$REPLY" ]; then REPLY=$default; fi
}

# Lower-case, drop a scheme, a path and a trailing dot.
clean_host() {
  local h
  h=$(printf '%s' "$1" | tr '[:upper:]' '[:lower:]')
  h=${h#http://}; h=${h#https://}; h=${h%%/*}; h=${h%.}
  printf '%s' "$h"
}

valid_host() {
  local h=$1
  [ "${#h}" -le "$HOST_MAX_LENGTH" ] || return 1
  printf '%s' "$h" | grep -Eq '^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]([a-z0-9-]{0,61}[a-z0-9])?$'
}

is_v4() { printf '%s' "$1" | grep -Eq '^[0-9]{1,3}(\.[0-9]{1,3}){3}$'; }
is_public_v4() {
  is_v4 "$1" || return 1
  case "$1" in
    10.*|127.*|169.254.*|192.168.*|0.*) return 1 ;;
    172.1[6-9].*|172.2[0-9].*|172.3[01].*) return 1 ;;
    100.6[4-9].*|100.[7-9][0-9].*|100.1[01][0-9].*|100.12[0-7].*) return 1 ;;
  esac
  return 0
}
md() { curl -fsS --max-time "$METADATA_TIMEOUT_SECONDS" "$METADATA_BASE/$1" 2>/dev/null || true; }

# IP: the address to point DNS at (the reserved IP when one is assigned,
# else the droplet's own). IPS: every address DNS may point at. IP6: the
# droplet's IPv6, if it has one. PUBLIC_IP overrides the lookup.
detect_ips() {
  local pub="" res=""
  IP6=""
  if [ -n "${PUBLIC_IP:-}" ]; then
    pub=$PUBLIC_IP
  else
    pub=$(md interfaces/public/0/ipv4/address)
    is_v4 "$pub" || pub=""
    if [ "$(md reserved_ip/ipv4/active)" = "true" ]; then
      res=$(md reserved_ip/ipv4/ip_address)
      is_v4 "$res" || res=""
    fi
    IP6=$(md interfaces/public/0/ipv6/address | tr '[:upper:]' '[:lower:]')
    if [ -z "$pub" ]; then
      for a in $(hostname -I 2>/dev/null); do
        if is_public_v4 "$a"; then pub=$a; break; fi
      done
    fi
  fi
  IP=${res:-$pub}
  IPS=$(printf '%s %s' "$res" "$pub" | xargs)
}

# The addresses a name resolves to, one per line (CNAMEs followed). TYPE is
# A or AAAA; without dig, only A through getent.
resolve() {
  local type=$1 name=$2
  if command -v dig >/dev/null 2>&1; then
    dig +short +time="$DNS_QUERY_TIMEOUT_SECONDS" +tries=1 "$type" "$name" "@$DNS_RESOLVER" 2>/dev/null \
      | if [ "$type" = A ]; then grep -E '^[0-9]+(\.[0-9]+){3}$'; else grep -E '^[0-9a-fA-F:]+:[0-9a-fA-F:]*$'; fi \
      | tr '[:upper:]' '[:lower:]' | sort -u || true
  elif [ "$type" = A ]; then
    getent ahostsv4 "$name" 2>/dev/null | awk '{print $1}' | sort -u || true
  fi
}

# Prints a line per name; returns 0 when every name resolves, and only to
# this droplet's addresses. An AAAA record elsewhere is a warning, not a stop.
dns_ok() {
  local ok=0 name got a bad v6 w
  for name in "$@"; do
    got=$(resolve A "$name")
    bad=""
    for a in $got; do
      case " $IPS " in *" $a "*) ;; *) bad="$bad $a" ;; esac
    done
    if [ -z "$got" ]; then
      echo "  ${Y}missing${N}  $name does not resolve yet"; ok=1
    elif [ -n "$bad" ]; then
      echo "  ${Y}wrong${N}    $name -> $(echo "$got" | xargs) (should be $IP)"; ok=1
    else
      echo "  ${G}ok${N}       $name -> $(echo "$got" | xargs)"
    fi
    v6=$(resolve AAAA "$name")
    for w in $v6; do
      if [ "$w" != "$IP6" ]; then
        echo "  ${Y}warning${N}  $name has an IPv6 (AAAA) record $w that is not this droplet. Let's Encrypt tries IPv6 first: remove it${IP6:+ or point it at $IP6}."
      fi
    done
  done
  return $ok
}

# ---------------------------------------------------------------------------

detect_ips
cat <<EOF

${B}Simple Host setup${N}

This droplet's public IPv4 address is ${B}${IP:-unknown}${N}.
You need a domain whose DNS you can change. Press Ctrl+C to stop; this runs
again at your next login.

EOF

while :; do
  while :; do
    ask "Address for the dashboard. Use a name like hosting.example.com" ""
    HOST=$(clean_host "$REPLY")
    if valid_host "$HOST"; then break; fi
    echo "  That is not a hostname. Use a name like hosting.example.com."
  done

  while :; do
    ask "Address the sites are served from" "sites.$HOST"
    CONTENT=$(clean_host "$REPLY")
    if ! valid_host "$CONTENT"; then
      echo "  That is not a hostname. Use a name like sites.hosting.example.com."
    elif [ "$CONTENT" = "$HOST" ]; then
      echo "  It has to differ from the dashboard address: sites must not share its origin."
    else
      break
    fi
  done

  # The records to add: the address, a wildcard under it (which covers the
  # sites address when it sits there), and the sites address when it does not.
  RECORDS=("$HOST" "*.$HOST")
  case "$CONTENT" in
    *."$HOST") ;;
    *) RECORDS+=("$CONTENT") ;;
  esac

  IP_OR_TEXT=${IP:-"this droplet's public IPv4 address"}
  echo
  echo "At your DNS provider, add A records for ${B}${RECORDS[*]}${N} pointing at ${B}${IP_OR_TEXT}${N}."
  echo "If the provider proxies traffic (Cloudflare's orange cloud), set these to DNS only."
  echo
  if [ -z "$IP" ]; then
    echo "This droplet's public IPv4 address is unknown, so DNS is not checked."
  else
    echo "Checking DNS (via $DNS_RESOLVER):"
    if ! dns_ok "$HOST" "$CONTENT"; then
      while :; do
        echo
        ask "r = check again for up to $(( (DNS_WAIT_SECONDS + 59) / 60 )) min, c = continue anyway, q = quit" "r"
        case "$REPLY" in
          r|R)
            waited=0; resolved=1
            while [ "$waited" -lt "$DNS_WAIT_SECONDS" ]; do
              if dns_ok "$HOST" "$CONTENT" >/dev/null; then resolved=0; break; fi
              sleep "$DNS_POLL_SECONDS"; waited=$((waited + DNS_POLL_SECONDS))
            done
            dns_ok "$HOST" "$CONTENT"
            if [ "$resolved" -eq 0 ]; then break; fi
            echo "  Not yet. DNS changes can take a while to show."
            ;;
          c|C)
            echo "  Continuing. HTTPS certificates are issued once DNS points here."
            break ;;
          q|Q)
            echo "Stopped. Run $SELF when DNS is ready, or log in again."
            exit 1 ;;
          *) echo "  Type r, c or q." ;;
        esac
      done
    fi
  fi

  echo
  echo "Dashboard:  https://$HOST"
  echo "Sites:      https://$CONTENT"
  ask "Install with these settings? (y/n)" "y"
  case "$REPLY" in
    y|Y|yes|YES) break ;;
    *) echo; echo "Starting over." ;;
  esac
done

echo
echo "Fetching the installer ($INSTALLER_RELEASE, commit ${INSTALLER_COMMIT:0:12})"
TMPF=$(mktemp)
if ! curl -fsSL --retry "$DOWNLOAD_RETRIES" --max-time "$DOWNLOAD_TIMEOUT_SECONDS" "$INSTALLER_URL" -o "$TMPF"; then
  echo "${R}Could not download the installer.${N} Check the droplet's network and run $SELF again."
  exit 1
fi
if ! printf '%s  %s\n' "$INSTALLER_SHA256" "$TMPF" | sha256sum -c --quiet - ; then
  echo "${R}The installer does not match its pinned sha256, so it was not run.${N}"
  echo "Run $SELF again; if it repeats, write to support@simple-host.app."
  exit 1
fi

# A fresh log per run, root-only: the installer prints the admin key, and
# the detached run removes that line once the install succeeds.
LOG="$LOG_DIR/simple-host-install-$(date +%Y%m%d-%H%M%S).log"
install -m 600 /dev/null "$LOG"
: > "$LOCK_FILE"; printf '%s\n' "$LOG" >&9

# Opened before the install starts: the detached run rewrites the log (without
# the key line) when it ends, and this keeps the version shown here whole.
exec 8<"$LOG"
nohup setsid -w bash "$(readlink -f "$0")" --apply "$TMPF" "$LOG" "$HOST" "$CONTENT" </dev/null >/dev/null 2>&1 8<&- &
pid=$!
TMPF=""   # the detached run owns and removes it now
trap 'echo; echo "The install goes on in the background. Follow it with: tail -f $LOG"; exit 130' INT
echo "Installing. If this connection drops, the install goes on; its log is $LOG"
echo
tail -n +1 -f --pid="$pid" /dev/fd/8
wait "$pid"; status=$?
trap - INT

if [ "$status" -ne 0 ]; then
  echo
  echo "${R}The install did not finish.${N} The output is in $LOG."
  echo "Run $SELF to try again (it is safe to re-run). Help: https://simple-host.app/setup?product=small-box#help"
  exit 1
fi

cat <<EOF

${G}${B}Simple Host is running.${N}

Dashboard:  ${B}https://$HOST/admin${N}
Sign in with the admin key printed above.

To see the admin key again:  grep ^ADMIN_API_KEY= $INSTALL_DIR/.env
To upgrade later:            /opt/simple-host-setup/upgrade.sh
This setup will not run again at login.

EOF
