#!/usr/bin/env bash
# Stand up a Simple Host event instance on a fresh Ubuntu server.
#
# Idempotent: safe to re-run after a partial failure, which is the point. The
# setup skill's job is only to get a box and run this; everything that can go
# wrong lives here, where it can be tested, rather than in an agent's judgement.
#
#   install.sh --host hack.example.com --content sites.hack.example.com [--image REF]
#
# Prints a JSON summary on success. The admin key is generated here and shown
# exactly once, because nothing else ever displays it.
set -euo pipefail

HOST=""; CONTENT=""; IMAGE="ghcr.io/vineetu/simple-host:latest"; ACME_EMAIL=""; REF="main"
while [ $# -gt 0 ]; do
  case "$1" in
    --host)    HOST="$2"; shift 2 ;;
    --content) CONTENT="$2"; shift 2 ;;
    --image)   IMAGE="$2"; shift 2 ;;
    --email)   ACME_EMAIL="$2"; shift 2 ;;
    --ref)     REF="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
# No --host is a real choice, not a mistake: it is how a box installed from a
# provider's catalog comes up, asking where it lives instead of guessing.
SETUP_ONLY=0
[ -n "$HOST" ] || SETUP_ONLY=1
# Two hostnames, always. Participant pages must never share an origin with the
# admin interface, or anything a participant publishes could script it.
[ -n "$CONTENT" ] || { [ -n "$HOST" ] && CONTENT="sites.$HOST"; } || true

DIR=/opt/simple-host
say() { echo "==> $*"; }

say "installing docker"
if ! command -v docker >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  apt-get install -y -qq ca-certificates curl >/dev/null
  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
  chmod a+r /etc/apt/keyrings/docker.asc
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
    > /etc/apt/sources.list.d/docker.list
  apt-get update -qq
  apt-get install -y -qq docker-ce docker-ce-cli containerd.io docker-compose-plugin >/dev/null
fi
systemctl enable --now docker >/dev/null 2>&1 || true

mkdir -p "$DIR"

# Generated once and preserved across re-runs. Regenerating the admin key on a
# re-run would lock the organiser out of their own instance mid-event, and
# regenerating the database password would break the running database.
# A surviving database with a lost .env is unrecoverable by this script: the
# volume holds a password that only the deleted file knew. Generating a new one
# produces an instance that cannot authenticate to its own database, and the
# error it prints blames the password rather than the missing file.
if [ ! -f "$DIR/.env" ] && docker volume ls -q 2>/dev/null | grep -q '^simple-host_db$'; then
  echo "FAILED: a database volume exists but $DIR/.env is missing." >&2
  echo "Its password lived only in that file. Either restore it, or discard the" >&2
  echo "old instance and its data with:" >&2
  echo "  docker volume rm simple-host_db simple-host_sites simple-host_caddy_data simple-host_caddy_config" >&2
  exit 1
fi

ADMIN_KEY=""; DB_PASSWORD=""
if [ -f "$DIR/.env" ]; then
  ADMIN_KEY=$(grep '^ADMIN_API_KEY=' "$DIR/.env" | cut -d= -f2-)
  DB_PASSWORD=$(grep '^DB_PASSWORD=' "$DIR/.env" | cut -d= -f2-)
fi
# An interrupted first run can leave .env truncated with one or both values
# missing. Reusing that would rewrite the same broken file on every retry, so
# the script would never self-heal -- the opposite of the point.
if [ -n "$ADMIN_KEY" ] && [ -n "$DB_PASSWORD" ]; then
  say "reusing existing configuration"
else
  if [ -f "$DIR/.env" ]; then
    say "existing configuration is incomplete; regenerating"
    cp "$DIR/.env" "$DIR/.env.broken.$(date +%s)" 2>/dev/null || true
  fi
  ADMIN_KEY="sh_admin_$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')"
  DB_PASSWORD=$(head -c 18 /dev/urandom | od -An -tx1 | tr -d ' \n')
fi

say "writing configuration for $HOST"
cat > "$DIR/.env" <<EOF
IMAGE=$IMAGE
DB_PASSWORD=$DB_PASSWORD
ADMIN_API_KEY=$ADMIN_KEY
SITE_DOMAIN=$HOST
CONTENT_HOST=$CONTENT
SITE_ADDR=$HOST
CONTENT_ADDR=$CONTENT
PUBLIC_BASE_URL=https://$HOST
ACME_EMAIL=$ACME_EMAIL
HTTP_PORT=80
HTTPS_PORT=443
MAX_ARCHIVE_MB=auto
KEEP_VERSIONS=1
EOF
chmod 600 "$DIR/.env"

say "fetching compose files"
# Every directory first, then every fetch. A transient failure part-way through
# used to leave a half-populated directory that the next step then failed on
# for a different reason, which is a miserable thing to debug over SSH.
mkdir -p "$DIR/deploy/compose" "$DIR/db"
fetch() {
  local dest=$1 path=$2
  for attempt in 1 2 3; do
    if curl -fsSL --max-time 30 -o "$dest" "https://raw.githubusercontent.com/vineetu/simple-host/$REF/$path"; then
      return 0
    fi
    sleep $((attempt * 2))
  done
  echo "FAILED: could not fetch $path after three attempts." >&2
  exit 1
}
fetch "$DIR/compose.yaml" compose.yaml
fetch "$DIR/deploy/compose/Caddyfile" deploy/compose/Caddyfile
fetch "$DIR/db/schema.sql" db/schema.sql

# The published image is what runs; nothing is ever compiled on this box.
say "pulling $IMAGE"
cd "$DIR"
docker compose pull --quiet
docker compose up -d --no-build

say "waiting for the instance to answer"
healthy=0
for _ in $(seq 1 60); do
  if [ "$SETUP_ONLY" -eq 1 ]; then
    # The product is absent until setup finishes, so the setup page is the
    # only thing that can answer, and answering is what success means here.
    if curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1/v1/setup/state" 2>/dev/null; then healthy=1; break; fi
  else
    if curl -fsS -o /dev/null --max-time 3 -H "Host: $HOST" "http://127.0.0.1/healthz" 2>/dev/null; then healthy=1; break; fi
  fi
  sleep 2
done
# Never print the success envelope for an instance that did not come up. An
# agent reading this output would otherwise hand the organiser an admin key for
# something that is not running.
if [ "$healthy" -ne 1 ]; then
  echo "FAILED: the instance did not answer within two minutes." >&2
  echo "Diagnose with: cd $DIR && docker compose ps && docker compose logs --tail 50" >&2
  exit 1
fi

if [ "$SETUP_ONLY" -eq 1 ]; then
  IP=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || echo "<this server\'s address>")
  cat <<EOF

{"setup_url":"http://$IP/","dir":"$DIR"}

Open that address in a browser to finish. It asks where this instance lives.
EOF
else
  cat <<EOF

{"host":"https://$HOST","content_host":"https://$CONTENT","admin_api_key":"$ADMIN_KEY","dir":"$DIR"}

Keep the admin key. It is shown once and nothing else displays it.
EOF
fi
