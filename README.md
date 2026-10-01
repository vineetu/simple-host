# Simple Host

Ask your AI app for a website, and it goes live at its own address, `https://<site>.<handle>.simple-host.app/`, with a place to save things (RSVPs, votes, sign-ups).

- **Try it:** https://simple-host.app/
- **Run your own → https://simple-host.app/setup?product=small-box**
- **Features:** https://simple-host.app/features
- **Architecture:** https://simple-host.app/architecture.html
- **For agents and developers:** [`llms.txt`](https://simple-host.app/llms.txt) · [OpenAPI spec](https://simple-host.app/openapi.yaml) · [API docs](https://simple-host.app/docs.html) · [Get started in your AI app](https://simple-host.app/install.html)
- **Enterprise edition:** https://simple-host.app/enterprise · [github.com/vineetu/simple-host-enterprise](https://github.com/vineetu/simple-host-enterprise)
- **Enterprise on your Kubernetes cluster → https://simple-host.app/setup?product=enterprise** (Helm or Kubernetes YAML; existing or in-cluster Postgres)
- **What Enterprise costs to run → https://simple-host.app/costs** (AWS, Azure and Google Cloud at list prices, for your number of people)
- **Legal and help:** [Terms](https://simple-host.app/terms) · [Privacy](https://simple-host.app/privacy.html) · [Support](https://simple-host.app/support) (support@simple-host.app)

## Get started

Connect Simple Host to your AI app once, then ask it for a site.

- **Chat apps:** add the connector `https://simple-host.app/mcp` and sign in in the window that opens. Steps for each app: [get-started page](https://simple-host.app/install.html).
- **Coding agents:** install the skills with `npx skills add vineetu/simple-host`.
- **Claude Code plugin:** `/plugin marketplace add vineetu/simple-host`, then `/plugin install simple-host@simple-host`.

Then ask: *"Build me a wedding RSVP page and put it online."*

## Features

### Publish and versions
- Publish a folder (archive or inline files) in one call. Every publish is a new version.
- Look before it goes live: publish without making it live, open a one-hour preview link, then make it live.
- Roll back to any kept version. Rename a site and old links keep working.
- Take a site offline and back online. Nothing is deleted.
- Delete is recoverable for 7 days from Recently deleted.
- Download a copy of any site. Public or unlisted on your person page.
- Visitor analytics per site, from server logs, with top pages and referring domains. No tracking script.

### Addresses and custom domains
- Every site has its own address and browser origin: `<site>.<handle>.simple-host.app`.
- Every person has a page listing their public sites: `<handle>.simple-host.app`.
- A free `<name>.simple-host.app`, live at once.
- Your own domain, subdomain or bare domain. Ownership is proved with one TXT record; the certificate is issued automatically.
- `www` and the bare domain are set up together, one redirecting to the other.

### Saved data and lists
- Per-site saved data: one JSON document with atomic operations (set, increment, append, remove).
- Lists that visitors add to: comments, RSVPs, votes, sign-ups.
- Every piece of saved data has a kind: Shared (default, public), Page info (owner writes, everyone reads), Submissions (visitors send; private to the owner by default; each visitor sees, changes and withdraws their own; optional email digest), Personal (one private record per visitor) and Shared board (a list a group edits item by item).
- Private Submissions: only signed-in visitors add; the owner reads all, each visitor only their own.
- The owner can delete entries in any list, edit entries in a private list, empty a list, and download it as CSV. Every change to saved data can be undone for 30 days (History, Recently deleted).

### Visitor sign-in
- Visitors sign in with Google or an emailed code on the site's own address.
- Saves are made as that visitor. One sign-in covers one site.
- A small `auth.js` client handles it for the page.

### Your account, keys and data
- Sign in with an emailed code or Google.
- Named API keys: list, create and revoke them one at a time. Deploy-only keys for CI; a key unused for 180 days stops working. Sign out everywhere in one step.
- Change your sign-in email, confirmed from both addresses, with a 7-day undo.
- An email after each new sign-in or app connection. Can be turned off.
- Download all your data in one archive. Delete your account for good.

### For AI agents
- A connector (MCP) at `https://simple-host.app/mcp` with sign-in, for chat apps and agents.
- Skills: `website-deploy`, `website-deploy-builder`, `connect-domain`.
- Plugins for Claude and ChatGPT, and `npx skills add vineetu/simple-host` for coding agents.
- [`llms.txt`](https://simple-host.app/llms.txt) and a full [OpenAPI spec](https://simple-host.app/openapi.yaml).

### Operator tools
- Admin console: disk use, running release, every account and site, API traffic.
- Take down a site or suspend an account without deleting anything; restore later.
- Issue participant accounts. Download all entries.

### Self-host, small box, hackathons
- One Go binary, one Postgres, one folder on disk. Runs on 1 CPU and 1 GB of RAM.
- A Docker Compose install for a fresh server ([`deploy/install/install.sh`](deploy/install/install.sh)). Re-running it upgrades.
- A hackathon edition: an organiser stands up a private instance for an event and hands each participant a key. See https://simple-hack.app/.
- A setup helper, https://simple-host.app/setup?product=small-box, writes the install command and settings; [docs/advanced/](docs/advanced/README.md) explains every setting.

## How it works

1. A single **Go binary** serves every site's files and the REST API.
2. **Postgres** holds accounts, sites, versions and saved data.
3. A **folder on disk** holds the versioned site files.

No object store, no CDN, no build farm. Details: [ARCHITECTURE.md](ARCHITECTURE.md) and the [architecture page](https://simple-host.app/architecture.html).

## Run your own

On a company's Kubernetes cluster, run Simple Host Enterprise instead; https://simple-host.app/costs estimates what it costs there (AWS, Azure, Google Cloud).

```bash
docker run -d --name simple-host-postgres -p 5432:5432 -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost postgres:16-alpine
docker exec -i simple-host-postgres psql -U simplehost -d simplehost < db/schema.sql
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' DATA_DIR=./data/sites SITE_DOMAIN=localhost:8090 PUBLIC_BASE_URL=http://localhost:8090 ADMIN_API_KEY=$(openssl rand -hex 32) go run ./cmd/server
```

Open http://localhost:8090 and sign in with that `ADMIN_API_KEY` (the admin key). Production build:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ./simple-host ./cmd/server
```

### Configuration and advanced settings

`DB_DSN` and `ADMIN_API_KEY` are required; everything else has a default. The setup helper at
https://simple-host.app/setup?product=small-box writes the install command and `.env` for a small
box. It runs in your browser and never asks for a password or key; where the server has its model
backend, an optional check of your changed settings sends only their names and values. Every setting, by area with recipes (stricter sign-in,
shorter retention, a small hackathon box): [docs/advanced/](docs/advanced/README.md). The full
reference: [docs/configuration.md](docs/configuration.md).

## API

Authenticated routes take `X-API-Key: <key>`. JSON in, JSON out. The full contract is [`openapi.yaml`](https://simple-host.app/openapi.yaml); a readable version is [docs.html](https://simple-host.app/docs.html).

| Endpoint | Method | Description |
|---|---|---|
| `/v1/auth`, `/v1/auth/verify` | POST | Email-code sign-in, returns an API key |
| `/v1/sites` | GET | List your sites |
| `/v1/sites/{name}/files` | POST / PUT | Publish from JSON: `{"files": {"index.html": "<h1>hi</h1>"}}` |
| `/v1/sites/{name}` | POST / PUT / PATCH / DELETE | Publish an archive / new version / rename or offline / delete |
| `/v1/sites/{name}/state` | GET / PUT / PATCH | Saved data with atomic operations |
| `/v1/sites/{name}/collections/{coll}` | GET / POST | Lists |
| `/v1/sites/{name}/data/{name}` | GET / POST / PUT / PATCH | Saved data by kind (Page info, Submissions, Personal, Shared board); `.../kind` declares one |
| `/v1/me/keys` | GET / POST / DELETE | Named API keys, deploy-only keys for CI |
| `/v1/sites/{name}/domain` | POST / GET / DELETE | Your own domain |

## Working on this repo

- [CLAUDE.md](CLAUDE.md): working rules, build, test and deploy.
- [INTENT.md](INTENT.md): what it is for, and decisions with dates.
- [FEATURES.md](FEATURES.md): every feature and every surface it touches.
- [ARCHITECTURE.md](ARCHITECTURE.md): components, request flow, data model.
- [CHANGELOG.md](CHANGELOG.md): what changed, newest first.
- `make check`: format, build, vet, tests and the doc-sync checks.
- Skills source: [`simple-host-website/skills/`](simple-host-website/skills/). The Claude plugin in [`plugins/simple-host/`](plugins/simple-host/) is a generated copy (`bash scripts/sync-claude-plugin.sh`).

## License

MIT. See [`LICENSE`](LICENSE).
