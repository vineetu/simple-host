#!/usr/bin/env bash
# Publication checks and HTTP/server fixtures, deliberately outside make check.
# --mcpjam uses the fixture prepared by the separate MCPJam CI workflow.
set -uo pipefail
cd "$(dirname "$0")/.."

if [ "${1:-}" = --mcpjam ] && [ "$#" -eq 1 ]; then
  command -v npx >/dev/null || { echo "FAIL: MCPJam requires npx and npm network access"; exit 1; }
  [ -r "${FIX:-/tmp/mcpjam-fixture}/user.key" ] || { echo "FAIL: prepare the local MCPJam server with scripts/mcpjam/fixture.sh first"; exit 1; }
  bash scripts/mcpjam/checks.sh
  exit "$?"
fi
[ "$#" -eq 0 ] || { echo "usage: $0 [--mcpjam]"; exit 1; }
fail=0

echo "== the setup page pins the Enterprise Helm chart =="
js=internal/handler/static/setup/setup.js
ent_version=$(sed -n "s/^ *var ENT_CHART_VERSION = '\([0-9.]*\)';.*/\1/p" "$js")
release_tmp=$(mktemp -d)
chart="$release_tmp/Chart.yaml"
trap 'rm -rf "$release_tmp"' EXIT
if [ -z "$ent_version" ]; then
  echo "  FAIL: $js has no ENT_CHART_VERSION"; fail=1
elif [ -n "${SH_ENTERPRISE_REPO:-}" ]; then
  if ! cp "$SH_ENTERPRISE_REPO/deploy/helm/simple-host-enterprise/Chart.yaml" "$chart"; then
    echo "  FAIL: cannot read Enterprise chart from SH_ENTERPRISE_REPO"; fail=1
  fi
else
  url="https://raw.githubusercontent.com/vineetu/simple-host-enterprise/chart-v${ent_version}/deploy/helm/simple-host-enterprise/Chart.yaml"
  if ! status=$(curl -sSL --connect-timeout 10 --max-time 30 -o "$chart" -w '%{http_code}' "$url"); then
    echo "  FAIL: Enterprise chart publication could not be checked: network unavailable or request failed ($url)"; fail=1
  elif [ "$status" != 200 ]; then
    echo "  FAIL: published Enterprise chart unavailable: HTTP $status ($url)"; fail=1
  fi
fi
if [ "$fail" -eq 0 ]; then
  chart_version=$(sed -n 's/^version: //p' "$chart")
  if [ "$chart_version" != "$ent_version" ]; then
    echo "  FAIL: setup pins chart $ent_version, checked chart has '$chart_version' (publish the chart first)"; fail=1
  else
    echo "  ok Enterprise chart $ent_version (${SH_ENTERPRISE_REPO:-published chart tag})"
  fi
fi

# settings_docs.py otherwise probes ../simple-host-enterprise implicitly.
# Supply a known source so this check is independent of the runner's layout.
echo "== settings docs and Enterprise setup settings =="
enterprise_source=${ENTERPRISE_REPO:-}
if [ -z "$enterprise_source" ]; then
  enterprise_source="$release_tmp/enterprise"
  mkdir -p "$enterprise_source/docs/advanced"
  settings_url=https://raw.githubusercontent.com/vineetu/simple-host-enterprise/main/docs/advanced/settings.json
  if ! status=$(curl -sSL --connect-timeout 10 --max-time 30 -o "$enterprise_source/docs/advanced/settings.json" -w '%{http_code}' "$settings_url"); then
    echo "  FAIL: Enterprise settings could not be checked: network unavailable or request failed ($settings_url)"; fail=1
    enterprise_source=""
  elif [ "$status" != 200 ]; then
    echo "  FAIL: published Enterprise settings unavailable: HTTP $status ($settings_url)"; fail=1
    enterprise_source=""
  fi
fi
if [ -n "$enterprise_source" ]; then
  if [ ! -r "$enterprise_source/docs/advanced/settings.json" ]; then
    echo "  FAIL: ENTERPRISE_REPO has no readable docs/advanced/settings.json"; fail=1
  else
    ENTERPRISE_REPO="$enterprise_source" python3 scripts/settings_docs.py --check || fail=1
  fi
fi

# Only compare another checkout when explicitly supplied; there is no default
# path to a developer's machine or implicit second checkout in CI.
if [ -n "${PARITY_OTHER:-}" ]; then
  echo "== parity with the other repository =="
  python3 scripts/parity_compare.py PARITY.md "$PARITY_OTHER" || fail=1
fi

# Existing fixtures may exit zero when prerequisites disappear. Release checks
# must distinguish that from a completed check.
run_fixture() {
  local output result=0
  output=$(bash "$1" 2>&1) || result=1
  printf '%s\n' "$output"
  if [ "$result" -ne 0 ] || grep -qiE '^[[:space:]]*(skip(ped)?([ :—]|$)|== .*skip(ped)?|nginx fixture: skip)' <<<"$output"; then
    echo "  FAIL: $1 failed or skipped; release check incomplete"
    fail=1
  fi
}

echo "== HTTP fixtures (throwaway local servers) =="
if [ ! -x /usr/sbin/nginx ] || ! command -v curl >/dev/null || ! command -v openssl >/dev/null || ! command -v python3 >/dev/null; then
  echo "  FAIL: HTTP fixtures require /usr/sbin/nginx, curl, openssl and python3; checks did not run"; fail=1
else
  for test in deploy/family-certs/nginx_fixture_test.sh \
              deploy/prod/nginx-internal-lock_test.sh \
              deploy/prod/nginx-sso-log_test.sh \
              deploy/prod/nginx-site-base-domain_test.sh; do
    run_fixture "$test"
  done
fi

echo "== Caddy HTTP fixture (Docker images from registry) =="
if ! command -v docker >/dev/null || ! docker info >/dev/null 2>&1; then
  echo "  FAIL: Caddy fixture requires a running Docker daemon; check did not run"; fail=1
else
  images_ready=1
  for image in "${CADDY_IMAGE:-caddy:2-alpine}" python:3-alpine alpine; do
    if ! docker pull "$image"; then
      echo "  FAIL: cannot fetch $image: check network and registry availability"; images_ready=0; fail=1
    fi
  done
  if [ "$images_ready" -eq 1 ]; then
    run_fixture deploy/compose/Caddyfile_test.sh
  fi
fi

if [ "$fail" -ne 0 ]; then
  echo "RELEASE CHECKS FAILED — see failed checks or unavailable prerequisites above."
  exit 1
fi
echo "release checks ok ✓"
