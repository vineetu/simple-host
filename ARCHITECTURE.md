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
| Grok sidecar | CLIProxy: the Grok subscription as a local OpenAI-compatible API, the only model behind AI create | `cliproxy.service`, `127.0.0.1:8102` (`/opt/cliproxy`) |
| Speech-to-text | Moonshine, local; voice input in the builder chat | `moonshine-stt` `:8100`, `moonshine-stream` `:8103` |
| Geo DB | DB-IP Lite country files, read on this box | `GEOIP_DIR`, refreshed by `simple-host-geoip-refresh.timer` |
| Email | Resend, sends sign-in codes only | `internal/email` |
| Event DNS | Vercel DNS API, hands hackathon organisers hostnames; off unless configured | `internal/eventdns` |

There is no object store, CDN, queue, frontend build or notification service. simple-hack.app
is the same binary behind its own nginx vhost (`/` proxies to `/hackathons`).

## Hosts and how a request flows

Every hostname reaches the binary through nginx on `127.0.0.1:8090`. Inside, the handler chain
is (`cmd/server/main.go`):

`BoundSubdomains` → `SiteHosts` → `PersonHosts` → `LegacyHostRedirect` → app, where app is
`SecurityHeaders(CORS(apiMetrics(BearerAuth(mux))))`.

**Apex `simple-host.app` (and simple-hack.app).** nginx proxies everything to the app except
`/v1/transcribe/stream` (straight to Moonshine) and `/internal/` (404 from outside). The single
`ServeMux` answers the API (`/v1/...`), the pages (`ui.go`: `/`, `/dashboard`, `/admin`,
`/features`, `/hackathons`, `/enterprise*`, `/terms`, `/support`, `/analytics/{site}`), the
skill downloads (`/skills.zip`, `/plugin.zip`, `/install.sh`, `/.well-known/skills/...`), the
connector (`/mcp`, `/oauth/*`, `/.well-known/oauth-*`) and `/healthz`, `/readyz`. Agents write
here with `X-API-Key` or a connector bearer token.

**Site hosts `<site>.<handle>.simple-host.app`** (`sitehost.go`, live 2026-09-26). Every site
is its own origin: `SiteHosts` serves the site's live files at the root, `/v1/` answers for that
one site only, and visitor sign-in, sessions and private collections are bound to that host. A
two-label name needs its own certificate, `*.<handle>.simple-host.app`: the app drops
`SITE_CERT_DIR/requests/<handle>`, a root-owned issuer (`deploy/site-certs/`, systemd timer every
10 min, weekly budget 40, certbot DNS-01 through the Vercel hooks in
`/usr/local/lib/certbot-vercel/`) issues it, copies it for nginx and writes
`SITE_CERT_DIR/ready/<handle>`; the nginx server for `<site>.<person>.simple-host.app` loads the
cert by variable. The app never hands out or redirects to a site host before its ready marker
exists; until then that person's sites keep the person-path form. Mode comes from `SITE_HOSTS`:
`off` (default; event and self-hosted boxes), `serve` (answer, hand out person-path URLs),
`canonical` (live: the site host is the address handed out). Needs `PERSON_HOSTS` on.

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
`/eb2-api/*` sidecar proxies (`contentHostOnlySites` in `personhost.go` and the nginx block
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

**Custom domains** (`domains.go`, `domaincheck.go`). Bind with `POST /v1/sites/{site}/domain`;
the binding is provisional until DNS proves it (24 h expiry, can be taken over until verified).
On bind, `storage` makes `domains/<domain>` a symlink to the site and writes the
`domain-redirect` marker. Serving needs a per-domain nginx vhost plus a Let's Encrypt cert,
added by the operator (`deploy/prod/nginx-customdomain.example.conf`): files straight from
`domains/<domain>/current`, `/v1/` and `/internal/` proxied to the app. Off the domain, writes
for that site answer 401 `use_custom_domain`.

**MCP and OAuth** (`connector.go`, `internal/mcp`). An OAuth 2.1 authorization server
(dynamic client registration, PKCE, consent page `static/connect.html` reusing the normal
Google or email-code sign-in) guards a Streamable HTTP MCP endpoint at `/mcp`. Each tool call is
replayed in process into the bare mux, so it meets exactly the REST checks. Account keys are
stored only as hashes, so the replay carries a per-request internal credential (`shint_…`,
`internal/db/internalkey.go`: in memory only, revoked when the request ends, 15 min TTL backstop)
instead of the person's key. `BearerAuth` does the same for connector tokens on `/v1/`. Hand-registered clients:
`simple-host oauth-client create`. Reviewer password sign-in (`reviewer.go`) and the OpenAI
domain challenge are off unless configured.

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

**Writes and reads.** State (`stateops.go`) and collections (`collections.go`) are readable by
anyone (GETs are Origin/Referer-gated). Writes pass `visitorWriteOK`: any account's API key, or
a visitor session plus `X-SH-CSRF: 1` on the site's own address. On the old shared host a key
is the only way in when `PERSON_HOSTS=canonical`; on event and self-hosted instances
(`off`/`serve`) the shared host is where pages live and its writes stay open. Private collections
(`privatecollections.go`): only signed-in visitors on the site's address submit, only the owner
or admin reads, everyone else gets 404.

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
  `connector.go` OAuth server + MCP; `generate.go`, `generate_jobs.go` AI create;
  `transcribe.go` voice; `analytics.go` site traffic; `apimetrics.go` per-endpoint API counts;
  `eventdomain.go` hackathon hostnames; `setup.go` first-boot setup page; `instancehost.go`
  rewrites hostnames for instances on other domains; `ui.go`, `chrome.go`, `skillshub.go`
  pages and skill downloads; `notice_middleware.go` stale-skill notice; `ratelimit.go`,
  `cors.go`.
- `internal/mcp` — MCP JSON-RPC server, tool list, output schemas, instructions.
- `internal/db` — SQL, one file per area, `models.go` row types, `schemacheck.go` boot check.
- `internal/storage/disk.go` — the only writer of site files; owns symlinks and markers.
- `internal/analytics` — tails the nginx analytics log into aggregates, off the request path.
- Leaves: `config`, `auth` (API-key middleware; reaches `db`), `tarball`, `email`, `oauth`
  (Google; GitHub wired, unconfigured), `geoip`, `capacity`, `eventdns`.
- Outside Go: `db/schema.sql` (canonical) and `db/migrations/` (history, applied by hand);
  `deploy/prod/` nginx, logrotate, geoip timer; `simple-host-website/` the Website Deploy
  skills and plugin, embedded in the binary; `plugins/simple-host/` the Claude directory plugin
  (generated skill copies); `openai-plugin/` the ChatGPT package; `scripts/` checks and ops.

## Data model

On disk under `/srv/simple-host/sites`: `by-id/<user_id>/<site>/v<n>/` holds each upload,
`current` points at the live one; `handles/<handle>` links to `by-id/<user_id>`;
`domains/<domain>` links to a site; a `domain-redirect` file marks a site with its own domain.

Tables (`db/schema.sql`):

- `users` — every identity: owners, visitors, admin. `handle`, `display_name`.
- `api_keys` — account keys as hex SHA-256 only (`key_hash`, `user_id`); several per account.
- `handle_aliases`, `legacy_hostnames` — old handles and retired per-name hosts, same namespace.
- `sites` — owner, name, active version, `state` JSONB + `state_version`, custom domain and its
  status, `visibility` (listing only), `allowed_origins`.
- `versions` — one row per upload.
- `collection_items`, `collection_settings` — append-only lists; private flag, `submitted_by`.
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
  `SITE_CERT_DIR=/var/lib/simple-host-site-certs`, `WRITE_AUTH_MODE=on`, `BIND_ADDR=127.0.0.1`
  (empty = all interfaces, which Docker needs), `LLM_BASE_URL` (the sidecar), `TRANSCRIBE_URL`,
  `ANALYTICS_LOG`, `ANALYTICS_SALT` (visitor hash salt; empty = derived from `ADMIN_API_KEY`),
  `GEOIP_DIR`.
- Schema changes are hand-applied SQL; add them to `db/schema.sql` and `db/migrations/`.
  The binary refuses to start if a column it reads is missing (`schemacheck.go`).
- nginx edits are by hand, with a dated `.bak` first, then `nginx -t` and reload.

## Invariants

- **Visitor IPs never leave the box.** Site analytics keep a salted hash plus country counts;
  API caller IPs are stored truncated (/24, /48) for 30 days and located from local files only;
  the analytics log drops the query string. No IP goes to a
  geolocation service or any third party.
- **AI create is the Grok sidecar only.** One provider, no fallback, no metered API keys. If
  the sidecar is down the feature fails honestly.
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
- **Reads are public; every page write needs an identity.** The one private thing is a private
  collection. No view-lock, no private pages.
- **A site with a domain lives only there.** 302 (not 301) so disconnecting takes effect at once.
- **One namespace** for handles, claimed names, reserved names and retired hosts.
- **Never hand out a site host without its certificate.** A TLS name mismatch cannot be fixed
  after the handshake, so URLs and redirects use `<site>.<handle>` only once
  `SITE_CERT_DIR/ready/<handle>` exists.
- **`internal/handler` is the only package that imports other internal packages** (plus `auth`
  → `db`). Enforced by `scripts/check-layering.sh`.
- **`openapi.yaml` is the API contract;** `openapi.json` is generated from it.

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
