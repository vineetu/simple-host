#!/usr/bin/env bash
# Secrets are supplied after imaging, never baked into the snapshot or logged.
set -euo pipefail
SETUP_DIR=${SETUP_DIR:-/opt/simple-hack-setup}
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-hack}
DONE_FILE=${DONE_FILE:-$SETUP_DIR/.done}
BASHRC=${BASHRC:-/root/.bashrc}
if [ "${1:-}" = --apply ]; then
  trap '' HUP INT
  bash "$SETUP_DIR/install.sh" --dir "$INSTALL_DIR"
  touch "$DONE_FILE"
  sed -i '/^# >>> simple-hack setup >>>$/,/^# <<< simple-hack setup <<<$/{d;}' "$BASHRC"
  exit 0
fi
[ -f "$DONE_FILE" ] && exit 0
[ "$(id -u)" = 0 ] || { echo 'Run first-login setup as root.' >&2; exit 1; }
mkdir -p "$INSTALL_DIR"
chmod 700 "$INSTALL_DIR"
exec 8>"$INSTALL_DIR/.first-login.lock"
flock -n 8 || { echo "Setup is already running. Read $INSTALL_DIR/install.log"; exit 0; }
if [ ! -f "$INSTALL_DIR/.env" ]; then
  read -r -p 'Your Simple Hack domain (for example hack.example.com): ' SITE_DOMAIN
  SITE_DOMAIN=${SITE_DOMAIN,,}
  ip=$(curl -fsS --max-time 5 http://169.254.169.254/metadata/v1/interfaces/public/0/ipv4/address)
  for host in "$SITE_DOMAIN" "setup-check.$SITE_DOMAIN" "team.setup-check.$SITE_DOMAIN"; do
    dig +time=3 +tries=1 +short A "$host" @1.1.1.1 | grep -Fxq "$ip" || {
      echo "DNS for $host must point to this droplet ($ip). Set the apex and wildcard A records, then run this setup again." >&2
      exit 1
    }
    if [ -n "$(dig +time=3 +tries=1 +short AAAA "$host" @1.1.1.1)" ]; then
      echo "Remove the AAAA record for $host before this IPv4 setup. Configure and verify IPv6 separately after installation." >&2; exit 1
    fi
  done
  read -r -s -p 'Resend API key (hidden): ' RESEND_API_KEY; echo
  read -r -p 'Verified sender (for example Simple Hack <events@example.com>): ' MAIL_FROM
  export SITE_DOMAIN RESEND_API_KEY MAIL_FROM
  bash "$SETUP_DIR/install.sh" --dir "$INSTALL_DIR" --prepare-only
  unset RESEND_API_KEY MAIL_FROM
fi
install -m 600 /dev/null "$INSTALL_DIR/install.log"
# The detached child inherits the setup lock, so a second login cannot
# start a second installation while the first survives an SSH disconnect.
nohup setsid -w bash "$SETUP_DIR/digitalocean/first-login.sh" --apply >"$INSTALL_DIR/install.log" 2>&1 </dev/null &
pid=$!
echo "Installing. It continues if SSH disconnects. Log: $INSTALL_DIR/install.log"
tail --pid="$pid" -n +1 -f "$INSTALL_DIR/install.log" || true
wait "$pid"
echo "Setup complete. Sign in at your domain to create an event."
