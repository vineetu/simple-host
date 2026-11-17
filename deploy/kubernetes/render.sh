#!/usr/bin/env bash
# Render both charts to secret-free YAML. --check detects stale bundles.
set -euo pipefail
cd "$(dirname "$0")"
[[ $# == 0 || ( $# == 1 && $1 == --check ) ]] || { echo 'usage: render.sh [--check]' >&2; exit 2; }
for product in simple-host simple-hack; do
  out=manifests/$product.yaml
  render() {
    local extra=()
    if [[ $product == simple-host ]]; then
      extra+=(--set host=host.example.com --set 'ingress.events={}' --set extraConfig.HACK_INSTANCE_BUDGET_GB=null)
    fi
    helm template "$product" "./$product" --namespace "$product" \
      -f examples/values-example.yaml \
      --set secrets.existingSecret="$product-secrets" \
      --set database.dsn= --set signIn.oidc.clientSecret= "${extra[@]}" \
      | sed '/^# Source: /d'
  }
  if [[ ${1:-} == --check ]]; then
    if ! diff -u <(tail -n +4 "$out") <(render) >/dev/null; then
      echo "$out is stale: run bash deploy/kubernetes/render.sh" >&2
      exit 1
    fi
    echo "$out is current"
  else
    {
      echo "# Rendered by deploy/kubernetes/render.sh from deploy/kubernetes/$product with"
      echo '# examples/values-example.yaml. Edit your values and re-render for your cluster;'
      echo "# create the Secret $product-secrets first (secret.example.yaml)."
      render
    } > "$out"
    echo "wrote $out"
  fi
done
