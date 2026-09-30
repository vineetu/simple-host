#!/usr/bin/env bash
# Simple Host 1-Click: first-login setup.
#
# Hooked from /root/.bashrc by 001_onboot, so it runs the first time someone
# logs in as root. It asks for the address, the sites address and an email,
# checks that DNS points at this droplet, then runs the Simple Host installer
# (deploy/install/install.sh), fetched by commit and checked against its
# sha256 exactly as https://simple-host.app/setup does. When the installer
# succeeds it removes its own hook from .bashrc. Stopped or failed, the hook
# stays and it runs again at the next login.
#
# Nothing instance-specific is in the image: the admin key, the database
# password and the certificates are all made by the installer, here.
set -uo pipefail

# The installer this image pins. Written at image build time from the Packer
# variables (template.json); the defaults below are the same release.
PINS_FILE=${PINS_FILE:-/opt/simple-host-setup/installer.env}
INSTALLER_RELEASE=v0.7.4
INSTALLER_COMMIT=970c5afdb8b2bd105420bb5adeadabe0663da3ca
INSTALLER_SHA256=8c579ef16eac0b5116a70e5f7d733b12411d21e16ee3e84e1d8bccaa8726fb6e
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
METADATA_URL=${METADATA_URL:-http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address}
METADATA_TIMEOUT_SECONDS=${METADATA_TIMEOUT_SECONDS:-3}
DOWNLOAD_TIMEOUT_SECONDS=${DOWNLOAD_TIMEOUT_SECONDS:-60}
DOWNLOAD_RETRIES=${DOWNLOAD_RETRIES:-3}
HOST_MAX_LENGTH=${HOST_MAX_LENGTH:-253}

# Where things live. Overridable for tests.
BASHRC=${BASHRC:-/root/.bashrc}
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-host}
INSTALL_LOG=${INSTALL_LOG:-/root/simple-host-install.log}
SELF=${SELF:-/opt/simple-host-setup/first-login.sh}
HOOK_BEGIN='# >>> simple-host first-login >>>'
HOOK_END='# <<< simple-host first-login <<<'

if [ -t 1 ]; then B=$'\e[1m'; Y=$'\e[33m'; G=$'\e[32m'; R=$'\e[31m'; N=$'\e[0m'; else B=; Y=; G=; R=; N=; fi

remove_hook() {
  [ -f "$BASHRC" ] || return 0
  sed -i "\|^${HOOK_BEGIN}\$|,\|^${HOOK_END}\$|d" "$BASHRC"
}

stopped() {
  echo
  echo "Setup stopped. It runs again at your next login, or now with: $SELF"
  exit 130
}
trap stopped INT

# ask PROMPT DEFAULT -> REPLY. Stops cleanly when input ends.
ask() {
  local prompt=$1 default=${2:-}
  if [ -n "$default" ]; then prompt="$prompt [$default]"; fi
  if ! read -r -p "$prompt: " REPLY; then
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

valid_email() {
  printf '%s' "$1" | grep -Eq '^[^@[:space:]]+@[^@[:space:]]+\.[^@[:space:]]+$'
}

public_ip() {
  if [ -n "${PUBLIC_IP:-}" ]; then printf '%s' "$PUBLIC_IP"; return; fi
  local ip
  ip=$(curl -fsS --max-time "$METADATA_TIMEOUT_SECONDS" "$METADATA_URL" 2>/dev/null || true)
  if ! printf '%s' "$ip" | grep -Eq '^[0-9]+(\.[0-9]+){3}$'; then
    ip=$(hostname -I 2>/dev/null | awk '{print $1}')
  fi
  printf '%s' "$ip"
}

# The IPv4 addresses a name resolves to, one per line (CNAMEs followed).
resolve() {
  if command -v dig >/dev/null 2>&1; then
    dig +short +time="$DNS_QUERY_TIMEOUT_SECONDS" +tries=1 A "$1" "@$DNS_RESOLVER" 2>/dev/null \
      | grep -E '^[0-9]+(\.[0-9]+){3}$' || true
  else
    getent ahostsv4 "$1" 2>/dev/null | awk '{print $1}' | sort -u || true
  fi
}

# Prints a line per name; returns 0 when every name resolves to IP alone.
dns_ok() {
  local ok=0 name got
  for name in "$@"; do
    got=$(resolve "$name" | tr '\n' ' ' | sed 's/ $//')
    if [ "$got" = "$IP" ]; then
      echo "  ${G}ok${N}       $name -> $got"
    elif [ -z "$got" ]; then
      echo "  ${Y}missing${N}  $name does not resolve yet"; ok=1
    else
      echo "  ${Y}wrong${N}    $name -> $got (should be $IP)"; ok=1
    fi
  done
  return $ok
}

# Run the pinned installer. Returns its exit status.
run_installer() {
  local f status
  f=$(mktemp)
  echo "Fetching the installer ($INSTALLER_RELEASE, commit ${INSTALLER_COMMIT:0:12})"
  if ! curl -fsSL --retry "$DOWNLOAD_RETRIES" --max-time "$DOWNLOAD_TIMEOUT_SECONDS" "$INSTALLER_URL" -o "$f"; then
    echo "${R}Could not download the installer.${N} Check the droplet's network and run $SELF again."
    rm -f "$f"; return 1
  fi
  if ! printf '%s  %s\n' "$INSTALLER_SHA256" "$f" | sha256sum -c --quiet - ; then
    echo "${R}The installer does not match its pinned sha256, so it was not run.${N}"
    echo "Run $SELF again; if it repeats, write to support@simple-host.app."
    rm -f "$f"; return 1
  fi
  local args=(--host "$HOST" --content "$CONTENT")
  if [ -n "$EMAIL" ]; then args+=(--email "$EMAIL"); fi
  # The installer prints the admin key, so the log is readable by root only.
  ( umask 077; : > "$INSTALL_LOG" )
  if [ "$(id -u)" -eq 0 ]; then
    bash "$f" "${args[@]}" 2>&1 | tee -a "$INSTALL_LOG"
  else
    sudo bash "$f" "${args[@]}" 2>&1 | tee -a "$INSTALL_LOG"
  fi
  status=${PIPESTATUS[0]}
  rm -f "$f"
  return "$status"
}

# ---------------------------------------------------------------------------

# Already installed (a re-run, or an image made from a set-up droplet).
if [ -f "$INSTALL_DIR/.env" ] && grep -Eq '^SITE_DOMAIN=.+' "$INSTALL_DIR/.env"; then
  done_host=$(grep '^SITE_DOMAIN=' "$INSTALL_DIR/.env" | cut -d= -f2-)
  echo "Simple Host is already set up: https://$done_host/admin"
  remove_hook
  exit 0
fi

IP=$(public_ip)
cat <<EOF

${B}Simple Host setup${N}

This droplet's public IPv4 address is ${B}${IP:-unknown}${N}.
You need a domain whose DNS you can change. Press Ctrl+C to stop; this runs
again at your next login.

EOF

while :; do
  while :; do
    ask "Address for the dashboard (like hosting.example.com)" ""
    HOST=$(clean_host "$REPLY")
    if valid_host "$HOST"; then break; fi
    echo "  That is not a hostname. Use a name like hosting.example.com, without http://."
  done

  while :; do
    ask "Address the sites are served from" "sites.$HOST"
    CONTENT=$(clean_host "$REPLY")
    if ! valid_host "$CONTENT"; then
      echo "  That is not a hostname."
    elif [ "$CONTENT" = "$HOST" ]; then
      echo "  It has to differ from the dashboard address: sites must not share its origin."
    else
      break
    fi
  done

  while :; do
    ask "Your email, for certificate notices from Let's Encrypt (Enter to skip)" ""
    EMAIL=$REPLY
    if [ -z "$EMAIL" ] || valid_email "$EMAIL"; then break; fi
    echo "  That is not an email address."
  done

  # The records to add: the address, a wildcard under it (which covers the
  # sites address when it sits there), and the sites address when it does not.
  RECORDS=("$HOST" "*.$HOST")
  case "$CONTENT" in
    *."$HOST") ;;
    *) RECORDS+=("$CONTENT") ;;
  esac

  echo
  echo "At your DNS provider, add A records for ${B}${RECORDS[*]}${N} pointing at ${B}${IP:-this droplet}${N}."
  echo "If the provider proxies traffic (Cloudflare's orange cloud), set these to DNS only."
  echo
  echo "Checking DNS (via $DNS_RESOLVER):"
  if ! dns_ok "$HOST" "$CONTENT"; then
    while :; do
      echo
      ask "r = check again for up to $((DNS_WAIT_SECONDS / 60)) min, c = continue anyway, q = quit" "r"
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

  echo
  echo "Dashboard:  https://$HOST"
  echo "Sites:      https://$CONTENT"
  echo "Email:      ${EMAIL:-none}"
  ask "Install with these settings? (y/n)" "y"
  case "$REPLY" in
    y|Y|yes|YES) break ;;
    *) echo; echo "Starting over." ;;
  esac
done

echo
if ! run_installer; then
  echo
  echo "${R}The install did not finish.${N} The output is in $INSTALL_LOG."
  echo "Run $SELF to try again (it is safe to re-run). Help: https://simple-host.app/setup?product=small-box#help"
  exit 1
fi

remove_hook
cat <<EOF

${G}${B}Simple Host is running.${N}

Dashboard:  ${B}https://$HOST/admin${N}
Sign in with the admin key printed above.

To see the admin key again:  grep ^ADMIN_API_KEY= $INSTALL_DIR/.env
To upgrade later: https://github.com/vineetu/simple-host/blob/main/docs/platforms/digitalocean.md
This setup will not run again at login.

EOF
