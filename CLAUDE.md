# CLAUDE.md

Read `PRINCIPLES.md` first: the owner's design rules and the tie-breaks between them. If a change conflicts with one, say which and ask.

## How to change this code

Follow these steps literally. Every path below exists.

### Where things live

- `host/access` (accounts, keys, sign-in, passcodes, viewers, editors, teams): a small core (`host/access`: the person and look-ups, `ActsAsOwner`/`ResolveOwner`, `DecideVisit`, the visitor-session verdict; `auth/` the key middleware and scope gate; `repo/` the SQL) and one package per feature, each holding its routes (`Register`) beside its logic and tests: `signin`, `profile`, `keys`, `teams`, `editors`, `accountdata`, `operator`, `suspension`, `connector` (OAuth server and `/mcp`), `gate` (site gate, passcodes, named viewers, visitor sign-in, provider sign-in and the SSO hop). A feature imports the core, never a sibling; what two features need goes in the core. `host/access/accesshttp` only composes and mounts them.
- `host/sites` is the Sites core (the site service, `sites.Hosts`, the door look-ups, locks, gate storage). Each feature is a package beside it holding its logic, its routes (`routes.go`, a `Register` function) and its route tests: `deploy`, `versions`, `lifecycle` (delete, rename, settings, Recently deleted), `domains`, `families`, `sitelist`, `sitemap`, `export`, `showcase`, `admin`, `idle`, `siteanalytics`, `setup`, `events`; plus `analytics` (the traffic store host/ops reads), `namepeer` and `repo`. A feature imports the core and `host/sites/sitesroute` (the shared route state and helpers), never a sibling feature. `host/sites/siteshttp/routes.go` mounts the features in order and keeps the serving at sites' own hosts and the dashboard pages (`ui.go`). Route tests run on `host/sites/sitesroute/fixture` (copy `host/sites/versions/routes_test.go`).
- `host/data` (saved data): a small core (`host/data`: the caller facts, the gate, resource resolution, the rules decision, SQLite admission, quotas, the route core) and one package per feature, each with its logic, routes (`routes.go`, a `Register` the composition calls in `host/handler/routes.go`) and tests: `host/data/kv`, `rows`, `sqlconsole`, `files`, `resources`, `usage`, `visitors`, `notifications`, `push`. A feature imports the core, never another feature; the notification engine is `host/data/notify`, the shared test fixtures `host/data/storetest`.
- `host/secrets` (site keys): a core (the `site_secrets` table, the sealing, `Open`) and one package per feature, each with its routes and tests: `manage` (the owner's save, list, delete) and `call` (a site's page sends a request to the key's one destination; outbound connections go through `host/outbound`, the one SSRF guard). It sits above access, sites and data; nothing below imports it.
- Imports: access <- sites <- data (each imports only those to its left); `host/ops` sits above all three and holds the workflows that span them. A routes package imports any module, never another routes package and never a `repo` package.
- `host/mcp`: the AI tools. Each tool makes one REST call in process and never reimplements a rule; if a tool needs more in one answer, the REST route returns it. The one exception: after `create_site`/`update_site` the tool reads the site's storage list to warn about a page using a resource that does not exist (`sites_pagecheck.go`).
- `products/hack`: Simple Hack. One package per feature (`lifecycle`, `registration`, `organisers`, `teams`, `teamsites`, `messages`, `notices`, `content`, `entries`, `judging`, `voting`, `results`, `public`, `assistant`, `checklist`, `exports`, `pages`), each with its routes (`routes.go`, a `Register`), the workflow only it uses, and its tests. They share the core: `core/` (the caller in an event, the refusal writer, site links, shared JSON views), `app/` (shared workflows), `repository/` (SQL of the shared tables), `rules/` and `model/`. A feature never imports another feature; `products/hack` (`compose_routes.go`) registers them all, and `mcp/` holds the tools.
- `products/hack` may import `host`; `host` never imports `products`. `scripts/check-layering.sh` enforces the imports, and fails on SQL text or a repo import in a routes package, `host/handler` or `host/mcp` (exceptions with reasons: `scripts/split/thin-edges-allow.txt`).
- Each of these packages has a `doc.go` saying the same in a few lines.
- A table is read and written only by its owner package (`scripts/split/table-owners.txt`, e.g. `users` by `host/access/repo`, `sites` by `host/sites/repo`); other packages call its functions. `sqlcheck -owners` (in `make ready`) enforces it.

### Recipe: add a REST route

1. Put the rule or workflow in the module, beside the closest neighbour (e.g. `ListKV` in `host/data/kv/kv.go`; a data feature's route goes in its own `routes.go`); in Access, in the feature's package (e.g. `host/access/keys`). Hack: in the feature's package `products/hack/<feature>/`; only what several features call goes in `products/hack/app/<area>/` or `products/hack/core`.
2. Add one `mux.Handle` line in the module's (or the data or Hack feature's) `routes.go` (Access: the feature's `Register`; copy a neighbour) and a thin handler beside its siblings: it reads the request, calls that one function, and writes the answer. Decisions live in the module; web work (cookies, redirects, streaming, which page to show) stays in the handler. A refusal is a typed error from the module (`sites.Error`, `access.Refusal`) whose status comes from `httpx.RefusalStatus`; never branch on `sql.ErrNoRows` in a route.
3. If a site editor or a deploy-only key may call it, add the pattern to `editorRoutes` or `deployRoutes` in `host/access/auth/scope.go`. Otherwise they are refused.
4. Document it in `host/webassets/static/openapi.yaml` (Hack: `products/hack/webassets/openapi.part.yaml`).
5. Name the path in backticks under its feature in `docs/FEATURES.md`.
6. Test it through the real server: Access in the feature's own package on `host/access/featuretest` (mount only that feature; what crosses features goes in `host/server/servertest/accesstest`); a data feature in its own package's tests on the `host/server/servertest/datatest` fixture, mounting the routes it names; a Sites feature in its own package on `host/sites/sitesroute/fixture`; Hack in the feature's own package (its `fixture_test.go` mounts only that feature on `products/hack/hacktest`), or `products/hack/servertest` when the test drives two features.
7. Add a CHANGELOG line (see below). Run `make ready`.

### Recipe: add an MCP tool

1. Declare it beside its neighbours: Host in `host/mcp/<family>_tools.go` (e.g. `sites_read_tools.go`), Hack in `products/hack/mcp/hack_*_tools.go`. It makes one call to the REST route; if the answer needs more, add an optional field or query flag to that route.
2. Add its row ("read", "write" or "destructive") to `hostAnnotationTable` in `host/mcp/annotations_test.go` (Hack: `hackAnnotationTable` in `products/hack/mcp/annotations_test.go`).
3. Add it to the kit: `plugins/openai/chatgpt-app-submission.json` (Hack: `plugins/openai-hack/chatgpt-app-submission.json`), same three hints.
4. A Host tool with no `site` argument that should take `owner` (a team) must be named in `ownerTools` in `host/mcp/access_team_tools.go`.
5. Simple Hack gets a Host tool only if you add its name to `hostTools` in `products/hack/mcp/contribution.go` (then also to the Hack kit).
6. Name the tool in backticks under its feature in `docs/FEATURES.md`.
7. Add a CHANGELOG line. Run `make ready`.

### Recipe: add a field or a column

1. Host: edit `host/db/schema.sql`, add `host/db/migrations/NNNN-what.sql`, and list the columns the code reads in `requiredColumns` (`host/ops/repo/schemacheck.go`).
2. Hack: edit `products/hack/db/schema.sql`, add `products/hack/db/migrations/NNNN-hack-what.sql`, and list them in `hackColumns` (`products/hack/db/schemacheck.go`).
3. NNNN is the next free number across both migration folders. The file is plain SQL, idempotent (`ADD COLUMN IF NOT EXISTS`) and safe on a live database (additive, with a default). Full rule: `host/db/migrations/migrations.go`.
4. Add a line for the file to `scripts/split/migration-owners.txt` (`file | platform or hack | what it changes`).
5. A field that holds something a person typed is private by default. Say who may read it in the "Who sees" list of the owning package's `doc.go` (e.g. `products/hack/app/entries/doc.go`) and add it to each output listed there that should show it.
6. Simple Hack analytics drops it unless its key is allowed in `host/mcp/analytics_allow.go`. Allow only keys whose values come from a fixed set or name a thing, never a person's words.
7. Add a CHANGELOG line. Run `make ready`.

### Before you commit: `make ready`

- A feature change → `make feature` (its tests, cached, without the whole-server journeys).
- Before an install → `make ready` (everything, journeys included).

It runs gofmt (rewrites files), build, vet, the rules module, `make inventory` (regenerates
`docs/generated/` and Hack's `openapi.json`), the inventory check, `sqlcheck -migrations`,
`scripts/check-docs-sync.sh`, `scripts/check-layering.sh` and the DB-backed tests
(`scripts/test-integration.sh`, cached). When it passes, commit everything it changed,
generated files included. Each failure message says what to do. CI runs `make check FULL=1`.

### Commits and the CHANGELOG

- A new CHANGELOG line goes directly under the `# Changelog` heading, newest first:
  `YYYY-MM-DD: what changed, in plain words.`
- Commit messages are plain sentences saying what changed. Never skip hooks (`--no-verify`).

## Several Claude sessions share this repo (read first)

More than one Claude session builds and deploys this repo at the same time (Simple Host work and Simple Hack work). Before any build or deploy, read `/home/ubuntu/workspace/SIMPLE-HOST-COORDINATION.md`, add a row with the files you are touching and your deploy time, and keep it updated until you are done. Hold `flock /tmp/simple-host-deploy.lock` for the whole deploy, rebase onto the latest origin/main right before pushing, and never revert another session's commits.

Working rules for any agent (Claude Code, Codex, Cursor, ...) changing this repo. Simple Host
is one Go binary that hosts static sites with a small per-site JSON backend; it is live at
https://simple-host.app and runs in production for real users.

## Before you change anything

1. Read `INTENT.md` (what it is for, non-goals, decisions with dates),
   then `docs/FEATURES.md` (what it does today), then `ARCHITECTURE.md` (where it lives) and `docs/architecture/` (focused guides).
2. `git fetch` and fast-forward first. Two sessions have overwritten each other in production.
3. Map every surface a change touches before coding: routes, `openapi.yaml`, `llms.txt`, the
   skills, the dashboard and the showcase page, the MCP tools, the Claude and OpenAI plugins.
4. If a request conflicts with `INTENT.md`, say so in one line and ask. When the owner rules on
   something, add it to INTENT's decisions with the date and reason.

## In the same commit as a feature change

- Route and MCP tool indexes are generated (`make ready` runs `make inventory`). Reference
  features by their stable FEATURES.md anchor ID (`docs/feature-ids.txt`). `make check-release`
  holds network, publication and cross-repo checks; offline checks stay in `make check`.

- Update `docs/FEATURES.md` and add a line to `CHANGELOG.md` (newest first, plain language). A new
  section or bold-lead feature in `FEATURES.md` also gets an entry in
  `host/webassets/static/features.json` (the public /features page: plain words, an example,
  editions, status, docs links; or a `not_shown` row with a why), then
  `python3 scripts/build-features-page.py` regenerates `features.html`.
- Route changed: update `host/webassets/static/openapi.yaml` (the contract; the server reads it
  directly; one line per description), then `llms.txt` and the skills if it is user-facing. A Hack
  route goes in `products/hack/webassets/openapi.part.yaml`; `make inventory` recomposes Hack's
  served `openapi.json`. `scripts/check-docs-sync.sh` fails when a /v1 route and the spec disagree.
- Skills changed: there is one set, `host/webassets/simple-host-website/skills` (Hack:
  `products/hack/webassets/hack-toolkit/skills`), served by the program and copied into the
  Claude and ChatGPT plugins only at publish/release time. Bump `version` in
  `host/webassets/simple-host-website/.claude-plugin/plugin.json` (the stale-skill notice reads it).
- The plugin repos github.com/vineetu/simple-host-plugin and simple-hack-plugin are published
  from origin/main by the hourly `sh-plugin-repo-sync.timer` (it runs both `scripts/publish-*claude-plugin-repo.sh`);
  by hand: `sudo systemctl start sh-plugin-repo-sync`, or `DRY_RUN=1 bash scripts/publish-claude-plugin-repo.sh` to preview.
- Schema changed: follow "add a field or a column" above; never rename an existing migration
  file. `simple-host migrate` runs every file not yet recorded in `schema_migrations`, including
  on databases built from `schema.sql` that already have its effect. The historical files listed
  as baseline in `host/db/migrations/migrations.go` are never run. Small boxes migrate from `install.sh` on every run; the
  server migrates on start only with `MIGRATE_ON_START=true` (Kubernetes; off in hosted
  production). Production: apply the file by hand before deploying (the
  binary refuses to start if a column it reads is missing), then
  `simple-host migrate -mark <file>` (or just `simple-host migrate`); `migrate -status` lists
  what is pending.
- There is no Host-vs-Enterprise table here any more (`PARITY.md` deleted 2026-11-19): the
  separate Enterprise repo is frozen and Enterprise will be rebuilt here as add-ons (INTENT
  2026-11-12). Do not check changes against the Enterprise repo.
- Design docs in `docs/designs/` carry a status line at the top; update it when you ship or drop
  the thing. Finished plans go to `docs/history/`.

### Self-hosted releases

A release bumps the version in the pinned files (`deploy/hack/standalone/{compose.yaml,install.sh,Dockerfile,digitalocean/*}`, `products/hack/webassets/static/setup/simple-hack.json`, and the `VERSION=` line in `docs/platforms/simple-hack-standalone.md`); `scripts/check-hack-setup.py` checks they agree. Write what was tested to `docs/history/simple-hack-release-vX.Y.Z.md` and add a `CHANGELOG.md` entry. Never add release-status or "checked on" prose to `docs/platforms/`; the check fails on it.

## Build, test, deploy (as actually done on this box)

The box is aarch64 with little RAM, so builds run under a memory cap.

```
make ready    # before every commit
make check    # everything, as CI runs it
```
(`make check`: gofmt, build, vet, `scripts/test-integration.sh`, the inventory check and the `scripts/check-*.sh` checks;
`check-fresh-install.sh` needs a local postgres superuser.) `scripts/test-integration.sh` gives
the DB-backed test fixtures (connector, hack, analytics, netusage, papermetrics -- anything
gated on `DB_DSN`) a disposable Postgres and fails if any of them skip instead of running;
`make test` is the fast no-DB loop (`go test ./...`), which still skips those fixtures. Quick
loop: `go test ./host/data/kv/` (or the route package you changed) (wrap heavy runs in the same `sudo systemd-run ... MemoryMax=2G` as the build).
Each DB-backed test package has a `TestMain` calling `testdb.Main` (`host/testdb`): under the
script (`TESTDB_ISOLATE=1`) every test process gets its own clone of the schema database, dropped
afterwards, so packages never share rows. Fixtures that start background workers must cancel and
join them (`Wait`) in a `t.Cleanup` registered after the one that closes the database. To chase a
flaky test: `make test-db PKGS=./host/server/servertest/datatest RUN='^TestNotify' COUNT=5` (packages go in
`PKGS`/`TEST_PKGS`, never as a bare argument to the script).

Build, back up, install, restart both programs from one commit (one line, from the repo root; after the initial two-program deployment has installed both paths):

```
sudo systemd-run --quiet --wait --pipe --collect -p MemoryMax=2G -p WorkingDirectory=$PWD --uid=$(id -u) --gid=$(id -g) -E HOME=$HOME -E PATH=$PATH -E GOCACHE=$(go env GOCACHE) -E GOMODCACHE=$(go env GOMODCACHE) bash -c 'CGO_ENABLED=0 go build -ldflags "-X github.com/vsriram/simple-host/host/buildinfo.Version=$(git describe --tags --always --dirty) -X github.com/vsriram/simple-host/host/buildinfo.Commit=$(git rev-parse --short HEAD) -X github.com/vsriram/simple-host/host/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /tmp/simple-host.new ./cmd/simple-host && CGO_ENABLED=0 go build -ldflags "-X github.com/vsriram/simple-host/host/buildinfo.Version=$(git describe --tags --always --dirty) -X github.com/vsriram/simple-host/host/buildinfo.Commit=$(git rev-parse --short HEAD) -X github.com/vsriram/simple-host/host/buildinfo.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" -o /tmp/simple-hack.new ./cmd/simple-hack' && TS=$(date +%Y%m%d-%H%M%S) && sudo cp -p /usr/local/bin/simple-host /usr/local/bin/simple-host.bak-$TS && sudo cp -p /usr/local/bin/simple-hack /usr/local/bin/simple-hack.bak-$TS && sudo install -m 755 -o root -g root /tmp/simple-host.new /usr/local/bin/simple-host && sudo install -m 755 -o root -g root /tmp/simple-hack.new /usr/local/bin/simple-hack && sudo systemctl restart simple-host simple-hack
```

Before deploying, check what is live: the repo is not the binary
(`/usr/local/bin/simple-host version` names the release and commit; the startup log line and
the admin page say the same; `strings /usr/local/bin/simple-host | grep <feature>` for detail). Rollback: install the `.bak-<ts>`
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
- **Visitor IPs never go to a third party.** Geolocation is local (`host/geoip`); nothing
  is injected into hosted pages.
- **Hosted pages never hold an API key.** Middleware reads only `X-API-Key` or a connector
  token; never teach it to accept the visitor cookie.
- **Any change to auth, sessions, OAuth, the connector, permissions or storage rules
  gets a security review before it ships.**
- Public contact is `support@simple-host.app`, never a personal address.

## Things that look odd but are intentional

- The admin is a real `users` row, upserted at boot from `ADMIN_API_KEY` (no default in source).
  Do not reintroduce a synthetic admin id.
- HTML, CSS and JS are hand-written and embedded. The one frontend-build exception is
  docs/site (Astro/Starlight): Node at build time only, with output committed and embedded.
  Every feature change updates its task page under docs/site. A page edit needs a rebuild.
- **New pages use the shared theme.** The page carries the chrome markers (or `<!--sh:theme-->`
  alone when it has no header) and styles both themes from site.css tokens and
  `html[data-theme="dark"]`. It follows the visitor's system until they pick in the header's
  menu; never add a page's own `prefers-color-scheme`, `matchMedia`, theme key or light-only
  default (`theme_test.go` fails on them). INTENT 2026-09-28. The one exception is the Simple
  Hack landings (`hack-story.html`, `hack-home.html` and `hack-companies.html`), which stay light through CSS alone (INTENT 2026-10-09, 2026-10-13 and 2026-10-16).
- **Simple Hack UI changes start in the UX atlas** (`docs/ux-atlas/`, see its README). A new
  hack feature gets a row in `features.json` and a home on a screen in `spec.json` and the
  prototype before the real page is built; the atlas is published after each change.
- One `http.ServeMux`, no router library. New endpoint = a `mux.Handle` line in a handler's
  `Register` (grep `mux.Handle` to find any route).
- `site_view_daily` and `site_visitor_daily` are never written and must never be dropped.
- `PERSON_HOSTS` and `SITE_HOSTS` default to `off` so event and self-hosted instances keep the
  path model. simple-host.app runs both as `canonical`.

Background owners satisfy `workers.Owner` and are registered in `host/server/workers.go`; `GET /v1/admin/usage` reports their `workers` status. Drain HTTP before stopping/joining workers and closing dependencies.

### Skill staleness notice

`notice_middleware.go` reads the embedded `plugin.json` version at boot. On wrapped routes, a
older `X-Skill-Version` header (none at all is a plain API call: no notice) adds an `X-Skill-Notice` header to a JSON response and,
when the body is an object, a `_notice` field spliced in (other fields keep their exact bytes). A
response never changes shape because of the header: an array stays a bare array. The MCP
server surfaces it to the agent. State endpoints, static serving and downloads are not wrapped.

## Local dev

```
docker run -d --name simple-host-postgres -p 5432:5432 -e POSTGRES_USER=simplehost -e POSTGRES_PASSWORD=simplehost -e POSTGRES_DB=simplehost postgres:16-alpine
```
```
DB_DSN='postgres://simplehost:simplehost@localhost:5432/simplehost?sslmode=disable' DATA_DIR=./data/sites SITE_DOMAIN=localhost ADMIN_API_KEY=$(openssl rand -hex 32) go run ./cmd/simple-host
```
Apply `host/db/schema.sql` to the fresh database first. Without `SITE_DOMAIN` the binary starts in
setup mode.
