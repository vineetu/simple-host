#!/usr/bin/env bash
# Stand up a Simple Host event instance on a fresh Ubuntu server.
#
# Idempotent: safe to re-run after a partial failure, which is the point. It is
# also the upgrade: re-running the current script on an existing box pulls the
# release it pins and applies that release's database changes first. The
# setup skill's job is only to get a box and run this; everything that can go
# wrong lives here, where it can be tested, rather than in an agent's judgement.
#
#   install.sh --host hack.example.com --content sites.hack.example.com
#              [--email ADDR] [--max-site-mb N] [--keep-versions N]
#              [--image IMAGE --ref GIT_REF]   (override both together, or neither)
#
# --max-site-mb   how big one website may be, in megabytes. Default 100.
# --keep-versions how many deploys of a website to keep. Default 1; 0 keeps all.
#
# Both are written to /opt/simple-host/.env and preserved when this script is
# re-run without them, so a value chosen once is not silently reset by a retry.
# Any operational time or limit (docs/configuration.md), and the email and
# Google sign-in settings, added to that file are kept on a re-run too.
#
# Prints a JSON summary on success. The admin key is generated here and kept in
# /opt/simple-host/.env; every run prints it again (that is how to recover it).
set -euo pipefail

# The release this installer belongs to. The image and the compose file and
# schema it fetches all come from this one tag, so they cannot drift apart: a
# moving `latest` image against a schema fetched from a moving branch is exactly
# how a fresh install ended up crash-looping on a schema check. The release
# workflow refuses to publish a tag that does not match this line.
VERSION="v0.7.9"
HOST=""; CONTENT=""; IMAGE="ghcr.io/vineetu/simple-host:${VERSION#v}"; ACME_EMAIL=""; REF="$VERSION"
MAX_SITE_MB=""; KEEP_VERSIONS=""
while [ $# -gt 0 ]; do
  case "$1" in
    --host)           HOST="$2"; shift 2 ;;
    --content)        CONTENT="$2"; shift 2 ;;
    --image)          IMAGE="$2"; shift 2 ;;
    --email)          ACME_EMAIL="$2"; shift 2 ;;
    --ref)            REF="$2"; shift 2 ;;
    --max-site-mb)    MAX_SITE_MB="$2"; shift 2 ;;
    --keep-versions)  KEEP_VERSIONS="$2"; shift 2 ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
done
# Reject nonsense here rather than letting the server come up with a setting it
# will ignore, which looks identical to the setting having been applied. Written
# as plain ifs on purpose: a `[ x ] && action` guard returns non-zero when the
# test is false, which under `set -e` is a trap nobody wants in an installer.
check_number() {
  local flag=$1 value=$2 minimum=$3
  if [ -z "$value" ]; then return 0; fi
  case "$value" in
    ''|*[!0-9]*) echo "$flag must be a whole number, got: $value" >&2; exit 2 ;;
  esac
  if [ "$value" -lt "$minimum" ]; then
    echo "$flag must be at least $minimum, got: $value" >&2
    exit 2
  fi
}
check_number --max-site-mb "$MAX_SITE_MB" 1
check_number --keep-versions "$KEEP_VERSIONS" 0

# No --host is a real choice, not a mistake: it is how a box installed from a
# provider's catalog comes up, asking where it lives instead of guessing.
SETUP_ONLY=0
[ -n "$HOST" ] || SETUP_ONLY=1
# Two hostnames, always. Participant pages must never share an origin with the
# admin interface, or anything a participant publishes could script it.
[ -n "$CONTENT" ] || { [ -n "$HOST" ] && CONTENT="sites.$HOST"; } || true

DIR=/opt/simple-host
say() { echo "==> $*"; }

if ! command -v docker >/dev/null 2>&1; then
  say "installing docker"
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

ADMIN_KEY=""; DB_PASSWORD=""; SETUP_PASSWORD=""; KEPT_LIMITS=""
# Operational times and limits (docs/configuration.md; RATE_LIMIT_* too).
LIMIT_VARS=""
LIMIT_VARS+=" SIGNIN_CODE_TTL_MINUTES MAX_KEYS_PER_ACCOUNT KEY_IDLE_EXPIRY_DAYS HANDLE_RENAME_EVERY_DAYS SHOWCASE_BIO_MAX_LENGTH"
LIMIT_VARS+=" EMAIL_CHANGE_UNDO_DAYS MAX_SITES_PER_ACCOUNT KEEP_VERSIONS_SELF_SET KEEP_VERSIONS_OVERRIDES MAX_SITE_TOTAL_MB SITE_TOTAL_CAP_FROM MAX_SITE_TOTAL_OVERRIDES MAX_ACCOUNT_MB MAX_ACCOUNT_MB_OVERRIDES MAX_SITES_OVERRIDES MAX_ARCHIVE_MB_OVERRIDES MAX_FILES_PER_SITE"
LIMIT_VARS+=" SITE_STORAGE_SQL_CONCURRENCY SITE_STORAGE_ACQUIRE_WAIT_MS SITE_STORAGE_WRITE_LOCK_WAIT_MS SITE_STORAGE_OWNER_TIMEOUT_MS SITE_STORAGE_VISITOR_WRITE_TIMEOUT_MS SITE_STORAGE_VISITOR_QUERY_TIMEOUT_MS"
LIMIT_VARS+=" SITE_STORAGE_MAX_BYTES SITE_STORAGE_FILES_MAX_OBJECTS SITE_STORAGE_FILES_MAX_BYTES SITE_STORAGE_FILE_MAX_BYTES SITE_STORAGE_SQL_RESULT_MAX_BYTES"
LIMIT_VARS+=" PREVIEW_LINK_TTL_MINUTES EXPORT_LINK_TTL_MINUTES VISITOR_SESSION_DAYS"
LIMIT_VARS+=" SITE_PASSCODES PASSCODE_MIN_LENGTH PASSCODE_LOCKOUT_MINUTES PASSCODE_SITE_LOCKOUT_MINUTES"
LIMIT_VARS+=" VISITOR_SESSION_IDLE_DAYS OAUTH_ACCESS_TTL_MINUTES"
LIMIT_VARS+=" OAUTH_REFRESH_TTL_DAYS OAUTH_UNUSED_CLIENT_DAYS DOMAIN_UNPROVEN_HOURS"
LIMIT_VARS+=" DOMAIN_UNPROVEN_MAX_DAYS DOMAIN_LAPSE_WARN_HOURS DOMAIN_LAPSE_HOURS"
LIMIT_VARS+=" DOMAIN_CHECK_INTERVAL_MINUTES DOMAIN_CERTS_PER_ACCOUNT_DAILY"
LIMIT_VARS+=" ADDRESS_FAMILIES ADDRESS_FAMILIES_PER_ACCOUNT ADDRESS_FAMILY_UNPROVEN_HOURS"
LIMIT_VARS+=" ADDRESS_FAMILY_LAPSE_WARN_HOURS ADDRESS_FAMILY_LAPSE_HOURS ADDRESS_FAMILY_CHECK_INTERVAL_MINUTES"
LIMIT_VARS+=" ADDRESS_FAMILY_ACTIVE_RECHECK_MINUTES ADDRESS_FAMILY_CERTS_PER_ACCOUNT_DAILY"
LIMIT_VARS+=" ADDRESS_FAMILY_RESERVED_LABELS ADDRESS_FAMILY_CACHE_SECONDS"
LIMIT_VARS+=" EVENT_TTL_DAYS EVENT_MAX_CLAIMS EVENT_CREATE_PER_DAY EVENT_MAX_ACTIVE_PER_ORGANISER EVENT_TEAM_SIZE_DEFAULT EVENT_SITES_KEEP_DAYS EVENT_REMOVAL_WARN_DAYS HACK_INSTANCE_BUDGET_GB HACK_EVENT_ICON_MAX_BYTES DELETED_RETENTION_DAYS IDLE_AFTER_DAYS"
LIMIT_VARS+=" IDLE_GRACE_DAYS IDLE_REPLY_TO ANALYTICS_RETENTION_DAYS IDLE_EXEMPT_FAMILY_SITES"
LIMIT_VARS+=" ANALYTICS_PAGES_PER_SITE_DAY ANALYTICS_REFERRERS_PER_SITE_DAY"
LIMIT_VARS+=" API_METRICS_RETENTION_DAYS AI_MAX_JOBS_PER_USER AI_MAX_JOBS"
LIMIT_VARS+=" AI_JOB_TIMEOUT_MINUTES"
LIMIT_VARS+=" NETWORK_MONTHLY_ALLOWANCE_GB NETWORK_ALERT_PCT NETWORK_SAMPLE_MINUTES"
LIMIT_VARS+=" AI_JOB_TIMEOUT_MINUTES API_GROWTH_RETENTION_DAYS API_METRICS_FLUSH_SECONDS"
LIMIT_VARS+=" SAVED_DATA_UNDO_DAYS SAVED_DATA_HISTORY_MAX_MB SAVED_DATA_SITE_MAX_MB"
LIMIT_VARS+=" SAVED_DATA_SNAPSHOT_EVERY SAVED_DATA_SWEEP_MINUTES SAVED_DATA_WATCH_DAYS"
LIMIT_VARS+=" SAVED_DATA_WATCH_INC_MAX SAVED_DATA_WATCH_ITEM_KB SAVED_DATA_WATCH_KEEP_DAYS"
LIMIT_VARS+=" SAVED_DATA_IDEMPOTENCY_HOURS SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE"
LIMIT_VARS+=" SAVED_DATA_READ_PER_SEC SAVED_DATA_READ_BURST SAVED_DATA_APPEND_PER_MIN"
LIMIT_VARS+=" SAVED_DATA_APPEND_BURST SAVED_DATA_CONTENT_MAX_KB SAVED_DATA_CONTENT_NAMES_MAX"
LIMIT_VARS+=" SAVED_DATA_ENTRY_MAX_KB SAVED_DATA_ENTRIES_MAX SAVED_DATA_WITHDRAW_UNDO_MINUTES"
LIMIT_VARS+=" SAVED_DATA_NOTIFY_EACH_MINUTES SAVED_DATA_NOTIFY_DAILY_HOURS SAVED_DATA_SAVERS_MAX"
LIMIT_VARS+=" SAVED_DATA_ENTRIES_NAMES_MAX SAVED_DATA_DEFAULT_KIND"
LIMIT_VARS+=" SAVED_DATA_PERSONAL_MAX_KB SAVED_DATA_PERSONAL_NAMES_MAX SAVED_DATA_BOARD_ITEM_MAX_KB SAVED_DATA_BOARD_MAX SAVED_DATA_BOARD_NAMES_MAX SAVED_DATA_PERSONAL_PEOPLE_MAX SAVED_DATA_BOARD_WRITES_PER_MIN"
LIMIT_VARS+=" ASK_ENABLED ASK_BURST ASK_EVERY_SECONDS ASK_DAILY_MAX ASK_MAX_IN_FLIGHT ASK_MODEL ASK_REASONING_EFFORT ASK_MAX_TOKENS"
LIMIT_VARS+=" SETUP_CHECK_DAILY_MAX SETUP_CHECK_MAX_IN_FLIGHT SETUP_CHECK_PER_NETWORK_DAILY"
LIMIT_VARS+=" SETUP_ASSIST_DAILY_MAX SETUP_ASSIST_MAX_IN_FLIGHT SETUP_ASSIST_PER_NETWORK_DAILY"
# Email and Google sign-in (added by hand or from the setup helper at
# https://simple-host.app/setup): a re-run is also the upgrade, and must not
# quietly turn sign-in codes off.
LIMIT_VARS+=" RESEND_API_KEY MAIL_FROM GOOGLE_OAUTH_CLIENT_ID GOOGLE_OAUTH_CLIENT_SECRET"
LIMIT_VARS=${LIMIT_VARS# }
if [ -f "$DIR/.env" ]; then
  ADMIN_KEY=$(grep '^ADMIN_API_KEY=' "$DIR/.env" | cut -d= -f2-)
  DB_PASSWORD=$(grep '^DB_PASSWORD=' "$DIR/.env" | cut -d= -f2-)
  SETUP_PASSWORD=$(grep '^SETUP_PASSWORD=' "$DIR/.env" | cut -d= -f2- || true)
  # Settings the operator chose survive a re-run. Rewriting them from the
  # defaults would mean that retrying a failed install quietly undoes a decision
  # somebody made deliberately, which is the worst kind of idempotence bug.
  [ -z "$MAX_SITE_MB" ] && MAX_SITE_MB=$(grep '^MAX_ARCHIVE_MB=' "$DIR/.env" | cut -d= -f2- || true)
  [ -z "$KEEP_VERSIONS" ] && KEEP_VERSIONS=$(grep '^KEEP_VERSIONS=' "$DIR/.env" | cut -d= -f2- || true)
  # The same for the operational times and limits (docs/configuration.md)
  # an operator added to .env: they are carried over as written.
  KEPT_LIMITS=$(grep -E "^(${LIMIT_VARS// /|}|RATE_LIMIT_[A-Z_]+)=" "$DIR/.env" || true)
fi
# Defaults for a fresh event box. One kept version because every version is a
# full copy of the site; 100 MB per site because that is the server's own
# default and real sites are nowhere near it.
[ -n "$KEEP_VERSIONS" ] || KEEP_VERSIONS=1
[ -n "$MAX_SITE_MB" ] || MAX_SITE_MB=100
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

# Setup mode serves an open finish-setup page until a hostname is chosen.
# Without a password, whoever reaches the box first — not necessarily the
# organiser — can complete setup and receive the admin key in the response.
# Generated once and preserved across re-runs, same as the admin key above: an
# older config missing only this (from before this fix, or a truncated retry)
# still gets one filled in now rather than staying open.
if [ "$SETUP_ONLY" -eq 1 ] && [ -z "$SETUP_PASSWORD" ]; then
  SETUP_PASSWORD=$(openssl rand -hex 12)
fi

if [ "$SETUP_ONLY" -eq 1 ]; then
  say "writing configuration (the hostname is chosen on the setup page)"
else
  say "writing configuration for $HOST"
fi
cat > "$DIR/.env" <<EOF
IMAGE=$IMAGE
DB_PASSWORD=$DB_PASSWORD
ADMIN_API_KEY=$ADMIN_KEY
SETUP_PASSWORD=$SETUP_PASSWORD
SITE_DOMAIN=$HOST
CONTENT_HOST=$CONTENT
SITE_ADDR=$HOST
CONTENT_ADDR=$CONTENT
PUBLIC_BASE_URL=https://$HOST
ACME_EMAIL=$ACME_EMAIL
HTTP_PORT=80
HTTPS_PORT=443
MAX_ARCHIVE_MB=$MAX_SITE_MB
KEEP_VERSIONS=$KEEP_VERSIONS
EOF
if [ -n "$KEPT_LIMITS" ]; then
  printf '%s\n' "$KEPT_LIMITS" >> "$DIR/.env"
fi
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

# Upgrading is re-running this script: a newer release may add columns, and the
# server refuses to start against a database behind it. So the database is
# brought up first and `simple-host migrate` (from the image just pulled) applies
# whatever that release added, each file once, before the new app starts. On a
# new volume Postgres loads schema.sql first; migrate then runs and records each
# file, and a file whose effect schema.sql already has changes nothing.
say "updating the database"
docker compose up -d --no-build db
if ! docker compose run --rm -T app migrate; then
  echo "FAILED: the database could not be brought up to $VERSION." >&2
  echo "The app was not restarted. Diagnose with: cd $DIR && docker compose logs --tail 50 db" >&2
  exit 1
fi
docker compose up -d --no-build

say "waiting for the instance to answer"
healthy=0; CONFIGURED=0
for _ in $(seq 1 60); do
  if [ "$SETUP_ONLY" -eq 1 ]; then
    # Until setup finishes the setup page is the only thing that can answer.
    # A box set up earlier in the browser keeps its hostname in the database,
    # not in .env, so a re-run (the upgrade) also comes here: there the setup
    # page is gone (404) and the instance itself answers /healthz.
    if curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1/v1/setup/state" 2>/dev/null; then healthy=1; break; fi
    if curl -fsS -o /dev/null --max-time 3 "http://127.0.0.1/healthz" 2>/dev/null; then healthy=1; CONFIGURED=1; break; fi
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

if [ "$CONFIGURED" -eq 1 ]; then
  # Setup was finished in the browser: report the hostnames it chose.
  HOST=$(docker compose exec -T db psql -U simplehost -d simplehost -tAc "SELECT value FROM instance_config WHERE key = 'site_domain'" 2>/dev/null | tr -d '[:space:]' || true)
  CONTENT=$(docker compose exec -T db psql -U simplehost -d simplehost -tAc "SELECT value FROM instance_config WHERE key = 'content_host'" 2>/dev/null | tr -d '[:space:]' || true)
  cat <<EOF

{"host":"https://$HOST","content_host":"https://$CONTENT","admin_api_key":"$ADMIN_KEY","dir":"$DIR"}

Updated to $VERSION. This box was set up in the browser, so its hostnames live
in its database. The admin key is the one in $DIR/.env; every run prints it.
EOF
elif [ "$SETUP_ONLY" -eq 1 ]; then
  IP=$(curl -fsS --max-time 5 https://api.ipify.org 2>/dev/null || echo "this server's address")
  cat <<EOF

{"setup_url":"http://$IP/","dir":"$DIR"}

open http://$IP/ and enter this setup password: $SETUP_PASSWORD

It asks where this instance lives. The password is kept in $DIR/.env
(SETUP_PASSWORD), and re-running this script prints it again until setup is done.
EOF
else
  cat <<EOF

{"host":"https://$HOST","content_host":"https://$CONTENT","admin_api_key":"$ADMIN_KEY","dir":"$DIR"}

Keep the admin key. It is kept in $DIR/.env, and re-running this script prints it again.
EOF
fi
