#!/usr/bin/env bash
# MCPJam protocol conformance, OAuth conformance, host compatibility and the
# Claude directory policy lane, against the fixture from fixture.sh. Free: no
# MCPJam account and no eval credits.
set -uo pipefail
FIX=${FIX:-/tmp/mcpjam-fixture}
CLI=${MCPJAM_CLI:-@mcpjam/cli@6.0.0}
here=$(cd "$(dirname "$0")" && pwd)
URL=https://localhost:18443/mcp
export NODE_EXTRA_CA_CERTS="$FIX/cert.pem" BASE=https://localhost:18443 SH_TEST_KEY
SH_TEST_KEY=$(cat "$FIX/user.key")
jam() { npx -y "$CLI" --no-telemetry "$@"; }
failed=()
run() {
  local name=$1; shift
  echo "::group::$name"
  if "$@"; then echo "PASS $name"; else echo "FAIL $name"; failed+=("$name"); fi
  echo "::endgroup::"
}

# Checks that need a feature the server does not offer (prompts, resources,
# logging, input requests) cannot run; only a failed check fails here. The
# localhost DNS-rebinding checks are for servers that run on a person's own
# machine; this one is remote and serves many hosts, and only the fixture is
# on localhost.
protocol() {
  local v=$1 out="$FIX/protocol-$1.json"
  jam --format json protocol conformance --url "$URL" --protocol-version "$v" \
    --header "X-API-Key: $SH_TEST_KEY" --fixture-tool who_am_i --fixture-tool list_sites >"$out" 2>/dev/null
  python3 - "$out" <<'PY'
import json, re, sys
# The CLI's client logs a line or two to stdout ahead of the report.
text = open(sys.argv[1]).read()
d = json.loads(text[re.search(r"^\{", text, re.M).start():])
remote = {"localhost-host-rebinding-rejected", "localhost-host-valid-accepted"}
by = {}
for c in d["checks"]:
    status = "not-applicable" if c["id"] in remote else c["status"]
    by.setdefault(status, []).append(c["id"])
    if status == "failed":
        print("failed:", c["id"], (c.get("error") or {}).get("message", ""))
print({k: len(v) for k, v in by.items()})
sys.exit(1 if by.get("failed") else 0)
PY
}
for v in 2025-11-25 2026-07-28; do
  run "protocol conformance $v" protocol "$v"
done

# The full sign-in flow per revision, then the token is used to list and call
# a tool. The negative checks run on their own: one of them redeems a code
# twice, and the server rightly revokes that grant's tokens.
for v in 2025-06-18 2025-11-25 2026-07-28; do
  run "oauth $v" python3 "$here/oauth_consent.py" npx -y "$CLI" --no-telemetry --format human oauth conformance \
    --url "$URL" --protocol-version "$v" --registration dcr --auth-mode interactive --print-url \
    --verify-tools --verify-call-tool who_am_i
done
run "oauth negative checks" python3 "$here/oauth_consent.py" npx -y "$CLI" --no-telemetry --format human oauth conformance \
  --url "$URL" --protocol-version 2025-11-25 --registration dcr --auth-mode interactive --print-url --conformance-checks

compat() {
  jam --format json compat --offline --url "$URL" --header "X-API-Key: $SH_TEST_KEY" >"$FIX/compat.json" || return 1
  python3 - "$FIX/compat.json" <<'PY'
import json, sys
hosts = json.load(open(sys.argv[1]))["hosts"]
bad = [h["hostId"] + ": " + h["verdict"] for h in hosts if h["verdict"] != "works"]
print(", ".join(h["hostId"] for h in hosts), "->", "all work" if not bad else "; ".join(bad))
sys.exit(1 if bad else 0)
PY
}
run "host compatibility" compat

# The submission lanes need a listing profile and screenshots, so only
# violated requirements fail here.
directory() {
  jam --format json readiness check claude "$URL" --header "X-API-Key: $SH_TEST_KEY" >"$FIX/readiness.json"
  python3 - "$FIX/readiness.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
bad = [f["id"] + ": " + f["title"] for f in d["findings"] if f["status"] == "violated"]
print("\n".join(bad) or "no violated Claude directory requirements")
sys.exit(1 if bad else 0)
PY
}
run "claude directory policy" directory

if [ ${#failed[@]} -gt 0 ]; then
  printf 'failed: %s\n' "${failed[@]}"
  exit 1
fi
echo "all MCPJam checks passed"
