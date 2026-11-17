#!/usr/bin/env bash
# Chart checks that need no cluster: helm lint, the validation guards fail
# with their sentences, the rendered resources have the shape the runbook
# promises, and manifests/simple-hack.yaml is current.
#
#   bash deploy/kubernetes/test-chart.sh
set -uo pipefail
cd "$(dirname "$0")"
command -v helm >/dev/null || { echo "FAIL: helm is required for chart checks"; exit 1; }
fail=0
ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; fail=1; }

base=(--set host=hack.example.com --set database.dsn=postgres://x --set signIn.oidc.issuer=https://id.example.com --set signIn.oidc.clientId=a --set signIn.oidc.clientSecret=b)

for product in simple-host simple-hack; do
chart=./$product
echo "== $product: helm lint =="
if helm lint "$chart" "${base[@]}" >/dev/null 2>&1; then ok "lint"; else bad "lint"; helm lint "$chart" "${base[@]}"; fi

echo "== validation guards =="
expect_fail() {
  local want=$1; shift
  local out
  local status=0
  out=$(helm template t "$chart" "$@" 2>&1) || status=$?
  if [[ $status != 0 ]] && grep -q "$want" <<<"$out"; then ok "$want"; else bad "expected '$want'"; echo "$out" | tail -2; fi
}
expect_fail "host is required"
expect_fail "database.dsn is required" --set host=hack.example.com
expect_fail "not a lower-case hostname" --set host=Hack.Example --set database.dsn=x
expect_fail "company sign-in needs all" --set host=hack.example.com --set database.dsn=x --set signIn.oidc.issuer=https://a
expect_fail "nobody could sign in" --set host=hack.example.com --set database.dsn=x --set signIn.password.enabled=false
expect_fail "must be https" "${base[@]}" --set signIn.oidc.issuer=http://id.example.com
expect_fail "allowedEmailDomains is required" "${base[@]}" --set signIn.oidc.issuer=https://accounts.google.com
expect_fail "needs company or password sign-in" --set signIn.password.enabled=false --set host=hack.example.com --set database.dsn=x --set signIn.email.resendApiKey=k --set signIn.email.from=a@b.c --set signIn.email.enabled=off
if [[ $product == simple-hack ]]; then
  expect_fail "is not an event slug" "${base[@]}" --set 'ingress.events={Spring Hack}'
fi
expect_fail "replicas must be 1" "${base[@]}" --set replicas=2
expect_fail "strategy.type must be Recreate" "${base[@]}" --set strategy.type=RollingUpdate
expect_fail "loses every site" "${base[@]}" --set persistence.enabled=false
expect_fail "not both" "${base[@]}" --set database.existingSecret=pg

echo "== rendered shape =="
rendered=$(helm template hack "$chart" --namespace hack "${base[@]}" --set 'ingress.events={spring-hack}' --set ingress.catchAll=true --set networkPolicy.enabled=true 2>&1) || { bad "render"; echo "$rendered" | tail -3; }
has() { if grep -qE -- "$2" <<<"$rendered"; then ok "$1"; else bad "$1"; fi; }
has "one replica, Recreate"            '^  replicas: 1$'
has "strategy Recreate"                'type: Recreate'
has "non-root 65532"                   'runAsUser: 65532'
has "read-only root"                   'readOnlyRootFilesystem: true'
has "no capabilities"                  'drop: \["ALL"\]'
has "seccomp"                          'type: RuntimeDefault'
has "no service account token"         'automountServiceAccountToken: false'
has "startup probe on /healthz"        'path: /healthz'
has "readiness probe on /readyz"       'path: /readyz'
has "data volume at /data"             'mountPath: /data$'
has "tmp emptyDir"                     'mountPath: /tmp$'
has "PVC ReadWriteOnce"                '- ReadWriteOnce'
has "PVC kept on uninstall"            'helm.sh/resource-policy: keep'
has "explicit product initialization"  'args: \["migrate", "-init", "-record-product"\]'
has "MIGRATE_ON_START"                 'MIGRATE_ON_START: "true"'
has "server command" "command: \\[\"/usr/local/bin/$product\"\\]"
has "PERSON_HOSTS canonical" 'PERSON_HOSTS: canonical'
has "SITE_HOSTS canonical" 'SITE_HOSTS: canonical'
if [[ $product == simple-hack ]]; then
  has "EVENTS=hosted" 'EVENTS: hosted'
else
  if grep -qE 'EVENTS:|TEAM_SITE_FLAT_HOSTS:' <<<"$rendered"; then bad "Host has product settings"; else ok "Host has no product settings"; fi
fi
has "trusted proxies"                  'TRUSTED_PROXY_CIDRS: "10.0.0.0/8'
has "ingress apex"                     'host: "hack.example.com"'
has "ingress wildcard"                 'host: "\*.hack.example.com"'
if [[ $product == simple-hack ]]; then
has "ingress event wildcard"           'host: "\*.spring-hack.hack.example.com"'
has "ingress catch-all"                '^    - http:$'
fi
has "network policy"                   'kind: NetworkPolicy'
has "OIDC issuer"                      'OIDC_ISSUER: "https://id.example.com"'
has "OIDC secret in Secret"            'OIDC_CLIENT_SECRET: "b"'
has "passcode key generated"           'PASSCODE_ENC_KEY: "[A-Za-z0-9+/]{43}="'
if grep -qE 'cpu:' <<<"$(sed -n '/limits:/,/requests:/p' <<<"$rendered")"; then bad "no CPU limit"; else ok "no CPU limit"; fi

path=$(helm template hack "$chart" "${base[@]}" 2>&1)
if grep -q 'spring-hack' <<<"$path"; then bad "no event wildcard by default"; else ok "no event wildcard by default"; fi
if grep -q 'kind: NetworkPolicy' <<<"$path"; then bad "network policy off by default"; else ok "network policy off by default"; fi
if [[ $product == simple-hack ]]; then
if grep -q 'TEAM_SITE_FLAT_HOSTS: "false"' <<<"$path"; then ok "flat hosts off by default"; else bad "flat hosts off by default"; fi

fi

echo "== migrateOnStart=false =="
manual=$(helm template hack "$chart" "${base[@]}" --set migrateOnStart=false 2>&1)
if grep -q 'initContainers:' <<<"$manual"; then bad "migration init container disabled"; else ok "migration init container disabled"; fi
if grep -q 'MIGRATE_ON_START: "false"' <<<"$manual"; then ok "startup migrations disabled"; else bad "startup migrations disabled"; fi

if [[ $product == simple-hack ]]; then
echo "== teamSites.flatHosts =="
flat=$(helm template hack "$chart" --namespace hack "${base[@]}" --set 'ingress.events={spring-hack}' --set ingress.catchAll=true --set teamSites.flatHosts=true 2>&1) || { bad "flat render"; echo "$flat" | tail -3; }
hasFlat() { if grep -qE -- "$2" <<<"$flat"; then ok "$1"; else bad "$1"; fi; }
hasFlat "TEAM_SITE_FLAT_HOSTS on"       'TEAM_SITE_FLAT_HOSTS: "true"'
hasFlat "ingress apex still present"   'host: "hack.example.com"'
hasFlat "ingress wildcard still present" 'host: "\*.hack.example.com"'
hasFlat "TLS still covers the wildcard"  '\*.hack.example.com'
if grep -q 'spring-hack' <<<"$flat"; then bad "no per-event ingress rule or SAN under flatHosts, even with ingress.events and catchAll set"; else ok "no per-event ingress rule or SAN under flatHosts"; fi
if grep -q '^    - http:$' <<<"$flat"; then bad "no catch-all rule under flatHosts"; else ok "no catch-all rule under flatHosts"; fi

fi

ext=$(helm template hack "$chart" --set host=hack.example.com --set secrets.existingSecret=mine --set database.existingSecret=pg --set database.existingSecretKey=uri 2>&1)
if grep -q 'kind: Secret' <<<"$ext"; then bad "existingSecret makes no Secret"; else ok "existingSecret makes no Secret"; fi
if grep -q 'key: uri' <<<"$ext"; then ok "database.existingSecret key"; else bad "database.existingSecret key"; fi

# Check metadata/image pins against the single shared source and parse the
# rendered Deployment, including both the init and serving command.
python3 - "$product" <<'CHECK' || fail=1
import subprocess, sys, yaml
from pathlib import Path
product = sys.argv[1]
pins = dict(line.split('=', 1) for line in Path('../shared/versions.env').read_text().splitlines() if line and not line.startswith('#'))
chart = yaml.safe_load(Path(product, 'Chart.yaml').read_text())
version = pins['RELEASE_VERSION' if product == 'simple-host' else 'HACK_RELEASE_VERSION']
assert chart['version'] == pins['CHART_VERSION']
assert chart['appVersion'] == version
text = subprocess.check_output(['helm', 'template', 'check', './'+product, '--set', 'host=host.example.test', '--set', 'secrets.existingSecret=test'], text=True)
docs = list(yaml.safe_load_all(text))
deploy = next(d for d in docs if d['kind'] == 'Deployment')
assert deploy['spec']['replicas'] == 1
assert deploy['spec']['strategy'] == {'type': 'Recreate'}
pod = deploy['spec']['template']['spec']
image = (pins['HOST_IMAGE']+':'+version if product == 'simple-host'
         else pins['HACK_BASE_IMAGE']+':hack-'+version)
for container in pod['containers'] + pod['initContainers']:
    assert container['image'] == image
    assert container['command'] == ['/usr/local/bin/'+product]
assert pod['initContainers'][0]['args'] == ['migrate', '-init', '-record-product']
assert pod['volumes'][0]['persistentVolumeClaim']['claimName'] == 'check-data'
empty_tag = subprocess.check_output(['helm', 'template', 'check', './'+product,
    '--set', 'host=host.example.test', '--set', 'secrets.existingSecret=test',
    '--set-string', 'image.tag='], text=True)
deploy = next(d for d in yaml.safe_load_all(empty_tag) if d['kind'] == 'Deployment')
pod = deploy['spec']['template']['spec']
assert pod['initContainers'][0]['image'] == image, 'empty-tag migration image'
assert pod['containers'][0]['image'] == image, 'empty-tag serving image'
print('  ok   empty-tag migration and serving images')
if product == 'simple-host':
    site_hosts = subprocess.check_output(['helm', 'template', 'check', './'+product,
        '--set', 'host=host.example.test', '--set', 'secrets.existingSecret=test',
        '--set', 'ingress.extraHosts={*.builder.host.example.test}'], text=True)
    ingress = next(d for d in yaml.safe_load_all(site_hosts) if d['kind'] == 'Ingress')
    assert any(r['host'] == '*.builder.host.example.test' for r in ingress['spec']['rules'])
    assert '*.builder.host.example.test' in ingress['spec']['tls'][0]['hosts']
external = subprocess.check_output(['helm', 'template', 'check', './'+product, '--set', 'host=host.example.test', '--set', 'secrets.existingSecret=test', '--set', 'persistence.existingClaim=kept-data'], text=True)
docs = list(yaml.safe_load_all(external))
assert not any(d['kind'] == 'PersistentVolumeClaim' for d in docs)
deploy = next(d for d in docs if d['kind'] == 'Deployment')
assert deploy['spec']['template']['spec']['volumes'][0]['persistentVolumeClaim']['claimName'] == 'kept-data'
print('  ok   shared pins, commands and existing claim')

# Only configured secret inputs belong in the rollout checksum: rendering the
# Secret itself also generates random keys when lookup has no live cluster.
def secret_render(settings=(), existing=False):
    args = ['helm', 'template', 'check', './'+product, '--set', 'host=host.example.test',
            '--set', 'database.dsn=postgres://fixture',
            '--set', 'signIn.oidc.issuer=https://id.example.test',
            '--set', 'signIn.oidc.clientId=fixture', '--set', 'signIn.oidc.clientSecret=fixture',
            '--set', 'signIn.email.from=fixture@example.test']
    if existing:
        args += ['--set', 'secrets.existingSecret=test']
    for setting in settings:
        args += ['--set-string', setting]
    docs = list(yaml.safe_load_all(subprocess.check_output(args, text=True)))
    template = next(d for d in docs if d['kind'] == 'Deployment')['spec']['template']
    secret = next((d for d in docs if d['kind'] == 'Secret'), None)
    return template, secret

template, secret = secret_render()
repeated, fresh_secret = secret_render()
assert secret['stringData']['PASSCODE_ENC_KEY'] != fresh_secret['stringData']['PASSCODE_ENC_KEY']
assert template == repeated, 'generated keys must not cause a rollout on every render'
annotations = template['metadata']['annotations']
assert len(annotations['checksum/secret']) == 64
for setting, name in [
    ('adminApiKey=rotated-fixture', 'ADMIN_API_KEY'),
    ('database.dsn=postgres://rotated-fixture', 'DB_DSN'),
    ('signIn.oidc.clientSecret=rotated-fixture', 'OIDC_CLIENT_SECRET'),
    ('signIn.email.resendApiKey=rotated-fixture', 'RESEND_API_KEY'),
    ('extraSecrets.FIXTURE_SECRET=rotated-fixture', 'FIXTURE_SECRET'),
]:
    changed, changed_secret = secret_render([setting])
    assert changed_secret['stringData'][name] != secret['stringData'].get(name)
    assert changed['metadata']['annotations']['checksum/config'] == annotations['checksum/config'], name
    assert changed['metadata']['annotations']['checksum/secret'] != annotations['checksum/secret'], name
    assert changed != template, 'secret-only upgrade must replace pod: ' + name
    assert changed == secret_render([setting])[0], 'unchanged explicit secret must be stable: ' + name
external_template, external_secret = secret_render(existing=True)
assert external_secret is None
assert 'checksum/secret' not in external_template['metadata']['annotations']
print('  ok   secret-only upgrades restart pod; unchanged inputs and generated keys keep template stable')
CHECK
done

echo "== rendered manifests current =="
if ./render.sh --check >/dev/null 2>&1; then ok "manifests/simple-hack.yaml"; else bad "manifests/simple-hack.yaml is stale (run deploy/kubernetes/render.sh)"; fi

# Existing database-backed initialization rehearsal (TEST_PG_URL only).
python3 ../install/product_init_test.py chart || fail=1

echo
[ "$fail" -eq 0 ] && echo "chart ok ✓" || { echo "CHART CHECK FAILED"; exit 1; }
