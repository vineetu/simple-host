# FEATURES — blast-radius map

The one map to read **before changing anything**: every feature area, and every surface it
touches (routes, MCP tools, skill sections, pages, Go files, tables, env, outside services).
Derived from the code on `feat/capacity-and-hackathon-page` (last checked @ 5f03e6e), not from older docs.
Why the product works this way lives in `INTENT.md`; how it is built lives in `ARCHITECTURE.md`.

**Update this file in the same commit as any feature change**: a new or removed route, tool,
page, table, env flag or skill section. `scripts/check-features.sh` (run by
`scripts/check-docs-sync.sh`, so by `make check`) fails if a registered route path or an MCP tool
name is missing here.

Conventions:
- Paths are relative to the repo root. `h/` = `internal/handler/`, `st/` = `internal/handler/static/`,
  `sk/` = `simple-host-website/skills/` (the skills source; `plugins/simple-host/skills/` is a
  generated copy, and `openai-plugin/skills/` is a separate rewrite).
- **Status**: `live` = in the running binary and enabled in `/etc/simple-host.env`; `flag` = off
  unless the named env is set; `planned` = decided in INTENT, not built.
- Both site-data API forms exist for every page-facing route: `/v1/sites/{site}/…` (the site is
  resolved from the Host: site host, person host, claimed name or custom domain) and
  `/v1/u/{handle}/sites/{site}/…` (explicit owner; on a person host the handle must be the
  host's owner). Rows below list the first form and mark `(+/v1/u)` where both are registered.
  The `/v1/u/{handle}/sites/{sitename}/…` registrations:
  `/v1/u/{handle}/sites/{sitename}/state` (GET, PUT, PATCH, OPTIONS) ·
  `/v1/u/{handle}/sites/{sitename}/collections/{coll}` (GET, POST, OPTIONS) ·
  `/v1/u/{handle}/sites/{sitename}/collections/{coll}/items/{id}` (PATCH, DELETE) ·
  `/v1/u/{handle}/sites/{sitename}/me` (GET, OPTIONS) ·
  `/v1/u/{handle}/sites/{sitename}/visitor/auth` (POST, OPTIONS) ·
  `/v1/u/{handle}/sites/{sitename}/visitor/auth/verify` (POST, OPTIONS).
- Owner auth = `X-API-Key` via `authMiddleware` (`internal/auth/middleware.go`), or a connector
  Bearer token that `connector.BearerAuth` turns into a per-request internal key.
- Cross-cutting on every request: `SecurityHeaders` → `CORS` (`h/cors.go`) → `apiMetrics.Wrap`
  → `connector.BearerAuth` → mux, behind `BoundSubdomains` → `SiteHosts` →
  `PersonHosts` → `LegacyHostRedirect` host routing (`cmd/server/main.go`). JSON owner routes also go through
  `NoticeMiddleware` (stale-skill `_notice`, `h/notice_middleware.go`).

---

## 1. Sites and deploy

Upload a static site (tar.gz/zip or inline JSON files); versions, rollback, rename, delete,
export, public/unlisted listing. Delete is recoverable: the site goes offline at once and stays in
Recently deleted for 7 days (row kept with `deleted_at`, files moved to `DATA_DIR/deleted/<user>/<site_id>`,
name, saved data, collections and claimed names kept), `restore` brings it back whole, and an
hourly in-process sweep purges it after the window (except a taken-down site, or one of a suspended account: kept until the operator acts). Deleting a whole account (the person, or the admin) is immediate (§7).
Rename keeps old links: the old name's site host, person path and `sites.*` path 302 to the site's
current address (path and query kept, chains followed) until a site of that name is created again
(the new site wins); a site in Recently deleted is skipped and purge removes its old names.
Offline: the owner can take a site offline (`sites.offline_at`, an `offline` marker in the site
folder): every address answers a plain "This site is offline" page (503, no-store, noindex), visitor
saves answer 403 `site_offline`, it leaves the person page, and nothing is deleted; the owner's key
and connector still deploy, read and write. An operator take-down wins; the marker follows rename,
delete and restore and is re-synced at boot.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST`/`PUT /v1/sites/{sitename}` (archive) · `POST`/`PUT /v1/sites/{sitename}/files` (inline JSON, base64 allowed) · `GET /v1/sites` · `PATCH /v1/sites/{sitename}` (`{"name"}` renames, `{"offline"}` takes offline / back online) · `DELETE /v1/sites/{sitename}` (to Recently deleted; refused while taken down) · `POST /v1/sites/{sitename}/restore` · `GET /v1/me/deleted-sites` · `GET /v1/sites/{sitename}/versions` · `GET /v1/sites/{sitename}/versions/{version}/files` · `GET /v1/sites/{sitename}/versions/{version}/files/{path...}` · `PUT /v1/sites/{sitename}/active-version` · `PUT /v1/sites/{sitename}/visibility` (`public`/`unlisted`) · `GET /v1/sites/{sitename}/export.tar.gz` (files + saved data; `collections.json` entries carry `id`, `created_at`, `submitted_by` on private lists, `data`) · `POST /v1/sites/{sitename}/export-link` (owner mints a 10-minute signed link) · `GET /v1/export` (`?token=`; the same archive, no key: HMAC over owner+site+expiry, per-process key, 404 `export_link_invalid` when expired, tampered, or the site is gone, deleted or changed hands) · `GET /v1/sites` also returns `deployed_at` and, for a domain not live yet, `domain_last_error`, `domain_dns`, `domain_dns_txt`, `domain_expires_at` · `GET /internal/notfound` (branded 404, nginx `@notfound`; a renamed site's old name 302s) · `GET /internal/offline` (the offline page nginx and Caddy hand off to) |
| MCP tools | `list_sites`, `get_site`, `read_site_file`, `create_site`, `update_site`, `list_versions`, `rollback_site`, `delete_site`, `list_deleted_sites`, `restore_site`, `rename_site`, `set_visibility`, `set_site_offline`, `export_site` (download link) |
| Skill | `website-deploy/SKILL.md` §Two ways to deploy, §The one rule that breaks sites (relative links), §Rules that always apply, §Completion standard · `references/packaging-and-validation.md` (Package, Upload, Verify) · `references/operations.md` §Listing, §Rename, §Rollback, §Delete and restore, §Download a copy · `references/frameworks.md` · `website-deploy-builder/SKILL.md` §Capability tree 1 |
| Pages | owner app `st/showcase.html` (site inventory with live address, version and last deploy; versions, rename, visibility, Take offline / Put back online, Download (export), taken-down sites with the reason, delete with a count of what goes and "Download first", Recently deleted with Restore; an admin opening someone else's page sees their sites read-only, Open only) · `st/index.html` at `/dashboard` (site cards; for an account with a handle only a list with Manage links to the owner app; full controls for accounts without one and in the admin tab) · `st/notfound.html` |
| Go | `h/site.go` (create/update/list/rename/visibility, route table), `h/offline.go` (PATCH dispatch, offline switch and page), `h/deleted.go` (delete, restore, Recently deleted list, purge sweep), `h/versions.go`, `h/versionfiles.go`, `h/export.go`, `h/exportlink.go`, `h/sitename.go`, `h/usage.go` (per-site cap), `internal/tarball/{extract,sanitize,validate}.go`, `internal/storage/disk.go` (by-id layout, `handles/` symlinks), `internal/storage/trash.go` (`deleted/` area), `internal/db/queries.go`, `internal/db/deleted.go` |
| DB | `sites` (`deleted_at`: every serving and listing lookup skips deleted rows; `offline_at`), `versions`, `site_name_aliases` (old names of renamed sites; `internal/db/sitenames.go`) |
| Limits | 100 sites per account (admins exempt; sites in Recently deleted count, so restore needs no check); uploads, rollback, rename, delete, restore and take-down serialised per account+site; upload limiter 30 burst, 0.1/s; delete/rename/restore limiter 30 burst, 0.5/s; Recently deleted keeps a site 7 days |
| Env | `DATA_DIR`, `MAX_ARCHIVE_MB`, `KEEP_VERSIONS`, `DEPLOY_SCRIPT`, `PREVIEW_ACCOUNTS`, `PREVIEW_TTL_HOURS` (preview-site expiry sweep) |
| External | nginx serves files from `/srv/simple-host/sites/handles/<h>/<s>/` on the content host; Caddy does the same on event instances (`deploy/compose/Caddyfile`); both test the `offline` marker after the `suspended` one and hand off to `/internal/offline` (issuer template `deploy/domain-certs/vhost.conf.template`; live vhosts via `deploy/prod/nginx-suspended-marker.sh`) |

## 2. Per-site and per-person addresses, and legacy redirects

Every site lives at its own origin, `https://<site>.<handle>.simple-host.app/` (files at the
root, `/v1/` for that site only, sign-in, per-person saves and private collections bound to that
host; a sign-in covers that one site). Every handle is a host too:
`https://<handle>.simple-host.app/` lists that person's public sites. Each person gets a
certificate for `*.<handle>.simple-host.app`, issued automatically (usually within ~10 minutes of
their first site; brand-new accounts may queue behind the weekly and daily issuance caps); until it exists their
sites keep the person-path form `<handle>.simple-host.app/<site>/`, and every URL handed out is
whichever address is live. Old `<handle>.simple-host.app/<site>/…` and
`sites.simple-host.app/<handle>/<site>/…` links 302 to the site host (path and query kept). A
renamed site's old name keeps redirecting on all three forms until the name is reused (§1).
**Status: live** (`PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical`, 2026-09-26; design:
`docs/designs/per-site-subdomains.md`).

| Surface | Details |
|---|---|
| Routes | Host-routed, not mux: `<site>.<handle>.<SITE_DOMAIN>` → `SiteHosts` (files at `/`, `/v1` same-origin for that one site only) · `<handle>.<SITE_DOMAIN>` → `PersonHosts` (person page at `/`; `/<site>/…` 302s to the site host once the person's certificate is ready, else serves it by path) · `GET /internal/site-redirect/{handle}` · `GET /internal/site-redirect/{handle}/{sitename}` · `GET /internal/site-redirect/{handle}/{sitename}/{rest...}` (302 from `sites.simple-host.app/<h>/<s>/…` to the site's live address; nginx rewrites into these; `vineetu/eb2-wait` excepted in nginx and in `contentHostOnlySites`) · `LegacyHostRedirect`: a retired name (`legacy_hostnames`) 302s to its site's current address, or "This site was removed" once the site is gone or while it is in Recently deleted (its names stay held); any other unclaimed single-label `<name>.<SITE_DOMAIN>` that is not a handle 301s to `sites.<domain>/<handle>/<name>` (which then 302s as above) · an aliased old handle 301s to the new one (person host), 302s on site hosts, content-host paths and the owner app `/<old>` → `/<new>` · a renamed site's old name (`site_name_aliases`) 302s to its current address on the site host, person path, `/internal/site-redirect/*` and the content host's `@notfound` (`GET /internal/notfound` reads `X-Original-URI`) |
| MCP tools | none directly; site summaries return the live site address, `who_am_i` the person page |
| Skill | `website-deploy/SKILL.md` §Service (address form); host strings are rewritten per instance (`h/instancehost.go`) |
| Pages | `st/showcase.html` (person index / public view) |
| Go | `h/sitehost.go` (`SITE_HOSTS` off/serve/canonical, site-host routing, certificate requests and readiness), `h/personhost.go` (`PERSON_HOSTS` off/serve/canonical, `PersonPageURL`, `PersonReturnSite`, `contentHostRedirect`), `h/legacyhost.go`, `h/handles.go` (reserved handles, `assignHandle`), `internal/db/namespace.go` (one namespace for handles, claimed names, reserved and retired names; `RenameHandle`/`RenameHandleTx`, aliases, `HandleRenamedSince`), `h/instancehost.go` |
| DB | `users.handle`, `handle_aliases` (e.g. `admin` → `simple-host-team`), `legacy_hostnames` |
| Env | `PERSON_HOSTS`, `SITE_HOSTS` (needs `PERSON_HOSTS` on), `SITE_CERT_DIR` (e.g. `/var/lib/simple-host-site-certs`: `requests/<handle>` written by the app, `ready/<handle>` by the issuer), `SITE_DOMAIN`, `CONTENT_HOST` |
| External | live nginx `/etc/nginx/sites-enabled/sites-content-host` (rewrites to `/internal/site-redirect/*`) and `simple-host` (wildcard `*.simple-host.app` → app; a server for `<site>.<person>.simple-host.app` loads the per-person cert by variable); wildcard cert; per-person certs from the root-owned issuer in `deploy/site-certs/` (path unit on each request plus a 10-minute timer; at most 40 new certificates per rolling week and 12 per day; certbot DNS-01 via the Vercel hooks in `/usr/local/lib/certbot-vercel/`); Public Suffix List entry is **planned** |

## 3. Claimed `<name>.simple-host.app` and custom domains

A site takes a free `<name>.simple-host.app` (verified at once, first come) or the owner's own
domain (CNAME/A plus a TXT ownership record `_simple-host.<domain>` = the site's token,
`sites.domain_token`; provisional until both are seen, 24 h expiry if unproven and DNS never
pointed here). A custom domain counts as verified only while its TXT record matches and it
serves over HTTPS; something on this box answering the name proves nothing (domains verified
before 2026-09-27 are grandfathered while they stay verified, `domain_proof_exempt`). Once the
TXT matches and DNS points here, the domain's certificate is requested automatically (issuer in
`deploy/domain-certs/`, HTTP-01, which re-checks the TXT and the binding) and the domain goes
live without the operator; each account asks for at most 5 new domain certificates a day (the
6th waits, `certificate_status: failed` with the reason). A binding past its ownership proof
(certificate issuing, live or failed) cannot be taken over by another account. Taken-down sites
and suspended accounts get no domain checks and no certificate requests. Once active the
site lives only there; its other addresses redirect. Connecting a new domain keeps the site's
current own address serving (`previous_domain`) until the new one is verified with its
certificate; then the old address redirects to it. A verified domain that fails its checks for
24 h emails the owner (`last_error`); after 72 h it is released completely (binding, link, and
with them the issuer's server and certificate), so the site's own address serves again and the
domain can be connected afresh, with the TXT proof; while the issuer is still letting go of a
released domain, binding it answers 409 `domain_releasing`. The earlier address kept while a new
domain is pending is checked too, and let go after 72 h of failing; a new domain whose DNS points
here but never proves (its certificate keeps failing) is released after 7 days and the earlier
address comes back. `DELETE .../domain?domain=<d>` drops only that domain (409 `domain_changed`
otherwise; `remove_domain` always sends it). A free name the site lets go
(switch, disconnect, site delete) stays with it in `legacy_hostnames` and 302s to its current
address, or says "This site was removed". **Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/sites/{sitename}/domain` (bind; a `<name>.<SITE_DOMAIN>` value takes the free-name path) · `GET /v1/sites/{sitename}/domain` · `POST /v1/sites/{sitename}/domain/check` ("Check again": re-prove now; rate-limited per IP and per account, 429 `rate_limited`) · `DELETE /v1/sites/{sitename}/domain` · `GET /internal/tls-ask` (Caddy on-demand TLS gate) · `GET /internal/domain-redirect/{handle}/{sitename}` · `GET /internal/domain-redirect/{handle}/{sitename}/{rest...}` · host-routed `BoundSubdomains` serves a claimed name or custom domain at its root |
| MCP tools | `connect_domain`, `domain_status`, `remove_domain` (confirm-first: `confirm_domain`) |
| Skill | `connect-domain/SKILL.md` §The free address, §The flow (1–5, Disconnect), §Backend on a connected domain, §Gotchas · `connect-domain/references/registrars.md` (Vercel, GoDaddy, Porkbun, other) · `website-deploy/references/operations.md` §A nicer address · `website-deploy-builder/SKILL.md` §8 |
| Pages | `st/showcase.html` owner app (connect, disconnect, status; for a domain not live yet the certificate status, both DNS records (A/CNAME and TXT), last problem, expiry and Check again) · `st/index.html` (connect/disconnect on the site card, both DNS records and the last problem for a domain not live yet, accounts without a handle and admin tab) |
| API fields | `GET /domain`: `dns` (A/CNAME), `dns_txt` (TXT `_simple-host.<domain>` = token), `certificate_status`, `last_error`, `previous_domain`, `failing_since` · `GET /v1/sites`: `domain_dns`, `domain_dns_txt` for a domain not active · MCP `ownership_record` next to `dns_record` |
| Go | `h/domains.go` (bind, status, delete, `tlsAsk`, `domainRedirect`), `h/domaincheck.go` (TXT proof, background re-verify with a probe pinned to this server's public address and no redirects, switch of address, lapse + owner email), `h/domaincert.go` (certificate hand-off: request = token + link target, per-account daily cap), `h/platformsubdomain.go` (free names, `reservedSubdomainLabels`, `BoundSubdomains`, `siteOwnDomain`, `syncDomainRedirect`), `h/legacyhost.go` (retired names), `internal/db/domains.go`, `internal/db/namespace.go`, `internal/email/resend.go` (`SendNotice`) |
| DB | `sites.custom_domain`, `domain_status`, `domain_verified_at`, `domain_last_error`, `domain_bound_at`, `previous_domain`, `previous_domain_failing_since`, `domain_cert_status`, `domain_failing_since`, `domain_lapse_notified_at`, `domain_token`, `domain_proof_exempt`; `domain_cert_requests` (per-account cap); `legacy_hostnames` (`site_id` NULL once the site is deleted) |
| Env | `CNAME_TARGET` (subdomain CNAME), `CUSTOM_DOMAIN_IP` (apex A record), `SITE_DOMAIN`, `DOMAIN_CERT_DIR` (e.g. `/var/lib/simple-host-domain-certs`: `requests/<domain>` written by the app (the site's token and the domain link's target), `ready/<domain>` and `failed/<domain>` by the issuer; unset = certificates by hand) |
| Timing | unverified binding expires 24 h after bind unless DNS points here, and 7 days after bind in any case; re-check every 2 min, active verdict re-proved hourly; issuer every 10 min + on request (50 new/day for the box, 5 new/day per account in the app, failed retried after 6 h, DNS and TXT problems after 15 min); Check again: 3 at once per account, then one per 30 s; failing verified domain: email at 24 h, let go at 72 h |
| External | issuer `deploy/domain-certs/` (root timer + path unit; certbot webroot `/var/www/acme`; writes `sites-enabled/simple-host-domain-<domain>` from its template and removes it on disconnect; refuses (failed "already served here", never ready) any domain another server in the full `nginx -T` configuration would answer, by exact, wildcard or regex `server_name`, and withdraws its own server if one appears; reuses only certificates it issued (`owned/`), never another lineage in `/etc/letsencrypt/live`; checks the TXT record and that the link still points at the requesting site; skips taken-down sites; the take-down marker is checked in `location /` so `/v1/` still reaches the app; sandbox test `deploy/domain-certs/issue_test.sh`) + Let's Encrypt on prod; Caddy on-demand TLS on event instances; `scripts/check-reserved-subdomains.sh` compares reserved labels against live nginx |

## 4. Saved state (shared JSON per site)

One JSON document per site: read by anyone, written with atomic ops. On a site's own origin
(site host, person-path fallback, claimed name, custom domain) writes need a signed-in visitor (cookie + `X-SH-CSRF`)
or the site owner's API key (the admin's key writes any site; connector tokens and MCP act as
their person). Another account's key gets 404 `site not found` and writes nothing (fixed
2026-09-26; before, any account's key wrote any site). On the old shared content host (`sites.simple-host.app`) reads stay open
and writes need an API key or the connector: sign-in is never offered there, so an anonymous or
cookie write gets 401 `visitor_auth_required` (INTENT 2026-09-24, built 2026-09-26; only when
`PERSON_HOSTS=canonical`: event and self-hosted instances keep the shared host open, logged as
`outcome=public_host`). A site with its own domain takes no writes on its shared URL.
**Status: live** (`WRITE_AUTH_MODE=on`).

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/state` · `PUT /v1/sites/{sitename}/state` (whole doc, `If-Match` CAS) · `PATCH /v1/sites/{sitename}/state` (op list) · `OPTIONS /v1/sites/{sitename}/state` — all `(+/v1/u)` · `PUT /v1/sites/{sitename}/allowed-origins` (owner: extra origins allowed to call) |
| MCP tools | `get_state`, `update_state` |
| Skill | `website-deploy/references/backend.md` §Trust model, §Shared JSON state, §Saving from a page with the hosted helper, §Saving from an agent · `website-deploy-builder/SKILL.md` §Capability tree 2 |
| Pages | `st/auth.js` (`SH.state.get/put/patch`) · `st/showcase.html` owner app (saved data read-only, size against the 1 MB limit, Download JSON; the owner's key reads it from any page) |
| Ops | `set`, `inc`, `append`, `remove`, `removeWhere`; `PUT` uses ETag/`If-Match` |
| Go | `h/site.go` (`getSiteState`, `putSiteState`, `patchSiteState`, origin check `authorizeStateOrigin`), `h/stateops.go` (op set, max 100 ops), `h/visitorsession.go` (`visitorWriteOK`), `internal/db/queries.go` (`UpdateSiteStateCAS`) |
| DB | `sites.state`, `sites.allowed_origins`, `sites.allow_anonymous_writes` |
| Env | `WRITE_AUTH_MODE` (off/log/on) |
| Limits | 1 MB per state doc; `stateLimiter` 60 burst, 1/s per IP |

## 5. Collections, including private collections

Lists (comments, RSVPs, votes) that visitors append to, read by anyone. A collection can be made **private**
on a site's own origin: only signed-in visitors submit, same-origin, never by API key (server
stamps `_submitted_by`/`_submitted_at`),
only the owner (or operator) reads, and the owner may edit items. The owner (or operator) deletes
single items in any list, public included, and empties a whole list after repeating its name
(INTENT 2026-09-27); visitors only append. A list made public again keeps its submitters' emails
private: `_submitted_by` is left out of every read but the owner's (key, or signed in on the
site's own address) and the operator's (`withoutSubmitter`, `ownerBrowserView`). **Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/collections/{coll}` · `POST /v1/sites/{sitename}/collections/{coll}` · `OPTIONS /v1/sites/{sitename}/collections/{coll}` — `(+/v1/u)` · `GET /v1/sites/{sitename}/collections` (owner list) · `GET /v1/sites/{sitename}/collections/{coll}/export.csv` (owner) · `PUT /v1/sites/{sitename}/collections/{coll}/privacy` (owner) · `DELETE /v1/sites/{sitename}/collections/{coll}/items/{id}` (any list) and `PATCH` (private lists only) `(+/v1/u)` · `DELETE /v1/sites/{sitename}/collections/{coll}` `(+/v1/u)` with `{"confirm": "<coll>"}` (clear list) — owner key, connector, owner session on own origin, or admin |
| MCP tools | `list_collections`, `read_collection`, `add_to_collection`, `set_collection_privacy`, `update_collection_item`, `delete_collection_item`, `clear_collection` |
| Skill | `website-deploy/SKILL.md` §Personal details go in a private collection · `references/backend.md` §Append-only collections, §Private collections (1–3, editing, reading as owner, errors) · `references/operations.md` §Private collections · `website-deploy-builder/SKILL.md` §Capability tree |
| Pages | `st/auth.js` (`SH.collection(...).list/append/update/remove`), `st/showcase.html` owner app (every list with a public/private badge and switch, view, CSV, delete any entry, edit private entries, Clear list behind the typed name) · `st/index.html` (data tab, CSV export; accounts without a handle) |
| Go | `h/collections.go`, `h/privatecollections.go` (`onOwnDomain`, `strictVisitorSession`, `appendPrivate`, `privateManager`), `internal/db/collections.go`, `h/export.go` (collections in site export) |
| DB | `collection_items`, `collection_settings` (privacy flag) |
| Env | `WRITE_AUTH_MODE` |
| Limits | 64 KB per item; page size 50 default / 200 max; `stateLimiter`; a site with no address of its own (only on instances with `PERSON_HOSTS=off` and no domain) gets 409 `custom_domain_required` for privacy; CSV export is formula-safe |

## 6. Visitor sign-in (Google, emailed code)

A visitor on a site's own origin signs in with Google or an emailed code, gets a host-only
cookie for that origin, and page saves are made as them. `auth.js` is the documented client.
GitHub is wired but unconfigured. **Status: live** (Google configured).

| Surface | Details |
|---|---|
| Routes | `GET /v1/auth/oauth/providers` · `GET /v1/auth/oauth/{provider}` (start; a site sign-in is sent on to the site-host start) · `GET /v1/visitor/oauth/{provider}` (site-host start: sets the `__Host-sh_vnonce` browser-binding cookie, 10 min) · `GET /v1/auth/oauth/{provider}/callback` · `GET /v1/visitor/establish` (sets the site-origin cookie from a one-time token, only in the browser holding the start's nonce) · `POST /v1/visitor/logout` · `GET`/`OPTIONS /v1/sites/{sitename}/me` `(+/v1/u)` · `POST`/`OPTIONS /v1/sites/{sitename}/visitor/auth` `(+/v1/u)` · `POST`/`OPTIONS /v1/sites/{sitename}/visitor/auth/verify` `(+/v1/u)` · `GET /auth.js` (static; host-rewritten on other instances) |
| MCP tools | none |
| Skill | `website-deploy/SKILL.md` §Saving from a page: visitors sign in · `references/backend.md` §Saving from a page with the hosted helper |
| Pages | `st/auth.js` (`SH.requireSignIn`, `signIn`, `signOut`, `me`, `mount`, `ready`) |
| Go | `h/oauth.go` (provider flow, purposes, return-site checks), `h/visitorsession.go` (cookies, CSRF header, `visitorWriteOK`, `getVisitorMe`, establish/logout), `h/visitoremail.go` (site-bound email codes), `h/emailcode.go` (shared code issue/redeem + limiter), `internal/oauth/{google,github,provider}.go`, `internal/db/visitors.go` |
| DB | `oauth_identities`, `oauth_states` (+`nonce_hash`), `visitor_sessions`, `visitor_establish_tokens` (+`nonce_hash`), `auth_tokens` (purpose + site binding) |
| Env | `GOOGLE_OAUTH_CLIENT_ID`, `GOOGLE_OAUTH_CLIENT_SECRET`, `GITHUB_OAUTH_CLIENT_ID`, `GITHUB_OAUTH_CLIENT_SECRET`, `RESEND_API_KEY`, `MAIL_FROM`, `WRITE_AUTH_MODE` |
| External | Google OAuth; Resend (email) |
| Limits | OAuth `ipLimiter` 20/0.2 s⁻¹; visitor establish/logout 20/0.2; visitor email `visitorAuthLimiter` 20/0.2 plus shared per-email limiter 5/0.02 |

## 7. Owner auth (API keys, email codes, profile)

One account model. Sign in by emailed code (6 digits + dashboard-only magic link, 15 min, 3
tries) or Google (`owner` purpose → one-time token → `/v1/auth/verify`); each sign-in issues a
new API key (`shk_` prefix; older bare-hex keys keep working), stored only as SHA-256 in
`api_keys` (8c479d2), and keys from earlier sign-ins keep working; a stored key can never be
shown again. Each key has an id, a name (`dashboard sign-in`, `agent sign-in`, `event account`,
`replacement key`, or typed; NULL on keys from before 2026-09-27, shown as "Earlier key"), its
last 4 characters and `last_used_at` (stamped at most every 5 minutes on lookup). The owner
lists, mints (named, shown once) and revokes keys one at a time in the owner app's **Keys**
panel; minting, revoking and sign-out need one of the account's own keys (not the admin env key
or a connected app). Sign out (header, every page) first calls `POST /v1/me/sign-out`, which
deletes the key the browser held, then clears browser storage. Rotate ("Sign out everywhere")
replaces all keys and disconnects all connector grants. Owner-route 401s carry `code`
(`missing_api_key`, `wrong_auth_header`, `invalid_api_key`). The dashboard keeps the key in
`localStorage['apiKey']`; there is no owner cookie session.

**Your data (GDPR self-service, 2026-09-27).** "Download my data" (`GET /v1/me/export.tar.gz`,
streamed) gives one archive: `README.txt`, `account.json` (email, handle, display name, created,
old handles, claimed names incl. retired, custom domains, linked Google/GitHub), `keys.json`
(name, last4, created, last used; never keys or hashes), `connected_apps.json`, `visitor.json`
(sites signed in to as a visitor, entries sent to other people's private lists while signed in) and `sites/<name>/` per
live site (the per-site export). Analytics are left out (salted hashes, no personal data; the
README says so). "Delete my account" (`DELETE /v1/me` with `{"confirm": "<handle, or email with
no handle>"}`) is immediate and final, bypassing Recently deleted: every site (deleted ones too)
with files, versions, saved data, lists and analytics; keys; connected apps; sign-in identities;
visitor sessions; pending sign-in codes; and the entries the person sent to other people's private
lists while signed in (public-list entries and shared page data carry no link to anyone and stay;
the texts say so and point to support@simple-host.app). The
handle, old handles and claimed names are retired (`handle_aliases`/`legacy_hostnames` with
`user_id` NULL), never reusable by anyone; custom domains are unbound and their certificate
requests withdrawn. One confirmation email follows. Refused: 400 `confirm_required` /
`confirm_mismatch`, 400 `not_an_account_key` (connected app or admin env key), 403
`admin_account`, 403 `account_suspended` (the operator handles those), 409 `event_hostnames`.
The admin's `DELETE /v1/admin/users/{id}` runs the same erasure (`db.EraseAccount`). Buttons:
the owner app's **Your data** section, and `/dashboard` for accounts without a handle.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/auth` (send code) · `POST /v1/auth/verify` (code → key, optional `name`; creates the account and handle if new) · `GET /v1/me` · `POST /v1/me/api-key/rotate` (Sign out everywhere: replaces all keys) · `POST /v1/me/sign-out` (ends the calling key) · `GET /v1/me/keys` · `POST /v1/me/keys` (named key) · `DELETE /v1/me/keys/{id}` · `PATCH /v1/me` (display name, handle: free before publishing; after, once per 30 days, old handle kept as an alias so every old address redirects, new handle's certificate requested) · `GET /v1/me/export.tar.gz` (Download my data) · `DELETE /v1/me` (Delete my account, `{"confirm"}`; refused while suspended or holding a taken-down site, 403 `account_suspended`/`site_suspended`; takes every site's lock; domains and the earlier `previous_domain` unlinked and their certificate requests withdrawn only while still this account's) |
| MCP tools | `who_am_i` |
| Skill | `website-deploy/references/register.md` (email-code registration) · `references/operations.md` §API keys · `references/backend.md` §Saving from an agent (API key) |
| Pages | `st/index.html` (`/dashboard` sign-in: code, Google, paste key; Sign out everywhere), `st/showcase.html` (owner **Keys** panel `#owner-keys`; Your address, with Change; **Your data** `#owner-data`: Download my data, Delete my account with type-to-confirm), `st/index.html` **Your data** (`#my-data`, accounts without a handle), `st/privacy.html`, `st/terms.html`, `st/support.html` (point at the two buttons), `st/connect.html`, `st/partials/header.html` (Sign out → `/v1/me/sign-out`) |
| Go | `internal/auth/middleware.go` (`X-API-Key`, `shk_` keys, 401 codes, admin key, `RequireAdmin`), `h/user.go`, `h/keys.go` (list/mint/revoke/sign-out), `internal/db/apikeys.go`, `h/emailcode.go`, `h/accounts.go` (`patchMe`, handle validation), `h/account_data.go` (`exportMe`, `deleteMe`, `eraseAccountFiles`), `internal/db/account.go` (`LockAccountForDelete`, `EraseAccount`, export queries), `h/handles.go`, `internal/db/queries.go` (hashed key lookup, `ClaimHandle`), `internal/db/internalkey.go` (in-process per-request keys for the connector), `internal/email/resend.go` |
| DB | `users` (`handle_changed_at`), `handle_aliases` (`user_id` NULL = retired handle of a deleted account; `cp-gdpr-retired-handles.sql`), `api_keys`, `auth_tokens` (purpose-bound codes; expired ones purged) |
| Env | `ADMIN_API_KEY`, `RESEND_API_KEY`, `MAIL_FROM`, `PUBLIC_BASE_URL` |
| External | Resend |
| Limits | `ipLimiter` 20/0.2 s⁻¹ per IP; `emailLimiter` 5/0.02 s⁻¹ per address; at most 50 keys per account (`POST /v1/me/keys` → 409 `key_limit`); key names refuse control and invisible formatting characters; minting locks the account and the caller's key (a key revoked meanwhile gets 401 `invalid_api_key`); admin reissues are logged (`admin_key_reissue`) |

## 8. MCP connector and OAuth (chat apps)

Remote MCP endpoint behind an OAuth 2.1 authorization server (DCR, PKCE, refresh, revoke).
Sign-in on the consent page reuses Google or email code. Tool calls are replayed into the mux
as the person, so they meet the same checks as REST. Connector tokens are stored hashed.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /.well-known/oauth-protected-resource` · `GET /.well-known/oauth-protected-resource/mcp` · `GET /.well-known/oauth-authorization-server` · `GET /.well-known/oauth-authorization-server/mcp` · `POST /oauth/register` · `GET /oauth/authorize` (consent page) · `POST /oauth/authorize/decision` · `POST /oauth/token` · `POST /oauth/revoke` · `POST /oauth/reviewer-signin` · `POST`/`GET`/`DELETE /mcp` · `GET /v1/me/connections` · `DELETE /v1/me/connections/{client_id}` |
| MCP tools | all 27 (see §21); server metadata and instructions in `internal/mcp/instructions.go` |
| Skill | `website-deploy/SKILL.md` §Service, §Two ways to deploy (connector vs key); `openai-plugin/skills/website-deploy/SKILL.md` is the connector-only variant |
| Pages | `st/connect.html` (consent; own nonce CSP in `consentHeaders`), `st/showcase.html` (Connected apps) |
| Go | `h/connector.go` (AS, `BearerAuth`, `serveMCP`, connections, hourly sweep), `h/reviewer.go` (password sign-in for one designated store-review account), `internal/mcp/{server,jsonrpc,tools,outputs,instructions}.go`, `internal/db/connector.go`, `internal/db/internalkey.go`, `cmd/server/oauthclient.go` (`simple-host oauth-client …`, hand-registered clients e.g. a GPT Action), `cmd/server/reviewaccount.go` (`simple-host review-account …`) |
| DB | `oauth_clients`, `oauth_grants`, `oauth_codes`, `oauth_tokens` |
| Env | `PUBLIC_BASE_URL`, `REVIEW_ACCOUNT_EMAIL`, `REVIEW_ACCOUNT_PASSWORD_HASH`, `ADMIN_API_KEY` |
| Tokens | PKCE S256 only; code 60 s, access 1 h, refresh 90 days rotating (reuse revokes the grant); scope `sites`; tool calls run with a per-request `shint_` internal key |
| Errors | a refused tool call returns the server's message, its `code`, and one recovery hint chosen by the code (`codeHints` in `internal/mcp/tools.go`: `site_exists`, `domain_taken`, `invalid_name`, `name_reserved`, `invalid_domain`, `site_quota_reached`, `append_only`, `custom_domain_required`, `not_an_object`, private-list and sign-in codes, `site_suspended`, `account_suspended`); the HTTP status picks the hint only when there is no known code. REST errors carry the same `code` (openapi `Error` schema) |
| Limits | register 10 burst, 10/h; authorize and token 30 burst, 0.5/s; reviewer sign-in 10/IP then 1/min, 30 global then 30/h |
| Tests / e2e | `h/connector_test.go`, `h/reviewer_test.go`, `internal/mcp/*_test.go`, `scripts/e2e-connector.py`, `scripts/e2e-connector-browser.mjs`, `scripts/e2e-reviewer.py`, `scripts/seed-reviewer-demo.py` |

## 9. Skills and plugin distribution

Skills source is `simple-host-website/skills/` (embedded via `simple-host-website/embed.go`) at
version **0.20.4**, served over HTTP, packaged as a Claude plugin, an OpenAI/ChatGPT plugin, a
standalone plugin repo, and via `npx skills add vineetu/simple-host`. **Status: live**
(ChatGPT and Claude directory listings submitted 2026-09-24, pending).

| Surface | Details |
|---|---|
| Routes | `GET /skills.zip` (excludes `run-hackathon`) · `GET /skills/version` · `GET /skills/{dir}.zip` and `GET /skills/{dir}/SKILL.md` (one pair per bundled skill dir, registered in a loop) · `GET /plugin.zip` · `GET /install.sh` · `GET /install.ps1` · `GET /v1/skills` · `GET /v1/skills/{name}` · `GET /v1/skills/{name}/references/{file}` · `GET /.well-known/skills/index.json` · `GET /.well-known/skills/{name}/SKILL.md` · `GET /.well-known/skills/{name}/references/{file}` · `GET /.well-known/openai-apps-challenge` · `GET /{asset}` for each of `rewrittenAssets` (only on non-canonical instances) |
| Skills | `website-deploy` (SKILL.md + references `backend.md`, `operations.md`, `packaging-and-validation.md`, `register.md`, `frameworks.md`), `website-deploy-builder`, `connect-domain` (+ `references/registrars.md`), `run-hackathon` (source only; not in the plugin or `/skills.zip`) |
| Pages | `st/install.html`, `st/llms.txt`, `st/openapi.yaml` / `st/openapi.json`, `st/docs.html` (Swagger UI) |
| Go | `h/ui.go` (zips, install scripts, `PluginVersion`), `h/skillshub.go` (catalog; not host-rewritten), `h/instancehost.go` (`rewrittenAssets`, `controlPlaneSkills`), `h/notice_middleware.go` (`X-Skill-Version` → `_notice`), `h/openaichallenge.go`, `simple-host-website/embed.go` |
| Packaging | `plugins/simple-host/` (Claude plugin: `.claude-plugin/plugin.json`, `.mcp.json` → `https://simple-host.app/mcp`), `.claude-plugin/marketplace.json`, `openai-plugin/` (plugin.json 0.4.2, mcp.json, skills rewrite, assets, demo-sites, SUBMISSION.md), `dist/*.zip`, `simple-host-website/` (legacy plugin, `mcp-server/` Node stdio MCP, `setup.sh`, `template/`) |
| Scripts | `scripts/sync-claude-plugin.sh` (copy source → plugin, stamp version), `scripts/check-claude-plugin.sh` (drift + `X-Skill-Version` literals), `scripts/publish-claude-plugin-repo.sh` (→ github.com/vineetu/simple-host-plugin, tag `v$V`), `scripts/build-openai-plugin.sh`, `scripts/check-docs-sync.sh` (routes ↔ openapi ↔ llms.txt ↔ skills) |
| Env | `PUBLIC_BASE_URL`, `SITE_DOMAIN`, `CONTENT_HOST`, `CNAME_TARGET` (host rewriting), `OPENAI_APPS_CHALLENGE` |
| External | Claude plugin directory, OpenAI apps portal, GitHub `vineetu/simple-host-plugin`, skills CLI (`npx skills`) |

## 10. Owner dashboard and owner app

Sign-in page and dashboard at `/dashboard`; the owner app at `/<handle>` on the apex (same
template as the public person page, hydrated for the owner); per-site analytics page. The
owner app is the one place an account with a handle manages its sites: address, version, last
deploy, visibility, versions, rename, domain, lists, saved data, Download and Delete
(INTENT 2026-09-27). `/dashboard` sends such an account there; the apex view it can still reach
(`/?new=1`) lists the sites with a Manage link. Accounts without a handle and the admin tab keep
the full apex controls. Rotate API key stays in the apex app bar.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /dashboard` (`index.html`) · `GET /` (landing; a bare `/<handle>` renders the owner app via `ownerAppOrStatic`) · `GET /analytics/{sitename}` · `GET /internal/showcase/{handle}` (public person page on the content host) |
| MCP tools | — (the dashboard uses REST with the stored key) |
| Pages | `st/index.html`, `st/showcase.html`, `st/analytics.html`, `st/partials/{head,header,footer}.html`, `st/site.css` |
| Go | `h/ui.go` (`RegisterUIRoutes`, `serveStaticPage`, `adminUICSP` nonce CSP, `handlerOnlyPages`), `h/showcase.go` (`renderShowcase`, `renderNotFound`), `h/chrome.go` (header/footer injection, `HackHome`), `h/analytics.go` |
| Calls | everything in §1, §3, §5, §7, §8 (connections), §12, §14 |
| Note | Apex pages allow inline `<script>` only via the per-response nonce; `onclick=` attributes are blocked. `setup.html` is outside this wrapper |

## 11. Admin (operator)

Operator console: disk usage, release/commit and versions kept, participant account issuing, Entries (one row per deployed site
with an **Analytics** column linking `/analytics/{site}?owner={handle}`, a Take down / Restore
switch, and Download all entries), per-user cards (Suspend / Re-enable / Delete), API
traffic. Admin = `ADMIN_API_KEY` or the admin user. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /admin` (public shell) · `GET /v1/admin/users` (users with their sites, ids and suspension state) · `POST /v1/admin/users` (bulk-create participant accounts, returns keys) · `POST /v1/admin/users/{id}/key` (replace that account's keys with one new key, shown once; refused while suspended) · `DELETE /v1/admin/users/{id}` (the same erasure as `DELETE /v1/me`, suspended accounts included) · `POST /v1/admin/sites/{id}/suspend` (`{"reason"}`) and `POST /v1/admin/sites/{id}/restore` (take a site down / put it back) · `POST /v1/admin/users/{id}/suspend` (`{"reason"}`) and `POST /v1/admin/users/{id}/enable` (suspend / re-enable a person) · `GET /v1/admin/export.tar.gz` (every site with saved data and lists, one archive) · `GET /internal/suspended` (the take-down page nginx and Caddy hand off to) · `GET /v1/admin/usage` · `GET /v1/admin/api-analytics` · `PUT /v1/sites/{sitename}/allow-anonymous-writes?owner=` (`RequireAdmin`; `owner` picks that person's site, else the oldest of the name) · `GET /v1/sites/{sitename}/analytics?owner=` and `/analytics/geo?owner=`, `GET /v1/analytics/sites?all=1` (admin reads any site) |
| Pages | `st/admin.html` (tiles Users/Websites/Disk; line with versions kept and running release/commit from usage; Biggest websites; Issue participant accounts; Entries: Entry/Account/Link/**Analytics**/Status with Take down / Restore, Download CSV, Copy links, Download all entries; user cards with **New key**, Suspend / Re-enable / Delete; API traffic tables); `st/index.html` site cards and `st/showcase.html` owner inventory show a taken-down site and its reason, `st/index.html` Admin tab |
| Go | `h/site.go` (`adminUsers`, `adminUsage`), `h/suspend.go` (take-down: admin calls, `serveTakedown`, refusals, boot marker sync), `h/export.go` (`exportAll`), `h/accounts.go` (`createAccounts`, `reissueAccountKey`, `deleteAccount`, `accountAdmin`), `internal/db/suspend.go`, `internal/storage/disk.go` (`SetSuspended`/`IsSuspended`, the `suspended` marker file), `internal/capacity/capacity.go`, `h/apimetrics.go` (`AdminSummary`), `internal/auth/middleware.go` |
| DB | `users` (`suspended_at`, `suspended_reason`), `sites` (`suspended_at`, `suspended_reason`), `versions`, `api_keys`, `api_request_daily`, `api_ip_daily` |
| Take-down | A suspended site keeps everything; Go answers 410 "This site has been taken down" on every path of its site host, person path and claimed name; nginx (custom domains, content host) and Caddy (event boxes) check the `suspended` marker in the site folder and hand off to `/internal/suspended` (`deploy/prod/nginx-suspended-marker.sh` adds the check to live vhosts; `deploy/compose/Caddyfile`). Deploy, rollback, rename, delete, visibility, address and origin changes, state/list writes and public reads of its data answer 403 `site_suspended`; the owner's key still reads and exports. A suspended person's key, connector token, MCP calls and visitor sessions answer 403 `account_suspended` (with the reason), sign-in is refused, refresh tokens are refused unspent, their sites are down; nothing is deleted and re-enable reverses it (a site taken down on its own stays down). Markers are re-synced from the database at boot. |
| Env | `ADMIN_API_KEY`, `DATA_DIR`, `KEEP_VERSIONS` and `MAX_ARCHIVE_MB` (reported by usage as `keep_versions`, `site_limit_mb`) |

## 12. Analytics and geo

Server-side visitor analytics tailed from the nginx (or Caddy) log into hourly/daily aggregates,
with country from local IP-range data; per-endpoint API metrics for admin. No client script.
**Status: live** (`ANALYTICS_LOG` set).

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/analytics` · `GET /v1/sites/{sitename}/analytics/geo` · `GET /v1/analytics/sites` · `GET /v1/admin/api-analytics` |
| MCP tools | `site_analytics` |
| Skill | `website-deploy/references/operations.md` §Analytics |
| Pages | `st/analytics.html`, `st/showcase.html` Analytics tab, `st/index.html` site cards, `st/admin.html` API traffic |
| Go | `internal/analytics/{ingest,classify,geo,countries,rebuild}.go` (attributes views on site hosts, person hosts, claimed names, custom domains; bot/human classes; salted ip_hash), `h/analytics.go`, `h/apimetrics.go` (every `/v1/*` request; IPs stored as /24 or /48), `internal/geoip/geoip.go` (DB-IP mmdb, watched), `cmd/analytics-rebuild` (replays the rotated archives oldest first, then the live log), `cmd/ip-country-load`, `web/analytics-parse.js` |
| DB | `site_view_hourly`, `site_visitor_hourly`, `site_geo_daily`, `site_view_daily`, `site_visitor_daily` (legacy, pruned after 400 days), `analytics_ingest_state`, `ip_country_ranges`, `api_request_daily`, `api_ip_daily` |
| Env | `ANALYTICS_LOG`, `ANALYTICS_SALT`, `GEOIP_DIR` |
| External | nginx `log_format shanalytics` (`deploy/prod/nginx-analytics-logformat.conf`, query string stripped), `deploy/prod/logrotate-analytics.conf` (29 archives: raw IPs ≤30 days), DB-IP Lite via `scripts/geoip-refresh.sh` + `deploy/prod/simple-host-geoip-refresh.{service,timer}` |

## 13. Showcase / person index

Public page listing a person's `public` sites (each linked at its own address) at
`<handle>.simple-host.app/` (person host); old `sites.simple-host.app/<handle>` links 302 there
(`/internal/site-redirect/{handle}`; `/internal/showcase/{handle}` still renders it for
instances without person hosts). **Status: live.** See §2 and §10 for
routes (`GET /internal/showcase/{handle}`, host-routed person root). Go: `h/showcase.go`,
`h/personhost.go`, `h/sitehost.go`. Page: `st/showcase.html`. DB: `sites.visibility`. MCP: `set_visibility`.

## 14. AI create (Grok sidecar) and voice input

In-app builder chat: a signed-in owner describes a site and the model writes it (background
jobs); voice input by local speech-to-text. Secondary path; the skill in the person's own AI
app is primary. **Status: live, flag-gated.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/generate` · `GET /v1/generate/status` (only when `LLM_API_KEY` set) · `POST /v1/transcribe` · `POST /v1/transcribe/ticket` (only when `TRANSCRIBE_URL` set) · `/v1/transcribe/stream` is **nginx-only** (WebSocket straight to the speech service on :8103, signed ticket in the query) |
| Pages | `st/showcase.html` (builder chat, attachments, mic) |
| Go | `h/generate.go` (prompt/instructions, attachments ≤18 MB), `h/generate_jobs.go`, `h/transcribe.go` (audio ≤25 MB, ticket signing) |
| Env | `LLM_PROVIDER` (default `grok`), `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL`, `VISION_PROVIDER`, `VISION_API_KEY`, `VISION_BASE_URL`, `VISION_MODEL`, `TRANSCRIBE_URL`, `TRANSCRIBE_TICKET_SECRET` |
| External | Grok via the local CLIProxy sidecar (`/opt/cliproxy`, `127.0.0.1:8102/v1`) only, no fallbacks; Moonshine speech-to-text (`/opt/moonshine`, :8100 HTTP, :8103 stream) |
| Limits | generate 20 burst +1/12 s per IP, 30 burst +1/10 s per user, status 240/4 s⁻¹; transcribe 60 burst +1/3 s per IP and per user |

## 15. Hackathon / event instances (simple-hack.app)

An organiser runs a private instance for an event (their own cloud box, same binary): first-boot
setup page, participant accounts from admin, event hostnames under `simple-hack.app`, entries
list, export. simple-hack.app itself serves the organiser page. **Status:** marketing page and
setup live; event hostname claims wired (`EVENT_DOMAINS=simple-hack.app`) but currently failing
because Vercel rejects `EVENT_DNS_TOKEN`.

| Surface | Details |
|---|---|
| Routes | `GET /hackathons` (simple-hack.app `/` is proxied here by nginx) · `GET /v1/events` · `POST /v1/events` (`{name, ip, domain}` → Vercel A records for `<name>.<domain>` and `sites.<name>.<domain>`; 21-day claim, max 5 per account) · `DELETE /v1/events/{name}` · setup mode only (no hostname configured): `/` (`setup.html`), `GET /v1/setup/state`, `POST /v1/setup/verify`, `POST /v1/setup/own-domain`, `GET /v1/setup/dns-check`, `POST /v1/setup/free-name` (proxies to `SETUP_PUBLIC_API` `/v1/events`), `POST /v1/setup/finish` · admin routes in §11 · export in §1 |
| Skill | `sk/run-hackathon/SKILL.md` (§What the organiser needs, §The flow 1–8, + references `dns.md`, `install.md`, `providers.md`, `teardown.md`) — served per-skill, excluded from `/skills.zip` and the plugin |
| Pages | `st/hackathons.html` (organiser prompt, swaps in `location.host`), `st/og-hack.png`, `st/setup.html`, `st/admin.html` |
| Go | `h/eventdomain.go` (claim/release/list, hourly sweep), `internal/eventdns/{vercel,inuse}.go`, `h/setup.go` (`InstanceConfigured`, setup restart), `h/chrome.go` (`HackHome` when host is `simple-hack.*`), `h/instancehost.go` (host rewriting for non-canonical instances) |
| DB | `event_domains`, `instance_config` |
| Env | `EVENT_DNS_TOKEN`, `EVENT_DNS_TEAM_ID`, `EVENT_DOMAINS`, `SETUP_PASSWORD`, `SETUP_PUBLIC_API`, `PERSON_HOSTS` (off on event instances), `MAX_ARCHIVE_MB`, `KEEP_VERSIONS`, `BIND_ADDR` |
| External | Vercel DNS API; Docker Compose + Caddy (`deploy/compose/`, `deploy/install/install.sh`; container logs capped at 3 × 10 MB per service, Caddy access log rolled daily and rolls deleted after 28 days, so raw IPs ≤30 days); live nginx `/etc/nginx/sites-enabled/simple-hack.app`; `scripts/e2e-hackathon.sh`, `scripts/check-fresh-install.sh` |
| Limits | event claims 10 burst, 1/min; setup 5 burst, 1/min |

## 16. Enterprise and marketing pages

Static audience pages shared as direct links. **Status: live.**

| Route | Page |
|---|---|
| `GET /` | `st/index.html` (landing) |
| `GET /features` | `st/features.html` |
| `GET /enterprise` | `st/enterprise.html` |
| `GET /enterprise/brief` | `st/enterprise-brief.html` |
| `GET /enterprise/architecture` | `st/enterprise-architecture.html` |
| `GET /architecture.html` | `st/architecture.html` (file server) |
| `GET /hackathons` | see §15 |

Go: `h/ui.go`, `h/chrome.go`. Assets: `st/og.png`, `st/favicon.svg`, `st/site.css`.

## 17. Legal and support pages

**Status: live.** `GET /terms` → `st/terms.html`; `GET /support` → `st/support.html`;
`/privacy.html` → `st/privacy.html` (file server, no clean route). Linked from plugin
listings; public contact is support@simple-host.app. Go: `h/ui.go`.

## 18. Abuse limits and hardening

| Guard | Where |
|---|---|
| Per-IP token buckets | `h/ratelimit.go`; instances listed in each section (upload 30 burst/0.1 s⁻¹, state 60/1 s⁻¹, auth 20/0.2, email 5/0.02, connector, generate, transcribe, events, setup, reviewer) |
| Size caps | per-site archive `MAX_ARCHIVE_MB` (default 100 MB, `h/usage.go`), tarball total/file/path caps (`internal/tarball/extract.go`), state 1 MB, collection item 64 KB, ≤100 PATCH ops |
| Blocked upload types | `internal/tarball/validate.go` `blockedExtensions` |
| Write auth | `WRITE_AUTH_MODE`, `visitorWriteOK` (shared host key-only when `PERSON_HOSTS=canonical`), admin-only `allow_anonymous_writes` hatch (`PUT /v1/sites/{sitename}/allow-anonymous-writes`) |
| Origin checks | `authorizeStateOrigin`, `allowed_origins` (`PUT /v1/sites/{sitename}/allowed-origins`), `h/cors.go` |
| Headers / CSP | `SecurityHeaders`; apex nonce CSP `adminUICSP`; consent page `consentHeaders` |
| Reserved names | `h/handles.go` (handles), `h/platformsubdomain.go` `reservedSubdomainLabels`, `internal/db/namespace.go` |
| Preview accounts | `PREVIEW_ACCOUNTS`, `PREVIEW_TTL_HOURS` (expiry sweep in `h/site.go`) |
| Take-down | Operator suspend / restore of a site or a person, nothing deleted (§11) |
| Planned | Public Suffix List entry, subdomain blocklist/cap, AUP, report form, DMCA agent (INTENT / owner TODO) |

## 19. Signals and notifications

No owner-facing notifications, webhooks or signals exist. Outbound email is only sign-in codes
(`internal/email/resend.go`). Stale-skill `_notice` in JSON responses (§9) is the only in-band
notice.

## 20. Operations (health, schema, CLI)

| Surface | Details |
|---|---|
| Routes | `GET /healthz` · `GET /readyz` (DB ping) — `h/health.go` |
| Startup | logs `simple-host <release> (commit <hash>)` (`internal/buildinfo`, stamped by `-ldflags -X` in `Dockerfile`, `.github/workflows/release.yml`, the CLAUDE.md build line); `internal/db/schemacheck.go` `VerifySchema` (fails fast on missing columns, names `simple-host migrate`); never migrates |
| Schema | `db/schema.sql` (new database) + `db/migrations/*.sql`; `db/migrations/migrations.go` embeds them and applies pending files in lexical order, each once in its own transaction, tracked in `schema_migrations`, under a Postgres advisory lock; historical files are a fixed baseline, never run; new files must be idempotent (rule in that file) |
| CLI subcommands | `simple-host migrate` (apply pending; `-status`; `-mark FILE` records without running), `simple-host version` (release, commit, migrations in this build; no DB), `simple-host oauth-client`, `simple-host review-account`, `simple-host geoip-verify` (`cmd/server/`); `cmd/analytics-rebuild`, `cmd/ip-country-load` |
| Small-box upgrade | re-run `deploy/install/install.sh`: pulls the pinned release, `docker compose up -d db`, `docker compose run --rm app migrate`, then starts the new app; a failed migrate leaves the app as it was |
| Env | `DB_DSN`, `PORT`, `BIND_ADDR`, `DATA_DIR`, `SITE_DOMAIN`, `PUBLIC_BASE_URL`, `CONTENT_HOST`; dev-only `CHROME_SERVE_ADDR`, `CHROME_SERVE_FOR`; migration-only `UNIFY_KEEP` |
| Deploy | `/usr/local/bin/simple-host` as `simple-host.service`, env `/etc/simple-host.env`; `deploy/prod/*` (incl. log retention `logrotate-analytics.conf` and `journald-retention.conf`, 30 days), `Dockerfile`, `compose.yaml`, `Makefile`; checks `scripts/check-{docs-sync,features,html,layering,claude-plugin,reserved-subdomains,fresh-install}.sh` |

## 21. MCP tool index (`internal/mcp/tools.go`, 27 tools)

| Tool | REST call | § |
|---|---|---|
| `who_am_i` | `GET /v1/me` | 7 |
| `list_sites` | `GET /v1/sites` | 1 |
| `get_site` | `GET /v1/sites/{s}/versions/{v}/files` | 1 |
| `read_site_file` | `GET /v1/sites/{s}/versions/{v}/files/{path}` | 1 |
| `create_site` / `update_site` | `POST` / `PUT /v1/sites/{s}/files` | 1 |
| `list_versions` | `GET /v1/sites/{s}/versions` | 1 |
| `rollback_site` | `PUT /v1/sites/{s}/active-version` | 1 |
| `delete_site` | `DELETE /v1/sites/{s}` | 1 |
| `list_deleted_sites` | `GET /v1/me/deleted-sites` | 1 |
| `restore_site` | `POST /v1/sites/{s}/restore` | 1 |
| `rename_site` | `PATCH /v1/sites/{s}` | 1 |
| `set_visibility` | `PUT /v1/sites/{s}/visibility` | 1, 13 |
| `get_state` | `GET …/state` | 4 |
| `update_state` | `PATCH` or `PUT …/state` | 4 |
| `list_collections` | `GET /v1/sites/{s}/collections` | 5 |
| `read_collection` | `GET …/collections/{c}` | 5 |
| `add_to_collection` | `POST …/collections/{c}` | 5 |
| `set_collection_privacy` | `PUT /v1/sites/{s}/collections/{c}/privacy` | 5 |
| `update_collection_item` | `PATCH …/collections/{c}/items/{id}` | 5 |
| `delete_collection_item` | `DELETE …/collections/{c}/items/{id}` | 5 |
| `clear_collection` | `DELETE …/collections/{c}` | 5 |
| `connect_domain` | `POST /v1/sites/{s}/domain` | 3 |
| `domain_status` | `GET /v1/sites/{s}/domain` | 3 |
| `remove_domain` | `DELETE /v1/sites/{s}/domain` | 3 |
| `site_analytics` | `GET /v1/sites/{s}/analytics?days=` | 12 |
| `export_site` | `POST /v1/sites/{s}/export-link` (returns a link to `GET /v1/export?token=`) | 1 |

## 22. Unplaced routes and tools

None. Every `mux.Handle`/`HandleFunc` registration in `cmd/server` and `internal/handler`
(131 distinct method+path patterns, plus the looped `/mcp`, `/skills/{dir}.*` and
`rewrittenAssets` routes) and all 27 MCP tools are placed above. Routes that exist outside
the mux: host-routed site hosts / person hosts / claimed names / custom domains (§2, §3) and the
nginx-only `/v1/transcribe/stream` (§14).
