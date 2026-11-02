#!/usr/bin/env bash
# Renders the chart to plain manifests for a cluster installed without Helm
# (kubectl apply, Argo CD, a change-reviewed YAML bundle).
#
#   deploy/kubernetes/render.sh            writes manifests/simple-hack.yaml
#   deploy/kubernetes/render.sh --check    fails when that file is stale
#
# The rendered bundle carries no secrets: it expects a Secret named
# simple-hack-secrets (manifests/secret.example.yaml shows the keys) and the
# rest of the values from examples/values-example.yaml, which is why the
# example keeps placeholder hostnames. Re-run after any chart change; `make
# check` runs the --check form through deploy/kubernetes/test-chart.sh.
set -euo pipefail
cd "$(dirname "$0")"
out=manifests/simple-hack.yaml
render() {
  helm template simple-hack ./simple-hack --namespace simple-hack \
    -f examples/values-example.yaml \
    --set secrets.existingSecret=simple-hack-secrets \
    --set database.dsn= \
    --set signIn.oidc.clientSecret= \
    | sed '/^# Source: /d'
}
if [ "${1:-}" = --check ]; then
  if ! diff -u <(tail -n +4 "$out") <(render) >/dev/null; then
    echo "deploy/kubernetes/manifests/simple-hack.yaml is stale: run deploy/kubernetes/render.sh" >&2
    exit 1
  fi
  echo "rendered manifests are current"
  exit 0
fi
{
  echo "# Rendered by deploy/kubernetes/render.sh from deploy/kubernetes/simple-hack with"
  echo "# examples/values-example.yaml. Edit the values and re-render, or edit this file;"
  echo "# the Secret simple-hack-secrets is yours to create first (secret.example.yaml)."
  render
} > "$out"
echo "wrote $out"
