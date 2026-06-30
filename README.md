# Simple Host

**Ship a real website with one sentence to your coding agent — and every site gets a little backend for free.**

**Live:** https://simple-host.app · **For your agent:** [`/llms.txt`](https://simple-host.app/llms.txt) · **API:** [`/openapi.yaml`](https://simple-host.app/openapi.yaml)

---

## The idea

Hosting a static website is a solved problem. The thing nobody made *simple* is the little bit of backend almost every site quietly needs.

Look at the websites real people actually build: a portfolio, a wedding RSVP, a neighborhood poll, a class project, a small landing page collecting emails, a guestbook for a side project. The overwhelming majority — call it ninety-something percent of the web ordinary people need — are static pages with **one sliver of dynamic behavior**: save an RSVP, count a vote, append to a guestbook, remember a preference.

Today that one sliver is absurdly expensive. To store a single list of RSVPs you're told to stand up a separate backend service, run a database, register a domain, and thread environment variables through a build pipeline. The backend ends up heavier than the website it serves.

**Simple Host folds both halves into one tiny binary.** Static hosting *and* a lightweight per-site datastore — lighter than Supabase, no schema, no separate service — in the same upload. Your agent ships the HTML and the data layer together, the site goes live at `https://yourname.simple-host.app`, and it just works.

And because it stays small, it runs small. Simple Host serves all of its sites from a box with **1 CPU and 1 GB of RAM** — no CDN, no object store, no orchestration. One binary, one Postgres, one folder on disk. Most of the websites everyday people need, hosted on hardware you could forget under your desk.

## Get going

You don't deploy by hand. You tell your coding agent to, and it uses the Simple Host skill to do the rest.

If you found this on GitHub, you almost certainly already have an agent — Claude Code, Cursor, Codex, opencode, and a dozen more. Install the skill once, for any of them:

```bash
npx skills add vineetu/simple-host
```

On Claude Code you can also install via the bundled marketplace:

```bash
/plugin marketplace add vineetu/simple-host
/plugin install website-deploy@simple-host
```

Then just talk to your agent:

> *"Build me a wedding RSVP page and deploy it."*
> *"Deploy this folder."*
> *"Add a guestbook to my site."*

It signs you up (magic-link email → API key), builds the site, wires in state if the page needs it, and deploys. No terminal, no dashboard, no config files.

**On the web instead?** ChatGPT, Gemini, and Copilot can do it too — paste [simple-host.app/llms.txt](https://simple-host.app/llms.txt) into the chat, describe what you want, and it hands you a site to publish. Or use the **Build with AI** chat right on [simple-host.app](https://simple-host.app), which builds and previews a site for you on the box itself.

## What you get

- **One-call deploy** — upload a folder, get a live `https://{name}.simple-host.app`. Every deploy is a new immutable version; roll back instantly.
- **A little backend, free** — per-site JSON state with atomic ops (set / increment / append), plus append-only collections for guestbooks, signups, and submissions. No schema, no database to run yourself.
- **Drop-in widgets** — threaded comments and a feedback pin, one script tag each, theme-aware.
- **Private pages** — password-lock any site behind a signed-cookie gate.
- **Starter templates** — RSVP, waitlist, landing, résumé, and more, ready to fill in.
- **Build with AI** — a chat on the homepage that designs, previews, and publishes a site for you.
- **Magic-link auth** — register with an email, get an API key. That's the whole setup.

## How it works

Three moving parts, and you can hold all of them in your head at once:

1. A single **Go binary** serves every site's static files *and* exposes the REST API.
2. A **Postgres** tracks users, sites, and versions.
3. A **folder on disk** holds the versioned site files.

A wildcard DNS record points `*.simple-host.app` at the binary, which maps each subdomain to its folder. That's the whole system — no object store, no CDN, no build farm, which is exactly why it fits on a 1 GB box. The per-site datastore lives next to the files and is origin-checked, so only a site's own subdomain can read or write its data from the browser.

## Run your own

```bash
# 1. Postgres
docker run -d --name simple-host-postgres -p 5432:5432 \
  -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost \
  postgres:16-alpine

# 2. Schema (one-time)
docker exec -i simple-host-postgres psql -U simplehost -d simplehost < db/schema.sql

# 3. Run
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' \
DATA_DIR=./data/sites \
SITE_DOMAIN=localhost:8090 \
PUBLIC_BASE_URL=http://localhost:8090 \
ADMIN_API_KEY=$(openssl rand -hex 32) \
go run ./cmd/server
```

Open http://localhost:8090 and sign in with that `ADMIN_API_KEY` — it's the master key with admin access. Build a production binary with:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./simple-host ./cmd/server
```

A single self-contained binary: ship it anywhere, set the env vars, run it.

### Configuration

All via environment variables. `DB_DSN` and `ADMIN_API_KEY` are required; the rest have defaults.

| Env var | Required | Description |
|---|---|---|
| `DB_DSN` | ✅ | Postgres DSN |
| `ADMIN_API_KEY` | ✅ | Master admin key. Pick something long — the public source intentionally ships no default. |
| `SITE_DOMAIN` | | Domain suffix for site URLs (e.g. `simple-host.app`) |
| `PUBLIC_BASE_URL` | | Base URL used in magic-link emails |
| `DATA_DIR` | | Where versioned site files live on disk |
| `PORT` | | HTTP listen port (default `8090`) |
| `RESEND_API_KEY` | | Magic-link email via [Resend](https://resend.com); auth is disabled without it |
| `MAIL_FROM` | | Magic-link sender address |
| `ANTHROPIC_API_KEY` | | Enables the **Build with AI** endpoint; unset = disabled |

## API

Everything an agent needs is at [`/llms.txt`](https://simple-host.app/llms.txt), with the full spec at [`/openapi.yaml`](https://simple-host.app/openapi.yaml) and live docs at [`/docs.html`](https://simple-host.app/docs.html). Authenticated routes take `X-API-Key: <key>`; JSON in, JSON out. The headline endpoints:

| Endpoint | Method | Description |
|---|---|---|
| `/v1/auth`, `/v1/auth/verify` | POST | Magic-link sign-in → API key |
| `/v1/sites` | GET | List your sites |
| `/v1/sites/{name}/files` | POST / PUT | Deploy a site from a JSON `{path: content}` map |
| `/v1/sites/{name}` | POST / PUT / DELETE | Deploy from a tarball / roll a new version / delete |
| `/v1/sites/{name}/state` | GET / PUT / PATCH | Per-site JSON state with atomic ops (origin-checked) |
| `/v1/sites/{name}/collections/{coll}` | GET / POST | Append-only collections |
| `/v1/templates` | GET | Starter templates |
| `/v1/generate` | POST | Build with AI (when enabled) |

## The Website Deploy plugin

[`simple-host-website/`](simple-host-website/) is the agent integration that the install commands above pull in. It bundles:

- **Two skills** — `website-deploy` (the deploy workflow) and `website-deploy-builder` (helping decide what to build that fits a static-plus-light-state model).
- **An MCP server** (Node) exposing `register`, `deploy`, `status`, and `list` as agent-callable tools.

The plugin is embedded into the Go binary, so a running instance also serves it at `/skills.zip`, `/plugin.zip`, and per-skill ZIPs for manual upload (e.g. Claude.ai). It reports its version on every API call; if the server's bundle is newer, responses carry a `_notice` the agent surfaces so users know to update.

## License

MIT — see [`LICENSE`](LICENSE).
