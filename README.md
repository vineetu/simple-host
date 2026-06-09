# Simple Host

A small Go server that hosts static websites. Users register with an email, upload a `.tar.gz` of HTML/CSS/JS, and the site goes live at `https://{sitename}.ideaflow.page`. Includes a cross-IDE plugin (Website Deploy) that lets agents deploy via natural language.

**Live instance:** https://simple-host.ideaflow.page

## Why

Most static-site hosts are great if you have a build pipeline, a domain to register, and the patience to thread environment variables through CI. This one isn't trying to be that. The goal is one HTTP upload, one subdomain, one MCP tool call from Claude / Codex / Cursor — for vibe-coded prototypes, demos, class projects, and personal sites.

## What it is

- **Static-file server** — uploads land in `<DATA_DIR>/<site>/v<n>/`, a `current` symlink points at the active version, files served by `http.FileServer`.
- **Versioned** — every upload is a new immutable version; rollback flips the symlink.
- **Per-site JSON state** — sites can store up to 1 MB of shared state via `GET/PUT /v1/sites/{name}/state`. Origin-checked so only your subdomain can read/write.
- **Magic-link auth** — register with an email, get an API key, that's it.
- **Admin browser UI** — at `/`, log in with your API key, see and manage your sites.
- **Skill auto-update notice** — the API responds with a `_notice` field when the caller's installed Website Deploy skill is out of date.

## Architecture

```
Client (browser or agent)
   │
   ▼  HTTPS
┌──────────────────────────────────────┐
│  reverse proxy (yours)               │
│  • wildcard *.ideaflow.page          │
│  • routes <site>.ideaflow.page → /sites/<site>/
└──────────────────────────────────────┘
   │
   ▼
┌──────────────────────────────────────┐
│  Go server (cmd/server)              │
│  • REST API (/v1/...)                │
│  • static serve (/sites/<site>/...)  │
│  • admin UI (/)                      │
│  • embedded plugin bundle (/skills.zip, /plugin.zip)
└──────────────────────────────────────┘
   │              │
   ▼              ▼
Postgres      DATA_DIR (versioned site files on disk)
```

There's no separate object store, CDN, or build pipeline. Just one binary, a Postgres, and a directory.

## Quick start (local)

```bash
# Postgres
docker run -d --name simple-host-postgres -p 5432:5432 \
  -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost \
  postgres:16-alpine

# Schema
docker exec simple-host-postgres psql -U simplehost -d simplehost -c "
CREATE TABLE users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  username TEXT UNIQUE NOT NULL,
  api_key TEXT UNIQUE NOT NULL,
  is_admin BOOLEAN DEFAULT FALSE,
  created_at TIMESTAMPTZ DEFAULT now()
);
CREATE TABLE sites (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  name TEXT UNIQUE NOT NULL,
  active_version INTEGER NOT NULL DEFAULT 1,
  site_url TEXT,
  created_at TIMESTAMPTZ DEFAULT now(),
  updated_at TIMESTAMPTZ DEFAULT now()
);
CREATE TABLE versions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id UUID REFERENCES sites(id) ON DELETE CASCADE,
  version_number INTEGER NOT NULL,
  disk_path TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'uploading',
  created_at TIMESTAMPTZ DEFAULT now(),
  UNIQUE(site_id, version_number)
);
CREATE TABLE auth_tokens (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  email TEXT NOT NULL,
  code TEXT NOT NULL,
  link_token TEXT UNIQUE NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at TIMESTAMPTZ,
  attempts INT DEFAULT 0,
  created_at TIMESTAMPTZ DEFAULT now()
);
"

# Run
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' \
DATA_DIR=./data/sites \
ADMIN_API_KEY=$(openssl rand -hex 32) \
go run ./cmd/server
```

Open http://localhost:8090. The `ADMIN_API_KEY` printed (or whichever you set) is the master key that gives admin access.

## Configuration

All via environment variables. `DB_DSN` and `ADMIN_API_KEY` are required; everything else has a default.

| Env Var | Default | Description |
|---|---|---|
| `DB_DSN` | *(required)* | Postgres DSN |
| `ADMIN_API_KEY` | *(required)* | Hardcoded super-admin key. Pick something long. No default — public source intentionally won't ship one. |
| `DATA_DIR` | `/root/workspace/general/sites` | Where site files live |
| `SITE_DOMAIN` | `ideaflow.page` | Domain suffix used to build site URLs |
| `PORT` | `8090` | HTTP listen port |
| `PUBLIC_BASE_URL` | `https://simple-host.ideaflow.page` | Used in magic-link emails |
| `DEPLOY_SCRIPT` | `/root/workspace/general/scripts/deploy-site` | Optional hook run after each upload — receives the new site name |
| `MAIL_FROM` | `Simple Host <noreply@simple-host.app>` | Magic-link sender |
| `RESEND_API_KEY` | *(unset)* | If unset, `/v1/auth` will fail; magic-link auth is via [Resend](https://resend.com) |

The defaults are tuned for the maintainer's own deploy. Override them to match yours.

## API

All authenticated routes accept `X-API-Key: <key>`. JSON in, JSON out.

| Endpoint | Method | Auth | Description |
|---|---|---|---|
| `/v1/auth` | POST | none | Send sign-in code to email |
| `/v1/auth/verify` | POST | none | Exchange code (or magic-link token) for an API key |
| `/v1/me` | GET | yes | Current user |
| `/v1/sites` | GET | yes | List your sites (admin sees all) |
| `/v1/sites/{name}` | POST | yes | Create site (raw tarball body, `Content-Type: application/gzip`) |
| `/v1/sites/{name}` | PUT | yes | Upload new version (raw tarball body) |
| `/v1/sites/{name}` | DELETE | yes | Delete site |
| `/v1/sites/{name}/versions` | GET | yes | List versions for a site |
| `/v1/sites/{name}/active-version` | PUT | yes | Roll back / forward (`{"version_number": N}`) |
| `/v1/sites/{name}/state` | GET / PUT | origin-checked | Site's per-site JSON blob (≤ 1 MB) |
| `/skills/version` | GET | none | Current Website Deploy skill version, e.g. `{"version":"0.5.0"}` |
| `/skills.zip`, `/plugin.zip`, `/install.sh` | GET | none | The Website Deploy plugin bundle, served straight from the binary |
| `/healthz`, `/readyz` | GET | none | Probes |

Live OpenAPI at `/docs.html`.

## Plugin (Website Deploy)

`simple-host-website/` is a Claude/Codex/Cursor plugin. It bundles:

- An MCP server (Node) that exposes `register`, `deploy`, `status`, `list` as agent-callable tools.
- Two skills: `website-deploy` (the deploy workflow) and `website-deploy-builder` (helping a user decide what to build that fits a static-only model).
- A `setup.sh` that detects which IDEs are installed and wires the MCP entry + skill into each.

Install for end users:

```bash
curl -fsSL https://simple-host.ideaflow.page/install.sh | sh
```

…or clone this repo and run `bash simple-host-website/setup.sh` for the full plugin (skills + MCP server).

The MCP server reads its version from the bundled `plugin.json` at module load and sends `X-Skill-Version` on every API call. If the server-side bundle is newer, the response carries a `_notice` field that the MCP server surfaces as a `NOTICE:` block in the tool result — the calling agent will tell the user to re-run setup. Updates use the same install URL, so they're in-scope of the original install consent (no permission re-ask).

## Build / deploy

```bash
# local binary
go build -o ./simple-host ./cmd/server

# linux/amd64 (for a typical Linux box)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./simple-host ./cmd/server
```

There are no tests in the repo. Verify behavior by running the binary and exercising endpoints.

## License

MIT. See `LICENSE`.
