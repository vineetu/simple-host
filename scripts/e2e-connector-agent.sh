#!/usr/bin/env bash
# Connector-only agent proof: an AI with nothing but the Simple Host connector builds a
# shop against the browser fixture, then scripts/e2e-visitor-records.mjs --mode site checks
# what it built (visitor sign-in, own-read add-only storage, no /v1/auth, no key in a page)
# in a real browser with two customers and the owner.
#
#   bash scripts/e2e-connector-agent.sh claude [model]     # Claude Code, MCP over HTTP with X-API-Key
#   bash scripts/e2e-connector-agent.sh codex  [model]     # Codex, through a local header-adding proxy
#
# Needs: DB_DSN (throwaway Postgres with db/schema.sql), Playwright (NODE_PATH), CHROMIUM.
# Output: /tmp/vr-agent-<agent>.{log,out}, /tmp/vr-agent-<agent>-site.log
set -euo pipefail
cd "$(dirname "$0")/.."
agent=${1:-claude}; model=${2:-}
: "${DB_DSN:?set DB_DSN}"
fixture=/tmp/vr-agent-fixture.json
rm -f "$fixture"
export DB_DSN
sudo --preserve-env=DB_DSN systemd-run --quiet --wait --pipe --collect -p MemoryMax=3G -p WorkingDirectory="$PWD" --uid="$(id -u)" --gid="$(id -g)" \
  -E HOME="$HOME" -E PATH="$PATH" -E GOCACHE="$(go env GOCACHE)" -E GOMODCACHE="$(go env GOMODCACHE)" -E DB_DSN \
  -E VISITOR_RECORDS_FIXTURE="$fixture" -E VISITOR_RECORDS_TIMEOUT_MIN=60 \
  bash -c 'go test ./internal/handler/ -count=1 -run TestServeVisitorRecordsBrowser -v' > "/tmp/vr-agent-$agent-fixture.log" 2>&1 &
for _ in $(seq 1 180); do [ -s "$fixture" ] && break; sleep 2; done
[ -s "$fixture" ] || { echo "fixture did not start"; exit 1; }
url=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["url"])' "$fixture")
OWNER_KEY=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["owner_key"])' "$fixture")
export OWNER_KEY   # the throwaway fixture key travels in the environment, never on a command line
prompt='Build a small spice shop where customers add items to a cart, sign in at checkout, place an order, and see their own orders. Call the site spice-shop. You have only the Simple Host connector; do not ask me anything, make reasonable choices, and finish by telling me the address.'

if [ "$agent" = claude ]; then
  cfg=/tmp/vr-agent-claude-mcp.json
  python3 - "$cfg" "$url" <<'PY'
import json, os, sys
json.dump({"mcpServers": {"simple-host": {"type": "http", "url": sys.argv[2] + "/mcp", "headers": {"X-API-Key": os.environ["OWNER_KEY"]}}}}, open(sys.argv[1], "w"))
PY
  chmod 600 "$cfg"
  work=$(mktemp -d /tmp/vr-agent-claude-work.XXXX)
  (cd "$work" && claude -p --model "${model:-sonnet}" --tools "" --mcp-config "$cfg" --strict-mcp-config \
     --dangerously-skip-permissions --output-format text "$prompt" </dev/null > "/tmp/vr-agent-$agent.out" 2> "/tmp/vr-agent-$agent.log") || true
elif [ "$agent" = codex ]; then
  # Codex speaks HTTP MCP with a bearer token only; a local proxy adds the owner's key.
  proxy_port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
  python3 - "$proxy_port" "$url" > /tmp/vr-agent-codex-proxy.log 2>&1 <<'PY' &
import http.server, os, sys, urllib.request
port, upstream, key = int(sys.argv[1]), sys.argv[2], os.environ["OWNER_KEY"]
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
        req = urllib.request.Request(upstream + self.path, data=body, method="POST")
        for h in ("Content-Type", "Accept", "MCP-Protocol-Version", "Mcp-Method", "Mcp-Name"):
            if self.headers.get(h): req.add_header(h, self.headers[h])
        req.add_header("X-API-Key", key)
        try:
            with urllib.request.urlopen(req) as r:
                data, status, ct = r.read(), r.status, r.headers.get("Content-Type", "application/json")
        except urllib.error.HTTPError as e:
            data, status, ct = e.read(), e.code, e.headers.get("Content-Type", "application/json")
        self.send_response(status); self.send_header("Content-Type", ct); self.send_header("Content-Length", str(len(data))); self.end_headers(); self.wfile.write(data)
    def do_GET(self): self.send_response(405); self.send_header("Content-Length", "0"); self.end_headers()
    def log_message(self, *a): pass
http.server.ThreadingHTTPServer(("127.0.0.1", port), H).serve_forever()
PY
  proxy_pid=$!
  sleep 1
  work=$(mktemp -d /tmp/vr-agent-codex-work.XXXX)
  # Codex approves non-read-only MCP tools only with the bypass flag; the work dir is empty
  # and the prompt tells it to use only the connector.
  (cd "$work" && codex exec --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox -C "$work" ${model:+-m "$model"} \
     -c "mcp_servers.simple_host.url=\"http://127.0.0.1:$proxy_port/mcp\"" \
     -o "/tmp/vr-agent-$agent.out" "$prompt" </dev/null > "/tmp/vr-agent-$agent.log" 2>&1) || true
  kill "$proxy_pid" 2>/dev/null || true
else
  echo "unknown agent $agent"; exit 2
fi
echo "agent finished; checking the site in a browser"
SITE_DUMP_DIR="/tmp/vr-agent-$agent-site" PLAYWRIGHT_DIR=${PLAYWRIGHT_DIR:-/tmp/tsx/node_modules} CHROMIUM=${CHROMIUM:-/home/ubuntu/.cache/ms-playwright/chromium-1234/chrome-linux/chrome} \
  timeout 900 node scripts/e2e-visitor-records.mjs "$fixture" --mode site --site spice-shop > "/tmp/vr-agent-$agent-site.log" 2>&1 || status=$?
tail -c 2500 "/tmp/vr-agent-$agent-site.log"
curl -s -X POST "$url/_fixture/stop" -o /dev/null || true
exit "${status:-0}"
