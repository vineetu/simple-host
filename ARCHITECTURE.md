# Architecture

A current map of the binary, request paths and package boundaries. Read [FEATURES.md](FEATURES.md)
for behavior and [INTENT.md](INTENT.md) for product decisions. The focused guides under
[docs/architecture/](docs/architecture/) explain domain state, persistence, frontend and contribution.
[Target architecture](docs/architecture/target.md) gives the dependency rationale and where to add a feature;
its planned packages are not all present yet.

## What runs

`cmd/server/main.go:main` wires one Go binary around one `http.ServeMux`, Postgres and a local
site directory. Hosted Simple Host and Simple Hack run separate instances of that binary,
with separate databases and file roots. Self-hosted installations use the same implementation.

Main calls `edition.New(cfg)` once, after the schema check and before registering routes.
`EVENTS=hosted` selects Hack; other values select Host (`internal/edition/edition.go:New`).
The injected `*edition.Edition` holds fixed route, presentation, identity, storage, notification
and limit policies; event stages and deadlines remain database state.

| Policy | Host | Hosted Hack |
|---|---|---|
| Routes and presentation | Hosting pages, personal sites | Event routes, Hack pages and skills |
| Identity | Personal handles/sites | Random user handles, event holding accounts, scoped team/event website credentials |
| Storage | KV/SQLite/files and grandfathered legacy APIs | KV/SQLite/files; legacy APIs return 410 |
| Site policy | Deployment file caps, photos | Strict archives, event retention and judging previews |
| Notifications | Site scope | Event membership and website scope |

Source: `internal/edition/{routes,presentation,identity,storage,notifications}.go`;
`Limits()` returns the configured limits by value.

| Component | Responsibility and source |
|---|---|
| Go service | HTTP pages/API, site-host files, OAuth/MCP, background work; `cmd/server/main.go` |
| Postgres | Accounts, site metadata, resources/KV, Hack state, sessions, outboxes and aggregates; `db/schema.sql` |
| Local disk | Deployment versions, links/markers, runtime SQLite and raw files; `internal/storage` |
| TLS reverse proxy | Hosted nginx routes hosts and serves some files directly; self-hosted Caddy configurations are in `deploy/` |
| Certificate issuers | External root-owned request/ready-marker services; `deploy/site-certs/`, `domain-certs/`, `family-certs/` |
| Local Geo DB | DB-IP Lite files in `GEOIP_DIR`; `internal/geoip`, `scripts/geoip-refresh.sh`, `simple-host-geoip-refresh.timer` |
| Email and optional providers | Resend (`internal/email`; recipient-free Hack acceptance/failure journal, `scripts/hack-mail-report.py`), optional Vercel event DNS (`internal/eventdns/vercel.go`), OAuth (`internal/oauth`), PhotoDNA (`internal/imagescan`) |

There is no external queue or object store in this implementation. Webhook/MCP Events delivery
uses Postgres outboxes and in-process workers (`internal/handler/notify*.go`, `internal/mcp/events.go`).
Visitor writes emit in both editions, excluding owner writes. Host also emits deploy/activation,
domain-status, named-viewer requests and visitor-sign-in events; `notify.go:emitSiteNotification`
excludes Hack. [Hosts and access](docs/architecture/hosts-and-access.md#notification-triggers) lists the sources.
No CLI-proxy sidecar is needed by the Go request path; deployment-specific integrations belong
with the proxy configuration.

Main starts certificate requests, Hack reconciliation/entry settling, event retention and mail,
trash/idle cleanup, legacy retention, submission emails, notification delivery, address-family
verification, network sampling, image scans and paper metrics. Enabled providers/settings determine
which loops do work. Background owners satisfy `workers.Owner` and are registered in
`cmd/server/workers.go`; `GET /v1/admin/usage` exposes their `workers` status array.
HTTP drains before the worker set stops and waits, with a separate worker context.

Startup can initialize/migrate with `MIGRATE_ON_START=true`, then verifies required schema;
without a configured hostname it serves only setup and exits after configuration so the supervisor
can restart it. See `cmd/server/main.go:main` and [deployment profiles](docs/operations/deployment-profiles.md).

## Hosts and how a request flows

Hosted nginx proxies app requests to the configured Go listener (normally `127.0.0.1:8090`)
and blocks public `/internal/` access. Some content/custom-domain/family files bypass Go;
the matching proxy templates and disk markers are part of the request path.

The dispatch order in `cmd/server/main.go:main` is:

```text
FamilyHosts → SiteBaseHosts → BoundSubdomains → SiteHosts → PersonHosts
            → LegacyHostRedirect → app
app = SecurityHeaders(CORS(apiMetrics(BearerAuth(gated))))
gated = APIErrors(LegacyStorageCutoffGate(HackLegacyStorageGate(ScopeGate(mux))))
```

A matched host can serve files directly or send API requests to `app`; this is not one universal
middleware stack over every file response. Optional paper metrics wrap the whole dispatcher.
Route registrations and source locations are in [routes](docs/generated/routes.md). Detailed
host/TLS hand-offs, identity, view locks, legacy access and request accounting are in
[hosts and access](docs/architecture/hosts-and-access.md).

| Host | Routing, identity and code |
|---|---|
| Apex `simple-host.app` / `simple-hack.app` | Pages, `/v1/`, OAuth, `/mcp`, skill downloads and probes. Protected account routes require `X-API-Key` or a connector bearer token, with route-specific permissions. `handler/ui.go`, `connector.go`, `auth/middleware.go`; Hack registration in `hackhttp/hack.go`. |
| `<site>.<handle>.simple-host.app` | Files at `/`, API bound to that site, visitor cookies on that origin. Own-address redirects and rename/handle aliases are resolved before files; `handler/sitehost.go:SiteHosts`. |
| Custom domain | nginx serves files and proxies API/gates; visitor identity stays on that site's origin. Verification/issuer lifecycle: `handler/domains.go`, `domaincheck.go`, `domaincert.go`; `deploy/domain-certs/`. |
| Claimed `<name>.simple-host.app` | Immediately verified through the domain API; `handler/platformsubdomain.go:BoundSubdomains` serves files and same-origin API. Retired names redirect to their current site with 302; older unbound per-name hosts keep the legacy 301 path redirect (`legacyhost.go:LegacyHostRedirect`). |
| `<label>.<suffix>` address family | Host resolves one account and its prefixed site. nginx serves files; `handler/familyhost.go:FamilyHosts` handles API, misses, aliases and own-domain redirects. Visitor auth is site-bound. |
| `<handle>.simple-host.app` | Public site listing/home page and legacy `/<site>/` fallback; `/v1/` binds to that person's sites. Own addresses get 302 redirects; site-host redirects also require canonical mode and readiness. `handler/personhost.go:PersonHosts`. |
| Legacy `sites.simple-host.app/<handle>/<site>/` | nginx redirects through `/internal/domain-redirect` or `/internal/site-redirect`; same path/query, 302 with no-store. `/v1/` remains; canonical person-host mode requires keys for shared-host writes, without visitor sign-in. `personhost.go:contentHostRedirect`, `domains.go`. |
| Hack `<event>.simple-hack.app` | `PersonHosts` selects `handler/hack_mode.go:serveHackEventHost`: event page/gallery screenshot, or custom event website. Only a custom website's own storage/visitor API allowlist is served here; event management stays apex-only. |
| Hack `<team>.<event>.simple-hack.app` | Site-host dispatch with the event holding account and team site. Scoped credentials authorize website management; visitor sessions cover that website. `handler/sitehost.go`, `hack_sites.go`, `auth/scope.go`. |

Host modes retain the path model when off; canonical site URLs wait for certificate readiness
when a hand-off directory is configured. Without that directory the app treats certificates as
ready (`handler/sitebase.go:siteCertReadyOn`). Custom domains and families have separate verification
and readiness contracts. Owner keys, connector credentials and site-scoped visitor sessions serve
different routes; the [access guide](docs/architecture/hosts-and-access.md) specifies each boundary.

## Boundaries

Current flow: HTTP/MCP transport → application services → rules and repositories/disk mechanics.
Services own extracted workflows and transactions; rules make pure decisions; repositories execute
SQL. This is an ongoing boundary, not a claim that transport is already free of SQL or disk calls.
The following packages exist today (verified with `go list ./internal/...`):

| Package under `internal/` | Responsibility |
|---|---|
| `handler` | Shared HTTP, accounts, sites/storage, admin, connector composition, asset presentation, remaining workers |
| `hackhttp` | Hack HTTP/pages and transport mapping; no production import of `handler` |
| `mcp` | JSON-RPC, discovery, family registrars and common tool/schema inventory (`registry.go`); MCP Events |
| `mcp/apps/{bundlegen,gentool}` | MCP Apps browser-bundle generator and command |
| `webassets` | Embedded static pages, Hack modules and Host/Hack skill/download bundles; no internal dependencies |
| `workers` | Standard-library lifecycle owners, cancellation, joins and progress/status tracking |
| `httpx` | Shared HTTP responses, limits, signed links and route metadata |
| `auth` | Account credential/context and scope middleware |
| `app/hack/events` | Event lifecycle/content, organisers, settings and public directory |
| `app/hack/teams` | Team lifecycle/admin/links and team finding |
| `app/hack/members` | Registration, applications, tracks and leaving |
| `app/hack/entries` | Entry writes/settling, judging, assignments, scoring, voting and results |
| `app/sites` | Deploy/publication, versions, addresses, site/account file lifecycle and exports |
| `app/storage` | KV, SQLite and runtime-file workflows, quotas and access decisions |
| `app/legacy` | Host saved-data compatibility adapter, including cutoff/access rules |
| `rules/{auth,sites,storage}` | Pure grant/name/storage decisions |
| `rules/{hackevents,hackteams,hackentries}` | Pure event codes, team decisions, eligibility, judging and result arithmetic |
| `repository/{accounts,sites,storage,legacy}` | Extracted account/site/storage/legacy SQL |
| `repository/{hackevents,hackteams,hackentries}` | Extracted Hack SQL |
| `repository/sqlkit` | Shared Querier and transaction-opening primitives |
| `db` | Remaining account/session/notification/operations SQL, schema checks, compatibility aliases/forwarders |
| `model` | Shared records without SQL or transport |
| `storage` | Deployment trees, symlinks/markers, runtime files and per-site SQLite mechanics |
| `edition`, `config` | Injected fixed product policy; environment parsing and settings catalog |
| `analytics`, `netusage`, `papermetrics` | Access-log aggregation, network sampling and research metrics |
| `email`, `eventdns`, `oauth`, `imagescan` | Provider clients |
| `geoip`, `capacity`, `tarball`, `wpimport` | Local country lookup, disk capacity, archives and WordPress import |
| `brain`, `buildinfo` | Product-knowledge bundle support; stamped version/commit/build time |
| `testdb`, `geoip/geoiptest` | Test fixtures |

`scripts/check-layering.sh` checks Go imports, including test imports, against the final allowlist
and exact transitional edges in `scripts/layering/rules.txt`. Each exception names its removal
step. Account/notification/operations services and `sitelocks` from the
target map do not exist yet. Shared lock maps are injected by existing wiring
(`app/sites/service.go:New`, `app/storage/service.go:Deps`), not by a new lock package.
Do not copy a transitional import as precedent; follow [target §2](docs/architecture/target.md#2-package-map-and-import-contract).

Outside `internal/`: `db/schema.sql` and `db/migrations` define fresh/install upgrades;
`cmd/analytics-rebuild` and `cmd/ip-country-load` are operator tools; `cmd/server/reviewaccount.go`
manages reviewer accounts. `handler/instancehost.go` rewrites instance hostnames in assets.
`openai-plugin/` is the ChatGPT package. `deploy/` holds proxy,
issuer and deployment templates; `scripts/` holds checks/generators/ops. Website skills live in
`internal/webassets/simple-host-website/`, with generated plugin copies; frontend sources, embeds and generated
MCP Apps are mapped in [frontend](docs/architecture/frontend.md). Hosted Hack onboarding
`/get-started` is rendered through `handler/chrome.go:chromePageRaw` from `static/hack-get-started.html`.

## Where to read next

| Need | Read |
|---|---|
| Hosts, certificates, identity, view locks and request accounting | [Hosts and access](docs/architecture/hosts-and-access.md) |
| Event/team/entry state and actors | [Hack domain](docs/architecture/hack-domain.md) |
| Tables, disk layout, quotas, transactions, locks, outboxes and recovery | [Persistence](docs/architecture/persistence.md) |
| Screens, shared browser code, generated assets, themes and skills | [Frontend](docs/architecture/frontend.md) |
| Tests, migration workflow, release verification and rollback | [Contributing](docs/architecture/contributing.md) |
| Hosted/systemd, Compose and Kubernetes layouts | [Deployment profiles](docs/operations/deployment-profiles.md) |
| Settings and full generated reference | [Configuration](docs/configuration.md), [advanced settings](docs/advanced/) |
| Exact endpoint/tool registrations | [Routes](docs/generated/routes.md), [MCP tools](docs/generated/mcp-tools.md) |
| Behavior and product constraints | [FEATURES](FEATURES.md), [INTENT](INTENT.md) |

## Invariants and traps

- Visitor analytics use local country lookup and salted visitor hashes, without injected page
  analytics. Logs exclude query strings (`deploy/prod/nginx-analytics-logformat.conf`); API metrics
  truncate IPs to /24 or /48 (`internal/analytics`, `handler/apimetrics.go:truncateIP`).
- Account middleware reads keys, not visitor cookies (`auth/middleware.go:Middleware`). Hosted
  pages use site-scoped visitor identity; do not embed an owner's key in a hosted page.
- Site gates include suspension/offline, passcodes or named viewers; unlisted is listing/robots
  policy, not an access lock. Reads are not universally public (`handler/passcode.go:siteGate`,
  `viewers.go`, `unlisted.go`, `internal/app/storage/access.go`). A shared site-wide passcode and
  named viewers are mutually exclusive; named viewers sign in on the site origin. There is no per-page lock.
- Normal site requests redirect to their verified own address with 302, so disconnection takes
  effect; version previews are an explicit exception (`handler/sitehost.go:SiteHosts`).
- Handles, claimed platform names, reserved names and retired hosts share a namespace
  (`handler/handlecheck.go:JudgeHandle`). Configured certificate hand-offs require readiness before advertising site URLs
  (`handler/sitebase.go:siteCertReadyOn`); no hand-off directory means ready.
- Account UI and consent pages use nonce CSP; passcode pages use fixed script hashes. Do not
  assume all apex responses use the same CSP (`handler/ui.go:adminUICSP`, `connector.go:consentHeaders`, `passcode.go`).
- `openapi.yaml` is the source contract; JSON is generated. Served specs filter `x-audience`
  operations and unused components (`handler/hack_assets.go:publicOpenAPISpec`).
- Deployment versions and runtime saved files are distinct. `storage/disk.go` is not the only
  writer under a site directory (`storage/runtime_files.go`, `handler/imagescan.go`).
- `index.html` and `showcase.html` inline the same analytics parser; showcase also runs on the
  content host. Keep both copies generated (`scripts/inline-analytics-parser.sh`).
- The analytics tail tracks inode/offset: rotate with `create` and `delaycompress`, never
  `copytruncate` (`deploy/prod/logrotate-analytics.conf`). Embedded HTML changes need a rebuild;
  a changed checkout is not a deployed binary.

## Checks

`make test` is the no-DB loop; `make check` builds/vets and runs disposable-Postgres integration
fixtures plus local consistency checks. `make check-release` holds network/publication/cross-repo
checks. The Makefile is authoritative; commands, fixture lifecycle and deploy/rollback steps are
in [contributing](docs/architecture/contributing.md). Historical rollout notes are in
[architecture history](docs/history/architecture-notes-2026-10.md).
