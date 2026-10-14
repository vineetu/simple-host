#!/usr/bin/env bash
# Test for nginx-unlisted-marker.sh on rendered templates (no nginx needed).
set -euo pipefail
cd "$(dirname "$0")/../.."
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
fail=0
ok() { echo "ok   $1"; }
bad() { echo "FAIL $1"; fail=1; }
mkdir -p "$T/en"
# A domain server and a family server as the issuers wrote them before the
# unlisted line, and a hand-made vhost that must be left alone.
sed -e '/__PARTNER_BEGIN__/,/__PARTNER_END__/d' -e 's/__DOMAIN__/d.test/g' deploy/domain-certs/vhost.conf.template | grep -v '/unlisted)' > "$T/en/simple-host-domain-d.test"
sed -e 's/__SUFFIX_RE__/f\\.test/g' -e 's/__SUFFIX__/f.test/g' -e 's/__PREFIX__/p-/g' -e 's/__CERT__/f.test/g' -e '/__RESERVED_BEGIN__/,/__RESERVED_END__/d' deploy/family-certs/vhost.conf.template | grep -v '/unlisted)' > "$T/en/simple-host-family-f.test"
printf 'server {\n  root /srv/simple-host/sites/domains/hand.test/current;\n  if (-f /srv/simple-host/sites/domains/hand.test/passcode) { rewrite ^ /internal/passcode$uri last; }\n}\n' > "$T/en/hand.test"
cp "$T/en/hand.test" "$T/hand.orig"
run() { NGINX_SITES_DIR="$T/en" BACKUP_DIR="$T/bak" NGINX_TEST=true NGINX_RELOAD=true bash deploy/prod/nginx-unlisted-marker.sh "$@"; }
out=$(run)
[[ "$out" == *"would $T/en/simple-host-domain-d.test (1 insert(s))"* && "$out" == *"would $T/en/simple-host-family-f.test (1 insert(s))"* ]] && ok "dry run names both managed files" || bad "dry run: $out"
grep -q unlisted "$T/en/simple-host-domain-d.test" && bad "dry run changed a file" || ok "dry run changes nothing"
run --apply >/dev/null
d="$T/en/simple-host-domain-d.test"; f="$T/en/simple-host-family-f.test"
grep -qF 'if (-f /srv/simple-host/sites/domains/d.test/unlisted) { rewrite ^ /internal/passcode$uri last; }' "$d" && ok "domain gets the unlisted check" || bad "domain check missing"
[ "$(grep -n 'd.test/passcode)' "$d" | cut -d: -f1)" -lt "$(grep -n 'd.test/unlisted)' "$d" | cut -d: -f1)" ] && ok "domain: after passcode" || bad "domain order"
grep -qF 'if (-f /srv/simple-host/sites/families/f.test/p-$sh_label/unlisted) { rewrite ^ /internal/passcode$uri last; }' "$f" && ok "family gets the unlisted check" || bad "family check missing"
[ "$(grep -n 'lives-elsewhere)' "$f" | cut -d: -f1)" -lt "$(grep -n 'p-$sh_label/unlisted)' "$f" | cut -d: -f1)" ] && ok "family: after lives-elsewhere" || bad "family order"
# The result is what the templates render now.
diff <(sed -e '/__PARTNER_BEGIN__/,/__PARTNER_END__/d' -e 's/__DOMAIN__/d.test/g' deploy/domain-certs/vhost.conf.template | grep -v '^ *#') <(grep -v '^ *#' "$d") >/dev/null && ok "domain matches the template" || bad "domain differs from the template"
cmp -s "$T/en/hand.test" "$T/hand.orig" && ok "hand-made vhost untouched" || bad "hand-made vhost changed"
out=$(run --apply); [[ "$out" == *"nothing to change"* ]] && ok "idempotent" || bad "second run: $out"
exit $fail
