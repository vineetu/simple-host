#!/usr/bin/env bash
# The 1-Click image pins the same installer as https://simple-host.app/setup:
# release, commit and sha256 agree between setup.js (checked against the tag by
# TestSetupHelperInstallerRelease), template.json and first-login.sh's defaults.
set -euo pipefail
cd "$(dirname "$0")/.."
root=../../..
js=$root/internal/handler/static/setup/setup.js
fl=files/opt/simple-host-setup/first-login.sh
fail=0
check() {
  local name=$1 want got_t got_f
  want=$(sed -n "s/^ *var INSTALLER_${name} = '\([^']*\)';.*/\1/p" "$js")
  got_t=$(python3 -c "import json,sys;print(json.load(open('template.json'))['variables']['installer_$(echo "$name" | tr '[:upper:]' '[:lower:]')'])")
  got_f=$(sed -n "s/^INSTALLER_${name}=\(.*\)$/\1/p" "$fl")
  if [ -z "$want" ]; then echo "FAIL: setup.js has no INSTALLER_$name"; fail=1; return; fi
  if [ "$got_t" != "$want" ]; then echo "FAIL: template.json installer_${name,,} is $got_t, setup.js pins $want"; fail=1; fi
  if [ "$got_f" != "$want" ]; then echo "FAIL: first-login.sh INSTALLER_$name is $got_f, setup.js pins $want"; fail=1; fi
}
check RELEASE; check COMMIT; check SHA256
app=$(python3 -c "import json;print(json.load(open('template.json'))['variables']['application_version'])")
rel=$(python3 -c "import json;print(json.load(open('template.json'))['variables']['installer_release'])")
if [ "v$app" != "$rel" ]; then echo "FAIL: application_version $app is not installer_release $rel"; fail=1; fi
if [ "$fail" -eq 0 ]; then echo "ok: the 1-Click pins the setup page's installer ($rel)"; fi
exit "$fail"
