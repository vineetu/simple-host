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
export, public/unlisted listing. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST`/`PUT /v1/sites/{sitename}` (archive) · `POST`/`PUT /v1/sites/{sitename}/files` (inline JSON, base64 allowed) · `GET /v1/sites` · `PATCH /v1/sites/{sitename}` (rename) · `DELETE /v1/sites/{sitename}` · `GET /v1/sites/{sitename}/versions` · `GET /v1/sites/{sitename}/versions/{version}/files` · `GET /v1/sites/{sitename}/versions/{version}/files/{path...}` · `PUT /v1/sites/{sitename}/active-version` · `PUT /v1/sites/{sitename}/visibility` (`public`/`unlisted`) · `GET /v1/sites/{sitename}/export.tar.gz` (files + saved data) · `GET /internal/notfound` (branded 404, nginx `@notfound`) |
| MCP tools | `list_sites`, `get_site`, `read_site_file`, `create_site`, `update_site`, `list_versions`, `rollback_site`, `delete_site`, `rename_site`, `set_visibility` |
| Skill | `website-deploy/SKILL.md` §Two ways to deploy, §The one rule that breaks sites (relative links), §Rules that always apply, §Completion standard · `references/packaging-and-validation.md` (Package, Upload, Verify) · `references/operations.md` §Listing, §Rename, §Rollback, §Delete · `references/frameworks.md` · `website-deploy-builder/SKILL.md` §Capability tree 1 |
| Pages | owner app `st/showcase.html` (site inventory, versions, delete, visibility) · `st/index.html` at `/dashboard` (site cards, rename, delete, versions) · `st/notfound.html` |
| Go | `h/site.go` (create/update/list/delete/rename/visibility, route table), `h/versions.go`, `h/versionfiles.go`, `h/export.go`, `h/sitename.go`, `h/usage.go` (per-site cap), `internal/tarball/{extract,sanitize,validate}.go`, `internal/storage/disk.go` (by-id layout, `handles/` symlinks), `internal/db/queries.go` |
| DB | `sites`, `versions` |
| Limits | 100 sites per account (admins exempt); uploads serialised per site; upload limiter 30 burst, 0.1/s |
| Env | `DATA_DIR`, `MAX_ARCHIVE_MB`, `KEEP_VERSIONS`, `DEPLOY_SCRIPT`, `PREVIEW_ACCOUNTS`, `PREVIEW_TTL_HOURS` (preview-site expiry sweep) |
| External | nginx serves files from `/srv/simple-host/sites/handles/<h>/<s>/` on the content host; Caddy does the same on event instances (`deploy/compose/Caddyfile`) |

## 2. Per-site and per-person addresses, and legacy redirects

Every site lives at its own origin, `https://<site>.<handle>.simple-host.app/` (files at the
root, `/v1/` for that site only, sign-in, per-person saves and private collections bound to that
host; a sign-in covers that one site). Every handle is a host too:
`https://<handle>.simple-host.app/` lists that person's public sites. Each person gets a
certificate for `*.<handle>.simple-host.app`, issued automatically (usually within ~10 minutes of
their first site; brand-new accounts may queue behind the weekly and daily issuance caps); until it exists their
sites keep the person-path form `<handle>.simple-host.app/<site>/`, and every URL handed out is
whichever address is live. Old `<handle>.simple-host.app/<site>/…` and
`sites.simple-host.app/<handle>/<site>/…` links 302 to the site host (path and query kept).
**Status: live** (`PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical`, 2026-09-26; design:
`docs/designs/per-site-subdomains.md`).

| Surface | Details |
|---|---|
| Routes | Host-routed, not mux: `<site>.<handle>.<SITE_DOMAIN>` → `SiteHosts` (files at `/`, `/v1` same-origin for that one site only) · `<handle>.<SITE_DOMAIN>` → `PersonHosts` (person page at `/`; `/<site>/…` 302s to the site host once the person's certificate is ready, else serves it by path) · `GET /internal/site-redirect/{handle}` · `GET /internal/site-redirect/{handle}/{sitename}` · `GET /internal/site-redirect/{handle}/{sitename}/{rest...}` (302 from `sites.simple-host.app/<h>/<s>/…` to the site's live address; nginx rewrites into these; `vineetu/eb2-wait` excepted in nginx and in `contentHostOnlySites`) · `LegacyHostRedirect`: 301 for an unclaimed single-label `<name>.<SITE_DOMAIN>` that is not a handle, to `sites.<domain>/<handle>/<name>` (which then 302s as above) · an aliased old handle 301s to the new one |
| MCP tools | none directly; site summaries return the live site address, `who_am_i` the person page |
| Skill | `website-deploy/SKILL.md` §Service (address form); host strings are rewritten per instance (`h/instancehost.go`) |
| Pages | `st/showcase.html` (person index / public view) |
| Go | `h/sitehost.go` (`SITE_HOSTS` off/serve/canonical, site-host routing, certificate requests and readiness), `h/personhost.go` (`PERSON_HOSTS` off/serve/canonical, `PersonPageURL`, `PersonReturnSite`, `contentHostRedirect`), `h/legacyhost.go`, `h/handles.go` (reserved handles, `assignHandle`), `internal/db/namespace.go` (one namespace for handles, claimed names, reserved and retired names; `RenameHandle`, aliases), `h/instancehost.go` |
| DB | `users.handle`, `handle_aliases` (e.g. `admin` → `simple-host-team`), `legacy_hostnames` |
| Env | `PERSON_HOSTS`, `SITE_HOSTS` (needs `PERSON_HOSTS` on), `SITE_CERT_DIR` (e.g. `/var/lib/simple-host-site-certs`: `requests/<handle>` written by the app, `ready/<handle>` by the issuer), `SITE_DOMAIN`, `CONTENT_HOST` |
| External | live nginx `/etc/nginx/sites-enabled/sites-content-host` (rewrites to `/internal/site-redirect/*`) and `simple-host` (wildcard `*.simple-host.app` → app; a server for `<site>.<person>.simple-host.app` loads the per-person cert by variable); wildcard cert; per-person certs from the root-owned issuer in `deploy/site-certs/` (path unit on each request plus a 10-minute timer; at most 40 new certificates per rolling week and 12 per day; certbot DNS-01 via the Vercel hooks in `/usr/local/lib/certbot-vercel/`); Public Suffix List entry is **planned** |

## 3. Claimed `<name>.simple-host.app` and custom domains

A site takes a free `<name>.simple-host.app` (verified at once, first come) or the owner's own
domain (CNAME/A, provisional until DNS proves it, 24 h expiry if unproven). Once active the site
lives only there; its other addresses redirect. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/sites/{sitename}/domain` (bind; a `<name>.<SITE_DOMAIN>` value takes the free-name path) · `GET /v1/sites/{sitename}/domain` · `DELETE /v1/sites/{sitename}/domain` · `GET /internal/tls-ask` (Caddy on-demand TLS gate) · `GET /internal/domain-redirect/{handle}/{sitename}` · `GET /internal/domain-redirect/{handle}/{sitename}/{rest...}` · host-routed `BoundSubdomains` serves a claimed name or custom domain at its root |
| MCP tools | `connect_domain`, `domain_status` |
| Skill | `connect-domain/SKILL.md` §The free address, §The flow (1–5, Disconnect), §Backend on a connected domain, §Gotchas · `connect-domain/references/registrars.md` (Vercel, GoDaddy, Porkbun, other) · `website-deploy/references/operations.md` §A nicer address · `website-deploy-builder/SKILL.md` §8 |
| Pages | `st/index.html` (connect/disconnect domain on the site card) |
| Go | `h/domains.go` (bind, status, delete, `tlsAsk`, `domainRedirect`), `h/domaincheck.go` (background re-verify), `h/platformsubdomain.go` (free names, `reservedSubdomainLabels`, `BoundSubdomains`, `siteOwnDomain`), `internal/db/domains.go`, `internal/db/namespace.go` |
| DB | `sites.custom_domain`, `domain_status`, `domain_verified_at`, `domain_last_error`, `domain_bound_at`; `legacy_hostnames` |
| Env | `CNAME_TARGET` (subdomain CNAME), `CUSTOM_DOMAIN_IP` (apex A record), `SITE_DOMAIN` |
| Timing | unverified binding expires 24 h after bind; re-check every 2 min, active verdict re-proved hourly |
| External | per-domain nginx vhost (template `deploy/prod/nginx-customdomain.example.conf`) + Let's Encrypt on prod; Caddy on-demand TLS on event instances; `scripts/check-reserved-subdomains.sh` compares reserved labels against live nginx |

## 4. Saved state (shared JSON per site)

One JSON document per site: read by anyone, written with atomic ops. On a site's own origin
(site host, person-path fallback, claimed name, custom domain) writes need a signed-in visitor (cookie + `X-SH-CSRF`)
or any valid API key. On the old shared content host (`sites.simple-host.app`) reads stay open
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
| Pages | `st/auth.js` (`SH.state.get/put/patch`) |
| Ops | `set`, `inc`, `append`, `remove`, `removeWhere`; `PUT` uses ETag/`If-Match` |
| Go | `h/site.go` (`getSiteState`, `putSiteState`, `patchSiteState`, origin check `authorizeStateOrigin`), `h/stateops.go` (op set, max 100 ops), `h/visitorsession.go` (`visitorWriteOK`), `internal/db/queries.go` (`UpdateSiteStateCAS`) |
| DB | `sites.state`, `sites.allowed_origins`, `sites.allow_anonymous_writes` |
| Env | `WRITE_AUTH_MODE` (off/log/on) |
| Limits | 1 MB per state doc; `stateLimiter` 60 burst, 1/s per IP |

## 5. Collections, including private collections

Append-only lists (comments, RSVPs, votes) read by anyone. A collection can be made **private**
on a site's own origin: only signed-in visitors submit, same-origin, never by API key (server
stamps `_submitted_by`/`_submitted_at`),
only the owner (or operator) reads, and the owner may edit/delete items. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/collections/{coll}` · `POST /v1/sites/{sitename}/collections/{coll}` · `OPTIONS /v1/sites/{sitename}/collections/{coll}` — `(+/v1/u)` · `GET /v1/sites/{sitename}/collections` (owner list) · `GET /v1/sites/{sitename}/collections/{coll}/export.csv` (owner) · `PUT /v1/sites/{sitename}/collections/{coll}/privacy` (owner) · `PATCH`/`DELETE /v1/sites/{sitename}/collections/{coll}/items/{id}` `(+/v1/u)` (private lists only: owner key, connector, owner session on own origin, or admin) |
| MCP tools | `list_collections`, `read_collection`, `add_to_collection`, `set_collection_privacy`, `update_collection_item`, `delete_collection_item` |
| Skill | `website-deploy/SKILL.md` §Personal details go in a private collection · `references/backend.md` §Append-only collections, §Private collections (1–3, editing, reading as owner, errors) · `references/operations.md` §Private collections · `website-deploy-builder/SKILL.md` §Capability tree |
| Pages | `st/auth.js` (`SH.collection(...).list/append/update/remove`), `st/showcase.html` and `st/index.html` (data tab, CSV export, item edit/delete) |
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
new API key, stored only as SHA-256 in `api_keys` (8c479d2), and keys from earlier sign-ins keep
working; a stored key can never be shown again. Rotate replaces all keys and disconnects all
connector grants. The dashboard keeps the key in `localStorage['apiKey']`;
there is no owner cookie session. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/auth` (send code) · `POST /v1/auth/verify` (code → key; creates the account and handle if new) · `GET /v1/me` · `POST /v1/me/api-key/rotate` (replaces all keys) · `PATCH /v1/me` (display name, handle) |
| MCP tools | `who_am_i` |
| Skill | `website-deploy/references/register.md` (email-code registration) · `references/operations.md` §API key rotation · `references/backend.md` §Saving from an agent (API key) |
| Pages | `st/index.html` (`/dashboard` sign-in: code, Google, paste key), `st/showcase.html`, `st/connect.html` |
| Go | `internal/auth/middleware.go` (`X-API-Key`, admin key, `RequireAdmin`), `h/user.go`, `h/emailcode.go`, `h/accounts.go` (`patchMe`, handle validation), `h/handles.go`, `internal/db/queries.go` (hashed key lookup, `ClaimHandle`), `internal/db/internalkey.go` (in-process per-request keys for the connector), `internal/email/resend.go` |
| DB | `users`, `api_keys`, `auth_tokens` (purpose-bound codes; expired ones purged) |
| Env | `ADMIN_API_KEY`, `RESEND_API_KEY`, `MAIL_FROM`, `PUBLIC_BASE_URL` |
| External | Resend |
| Limits | `ipLimiter` 20/0.2 s⁻¹ per IP; `emailLimiter` 5/0.02 s⁻¹ per address |

## 8. MCP connector and OAuth (chat apps)

Remote MCP endpoint behind an OAuth 2.1 authorization server (DCR, PKCE, refresh, revoke).
Sign-in on the consent page reuses Google or email code. Tool calls are replayed into the mux
as the person, so they meet the same checks as REST. Connector tokens are stored hashed.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /.well-known/oauth-protected-resource` · `GET /.well-known/oauth-protected-resource/mcp` · `GET /.well-known/oauth-authorization-server` · `GET /.well-known/oauth-authorization-server/mcp` · `POST /oauth/register` · `GET /oauth/authorize` (consent page) · `POST /oauth/authorize/decision` · `POST /oauth/token` · `POST /oauth/revoke` · `POST /oauth/reviewer-signin` · `POST`/`GET`/`DELETE /mcp` · `GET /v1/me/connections` · `DELETE /v1/me/connections/{client_id}` |
| MCP tools | all 22 (see §21); server metadata and instructions in `internal/mcp/instructions.go` |
| Skill | `website-deploy/SKILL.md` §Service, §Two ways to deploy (connector vs key); `openai-plugin/skills/website-deploy/SKILL.md` is the connector-only variant |
| Pages | `st/connect.html` (consent; own nonce CSP in `consentHeaders`), `st/showcase.html` (Connected apps) |
| Go | `h/connector.go` (AS, `BearerAuth`, `serveMCP`, connections, hourly sweep), `h/reviewer.go` (password sign-in for one designated store-review account), `internal/mcp/{server,jsonrpc,tools,outputs,instructions}.go`, `internal/db/connector.go`, `internal/db/internalkey.go`, `cmd/server/oauthclient.go` (`simple-host oauth-client …`, hand-registered clients e.g. a GPT Action), `cmd/server/reviewaccount.go` (`simple-host review-account …`) |
| DB | `oauth_clients`, `oauth_grants`, `oauth_codes`, `oauth_tokens` |
| Env | `PUBLIC_BASE_URL`, `REVIEW_ACCOUNT_EMAIL`, `REVIEW_ACCOUNT_PASSWORD_HASH`, `ADMIN_API_KEY` |
| Tokens | PKCE S256 only; code 60 s, access 1 h, refresh 90 days rotating (reuse revokes the grant); scope `sites`; tool calls run with a per-request `shint_` internal key |
| Limits | register 10 burst, 10/h; authorize and token 30 burst, 0.5/s; reviewer sign-in 10/IP then 1/min, 30 global then 30/h |
| Tests / e2e | `h/connector_test.go`, `h/reviewer_test.go`, `internal/mcp/*_test.go`, `scripts/e2e-connector.py`, `scripts/e2e-connector-browser.mjs`, `scripts/e2e-reviewer.py`, `scripts/seed-reviewer-demo.py` |

## 9. Skills and plugin distribution

Skills source is `simple-host-website/skills/` (embedded via `simple-host-website/embed.go`) at
version **0.19.1**, served over HTTP, packaged as a Claude plugin, an OpenAI/ChatGPT plugin, a
standalone plugin repo, and via `npx skills add vineetu/simple-host`. **Status: live**
(ChatGPT and Claude directory listings submitted 2026-09-24, pending).

| Surface | Details |
|---|---|
| Routes | `GET /skills.zip` (excludes `run-hackathon`) · `GET /skills/version` · `GET /skills/{dir}.zip` and `GET /skills/{dir}/SKILL.md` (one pair per bundled skill dir, registered in a loop) · `GET /plugin.zip` · `GET /install.sh` · `GET /install.ps1` · `GET /v1/skills` · `GET /v1/skills/{name}` · `GET /v1/skills/{name}/references/{file}` · `GET /.well-known/skills/index.json` · `GET /.well-known/skills/{name}/SKILL.md` · `GET /.well-known/skills/{name}/references/{file}` · `GET /.well-known/openai-apps-challenge` · `GET /{asset}` for each of `rewrittenAssets` (only on non-canonical instances) |
| Skills | `website-deploy` (SKILL.md + references `backend.md`, `operations.md`, `packaging-and-validation.md`, `register.md`, `frameworks.md`), `website-deploy-builder`, `connect-domain` (+ `references/registrars.md`), `run-hackathon` (source only; not in the plugin or `/skills.zip`) |
| Pages | `st/install.html`, `st/llms.txt`, `st/openapi.yaml` / `st/openapi.json`, `st/docs.html` (Swagger UI) |
| Go | `h/ui.go` (zips, install scripts, `PluginVersion`), `h/skillshub.go` (catalog; not host-rewritten), `h/instancehost.go` (`rewrittenAssets`, `controlPlaneSkills`), `h/notice_middleware.go` (`X-Skill-Version` → `_notice`), `h/openaichallenge.go`, `simple-host-website/embed.go` |
| Packaging | `plugins/simple-host/` (Claude plugin: `.claude-plugin/plugin.json`, `.mcp.json` → `https://simple-host.app/mcp`), `.claude-plugin/marketplace.json`, `openai-plugin/` (plugin.json 0.4.0, mcp.json, skills rewrite, assets, demo-sites, SUBMISSION.md), `dist/*.zip`, `simple-host-website/` (legacy plugin, `mcp-server/` Node stdio MCP, `setup.sh`, `template/`) |
| Scripts | `scripts/sync-claude-plugin.sh` (copy source → plugin, stamp version), `scripts/check-claude-plugin.sh` (drift + `X-Skill-Version` literals), `scripts/publish-claude-plugin-repo.sh` (→ github.com/vineetu/simple-host-plugin, tag `v$V`), `scripts/build-openai-plugin.sh`, `scripts/check-docs-sync.sh` (routes ↔ openapi ↔ llms.txt ↔ skills) |
| Env | `PUBLIC_BASE_URL`, `SITE_DOMAIN`, `CONTENT_HOST`, `CNAME_TARGET` (host rewriting), `OPENAI_APPS_CHALLENGE` |
| External | Claude plugin directory, OpenAI apps portal, GitHub `vineetu/simple-host-plugin`, skills CLI (`npx skills`) |

## 10. Owner dashboard and owner app

Sign-in page and dashboard at `/dashboard`; the owner app at `/<handle>` on the apex (same
template as the public person page, hydrated for the owner); per-site analytics page.
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

Operator console: disk usage, participant account issuing, Entries (one row per deployed site
with an **Analytics** column linking `/analytics/{site}?owner={handle}`), per-user cards, API
traffic. Admin = `ADMIN_API_KEY` or the admin user. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /admin` (public shell) · `GET /v1/admin/users` · `POST /v1/admin/users` (bulk-create participant accounts, returns keys) · `DELETE /v1/admin/users/{id}` · `GET /v1/admin/usage` · `GET /v1/admin/api-analytics` · `PUT /v1/sites/{sitename}/allow-anonymous-writes` (`RequireAdmin`) · `GET /v1/sites/{sitename}/analytics?owner=` and `/analytics/geo?owner=`, `GET /v1/analytics/sites?all=1` (admin reads any site) |
| Pages | `st/admin.html` (tiles Users/Websites/Disk; Biggest websites; Issue participant accounts; Entries: Entry/Account/Link/**Analytics**; user cards; API traffic tables), `st/index.html` Admin tab |
| Go | `h/site.go` (`adminUsers`, `adminUsage`), `h/accounts.go` (`createAccounts`, `deleteAccount`, `accountAdmin`), `internal/capacity/capacity.go`, `h/apimetrics.go` (`AdminSummary`), `internal/auth/middleware.go` |
| DB | `users`, `sites`, `versions`, `api_request_daily`, `api_ip_daily` |
| Env | `ADMIN_API_KEY`, `DATA_DIR` |

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
| Go | `internal/analytics/{ingest,classify,geo,countries,rebuild}.go` (attributes views on site hosts, person hosts, claimed names, custom domains; bot/human classes; salted ip_hash), `h/analytics.go`, `h/apimetrics.go` (every `/v1/*` request; IPs stored as /24 or /48), `internal/geoip/geoip.go` (DB-IP mmdb, watched), `cmd/analytics-rebuild`, `cmd/ip-country-load`, `web/analytics-parse.js` |
| DB | `site_view_hourly`, `site_visitor_hourly`, `site_geo_daily`, `site_view_daily`, `site_visitor_daily` (legacy, pruned after 400 days), `analytics_ingest_state`, `ip_country_ranges`, `api_request_daily`, `api_ip_daily` |
| Env | `ANALYTICS_LOG`, `ANALYTICS_SALT`, `GEOIP_DIR` |
| External | nginx `log_format shanalytics` (`deploy/prod/nginx-analytics-logformat.conf`, query string stripped), `deploy/prod/logrotate-analytics.conf`, DB-IP Lite via `scripts/geoip-refresh.sh` + `deploy/prod/simple-host-geoip-refresh.{service,timer}` |

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
| External | Vercel DNS API; Docker Compose + Caddy (`deploy/compose/`, `deploy/install/install.sh`); live nginx `/etc/nginx/sites-enabled/simple-hack.app`; `scripts/e2e-hackathon.sh`, `scripts/check-fresh-install.sh` |
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
| Planned | Public Suffix List entry, subdomain blocklist/cap, AUP, report + takedown, DMCA agent (INTENT / owner TODO) |

## 19. Signals and notifications

No owner-facing notifications, webhooks or signals exist. Outbound email is only sign-in codes
(`internal/email/resend.go`). Stale-skill `_notice` in JSON responses (§9) is the only in-band
notice.

## 20. Operations (health, schema, CLI)

| Surface | Details |
|---|---|
| Routes | `GET /healthz` · `GET /readyz` (DB ping) — `h/health.go` |
| Startup | `internal/db/schemacheck.go` `VerifySchema` (fails fast on missing columns); `db/schema.sql` + `db/migrations/*.sql` |
| CLI subcommands | `simple-host oauth-client`, `simple-host review-account`, `simple-host geoip-verify` (`cmd/server/`); `cmd/analytics-rebuild`, `cmd/ip-country-load` |
| Env | `DB_DSN`, `PORT`, `BIND_ADDR`, `DATA_DIR`, `SITE_DOMAIN`, `PUBLIC_BASE_URL`, `CONTENT_HOST`; dev-only `CHROME_SERVE_ADDR`, `CHROME_SERVE_FOR`; migration-only `UNIFY_KEEP` |
| Deploy | `/usr/local/bin/simple-host` as `simple-host.service`, env `/etc/simple-host.env`; `deploy/prod/*`, `Dockerfile`, `compose.yaml`, `Makefile`; checks `scripts/check-{docs-sync,features,html,layering,claude-plugin,reserved-subdomains,fresh-install}.sh` |

## 21. MCP tool index (`internal/mcp/tools.go`, 22 tools)

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
| `connect_domain` | `POST /v1/sites/{s}/domain` | 3 |
| `domain_status` | `GET /v1/sites/{s}/domain` | 3 |
| `site_analytics` | `GET /v1/sites/{s}/analytics?days=` | 12 |

## 22. Unplaced routes and tools

None. Every `mux.Handle`/`HandleFunc` registration in `cmd/server` and `internal/handler`
(129 distinct method+path patterns, plus the looped `/mcp`, `/skills/{dir}.*` and
`rewrittenAssets` routes) and all 22 MCP tools are placed above. Routes that exist outside
the mux: host-routed site hosts / person hosts / claimed names / custom domains (§2, §3) and the
nginx-only `/v1/transcribe/stream` (§14).
