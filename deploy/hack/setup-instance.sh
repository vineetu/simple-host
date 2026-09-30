#!/usr/bin/env bash
# One-time (idempotent) setup for the simple-hack.app instance on this box:
# system user, directories, Postgres role/database, /etc/simple-hack.env,
# the systemd unit, logrotate, and the site-certs conf and units.
#
# Dry run by default: prints what it would do, never a secret.
#   sudo bash deploy/hack/setup-instance.sh            # what would change
#   sudo bash deploy/hack/setup-instance.sh --apply    # do it
# Does not start simple-hack.service and does not touch nginx or DNS.
# Every path is overridable for deploy/hack/setup-instance_test.sh.
set -euo pipefail

MODE=dry
for a in "$@"; do
  case "$a" in
    --apply) MODE=apply ;;
    *) echo "usage: $0 [--apply]" >&2; exit 2 ;;
  esac
done

HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)

HACK_USER=${HACK_USER:-simplehack}
HACK_GROUP=${HACK_GROUP:-$HACK_USER}
SITES_DIR=${SITES_DIR:-/srv/simple-hack/sites}
LOG_DIR=${LOG_DIR:-/var/log/simple-hack}
ENV_FILE=${ENV_FILE:-/etc/simple-hack.env}
HOST_ENV=${HOST_ENV:-/etc/simple-host.env}
SYSTEMD_DIR=${SYSTEMD_DIR:-/etc/systemd/system}
LOGROTATE_DEST=${LOGROTATE_DEST:-/etc/logrotate.d/simple-hack}
SITE_CERTS_CONF=${SITE_CERTS_CONF:-/etc/simple-host-site-certs-hack.conf}
SITE_CERTS_DIR=${SITE_CERTS_DIR:-$HERE/../site-certs}
SCHEMA=${SCHEMA:-$REPO/db/schema.sql}
SIMPLE_HOST=${SIMPLE_HOST:-/usr/local/bin/simple-host}
PSQL=${PSQL:-sudo -u postgres psql}
PSQL_APP=${PSQL_APP:-sudo -u simplehack psql}
PG_ROLE=${PG_ROLE:-simplehack}
PG_DB=${PG_DB:-simplehack}
PG_HOST=${PG_HOST:-127.0.0.1}
PG_PORT=${PG_PORT:-5432}

say() { echo "setup-instance: $*"; }
die() { echo "setup-instance: $*" >&2; exit 1; }

name_re='^[a-z_][a-z0-9_-]*$'
[[ "$HACK_USER" =~ $name_re ]] || die "bad HACK_USER: $HACK_USER"
[[ "$HACK_GROUP" =~ $name_re ]] || die "bad HACK_GROUP: $HACK_GROUP"
[[ "$PG_ROLE" =~ $name_re ]] || die "bad PG_ROLE: $PG_ROLE"
[[ "$PG_DB" =~ $name_re ]] || die "bad PG_DB: $PG_DB"

psql_super() {
  # shellcheck disable=SC2086
  $PSQL "$@"
}
psql_app() {
  # shellcheck disable=SC2086
  $PSQL_APP "$@"
}

role_exists() {
  local out
  out=$(psql_super -tAc "SELECT 1 FROM pg_roles WHERE rolname='${PG_ROLE}'" 2>/dev/null || true)
  [[ "$out" == *1* ]]
}
db_exists() {
  local out
  out=$(psql_super -tAc "SELECT 1 FROM pg_database WHERE datname='${PG_DB}'" 2>/dev/null || true)
  [[ "$out" == *1* ]]
}
users_table_exists() {
  local out
  out=$(psql_super -d "$PG_DB" -tAc "SELECT to_regclass('public.users')" 2>/dev/null || true)
  [[ "$out" == *users* ]]
}
user_exists() {
  getent passwd "$HACK_USER" >/dev/null 2>&1
}

dsn_from_env() {
  grep '^DB_DSN=' "$ENV_FILE" | cut -d= -f2-
}
# Password from a postgres:// URI; prints only to stdout for capture.
password_from_dsn() {
  local rest cred
  rest=${1#*://}
  cred=${rest%%@*}
  printf '%s' "${cred#*:}"
}

# Write /etc/simple-hack.env from the example values plus generated secrets.
# $1 is the database password (32 hex). Never printed.
write_env() {
  local dbpw=$1 admin_key passcode salt envdir tmp old_umask
  envdir=$(dirname "$ENV_FILE")
  mkdir -p "$envdir"
  old_umask=$(umask)
  umask 077
  tmp=$(mktemp "$envdir/.simple-hack.env.XXXXXX")
  admin_key=$(openssl rand -hex 32)
  passcode=$(openssl rand -base64 32)
  salt=$(openssl rand -hex 32)
  cat > "$tmp" <<EOF
# Written by deploy/hack/setup-instance.sh. Secrets stay in this file.
SITE_DOMAIN=simple-hack.app
PUBLIC_BASE_URL=https://simple-hack.app
PORT=8091
BIND_ADDR=127.0.0.1
DATA_DIR=/srv/simple-hack/sites
EVENTS=hosted
PERSON_HOSTS=canonical
SITE_HOSTS=canonical
SITE_CERT_DIR=/var/lib/simple-host-site-certs-hack
MAX_ARCHIVE_MB=25
KEEP_VERSIONS=2
EVENT_NAME_PEER=http://127.0.0.1:8090
ANALYTICS_LOG=/var/log/simple-hack/analytics.log
CUSTOM_DOMAIN_IP=147.224.49.228
WRITE_AUTH_MODE=on
MAIL_FROM=Simple Hack <noreply@simple-host.app>
DB_DSN=postgres://${PG_ROLE}:${dbpw}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable
ADMIN_API_KEY=${admin_key}
PASSCODE_ENC_KEY=${passcode}
ANALYTICS_SALT=${salt}
EOF
  if [ -r "$HOST_ENV" ]; then
    grep -E '^(RESEND_API_KEY|GOOGLE_OAUTH_CLIENT_ID|GOOGLE_OAUTH_CLIENT_SECRET)=' "$HOST_ENV" >> "$tmp" || true
  fi
  local k
  for k in RESEND_API_KEY GOOGLE_OAUTH_CLIENT_ID GOOGLE_OAUTH_CLIENT_SECRET; do
    grep -q "^${k}=" "$tmp" || printf '%s=\n' "$k" >> "$tmp"
  done
  cat >> "$tmp" <<'EOF'
#EVENT_CREATE_PER_DAY=3
#EVENT_MAX_ACTIVE_PER_ORGANISER=2
#EVENT_TEAM_SIZE_DEFAULT=4
#EVENT_SITES_KEEP_DAYS=30
#HACK_INSTANCE_BUDGET_GB=10
EOF
  chmod 0600 "$tmp"
  mv -f "$tmp" "$ENV_FILE"
  chmod 0600 "$ENV_FILE"
  chown root:root "$ENV_FILE"
  umask "$old_umask"
}

install_units() {
  mkdir -p "$SYSTEMD_DIR" "$(dirname "$LOGROTATE_DEST")" "$(dirname "$SITE_CERTS_CONF")"
  install -m 644 "$HERE/simple-hack.service" "$SYSTEMD_DIR/simple-hack.service"
  install -m 644 "$HERE/logrotate-simple-hack.conf" "$LOGROTATE_DEST"
  if [ ! -f "$SITE_CERTS_CONF" ]; then
    install -m 644 "$SITE_CERTS_DIR/simple-host-site-certs-hack.conf.example" "$SITE_CERTS_CONF"
  fi
  install -m 644 "$SITE_CERTS_DIR/simple-host-site-certs-hack.service" "$SYSTEMD_DIR/simple-host-site-certs-hack.service"
  install -m 644 "$SITE_CERTS_DIR/simple-host-site-certs-hack.path" "$SYSTEMD_DIR/simple-host-site-certs-hack.path"
  install -m 644 "$SITE_CERTS_DIR/simple-host-site-certs-hack.timer" "$SYSTEMD_DIR/simple-host-site-certs-hack.timer"
  systemctl daemon-reload
  systemctl enable simple-host-site-certs-hack.path simple-host-site-certs-hack.timer
}

# --- dry run: read, never write, never print a secret ---
if [ "$MODE" = dry ]; then
  if user_exists; then say "ok: system user $HACK_USER exists"
  else say "would add system user $HACK_USER (no home, no login shell)"; fi
  say "would ensure $SITES_DIR (0750 ${HACK_USER}:${HACK_GROUP})"
  say "would ensure $LOG_DIR (0755 root:root)"
  if role_exists; then say "ok: Postgres role $PG_ROLE exists"
  else say "would create Postgres role $PG_ROLE (generated password)"; fi
  if db_exists; then say "ok: Postgres database $PG_DB exists"
  else say "would create Postgres database $PG_DB owned by $PG_ROLE"; fi
  if db_exists && users_table_exists; then say "ok: $PG_DB already has a users table"
  else say "would apply $SCHEMA to $PG_DB as $PG_ROLE (if no users table)"; fi
  say "would run $SIMPLE_HOST migrate"
  if [ -f "$ENV_FILE" ]; then say "ok: $ENV_FILE exists (left untouched)"
  else
    say "would write $ENV_FILE (0600 root:root) with generated secrets"
    if [ -r "$HOST_ENV" ]; then
      say "would copy RESEND_API_KEY and Google OAuth keys from $HOST_ENV"
    else
      say "would leave RESEND_API_KEY and Google OAuth keys empty ($HOST_ENV missing)"
    fi
  fi
  say "would install $SYSTEMD_DIR/simple-hack.service"
  say "would install $LOGROTATE_DEST"
  if [ -f "$SITE_CERTS_CONF" ]; then say "ok: $SITE_CERTS_CONF exists (left untouched)"
  else say "would install $SITE_CERTS_CONF"; fi
  say "would install site-certs-hack units, daemon-reload, enable path and timer"
  say "would not start simple-hack.service; would not touch nginx or DNS"
  echo "dry run: nothing changed (add --apply)"
  exit 0
fi

# --- apply ---
if user_exists; then
  say "ok: system user $HACK_USER exists"
else
  useradd --system --no-create-home --shell /usr/sbin/nologin "$HACK_USER"
  say "added system user $HACK_USER"
fi

install -d -m 0750 -o "$HACK_USER" -g "$HACK_GROUP" "$SITES_DIR"
install -d -m 0755 -o root -g root "$LOG_DIR"
say "directories: $SITES_DIR, $LOG_DIR"

dbpw=""
if role_exists; then
  say "ok: Postgres role $PG_ROLE exists"
else
  if [ -f "$ENV_FILE" ]; then
    dbpw=$(password_from_dsn "$(dsn_from_env)")
  else
    dbpw=$(openssl rand -hex 16)
  fi
  [ -n "$dbpw" ] || die "cannot create Postgres role $PG_ROLE without a password"
  psql_super -v ON_ERROR_STOP=1 -c "CREATE ROLE ${PG_ROLE} LOGIN PASSWORD '${dbpw}'"
  say "created Postgres role $PG_ROLE"
fi
if db_exists; then
  say "ok: Postgres database $PG_DB exists"
else
  psql_super -v ON_ERROR_STOP=1 -c "CREATE DATABASE ${PG_DB} OWNER ${PG_ROLE}"
  say "created Postgres database $PG_DB"
fi

if [ -f "$ENV_FILE" ]; then
  say "ok: $ENV_FILE exists (left untouched)"
else
  if [ -z "$dbpw" ]; then
    die "$ENV_FILE is missing but Postgres role $PG_ROLE already exists; restore the env file (its password is not in this script)"
  fi
  write_env "$dbpw"
  say "wrote $ENV_FILE (0600)"
fi

dsn=$(dsn_from_env)
[ -n "$dsn" ] || die "$ENV_FILE has no DB_DSN"

if users_table_exists; then
  say "ok: $PG_DB already has a users table"
else
  [ -f "$SCHEMA" ] || die "missing $SCHEMA"
  psql_app -v ON_ERROR_STOP=1 -f "$SCHEMA" "$dsn"
  say "applied schema to $PG_DB"
fi

DB_DSN="$dsn" "$SIMPLE_HOST" migrate
say "ran $SIMPLE_HOST migrate"

install_units
say "installed unit, logrotate, site-certs conf and units; path and timer enabled"
say "not starting simple-hack.service; nginx and DNS are unchanged"
