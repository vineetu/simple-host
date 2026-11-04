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
| Local Geo DB | Country lookup from DB-IP files; `internal/geoip`, refresh script in `scripts/` |
| Email and optional providers | Resend (`internal/email`), DNS (`internal/eventdns`), OAuth (`internal/oauth`), PhotoDNA (`internal/imagescan`) |

There is no external queue or object store in this implementation. Webhook/MCP Events delivery
uses Postgres outboxes and in-process workers (`internal/handler/notify*.go`, `internal/mcp/events.go`).
No CLI-proxy sidecar is needed by the Go request path; deployment-specific integrations belong
with the proxy configuration.

Main starts certificate requests, Hack reconciliation/entry settling, event retention and mail,
trash/idle cleanup, legacy retention, submission emails, notification delivery, address-family
verification, network sampling, image scans and paper metrics. Enabled providers/settings determine
which loops do work (`cmd/server/main.go:main`, calls to `Start*`). Connector/event-name sweeps,
Geo DB watching and optional access-log ingestion start earlier; handler constructors also start
limiter/cache, expiry and domain-check loops. Worker shutdown is not yet uniformly owned/joined:
main's signal context cancels its workers before HTTP draining completes.

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
Route registrations and source locations are in [routes](docs/generated/routes.md).

| Host | Routing, identity and code |
|---|---|
| Apex `simple-host.app` / `simple-hack.app` | Pages, `/v1/`, OAuth, `/mcp`, skill downloads and probes. Protected account routes require `X-API-Key` or a connector bearer token, with route-specific permissions. `handler/ui.go`, `connector.go`, `auth/middleware.go`; Hack registration in `hackhttp/hack.go`. |
| `<site>.<handle>.simple-host.app` | Files at `/`, API bound to that site, visitor cookies on that origin. Own-address redirects and rename/handle aliases are resolved before files; `handler/sitehost.go:SiteHosts`. |
| Custom domain | nginx serves files and proxies API/gates; visitor identity stays on that site's origin. Verification/issuer lifecycle: `handler/domains.go`, `domaincheck.go`, `domaincert.go`; `deploy/domain-certs/`. |
| Claimed `<name>.simple-host.app` | Immediately verified through the domain API; `handler/platformsubdomain.go:BoundSubdomains` serves files and same-origin API. Retired names redirect to their current site with 302; older unbound per-name hosts keep the legacy 301 path redirect (`legacyhost.go:LegacyHostRedirect`). |
| `<label>.<suffix>` address family | Host resolves one account and its prefixed site. nginx serves files; `handler/familyhost.go:FamilyHosts` handles API, misses, aliases and own-domain redirects. Visitor auth is site-bound. |
| `<handle>.simple-host.app` | Public site listing/home page and legacy `/<site>/` fallback; `/v1/` binds to that person's sites. Ready site hosts or own domains get 302 redirects. `handler/personhost.go:PersonHosts`. |
| Legacy `sites.simple-host.app/<handle>/<site>/` | nginx redirects through `/internal/domain-redirect` or `/internal/site-redirect`; same path/query, 302 with no-store. `/v1/` remains; canonical person-host mode requires keys for shared-host writes, without visitor sign-in. `personhost.go:contentHostRedirect`, `domains.go`. |
| Hack `<event>.simple-hack.app` | `PersonHosts` selects `handler/hack_mode.go:serveHackEventHost`: event page/gallery screenshot, or custom event website. Only a custom website's own storage/visitor API allowlist is served here; event management stays apex-only. |
| Hack `<team>.<event>.simple-hack.app` | Site-host dispatch with the event holding account and team site. Scoped credentials authorize website management; visitor sessions cover that website. `handler/sitehost.go`, `hack_sites.go`, `auth/scope.go`. |

**Addresses and TLS.** `PERSON_HOSTS` and `SITE_HOSTS` support `off`, `serve`, `canonical`;
site hosts require person hosts. Defaults are off, preserving the path model. Serve mode answers
hosts while handing out path URLs; canonical mode hands out ready host URLs. A site host is
advertised only after `SITE_CERT_DIR/ready/<handle>` exists (`sitehost.go:siteHostLiveOn`).
The external site issuer issues `*.<handle>.<base>` from queued requests using DNS-01.
Before readiness, Host uses the person-path fallback; Hack does not expose that fallback.

Custom domains retain their previous working address while verification is pending. The checker
queues HTTP-01 issuance once DNS points here; the issuer writes nginx configuration and a ready
or failed marker. It removes its managed configuration when the domain link disappears and leaves
hand-made vhosts alone (`deploy/domain-certs/issue.sh`). Active-domain failures notify after 24h
and release the binding after 72h by default; limits are configurable and an emailed release date
is preserved (`handler/domaincheck.go:applyDomainCheck`, `config/limits.go:DefaultLimits`).

Address families require TXT proof (or an admin exemption) and wildcard DNS. Their issuer adopts
an existing operator-managed wildcard certificate rather than issuing one; readiness must match
the site's prefix. Proven but non-live family hosts return 404. Address ranking is custom domain,
most-specific canonical family, then site host (`handler/familyhost.go`, `familyapi.go`;
[family design](docs/designs/address-families.md)).

The alternate site-base move is off: `SiteBaseHosts` redirects alternate-base links back to the
matching platform host with path/query intact. Configuration and issuer details live in
[the paused-base runbook](docs/operations/site-base-paused.md).
`contentHostOnlySites` keeps `vineetu/eb2-wait` on the legacy content host for its nginx
`/eb2-api/*` integrations (`handler/personhost.go`).

**Owner, connector and visitor identity.** Email-code or configured OAuth sign-in issues an
account key; only its SHA-256 is stored. Admin is a real user row ensured at boot.
OAuth 2.1 with registration, PKCE and consent guards Streamable HTTP `/mcp`. Connector bearer
and MCP REST replay mint per-request in-memory credentials, revoked after use, and enter the
same gated mux as REST (`handler/connector.go`, `internal/db/internalkey.go`). MCP tools do
not bypass scope or legacy gates; Hack filters legacy and chat-facing credential tools.

Browser SSO uses the apex host-only `__Host-sh_sso` cookie only in `/v1/sso/*`. It does not
replace account API authentication. A nonce-bound, one-time hop establishes a site-scoped
visitor session (`handler/sso.go`, `visitorsession.go`). `/auth.js` supplies `SH.signIn()` and
`SH.requireSignIn()`; legacy email/provider flows remain for cached pages and instances without
an apex hop (`visitoremail.go`, `oauth.go`). Hosted Hack serves `SH.storage`, without legacy
`SH.state`, `SH.collection` or `SH.data` helpers.

Host's grandfathered state/collection reads apply Origin/Referer and private-record rules.
Writes use the owner's/admin's key or an allowed visitor identity plus CSRF on the site's own
address; shared-host behavior depends on the person-host/write-auth configuration. Storage
resources have their own declared access policy. Hosted Hack's legacy routes return 410 on
site and handle aliases; event hosts retain their narrower allowlist (`handler/kinds.go`,
`hack_legacy_storage.go`, `internal/app/legacy/kinds.go:VisitorWrite`, `internal/app/storage/access.go`).

## Boundaries

Current flow: HTTP/MCP transport → application services → rules and repositories/disk mechanics.
Services own extracted workflows and transactions; rules make pure decisions; repositories execute
SQL. This is an ongoing boundary, not a claim that transport is already free of SQL or disk calls.
The following packages exist today (verified with `go list ./internal/...`):

| Package under `internal/` | Responsibility |
|---|---|
| `handler` | Shared HTTP, accounts, sites/storage, admin, connector composition, embedded `static/`, remaining workers |
| `hackhttp` | Hack HTTP/pages and transport mapping; no production import of `handler` |
| `mcp` | JSON-RPC, discovery, family registrars and common tool/schema inventory (`registry.go`); MCP Events |
| `mcp/apps/{bundlegen,gentool}` | MCP Apps browser-bundle generator and command |
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
step. Account/notification/operations services, `webassets`, `workers` and `sitelocks` from the
target map do not exist yet. Shared lock maps are injected by existing wiring
(`app/sites/service.go:New`, `app/storage/service.go:Deps`), not by a new lock package.
Do not copy a transitional import as precedent; follow [target §2](docs/architecture/target.md#2-package-map-and-import-contract).

Outside `internal/`: `db/schema.sql` and `db/migrations` define fresh/install upgrades;
`cmd/analytics-rebuild` and `cmd/ip-country-load` are operator tools. `deploy/` holds proxy,
issuer and deployment templates; `scripts/` holds checks/generators/ops. Website skills live in
`simple-host-website/`, with generated plugin copies; frontend sources, embeds and generated
MCP Apps are mapped in [frontend](docs/architecture/frontend.md). Hosted Hack onboarding
`/get-started` is rendered by `handler/hack_get_started.go` from `static/hack-get-started.html`.

## Where to read next

| Need | Read |
|---|---|
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
  analytics. API metrics truncate IPs to /24 or /48 (`internal/analytics`, `handler/apimetrics.go:truncateIP`).
- Account middleware reads keys, not visitor cookies (`auth/middleware.go:Middleware`). Hosted
  pages use site-scoped visitor identity; do not embed an owner's key in a hosted page.
- Site gates include suspension/offline, passcodes or named viewers; unlisted is listing/robots
  policy, not an access lock. Reads are not universally public (`handler/passcode.go:siteGate`,
  `viewers.go`, `unlisted.go`, `internal/app/storage/access.go`). There is no per-page view lock.
- Normal site requests redirect to their verified own address with 302, so disconnection takes
  effect; version previews are an explicit exception (`handler/sitehost.go:SiteHosts`).
- Handles, claimed platform names, reserved names and retired hosts share a namespace
  (`handler/handlecheck.go:JudgeHandle`). Certificate readiness precedes advertised site URLs.
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
