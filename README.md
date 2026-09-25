# Simple Host

**Ship a real website with one sentence to your coding agent — and every site gets a little backend for free.**

**Live:** https://simple-host.app · **For your agent:** [`/llms.txt`](https://simple-host.app/llms.txt) · **API:** [`/openapi.yaml`](https://simple-host.app/openapi.yaml)

---

## The idea

Hosting a static website is a solved problem. The thing nobody made *simple* is the little bit of backend almost every site quietly needs.

Look at the websites real people actually build: a portfolio, a wedding RSVP, a neighborhood poll, a class project, a small landing page collecting emails, a guestbook for a side project. The overwhelming majority — call it ninety-something percent of the web ordinary people need — are static pages with **one sliver of dynamic behavior**: save an RSVP, count a vote, append to a guestbook, remember a preference.

Today that one sliver is absurdly expensive. To store a single list of RSVPs you're told to stand up a separate backend service, run a database, register a domain, and thread environment variables through a build pipeline. The backend ends up heavier than the website it serves.

**Simple Host folds both halves into one tiny binary.** Static hosting *and* a lightweight per-site datastore — lighter than Supabase, no schema, no separate service — in the same upload. Your agent ships the HTML and the data layer together, the site goes live at `https://<handle>.simple-host.app/<site>/` (or on your own domain), and it just works.

And because it stays small, it runs small. Simple Host serves all of its sites from a box with **1 CPU and 1 GB of RAM** — no CDN, no object store, no orchestration. One binary, one Postgres, one folder on disk. Most of the websites everyday people need, hosted on hardware you could forget under your desk.

## Get going

You don't deploy by hand. You tell your coding agent to, and it uses the Simple Host skill to do the rest.

If you found this on GitHub, you almost certainly already have an agent — Claude Code, Cursor, Codex, opencode, GitHub Copilot, and a dozen more. Install the skill once, for any of them:

```bash
npx skills add vineetu/simple-host
```

**Install in Claude:** the [Simple Host plugin](plugins/simple-host/) bundles the skills and the Simple Host connector (`https://simple-host.app/mcp`, you sign in once when Claude first uses it). On Claude Code:

```bash
/plugin marketplace add vineetu/simple-host
/plugin install simple-host@simple-host
```

The older skills-only `website-deploy@simple-host` plugin still installs and updates, but is superseded by `simple-host`.

Using **Hermes** or **OpenClaw**? The skills are plain `SKILL.md`, so they install natively too — e.g. Hermes: `hermes skills install https://simple-host.app/skills/website-deploy/SKILL.md --name website-deploy`. That fetches one file; `website-deploy`'s SKILL.md routes to reference documents, and cites each by full URL as well as relative path so a single-file install can still fetch them (`https://simple-host.app/v1/skills/website-deploy/references/<name>.md`). Per-agent paths are under "Install the skills manually" on the [API page](https://simple-host.app/docs.html#install-skills).

Then just talk to your agent:

> *"Build me a wedding RSVP page and deploy it."*
> *"Deploy this folder."*
> *"Add a guestbook to my site."*

It signs you up (emailed code → API key), builds the site, wires in state if the page needs it, and deploys. No terminal, no dashboard, no config files.

**In ChatGPT, Claude or Grok instead?** Add Simple Host as a connector once — `https://simple-host.app/mcp`, sign in in the window that opens — and every chat after that can build and publish sites. Steps for each app are on the [get-started page](https://simple-host.app/install.html).

## What you get

- **One-call deploy** — upload a folder, get a live `https://{handle}.simple-host.app/{site}/`. Every deploy is a new immutable version; roll back instantly.
- **A little backend, free** — per-site JSON state with atomic ops (set / inc / append), plus append-only collections for guestbooks, signups, and submissions. No schema, no database to run yourself.
- **See what your site collected** — read and download whatever visitors saved to it.
- **Connect your own domain** — subdomain or apex, or take a free `<name>.simple-host.app`. Optional: every account already has its own address, `https://<handle>.simple-host.app/`.
- **Build with AI** — a chat on the homepage that designs, previews, and publishes a site for you. Describe what you want, watch it being written, then publish.
- **Talk to it** — dictate your idea instead of typing. Captions appear as you speak, and you can edit the text before sending.
- **Show it what you mean** — attach screenshots or notes to the chat and it builds from them.
- **Sign in your way** — an emailed code, or Google (more providers later). Visitors to a site sign in the same way, on the site's own address, so a page can save on their behalf; a collection can be made private so only the owner reads it.
- **Your own admin view** — see every account on your instance and the sites they have made.

## How it works

Three moving parts, and you can hold all of them in your head at once:

1. A single **Go binary** serves every site's static files *and* exposes the REST API.
2. A **Postgres** tracks users, sites, and versions.
3. A **folder on disk** holds the versioned site files.

Every account has its own address, `{handle}.simple-host.app`, and each site is served at `{handle}.simple-host.app/{site}/`, which maps each path to its folder on disk; a connected custom domain serves the same folder. Old `sites.simple-host.app/{handle}/{site}/` links keep working. That's the whole system — no object store, no CDN, no build farm, which is exactly why it fits on a 1 GB box. The per-site datastore lives next to the files: reads are public (except private collections, which only the owner reads); a page writes after the visitor signs in on the site's own address (Google or an emailed code); agents write with an account's `X-API-Key`.

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
| `LLM_API_KEY` / `LLM_BASE_URL` / `LLM_MODEL` | | The model behind **Build with AI**. Any OpenAI-compatible provider; unset = the feature is off. |
| `TRANSCRIBE_URL` / `TRANSCRIBE_TICKET_SECRET` | | Speech-to-text for the chat mic. Unset = the mic is hidden. |
| `GOOGLE_OAUTH_CLIENT_ID` / `GOOGLE_OAUTH_CLIENT_SECRET` | | Enables Google sign-in, for owners and for visitors to sites. Both needed, or it stays off. |

## API

Everything an agent needs is at [`/llms.txt`](https://simple-host.app/llms.txt), with the full spec at [`/openapi.yaml`](https://simple-host.app/openapi.yaml) and live docs at [`/docs.html`](https://simple-host.app/docs.html). Authenticated routes take `X-API-Key: <key>`; JSON in, JSON out. The headline endpoints:

| Endpoint | Method | Description |
|---|---|---|
| `/v1/auth`, `/v1/auth/verify` | POST | Email-code sign-in → API key |
| `/v1/sites` | GET | List your sites |
| `/v1/sites/{name}/files` | POST / PUT | Deploy a site from a JSON `{path: content}` map |
| `/v1/sites/{name}` | POST / PUT / DELETE | Deploy from a tarball / roll a new version / delete |
| `/v1/sites/{name}/state` | GET / PUT / PATCH | Per-site JSON state with atomic ops (reads public; writes need a signed-in visitor on the site's own address or an account's API key) |
| `/v1/sites/{name}/collections/{coll}` | GET / POST | Append-only collections (POST is a write) |
| `/v1/sites/{name}/domain` | POST / GET / DELETE | Connect your own domain |
| `/v1/generate` | POST | Build with AI (when enabled) |

## The Website Deploy plugin

[`simple-host-website/`](simple-host-website/) is the agent integration that the install commands above pull in. It bundles:

- **Three skills** — `website-deploy` (the deploy workflow, a router plus reference documents under `references/`), `website-deploy-builder` (helping decide what to build that fits a static-plus-light-state model), and `connect-domain` (giving a site a nicer address: your own domain or a free `<name>.simple-host.app`).
- **An MCP server** (Node) exposing `register`, `deploy`, `status`, and `list` as agent-callable tools.

For Claude, [`plugins/simple-host/`](plugins/simple-host/) packages the same three skills with the hosted connector as the `simple-host` plugin. Its skills are generated copies — edit `simple-host-website/skills/` and run `bash scripts/sync-claude-plugin.sh`; `scripts/check-claude-plugin.sh` (part of `make check`) fails if the copy or version drifts.

The plugin is embedded into the Go binary, so a running instance also serves it at `/skills.zip`, `/plugin.zip`, and per-skill ZIPs for manual upload (e.g. Claude.ai). It reports its version on every API call; if the server's bundle is newer, responses carry a `_notice` the agent surfaces so users know to update.

## License

MIT — see [`LICENSE`](LICENSE).
