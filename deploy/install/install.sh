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
[ -n "$HOST" ] || { echo "--host is required" >&2; exit 2; }
# Two hostnames, always. Participant pages must never share an origin with the
# admin interface, or anything a participant publishes could script it.
[ -n "$CONTENT" ] || CONTENT="sites.$HOST"

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
if [ -f "$DIR/.env" ]; then
  say "reusing existing configuration"
  ADMIN_KEY=$(grep '^ADMIN_API_KEY=' "$DIR/.env" | cut -d= -f2-)
  DB_PASSWORD=$(grep '^DB_PASSWORD=' "$DIR/.env" | cut -d= -f2-)
else
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
EOF
chmod 600 "$DIR/.env"

say "fetching compose files"
curl -fsSL -o "$DIR/compose.yaml" https://raw.githubusercontent.com/vineetu/simple-host/$REF/compose.yaml
mkdir -p "$DIR/deploy/compose" "$DIR/db"
curl -fsSL -o "$DIR/deploy/compose/Caddyfile" https://raw.githubusercontent.com/vineetu/simple-host/$REF/deploy/compose/Caddyfile
curl -fsSL -o "$DIR/db/schema.sql" https://raw.githubusercontent.com/vineetu/simple-host/$REF/db/schema.sql

# The published image is what runs; nothing is ever compiled on this box.
say "pulling $IMAGE"
cd "$DIR"
docker compose pull --quiet
docker compose up -d --no-build

say "waiting for the instance to answer"
for _ in $(seq 1 60); do
  if curl -fsS -o /dev/null --max-time 3 -H "Host: $HOST" "http://127.0.0.1/healthz" 2>/dev/null; then break; fi
  sleep 2
done

cat <<EOF

{"host":"https://$HOST","content_host":"https://$CONTENT","admin_api_key":"$ADMIN_KEY","dir":"$DIR"}

Keep the admin key. It is shown once and nothing else displays it.
EOF
