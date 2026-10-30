#!/usr/bin/env bash
# MCPJam protocol conformance, OAuth conformance, host compatibility and the
# Claude directory policy lane, against the fixture from fixture.sh. Free: no
# MCPJam account and no eval credits.
set -uo pipefail
FIX=${FIX:-/tmp/mcpjam-fixture}
CLI=${MCPJAM_CLI:-@mcpjam/cli@6.0.0}
here=$(cd "$(dirname "$0")" && pwd)
BASE=${BASE:-https://localhost:${TLS_PORT:-18443}}
URL="$BASE/mcp"
export NODE_EXTRA_CA_CERTS="$FIX/cert.pem" BASE SH_TEST_KEY
SH_TEST_KEY=$(cat "$FIX/user.key")
jam() { npx -y "$CLI" --no-telemetry "$@"; }
failed=()
run() {
  local name=$1; shift
  echo "::group::$name"
  if "$@"; then echo "PASS $name"; else echo "FAIL $name"; failed+=("$name"); fi
  echo "::endgroup::"
}

# Checks that need a feature the server does not offer (prompts to name,
# logging, input requests) cannot run; any other check that cannot
# run fails here. The localhost DNS-rebinding checks are for servers that run
# on a person's own machine; this one is remote and serves many hosts, and
# only the fixture is on localhost.
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
unoffered = {"modern-name-header-mismatch", "modern-undeclared-capability-error",
             "modern-logs-require-log-level", "modern-log-level-filtering"}
by = {}
for c in d["checks"]:
    status = c["status"]
    if c["id"] in remote:
        status = "not-applicable"
    elif c.get("skipReason") == "could-not-run":
        status = "unoffered" if c["id"] in unoffered else "failed"
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
# Apps are optional: these hosts retain the unchanged tool text.
# The picker hides itself when the host does not advertise serverTools.
# Site preview needs nested frames and selects the link text when clipboard
# permission is refused; so does the passcode screen. The passcode screen's
# update-model-context note is optional and skipped where a host lacks it.
# My sites always uses tiles and needs no frame exception.
def known_limit(h):
    return (h["verdict"] == "degraded" and h.get("findings")
            and all(f.get("code") == "widget_text_fallback"
                    or (f.get("code") == "capability_unsupported"
                        and (f.get("capability") == "serverTools"
                             or (f.get("capability") == "cspFrameDomains"
                                 and bool(f.get("tools"))
                                 and set(f["tools"]) == {"get_site"})
                             or (f.get("capability") == "sandboxPermissions"
                                 and bool(f.get("tools"))
                                 and set(f["tools"]) <= {"get_site", "get_site_passcode", "set_site_passcode"})
                             or f.get("capability") == "updateModelContext"))
                    for f in h["findings"]))
bad = [h["hostId"] + ": " + h["verdict"] for h in hosts
       if h["verdict"] != "works" and not known_limit(h)]
print("documented host limits:", ", ".join(h["hostId"] for h in hosts if known_limit(h)))
print(", ".join(h["hostId"] for h in hosts), "->", "supported or documented limitation" if not bad else "; ".join(bad))
sys.exit(1 if bad else 0)
PY
}
run "host compatibility" compat

apps() {
  jam --format json apps conformance --url "$URL" --header "X-API-Key: $SH_TEST_KEY" --reporter json-summary >"$FIX/apps-conformance.json"
}
run "MCP Apps conformance" apps

# The submission lanes need a listing profile and screenshots, and the
# intrusive probes and RFC 8707 check need a completed sign-in (the OAuth runs
# above cover that). Anything else not evaluated fails, as does a violation.
directory() {
  jam --format json readiness check claude "$URL" --header "X-API-Key: $SH_TEST_KEY" >"$FIX/readiness.json"
  python3 - "$FIX/readiness.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
def expected_gap(f):
    # CLI 6's directory lane does not capture UI HTML or execute tool calls.
    # Apps resource validation runs separately above; browser checks cover design.
    if f["id"].startswith("claude.apps.design.") and f.get("notEvaluatedReason") == "no widget HTML was captured, so the static design lints could not run":
        return True
    if f["id"] == "claude.apps.result-size-budget" and f.get("class") == "manual-review":
        return True
    return (f["lane"] in ("submission-artifacts", "optional-features")
            or f["id"].startswith("claude.intrusive.") or f["id"] == "claude.auth.rfc8707-resource-canonical")
# The default-off site preview still declares the Simple Host frame wildcard.
# My sites and dashboard must pass without a frame-domain exception.
def preview_frame_pattern(f):
    return (f["id"] == "claude.apps.csp-shape" and f.get("class") == "recommended"
            and bool(f.get("details", {}).get("resources"))
            and set(f["details"]["resources"]) == {"ui://simple-host/site-preview"})
for f in d["findings"]:
    if f["status"] == "violated" and preview_frame_pattern(f):
        print("accepted recommendation exception: site preview requires the Simple Host frame-domain wildcard")
bad = [f["status"] + " " + f["id"] + ": " + f["title"] for f in d["findings"]
       if (f["status"] == "violated" and not preview_frame_pattern(f)) or (f["status"] == "not-evaluated" and not expected_gap(f))]
if not any(f["id"] == "claude.tools.title-present" and f["status"] == "satisfied" for f in d["findings"]):
    bad.append("the tool listing was not inspected")
print("\n".join(bad) or "no unexpected Claude directory findings")
sys.exit(1 if bad else 0)
PY
}
run "claude directory policy" directory

if [ ${#failed[@]} -gt 0 ]; then
  printf 'failed: %s\n' "${failed[@]}"
  exit 1
fi
echo "all MCPJam checks passed"
