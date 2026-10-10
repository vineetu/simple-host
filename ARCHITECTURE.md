# Architecture

Where things live and how a request moves through them. What the product does is in
`FEATURES.md`; why it is that way is in `INTENT.md`. This file is only about where it lives.
Anything that must stay true is enforced by a check in `make check`, not by this document.

## Components

| Piece | What it is | Where |
|---|---|---|
| Go service | One binary: API, dashboard and pages, site files for site hosts, person hosts and claimed names, MCP connector, OAuth server | `/usr/local/bin/simple-host`, `simple-host.service`, `127.0.0.1:8090` (`BIND_ADDR`) |
| nginx | TLS (wildcard cert plus one `*.<handle>` cert per person), hostname routing, serves the legacy content host and custom-domain files from disk, writes the analytics log | `/etc/nginx/sites-enabled/*` (repo copies in `deploy/prod/`) |
| Postgres 16 | Accounts, sites, versions, state, collections, sessions, OAuth, aggregates | database `simplehost`, role `simplehost`; schema `db/schema.sql` |
| Site files | Versioned folders on local disk | `/srv/simple-host/sites` (`DATA_DIR`) |
| Geo DB | DB-IP Lite country files, read on this box | `GEOIP_DIR`, refreshed by `simple-host-geoip-refresh.timer` |
| Email | Resend transactional mail; Hack sends write recipient-free accepted/failure journal records | `internal/email`, `scripts/hack-mail-report.py` |
| Event DNS | Vercel DNS API, hands hackathon organisers hostnames; off unless configured | `internal/eventdns` |

There is no object store, CDN, queue, frontend build or notification service. simple-hack.app
is the same binary behind its own nginx vhost (`/` proxies to `/hackathons`).

Hosted Simple Hack's `/get-started` onboarding is `internal/handler/static/hack-get-started.html`,
served through the existing CSP and shared Hack chrome by `hack_get_started.go`.
Native exclusive `<details>` rows explain OAuth setup; hash links open the matching AI. FAQ
answers use native accordions too. The page offers Copy buttons for connector addresses, the five-skill
coding-agent install command and role prompts, and keeps downloads/raw skill links
in a closed `<details>` section. The archives and `/v1/skills/` handlers are unchanged.
`node scripts/check-hack-get-started.mjs` checks the served page at 320, 390 and
1280 px in both system themes, Copy buttons and fallbacks, CSP, and local-only
resource loads; `HACK_GET_STARTED_URL` selects a preview instead of the live page.

Simple Host’s landing is `static/host-story.html` (2026-10-10: a click-to-play video
whose files live in `static/film/` and are served by `film.go` at `/film/`), separate
from the `index.html` dashboard shell. Root sign-in/build queries redirect to `/dashboard`.
`chrome.go` appends `host-ink.css` after page layout styles when rendering Host
app pages; Hack keeps `hack-ink.css`. Bare status pages use `host-status.css`,
with an embedded font and no network loads. The passcode CSP allows only the
existing script hash, both stylesheet hashes and the embedded font. No deployed
user HTML passes through this theme path. First-boot setup serves the local
stylesheet/fonts. Inventory and checks: `docs/history/simple-host-ink-theme-2026-10-05.md`.

## Hosts and how a request flows

The hosted base move is paused (2026-10-09): `SITE_BASE_MOVE=off`, with
`SITE_BASE_DOMAIN` retained for the app's 301 back to matching `SITE_DOMAIN`
hosts, path and query kept. `SiteBaseHosts` handles this before person/site
routing. nginx keeps the old TLS proxy blocks and certificates for shared links;
the script's `HTTP_MODE=app` sends HTTP straight to .app. `.site` issuer units stay disabled. See
`docs/operations/site-base-paused.md` (revisit about 2026-11-09).

Every hostname reaches the binary through nginx on `127.0.0.1:8090`. Inside, the handler chain
is (`cmd/server/main.go`):

`FamilyHosts` → `SiteBaseHosts` → `BoundSubdomains` → `SiteHosts` → `PersonHosts` →
`LegacyHostRedirect` → app, where app is
`SecurityHeaders(CORS(apiMetrics(BearerAuth(mux))))`.

**Apex `simple-host.app` (and simple-hack.app).** nginx proxies everything to the app except
`/internal/` (404 from outside). The single
`ServeMux` answers the API (`/v1/...`), the pages (`ui.go`: `/`, `/dashboard`, `/admin`,
`/features`, `/hackathons`, `/enterprise*`, `/terms`, `/support`, `/analytics/{site}`), the
skill downloads (`/skills.zip`, `/plugin.zip`, `/install.sh`, `/.well-known/skills/...`), the
connector (`/mcp`, `/oauth/*`, `/.well-known/oauth-*`) and `/healthz`, `/readyz`. Agents write
here with `X-API-Key` or a connector bearer token.

**Site hosts `<site>.<handle>.simple-host.app`** (`sitehost.go`, live 2026-09-26). Every site
is its own origin: `SiteHosts` serves the site's live files at the root, `/v1/` answers for that
one site only, and visitor sign-in, sessions and private collections are bound to that host. A
two-label name needs its own certificate, `*.<handle>.simple-host.app`: the app drops
`SITE_CERT_DIR/requests/<handle>`, a root-owned issuer (`deploy/site-certs/`, run by a path
unit when a request lands and a 10-minute timer; queue bounds of 10000/week, 1000/day and 30/run; LE rate limits fall back to Google
Trust Services then ZeroSSL with shared Google request/order pacing; certbot DNS-01 through the Vercel hooks in
`/usr/local/lib/certbot-vercel/`) issues it, copies it for nginx and writes
`SITE_CERT_DIR/ready/<handle>`; the nginx server for `<site>.<person>.simple-host.app` loads the
cert by variable. The app never hands out or redirects to a site host before its ready marker
exists; until then that person's sites keep the person-path form. Mode comes from `SITE_HOSTS`:
`off` (default; event and self-hosted boxes), `serve` (answer, hand out person-path URLs),
`canonical` (live: the site host is the address handed out). Needs `PERSON_HOSTS` on.

**Custom domains** (`domains.go`, `domaincheck.go`, `domaincert.go`). A bound domain is
re-checked every 2 minutes (hourly once active). Once it resolves here but HTTPS does not answer,
the app drops `DOMAIN_CERT_DIR/requests/<domain>`; a root-owned issuer (`deploy/domain-certs/`,
path unit + 10-minute timer, at most 50 new certificates a day) checks the A/AAAA records, runs
certbot HTTP-01 against the webroot every port-80 server already answers (`/var/www/acme`),
writes `sites-enabled/simple-host-domain-<domain>` from its template, reloads nginx and writes
`ready/<domain>`, or `failed/<domain>` with one line the app shows as `last_error`. The issuer
only issues while the domain's link in `/srv/simple-host/sites/domains/` exists and removes its
own server (and certificate) once the link is gone; hand-made servers (any other enabled file whose `server_name` names the domain) are
left alone. A site keeps serving at its earlier own address (`previous_domain`) until a new
domain is verified; a verified domain failing for 24 h emails the owner, and after 72 h its
verification is cleared. A claimed `<name>.simple-host.app` the site lets go is kept in
`legacy_hostnames` and redirects to the site (`legacyhost.go`).

**Address families `<label>.<suffix>`** (`familyhost.go`, `familyapi.go`, `db/families.go`;
design `docs/designs/address-families.md`). An account connects `*.<suffix>`; its site named
`<site_prefix><label>` answers at `<label>.<suffix>`. The host alone names the account and the
site, so no other account's site is reachable there. A family is verified by its TXT record
(or the admin's proof exemption) plus the wildcard resolving only here, re-checked every 10
minutes (hourly once active); once verified the app links `/srv/simple-host/sites/families/<suffix>`
→ `by-id/<user_id>` and writes `ADDRESS_FAMILY_CERT_DIR/requests/<suffix>` (token, link target,
certificate mode and lineage, site prefix, reserved labels). A root-owned issuer
(`deploy/family-certs/issue.sh`, installed as `/usr/local/sbin/simple-host-family-certs`, state
`/var/lib/simple-host-family-certs`, path unit + 10-minute timer) checks that the operator's
wildcard lineage covers `*.<suffix>` (it never issues, renews or deletes a certificate; certbot
renews it as usual), refuses a family any other nginx server answers, writes
`sites-enabled/simple-host-family-<suffix>` from `vhost.conf.template`, reloads nginx and writes
`ready/<suffix>`. The app treats a family as live only while that marker's prefix matches.
nginx serves files straight from `families/<suffix>/<prefix>$label/current` with the
take-down, offline, passcode and lives-elsewhere markers, and sends `/v1/`, `/internal/` and
misses to the app, where `FamilyHosts` (outermost in the chain) answers them: the API bound to
that one site, renamed labels (302 to the new one), missing sites (404), and a site with a
domain of its own (302 there). The main-address ranking (domain > most specific canonical family
> site host) decides where the site host redirects and where sign-in and saves happen.
`deploy/prod/family-adopt.sh` moves a hand-made wildcard vhost to the managed file (dry run by
default, `--apply`, `--rollback`): ready marker first, then it waits for the admin API's `live`
(the routing index), then swaps. A host under a proven family (verified or proof-exempt) that is not live is a plain 404.

**Person hosts `<handle>.simple-host.app`** (`personhost.go`). The wildcard vhost
proxies to the app; `PersonHosts` recognises the handle (aliases such as `admin` →
`simple-host-team` resolve too). `/` is the person's page of public sites. `/<site>/...` 302s
to the site host (path and query kept) once the person's certificate is ready; before that it
is the site's live files served by Go (relative links keep working), with `/v1/` host-bound to
that person's sites. A site with its own domain 302s to it. Mode comes from
`PERSON_HOSTS`: `off` (default, event and self-hosted boxes: path model only), `serve` (answer,
but hand out path URLs), `canonical` (live).

**Legacy `sites.simple-host.app/<handle>/<site>/`.** nginx still owns this host; the redirect
below has been live since 2026-09-25 16:26 UTC. A path with a
`domain-redirect` marker file in the site folder is rewritten to
`/internal/domain-redirect/...` (302 to the custom domain). Every other
`/<handle>/<site>/...` is rewritten to `/internal/site-redirect/...`, which 302s
(`Cache-Control: no-store`) to the site's live address (site host, or person path while the
certificate is pending), path and query kept. Exception:
`vineetu/eb2-wait` is still served from disk here because it calls the content host's
`/eb2-api/*` legacy integration proxies (`contentHostOnlySites` in `personhost.go` and the nginx block
agree on it). `/<handle>` redirects to the person page. `/v1/` on this host is kept for
good: old pages and agents call it. Reads there stay open; writes need an API key (no visitor
sign-in is offered on a host every site shares) when `PERSON_HOSTS=canonical`. The 302 becomes
301 after a quiet soak
(`docs/designs/per-person-subdomains-nginx.md`).

**Claimed `<name>.simple-host.app`** (`platformsubdomain.go`). A site claims a free name with
`POST /v1/sites/{site}/domain`; it is verified at once. `BoundSubdomains` serves it in process
like a custom domain: files at the root, `/v1/` same-origin, visitor sign-in. Unclaimed
single-label names that match an old per-name host get a 301 from `LegacyHostRedirect`
(`legacyhost.go`).

Off its custom domain (see **Custom domains** above), writes for that site answer 401
`use_custom_domain`.

**MCP and OAuth** (`connector.go`, `internal/mcp`). An OAuth 2.1 authorization server
(dynamic client registration, PKCE, consent page `static/connect.html` reusing the normal
Google or email-code sign-in) guards a Streamable HTTP MCP endpoint at `/mcp`. Each tool call is
replayed in process into the bare mux, so it meets exactly the REST checks. Account keys are
stored only as hashes, so the replay carries a per-request internal credential (`shint_…`,
`internal/db/internalkey.go`: in memory only, revoked when the request ends, 15 min TTL backstop)
instead of the person's key. `BearerAuth` does the same for connector tokens on `/v1/`. Hand-registered clients:
`simple-host oauth-client create`. Reviewer password sign-in (`reviewer.go`) and the OpenAI
domain challenge are off unless configured.
Hosted Simple Hack filters the old storage tools and chat-facing credential
creation from MCP discovery and direct calls. A selected team's current
website tools still use its separate scoped credential and REST permissions;
Simple Host continues to expose its deprecated legacy tools.

**Owner sign-in.** Email code (`/v1/auth`, `/v1/auth/verify`, Resend) or Google
(`/v1/auth/oauth/{provider}`) returns a new API key (each sign-in issues one; `api_keys` keeps
only its SHA-256; rotate replaces them all); the dashboard keeps it and sends `X-API-Key`. The
admin is a real `users` row upserted at boot and authenticates with `ADMIN_API_KEY`.

**Visitor sign-in** (`visitorsession.go`, `visitoremail.go`, `oauth.go`, `static/auth.js`).
Only on a site's own address (site host, person-path fallback, claimed name, custom domain); a
session covers that one site. Google: `SH.signIn()` starts on the site host
(`/v1/visitor/oauth/{provider}`), which sets a 10-minute `__Host-sh_vnonce` cookie there and
stores only its SHA-256 with the OAuth state (an apex start for a site is redirected there). The
callback lands on the apex, mints a one-time token carrying that hash, and redirects to
`<site host>/v1/visitor/establish?once=...`, which sets the host-only `__Host-sh_vsess` cookie
only in the browser holding the nonce (the code is burned on any failure). So a sign-in finished
in one browser cannot be completed in another (login CSRF).
Email: `POST /v1/sites/{site}/visitor/auth` + `/verify` on the site host; codes are bound to
that one site. Pages include `/auth.js` (`window.SH`) and call `SH.requireSignIn()` before
saving; `GET /v1/sites/{site}/me` reports the session without extending it.
Hosted Simple Hack serves the same visitor sign-in and `SH.storage` helpers,
but strips the old `SH.state`, `SH.collection` and `SH.data` methods.

**Simple Host legacy writes and reads.** State (`stateops.go`) and collections (`collections.go`) are readable by
anyone: a GET with no Origin/Referer is served; one from a page is Origin/Referer-gated (private
lists refuse the former). Writes pass `visitorWriteOK`: the site owner's API key (or the admin's; connector tokens and MCP arrive as the person's in-process key), or
a visitor session plus `X-SH-CSRF: 1` on the site's own address. On the old shared host a key
is the only way in when `PERSON_HOSTS=canonical`; on event and self-hosted instances
(`off`/`serve`) the shared host is where pages live and its writes stay open. Private collections
(`privatecollections.go`): only signed-in visitors on the site's address submit, the owner or
admin reads all, and a visitor reads only their own entries. Personal (`mine`) records are read
and written only by their person; Shared boards (`board`) are edited item by item with a version
check.
Hosted Simple Hack instead answers 410 for state, collection, declared-data,
saver and history routes on both site and handle aliases. `HackLegacyStorageGate`
wraps public requests and the connector's in-process REST calls; event hosts
also keep their narrower `/v1/` allowlist. Historical data stays in owner
recovery exports without an active legacy API.

## Code map

- `cmd/server/main.go` — wiring: config, schema check, setup mode, admin row, handlers,
  middleware chain, background loops. `oauthclient.go`, `reviewaccount.go` are subcommands.
- `cmd/analytics-rebuild`, `cmd/ip-country-load` — one-off operator tools.
- `internal/handler` — every HTTP handler and the embedded web UI (`static/`, hand-written
  HTML, no build step). Landmarks: `site.go` deploy, versions, rollback, route registration;
  `sitehost.go` per-site hosts and certificate requests; `personhost.go` person hosts and `/internal/site-redirect`; `platformsubdomain.go` claimed
  names and in-process file serving; `domains.go` custom domains, `/internal/domain-redirect`,
  `/internal/tls-ask`; `legacyhost.go` old per-name hosts; `handles.go` the one namespace;
  `stateops.go`, `collections.go`, `privatecollections.go`, `export.go` the datastore;
  `visitorsession.go`, `visitoremail.go`, `oauth.go`, `emailcode.go`, `user.go` sign-in;
  `connector.go` OAuth server + MCP;
  `analytics.go` site traffic; `apimetrics.go` per-endpoint API counts;
  `eventdomain.go` hackathon hostnames; `setup.go` first-boot setup page; `instancehost.go`
  rewrites hostnames for instances on other domains; `ui.go`, `chrome.go`, `skillshub.go`
  pages and skill downloads; `notice_middleware.go` stale-skill notice; `ratelimit.go`,
  `cors.go`.
- `internal/mcp` — MCP JSON-RPC server, tool list, output schemas, instructions.
- `internal/db` — SQL, one file per area, `models.go` row types, `schemacheck.go` boot check.
- `internal/storage/disk.go` — the only writer of site files; owns symlinks and markers.
- `internal/analytics` — tails the nginx analytics log into aggregates, off the request path.
- Leaves: `config`, `auth` (API-key middleware; reaches `db`), `tarball`, `email`, `oauth`
  (Google; GitHub wired, unconfigured), `geoip`, `capacity`, `eventdns`, `buildinfo` (release
  and commit stamped with `-ldflags -X`).
- `db/migrations` (Go package outside `internal/`) — embeds the migration files and runs
  `simple-host migrate`: pending files in lexical order, each once, tracked in
  `schema_migrations`, under an advisory lock; historical files are a fixed baseline, never run.
- Outside Go: `db/schema.sql` (canonical, for a new database) and `db/migrations/*.sql`;
  `deploy/prod/` nginx, logrotate, journald retention, geoip timer; `simple-host-website/` the Website Deploy
  skills and plugin, embedded in the binary; `plugins/simple-host/` the Claude directory plugin
  (generated skill copies); `openai-plugin/` the ChatGPT package; `scripts/` checks and ops.

The public `/setup` Enterprise wizard (`static/setup/setup.js`) generates `values.yaml`
and a persistent Secret template for the Enterprise repository's pinned Helm chart.
Helm and Kubernetes YAML share the same chart; the latter is `helm template` followed by
`kubectl apply`. The form and generated files follow the pinned chart. No cloud provisioning runs through this flow.

## Data model

The 2026-10-02 site-storage-primitives work adds owner-configured KV, SQLite,
and raw-file resources for hosted/single-instance sites. Runtime SQLite and
files live below each site's `runtime/` directory, outside `vN` and `current`;
resource policy and KV values use additive PostgreSQL tables. A decimal
10,000,000-byte allowance per website (Simple Hack: 1,000,000) pools KV key bytes plus normalized value bytes, SQLite
main-file allocation after checkpoint, and raw file bytes. The owner usage
route reports those same counters and the remaining allowance. WAL, deployment
versions and legacy saved data remain separately accounted for. Site rename,
trash, restore and deletion move the directory; site/account exports include
runtime files plus resource declarations and KV. The active contract,
passcode/visitor policy, isolation rules and measured size comparison are in
`docs/designs/site-storage-primitives.md`. Enterprise replicas/S3 do not get
local per-site SQLite through this rollout.
Simple Host keeps legacy saved-data APIs for existing sites. Hosted Simple
Hack retains any historical records for owner recovery exports but exposes
only KV, SQLite and files as active website storage.
The CGO-free SQLite driver uses ncruces/go-sqlite3's compiled Go translation
of its SQLite WebAssembly build. It needs no runtime code generation or
writable-executable memory, so it runs under both hosted systemd services'
existing executable-memory restriction.

On disk under `/srv/simple-host/sites`: `by-id/<user_id>/<site>/v<n>/` holds each upload,
`current` points at the live one; `handles/<handle>` links to `by-id/<user_id>`;
`domains/<domain>` links to a site; a `domain-redirect` file marks a site with its own domain;
a `suspended` file marks a site the operator has taken down (mirrors `sites.suspended_at` or
the owner's `users.suspended_at`; re-synced at boot). Go checks it in `serveSiteFile`; nginx
(custom domains, content host) and Caddy (event boxes) check it where they read files from disk
and rewrite to `/internal/suspended` (`deploy/prod/nginx-suspended-marker.sh` adds the nginx check).

Tables (`db/schema.sql`):

- `users` — every identity: owners, visitors, admin. `handle`, `display_name`.
- `users` also carries `event_account` (organiser-made accounts), suspension and `signin_alerts`.
- `api_keys` — account keys as hex SHA-256 only (`key_hash`, `user_id`); several per account;
  `scope` (full or deploy) and `expires_at`.
- `handle_aliases`, `legacy_hostnames` — old handles and retired per-name hosts, same namespace.
- `sites` — owner, name, active version, `state` JSONB + `state_version`, custom domain and its
  status, `visibility` (listing only), `allowed_origins`.
- `versions` — one row per upload.
- `collection_items`, `collection_settings` — lists, boards and Personal records (`kind`,
  `version`); private flag, `submitted_by`.
- `data_history` — every saved-data change for 30-day undo; `idempotency_keys`; `data_watch`
  (counts of the visitor writes a later step may tighten); `site_savers` (who may save, blocks).
- `site_name_aliases` — a renamed site's old names. `email_changes`, `email_change_undos`,
  `signin_alerts_sent` — sign-in email changes and alerts.
- `domain_cert_requests` — the certificate issuer's daily cap.
- `site_page_daily`, `site_referrer_daily` — top pages and referring domains.
- `auth_tokens` — email codes, bound to a purpose and, for visitors, one site; expired rows
  purged.
- `oauth_identities`, `oauth_states` — Google sign-in.
- `visitor_sessions`, `visitor_establish_tokens` — site-scoped cookies and their one-time hand-off.
- `oauth_clients`, `oauth_grants`, `oauth_codes`, `oauth_tokens` — the connector (hashes only).
- `event_domains` — hackathon hostnames. `instance_config` — answers from the setup page.
- Analytics: `site_view_hourly`, `site_visitor_hourly`, `site_geo_daily`,
  `analytics_ingest_state`; legacy `site_view_daily`, `site_visitor_daily` (never written,
  pruned after 400 days). API: `api_request_daily`, `api_ip_daily` (caller IP truncated to
  IPv4 /24 or IPv6 /48, pruned at 30 days).
- `ip_country_ranges` — reference data for visitor-analytics country counts.

## Deploy

- Binary `/usr/local/bin/simple-host`, `simple-host.service` (user `simplehost`, sandboxed,
  writes only `/srv/simple-host/sites`), env `/etc/simple-host.env` (secrets; never print it).
- Build on the box (aarch64) under a memory cap, back up the running binary to
  `/usr/local/bin/simple-host.bak-<timestamp>`, install, restart, verify from a client. Exact
  steps in `CLAUDE.md`. Rollback = install the `.bak` and restart.
- Flags in the env file: `PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical`,
  `SITE_CERT_DIR=/var/lib/simple-host-site-certs`,
  `DOMAIN_CERT_DIR=/var/lib/simple-host-domain-certs`, `WRITE_AUTH_MODE=on`, `BIND_ADDR=127.0.0.1`
  (empty = all interfaces, which Docker needs),
  `ANALYTICS_LOG`, `ANALYTICS_SALT` (visitor hash salt; empty = derived from `ADMIN_API_KEY`),
  `GEOIP_DIR`.
- Schema changes are hand-applied SQL here; add them to `db/schema.sql` and `db/migrations/`
  (idempotent, rule in `db/migrations/migrations.go`), apply before deploying, then record with
  `simple-host migrate -mark <file>`. The server never migrates on start; small boxes run
  `simple-host migrate` from `install.sh`. The binary refuses to start if a column it reads is
  missing (`schemacheck.go`).
- nginx edits are by hand, with a dated `.bak` first, then `nginx -t` and reload.
- Log retention matches the privacy page's 30 days: `deploy/prod/logrotate-analytics.conf`
  (installed as `/etc/logrotate.d/simple-host-analytics`; live day + 29 daily archives) and
  `deploy/prod/journald-retention.conf` (installed as
  `/etc/systemd/journald.conf.d/30-retention.conf`, `MaxRetentionSec=30day`, box-wide).
  `analytics-rebuild` replays those archives, so a rebuild reaches back about 30 days.

## Invariants

- **Visitor IPs never leave the box.** Site analytics keep a salted hash plus country counts;
  API caller IPs are stored truncated (/24, /48) for 30 days and located from local files only;
  the analytics log drops the query string. No IP goes to a
  geolocation service or any third party.

- **No client-side analytics.** Nothing is injected into hosted pages; traffic comes from the
  server's own access log.
- **API keys are never stored in plaintext.** Only SHA-256 in `api_keys`; in-process callers
  use a per-request internal credential, never a stored key.
- **Apex pages run no inline script without a nonce.** `script-src` carries a per-response
  nonce (`adminUICSP`, `consentHeaders`), no `unsafe-inline`; inline `on*=` handlers are
  blocked, so pages bind events with `addEventListener`.
- **Hosted pages never hold an API key.** Everything a page does works with the site-scoped
  cookie. Middleware reads only `X-API-Key` (or a connector token); a site session is never
  owner power.
- **Reads are public; every page write needs an identity.** The private things are private
  Submissions and Personal records. The one view-lock is a single passcode on a whole site
  (`h/passcode.go`, 2026-09-29): a shared code, not a login; no per-page lock, no per-person
  viewer list.
- **A site with a domain lives only there.** 302 (not 301) so disconnecting takes effect at once.
- **One namespace** for handles, claimed names, reserved names and retired hosts.
- **Never hand out a site host without its certificate.** A TLS name mismatch cannot be fixed
  after the handshake, so URLs and redirects use `<site>.<handle>` only once
  `SITE_CERT_DIR/ready/<handle>` exists.
- **`internal/handler` is the only package that imports other internal packages** (plus `auth`
  → `db`). Enforced by `scripts/check-layering.sh`.
- **`openapi.yaml` is the shared API contract;** `openapi.json` is generated from it.
  Each operation carries `x-audience` (`public-host`, `public-hack`, `internal`, `legacy`).
  `internal/handler/hack_docs.go` filters operations and unused tags/components at the served
  `/openapi.json` and `/openapi.yaml` URLs (JSON is valid YAML 1.2). The docs page
  loads that filtered reference. Internal and legacy (older saved-data) operations stay in the source for
  coverage; each product’s `llms.txt` describes its public API.

## Traps

- **One payload, two pages.** `index.html` and `showcase.html` both render analytics; check
  both on any response-shape change. The analytics parser is shared and checked verbatim.
- **Two origins.** `showcase.html` is served on the apex and on the content host, so shared
  frontend code is inlined, not linked.
- **The log tail is stateful** (inode + offset): logrotate must use `create` and
  `delaycompress`, never `copytruncate`.
- **The compiled binary is not the repo.** Nothing changes until rebuild and restart; embedded
  HTML needs a rebuild too.

## Checks

`make check` runs gofmt, build, vet, `go test ./...` and `scripts/check-html.sh`,
`check-layering.sh`, `check-docs-sync.sh`, `check-claude-plugin.sh`,
`check-fresh-install.sh`. The Makefile is what executes; this list is prose.
`scripts/check-features.sh` (every route and MCP tool is placed in `FEATURES.md`) and
`scripts/check-reserved-subdomains.sh` run on their own.

Hosted Simple Hack presentation (2026-10-04): `hack-story.html` is the home; `chrome.go` adds `hack-ink.css` only to Hack chrome, after layout styles. Fonts are local WOFF2/OFL assets, rough.js stays inline. `hack_colors.go` maps event slugs or stored palette names to consistent accents; `z8-hack-ink-color.sql` adds the optional choice. Host index/dashboard/site.css remain unchanged.

Full Simple Hack instances (2026-10-04): `hack_instance.go` rewrites only
Hack presentation/skill text to PUBLIC_BASE_URL; it never applies the older
Simple Host shared-origin/control-plane note to the full event platform.
MCP Hack instructions use the server's configured APIHost. Root token/nonce
returns redirect locally to `/signin`, preserving the existing nonce verifier.

Account homes (2026-10-05): `users.home_site_id` is a nullable site FK. `home.go`
serves the selected site's current tree at the person host root through `siteGate`;
missing paths retain legacy site-link redirects. API lookups, origin authorization,
OAuth return addresses and session checks bind to that site and the exact request host.
Unavailable sites render the showcase; deleting a site clears the FK at soft delete,
and hard delete uses ON DELETE SET NULL. Renames preserve it. The owner app stays on
its apex origin. Hosted events have no home-setting surface.

The public JSON feed `/v1/u/{handle}/showcase.json` and showcase HTML share one
projection in `showcase.go`. Metadata is parsed locally from visible deployed
indexes; hidden/protected/offline/taken-down sites never reach this path. The
feed is public, credential-free CORS, cached for 30 seconds and limited per
resolved owner ID and client IP with the public-read settings. Bio is plain text in
users.showcase_bio; site-ID pin/order settings survive rename and restore. The shared
projection sorts pinned first, ascending order, then existing creation order. All
curation writes require the owner key or connector, excluding deploy-only keys.
Whole-space custom apex domains are not implemented: the family issuer only checks
pre-provisioned wildcard certificates and its template has no account-home apex route.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [docs/your-home-page.md](docs/your-home-page.md).

Website file caps (2026-10-05): `h/storagecaps.go` checks the post-prune footprint before the shared create/update commits write any files. `storage/footprint.go` counts only current and vN folders, including Recently deleted sites in account totals; backend resources remain separate. Account deploy locks cover promotion and pruning across site names. `h/versions.go` retains actual newest rows plus any older live version and resolves allowlists/default overrides through handle aliases.

Storage story (shipped 2026-10-06): site_storage_resources.write_mode preserves
full writes by default; site_storage_kv.writer_id and site_storage_files track
writer identities. site_storage_rows.go builds bound fixed INSERT/SELECT for
visitors in add/own databases. Own schema tables have an indexed visitor_id TEXT.
Host files use SITE_STORAGE_FILES_MAX_BYTES separately from KV/SQLite. Hack keeps
its old pooled budget. No per-visitor tables/views or visitor-provided SQL.

Order history (shipped 2026-10-06): fixed add-only SQLite inserts on own-read
databases run in BEGIN IMMEDIATE. Declared foreign keys use indexed joins to
check the inserted row's actual reference (including defaults) and parent
visitor_id before COMMIT; refusals roll back the insert. Composite and implicit
primary-key references are supported; optional links must be all NULL, with partial composite NULLs refused.
Foreign keys are explicitly enabled. Parent checks use the RETURNING row ID
and allow owner-created catalog rows with NULL/empty identities. The server overrides visitor_id and stamps UTC created_at on visitor
inserts. Orders/history use separate reads; no include option.

Storage security (shipped 2026-10-06): storageWriteLock waits up to the
configured 2 s before 503 with Retry-After. Owner operations retain 5 s and
visitor writes 2 s; body reads and pure reads do not lock. Visitor write buckets
are keyed by site and IP/visitor; reads do not consume them. SQLite execution
uses a CPU-sized process semaphore with a configurable 250 ms acquisition wait;
writers take the site lock before the slot. Own-file pagination advances past
stale metadata. New keys/paths normalize to NFC; legacy exact names remain
readable/deletable. File buckets cap committed object counts, and populated
non-own SQLite databases cannot convert to own reads. Existing clientIP proxy
trust remains documented; deployments must restrict direct app access.
