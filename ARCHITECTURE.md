# Architecture

A current map of the binary, request paths and package boundaries. Read [FEATURES.md](docs/FEATURES.md)
for behavior and [INTENT.md](INTENT.md) for product decisions. The focused guides under
[docs/architecture/](docs/architecture/) explain domain state, persistence, frontend and contribution.
[Target architecture](docs/architecture/target.md) gives the implemented dependency contract and where to add a feature.
[Products and add-ons](docs/architecture/products.md) lists the hooks and their callers, plus the
light-product and add-on designs, which are not built.

## What runs

`host/server/build.go:Build` wires each Go program around one `http.ServeMux`, Postgres and a local
site directory. `cmd/simple-host/main.go` and `cmd/simple-hack/main.go` pass arguments to
`server.Run`, which owns CLI dispatch and the process lifecycle. Hosted Simple Host and Simple Hack
run their respective programs with separate databases and file roots. Self-hosted installations
use the same entry points.

`cmd/simple-host` passes no extension; `cmd/simple-hack` always passes `hack.Extension()` from `products/hack`.
`EVENTS` is compatibility-only and no longer selects the product: `simple-hack` accepts
`EVENTS=hosted` or unset, while `simple-host` refuses `EVENTS=hosted` at startup.
`server.Build` selects its edition after the schema check and before registering routes.
Without an extension the server uses the Host edition.
The injected `*edition.Edition` holds fixed route, presentation, identity, storage, notification
and limit policies; event stages and deadlines remain database state.

| Policy | Host | Hosted Hack |
|---|---|---|
| Routes and presentation | Hosting pages, personal sites | Event routes, Hack pages and skills |
| Identity | Personal handles/sites | Random user handles, event holding accounts, scoped team/event website credentials |
| Storage | KV/SQLite/files | KV/SQLite/files |
| Site policy | Deployment file caps, photos | Strict archives, event retention and judging previews |
| Notifications | Site scope | Event membership and website scope |

Source: `host/edition/{edition,policies}.go`;
`Limits()` returns the configured limits by value.

| Component | Responsibility and source |
|---|---|
| Go service | HTTP pages/API, site-host files, OAuth/MCP, background work; `host/server/build.go` |
| Postgres | Platform accounts, site metadata, resources, sessions, outboxes and aggregates in `host/db/schema.sql`; Hack adds state from `products/hack/db/schema.sql`. Key-value entries are not in Postgres: each KV namespace is one SQLite file in its site's directory (`runtime/kv/<name>.sqlite`), the only KV store. |
| Local disk | Deployment versions, links/markers, runtime SQLite and raw files; `host/storage` |
| TLS reverse proxy | Hosted nginx routes hosts and serves some files directly; self-hosted Caddy configurations are in `deploy/` |
| Certificate issuers | External root-owned request/ready-marker services; `deploy/site-certs/`, `domain-certs/`, `family-certs/` |
| Local Geo DB | DB-IP Lite files in `GEOIP_DIR`; `host/geoip`, `scripts/geoip-refresh.sh`, `simple-host-geoip-refresh.timer` |
| Email and optional providers | Resend (`host/email`; recipient-free Hack acceptance/failure journal, `scripts/hack-mail-report.py`), optional Vercel event DNS (`host/sites/eventdns/vercel.go`), OAuth (`host/oauth`) |

There is no external queue or object store in this implementation. Webhook/MCP Events/Web Push
delivery uses Postgres outboxes and in-process workers (`host/data/notify.go`, `host/data/notifications`, `host/data/repo/notify`,
`host/mcp/events.go`). Web Push (SIM-360, behind `WEB_PUSH_ACCOUNTS`) is a third delivery
`kind` of the same `site_notifications` row and outbox, not a parallel engine: the row-write/kv/
file emission call sites (`host/data`) carry the row's own `visitor_id` and whether
the actor was the owner, so the fan-out (`host/data/repo/notify.EmitSiteEventFor`) can route a
push to the right devices without that layer knowing push exists; the actual RFC 8291/8292
encryption and send (`host/data/push/routes.go`, `github.com/SherClockHolmes/webpush-go`)
reuses the webhook delivery client's SSRF-guarded dial.
Visitor writes emit in both editions, excluding owner writes (an owner write with no associated
visitor row/key still emits nothing; one that touches a visitor's row -- D1's owner edit, or
SIM-358's owner-on-behalf-of add -- emits so that visitor's own push devices, never the owner's
own subscriptions, can be reached). The fan-out's own CASE (not the emission guard above) is what
actually keeps an owner-authored write, once stored, from ever reaching a webhook or MCP Events
subscription: it requires the non-push branch to see an actor other than "owner", so storing the
event for push's sake never becomes a behaviour change for those two kinds. The push branch's own
owner-recipient check is broader on purpose: it also excludes the site's own `owner_id` (not only
the literal "owner" actor), since D1's own write stamps the actor as its stampID -- the owner's
account id -- for webhook/MCP compatibility, but that same id can only ever mean the owner when
compared against an owner-role push subscription's own `owner_id`. These same two routing facts
(`push_actor`/`push_target`) are persisted on the stored event itself (`site_events.push_actor`/
`push_target`, never delivered to any subscriber) so a cursor-based MCP replay applies the
identical eligibility CASE a live delivery would have, instead of none at all. Host also emits
deploy/activation, domain-status, named-viewer requests and visitor-sign-in events;
`host/data/notify.go:EmitSiteNotification` excludes Hack.
[Hosts and access](docs/architecture/hosts-and-access.md#notification-triggers) lists the sources.
The product's hooks are few and named in `host/extension/extension.go`: `Extension.Early`
returns `access.GroupHooks` (its one field, `Erasure`, is the product's answer before an
account it holds is erased), and `Parts.Hooks` carries the storage binding provider, the
sitemap filter, `GroupHosts` (group lookups, product pages, notification addresses and pinned
versions) and `GroupGate` (writes to a held site). Platform-only installs have none and issue
no product-table queries.

Cutting a person off (erasure, team deletion, suspension) is one workflow in
`host/ops/person.go`, on one transaction where it must be atomic; each module does its own part
with its own functions and the routes call it once.

No CLI-proxy sidecar is needed by the Go request path; deployment-specific integrations belong
with the proxy configuration.

Main starts certificate requests, Hack reconciliation/entry settling, event retention and mail,
trash/idle cleanup, notification delivery, address-family
verification, network sampling and paper metrics. Enabled providers/settings determine
which loops do work. Background owners satisfy `workers.Owner` and are registered in
`host/server/workers.go`; `GET /v1/admin/usage` exposes their `workers` status array.
HTTP drains before the worker set stops and waits, with a separate worker context.
Hack supplies `GroupHosts` through `extension.Hooks` for group lookups, product pages,
notification addresses and pinned versions. `Parts.SiteSweep` supplies its named sweep
and after-pass callback; the platform reconciles suspension markers between those steps
every tenth pass.

Startup can initialize/migrate with `MIGRATE_ON_START=true`, then verifies required schema;
without a configured hostname it serves only setup and exits after configuration so the supervisor
can restart it. See `host/server/build.go:Build` and [deployment profiles](docs/operations/deployment-profiles.md).

Root `_redirects` is parsed by the pure `host/sites/redirects` package. `host/sites` validates the final file map before creating a version; `storage.UpdateCurrent` retains the file in versions and writes a sibling `redirects` marker instead of serving it. `host/sites/siteshttp/redirects.go` caches marker parses (including failures), applies rules after `siteGate` in `serveSiteFile`, and supports legacy `current/_redirects`. Shared exports restore the file from the marker, including account/trash exports. Proxy templates send rule-bearing sites to this same gated path. `host/sites/pagepath.Resolve` decides what a page address means: the exact file, the HTML sibling, the folder index, or the redirect to the page's other spelling. Rules are applied first; the slash redirect is always the last hop. See [Page address resolution](docs/architecture/page-path.md#page-address-resolution).

## Hosts and how a request flows

Hosted nginx proxies app requests to the configured Go listener (normally `127.0.0.1:8090`)
and blocks public `/internal/` access. Some content/custom-domain/family files bypass Go;
the matching proxy templates and disk markers are part of the request path.

The dispatch order in `host/server/build.go:Build` is:

```text
FamilyHosts → SiteBaseHosts → BoundSubdomains → SiteHosts → PersonHosts
            → LegacyHostRedirect → app
app = SecurityHeaders(CORS(apiMetrics(BearerAuth(gated))))
gated = APIErrors(ScopeGate(mux))
```

A matched host can serve files directly or send API requests to `app`; this is not one universal
middleware stack over every file response. Optional paper metrics wrap the whole dispatcher.
Route registrations and source locations are in [routes](docs/generated/routes.md). Detailed
host/TLS hand-offs, identity, view locks, legacy access and request accounting are in
[hosts and access](docs/architecture/hosts-and-access.md).

| Host | Routing, identity and code |
|---|---|
| Apex `simple-host.app` / `simple-hack.app` | Pages, `/v1/`, OAuth, `/mcp`, skill downloads and probes. Protected account routes require `X-API-Key` or a connector bearer token, with route-specific permissions. `sites/siteshttp/ui.go`, `access/connector/connector.go`, `access/auth/middleware.go`; Hack registration in `products/hack/compose_routes.go`. |
| `<site>.<handle>.simple-host.app`, or `<site>--<handle>.simple-host.site` for a person without a certificate of their own | Files at `/`, API bound to that site, visitor cookies on that origin. Own-address redirects and rename/handle aliases are resolved before files; `sites/siteshttp/sitehost.go:SiteHosts`. |
| Custom domain | nginx serves files and proxies API/gates; visitor identity stays on that site's origin. Verification/issuer lifecycle: `sites/siteshttp/domains.go`, `domaincheck.go`, `domaincert.go`; `deploy/domain-certs/`. |
| Claimed `<name>.simple-host.app` | Immediately verified through the domain API; `sites/siteshttp/platformsubdomain.go:BoundSubdomains` serves files and same-origin API. Retired names redirect to their current site with 302; older unbound per-name hosts keep the legacy 301 path redirect (`legacyhost.go:LegacyHostRedirect`). |
| `<label>.<suffix>` address family | Host resolves one account and its prefixed site. nginx serves files; `sites/siteshttp/familyhost.go:FamilyHosts` handles API, misses, aliases and own-domain redirects. Visitor auth is site-bound. |
| `<handle>.simple-host.app` (or `.site`, the same person's base) | Public site listing/home page and legacy `/<site>/` fallback; `/v1/` binds to that person's sites. Own addresses get 302 redirects; site-host redirects also require canonical mode and readiness. `sites/siteshttp/personhost.go:PersonHosts`. |
| Legacy `sites.simple-host.app/<handle>/<site>/` | nginx redirects through `/internal/domain-redirect` or `/internal/site-redirect`; same path/query, 302 with no-store. `/v1/` remains; canonical person-host mode requires keys for shared-host writes, without visitor sign-in. `personhost.go:contentHostRedirect`, `domains.go`. |
| Hack `<event>.simple-hack.app` | `PersonHosts` selects `sites/siteshttp/grouphost.go:ServeGroupHost`: event page/gallery screenshot, or custom event website. Only a custom website's own storage/visitor API allowlist is served here; event management stays apex-only. |
| Hack `<team>.<event>.simple-hack.app` | Site-host dispatch with the event holding account and team site. Scoped credentials authorize website management; visitor sessions cover that website. `sites/siteshttp/sitehost.go`, `heldsites.go`, `access/auth/scope.go`. |

Host modes retain the path model when off; canonical site URLs wait for certificate readiness
when a hand-off directory is configured. Without that directory the app treats certificates as
ready (`sites/addressing.go:SiteCertReadyOn`).

Addressing has one owner, `host/sites/addressing.go:Addressing`: every address handed out,
served or origin-checked is built there. Which shape a person has is one decision,
`FlatFor(handle)`: with `SITE_BASE_MOVE=flat` and a distinct `SITE_BASE_DOMAIN`, a person
without a `SITE_CERT_DIR/ready/<handle>` marker is flat (`<handle>.<base>`,
`<site>--<handle>.<base>`, on the base's wildcard; nothing is requested); everyone else keeps
`<site>.<handle>.<SITE_DOMAIN>`. `BaseFor(handle)` is that person's base; a person is served under
it only (`SiteCertReadyOn`, `Hosts.PersonHostOwner`), and `siteshttp/sitebase.go:SiteBaseHosts`
302s the other base's hosts there (`Hosts.BaseTarget`). The flat label is built by
`TeamSiteHost` and parsed by `Hosts.SiteHostParts` (split at the first `--`), the same functions
Simple Hack's flat team sites use. `flat` starts only on an install with no existing name that
could make a label ambiguous (`Hosts.RequireFlatClean`), and no new name holds `--` or is `xn`
(`rules/sites.NewHandleShape`, `ValidateReserved`, `ClaimPlatformAddress`); `<site>--<handle>`
must fit 63 characters (`Hosts.NameFits`). Under a split base a site host is live only in its
owner's exact shape (`Addressing.SiteHostIs`); any other shape gets a page redirect or a 404
(`siteshttp/sitehost.go:redirectPage`). Custom domains and families have separate verification
and readiness contracts. Owner keys, connector credentials and site-scoped visitor sessions serve
different routes; the [access guide](docs/architecture/hosts-and-access.md) specifies each boundary.

## Boundaries

The repository has three areas: `host/` is the platform, `products/` holds full products,
and `addons/` will be created with the first add-on. There is one Go module, except for
the nested rules-engine module at `host/storage/rules`. Platform runtime code imports
only platform code; a product imports the platform and itself. Products and add-ons
never import each other. `host/server` assembles the product's one description from
`host/extension`; platform consumers declare its plain interfaces and function fields.
See the [contribution and hook inventory](docs/architecture/products.md#composition-and-contribution-points).

Flow: HTTP/MCP transport → module functions (workflows and transactions) → rules and
repositories/disk mechanics. Routes and tools hold no SQL and import no repository;
`scripts/check-layering.sh` fails otherwise (exceptions with reasons in
`scripts/split/thin-edges-allow.txt`). The packages; Hack-only ones live under `products/hack/`.

| Package (under `host/` unless prefixed) | Responsibility |
|---|---|
| `access`, `access/repo`, `access/rules`, `access/auth`, `access/oauth` | Access core: the person and look-ups, owner resolution, the visit decision; its SQL; pure grant rules; credential and scope middleware; provider clients |
| `access/{signin,profile,keys,teams,editors,accountdata,operator,suspension,connector,gate}` | One package per Access feature (sign-in, profile, keys, teams, editors, account data, operator, suspension, the connector's OAuth and `/mcp`, the site gate with passcodes, named viewers and visitor sign-in): its logic, routes and tests; `access/featuretest` is their test fixture |
| `sites`, `sites/repo`, `sites/{analytics,eventdns,idempotency,namepeer,pagepath,photos,redirects}` | Sites core: the site service, `sites.Hosts`, door look-ups, locks and serving decisions; their SQL; analytics ingest; event DNS; deploy idempotency; name peers; page-address resolution; photos; `_redirects` rules |
| `sites/{deploy,versions,lifecycle,domains,families,sitelist,sitemap,export,showcase,admin,idle,siteanalytics,setup,events}` | One package per Sites feature: its logic, routes and route tests; `sites/sitesroute` is the route state they share, `sites/sitesroute/fixture` their test fixture |
| `data`, `data/repo`, `data/notify` | Data core: caller facts, the gate, resource resolution and rules decision, SQLite admission, quotas and the route core; their SQL; the notification engine |
| `data/{kv,rows,sqlconsole,files,resources,usage,visitors,notifications,push}` | One package per Data feature (KV, table rows, the SQL console, files, resource rules, usage, visitor records, notification and web-push settings): its logic, routes and tests; `data/storetest` is their shared fixture |
| `ops`, `ops/repo` | Workflows over more than one module (cutting a person off, named viewers with their devices, site export with saved data, setup) and the operator's reports; their SQL |
| `access/<feature>`, `sites/<feature>`, `data/<feature>` (`routes.go`, or Access's `Register`) | Each feature's HTTP routes, beside its logic: read the request, call the module once, write the answer. `access/accesshttp` and `sites/siteshttp` compose and mount their module's features; `handler` mounts Data's |
| `handler` | Builds the three route handlers with what they share and mounts them; the connector's visitor tools |
| `mcp` | JSON-RPC, discovery, the Host tool families (each one REST call, in process) and MCP Events |
| `products/hack/mcp` | Hack's AI tool declarations, instructions, screens-in-chat (`apps/`) and event recipe, handed to `mcp` as one `mcp.Contribution` through `extension.Parts.Tools` |
| `products/hack/core`, `products/hack/<feature>` | Hack's shared door and one package per feature (routes, own workflow, tests) |
| `products/hack/app/{events,teams,members,entries}` | Hack workflows: events, teams, registration, entries, judging and results |
| `products/hack/rules`, `products/hack/repository` | Hack's pure rules and its SQL |
| `webassets`, `web` | Platform pages, docs, skills and downloads; the page pipeline and shared HTTP refusals |
| `server`, `extension` | Program composition, CLI and lifecycle; the product description and its contributions |
| `workers` | Background job lifecycle, cancellation and status |
| `httpx` | Shared HTTP responses, the one refusal-code status table (`RefusalStatus`), limits and signed links |
| `model` | Shared records without SQL or transport |
| `storage` | Deployment trees, markers, runtime files and per-site SQLite mechanics |
| `rules/sites` | Pure site-name rules |
| `sqlkit`, `db`, `db/migrations` | Querier and transaction primitives; schema and migrations |
| `edition`, `config` | Fixed product policy; environment parsing and the settings catalog |
| `netusage`, `papermetrics` | Network counters and the nightly research measurement |
| `email`, `llm`, `geoip`, `capacity`, `tarball` | Mail, model and country clients; disk capacity; archives |
| `buildinfo` | Stamped version/commit/build time |
| `testdb`, `geoip/geoiptest` | Test fixtures |

`scripts/check-layering.sh` checks Go imports, including test imports, against both the fine
allowlist and the independent area rules in `scripts/layering/rules.txt`. `[transitional]`
is empty and adding an edge there fails. Only `host/server/**`, `products/**` and
`cmd/simple-hack` may import `host/extension`. Build-time inventory and SQL ownership tools
have explicit inspection exceptions; the `simple-host` dependency list contains no product.
Naming, SQL and embedded-text checks retain narrow documented exceptions, including existing
public cross-references; a green scan does not prove all historical text is gone.
Platform server tests use
`host/server/servertest` from `package server_test`; Hack integration tests and
their own isolated database fixture live in `products/hack/servertest`. Shared lock maps
are injected by existing wiring
(`host/sites/service.go:New`, `host/data/service.go`), not by a new lock package.
Follow [target §2](docs/architecture/target.md#2-package-map-and-import-contract).

Outside the platform, `products/hack` owns its extension, HTTP, MCP, app, repository, rule,
model, config, edition, assets and measurement packages. `host/db/schema.sql` and
`host/db/migrations` own platform installation; `products/hack/db` adds Hack's schema and
migrations in the combined order. Fresh Host databases create no Hack tables; existing
databases retain unused product objects and history. Startup checks `install_product`
before writes. The read-only adoption-evidence probe in `host/server/product.go` is the
single allowlisted platform product-table query. See [persistence](docs/architecture/persistence.md#install-product-identity).

`cmd/analytics-rebuild` and `cmd/ip-country-load` are operator tools; `host/server/reviewaccount.go`
manages reviewer accounts. `host/web/instancehost.go` rewrites instance hostnames in assets.
`plugins/openai/` is the Host ChatGPT package; Hack owns its bundles under
`products/hack/webassets/hack-toolkit/`. `deploy/` holds proxy,
issuer and deployment templates; `scripts/` holds checks/generators/ops. Website skills live in
`host/webassets/simple-host-website/`, with generated plugin copies; frontend sources, embeds and generated
MCP Apps are mapped in [frontend](docs/architecture/frontend.md). Hosted Hack onboarding
`/get-started` uses the shared page pipeline with `products/hack/webassets/static/hack-get-started.html`.

The Host owner page (`static/showcase.html`) groups its sections in `owner-tab-sites`,
`owner-tab-analytics`, and `owner-tab-settings` panels. `switchOwnerTab` hides panels;
section loaders retain their own `hidden` rules. `routeOwnerHash` handles tab and section
links, including later hash changes, and waits for a linked section to become visible
before scrolling. Site-control query links take priority. Small boxes share this file;
Simple Hack has no person pages.

## Product documentation

`docs/site` is the one exception to the handwritten frontend: Astro/Starlight builds
Markdown twice, for Host and Hack. `scripts/build-docs-site.sh` uses Node 22 only at
build time; committed output in `host/webassets/docs-site/host` and
`products/hack/webassets/docs-site/hack` has dedicated `all:` embeds and is never
reachable through the ordinary static server. `host/sites/siteshttp/ui.go`
serves `/docs/` with shared chrome, theme, instance text, limits, and UI CSP. Assembled
HTML and extracted shared chrome scripts are cached per asset pipeline. Pagefind search
uses only that brand's compiled pages. The API reference (`/docs/api/`) and MCP tools reference (`/docs/mcp/`) are generated per brand by `scripts/docs-reference.py`.
CI rebuilds both brands; the offline docs check verifies source and output hashes.

## Where to read next

| Need | Read |
|---|---|
| Hosts, certificates, identity, view locks and request accounting | [Hosts and access](docs/architecture/hosts-and-access.md) |
| Product boundary, hook callers, light-product and add-on designs | [Products and add-ons](docs/architecture/products.md) |
| Event/team/entry state and actors | [Hack domain](docs/architecture/hack-domain.md) |
| Tables, disk layout, quotas, transactions, locks, outboxes and recovery | [Persistence](docs/architecture/persistence.md) |
| Screens, shared browser code, generated assets, themes and skills | [Frontend](docs/architecture/frontend.md) |
| Tests, migration workflow, release verification and rollback | [Contributing](docs/architecture/contributing.md) |
| Hosted/systemd, Compose and Kubernetes layouts | [Deployment profiles](docs/operations/deployment-profiles.md) |
| Settings and full generated reference | [Configuration](docs/configuration.md), [advanced settings](docs/advanced/) |
| Exact endpoint/tool registrations | [Routes](docs/generated/routes.md), [MCP tools](docs/generated/mcp-tools.md) |
| Behavior and product constraints | [FEATURES](docs/FEATURES.md), [INTENT](INTENT.md) |

## Invariants and traps

- Visitor analytics use local country lookup and salted visitor hashes, without injected page
  analytics. Logs exclude query strings (`deploy/prod/nginx-analytics-logformat.conf`); API metrics
  truncate IPs to /24 or /48 (`host/analytics`, `host/ops/apimetrics.go:truncateIP`).
- Account middleware reads keys, not visitor cookies (`host/access/auth/middleware.go:Middleware`). Hosted
  pages use site-scoped visitor identity; do not embed an owner's key in a hosted page.
- An owner add on behalf of a visitor (`for`, SIM-358) is resolved and validated once
  (`host/data/visitors.go:Service.ResolveOwnerFor`) and carried on `Call.OwnerFor`; `stampInsertValues`
  (`host/data/row_write.go`) is the only place that turns it into `visitor_id` and the
  server-owned `written_by` column, for the owner's key, connector token, or the owner signed in
  on the site's own address alike. Do not set `visitor_id` from caller-supplied values anywhere
  else in that path; the owner's raw `storage_sql_execute`/`execute` SQL console is a separate,
  pre-existing, unrestricted door and is not where this check runs.
- Site gates include suspension/offline, passcodes or named viewers; List on my profile only controls the profile page
  and does not grant access. Reads are not universally public (`access/gate/passcode.go:SiteGate`, `host/access/visit_gate.go:DecideVisit`,
  `viewers.go`, `seo.go`, `host/data/access.go`). A shared site-wide passcode and
  named viewers are mutually exclusive; named viewers sign in on the site origin. There is no per-page lock.
- Normal site requests redirect to their verified own address with 302, so disconnection takes
  effect; version previews are an explicit exception (`sites/siteshttp/sitehost.go:SiteHosts`).
- Handles, claimed platform names, reserved names and retired hosts share a namespace
  (`host/access/account_handles.go:JudgeHandle`). Configured certificate hand-offs require readiness before advertising site URLs
  (`sites/addressing.go:SiteCertReadyOn`); no hand-off directory means ready.
- Account UI and consent pages use nonce CSP; passcode pages use fixed script hashes. Do not
  assume all apex responses use the same CSP (`host/web/shared_pages.go:AdminUICSP`, `access/connector/connector_authorize.go:consentHeaders`, `passcode.go`).
- `openapi.yaml` is the source contract; JSON is generated. Served specs filter `x-audience`
  operations and unused components (`host/webassets/openapi.go:PublicOpenAPISpec`).
- Deployment versions and runtime saved files are distinct. `storage/disk.go` is not the only
  writer under a site directory (`storage/runtime_files.go`).
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
