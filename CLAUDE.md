# CLAUDE.md

This file gives agents (Claude Code, Codex CLI, Cursor, etc.) the context they need to work in this repo. The user-facing overview is in `README.md`; this file is the agent crib sheet — architecture, conventions, gotchas.

## What this is

One Go binary that hosts static websites. Users register, upload tarballs via API, files are extracted to a versioned directory tree on disk. The same binary serves the API, the static sites, the admin browser UI, and the bundled Website Deploy plugin (as `/skills.zip`, `/plugin.zip`, `/install.sh`).

There is no separate object store, CDN, build pipeline, or microservices. Everything is `cmd/server/main.go` plus a data directory plus a Postgres.

**Live instance:** https://simple-host.app

## Layout

- `cmd/server/main.go` — wires config, opens Postgres, creates `DiskStorage`, mounts every handler onto a single `http.ServeMux`, runs the HTTP server with graceful shutdown. All routing decisions live here.
- `internal/config` — env-driven config. `DB_DSN` and `ADMIN_API_KEY` are required; everything else has a sensible default.
- `internal/auth` — `X-API-Key` middleware. Every key, including `ADMIN_API_KEY`, resolves to a real `users` row; the admin row is created or updated at boot by `db.EnsureAdminUser` so it has a genuine UUID and can own sites. Middleware reads **only** `X-API-Key` — never the site session cookie. A visitor who signs in on a hosted page (Google or emailed code, custom domain only) is the same kind of `users` row; the `__Host-sh_vsess` cookie is used by `visitorWriteOK` for state/collections writes and by the site-scoped `/me` read.
- `internal/db` — raw `database/sql` against Postgres. Schema is in `db/schema.sql` (no migrations framework).
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
- `/v1/sites/{site}/state` and `/collections/{coll}` — the per-site backend. Reads are public (GETs are Origin/Referer-gated, which a browser page satisfies by itself and `curl` can set by hand). Writes (`PUT`/`PATCH` state, `POST` collections) go through `visitorWriteOK`: on the shared content host they are open — no session, no key (every site there is one origin, so a sign-in could never be private to one site; the data is a public scratchpad anyone can change). On a site's own custom domain they need any account's `X-API-Key` (unknown key → 401 `invalid_api_key`) or a visitor session cookie + `X-SH-CSRF: 1` (401 `visitor_auth_required`, 403 `csrf_required`). A key is accepted anywhere except the shared host of a bound site. Once a site has a custom domain bound it lives only there (owner decision 2026-09-06): its shared-host page URL `https://sites.simple-host.app/{handle}/{site}/...` answers 302 to `https://<domain>/...` (same path and query — 302 on purpose, so disconnecting stops it at once and nothing stays cached; the owner accepts that links saved to the domain are stranded), and its shared-host API takes NO writes: they answer 401 `use_custom_domain` (with `domain`) even with a valid `X-API-Key`; reads there stay public, and `/me` there returns `code: use_custom_domain` plus the domain so `auth.js` can link the visitor to the same page on the domain. Agents write through the apex `https://simple-host.app/v1/...` (key) or the domain's own `/v1/` (key or session) — the skill already uses the apex. The redirect is a `domain-redirect` marker file in the site directory (written on bind, removed on disconnect) that the content-host nginx tests, routing to `GET /internal/domain-redirect/{handle}/{site}/...`. Sites and their data are public to anyone with the link. NOT wrapped with `noticeMW`.
- `GET /v1/sites/{site}/me` (and the user-scoped twin) reports the current visitor session without extending it. Hosted `/auth.js` exposes `window.SH` for sign-in (Google or emailed 6-digit code; `/visitor/auth`, `/visitor/auth/verify`), a status box, state and collections. Email codes are bound to where they were requested (dashboard vs one site).
- `/sites/{site}/...` — public static serving. Path safety + `http.FileServer` rooted at `<DATA_DIR>/<site>/current/`.
- `/skills.zip`, `/plugin.zip`, `/install.sh`, `/skills/version` — Website Deploy bundle downloads. Public, no auth.
- `/healthz`, `/readyz` — probes.
- `/v1/generate`, `/v1/generate/status` — build-with-AI. Answers with a job id; the client polls. Talks to the local Grok sidecar (CLIProxy) only — no fallback provider; if it is down the feature fails honestly.
- `/v1/transcribe`, `/v1/transcribe/ticket`, `/v1/transcribe/stream` — voice input for that chat. Live captions run over the WebSocket; the POST endpoint is the transcribe-on-stop fallback. Speech never leaves this host.
- `/admin` — instance-wide view of accounts and their sites. Admin only; everyone else gets a 404, including on the API behind it.
- `/` — admin browser UI. Login with an API key, manage your sites.

### Versioning

Uploads are *append-only*. Each upload writes `<DATA_DIR>/<site>/v<n>/` and updates the `current` directory to point at it. Rollback (`PUT /v1/sites/{name}/active-version`) re-points `current` at an older `vN`. Per-version delete is not exposed; whole-site delete is (`DELETE /v1/sites/{name}`).

### Skill staleness notice

`internal/handler/notice_middleware.go` reads the embedded `plugin.json` version at boot. For routes wrapped with the middleware, if the request's `X-Skill-Version` header is missing or mismatched, the JSON response body gets a `_notice` field injected (top-level for objects, `{data:[...], _notice}` for arrays). The MCP server in `simple-host-website/mcp-server/` reads `plugin.json` at module load, sends `X-Skill-Version` on every API call, and surfaces `_notice` as a `NOTICE:` text block in the tool result so the agent can relay it to the user.

When you bump the plugin, update `simple-host-website/.claude-plugin/plugin.json`'s `version`. The middleware reads it from the embedded FS — no separate constant to update.

## Things that look weird but are intentional

- **The admin is a real `users` row.** `db.EnsureAdminUser` (called from `main.go` at boot) upserts a row whose API key is `ADMIN_API_KEY`, so the admin identity has a genuine UUID and can own sites. The older synthetic `ID:"admin"` shortcut is gone — it violated the `sites.user_id` foreign key — so don't reintroduce it.
- **A site session is not owner power.** Custom domains reverse-proxy `/v1/` to this binary, so `__Host-sh_vsess` is sent to `/v1/sites/{name}`. Middleware ignores cookies; do not teach it to accept them.
- **No `ADMIN_API_KEY` default in source.** Required env var. The previous default (`simple-host-admin-key-2026`) was removed when this repo went public so the source doesn't ship a known key.
- **No tests in the repo.** `go test ./...` is a no-op. Verify behavior end-to-end against a running server.
- **HTML/JS is embedded, not served from disk.** Editing `internal/handler/static/index.html` requires a binary rebuild for the change to take effect.
- **The Website Deploy plugin's MCP server reads its version from `../../.claude-plugin/plugin.json` at runtime.** Don't restructure the install layout without updating that path.
- **Single ServeMux, no router library.** Adding a new endpoint = adding a `mux.Handle` line in `main.go` or a handler's `Register` method.

## Keeping the API and its docs in sync

The API surface is described in several places that drift independently:
`internal/handler/static/openapi.yaml` (**the source of truth**) + `openapi.json`
(regenerate with `python3 -c "import yaml,json;json.dump(yaml.safe_load(open('internal/handler/static/openapi.yaml')),open('internal/handler/static/openapi.json','w'),indent=2)"`),
`internal/handler/static/llms.txt` (curated LLM guide), and the skills in
`simple-host-website/skills/`. When you add or change a `/v1` route, update
openapi.yaml, then llms.txt/skills if it's a user-facing capability.

**Run `bash scripts/check-docs-sync.sh` after any route change** (and before
deploying). It hard-fails if a registered `/v1` route isn't in openapi.yaml (or
vice versa) and warns when a major capability is missing from llms.txt/the skills.
This is what catches "endpoint shipped but undocumented" — exactly how
collections were once missing from llms.txt.

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

Product decisions and their reasons live in `INTENT.md`; read it before changing the write model, sign-in, or the skills.
