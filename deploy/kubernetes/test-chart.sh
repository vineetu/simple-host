#!/usr/bin/env bash
# Chart checks that need no cluster: helm lint, the validation guards fail
# with their sentences, the rendered resources have the shape the runbook
# promises, and manifests/simple-hack.yaml is current.
#
#   bash deploy/kubernetes/test-chart.sh
set -u
cd "$(dirname "$0")"
command -v helm >/dev/null || { echo "helm is not installed; skipping chart checks"; exit 0; }
chart=./simple-hack
fail=0
ok()   { printf '  ok   %s\n' "$1"; }
bad()  { printf '  FAIL %s\n' "$1"; fail=1; }

base=(--set host=hack.example.com --set database.dsn=postgres://x --set signIn.oidc.issuer=https://id.example.com --set signIn.oidc.clientId=a --set signIn.oidc.clientSecret=b)

echo "== helm lint =="
if helm lint "$chart" "${base[@]}" >/dev/null 2>&1; then ok "lint"; else bad "lint"; helm lint "$chart" "${base[@]}"; fi

echo "== validation guards =="
expect_fail() {
  local want=$1; shift
  local out
  out=$(helm template t "$chart" "$@" 2>&1)
  if grep -q "$want" <<<"$out"; then ok "$want"; else bad "expected '$want'"; echo "$out" | tail -2; fi
}
expect_fail "host is required"
expect_fail "database.dsn is required" --set host=hack.example.com
expect_fail "not a lower-case hostname" --set host=Hack.Example --set database.dsn=x
expect_fail "company sign-in needs all" --set host=hack.example.com --set database.dsn=x --set signIn.oidc.issuer=https://a
expect_fail "nobody could sign in" --set host=hack.example.com --set database.dsn=x --set signIn.password.enabled=false
expect_fail "must be https" "${base[@]}" --set signIn.oidc.issuer=http://id.example.com
expect_fail "allowedEmailDomains is required" "${base[@]}" --set signIn.oidc.issuer=https://accounts.google.com
expect_fail "needs company or password sign-in" --set signIn.password.enabled=false --set host=hack.example.com --set database.dsn=x --set signIn.email.resendApiKey=k --set signIn.email.from=a@b.c --set signIn.email.enabled=off
expect_fail "is not an event slug" "${base[@]}" --set 'ingress.events={Spring Hack}'
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
has "MIGRATE_ON_START"                 'MIGRATE_ON_START: "true"'
has "EVENTS=hosted"                    'EVENTS: hosted'
has "SITE_HOSTS canonical"             'SITE_HOSTS: canonical'
has "trusted proxies"                  'TRUSTED_PROXY_CIDRS: "10.0.0.0/8'
has "ingress apex"                     'host: "hack.example.com"'
has "ingress wildcard"                 'host: "\*.hack.example.com"'
has "ingress event wildcard"           'host: "\*.spring-hack.hack.example.com"'
has "ingress catch-all"                '^    - http:$'
has "network policy"                   'kind: NetworkPolicy'
has "OIDC issuer"                      'OIDC_ISSUER: "https://id.example.com"'
has "OIDC secret in Secret"            'OIDC_CLIENT_SECRET: "b"'
has "passcode key generated"           'PASSCODE_ENC_KEY: "[A-Za-z0-9+/]{43}="'
if grep -qE 'cpu:' <<<"$(sed -n '/limits:/,/requests:/p' <<<"$rendered")"; then bad "no CPU limit"; else ok "no CPU limit"; fi

path=$(helm template hack "$chart" "${base[@]}" 2>&1)
if grep -q 'spring-hack' <<<"$path"; then bad "no event wildcard by default"; else ok "no event wildcard by default"; fi
if grep -q 'kind: NetworkPolicy' <<<"$path"; then bad "network policy off by default"; else ok "network policy off by default"; fi

ext=$(helm template hack "$chart" --set host=hack.example.com --set secrets.existingSecret=mine --set database.existingSecret=pg --set database.existingSecretKey=uri 2>&1)
if grep -q 'kind: Secret' <<<"$ext"; then bad "existingSecret makes no Secret"; else ok "existingSecret makes no Secret"; fi
if grep -q 'key: uri' <<<"$ext"; then ok "database.existingSecret key"; else bad "database.existingSecret key"; fi

echo "== rendered manifests current =="
if ./render.sh --check >/dev/null 2>&1; then ok "manifests/simple-hack.yaml"; else bad "manifests/simple-hack.yaml is stale (run deploy/kubernetes/render.sh)"; fi

echo
[ "$fail" -eq 0 ] && echo "chart ok ✓" || { echo "CHART CHECK FAILED"; exit 1; }
