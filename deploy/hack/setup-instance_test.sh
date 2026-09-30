#!/usr/bin/env bash
# Sandbox test for setup-instance.sh (no root, never /etc or the live
# database): every path pointed into a temp dir, fake psql/systemctl/useradd
# on PATH. Checks: dry run changes nothing; --apply writes the env file 0600
# with every key set exactly once and prints no secret; a second --apply
# leaves the env file untouched.
#   bash deploy/hack/setup-instance_test.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
S="$HERE/setup-instance.sh"
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "  ok — $1"; }
bad() { echo "  FAIL: $1"; fail=1; }
chk() { if eval "$2"; then ok "$1"; else bad "$1"; fi; }

R="$T/root"
mkdir -p "$T/bin" "$T/pg" "$R/etc" "$R/etc/systemd/system" "$R/etc/logrotate.d" \
  "$R/srv" "$R/var/log"
echo "CREATE TABLE users (id int);" > "$T/schema.sql"
cat > "$T/host.env" <<'EOF'
RESEND_API_KEY=re_test_secret_do_not_print
GOOGLE_OAUTH_CLIENT_ID=gid.apps.googleusercontent.com
GOOGLE_OAUTH_CLIENT_SECRET=gsecret_do_not_print
OTHER=ignored
EOF

cat > "$T/bin/install" <<EOF
#!/usr/bin/env bash
owner=""; group=""; a=()
while [ \$# -gt 0 ]; do
  case \$1 in
    -o) owner=\$2; shift 2 ;;
    -g) group=\$2; shift 2 ;;
    *) a+=("\$1"); shift ;;
  esac
done
echo "install -o \$owner -g \$group \${a[*]}" >> "$T/calls"
exec /usr/bin/install "\${a[@]}"
EOF
cat > "$T/bin/useradd" <<EOF
#!/bin/sh
echo "useradd \$*" >> "$T/calls"
touch "$T/user-exists"
EOF
cat > "$T/bin/getent" <<EOF
#!/bin/sh
echo "getent \$*" >> "$T/calls"
if [ "\$1" = passwd ] && [ -f "$T/user-exists" ]; then
  echo "simplehack:x:999:999::/nonexistent:/usr/sbin/nologin"
  exit 0
fi
exit 1
EOF
cat > "$T/bin/systemctl" <<EOF
#!/bin/sh
echo "systemctl \$*" >> "$T/calls"
EOF
cat > "$T/bin/chown" <<EOF
#!/bin/sh
echo "chown \$*" >> "$T/calls"
EOF
cat > "$T/bin/simple-host" <<EOF
#!/bin/sh
echo "simple-host \$*" >> "$T/calls"
exit 0
EOF
cat > "$T/bin/psql" <<EOF
#!/usr/bin/env bash
echo "psql \$*" >> "$T/calls"
file=""; query=""
args=("\$@")
i=0
while [ \$i -lt \${#args[@]} ]; do
  a=\${args[\$i]}
  case "\$a" in
    -f) i=\$((i+1)); file=\${args[\$i]} ;;
    -tAc|-c) i=\$((i+1)); query=\${args[\$i]} ;;
  esac
  i=\$((i+1))
done
if [ -n "\$file" ]; then
  if grep -q 'CREATE ROLE' "\$file" 2>/dev/null; then touch "$T/pg/role"; fi
  if grep -q 'CREATE TABLE' "\$file" 2>/dev/null; then touch "$T/pg/users"; fi
fi
case "\$query" in
  *CREATE\ ROLE*) touch "$T/pg/role" ;;
  *CREATE\ DATABASE*) touch "$T/pg/db" ;;
esac
case "\$query" in
  *pg_roles*) [ -f "$T/pg/role" ] && echo 1; exit 0 ;;
  *pg_database*) [ -f "$T/pg/db" ] && echo 1; exit 0 ;;
  *to_regclass*|*users*) [ -f "$T/pg/users" ] && echo users; exit 0 ;;
esac
exit 0
EOF
chmod +x "$T/bin/"*

ENVF="$R/etc/simple-hack.env"
export PATH="$T/bin:$PATH"
export PSQL=psql PSQL_APP=psql
export HACK_USER=simplehack
export SITES_DIR="$R/srv/simple-hack/sites"
export LOG_DIR="$R/var/log/simple-hack"
export ENV_FILE="$ENVF"
export HOST_ENV="$T/host.env"
export SYSTEMD_DIR="$R/etc/systemd/system"
export LOGROTATE_DEST="$R/etc/logrotate.d/simple-hack"
export SITE_CERTS_CONF="$R/etc/simple-host-site-certs-hack.conf"
export SITE_CERTS_DIR="$HERE/../site-certs"
export SCHEMA="$T/schema.sql"
export SIMPLE_HOST="$T/bin/simple-host"

run() { bash "$S" "$@"; }
snap() { (cd "$R" && find . -printf '%p %s\n' | sort); }

touch "$T/calls"
echo "== dry run =="
before=$(snap)
out=$(run 2>&1) || { echo "$out"; bad "dry run exited non-zero"; }
after=$(snap)
chk "dry run says it would add the user and write the env file" "grep -q 'would add system user simplehack' <<<\"\$out\" && grep -q 'would write $ENVF' <<<\"\$out\""
chk "dry run changes nothing" "[ \"\$before\" = \"\$after\" ]"
chk "dry run prints no copied secret" "! grep -qF re_test_secret_do_not_print <<<\"\$out\" && ! grep -qF gsecret_do_not_print <<<\"\$out\""
chk "dry run does not call useradd, systemctl or migrate" "! grep -q useradd '$T/calls' && ! grep -q systemctl '$T/calls' && ! grep -q simple-host '$T/calls'"
chk "dry run does not start simple-hack.service" "! grep -q simple-hack.service <<<\"\$out\" || grep -q 'would not start simple-hack.service' <<<\"\$out\""

echo "== apply =="
: > "$T/calls"
out=$(run --apply 2>&1) || { echo "$out"; echo "FAIL: --apply exited non-zero"; exit 1; }
printf '%s\n' "$out" > "$T/apply.out"
chk "env file written 0600" "[ -f '$ENVF' ] && [ \"\$(stat -c %a '$ENVF')\" = 600 ]"
keys='SITE_DOMAIN PUBLIC_BASE_URL PORT BIND_ADDR DATA_DIR EVENTS PERSON_HOSTS SITE_HOSTS SITE_CERT_DIR MAX_ARCHIVE_MB KEEP_VERSIONS EVENT_NAME_PEER ANALYTICS_LOG CUSTOM_DOMAIN_IP WRITE_AUTH_MODE MAIL_FROM DB_DSN ADMIN_API_KEY PASSCODE_ENC_KEY ANALYTICS_SALT RESEND_API_KEY GOOGLE_OAUTH_CLIENT_ID GOOGLE_OAUTH_CLIENT_SECRET'
key_fail=0
for k in $keys; do
  n=$(grep -c "^${k}=" "$ENVF" || true)
  if [ "$n" != 1 ]; then
    echo "  FAIL: $k appears $n times"
    key_fail=1
  fi
done
chk "every key set exactly once" "[ $key_fail = 0 ]"
chk "copied mail and Google keys from the host env" "grep -qx 'RESEND_API_KEY=re_test_secret_do_not_print' '$ENVF' && grep -qx 'GOOGLE_OAUTH_CLIENT_SECRET=gsecret_do_not_print' '$ENVF'"
chk "SITE_DOMAIN and EVENTS match the example" "grep -qx 'SITE_DOMAIN=simple-hack.app' '$ENVF' && grep -qx 'EVENTS=hosted' '$ENVF' && grep -qx 'PORT=8091' '$ENVF'"
admin=$(grep '^ADMIN_API_KEY=' "$ENVF" | cut -d= -f2-)
salt=$(grep '^ANALYTICS_SALT=' "$ENVF" | cut -d= -f2-)
pass=$(grep '^PASSCODE_ENC_KEY=' "$ENVF" | cut -d= -f2-)
dsn=$(grep '^DB_DSN=' "$ENVF" | cut -d= -f2-)
dbpw=${dsn#*://}; dbpw=${dbpw#*:}; dbpw=${dbpw%%@*}
chk "generated secrets are non-empty" "[ -n \"\$admin\" ] && [ -n \"\$salt\" ] && [ -n \"\$pass\" ] && [ -n \"\$dbpw\" ]"
secret_printed=0
for v in "$admin" "$salt" "$pass" "$dbpw" re_test_secret_do_not_print gsecret_do_not_print; do
  if grep -F -- "$v" "$T/apply.out" >/dev/null; then
    echo "  FAIL: secret leaked in apply output"
    secret_printed=1
  fi
done
chk "no secret printed to stdout/stderr" "[ $secret_printed = 0 ]"
chk "sites dir created" "[ -d '$R/srv/simple-hack/sites' ]"
chk "log dir created" "[ -d '$R/var/log/simple-hack' ]"
chk "useradd, migrate, daemon-reload, enable path and timer" "grep -q 'useradd --system' '$T/calls' && grep -q 'simple-host migrate' '$T/calls' && grep -q 'systemctl daemon-reload' '$T/calls' && grep -q 'systemctl enable simple-host-site-certs-hack.path simple-host-site-certs-hack.timer' '$T/calls'"
chk "simple-hack.service was not started" "! grep -q 'systemctl start simple-hack' '$T/calls' && ! grep -q 'systemctl enable --now simple-hack.service' '$T/calls'"
chk "unit, logrotate and site-certs conf installed" "[ -f '$R/etc/systemd/system/simple-hack.service' ] && [ -f '$R/etc/logrotate.d/simple-hack' ] && [ -f '$R/etc/simple-host-site-certs-hack.conf' ]"
chk "site-certs path and timer units installed" "[ -f '$R/etc/systemd/system/simple-host-site-certs-hack.path' ] && [ -f '$R/etc/systemd/system/simple-host-site-certs-hack.timer' ]"

echo "== second apply =="
cp -p "$ENVF" "$T/env.first"
meta=$(stat -c '%Y %s %a' "$ENVF")
: > "$T/calls"
out=$(run --apply 2>&1) || { echo "$out"; bad "second --apply exited non-zero"; }
chk "second --apply leaves the env file untouched" "cmp -s '$ENVF' '$T/env.first' && [ \"\$(stat -c '%Y %s %a' '$ENVF')\" = '$meta' ]"
chk "second --apply does not create the user again" "! grep -q useradd '$T/calls'"
chk "second --apply still migrates" "grep -q 'simple-host migrate' '$T/calls'"

[ "$fail" = 0 ] && echo "setup-instance: ok" || { echo "setup-instance: FAILED"; exit 1; }
