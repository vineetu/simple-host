# CLAUDE.md

This file gives agents (Claude Code, Codex CLI, Cursor, etc.) the context they need to work in this repo. The user-facing overview is in `README.md`; this file is the agent crib sheet — architecture, conventions, gotchas.

## What this is

One Go binary that hosts static websites. Users register, upload tarballs via API, files are extracted to a versioned directory tree on disk. The same binary serves the API, the static sites, the admin browser UI, and the bundled Website Deploy plugin (as `/skills.zip`, `/plugin.zip`, `/install.sh`).

There is no separate object store, CDN, build pipeline, or microservices. Everything is `cmd/server/main.go` plus a data directory plus a Postgres.

**Live instance:** https://simple-host.ideaflow.page

## Layout

- `cmd/server/main.go` — wires config, opens Postgres, creates `DiskStorage`, mounts every handler onto a single `http.ServeMux`, runs the HTTP server with graceful shutdown. All routing decisions live here.
- `internal/config` — env-driven config. `DB_DSN` and `ADMIN_API_KEY` are required; everything else has a sensible default.
- `internal/auth` — `X-API-Key` middleware. The hardcoded `ADMIN_API_KEY` short-circuits to a synthetic admin user with `ID="admin"`; everything else looks up the `users` table.
- `internal/db` — raw `database/sql` against Postgres. Schema is in `README.md` (no migrations framework).
- `internal/storage/disk.go` — versioned site layout on disk: `<DATA_DIR>/<site>/v<n>/` with a `current` directory holding the live version.
- `internal/tarball` — extracts and validates uploaded archives (path traversal guards, size limits, extension denylist for source-script types only).
- `internal/handler/*.go` — one file per feature area. Treat them as separate apps that happen to share a `*sql.DB` and the same `mux`.
- `internal/handler/static/` — embedded HTML/CSS/fonts for the landing page, admin UI, docs page, OpenAPI spec.
- `internal/handler/notice_middleware.go` — wraps responses on agent-facing routes with a `_notice` field when the caller's `X-Skill-Version` header is missing or stale. NOT applied to state endpoints, static serving, or skill downloads.
- `simple-host-website/` — the Website Deploy plugin. Embedded into the Go binary via `embed.go` so `/skills.zip`, `/plugin.zip`, `/install.sh` work out of the box.

## Architecture conventions

### Request routing

One `http.ServeMux` in `main.go`. Major prefixes:

- `/api/auth`, `/v1/me`, `/v1/sites/*` — REST surface. Mostly auth-gated. Wrapped with `noticeMW`.
- `/v1/sites/{site}/state` — per-site JSON state. **Public per-site scratch storage**, not a secure store: the only gate is an `Origin`/`Referer` check (browser pages hold no API key), which real browsers can't forge cross-site but `curl` can. Treat as readable/writable by anyone who knows the site name — no confidentiality/integrity guarantee; never put secrets in it. Abuse is bounded by a per-IP rate limit + a 1 MB cap. NOT wrapped with `noticeMW` since browser pages parse the body directly.
- `/sites/{site}/...` — public static serving. Path safety + `http.FileServer` rooted at `<DATA_DIR>/<site>/current/`.
- `/skills.zip`, `/plugin.zip`, `/install.sh`, `/skills/version` — Website Deploy bundle downloads. Public, no auth.
- `/healthz`, `/readyz` — probes.
- `/` — admin browser UI. Login with an API key, manage your sites.

### Versioning

Uploads are *append-only*. Each upload writes `<DATA_DIR>/<site>/v<n>/` and updates the `current` directory to point at it. Rollback (`PUT /v1/sites/{name}/active-version`) re-points `current` at an older `vN`. Per-version delete is not exposed; whole-site delete is (`DELETE /v1/sites/{name}`).

### Skill staleness notice

`internal/handler/notice_middleware.go` reads the embedded `plugin.json` version at boot. For routes wrapped with the middleware, if the request's `X-Skill-Version` header is missing or mismatched, the JSON response body gets a `_notice` field injected (top-level for objects, `{data:[...], _notice}` for arrays). The MCP server in `simple-host-website/mcp-server/` reads `plugin.json` at module load, sends `X-Skill-Version` on every API call, and surfaces `_notice` as a `NOTICE:` text block in the tool result so the agent can relay it to the user.

When you bump the plugin, update `simple-host-website/.claude-plugin/plugin.json`'s `version`. The middleware reads it from the embedded FS — no separate constant to update.

## Things that look weird but are intentional

- **Admin user has no DB row.** `auth.Middleware` constructs a fake `&db.User{ID:"admin"}` when the request key matches `ADMIN_API_KEY`. Don't write code that joins `users.id = "admin"`.
- **No `ADMIN_API_KEY` default in source.** Required env var. The previous default (`simple-host-admin-key-2026`) was removed when this repo went public so the source doesn't ship a known key.
- **No tests in the repo.** `go test ./...` is a no-op. Verify behavior end-to-end against a running server.
- **HTML/JS is embedded, not served from disk.** Editing `internal/handler/static/index.html` requires a binary rebuild for the change to take effect.
- **The Website Deploy plugin's MCP server reads its version from `../../.claude-plugin/plugin.json` at runtime.** Don't restructure the install layout without updating that path.
- **Single ServeMux, no router library.** Adding a new endpoint = adding a `mux.Handle` line in `main.go` or a handler's `Register` method.

## Local dev

```bash
# Postgres (one-time)
docker run -d --name simple-host-postgres -p 5432:5432 \
  -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost \
  postgres:16-alpine

# Run
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' \
DATA_DIR=./data/sites \
ADMIN_API_KEY=$(openssl rand -hex 32) \
go run ./cmd/server
```

Use `$ADMIN_API_KEY` in `X-API-Key` to act as admin; or hit `/v1/auth` from the admin UI to create a real user via magic-link (needs `RESEND_API_KEY` set).

## Build for prod

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./simple-host ./cmd/server
```

That's a single self-contained binary. Ship it to wherever, set the env vars, run it. The Website Deploy plugin (with MCP server + skills) is embedded; users download it from your `/skills.zip` or `/install.sh`.
