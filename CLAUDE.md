# CLAUDE.md

Working rules for any agent (Claude Code, Codex, Cursor, ...) changing this repo. Simple Host
is one Go binary that hosts static sites with a small per-site JSON backend; it is live at
https://simple-host.app and runs in production for real users.

## Before you change anything

1. Read `INTENT.md` (what it is for, non-goals, decisions with dates),
   then `FEATURES.md` (what it does today), then `ARCHITECTURE.md` (where it lives).
2. `git fetch` and fast-forward first. Two sessions have overwritten each other in production.
3. Map every surface a change touches before coding: routes, `openapi.yaml`, `llms.txt`, the
   skills, the dashboard and the showcase page, the MCP tools, the Claude and OpenAI plugins.
4. If a request conflicts with `INTENT.md`, say so in one line and ask. When the owner rules on
   something, add it to INTENT's decisions with the date and reason.

## In the same commit as a feature change

- Update `FEATURES.md` and add a line to `CHANGELOG.md` (newest first, plain language).
- Route changed: update `internal/handler/static/openapi.yaml` (the contract), regenerate
  `openapi.json` with
  `python3 -c "import yaml,json;json.dump(yaml.safe_load(open('internal/handler/static/openapi.yaml')),open('internal/handler/static/openapi.json','w'),indent=2)"`,
  then `llms.txt` and the skills if it is user-facing. `scripts/check-docs-sync.sh` fails on drift.
- Skills changed: bump `version` in `simple-host-website/.claude-plugin/plugin.json` (the
  stale-skill notice reads it from the embedded file), then run
  `bash scripts/sync-claude-plugin.sh`. Never hand-edit `plugins/simple-host/skills/`.
- Schema changed: edit `db/schema.sql`, add a file under `db/migrations/`, apply it by hand
  before deploying (the binary refuses to start if a column it reads is missing).
- Parity with Simple Host Enterprise (`PARITY.md`, identical in both repos): any security fix in
  one repo is checked against the other the same day (note it in PARITY.md). A new or changed
  feature updates PARITY.md in the same commit. `scripts/check-parity.sh` (in
  `check-docs-sync.sh`) fails if a `## N.` section of FEATURES.md has no PARITY.md row.
- Design docs in `docs/designs/` carry a status line at the top; update it when you ship or drop
  the thing. Finished plans go to `docs/history/`.

## Build, test, deploy (as actually done on this box)

The box is aarch64 with little RAM, so builds run under a memory cap.

```
make check
```
(gofmt, build, vet, `go test ./...` and the `scripts/check-*.sh` checks; `check-fresh-install.sh`
needs a local postgres superuser.) Quick loop: `go test ./internal/handler/` (wrap heavy runs in the same `sudo systemd-run ... MemoryMax=2G` as the build).

Build, back up, install, restart (one line, from the repo root):

```
sudo systemd-run --quiet --wait --pipe --collect -p MemoryMax=2G -p WorkingDirectory=$PWD --uid=$(id -u) --gid=$(id -g) -E HOME=$HOME -E PATH=$PATH -E GOCACHE=$(go env GOCACHE) -E GOMODCACHE=$(go env GOMODCACHE) bash -c 'CGO_ENABLED=0 go build -o /tmp/simple-host.new ./cmd/server' && TS=$(date +%Y%m%d-%H%M%S) && sudo cp -p /usr/local/bin/simple-host /usr/local/bin/simple-host.bak-$TS && sudo install -m 755 -o root -g root /tmp/simple-host.new /usr/local/bin/simple-host && sudo systemctl restart simple-host
```

Before deploying, check what is live: the repo is not the binary
(`strings /usr/local/bin/simple-host | grep <feature>`). Rollback: install the `.bak-<ts>`
and restart.

**Verify from the client, not the server.** "Deployed" is not "visible". After a restart:

- `curl -s -o /dev/null -w '%{http_code}\n' https://simple-host.app/healthz` → 200
- the page or route you changed, with `curl`, as a visitor would hit it
- a site host (`https://madurai-idly.vineetu.simple-host.app/`), a person host
  (`https://vineetu.simple-host.app/`) and a legacy link
  (`curl -sI https://sites.simple-host.app/vineetu/madurai-idly/` → 302 to `https://madurai-idly.vineetu.simple-host.app/`)
- the neighbours on the same nginx and binary: `https://simple-hack.app/`,
  `https://vineetsriram.com/`, `https://sf-fog.today/` → 200
- `journalctl -u simple-host -n 50` for boot errors

Never report branch work as fixed; only what a client can see is shipped.

## Hard rules

- **Never print secrets.** Not `/etc/simple-host.env`, not API keys, tokens or DSNs. Change one
  key with `sed` after a backup; never `cat` the file.
- **Never open `/etc/nginx/sites-enabled/sites-content-host`** (it holds a plaintext sidecar
  token). Change it only through a script that never prints it, with the owner's approval, after
  a dated `.bak`, then `sudo nginx -t && sudo systemctl reload nginx`.
- **Do not alter the `simplehost` Postgres role** (password, grants, ownership).
- **Commands handed to the owner are one line**, copy-pasteable, with self-filling
  substitutions instead of placeholders.
- **Visitor IPs never go to a third party.** Geolocation is local (`internal/geoip`); nothing
  is injected into hosted pages.
- **AI create is the Grok sidecar only.** No fallback provider, no metered API keys.
- **Hosted pages never hold an API key.** Middleware reads only `X-API-Key` or a connector
  token; never teach it to accept the visitor cookie.
- **Any change to auth, sessions, OAuth, the connector, permissions or private collections
  gets a security review before it ships.**
- Public contact is `support@simple-host.app`, never a personal address.

## Things that look odd but are intentional

- The admin is a real `users` row, upserted at boot from `ADMIN_API_KEY` (no default in source).
  Do not reintroduce a synthetic admin id.
- HTML, CSS and JS are hand-written and embedded; there is no frontend build. A page edit needs
  a rebuild.
- One `http.ServeMux`, no router library. New endpoint = a `mux.Handle` line in a handler's
  `Register` (grep `mux.Handle` to find any route).
- `site_view_daily` and `site_visitor_daily` are never written and must never be dropped.
- `PERSON_HOSTS` and `SITE_HOSTS` default to `off` so event and self-hosted instances keep the
  path model. simple-host.app runs both as `canonical`.

### Skill staleness notice

`notice_middleware.go` reads the embedded `plugin.json` version at boot. On wrapped routes, a
missing or older `X-Skill-Version` header adds a `_notice` field to the JSON response; the MCP
server surfaces it to the agent. State endpoints, static serving and downloads are not wrapped.

## Local dev

```
docker run -d --name simple-host-postgres -p 5432:5432 -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost postgres:16-alpine
```
```
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' DATA_DIR=./data/sites SITE_DOMAIN=localhost ADMIN_API_KEY=$(openssl rand -hex 32) go run ./cmd/server
```
Apply `db/schema.sql` to the fresh database first. Without `SITE_DOMAIN` the binary starts in
setup mode.
