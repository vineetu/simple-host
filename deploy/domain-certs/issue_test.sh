#!/usr/bin/env bash
# Sandbox test for issue.sh: runs it against a temp tree with fake certbot,
# nginx, dig, systemctl and install, and checks that it never takes a name
# another nginx server answers (exact, wildcard or regex server_name), never
# reuses a certificate it did not issue, issues only with a matching TXT
# ownership record for the site the domain is still bound to, skips taken-down
# sites, and cleans up after disconnected domains. The www / bare partner
# goes on the certificate as a redirect only when it passes the same checks.
# An address family's server (simple-host-family-*) counts as another server
# unless it belongs to the same account as the domain.
#
#   bash deploy/domain-certs/issue_test.sh
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT

mkdir -p "$T/bin" "$T/state/requests" "$T/state/ready" "$T/state/owned" "$T/state/failed" \
  "$T/sites/domains" "$T/sites/by-id/u/s" "$T/sites/by-id/u/down" "$T/avail" "$T/enabled" "$T/confd" "$T/live" "$T/webroot" "$T/txt" "$T/noa" "$T/lefail"
S=$T/sites/domains
cp "$here/vhost.conf.template" "$T/template"
cat > "$T/conf" <<EOF
STATE=$T/state
SITES=$S
WEBROOT=$T/webroot
TEMPLATE=$T/template
AVAILABLE=$T/avail
ENABLED=$T/enabled
LE_LIVE=$T/live
LOCK=$T/lock
IP=203.0.113.7
PER_RUN=50
FAMILY_SITES=$T/sites/families
EOF
cat > "$T/bin/certbot" <<EOF
#!/usr/bin/env bash
echo "certbot \$*" >> "$T/calls"
if [ "\$1" = certonly ]; then
  while [ \$# -gt 0 ]; do
    [ "\$1" = --cert-name ] && c=\$2
    [ "\$1" = -d ] && [ -e "$T/lefail/\$2" ] && { echo "Detail: no challenge for \$2" >&2; exit 1; }
    shift
  done
  mkdir -p "$T/live/\$c" && touch "$T/live/\$c/fullchain.pem" "$T/live/\$c/privkey.pem"
fi
EOF
# dig: every A record points here (except names in $T/noa); TXT answers come
# from $T/txt/<domain>.
cat > "$T/bin/dig" <<EOF
#!/usr/bin/env bash
t=; n=
for a in "\$@"; do case \$a in +short) ;; A|AAAA|TXT) t=\$a ;; *) n=\$a ;; esac; done
case \$t in
  A) [ -e "$T/noa/\$n" ] || echo 203.0.113.7 ;;
  TXT) f="$T/txt/\${n#_simple-host.}"; [ -f "\$f" ] && sed 's/.*/"&"/' "\$f" ;;
esac
exit 0
EOF
# nginx: -T dumps every enabled file plus conf.d the way nginx does.
cat > "$T/bin/nginx" <<EOF
#!/usr/bin/env bash
if [ "\${1:-}" = -T ]; then
  [ -f "$T/nginx-T-fails" ] && exit 1
  echo "nginx: the configuration file /etc/nginx/nginx.conf syntax is ok" >&2
  for f in "$T/confd"/* "$T/enabled"/*; do
    [ -e "\$f" ] || continue
    echo "# configuration file \$f:"
    cat "\$f"; echo
  done
fi
exit 0
EOF
printf '#!/bin/sh\necho "systemctl $*" >> %s/calls\n' "$T" > "$T/bin/systemctl"
# install without -o/-g (not root here).
cat > "$T/bin/install" <<'EOF'
#!/usr/bin/env bash
a=(); while [ $# -gt 0 ]; do case $1 in -o|-g) shift 2 ;; *) a+=("$1"); shift ;; esac; done
exec /usr/bin/install "${a[@]}"
EOF
chmod +x "$T/bin/"*
touch "$T/calls"

TOK=sh-0123456789abcdef0123456789abcdef
OTHER=sh-ffffffffffffffffffffffffffffffff
touch "$T/sites/by-id/u/down/suspended"

# Servers the operator wrote (names like the live box's).
printf 'server {\n  listen 443 ssl;\n  server_name vineetsriram.com\n              www.vineetsriram.com;\n}\n' > "$T/enabled/vineetsriram.com"
printf 'server { server_name ielts.vineetsriram.com; }\n' > "$T/enabled/ielts.vineetsriram.com"
printf 'server { server_name *.wild.test; }\n# server_name commented.test;\n' > "$T/enabled/wild"
printf 'server {\n    server_name ~^(?<client>[a-z0-9-]+)\\.quotes\\.example\\.com$;\n    location / { return 404; }\n}\n' > "$T/enabled/sub-quotes.example.com"
printf 'server { server_name "~^[a-z]{2,3}\\.re\\.test$"; }\n' > "$T/enabled/quoted-regex"
printf 'server { server_name .dot.test; }\nmap $ssl_server_name $x { default 1; }\n' > "$T/enabled/dot"
printf 'server { server_name shop.*; }\n' > "$T/enabled/trailing"
printf 'server { listen 80 default_server; server_name _ ""; }\n' > "$T/confd/default.conf"
# A hand lineage exists for the hand-made domain (the old takeover path),
# and one for a name no server names (a foreign certificate).
for d in vineetsriram.com foreign.test; do
  mkdir -p "$T/live/$d" && touch "$T/live/$d/fullchain.pem" "$T/live/$d/privkey.pem"
done
# Our own lineage from an earlier issue: reused.
mkdir -p "$T/live/again.test" && touch "$T/live/again.test/fullchain.pem" "$T/live/again.test/privkey.pem" "$T/state/owned/again.test"
# A server of ours written before the hand-made one appeared.
echo "old" > "$T/avail/simple-host-domain-ielts.vineetsriram.com"
ln -s "$T/avail/simple-host-domain-ielts.vineetsriram.com" "$T/enabled/simple-host-domain-ielts.vineetsriram.com"
# A ready domain of ours that a hand-made server now names.
echo "ours" > "$T/avail/simple-host-domain-late.test"
ln -s "$T/avail/simple-host-domain-late.test" "$T/enabled/simple-host-domain-late.test"
touch "$T/state/ready/late.test"
printf 'server { server_name late.test; }\n' > "$T/enabled/late.test"
# A certificate of ours whose domain was disconnected (no link, no ready).
mkdir -p "$T/live/gone2.test" && touch "$T/state/owned/gone2.test"

hand="vineetsriram.com ielts.vineetsriram.com a.wild.test acme.quotes.example.com ab.re.test dot.test x.dot.test shop.example"
issue="fresh.test sub.vineetsriram.com commented.test b.quotes.example.com.evil.test"
for d in $hand $issue again.test foreign.test notxt.test wrongtxt.test moved.test late.test kept.test oldstyle.test; do
  ln -s ../by-id/u/s "$S/$d"
done
ln -s ../by-id/u/down "$S/down.test"
for d in $hand $issue again.test foreign.test notxt.test wrongtxt.test down.test; do
  echo "$TOK" > "$T/txt/$d"
done
echo "$OTHER" > "$T/txt/wrongtxt.test"
rm -f "$T/txt/notxt.test"
echo "$TOK" > "$T/txt/moved.test"
for d in $hand $issue again.test foreign.test notxt.test wrongtxt.test; do
  printf '%s\n../by-id/u/s\n' "$TOK" > "$T/state/requests/$d"
done
printf '%s\n../by-id/u/down\n' "$TOK" > "$T/state/requests/down.test"
# Made for an earlier binding: the link now points at another site.
printf '%s\n../by-id/u/other\n' "$TOK" > "$T/state/requests/moved.test"
: > "$T/state/requests/oldstyle.test"
echo "old failure" > "$T/state/failed/gone.test"
echo "dns" > "$T/state/failed/kept.test"
touch -d '@0' "$T/state/failed/kept.test"

# www / bare partners.
printf 'server { server_name solo.test; }\n' > "$T/enabled/solo.test"
for d in pair.test www.solo.test nodns.test bogus.test grow.test own.test www.own.test lefail.test; do
  ln -s ../by-id/u/s "$S/$d"
  echo "$TOK" > "$T/txt/$d"
done
printf '%s\n../by-id/u/s\nwww.pair.test\n' "$TOK" > "$T/state/requests/pair.test"
printf '%s\n../by-id/u/s\nsolo.test\n' "$TOK" > "$T/state/requests/www.solo.test"
printf '%s\n../by-id/u/s\nwww.nodns.test\n' "$TOK" > "$T/state/requests/nodns.test"
touch "$T/noa/www.nodns.test"
printf '%s\n../by-id/u/s\nevil.example\n' "$TOK" > "$T/state/requests/bogus.test"
# Issued alone before; its partner now points here: the certificate grows.
mkdir -p "$T/live/grow.test" && touch "$T/live/grow.test/fullchain.pem" "$T/live/grow.test/privkey.pem"
echo grow.test > "$T/state/owned/grow.test"
printf '%s\n../by-id/u/s\nwww.grow.test\n' "$TOK" > "$T/state/requests/grow.test"
# own.test serves www.own.test as its partner; www.own.test now proves itself.
mkdir -p "$T/live/own.test" && touch "$T/live/own.test/fullchain.pem" "$T/live/own.test/privkey.pem"
printf 'own.test\nwww.own.test\n' > "$T/state/owned/own.test"
echo "partner www.own.test" > "$T/state/ready/own.test"
printf 'server { server_name own.test; }\nserver { server_name www.own.test; }\n' > "$T/avail/simple-host-domain-own.test"
ln -s "$T/avail/simple-host-domain-own.test" "$T/enabled/simple-host-domain-own.test"
printf '%s\n../by-id/u/s\n' "$TOK" > "$T/state/requests/www.own.test"
# A ready domain whose partner a hand-made server names now: partner comes off.
ln -s ../by-id/u/s "$S/late2.test"
echo "partner www.late2.test" > "$T/state/ready/late2.test"
printf 'server { server_name late2.test; }\nserver { server_name www.late2.test; }\n' > "$T/avail/simple-host-domain-late2.test"
ln -s "$T/avail/simple-host-domain-late2.test" "$T/enabled/simple-host-domain-late2.test"
printf 'server { server_name www.late2.test; }\n' > "$T/enabled/www.late2.test"
# The partner is bound to another account's site that has not proved it yet:
# it stays off this one's certificate.
ln -s ../by-id/u/s "$S/taken.test"
ln -s ../by-id/v/theirs "$S/www.taken.test"
echo "$TOK" > "$T/txt/taken.test"
printf '%s\n../by-id/u/s\nwww.taken.test\n' "$TOK" > "$T/state/requests/taken.test"
# Let's Encrypt refuses the partner: the chosen name is issued alone.
touch "$T/lefail/www.lefail.test"
printf '%s\n../by-id/u/s\nwww.lefail.test\n' "$TOK" > "$T/state/requests/lefail.test"

# Address families (servers written by simple-host-family-certs): a custom
# domain inside a family of the same account is not "served elsewhere"; one
# inside another account's family, or a family with no link, is.
mkdir -p "$T/sites/families"
fam() { printf 'server {\n    listen 443 ssl;\n    server_name "~^(?<sh_label>(?!xn--)[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)\\.%s$";\n}\n' "${1//./\\.}" > "$T/avail/simple-host-family-$1"; ln -s "$T/avail/simple-host-family-$1" "$T/enabled/simple-host-family-$1"; }
fam mine.test; ln -s ../by-id/u "$T/sites/families/mine.test"
fam theirs.test; ln -s ../by-id/v "$T/sites/families/theirs.test"
fam nolink.test
for d in buy.mine.test buy.theirs.test buy.nolink.test; do
  ln -s ../by-id/u/s "$S/$d"
  echo "$TOK" > "$T/txt/$d"
  printf '%s\n../by-id/u/s\n' "$TOK" > "$T/state/requests/$d"
done

# Simple Host's own zones are never custom domains, even bound and proven.
platform="simple-host.site evil.simple-host.site a.b.simple-host.site shop.simple-host.app simple-hack.app x.simple-hack.app"
for d in $platform; do
  ln -s ../by-id/u/s "$S/$d"
  echo "$TOK" > "$T/txt/$d"
  printf '%s\n../by-id/u/s\n' "$TOK" > "$T/state/requests/$d"
done

run() { PATH="$T/bin:$PATH" SIMPLE_HOST_DOMAIN_CERTS_CONF="$T/conf" bash "$here/issue.sh" > "$T/out" 2>&1 || { cat "$T/out"; echo "FAIL: issue.sh exited non-zero"; exit 1; }; }
run

fail=0
check() { if eval "$2"; then echo "  ok   $1"; else echo "  FAIL $1"; fail=1; fi; }
issued() { sed 's/$/ /' "$T/calls" | grep -q -- "certonly.* -d $1 "; }
for d in $hand; do
  check "$d: served by another server: failed, never ready, nothing of ours, no certificate" \
    "[ ! -e '$T/state/ready/$d' ] && grep -q 'already served here' '$T/state/failed/$d' && [ ! -e '$T/avail/simple-host-domain-$d' ] && [ ! -e '$T/enabled/simple-host-domain-$d' ] && ! issued $d"
done
check "hand-made files untouched" "grep -q 'www.vineetsriram.com' '$T/enabled/vineetsriram.com' && [ -f '$T/enabled/ielts.vineetsriram.com' ]"
for d in $issue; do
  check "$d: issued and served by ours" "[ -f '$T/state/ready/$d' ] && [ -L '$T/enabled/simple-host-domain-$d' ] && issued $d && [ -f '$T/state/owned/$d' ]"
done
check "template carries the take-down check" "grep -q 'domains/fresh.test/suspended' '$T/avail/simple-host-domain-fresh.test'"
check "template carries the offline check, after the take-down check" "grep -q 'domains/fresh.test/offline) { rewrite ^ /internal/offline last; }' '$T/avail/simple-host-domain-fresh.test' && [ \"\$(grep -n 'domains/fresh.test/suspended' '$T/avail/simple-host-domain-fresh.test' | cut -d: -f1)\" -lt \"\$(grep -n 'domains/fresh.test/offline' '$T/avail/simple-host-domain-fresh.test' | cut -d: -f1)\" ]"
check "template carries the passcode check, after the offline check" "grep -qF 'domains/fresh.test/passcode) { rewrite ^ /internal/passcode\$uri last; }' '$T/avail/simple-host-domain-fresh.test' && [ \"\$(grep -n 'domains/fresh.test/offline' '$T/avail/simple-host-domain-fresh.test' | cut -d: -f1)\" -lt \"\$(grep -n 'domains/fresh.test/passcode' '$T/avail/simple-host-domain-fresh.test' | cut -d: -f1)\" ]"
check "again.test: our own certificate reused, no new issue" "[ -f '$T/state/ready/again.test' ] && [ -L '$T/enabled/simple-host-domain-again.test' ] && ! issued again.test"
check "foreign.test: a certificate we did not issue is never reused" "[ ! -e '$T/state/ready/foreign.test' ] && grep -q 'already served here' '$T/state/failed/foreign.test' && [ ! -e '$T/enabled/simple-host-domain-foreign.test' ] && ! issued foreign.test"
check "notxt.test: no ownership record, no certificate" "[ ! -e '$T/state/ready/notxt.test' ] && grep -q 'ownership record' '$T/state/failed/notxt.test' && ! issued notxt.test"
check "wrongtxt.test: another site's token, no certificate" "[ ! -e '$T/state/ready/wrongtxt.test' ] && grep -q 'ownership record' '$T/state/failed/wrongtxt.test' && ! issued wrongtxt.test"
check "moved.test: request for an earlier binding left alone" "[ -f '$T/state/requests/moved.test' ] && [ ! -e '$T/state/ready/moved.test' ] && ! issued moved.test"
check "oldstyle.test: unreadable request kept for the app to rewrite" "[ -f '$T/state/requests/oldstyle.test' ] && ! issued oldstyle.test"
check "down.test: taken-down site gets no certificate" "[ ! -e '$T/state/requests/down.test' ] && [ ! -e '$T/state/ready/down.test' ] && ! issued down.test"
check "late.test: ours withdrawn once a hand-made server names it" "[ ! -e '$T/state/ready/late.test' ] && [ ! -e '$T/enabled/simple-host-domain-late.test' ] && grep -q 'already served here' '$T/state/failed/late.test'"
check "gone2.test: our certificate deleted once its binding is gone" "grep -q 'delete --non-interactive --quiet --cert-name gone2.test' '$T/calls' && [ ! -e '$T/state/owned/gone2.test' ]"
check "failure note of a disconnected domain removed" "[ ! -e '$T/state/failed/gone.test' ]"
check "failure note of a connected domain kept" "[ -e '$T/state/failed/kept.test' ]"
check "pair.test: one certificate for both names" "grep -q -- '--cert-name pair.test -d pair.test -d www.pair.test' '$T/calls' && grep -qx 'partner www.pair.test' '$T/state/ready/pair.test' && grep -qx www.pair.test '$T/state/owned/pair.test'"
check "pair.test: partner redirects to the chosen name" "grep -q 'server_name www.pair.test;' '$T/avail/simple-host-domain-pair.test' && grep -q 'return 301 https://pair.test\$request_uri' '$T/avail/simple-host-domain-pair.test' && ! grep -q '__' '$T/avail/simple-host-domain-pair.test'"
check "pair.test: take-down, offline and passcode checks on the chosen name, partner only redirects there" "grep -q 'domains/pair.test/suspended) { rewrite ^ /internal/suspended last; }' '$T/avail/simple-host-domain-pair.test' && grep -q 'domains/pair.test/offline) { rewrite ^ /internal/offline last; }' '$T/avail/simple-host-domain-pair.test' && grep -q 'domains/pair.test/passcode) { rewrite ^ /internal/passcode' '$T/avail/simple-host-domain-pair.test' && ! sed -n '/partner name answers/,\$p' '$T/avail/simple-host-domain-pair.test' | grep -qE 'try_files|alias |sites/domains|internal/'"
check "fresh.test: no partner block without a partner" "! grep -q '301 https://fresh.test' '$T/avail/simple-host-domain-fresh.test' && ! grep -q '__' '$T/avail/simple-host-domain-fresh.test'"
check "www.solo.test: partner served by another server stays off" "issued www.solo.test && ! issued solo.test && grep -q '^partner-not-set-up solo.test: .*already served here' '$T/state/ready/www.solo.test' && ! grep -q 'server_name solo.test' '$T/avail/simple-host-domain-www.solo.test'"
check "nodns.test: partner not pointed here is reported, chosen name live" "issued nodns.test && ! issued www.nodns.test && grep -q '^partner-not-set-up www.nodns.test: .*does not point' '$T/state/ready/nodns.test'"
check "bogus.test: a partner that is not www / bare is ignored" "issued bogus.test && ! grep -q evil.example '$T/calls' && [ ! -s '$T/state/ready/bogus.test' ]"
check "grow.test: certificate expanded with the partner" "grep -q -- '--cert-name grow.test --expand -d grow.test -d www.grow.test' '$T/calls' && grep -qx 'partner www.grow.test' '$T/state/ready/grow.test'"
check "www.own.test: proven on its own, comes off own.test's server" "grep -q '^partner-not-set-up www.own.test: .*site of its own' '$T/state/ready/own.test' && ! grep -q 'server_name www.own.test' '$T/avail/simple-host-domain-own.test' && issued www.own.test && [ -f '$T/state/ready/www.own.test' ]"
check "taken.test: partner bound to another site stays off" "issued taken.test && ! issued www.taken.test && grep -q '^partner-not-set-up www.taken.test: .*connected to another site' '$T/state/ready/taken.test' && ! grep -q 'server_name www.taken.test' '$T/avail/simple-host-domain-taken.test'"
check "lefail.test: partner refused, chosen name issued alone" "grep -q '^partner-not-set-up www.lefail.test: .*refused' '$T/state/ready/lefail.test' && grep -qx lefail.test '$T/state/owned/lefail.test' && ! grep -qx www.lefail.test '$T/state/owned/lefail.test'"
check "late2.test: partner named by a hand-made server comes off ours" "grep -q '^partner-not-set-up www.late2.test: .*already served here' '$T/state/ready/late2.test' && ! grep -q 'server_name www.late2.test' '$T/avail/simple-host-domain-late2.test' && grep -q 'server_name late2.test' '$T/avail/simple-host-domain-late2.test'"
for d in $platform; do
  check "$d: a platform zone is refused (request dropped, no certificate, no server)" \
    "[ ! -e '$T/state/requests/$d' ] && [ ! -e '$T/state/ready/$d' ] && [ ! -e '$T/avail/simple-host-domain-$d' ] && ! issued $d"
done
check "buy.mine.test: inside a family of the same account: issued" "issued buy.mine.test && [ -f '$T/state/ready/buy.mine.test' ] && [ -L '$T/enabled/simple-host-domain-buy.mine.test' ]"
check "buy.theirs.test: inside another account's family: already served" "! issued buy.theirs.test && [ ! -e '$T/state/ready/buy.theirs.test' ] && grep -q 'already served here' '$T/state/failed/buy.theirs.test'"
check "buy.nolink.test: inside a family with no link: already served" "! issued buy.nolink.test && [ ! -e '$T/state/ready/buy.nolink.test' ] && grep -q 'already served here' '$T/state/failed/buy.nolink.test'"
check "family servers untouched" "[ -L '$T/enabled/simple-host-family-mine.test' ] && [ -L '$T/enabled/simple-host-family-theirs.test' ]"
check "no nginx configuration in the output" "! grep -q 'server_name' '$T/out'"

# Without a readable nginx configuration nothing is issued.
touch "$T/nginx-T-fails"
ln -s ../by-id/u/s "$S/blind.test"
echo "$TOK" > "$T/txt/blind.test"
printf '%s\n../by-id/u/s\n' "$TOK" > "$T/state/requests/blind.test"
run
check "blind.test: nginx -T failing issues nothing and keeps the request" "! issued blind.test && [ -f '$T/state/requests/blind.test' ] && [ ! -e '$T/state/ready/blind.test' ]"

[ "$fail" = 0 ] || { echo "--- issue.sh output"; cat "$T/out"; exit 1; }
echo "issue.sh sandbox: ok"
