#!/usr/bin/env bash
# Exercise primary failures, identical challenge/name/key arguments, credential
# handling, cleanup, renewal settings and hourly Signal throttling off-box.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
T=$(mktemp -d)
trap 'rm -rf "$T"' EXIT
mkdir -p "$T/bin" "$T/live"
export T LE_LIVE="$T/live" CERT_ALERT_STATE="$T/alerts" CERT_ALERT_ENV="$T/alert.env" ZEROSSL_EAB_FILE="$T/eab.json"
export PATH="$T/bin:$PATH"
printf 'SIGNAL_ACCOUNT=fixture\nSIGNAL_TO=fixture\n' > "$CERT_ALERT_ENV"
printf '{"success":true,"eab_kid":"fixture-kid","eab_hmac_key":"fixture-key"}\n' > "$ZEROSSL_EAB_FILE"
chmod 0600 "$ZEROSSL_EAB_FILE"
cat > "$T/bin/certbot" <<'STUB'
#!/usr/bin/env python3
import os, sys, pathlib, stat
args = sys.argv[1:]
t = pathlib.Path(os.environ['T'])
with (t/'argv').open('a') as f:
    f.write(repr(args) + '\n')
if '--config' not in args:
    if os.environ['PRIMARY'] == 'ok': sys.exit(0)
    print(os.environ['PRIMARY'], file=sys.stdout if os.environ.get('STDOUT_ERROR') else sys.stderr)
    sys.exit(1)
p = pathlib.Path(args[args.index('--config')+1])
assert stat.S_IMODE(p.stat().st_mode) == 0o600
assert 'eab-kid = fixture-kid' in p.read_text()
assert 'eab-hmac-key = fixture-key' in p.read_text()
assert 'email = support@simple-host.app' in p.read_text()
assert 'server = https://acme.zerossl.com/v2/DV90' in p.read_text()
original = args[:args.index('--config')]
import ast
primary = ast.literal_eval((t/'argv').read_text().splitlines()[-2])
assert original == primary, 'issuance args changed'
(t/'temp-path').write_text(str(p.parent))
logs = pathlib.Path(args[args.index('--logs-dir')+1])
logs.mkdir()
(logs/'private.log').write_text('fixture-key')
if os.environ.get('FALLBACK_FAIL'):
    print('fixture-key', file=sys.stderr)
    sys.exit(1)
name = args[args.index('--cert-name')+1]
renewal = t/'renewal'
renewal.mkdir(exist_ok=True)
(renewal/(name+'.conf')).write_text('[renewalparams]\nserver = https://acme.zerossl.com/v2/DV90\naccount = fixture-account\nlogs_dir = '+str(logs)+'\n')
STUB
cat > "$T/bin/curl" <<'STUB'
#!/usr/bin/env bash
cat >> "$T/messages"
echo >> "$T/messages"
printf '{"jsonrpc":"2.0","id":1,"result":{}}\n'
STUB
chmod +x "$T/bin/"*
# shellcheck source=deploy/cert-issuers/fallback.sh
. "$here/fallback.sh"
export CERT_FALLBACK_CA=zerossl PRIMARY=ok
run() { cert_issue "$1" "$2" certonly --non-interactive --agree-tos --quiet --key-type ecdsa --cert-name "$2" "${@:3}" > "$T/output" 2>&1; }
run simple-host.app alice.simple-host.app --manual --preferred-challenges dns -d '*.alice.simple-host.app'
[ "$(wc -l < "$T/argv")" = 1 ]
for error in 'too many certificates' 'rateLimited' 'urn:ietf:params:acme:error:rateLimited'; do
  export PRIMARY="$error"
  run simple-host.app alice.simple-host.app --manual --preferred-challenges dns --manual-auth-hook fixture-auth --manual-cleanup-hook fixture-cleanup --deploy-hook fixture-deploy -d '*.alice.simple-host.app'
  [ ! -e "$(cat "$T/temp-path")" ]
done
[ "$(wc -l < "$T/messages")" = 1 ]
grep -q 'issued alice.simple-host.app via ZeroSSL' "$T/messages"
grep -q '^logs_dir = /var/log/letsencrypt$' "$T/renewal/alice.simple-host.app.conf"
if grep -q 'fixture-key\|fixture-kid' "$T/argv" "$T/output" "$T/renewal/"*; then exit 1; fi
# A different domain has its own throttle; HTTP webroot and both SANs survive.
export STDOUT_ERROR=1
run brand.example brand.example --webroot -w fixture-root --deploy-hook 'systemctl reload nginx' -d brand.example -d www.brand.example
[ "$(wc -l < "$T/messages")" = 2 ]
# Same domain after an hour sends another note.
printf '%s\n' "$(( $(date +%s) - 3601 ))" > "$T/alerts/simple-host.app.sent"
run simple-host.app bob.simple-host.app --manual --preferred-challenges dns -d '*.bob.simple-host.app'
[ "$(wc -l < "$T/messages")" = 3 ]
export CERT_FALLBACK_CA=none
before=$(wc -l < "$T/argv")
if run no.example no.example --webroot -w fixture-root -d no.example; then exit 1; fi
[ "$(wc -l < "$T/argv")" = "$((before + 1))" ]
export CERT_FALLBACK_CA=zerossl PRIMARY='Detail: unauthorized'
if run no.example no.example --webroot -w fixture-root -d no.example; then exit 1; fi
[ "$(wc -l < "$T/argv")" = "$((before + 2))" ]
export PRIMARY=rateLimited FALLBACK_FAIL=1
if run failed.example failed.example --webroot -w fixture-root -d failed.example; then exit 1; fi
[ ! -e "$(cat "$T/temp-path")" ]
if grep -q fixture-key "$T/output"; then exit 1; fi
grep -q 'ZeroSSL issuance failed' "$T/messages"
unset FALLBACK_FAIL
export ZEROSSL_EAB_FILE="$T/missing"
if run missing.example missing.example --webroot -w fixture-root -d missing.example; then exit 1; fi
grep -q 'credentials unavailable' "$T/messages"
echo 'cert-fallback sandbox: primary success, three rate-limit forms, DNS/HTTP SANs, none, unrelated failure, fallback failure, credentials, cleanup and alerts: ok'
