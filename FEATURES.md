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
  → `connector.BearerAuth` → mux, behind `FamilyHosts` → `SiteBaseHosts` → `BoundSubdomains` → `SiteHosts` →
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
saves (private lists included), visitors' reads of its saved data and lists, and visitor sign-in
answer 403 `site_offline`, it leaves the person page, and nothing is deleted; the owner's key and
connector still deploy, read and write. An operator take-down wins; the marker follows rename,
delete and restore and is re-synced at boot.
Look before it goes live: an update with `?publish=false` stores the version (`versions.status`
`ready`) without making it live and answers `unpublished_version` and `preview_url`; the owner mints
an hour-long preview link for any kept version (a bearer link: anyone who has it can open it for
that hour; the copy says so), served on the site's own host at
`/__preview/<n>/<token>/…` (person path `/<site>/__preview/…` until the certificate is ready; HMAC
over site+version+expiry with the per-process export key, own domain string), noindex, no-store,
saves from it refused, private lists included (403 `preview_read_only`, by same-host `Referer`: a
page that suppresses its Referer is not stopped, so this guards against accidents; the pages are
the owner's own and share the live site's origin); "Make live" is the rollback,
which marks the version `active`.
**Status: live.**

**Versions kept per site (2026-09-29).** Every deploy is a full copy, so a site republished on
every change (a photo library) holds its size times its whole history. `sites.keep_versions`
(0 = the instance's `KEEP_VERSIONS`, N ≥ 1 = the newest N plus always the live one) is set by the
owner with `PUT /v1/sites/{sitename}/keep-versions` or the Versions panel on the owner app;
setting it prunes at once and every later deploy prunes to it, through the same
`pruneThreshold` / `db.PruneVersions` / `DeleteVersion` path as `KEEP_VERSIONS` (rows first,
then folders; the live version is never removed, re-read inside the DELETE so a racing rollback
keeps its version). Lowering it deletes history for good, so a deploy-only key is refused.
Disk usage (`/v1/admin/usage`) is a walk of the data directory, re-taken on the next request
after any deploy, prune or delete (`DiskStorage.Changes`), so it drops at once. Needs migration `v075-site-keep-versions.sql`.
**Status: live.**

**Site passcode (INTENT 2026-09-29). Status: flag** (`SITE_PASSCODES`, default on, needs
`PASSCODE_ENC_KEY`; on at simple-host.app since 2026-09-30). An
owner may put one passcode on a whole site (owner app "Passcode" dialog: set, "Make one for me",
show, copy link and passcode, change, sign everyone out, remove; `PUT /v1/sites/{sitename}/lock`;
MCP `set_site_passcode`, which asks the person first). Every address of the site (its site host,
the person path while its certificate is pending, a claimed free name, a custom domain) then
answers a plain "This site is protected" page (401, noindex, no-store, no site title, owner or
preview image; `robots.txt` answers `Disallow: /`); old `sites.<domain>/<h>/<s>/` links 302 to
the site's own address and never show the gate. A right passcode (`POST /v1/site-unlock`, a
same-origin form) sets an unlock cookie for that one host (`__Host-sh_pass_<id>`, HttpOnly,
SameSite=Lax, 400 days, HMAC over site, host, expiry and `sites.passcode_generation`) that lasts
until the passcode changes or the owner signs everyone out; `.app` and `.site` hosts each need
their own unlock. The passcode is any `PASSCODE_MIN_LENGTH` (default 6) to 128 characters, no
format rules, surrounding spaces trimmed ("generate" makes 6 digits), sealed with AES-256-GCM under
`PASSCODE_ENC_KEY` (`sites.passcode_enc`, the site id as associated data) so the owner can read
it back, and checked in constant time. The same code again changes nothing; a different one signs
everyone out. The page-facing data routes (state, collections, `/data`, kinds, history, `me`,
visitor sign-in, both `/v1/sites` and `/v1/u` forms) answer 403 `site_locked` without the
unlock cookie for that host, the owner's or admin's key or connector token, or (reads only) a
valid preview page; pages on the site's `allowed_origins` cannot read it. Previews bypass it (the
preview link is the capability). The site leaves the person page; site objects carry
`passcode_protected: true`. Take-down (410) and offline (503) win over the passcode. Refused with
409 `passcode_needs_own_address` where every site shares one address (`SITE_HOSTS` and
`PERSON_HOSTS` off: small boxes, event boxes) and 409 `passcodes_not_enabled` when
`SITE_PASSCODES=off` or `PASSCODE_ENC_KEY` is unset (sites that already have one keep asking).
Wrong tries: per site and address `RATE_LIMIT_PASSCODE_IP` (5,3m) then
`PASSCODE_LOCKOUT_MINUTES` (15), per site `RATE_LIMIT_PASSCODE_SITE` (60,1m) then
`PASSCODE_SITE_LOCKOUT_MINUTES` (15) in which every try is refused (visitors already in are not
affected), 429 with `Retry-After`. The admin can remove a passcode and mint a preview link for
moderation (both logged) and never reads the code. No end dates: owners use offline or delete.
The copy everywhere says: "A passcode keeps out people who don’t have it: search engines, link
previews, and anyone who finds or is forwarded the link without it. Anyone you give it to can
open the site and pass it on. It is not a login, it doesn’t tell you who visited, and it doesn’t
make saved data private per person. Changing it signs everyone out. Pages people already opened
may stay in their browser. Simple Host can still read the site." Disk-served addresses: a
`passcode` marker file next to `current` makes nginx and Caddy hand the request to
`GET /internal/passcode/{rest...}` (written before the database on lock and removed after it on
unlock, so a failure leaves the site closed; re-synced at boot). Needs migration
`v076-site-passcode.sql`.

**Idle-site cleanup (INTENT 2026-09-27). Status: flag** (`IDLE_CLEANUP=on`; default off). A site
with no visits by people (analytics class `person`; the owner's own visits cannot be told apart,
so any person's visit counts), no new version, no saved-data or list write and no other change
by its owner (rename, offline, visibility: `sites.updated_at`) for 90 days gets its owner an
email (Resend, reply-to support@simple-host.app) with a **Keep it** link (resets the clock, no
sign-in) and a link to their page (sign in there to download it). 30 days later with nothing
done it moves to Recently deleted and a second email carries a **Restore it** link. The emailed
links open a confirmation page and act only on its button (POST), so mail scanners change
nothing; they do nothing while the site is taken down, and all of an account's links end when
its sign-in email changes (a live site's pending warning is dropped, so the new address is
warned). Every restore (owner app, API, MCP, the link) ends the idle state and restarts the
clock. Exempt: a custom domain or claimed name (current, earlier or retired), the **Keep** flag
(owner app "Keep for good", `PUT /v1/sites/{sitename}/keep`, MCP `keep_site`), admin, event
(holds an "event account" key) and plugin-reviewer (`REVIEW_ACCOUNT_EMAIL`) accounts, handles
in `IDLE_CLEANUP_EXEMPT_HANDLES`, taken-down sites, sites taken offline by their owner (a warning
pending when a site goes offline is dropped), suspended accounts, preview sites. Runs every 6 h,
at most `IDLE_CLEANUP_MAX_EMAILS` (default 50) emails per run, removals first. The warning is
recorded in the transaction that sends it (committed only after the email is accepted; a crash
in between means a repeat email, never an unwarned removal); the removal re-checks every
condition in the transaction that moves the site and sends its email before the files move, so
a Keep, deploy, save or new domain that lands mid-run wins and a failed send leaves the site in
place. Nothing runs without 90 days of visit records and an analytics ingest in the last 6 h.
Link tokens are random, stored as SHA-256, replaced at every step and spent on use. The admin
page shows a dry run ("Sites that would be warned / removed") on or off.

| Surface | Details |
|---|---|
| Routes | `POST`/`PUT /v1/sites/{sitename}` (archive) · `POST`/`PUT /v1/sites/{sitename}/files` (inline JSON, base64 allowed) · `GET /v1/sites` · `PATCH /v1/sites/{sitename}` (`{"name"}` renames, `{"offline"}` takes offline / back online) · `DELETE /v1/sites/{sitename}` (to Recently deleted; refused while taken down) · `POST /v1/sites/{sitename}/restore` · `GET /v1/me/deleted-sites` · `GET /v1/sites/{sitename}/versions` · `GET /v1/sites/{sitename}/versions/{version}/files` · `GET /v1/sites/{sitename}/versions/{version}/files/{path...}` · `PUT /v1/sites/{sitename}/active-version` (also makes a stored version live) · `POST /v1/sites/{sitename}/versions/{version}/preview-link` (owner mints an hour-long preview address; 409 `preview_unavailable` with no address of its own) · `?publish=false` on `PUT /v1/sites/{sitename}` and `PUT /v1/sites/{sitename}/files` · `?create=1` on the same two PUTs creates the site when the caller has none of that name (the create checks apply: reserved name, site quota, first version always live; 201), so CI deploys in one call; `POST` onto an existing site is 409 `site_exists` saying to use PUT, and a PUT to a missing site without it is 404 `not_found` saying to add `?create=1` · `PUT /v1/sites/{sitename}/visibility` (`public`/`unlisted`) · `PUT /v1/sites/{sitename}/keep` (`{"keep"}`; idle cleanup never flags a kept site) · `PUT /v1/sites/{sitename}/keep-versions` (`{"keep_versions": N}`: the newest N plus the live one, 0 = the instance's `KEEP_VERSIONS`; prunes at once, rows then folders, and every later deploy prunes to it; answers `removed_versions`; not for deploy-only keys) · `GET`/`POST /v1/idle/keep`, `GET`/`POST /v1/idle/restore` (`t`; the idle-cleanup email links, no key; GET shows a confirmation page, POST from its button acts; HTML pages; rate-limited per IP) · `GET /v1/sites/{sitename}/export.tar.gz` (files + saved data; `collections.json` entries carry `id`, `created_at`, `submitted_by` on private lists, `data`) · `POST /v1/sites/{sitename}/export-link` (owner mints a 10-minute signed link) · `GET /v1/export` (`?token=`; the same archive, no key: HMAC over owner+site+expiry, per-process key, 404 `export_link_invalid` when expired, tampered, or the site is gone, deleted or changed hands) · `GET /v1/sites` also returns `deployed_at`, `keep`, `keep_versions` (when set), `idle_removal_at` (warned idle site) and, for a domain not live yet, `domain_last_error`, `domain_dns`, `domain_dns_txt`, `domain_expires_at` · `GET /internal/notfound` (branded 404, nginx `@notfound`; a renamed site's old name 302s) · `GET /internal/offline` (the offline page nginx and Caddy hand off to) · `GET`/`PUT`/`DELETE /v1/sites/{sitename}/lock` (the site passcode: read it back, set or change it with `{"passcode"}` or `{"generate": true}`, remove it; owner's key or connector, not deploy-only keys; no-store) · `POST /v1/sites/{sitename}/lock/sign-out-everyone` (409 `no_passcode`) · `POST /v1/site-unlock` (the gate's form, no key: `passcode`, `next`; 303 with the unlock cookie, 401 gate page, 429 `Retry-After`, 403 `origin_not_allowed` from another origin) · `GET /internal/passcode/{rest...}` (the gate, or the file once unlocked, for nginx and Caddy) · `GET /v1/sites` also returns `passcode_protected` |
| MCP tools | `list_sites`, `get_site`, `read_site_file`, `create_site`, `update_site` (`publish: false` returns `preview_url`), `list_versions` (`not_yet_live`), `rollback_site`, `preview_version`, `delete_site`, `list_deleted_sites`, `restore_site`, `rename_site`, `set_visibility`, `set_site_offline`, `set_site_passcode` (`action`: `set`/`remove`/`sign_out_everyone`/`read`), `keep_site`, `export_site` (download link) |
| Skill | `website-deploy/SKILL.md` §Two ways to deploy, §Where a site lives, §Rules that always apply, §Completion standard · `references/packaging-and-validation.md` (Package, Upload, Verify) · `references/operations.md` §Listing, §Rename, §Deploy from CI (PUT ?create=1, GitHub Actions), §Rollback, §Versions kept, §Site passcode, §Delete and restore, §Download a copy · `references/frameworks.md` · `website-deploy-builder/SKILL.md` §Capability tree 1 |
| Pages | owner app `st/showcase.html` (site inventory with live address, version and last deploy; versions, rename, visibility, Take offline / Put back online, Passcode (dialog; a "passcode" badge on the site), Versions with Preview and Make live and "Keep the newest N versions", Download (export), Keep for good and the idle warning with its date, taken-down sites with the reason, delete with a count of what goes and "Download first", Recently deleted with Restore; an admin opening someone else's page sees their sites read-only, Open only) · `st/index.html` at `/dashboard` (site cards; for an account with a handle only a list with Manage links to the owner app; full controls for accounts without one and in the admin tab) · `st/notfound.html` |
| Go | `h/site.go` (create/update/list/rename/visibility, route table), `h/offline.go` (PATCH dispatch, offline switch and page), `h/passcode.go` (site passcode: seal/open, gate page, unlock cookie, `passcodeGate` on the data routes, wrong-try limits, owner routes), `internal/db/passcode.go`, `h/preview.go` (publish=false, preview links and serving), `h/deleted.go` (delete, restore, Recently deleted list, purge sweep; `trashSite`/`restoreTrashedSite` shared with the idle cleanup), `h/idle.go` (idle cleanup, Keep flag, email links, admin dry run), `internal/db/idle.go`, `h/versions.go` (retention: `KEEP_VERSIONS`, per-site `keep_versions`, `PUT .../keep-versions`), `h/versionfiles.go`, `h/export.go`, `h/exportlink.go`, `h/sitename.go`, `h/usage.go` (per-site cap), `internal/tarball/{extract,sanitize,validate}.go`, `internal/storage/disk.go` (by-id layout, `handles/` symlinks), `internal/storage/trash.go` (`deleted/` area), `internal/db/queries.go`, `internal/db/deleted.go` |
| DB | `sites` (`deleted_at`: every serving and listing lookup skips deleted rows; `offline_at`; `passcode_enc`, `passcode_set_at`, `passcode_generation`: `v076-site-passcode.sql`; `keep_versions`: `v075-site-keep-versions.sql`; `idle_keep`, `idle_kept_at`, `idle_warned_at`, `idle_removed_at`, `idle_token_hash`: `w2-addr-idle-cleanup.sql`), `versions`, `site_name_aliases` (old names of renamed sites; `internal/db/sitenames.go`), `site_view_hourly`, `collection_items` and `analytics_ingest_state` (read for idleness) |
| Limits | 100 sites per account (`MAX_SITES_PER_ACCOUNT`; admins exempt; sites in Recently deleted count, so restore needs no check); `MAX_SITES_OVERRIDES` (`<handle>:<n>`, comma list, 1–100000) gives named accounts their own cap in place of it, matched at check time against the current handle and earlier ones (it follows a handle change); `/v1/me` and the admin users list report the account's cap as `max_sites`, and the admin Users panel shows "N of M sites"; per-site size `MAX_ARCHIVE_MB` (the upload and its unpacked files, i.e. the version being deployed, never the kept versions; admins not exempt), with `MAX_ARCHIVE_MB_OVERRIDES` (`<handle>:<MB>`, 1–500, matched the same way) giving named accounts their own on every deploy path (archive and JSON, POST, PUT, PUT `?create=1`, the connector); over it is 413 `site_too_large` naming the limit; it holds new deploys only, so a larger site already live stays up and can be rolled back to; `/v1/me` and the admin users list report it as `max_site_mb` and the Users panel shows "up to N MB each"; uploads, rollback, rename, delete, restore and take-down serialised per account+site; upload limiter 30 burst, 0.1/s; delete/rename/restore limiter 30 burst, 0.5/s; Recently deleted keeps a site 7 days; wrong passcode tries `RATE_LIMIT_PASSCODE_IP` / `RATE_LIMIT_PASSCODE_SITE` with their lockouts |
| Env | `DATA_DIR`, `MAX_ARCHIVE_MB`, `KEEP_VERSIONS`, `DEPLOY_SCRIPT`, `PREVIEW_ACCOUNTS`, `PREVIEW_TTL_HOURS` (preview-site expiry sweep), `IDLE_CLEANUP` (`on` enables the idle cleanup), `IDLE_CLEANUP_MAX_EMAILS`, `IDLE_CLEANUP_EXEMPT_HANDLES` (comma list of handles never warned or removed), `REVIEW_ACCOUNT_EMAIL` (also exempt), `SITE_PASSCODES`, `PASSCODE_ENC_KEY` (secret; `openssl rand -base64 32`; changing it makes stored passcodes unreadable), `PASSCODE_MIN_LENGTH`, `PASSCODE_LOCKOUT_MINUTES`, `PASSCODE_SITE_LOCKOUT_MINUTES` |
| External | nginx serves files from `/srv/simple-host/sites/handles/<h>/<s>/` on the content host; Caddy does the same on event instances (`deploy/compose/Caddyfile`); both test the `offline` marker after the `suspended` one and hand off to `/internal/offline`, then the `passcode` marker (hand off to `/internal/passcode$uri`) (issuer template `deploy/domain-certs/vhost.conf.template`; live vhosts via `deploy/prod/nginx-suspended-marker.sh`, which adds both checks to every server block serving a folder under `/srv/simple-host/sites/…/current` — custom domains, the content host, hand-made `by-id`, `$client` and `$sub` vhosts — and exact `/internal/suspended`/`/internal/offline` proxies and a `^~ /internal/passcode/` one where a block has no `/internal/` proxy; tested by `nginx-suspended-marker_test.sh` in `make check`) |

## 2. Per-site and per-person addresses, and legacy redirects

Every site lives at its own origin, `https://<site>.<handle>.simple-host.app/` (files at the
root, `/v1/` for that site only, sign-in, per-person saves and private collections bound to that
host; a sign-in covers that one site). Every handle is a host too:
`https://<handle>.simple-host.app/` lists that person's public sites. Each person gets a
certificate for `*.<handle>.simple-host.app`, issued automatically (usually within ~10 minutes of
their first site; brand-new accounts may queue behind the weekly and daily issuance caps); until it exists their
sites keep the person-path form `<handle>.simple-host.app/<site>/`, and every URL handed out is
whichever address is live. While it is not ready, `GET /v1/me` `address`, each such site's
`address_state`, `who_am_i` `address` / site `address_note`, and a note at the top of the owner
app say so: `waiting` (queued; a rough time from the queue, what the issuer issued in the last
week and its weekly/daily caps) or `failing` (retried after the issuer's 6-hour wait), and that
visitors' sign-ins and browser-kept data start fresh when the address switches. Old `<handle>.simple-host.app/<site>/…` and
`sites.simple-host.app/<handle>/<site>/…` links 302 to the site host (path and query kept). A
renamed site's old name keeps redirecting on all three forms until the name is reused (§1).
**Status: live** (`PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical`, 2026-09-26; design:
`docs/designs/per-site-subdomains.md`).

**Moving to `simple-host.site`** (INTENT 2026-09-28): `SITE_BASE_DOMAIN` is the domain person,
site and free-name addresses live under, and `SITE_BASE_MOVE` how far they have moved from
`SITE_DOMAIN`: `serve` (both answer; `.app` handed out), `canonical` (`.site` handed out),
`redirect` / `permanent` (an old address 302s / 301s to the same labels, path and query under
the base; `/v1/` is never redirected; a site host whose owner has no certificate under the base
yet goes to its person path there, 302). The base's apex, www and reserved names 301 to the app.
Each base has its own certificate hand-off (`SITE_CERT_DIR`, `SITE_BASE_CERT_DIR`); new people
are asked a certificate under the base from `serve`, and none under `SITE_DOMAIN` from
`canonical`. A name is one name under both domains: lookups, claims (stored in the handed-out
form), the handle namespace, sign-in return addresses, origin checks and analytics accept either
form. `simple-host move-site-base --from <old> --to <new> [--apply]` rewrites stored free and
retired names (dry run by default; idempotent). **Status: built, dormant** (`SITE_BASE_MOVE`
unset on simple-host.app; design: `docs/designs/site-base-domain-move.md`).

| Surface | Details |
|---|---|
| Routes | Host-routed, not mux: `<site>.<handle>.<SITE_DOMAIN>` → `SiteHosts` (files at `/`, `/v1` same-origin for that one site only) · `<handle>.<SITE_DOMAIN>` → `PersonHosts` (person page at `/`; `/<site>/…` 302s to the site host once the person's certificate is ready, else serves it by path) · `GET /internal/site-redirect/{handle}` · `GET /internal/site-redirect/{handle}/{sitename}` · `GET /internal/site-redirect/{handle}/{sitename}/{rest...}` (302 from `sites.simple-host.app/<h>/<s>/…` to the site's live address; nginx rewrites into these; `vineetu/eb2-wait` excepted in nginx and in `contentHostOnlySites`) · `LegacyHostRedirect`: a retired name (`legacy_hostnames`) 302s to its site's current address, or "This site was removed" once the site is gone or while it is in Recently deleted (its names stay held); any other unclaimed single-label `<name>.<SITE_DOMAIN>` that is not a handle 301s to `sites.<domain>/<handle>/<name>` (which then 302s as above) · an aliased old handle 301s to the new one (person host), 302s on site hosts, content-host paths and the owner app `/<old>` → `/<new>` · a renamed site's old name (`site_name_aliases`) 302s to its current address on the site host, person path, `/internal/site-redirect/*` and the content host's `@notfound` (`GET /internal/notfound` reads `X-Original-URI`) |
| MCP tools | none directly; site summaries return the live site address (and `address_note` while at the fallback), `who_am_i` the person page and `address` |
| Skill | `website-deploy/SKILL.md` §Service (address form); host strings are rewritten per instance (`h/instancehost.go`) |
| Pages | `st/showcase.html` (person index / public view) |
| Go | `h/sitehost.go` (`SITE_HOSTS` off/serve/canonical, site-host routing, certificate requests and readiness), `h/siteaddress.go` (own-address state: ready / waiting with an estimate / failing), `h/personhost.go` (`PERSON_HOSTS` off/serve/canonical, `PersonPageURL`, `PersonReturnSite`, `contentHostRedirect`), `h/legacyhost.go`, `h/handles.go` (reserved handles, `assignHandle`; `handleSeed`: the instance admin row's first handle is the domain's first label, or `organiser` when that is reserved, instead of `admin-2`), `internal/db/namespace.go` (one namespace for handles, claimed names, reserved and retired names; `RenameHandle`/`RenameHandleTx`, aliases, `HandleRenamedSince`), `h/instancehost.go` |
| DB | `users.handle`, `handle_aliases` (e.g. `admin` → `simple-host-team`), `legacy_hostnames` |
| Env | `SITE_BASE_DOMAIN`, `SITE_BASE_MOVE`, `SITE_BASE_CERT_DIR` (`h/sitebase.go`; served text `h/basetext.go`; `internal/db/sitebasemove.go` and `cmd/server/movesitebase.go`), `PERSON_HOSTS`, `SITE_HOSTS` (needs `PERSON_HOSTS` on), `SITE_CERT_DIR` (e.g. `/var/lib/simple-host-site-certs`: `requests/<handle>` written by the app, `ready/<handle>`, `failed/<handle>`, `issued.log` and `limits` by the issuer and read by the app for the address state), `SITE_DOMAIN`, `CONTENT_HOST` |
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

**www and the bare domain.** Connecting `brand.com` or `www.brand.com` also sets up the other
name (its partner) as a redirect-only host: 301 to the chosen name, both on one certificate.
The TXT record on the chosen name covers both; the answer adds the partner's record
(`dns_partner`: A for a bare domain, CNAME for www). The issuer adds the partner only if it
points here, no other server here answers it, no certificate here it did not issue names it,
and it is not connected to a site of its own; otherwise the chosen name goes live alone and
`partner_status: not_set_up` with `partner_note` says why. A live domain whose partner is not set
up asks the issuer again (certificate expanded) once the partner points here, at most every 6 h.
A partner that later proves itself as a site of its own is taken off the other server first.
A partner another site has bound (any account's, even before it proves itself) is neither
offered nor asked for, and the issuer skips a partner whose `domains/` link points at another
site.
Nothing is stored for the partner: it follows from the domain, its state is the ready marker.

**Address families (`*.<domain>` for every site of an account).** An account connects
`*.<suffix>` once (e.g. `*.trips.example.com`); every site of the account named
`<site_prefix><label>` then answers at `<label>.<suffix>`, including sites made later
(`site_prefix` is optional: with `voucher-`, `meera.voucher.example.com` is the site
`voucher-meera`). No per-site opt-in; another account's sites never answer there; labels in
`ADDRESS_FAMILY_RESERVED_LABELS` (`www`) never name a site. Proof: TXT `_simple-host.<suffix>` =
the family's token plus the wildcard record (`CNAME *.<suffix>` → the CNAME target, or `A`)
pointing only here; nothing is served before that, and a pending family is dropped after 24 h.
Several accounts may wait on one name: the first to prove it wins, the others are dropped and
emailed. Verified = exclusive: no other account can connect it, an overlapping family, or a
custom domain under it (409 `domain_taken`); the same account may put a custom domain under its
own family (the exact domain wins, and the site's family addresses redirect to its domain).
Release 1 certificates: `cert_mode` `wildcard` only; the operator sets up the wildcard
certificate (certbot renews it as usual) and the admin names its lineage (`cert_name`); until
then `certificate.status` is `waiting_for_operator`. `per_host` answers 400
`cert_mode_unavailable` (release 2). Main address (`canonical`, default on): custom domain or
free name > the most specific canonical family (longest prefix, then lowest `rank`, then
suffix) > the site host; when a family address is main, the site host and old links 302 there
and refuse sign-in and saves (`use_custom_domain`). Every family address keeps working either
way. On a family address sign-in (Google, email code with a same-origin request), saved data,
private lists, analytics, the report form, take-down/offline/passcode, rename (old label 302s),
delete (404) and restore work as on a custom domain. A verified family failing its checks is
emailed at 24 h and disconnected at 72 h (reconnecting needs the TXT again). The admin may mark
one `proof_exempt` (the wildcard must still point here). Idle cleanup exempts sites whose main
address is a family (`IDLE_EXEMPT_FAMILY_SITES`). `simple-hack.app` (`EVENT_DOMAINS`) and every
platform zone are refused as custom domains and as family suffixes. **Status: live** (hosted
only; `flag` elsewhere: needs `ADDRESS_FAMILY_CERT_DIR`). Design: `docs/designs/address-families.md`.

| Surface | Details |
|---|---|
| Routes | `POST /v1/sites/{sitename}/domain` (bind; a `<name>.<SITE_DOMAIN>` value takes the free-name path) · `GET /v1/sites/{sitename}/domain` · `POST /v1/sites/{sitename}/domain/check` ("Check again": re-prove now; rate-limited per IP and per account, 429 `rate_limited`) · `DELETE /v1/sites/{sitename}/domain` · `GET /internal/tls-ask` (Caddy on-demand TLS gate) · `GET /internal/domain-redirect/{handle}/{sitename}` · `GET /internal/domain-redirect/{handle}/{sitename}/{rest...}` · host-routed `BoundSubdomains` serves a claimed name or custom domain at its root |
| MCP tools | `connect_domain`, `domain_status`, `remove_domain` (confirm-first: `confirm_domain`); each takes `*.<domain>` (no site) for an address family |
| Family routes | `POST /v1/me/address-families` · `GET /v1/me/address-families` · `GET /v1/me/address-families/{suffix}` (`?sites=1`) · `PATCH /v1/me/address-families/{suffix}` (`site_prefix`, `rank`, `canonical`) · `DELETE /v1/me/address-families/{suffix}` · `POST /v1/me/address-families/{suffix}/check` (rate-limited, 429 `rate_limited`) · `POST /v1/sites/{sitename}/domain` with `*.x` → 400 `use_address_family` · admin: `GET /v1/admin/address-families` · `POST /v1/admin/users/{id}/address-families` (+`cert_name`, `proof_exempt`) · `PUT /v1/admin/address-families/{id}/cert-mode` · `PUT /v1/admin/address-families/{id}/proof-exempt` · `POST /v1/admin/address-families/{id}/check` · `DELETE /v1/admin/address-families/{id}` · `GET /internal/family/{rest...}` (nginx's lives-elsewhere rewrite: redirect to the site's own domain) · host-routed `FamilyHosts` |
| Family fields | family: `family`, `suffix`, `site_prefix`, `rank`, `canonical`, `status` (`pending`/`active`/`failing`), `live`, `last_error`, `expires_at`, `failing_since`, `release_at`, `dns`, `dns_a`, `dns_txt`, `proof_exempt`, `certificate {mode, status: waiting_for_operator/pending/issuing/live/failed, note, expires_at}`, `example_url`, `sites[]` · site responses: `family_address` (main family address), `family_addresses[]` (`site_url` unchanged) · MCP `family {site_prefix, main_address, live, example_url, certificate_note}`; `url` = active domain, else `family_address`, else `site_url` |
| Family code | `h/familyhost.go` (index of live families, resolver, ranking, `FamilyHosts`, lives-elsewhere marker), `h/familyapi.go` (API, checks, lapse, issuer hand-off, admin), `internal/db/families.go`, `internal/storage/families.go` (`families/<suffix>` → `by-id/<user_id>`); tables `address_families`, `family_cert_requests` (migration `v077`); issuer `deploy/family-certs/` (`issue.sh` → `/usr/local/sbin/simple-host-family-certs`, state `/var/lib/simple-host-family-certs`, one `sites-enabled/simple-host-family-<suffix>` per family from `vhost.conf.template`; never issues, renews or deletes a certificate; refuses a family another server answers) · `deploy/prod/family-adopt.sh` (hand-made wildcard vhost → managed file; dry run, `--apply`, `--rollback`) · env `ADDRESS_FAMILIES`, `ADDRESS_FAMILY_*`, `IDLE_EXEMPT_FAMILY_SITES`, `RATE_LIMIT_ADDRESS_FAMILY_CHECK(_USER)` |
| Skill | `connect-domain/SKILL.md` §The free address, §The flow (1–5, Disconnect), §Backend on a connected domain, §Gotchas · `connect-domain/references/registrars.md` (Vercel, GoDaddy, Porkbun, other) · `website-deploy/references/operations.md` §A nicer address · `website-deploy-builder/SKILL.md` §8 |
| Pages | `st/showcase.html` owner app (connect, disconnect, status; for a domain not live yet the certificate status, both DNS records (A/CNAME and TXT), last problem, expiry and Check again) · `st/index.html` (connect/disconnect on the site card, both DNS records and the last problem for a domain not live yet, the www/bare partner's record and state, accounts without a handle and admin tab) |
| API fields | `GET /domain`: `dns` (A/CNAME), `dns_txt` (TXT `_simple-host.<domain>` = token), `certificate_status`, `last_error`, `previous_domain`, `failing_since`, `partner_domain`, `dns_partner`, `partner_status` (`pending`/`live`/`not_set_up`), `partner_note` · `GET /v1/sites`: `domain_dns`, `domain_dns_txt` for a domain not active; `domain_partner`, `domain_partner_dns`, `domain_partner_status`, `domain_partner_note` · MCP `ownership_record` next to `dns_record`, `partner` |
| Go | `h/domains.go` (bind, status, delete, `tlsAsk`, `domainRedirect`), `h/domaincheck.go` (TXT proof, background re-verify with a probe pinned to this server's public address and no redirects, switch of address, lapse + owner email), `h/domaincert.go` (certificate hand-off: request = token + link target + www/bare partner, per-account daily cap), `h/domainpartner.go` (partner name, state from the ready marker, asking again), `h/platformsubdomain.go` (free names, `reservedSubdomainLabels`, `BoundSubdomains`, `siteOwnDomain`, `syncDomainRedirect`), `h/legacyhost.go` (retired names), `internal/db/domains.go`, `internal/db/namespace.go`, `internal/email/resend.go` (`SendNotice`) |
| DB | `sites.custom_domain`, `domain_status`, `domain_verified_at`, `domain_last_error`, `domain_bound_at`, `previous_domain`, `previous_domain_failing_since`, `domain_cert_status`, `domain_failing_since`, `domain_lapse_notified_at`, `domain_token`, `domain_proof_exempt`; `domain_cert_requests` (per-account cap); `legacy_hostnames` (`site_id` NULL once the site is deleted) |
| Env | `CNAME_TARGET` (subdomain CNAME), `CUSTOM_DOMAIN_IP` (apex A record), `SITE_DOMAIN`, `DOMAIN_CERT_DIR` (e.g. `/var/lib/simple-host-domain-certs`: `requests/<domain>` written by the app (the site's token and the domain link's target), `ready/<domain>` (line 1: `partner <name>` or `partner-not-set-up <name>: <why>`) and `failed/<domain>` by the issuer; unset = certificates by hand) |
| Timing | unverified binding expires 24 h after bind unless DNS points here, and 7 days after bind in any case; re-check every 2 min, active verdict re-proved hourly; issuer every 10 min + on request (50 new/day for the box, 5 new/day per account in the app, failed retried after 6 h, DNS and TXT problems after 15 min); Check again: 3 at once per account, then one per 30 s; failing verified domain: email at 24 h, let go at 72 h |
| External | issuer `deploy/domain-certs/` (root timer + path unit; certbot webroot `/var/www/acme`; writes `sites-enabled/simple-host-domain-<domain>` from its template and removes it on disconnect; refuses (failed "already served here", never ready) any domain another server in the full `nginx -T` configuration would answer, by exact, wildcard or regex `server_name`, and withdraws its own server if one appears; reuses only certificates it issued (`owned/`), never another lineage in `/etc/letsencrypt/live`; checks the TXT record and that the link still points at the requesting site; skips taken-down sites; the take-down marker is checked in `location /` so `/v1/` still reaches the app; the www/bare partner joins the certificate (`--expand` for an existing one) only after the same checks, as a redirect-only server block; sandbox test `deploy/domain-certs/issue_test.sh`) + Let's Encrypt on prod; Caddy on-demand TLS on event instances; `scripts/check-reserved-subdomains.sh` compares reserved labels against live nginx |

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

**History and undo (saved-data redesign step 1, INTENT 2026-09-27).** Every PUT, PATCH and
restore keeps the document from before it in `data_history` for 30 days by time
(`SAVED_DATA_UNDO_DAYS`), with who made it (account id, the address they were signed in
with, and owner/admin/visitor/anonymous; the operator's moderation shows as "Simple Host
(operator)", never the admin's address) and when; a write that leaves the document as it was
records nothing. A PATCH keeps only what it changed (a reverse diff in `data_history.diff`),
with a full copy on each day's first change and at least every `SAVED_DATA_SNAPSHOT_EVERY` (50)
changes; any version is rebuilt exactly from the nearest newer full copy. Past
`SAVED_DATA_HISTORY_MAX_MB` (20) per site the oldest versions are thinned, always keeping each
day's first full copy and the rows that hold no value (who deleted what): at once when a write
crosses it (`sites.history_bytes`, a running size kept by triggers on `data_history`; at most
once a second per site, `boundHistory`), and by the sweep. The undo-window purge keeps an expired
change while an earlier change to the document is still kept (a diff is rebuilt through every
newer change). The owner (key,
connector, admin) lists changes, reads a version and restores it (a restore is itself a
change), and can **clear the site's history** for good (`{"confirm": "<site>"}`, logged).
PATCH keeps numbers exact (`UseNumber`; a whole-number `inc` adds exactly; whole numbers of any
size match exactly in `removeWhere`, 1 and 1.0 still match; a float sum that overflows is a
400). `Idempotency-Key` on PATCH is saved once for a writer with an identity (§18).

**The per-site cap** (review fix 2026-09-27): a site's **live** saved data, the document plus
live list items, may not grow past `SAVED_DATA_SITE_MAX_MB` (50); history and Recently deleted
are not counted (history has its own cap above). Only a write that grows it is refused (507
`site_full`); a shrinking or same-size write always goes through, owner included, and deleting
items or clearing a list makes room at once. The size is a running counter,
`sites.data_bytes` (+ `state_bytes`), kept by triggers in the same transaction as every write,
so the check is one row read, never a scan. Nothing a page may do changed: visitor PUT,
non-object documents and any `inc` size still work; the watch (§11) counts them for the
`SAVED_DATA_WATCH_DAYS` (7) window before a later step tightens them. **Status: live once deployed.**

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/state` · `PUT /v1/sites/{sitename}/state` (whole doc, `If-Match` CAS) · `PATCH /v1/sites/{sitename}/state` (op list, `Idempotency-Key`) · `OPTIONS /v1/sites/{sitename}/state` — all `(+/v1/u)` · `PUT /v1/sites/{sitename}/allowed-origins` (owner: extra origins allowed to call) · owner: `GET /v1/sites/{sitename}/state/history` (changes, newest first) · `GET /v1/sites/{sitename}/state/history/{id}` (the document before that change) · `POST /v1/sites/{sitename}/state/history/{id}/restore` · `DELETE /v1/sites/{sitename}/history` (`{"confirm": "<site>"}`: every earlier version of the site's saved data and lists, for good) · the same by handle: `GET /v1/u/{handle}/sites/{sitename}/state/history` · `GET /v1/u/{handle}/sites/{sitename}/state/history/{id}` · `POST /v1/u/{handle}/sites/{sitename}/state/history/{id}/restore` · `DELETE /v1/u/{handle}/sites/{sitename}/history` |
| MCP tools | `get_state`, `update_state`, `data_history`, `restore_data`, `delete_forever` (`history: true`) |
| Skill | `website-deploy/references/backend.md` §Trust model, §Shared JSON state, §Saving from a page with the hosted helper, §Saving from an agent · `website-deploy-builder/SKILL.md` §Capability tree 2 |
| Pages | `st/auth.js` (`SH.state.get/put/patch`) · `st/showcase.html` owner app (saved data read-only, size against the 1 MB limit, Download JSON, **History** with View before and Restore, and **Clear history** behind the typed site name, shown while the data is empty too; the owner's key reads it from any page) |
| Ops | `set`, `inc`, `append`, `remove`, `removeWhere`; `PUT` uses ETag/`If-Match` |
| Go | `h/site.go` (`getSiteState`, `putSiteState`, `patchSiteState`, origin check `authorizeStateOrigin`), `h/stateops.go` (op set, max 100 ops, exact numbers, `watchVisitorOps`), `h/visitorsession.go` (`visitorWriteOK`, returns who writes), `h/saveddata.go` (history routes, delete for good, `siteHasRoom`, `idemBegin`/`idemSave`, `allowRead`, `allowAppend`, `boundHistory`, `adminNeedsHandle`, sweep), `internal/db/datahistory.go` (`WriteSiteState`, `PatchSiteState`, `RecordStateChange`, `stateBefore`, `RestoreStateVersion`, `ThinSiteHistory`, `PurgeSavedData`, `ClearSiteHistory`, `HasRoom`), `internal/db/statediff.go` (reverse diffs) |
| DB | `sites.state`, `sites.allowed_origins`, `sites.allow_anonymous_writes`, `sites.legacy_data` (true for every site until the kinds step), `sites.state_bytes` + `sites.data_bytes` (triggers `sites_state_bytes`, `collection_items_bytes_*`), `data_history` (`prev` or `diff`), `idempotency_keys` (status, ETag, ref, body hash; never a body), `data_watch` (migrations `sd1-saved-data-safety.sql`, `sd1-saved-data-safety2-limits.sql`) |
| Env | `WRITE_AUTH_MODE` (off/log/on) · `SAVED_DATA_UNDO_DAYS` (30) · `SAVED_DATA_HISTORY_MAX_MB` (20) · `SAVED_DATA_SITE_MAX_MB` (50) · `SAVED_DATA_SWEEP_MINUTES` (15) · `SAVED_DATA_WATCH_DAYS` (7) · `SAVED_DATA_WATCH_INC_MAX` (10) · `SAVED_DATA_WATCH_ITEM_KB` (16) · `SAVED_DATA_IDEMPOTENCY_HOURS` (24) · `SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE` (10000) · `SAVED_DATA_READ_PER_SEC` (30) · `SAVED_DATA_READ_BURST` (60) · `SAVED_DATA_APPEND_PER_MIN` (30) · `SAVED_DATA_APPEND_BURST` (30) · `SAVED_DATA_SNAPSHOT_EVERY` (50) · `SAVED_DATA_WATCH_KEEP_DAYS` (90) |
| Limits | 1 MB per state doc; `stateLimiter` 60 burst, 1/s per IP; reads 30/s, burst 60 (429 `rate_limited`) per site and address, or per account for a valid key or connector; a site's live saved data at most 50 MB, refusing only growth (507 `site_full`); size errors carry `item_too_large` |

## 5. Collections, including private collections

Lists (comments, RSVPs, votes) that visitors append to, read by anyone. A collection can be made **private**
on a site's own origin: only signed-in visitors submit, same-origin, never by API key (server
stamps `_submitted_by`/`_submitted_at`),
only the owner (or operator) reads, and the owner may edit items. The owner (or operator) deletes
single items in any list, public included, and empties a whole list after repeating its name
(INTENT 2026-09-27); visitors only append. A list made public again keeps its submitters' emails
private: `_submitted_by` is left out of every read but the owner's (key, or signed in on the
site's own address) and the operator's (`withoutSubmitter`, `ownerBrowserView`). **Status: live.**

Step 1 of the saved-data redesign (INTENT 2026-09-27): every item sent while signed in records
who sent it (`collection_items.submitted_by` + `submitted_email`), public lists included; the
owner's reads add `by` (and the CSV a last `sent_by` column), public reads and the POST answer
are unchanged, and deleting an account takes that person's entries in every list. Deleting an
item or clearing a list keeps the items in the list's **Recently deleted** for 30 days
(`deleted_at`); every edit, delete, clear and restore is in the list's history. The owner
restores one item, all of them, or undoes one change, and can **delete for good** one item of
Recently deleted or all of it (`{"confirm": "<coll>"}`), with its history: for a visitor who
asks to be erased, or a flood of spam (logged; live items are never touched). Recently deleted
does not count toward the site's cap, so clearing a flooded list makes room at once. Items added
without the owner's key are limited per address (`SAVED_DATA_APPEND_PER_MIN`, 429
`rate_limited`). `Idempotency-Key` on POST saves a retried item once for a writer with an
identity. Recently deleted pages by (`deleted_at`, `id`); `next` is an opaque cursor.

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/collections/{coll}` · `POST /v1/sites/{sitename}/collections/{coll}` · `OPTIONS /v1/sites/{sitename}/collections/{coll}` — `(+/v1/u)` · `GET /v1/sites/{sitename}/collections` (owner list) · `GET /v1/sites/{sitename}/collections/{coll}/export.csv` (owner) · `PUT /v1/sites/{sitename}/collections/{coll}/privacy` (owner) · `DELETE /v1/sites/{sitename}/collections/{coll}/items/{id}` (any list) and `PATCH` (private lists only) `(+/v1/u)` · `DELETE /v1/sites/{sitename}/collections/{coll}` `(+/v1/u)` with `{"confirm": "<coll>"}` (clear list; items to Recently deleted) — owner key, connector, owner session on own origin, or admin · owner key/connector/admin: `GET /v1/sites/{sitename}/collections/{coll}/history` · `GET /v1/sites/{sitename}/collections/{coll}/history/{id}` · `POST /v1/sites/{sitename}/collections/{coll}/history/{id}/restore` · `GET /v1/sites/{sitename}/collections/{coll}/deleted` · `POST /v1/sites/{sitename}/collections/{coll}/items/{id}/restore` · `POST /v1/sites/{sitename}/collections/{coll}/deleted/restore` (`{"all": true}`) · `DELETE /v1/sites/{sitename}/collections/{coll}/deleted/{id}` (one item, for good) · `DELETE /v1/sites/{sitename}/collections/{coll}/deleted` (`{"confirm": "<coll>"}`: all of it, for good) · the same by handle: `GET /v1/u/{handle}/sites/{sitename}/collections/{coll}/history` · `GET /v1/u/{handle}/sites/{sitename}/collections/{coll}/history/{id}` · `POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/history/{id}/restore` · `GET`/`DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted` · `POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted/restore` · `DELETE /v1/u/{handle}/sites/{sitename}/collections/{coll}/deleted/{id}` · `POST /v1/u/{handle}/sites/{sitename}/collections/{coll}/items/{id}/restore` |
| MCP tools | `list_collections` (with `deleted` counts), `read_collection` (with `by`), `add_to_collection`, `set_collection_privacy`, `update_collection_item`, `delete_collection_item`, `clear_collection`, `data_history`, `restore_data`, `list_deleted`, `restore_item`, `delete_forever` |
| Skill | `website-deploy/SKILL.md` §Personal details go in a private collection · `references/backend.md` §Collections, §Private collections (1–3, editing, reading as owner, errors) · `references/operations.md` §Private collections · `website-deploy-builder/SKILL.md` §Capability tree |
| Pages | `st/auth.js` (`SH.collection(...).list/append/update/remove`), `st/showcase.html` owner app (every list with a public/private badge and switch, view with a "sent by" column, CSV, delete any entry, edit private entries, Clear list behind the typed name, **History** and **Recently deleted (N)** with Restore, Restore all, Delete forever (type `delete`) and Delete all forever (type the list name)) · `st/index.html` (data tab, CSV export; accounts without a handle) |
| Go | `h/collections.go`, `h/privatecollections.go` (`onOwnDomain`, `strictVisitorSession`, `appendPrivate`, `privateManager`), `h/saveddata.go` (history, Recently deleted, restore, delete for good), `internal/db/collections.go`, `internal/db/datahistory.go` (`SoftDeleteItem`, `SoftClearCollection`, `UndeleteItems`, `RestoreItemVersion`, `ListDeletedItems`, `PurgeDeletedItems`), `h/export.go` (collections in site export, live items only) |
| DB | `collection_items` (`deleted_at`, `submitted_by`, `submitted_email`), `collection_settings` (privacy flag), `data_history` |
| Env | `WRITE_AUTH_MODE` · the `SAVED_DATA_*` knobs of §4 |
| Limits | 64 KB per item (413 `item_too_large`); deleted items kept 30 days; page size 50 default / 200 max; `stateLimiter`; a site with no address of its own (only on instances with `PERSON_HOSTS=off` and no domain) gets 409 `custom_domain_required` for privacy; CSV export is formula-safe |

### Kinds: Page info and Submissions (saved data, step 2)

Step 2 of the saved-data redesign (INTENT 2026-09-27). The owner's agent declares each data name
once as one **kind**, and the kind decides who reads and who changes what; nobody writes access
rules. **Page info** (`content`): one JSON object the owner writes (key, connector, or the owner
signed in on the site; everyone else 403 `owner_only`) and everyone reads; up to
`SAVED_DATA_CONTENT_MAX_KB` (1 MB) each and `SAVED_DATA_CONTENT_NAMES_MAX` (20) names per site;
kept as the name's one row in `collection_items`, so it has the same history, undo and size
accounting. **Submissions** (`entries`): visitors add entries (16 KB each,
`SAVED_DATA_ENTRY_MAX_KB`; 10,000 live per name, `SAVED_DATA_ENTRIES_MAX`, 409 `list_full`);
private to the owner by default (`visibility: public` to show them to everyone, without who sent
them); each signed-in visitor lists (`?mine=1`), changes (`PATCH`, stamps never change) and
withdraws (`DELETE`) only their own (anything else is 404), and can undo their own withdrawal for
`SAVED_DATA_WITHDRAW_UNDO_MINUTES` (10; the owner restores anything for 30 days);
`one_per_person` (409 `one_per_person` with the id of the entry they have); `?count=1` follows
the list's read rule. **Email on new submissions** (`notify`): `daily` by default for private
Submissions, `off` for public ones, or `each` (at most one email per name every
`SAVED_DATA_NOTIFY_EACH_MINUTES`, counting what arrived); sent through the Resend notice sender
to the site owner's address, with a "stop these" link (HMAC-signed, confirmation page, then POST).
**Who may save here** (a site setting, every visitor write on the site: page data, lists and
Submissions): anyone who signs in (default), or only listed emails and whole `@domains`, plus a
block list in either mode (403 `not_allowed_to_save`), filled from **Block** next to any entry;
the owner always may (save and undo); a blocked visitor can still withdraw their own entries. A
person is a signed-in account: a block and one per person also match its address with the `+tag`
dropped, and Block by entry uses only the address the server stamped. Declared Submissions take
entries only from a signed-in visitor (401 `visitor_auth_required`) on the site's own address
(declaring them needs one: 409 `custom_domain_required`); at most
`SAVED_DATA_ENTRIES_NAMES_MAX` (50) Submissions names per site. Making a private name that holds
entries public (declaring it public Submissions, or `{"private": false}` on the privacy switch) is
409 `confirm_public` (with the count) until `confirm_public: true`; the owner app asks first. A
private name becomes Page info only while it is empty (409 `has_entries`). Both count the
name's Recently deleted (a restore brings it back) and run under the name's lock; a save that
lands just as the kind or the privacy changes is refused (409 `kind_changed`, nothing saved). Page info brings a
deleted document back only while it holds none (409 `one_document`), so it stays one document. A
Page info read shows `_submitted_by` to nobody but the owner. With `WRITE_AUTH_MODE=off` public Submissions are
refused (409 `visitor_sign_in_off`: that mode reads no visitor sign-in on public saves). Submission emails are claimed before they are
sent (once across servers; a failed send is not retried) and count the entries after the last one
emailed, by id, so one saved just as a digest ran is in the next. **A name nobody declared is Shared**
(owner decision 2026-09-27): anyone reads it and signed-in visitors save to it, as before the
kinds, on every site, so old skills, AI create and uploads keep working; `/kind` says `label`
"Shared", `accepts_saves: true`; the owner app shows a "shared" badge. With
`SAVED_DATA_DEFAULT_KIND=declare_first` a site made after the kinds (`legacy_data` false) takes
no saves under an undeclared name, the owner's included (409 `declare_first`, naming the call to
make), and `set_collection_privacy` there declares the name as Submissions; sites from before
(`legacy_data`) stay Shared either way. `SH.data(name, kind)` refuses an undeclared name
(`declare_first`), so a page asking for Submissions never saves to a public list. Lists gain the
visitor's own edit/withdraw. Page data (`/state`) is unchanged on every site; the three
tightenings wait for the 7-day watch. **Status: built (branch sd/step2).**

| Surface | Details |
|---|---|
| Routes | page-facing (`(+/v1/u)`): `GET /v1/sites/{sitename}/data/{coll}` (Page info document, or the list; `?mine=1`, `?count=1`) · `GET /v1/sites/{sitename}/data/{coll}/kind` (public: kind, visibility, one per person) · `POST /v1/sites/{sitename}/data/{coll}` (add an entry) · `PUT /v1/sites/{sitename}/data/{coll}` (Page info, owner) · `PATCH`/`DELETE /v1/sites/{sitename}/data/{coll}/items/{id}` (own entry; the owner's go to the list's owner edit/delete) · `POST /v1/sites/{sitename}/data/{coll}/items/{id}/undo` · `OPTIONS` on each · the same by handle: `GET`/`POST`/`PUT /v1/u/{handle}/sites/{sitename}/data/{coll}` · `GET /v1/u/{handle}/sites/{sitename}/data/{coll}/kind` · `PATCH`/`DELETE /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}` · `POST /v1/u/{handle}/sites/{sitename}/data/{coll}/items/{id}/undo` · owner (key, connector, admin): `GET /v1/sites/{sitename}/data` (every name with kind and settings, who may save) · `PUT /v1/sites/{sitename}/data/{coll}/kind` · `GET`/`PUT /v1/sites/{sitename}/savers` · `POST /v1/sites/{sitename}/savers/block` (`{"email"}` or `{"collection", "id"}`) · emailed link: `GET`/`POST /v1/data-notify/stop` |
| MCP tools | `declare_data`, `list_data`, `update_data` (Page info), `set_who_can_save`, `block_person`; `set_collection_privacy` declares on `declare_first` installs |
| Skill | `website-deploy/SKILL.md` §What is this data? · `references/backend.md` §Kinds · `website-deploy-builder/SKILL.md` §Capability tree |
| Pages | `st/auth.js` `SH.data(name, kind)`: `get`/`set` (Page info), `add`/`mine`/`list`/`count`/`update`/`remove`/`undo` (Submissions); writes carry an `Idempotency-Key` and retry once after a network error · `st/showcase.html` owner app: each name with its kind badge (page info / submissions / shared), public/private, one per person; **Settings** (kind, one per person, email me: no / soon after they arrive / once a day); **Who may save here** (anyone / only these people, emails and @domains; blocked); **Block** next to any entry with a sender |
| Go | `h/kinds.go` (`declareData`, `getData`, `putContent`, `updateEntry`, `withdrawEntry`, `undoWithdraw`, `visitorWriteOK` + `saverOK`, savers, `sendSubmissionEmails`, `notifyStop`), `h/collections.go` / `h/privatecollections.go` (kind check, entry rules), `internal/db/kinds.go`, `internal/mcp/kinds.go` |
| DB | `collection_settings` (`kind`, `one_per_person`, `notify`, `notify_sent_at`, `declared_at`), `sites.savers_mode`, `site_savers`, `sites.legacy_data` (true for sites from before the kinds; false on create) · migration `sd2-saved-data-kinds.sql` |
| Env | `SAVED_DATA_CONTENT_MAX_KB`, `SAVED_DATA_CONTENT_NAMES_MAX`, `SAVED_DATA_ENTRY_MAX_KB`, `SAVED_DATA_ENTRIES_MAX`, `SAVED_DATA_WITHDRAW_UNDO_MINUTES`, `SAVED_DATA_NOTIFY_EACH_MINUTES`, `SAVED_DATA_NOTIFY_DAILY_HOURS`, `SAVED_DATA_SAVERS_MAX`, `SAVED_DATA_ENTRIES_NAMES_MAX`, `SAVED_DATA_DEFAULT_KIND` (`shared` \| `declare_first`) |
| Limits | codes `declare_first`, `wrong_kind`, `owner_only`, `one_per_person`, `list_full`, `not_allowed_to_save`, `undo_expired`, `too_many_names`, `has_entries`, `invalid_kind`, `invalid_savers`, `too_many_savers`, `no_author`, `item_too_large`, `confirm_public`, `visitor_auth_required`, `visitor_sign_in_off`, `kind_changed`, `one_document` (Submissions, Page info); `invalid_json` (400: a NUL character, half a surrogate pair or bytes that are not UTF-8, which the database cannot store) |

### Personal and Shared board (saved data, steps 3 and 4)

Steps 3 and 4 of the saved-data redesign (INTENT 2026-09-27). Two more kinds, stored like the
others (a row per item in `collection_items`, the kind in `collection_settings`), with the same
history, undo, size accounting, idempotency and rate limits. **Personal** (`mine`): one private
record per signed-in visitor per name (a habit tracker, saved progress, preferences), kept on the
server against their sign-in so it follows them to any device. Only that visitor reads and
writes it, signed in on the site's own address from a page there: `GET` (their record, `null`
before they save; `ETag`, 304 on `If-None-Match`), `PUT` (the whole record, one JSON object, at
most `SAVED_DATA_PERSONAL_MAX_KB`, 64 KB), `PATCH` (the `/state` ops: set, inc, append, remove,
removeWhere), `DELETE` (their record, restorable), and their own history (`GET .../history`,
`.../history/{id}`, `POST .../history/{id}/restore`, 30 days). Simple Host's owner tools never
show a person's Personal record: not other visitors, not the owner's key, connector, CSV, History,
Recently deleted, Block by entry or the site's download, not the operator (403 `personal_data`).
The site's own pages run in the visitor's browser and can read that visitor's record, so Personal
is as private as the site's pages are trustworthy; every surface says so (declare message, MCP
instructions and tools, skills, owner app, llms.txt, privacy page) and the skills tell the AI never
to write a page that sends a record anywhere else. The owner sees how many people have a record
and their total size (`list_data`, owner app, `?count=1`) from 3 people up (one or two: `few`,
no numbers, no last-save time) and can clear the name for everyone (typed confirmation); Restore
then brings back only what the clear took, never a record its person deleted (a person can delete
a record the clear took, and it stays deleted). Each person has one row per name for good (a save
after a delete writes over it); at most `SAVED_DATA_PERSONAL_PEOPLE_MAX` (1,000) people per name
(409 `people_full` for someone new). A Personal name is stored `private = true`, so a binary
from before v0.7.0 treats it as owner-only (no rollback below v0.7.0 once a Personal name or
board exists). A Personal name must be empty to be declared (409 `has_entries`) and cannot
become another kind while it holds records (409 `has_records`); both checks run under the name's
lock, so a save cannot land between the check and the change. Records go into the person's
"Download my data" and are erased with their account. **Shared board** (`board`): a list a
group keeps together (a shopping list, a kanban, a potluck sign-up). Anyone who can open the
site reads it (who added each item is the owner's to see); signed-in visitors allowed to save
add items (`POST`, one JSON object, at most `SAVED_DATA_BOARD_ITEM_MAX_KB`, 16 KB; at most
`SAVED_DATA_BOARD_MAX`, 2,000 live, 409 `list_full`), and change (`PATCH`, fields merged) and
delete (`DELETE`) any item one at a time; every item carries a `version`, and `If-Match: "<n>"`
on a change is refused with 409 `version_conflict` and the current item when someone changed it
first. Whoever deleted an item brings it back for `SAVED_DATA_WITHDRAW_UNDO_MINUTES` (`.../undo`);
the owner restores anything from History and Recently deleted, never past the board's cap (409
`list_full`); the owner's Restore all on a board names a window (`within_minutes`, required on a
board; the owner app offers 15 minutes to 30 days), so after vandalism it brings back what went
since then, not what people deleted on purpose before. Only the owner empties it (the list clear,
typed confirmation); no visitor route touches more than one item, and visitor adds, changes and
deletes share the per-address write rate and, per signed-in person whatever their address,
`SAVED_DATA_BOARD_WRITES_PER_MIN` (30; 429). A visitor's `_submitted_by`/`_submitted_at` are
dropped on add and change. `GET` carries an `ETag` (304 while unchanged), so pages poll it
(`SH.data(name, 'board').watch(fn)`, which reads every page of the board, as `list()` does); there
is no live feed (decided later). Private Submissions become a board only with `confirm_public`. Both kinds need the site
on an address of its own (409 `custom_domain_required`); at most `SAVED_DATA_PERSONAL_NAMES_MAX`
and `SAVED_DATA_BOARD_NAMES_MAX` (20 each) names per site. **Status: built (branch sd/step34).**

| Surface | Details |
|---|---|
| Routes | page-facing (`(+/v1/u)`): `GET`/`PUT`/`PATCH`/`DELETE /v1/sites/{sitename}/data/{coll}` (Personal: the visitor's own record; a board reads and adds with `GET`/`POST`) · `GET /v1/sites/{sitename}/data/{coll}/history` · `GET /v1/sites/{sitename}/data/{coll}/history/{id}` · `POST /v1/sites/{sitename}/data/{coll}/history/{id}/restore` (Personal, own record only) · `PATCH`/`DELETE /v1/sites/{sitename}/data/{coll}/items/{id}` (board: any signed-in visitor; `If-Match`) · `POST /v1/sites/{sitename}/data/{coll}/items/{id}/undo` (board: whoever deleted it) · `OPTIONS` on each · the same by handle: `GET`/`PUT`/`PATCH`/`DELETE /v1/u/{handle}/sites/{sitename}/data/{coll}` · `GET /v1/u/{handle}/sites/{sitename}/data/{coll}/history` · `GET /v1/u/{handle}/sites/{sitename}/data/{coll}/history/{id}` · `POST /v1/u/{handle}/sites/{sitename}/data/{coll}/history/{id}/restore` · owner: `PUT .../data/{coll}/kind` with `{"kind": "mine"}` or `{"kind": "board"}` · `DELETE /v1/sites/{sitename}/collections/{coll}` clears either |
| MCP tools | `declare_data` (kinds `mine`, `board`), `list_data` (counts and `bytes`); `read_collection`, `data_history`, `list_deleted` refuse a Personal name (`personal_data`); `clear_collection` clears it |
| Skill | `website-deploy/SKILL.md` §What is this data? · `references/backend.md` §Personal, §Shared board · `website-deploy-builder/SKILL.md` §Capability tree |
| Pages | `st/auth.js` `SH.data(name, 'personal')`: `get`, `set(obj)` / `set(path, value)`, `patch(ops)`, `inc(path)`, `clear`, `history`, `restore(id)`; `SH.data(name, 'board')`: `list`, `add`, `update(id, fields, {version})`, `remove`, `undo`, `watch(fn, {every})` (reads send the last `ETag`) · `st/showcase.html` owner app: "personal" badge with "N people have a record · size", no View, spreadsheet or History, "Clear for everyone" and a Recently deleted panel that only restores what the clear took; "shared board" badge with the list's View, spreadsheet, History and Clear; Settings offers both kinds |
| Go | `h/personal.go` (`getPersonal`, `putPersonal`, `patchData`, `deleteData`, `personalHistory`, `restorePersonal`, `writePersonalOnly`, `writeJSONETag`), `h/board.go` (`appendBoard`, `updateBoardItem`, `deleteBoardItem`, `undoBoardDelete`, `boardWriter`), `h/kinds.go` (declare, dispatch), `internal/db/kinds.go` (`SavePersonal`, `GetPersonal`, `ListItemHistory`, `NameHasRows`), `internal/db/collections.go` (`UpdateItemVersioned`) |
| DB | `collection_items.version` (changes counted from 1) · `collection_settings.private = true` for Personal names · migrations `sd3-saved-data-personal-board.sql`, `sd3-saved-data-personal-private.sql` |
| Env | `SAVED_DATA_PERSONAL_MAX_KB`, `SAVED_DATA_PERSONAL_NAMES_MAX`, `SAVED_DATA_BOARD_ITEM_MAX_KB`, `SAVED_DATA_BOARD_MAX`, `SAVED_DATA_BOARD_NAMES_MAX`, `SAVED_DATA_PERSONAL_PEOPLE_MAX`, `SAVED_DATA_BOARD_WRITES_PER_MIN` |
| Limits | codes `personal_data`, `has_records`, `people_full`, `window_required`, `invalid_window`, `rate_limited`, `version_conflict`, `item_too_large`, `list_full`, `too_many_names`, `not_allowed_to_save`, `undo_expired`, `wrong_kind` |

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

**Sign-up source** (2026-09-29): each account records, once at creation, where it came from
(`users.signup_source`: `website` for a same-site browser request or Google from the site's pages;
`connector:<app>` when made on the /mcp consent page, from `signup_client` in the verify body or the
Google return address's `client_id`; `agent` otherwise, with `signup_agent` from
`X-Simple-Host-Client`, else `skill <X-Skill-Version>`, else the User-Agent product token, 40
characters, IP-shaped values dropped; `visitor`, `admin`, `reviewer`), `signup_method`
(email, google, issued, password) and `signup_inferred` for backfilled older accounts. A label
only (`h/signup.go`); shown on the admin page and in `GET /v1/admin/users`. Never an IP.

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
(`missing_api_key`, `wrong_auth_header`, `invalid_api_key`, `key_expired`, `key_expired_idle`).
The dashboard keeps the key in `localStorage['apiKey']`; there is no owner cookie session.

**Deploy-only keys and expiry (2026-09-27).** A key minted from the Keys panel may be **deploy
only** (`POST /v1/me/keys {"scope": "deploy"}`; `api_keys.scope`, default `full`): it may create,
update, roll back and list sites, read their versions and files, and make preview links, nothing
else (no delete, rename, domains, keys, saved data or lists, analytics, account, AI create,
connecting apps). One route table decides it (`deployRoutes` in `internal/auth/scope.go`); the
gate `auth.ScopeGate` wraps the whole mux, so REST routes, the page-data routes that read the key
themselves and the MCP server's calls back into the mux all meet it (403 `deploy_only_key`, with
a recovery hint on MCP). A key unused for `KEY_IDLE_EXPIRY_DAYS` (default 180; 0 = never) stops
working, counted from the later of its last use and `idle_from` (creation, or the day the
migration ran for older keys): 401 `key_expired_idle` saying to create a new key. A panel-minted
key may also carry a fixed expiry (`expires_in_days` 1–3650, `api_keys.expires_at`; 401
`key_expired` with the date). Expired keys stay listed (`expired`: `expired`/`idle`), do not count
toward `MAX_KEYS_PER_ACCOUNT`, and are deleted 30 days after they stopped (the hourly connector
sweep). A deploy key never carries admin powers, even one an admin minted (it lists only the
account's own sites and the site quota applies). An expired key on `/mcp` gets the same 401
`key_expired`/`key_expired_idle` (no OAuth challenge), and an expired deploy key off its routes
gets that 401 rather than 403 `deploy_only_key`. `PUT ?create=1` racing another create of the
same name publishes as an update instead of answering `site_exists`. Treat a deploy key like the
site itself: it can ship code that runs when you view the site. The Keys list shows a "Deploy only" badge, last used, and the expiry or the date it
stops if unused (`scope`, `expires_at`, `idle_expires_at` on `GET /v1/me/keys`).

**Choosing the address at sign-up (2026-09-28).** When an emailed code, link or Google sign-in would
create a new account, the page asks for the address first ("Choose your address", prefilled with the
address it would have been given, checked while typing against `GET /v1/handles/check`; Continue keeps
it) and the account is created with it; closing the question creates nothing and the code still works.
Agents send `choose_handle` and then `handle` to `POST /v1/auth/verify` (skills 0.27.1). The owner
app's Your address → Change checks the same way while typing and before asking to confirm; a refused
save names the address under the field and never signs out or leaves the page.

**Your data (GDPR self-service, 2026-09-27).** "Download my data" (`GET /v1/me/export.zip`,
streamed; the older `/v1/me/export.tar.gz` serves the same zip) gives one .zip: `README.txt`, `account.json` (email, handle, display name, created,
old handles, claimed names incl. retired, custom domains, linked Google/GitHub), `keys.json`
(name, last4, scope, created, last used, fixed expiry; never keys or hashes), `connected_apps.json` (with the approving browser's summary), `visitor.json`
(sites signed in to as a visitor, entries sent to other people's lists and `changed`, changes to
their saved data, while signed in), `sites/<name>/` per live site (the per-site export) and
`recently-deleted/<name>/` per site in Recently deleted (same layout plus `deleted.json`: deleted
and removed-for-good dates; files from the trash folder). Analytics are left out (salted hashes, no personal data; the
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

**Change sign-in email (2026-09-27; hardened after review the same day).** `POST /v1/me/email`
`{"email"}` sends a 6-digit code to the new address AND one to the current address (15 min,
3 tries, the sign-in throttles per address and per account; one pending change per account in
`email_changes`, both codes stored as SHA-256), answering `current_email`.
`POST /v1/me/email/verify` `{"code", "current_code"}` needs both: it moves the account
(`users.username`) to the new address and, in the same transaction, spends every open emailed
code and link for the old address (a verify racing it serializes on the token row, so it never
creates an empty account under the old address), revokes every other API key of the account
(the calling key stays) and every sign-in on sites, ends the account's emailed idle-cleanup links, and records a 7-day
undo link. The old address is emailed "Your Simple Host sign-in email was changed to n***@…"
with that link (token in the fragment, `#t=`, so it never reaches a server log; older `?t=` links still work): `GET /v1/me/email/undo` shows a confirmation page, its button (`POST`) puts
the account back on the old address, signs out every key and every sign-in on sites, and removes Google/GitHub sign-ins and
connected apps added since the change (`email_change_undos`; 409 page when another account holds
the old address now). The account's own key only (400 `not_an_account_key` for a connected app
or the admin env key); 400 `same_email` / `invalid_email` / `codes_required` /
`no_pending_change` / `reserved_email` (the reviewer's address), 401 `invalid_code`, 403
`reviewer_account` / `admin_account` / `event_account` / `preview_account` / `no_current_email`.
An address another account signs in with is refused only after the codes are verified (409
`email_taken`), so asking reveals no more than sign-in does. The handle, sites and connected
apps stay; linked Google/GitHub sign-ins stay linked (sign-in keys on the Google account, not the
email) and are listed with **Unlink** (`GET /v1/me/identities`, `DELETE /v1/me/identities/{id}`,
own key only). Private-list entries keep the address they were stamped with (nothing is
rewritten). Buttons: the owner app's **Sign-in** section, and `/dashboard` for accounts without
a handle.

**Sign-in alerts (2026-09-27).** After each successful owner sign-in (emailed code or link,
Google) and each app connected on the consent screen, one short email: the time (UTC), the
browser or app in a few words (`summarizeUserAgent`: "Chrome on macOS", "curl"; never the IP or
a location; a connected app's self-declared name, and the browser/app words, are quoted in the
body as "reported by the app", cut to 40 characters with addresses, links and domain-like words
removed, never in the subject), and
a link to the owner app (`/dashboard` without a handle) where "Sign out everywhere" and the
switch are. At most one per account, browser/app summary (plus app name for a connection) and
UTC day (`signin_alerts_sent`, pruned after two days). None for API key calls, event accounts
(any `event account` key), the plugin reviewer account (`REVIEW_ACCOUNT_EMAIL`), or an owner who
turned them off (`PATCH /v1/me {"signin_alerts": false}`, own key only; `GET /v1/me` returns
`signin_alerts`; default on). Sent in the background; a sign-in never waits on it.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `POST /v1/auth` (send code) · `POST /v1/auth/verify` (code → key, optional `name`; creates the account if new: with `choose_handle` a new account first gets 409 `choose_handle` + `suggested_handle`, then takes the `handle` sent — refused as `invalid_handle` / `handle_reserved` / `handle_taken` without using up the code; without either it is given one) · `GET /v1/handles/check` (`?handle=`: available, or why not; own handle with X-API-Key; per-IP `RATE_LIMIT_HANDLE_CHECK`; cross-site refused) · `GET /v1/me` · `POST /v1/me/api-key/rotate` (Sign out everywhere: replaces all keys) · `POST /v1/me/sign-out` (ends the calling key) · `GET /v1/me/keys` · `POST /v1/me/keys` (named key) · `DELETE /v1/me/keys/{id}` · `PATCH /v1/me` (display name, handle — taken or reserved is 409 `handle_taken` / `handle_reserved` naming the address, malformed 400 `invalid_handle`: free before publishing; after, even with every site gone, once per 30 days, old handle kept as an alias so every old address redirects, new handle's certificate requested; `signin_alerts`, own key only) · `POST /v1/me/email` (codes to the new and the current address) · `POST /v1/me/email/verify` (both codes; moves the account, other keys revoked; old address told with an undo link) · `GET`/`POST /v1/me/email/undo` (`t` from the link's fragment, posted back; the undo link, no key; GET confirmation page, POST acts; rate-limited per IP) · `GET /v1/me/identities` · `DELETE /v1/me/identities/{id}` (linked Google/GitHub sign-ins, Unlink) · `GET /v1/me/export.tar.gz` (Download my data) · `DELETE /v1/me` (Delete my account, `{"confirm"}`; refused while suspended or holding a taken-down site, 403 `account_suspended`/`site_suspended`; takes every site's lock; domains and the earlier `previous_domain` unlinked and their certificate requests withdrawn only while still this account's) |
| MCP tools | `who_am_i` |
| Skill | `website-deploy/references/register.md` (email-code registration) · `references/operations.md` §API keys (deploy-only, expiry), §Deploy from CI (GitHub Actions) · `references/backend.md` §Saving from an agent (API key) |
| Pages | `st/index.html` (`/dashboard` sign-in: code, Google, paste key; Sign out everywhere), `st/showcase.html` (owner **Keys** panel `#owner-keys`; Your address, with Change; **Your data** `#owner-data`: Download my data, Delete my account with type-to-confirm; **Sign-in** `#owner-signin`: Change email with both codes, linked Google/GitHub sign-ins with Unlink, Sign-in alerts switch), `st/index.html` **Your data** (`#my-data`) and **Sign-in** (`#my-signin`), accounts without a handle, `st/privacy.html`, `st/terms.html`, `st/support.html` (point at the two buttons), `st/connect.html`, `st/partials/header.html` (Sign out → `/v1/me/sign-out`) |
| Go | `internal/auth/middleware.go` (`X-API-Key`, `shk_` keys, 401 codes incl. expired keys, admin key, `RequireAdmin`), `internal/auth/scope.go` (deploy-only route table, `ScopeGate`), `h/user.go`, `h/keys.go` (list/mint/revoke/sign-out), `internal/db/apikeys.go`, `h/emailcode.go`, `h/accounts.go` (`patchMe`, handle validation), `h/account_data.go` (`exportMe`, `deleteMe`, `eraseAccountFiles`), `h/account_email.go` (change email), `h/signin_alert.go` (sign-in alerts, `summarizeUserAgent`), `internal/db/signin.go`, `internal/db/account.go` (`LockAccountForDelete`, `EraseAccount`, export queries), `h/handles.go`, `internal/db/queries.go` (hashed key lookup, `ClaimHandle`), `internal/db/internalkey.go` (in-process per-request keys for the connector), `internal/email/resend.go` |
| DB | `users` (`handle_changed_at`, `signin_alerts`), `email_changes`, `signin_alerts_sent` (`w2-account-signin-email.sql`), `email_changes.old_code_hash` and `email_change_undos` (`w2-signin-email-undo.sql`), `oauth_identities` (Unlink), `handle_aliases` (`user_id` NULL = retired handle of a deleted account; `cp-gdpr-retired-handles.sql`), `api_keys` (`scope`, `expires_at`, `idle_from`: `w3-keys-scope-expiry.sql`), `auth_tokens` (purpose-bound codes; expired ones purged) |
| Env | `ADMIN_API_KEY`, `RESEND_API_KEY`, `MAIL_FROM`, `PUBLIC_BASE_URL`, `KEY_IDLE_EXPIRY_DAYS` |
| External | Resend |
| Limits | `ipLimiter` 20/0.2 s⁻¹ per IP; `emailLimiter` 5/0.02 s⁻¹ per address; at most 50 keys per account (`POST /v1/me/keys` → 409 `key_limit`); key names refuse control and invisible formatting characters; minting locks the account and the caller's key (a key revoked meanwhile gets 401 `invalid_api_key`); admin reissues are logged (`admin_key_reissue`) |

## 8. MCP connector and OAuth (chat apps)

Remote MCP endpoint behind an OAuth 2.1 authorization server (DCR, PKCE, refresh, revoke).
Sign-in on the consent page reuses Google or email code. Tool calls are replayed into the mux
as the person, so they meet the same checks as REST. Connector tokens are stored hashed.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /.well-known/oauth-protected-resource` · `GET /.well-known/oauth-protected-resource/mcp` · `GET /.well-known/oauth-authorization-server` · `GET /.well-known/oauth-authorization-server/mcp` · `POST /oauth/register` · `GET /oauth/authorize` (consent page) · `POST /oauth/authorize/decision` · `POST /oauth/token` · `POST /oauth/revoke` · `POST /oauth/reviewer-signin` · `POST`/`GET`/`DELETE /mcp` · `GET /v1/me/connections` (name, connected, last used, and `device`: the consent page's browser summarised, "Chrome on macOS", kept on the code and copied to the grant; never the user agent or an IP; empty for connections made before 2026-09-27) · `DELETE /v1/me/connections/{client_id}` |
| MCP tools | all 27 (see §21); server metadata and instructions in `internal/mcp/instructions.go` |
| Skill | `website-deploy/SKILL.md` §Service, §Two ways to deploy (connector vs key); `openai-plugin/skills/website-deploy/SKILL.md` is the connector-only variant |
| Pages | `st/connect.html` (consent; own nonce CSP in `consentHeaders`), `st/showcase.html` (Connected apps) |
| Go | `h/connector.go` (AS, `BearerAuth`, `serveMCP`, connections, hourly sweep), `h/reviewer.go` (password sign-in for one designated store-review account), `internal/mcp/{server,jsonrpc,tools,outputs,instructions}.go`, `internal/db/connector.go`, `internal/db/internalkey.go`, `cmd/server/oauthclient.go` (`simple-host oauth-client …`, hand-registered clients e.g. a GPT Action), `cmd/server/reviewaccount.go` (`simple-host review-account …`) |
| DB | `oauth_clients`, `oauth_grants`, `oauth_codes`, `oauth_tokens` (`device` on grants and codes: `w3-connection-device.sql`) |
| Env | `PUBLIC_BASE_URL`, `REVIEW_ACCOUNT_EMAIL`, `REVIEW_ACCOUNT_PASSWORD_HASH`, `ADMIN_API_KEY` |
| Tokens | PKCE S256 only; code 60 s, access 1 h, refresh 90 days rotating (reuse revokes the grant); scope `sites`; tool calls run with a per-request `shint_` internal key |
| Errors | a refused tool call returns the server's message, its `code`, and one recovery hint chosen by the code (`codeHints` in `internal/mcp/tools.go`: `site_exists`, `domain_taken`, `invalid_name`, `name_reserved`, `invalid_domain`, `site_quota_reached`, `append_only`, `custom_domain_required`, `not_an_object`, private-list and sign-in codes, `site_suspended`, `account_suspended`, `deploy_only_key`, `key_expired`, `key_expired_idle`); the HTTP status picks the hint only when there is no known code. REST errors carry the same `code` (openapi `Error` schema) |
| Limits | register 10 burst, 10/h; authorize and token 30 burst, 0.5/s; reviewer sign-in 10/IP then 1/min, 30 global then 30/h |
| Tests / e2e | `h/connector_test.go`, `h/reviewer_test.go`, `internal/mcp/*_test.go`, `scripts/e2e-connector.py`, `scripts/e2e-connector-browser.mjs`, `scripts/e2e-reviewer.py`, `scripts/seed-reviewer-demo.py` |

## 9. Skills and plugin distribution

Skills source is `simple-host-website/skills/` (embedded via `simple-host-website/embed.go`) at
version **0.27.0**, served over HTTP, packaged as a Claude plugin, an OpenAI/ChatGPT plugin, a
standalone plugin repo, and via `npx skills add vineetu/simple-host`. **Status: live**
(ChatGPT and Claude directory listings submitted 2026-09-24, pending).

Every skill that publishes, deletes or changes visibility tells the agent to check with the
person first: before a new site goes online the first time it asks once (name, address, public
to anyone with the link); it always asks before deleting a site or data, making private data
public, changing who can see or save, connecting a domain, rolling back or taking a site
offline; updates the person asked for to a site from the same conversation go ahead without a
second question. `llms.txt` and the connector's server instructions say the same (INTENT
2026-09-28).

| Surface | Details |
|---|---|
| Routes | `GET /skills.zip` (excludes `run-hackathon`) · `GET /skills/version` · `GET /skills/{dir}.zip`, `GET /skills/{dir}/SKILL.md`, `GET /skills/{dir}` and `GET /skills/{dir}/references/{file}` (per bundled skill dir, registered in a loop) · `GET /plugin.zip` · `GET /install.sh` · `GET /install.ps1` · `GET /v1/skills` · `GET /v1/skills/{name}` · `GET /v1/skills/{name}/SKILL.md` · `GET /v1/skills/{name}/references/{file}` · `GET /.well-known/skills/index.json` · `GET /.well-known/skills/{name}/SKILL.md` · `GET /.well-known/skills/{name}/references/{file}` · `GET /.well-known/openai-apps-challenge` · `GET /{asset}` for each of `rewrittenAssets` (only on non-canonical instances). Every skill file route (SKILL.md and references, on `/skills/`, `/v1/skills/` and `/.well-known/skills/`) and the zips serve the text with this instance's hostnames and limits (`MAX_ARCHIVE_MB` and the limit knobs) written in, except the control-plane skill (`skillServedText`, `copyRewritten`) |
| Skills | `website-deploy` (SKILL.md + references `backend.md`, `operations.md`, `packaging-and-validation.md`, `register.md`, `frameworks.md`), `website-deploy-builder`, `connect-domain` (+ `references/registrars.md`), `run-hackathon` (source only; not in the plugin or `/skills.zip`) |
| Pages | `st/install.html`, `st/llms.txt`, `st/openapi.yaml` / `st/openapi.json`, `st/docs.html` (Swagger UI) |
| Go | `h/ui.go` (zips, install scripts, `PluginVersion`), `h/skillshub.go` (catalog; not host-rewritten), `h/instancehost.go` (`rewrittenAssets`, `controlPlaneSkills`), `h/notice_middleware.go` (`X-Skill-Version` → `_notice`), `h/openaichallenge.go`, `simple-host-website/embed.go` |
| Packaging | `plugins/simple-host/` (Claude plugin: `.claude-plugin/plugin.json`, `.mcp.json` → `https://simple-host.app/mcp`), `.claude-plugin/marketplace.json`, `openai-plugin/` (plugin.json 0.9.1, mcp.json, skills rewrite, assets, demo-sites, SUBMISSION.md), `dist/*.zip`, `simple-host-website/` (legacy plugin, `mcp-server/` Node stdio MCP, `setup.sh`, `template/`) |
| Scripts | `scripts/sync-claude-plugin.sh` (copy source → plugin, stamp version), `scripts/check-claude-plugin.sh` (drift + `X-Skill-Version` literals), `scripts/publish-claude-plugin-repo.sh` (→ github.com/vineetu/simple-host-plugin, tag `v$V`), `scripts/build-openai-plugin.sh`, `scripts/check-docs-sync.sh` (routes ↔ openapi ↔ llms.txt ↔ skills) |
| Env | `PUBLIC_BASE_URL`, `SITE_DOMAIN`, `CONTENT_HOST`, `CNAME_TARGET` (host rewriting), `OPENAI_APPS_CHALLENGE` |
| External | Claude plugin directory, OpenAI apps portal, GitHub `vineetu/simple-host-plugin`, skills CLI (`npx skills`) |

A separate Simple Hack submission kit lives in `hack-toolkit/`: deterministic
skill-only, OpenAI MCP/plugin, Claude plugin and standalone skill ZIP downloads,
plus a static download/checklist website. It uses the live Simple Hack MCP URL,
its organiser/team roles and the maintained run-hackathon skill (0.27.9). The
full MCP directory submission still needs a reviewer account, demo and portal
checks; a package is not an approved directory listing.

## 10. Owner dashboard and owner app

Sign-in page and dashboard at `/dashboard`; the owner app at `/<handle>` on the apex (same
template as the public person page, hydrated for the owner); per-site analytics page. The
owner app is the one place an account with a handle manages its sites: address, version, last
deploy, visibility, versions, rename, domain, lists, saved data, Download and Delete
(INTENT 2026-09-27). `/dashboard` sends such an account there; the apex view it can still reach
(`/?new=1`) lists the sites with a Manage link. Accounts without a handle and the admin tab keep
the full apex controls. Sign out everywhere (key rotate) stays in the apex app bar.
**Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /dashboard` (`index.html`) · `GET /` (landing; a bare `/<handle>` renders the owner app via `ownerAppOrStatic`) · `GET /analytics/{sitename}` · `GET /internal/showcase/{handle}` (public person page on the content host) |
| MCP tools | — (the dashboard uses REST with the stored key) |
| Pages | `st/index.html`, `st/showcase.html`, `st/analytics.html`, `st/partials/{head,header,footer,theme}.html`, `st/site.css` |
| Go | `h/ui.go` (`RegisterUIRoutes`, `serveStaticPage`, `adminUICSP` nonce CSP, `handlerOnlyPages`), `h/showcase.go` (`renderShowcase`, `renderNotFound`), `h/chrome.go` (header/footer injection, `HackHome`), `h/analytics.go` |
| Calls | everything in §1, §3, §5, §7, §8 (connections), §12, §14 |
| Note | Apex pages allow inline `<script>` only via the per-response nonce; `onclick=` attributes are blocked. `setup.html` is outside this wrapper |
| Theme | One light/dark setting for every page the app serves (2026-09-28). With nothing picked a page follows the visitor's system setting, live; the header's theme button opens **Match my system / Light / Dark**, and the choice is kept once (`localStorage` `sh-theme`: `light`, `dark`, or absent for the system) and applied to every page before first paint, other open tabs included. The only theme code is `st/partials/theme.html` (in the head partial; `<!--sh:theme-->` alone for pages with no header: the first-run wizard `setup.html`, the offline and taken-down pages, the sign-in-failed and temporarily-unavailable pages); pages style both themes from `html[data-theme]` and site.css tokens, the navy pages (`/enterprise/architecture`, `/setup`, the enterprise Ask panel) with their own navy dark palette. Person and site hosts are other origins, so they follow the system until the person picks there. `theme_test.go` fails on any page with its own theme logic or `prefers-color-scheme`; `scripts/e2e-theme.js` checks it in a browser |
| Site order | The dashboard's site list and the owner app's Site Inventory have a Sort control, **Recently updated** by default (2026-09-29): newest `deployed_at` first (when the live version went up; a site with nothing deployed uses `created_at`), then **Recently created** (`created_at`), and on the dashboard Most people / Most bots / Most traffic (all) / Name, on the owner app Name / Most viewed (people, 30 days, from `GET /v1/analytics/sites`, fetched when chosen). Each card or row shows the matching date in muted text ("updated 2 days ago", "created 3 Sep"). The choice is kept per browser (`localStorage` `sh-site-sort` on the dashboard, `sh-owner-site-sort` on the owner app); ties go by name |
| Dialogs | No page calls the browser's native `confirm`, `alert` or `prompt` (2026-09-28): an AI browser agent cannot see or press those, so the page hung. Every question is asked in the page by `shConfirm` / `shPrompt` / `shAlert` (`st/partials/dialog.html`, in the head partial; styles `.sh-dlg` in site.css): a modal `<dialog>` with a title, real buttons named for the action ("Yes, change my address", "Delete site", "Cancel"), focus moved into it and back, Escape or the backdrop cancels. Used by the dashboard, the owner app, admin and the sign-out warning. `nodialogs_test.go` fails on any native dialog call in a served page, script or Go-built HTML (swagger-ui-bundle.js excepted); `scripts/e2e-dialogs.js` drives the flows in a browser and fails on any native dialog |

## 11. Admin (operator)

Operator console at `/admin`, one sign-in, six tabs (the tab is kept in the address as `#overview`,
`#users`, `#sites`, `#api`, `#moderation`, `#tools`; each tab loads its data the first time it is opened):
**Overview** (users, new in 7 and 30 days, live sites, site storage, disk used, suspended accounts and
taken-down sites; latest 10 sign-ups with their source; sign-ups by source for 7 days, 30 days and all
time; the 10 biggest websites with their live copy and total on disk; disk bar, versions kept and running release/commit;
**Network** for the calendar month (UTC): the box's bytes out and in, with a bar against
`NETWORK_MONTHLY_ALLOWANCE_GB` that turns amber at `NETWORK_ALERT_PCT`, and the bytes the web server sent for websites
today, over 7 and 30 days and this month, split pages / API (`/v1`), with the 10 websites that took the most this
month and their owners), **Users**
(searchable table newest first with Source; a row opens that person's sites and **New key**,
Suspend / Re-enable, Delete), **Sites** (every site in one sortable, searchable table with its live size and size on disk, 50 rows a page,
filters All / Taken down / Has custom domain / Unlisted, default most recently updated; a "⋯" menu per
row with Open, Analytics, Versions (and Make active), Data (lists, rows, CSV), Connect / Disconnect
domain, Take down / Restore, Delete), **API** (Growth: a 7 days / 14 days / 30 days / 6 months
picker; API calls (`/v1/*` and `/mcp`) per day stacked by kind (deploy & sites, saved data, sign-in &
keys, connector, admin, other), new accounts per day and new sites per day, each with its total
and the change against the previous period of the same length ("+34% vs previous 30 days";
6 months is drawn per week); a world map shaded by API calls per country in the range, with a
legend, a tap or hover count and the top 10 countries beside it (under it on a phone), drawn from
`/world-map.svg`, Natural Earth country shapes served by this server, no tiles or outside
scripts; then API traffic: calls, callers, errors and AI builds today, routes and callers; a
request that matches no API route (bots probing `/v1/<anything>`, a method a route does not take)
is not counted at all, and calls from this server itself (loopback and its own public
`CUSTOM_DOMAIN_IP`: health checks, canaries) are kept apart in `api_self_daily` and shown
as "from this server", never as calls, errors, callers or growth),
**Moderation** (taken-down sites with Restore, Idle sites, Saved-data watch) and **Tools** (Issue participant accounts, Entries for judges with
CSV / Copy links / Download all entries). Every account records where it came from (§7 Sign-up
source). Admin = `ADMIN_API_KEY` or the admin user. **Status: live.**

| Surface | Details |
|---|---|
| Routes | `GET /admin` (public shell) · `GET /v1/admin/users` (every user, unpaged, with their sites, ids, page address and suspension state) · `POST /v1/admin/users` (bulk-create participant accounts, returns keys) · `POST /v1/admin/users/{id}/key` (replace that account's keys with one new key, shown once; refused while suspended) · `DELETE /v1/admin/users/{id}` (the same erasure as `DELETE /v1/me`, suspended accounts included) · `POST /v1/admin/sites/{id}/suspend` (`{"reason"}`) and `POST /v1/admin/sites/{id}/restore` (take a site down / put it back) · `POST /v1/admin/users/{id}/suspend` (`{"reason"}`) and `POST /v1/admin/users/{id}/enable` (suspend / re-enable a person) · `GET /v1/admin/sites/{id}/versions`, `PUT /v1/admin/sites/{id}/active-version`, `GET /v1/admin/sites/{id}/collections`, `GET /v1/admin/sites/{id}/collections/{coll}`, `GET /v1/admin/sites/{id}/collections/{coll}/export.csv`, `POST` and `DELETE /v1/admin/sites/{id}/domain`, `DELETE /v1/admin/sites/{id}/lock` (removes a site passcode for moderation; the admin never reads it), `POST /v1/admin/sites/{id}/versions/{version}/preview-link` (a preview link, to see a site with a passcode), `DELETE /v1/admin/sites/{id}` (the Sites menu on anyone's site: the owner route of the same name run as the site's owner, by site id, logged as `admin_site_action`; the handler re-checks the site id, so a rename meanwhile is refused; `h/adminsite.go`) · `GET /v1/admin/export.tar.gz` (every site with saved data and lists, one archive) · `GET /internal/suspended` (the take-down page nginx and Caddy hand off to) · `GET /v1/admin/usage` (`?sizes=1` adds every site's size) · `GET /v1/admin/api-analytics` · `GET /v1/admin/growth?range=7d|14d|30d|6m` (API calls per day by kind and country, new accounts and sites per day, totals and change against the previous period; cached a minute) · `GET /v1/admin/idle-sites` (idle-cleanup dry run: would warn / would remove, whether visit data can be trusted, on or off) · `GET /v1/admin/data-watch` (the saved-data watch: per site, visitor replaces, non-object documents, visitor ops by type, large incs, new list names, large items; `?days=`) · `PUT /v1/sites/{sitename}/allow-anonymous-writes?owner=` (`RequireAdmin`; `owner` picks that person's site, else the oldest of the name) · `GET /v1/sites/{sitename}/analytics?owner=` and `/analytics/geo?owner=`, `GET /v1/analytics/sites?all=1` (admin reads any site) |
| Pages | `st/admin.html` (signed out, with a key the server no longer takes, or with a key that is not the admin's: an **admin key sign-in** in place of the page, checked against `GET /v1/admin/users` (wrong keys count against `RATE_LIMIT_SITE_OPS` per address, so "Too many tries" is real) and kept in the browser only when it is the admin key; over plain http on anything but localhost the form is not shown and the page points to the https address, saying where a small box keeps it). Signed in: tabs Overview / Users / Sites / API / Moderation / Tools as above (`st/admin-growth.js`, `st/admin-growth.css`, `st/world-map.svg` draw the API growth views); tables scroll inside their own box on a phone, no page-level sideways scroll from 320px, 40px touch targets, shared theme in light and dark, questions asked in the page (`shConfirm` / `shPrompt`). `st/index.html` site cards and `st/showcase.html` owner inventory show a taken-down site and its reason; `st/index.html` Admin tab lists every site (its buttons act on the admin's own sites; the admin page's Sites menu acts on anyone's) |
| Go | `h/site.go` (`adminUsers`, `adminUsage`), `h/suspend.go` (take-down: admin calls, `serveTakedown`, refusals, boot marker sync), `h/export.go` (`exportAll`), `h/accounts.go` (`createAccounts`, `reissueAccountKey`, `deleteAccount`, `accountAdmin`), `internal/db/suspend.go`, `internal/storage/disk.go` (`SetSuspended`/`IsSuspended`, the `suspended` marker file), `internal/capacity/capacity.go`, `h/apimetrics.go` (`AdminSummary`), `h/apigrowth.go` (`AdminGrowth`, `BackfillGrowth`, route kinds), `cmd/server/apigrowth.go` (`simple-host api-growth-backfill [NGINX_LOG...]`), `internal/auth/middleware.go` |
| DB | `users` (`suspended_at`, `suspended_reason`, `signup_source`, `signup_agent`, `signup_method`, `signup_inferred`), `sites` (`suspended_at`, `suspended_reason`), `versions`, `api_keys`, `api_request_daily`, `api_ip_daily`, `api_growth_daily` (`v078-api-growth.sql`), `api_self_daily` (`v079-api-self-calls.sql`, which also removed the unmatched requests counted before) |
| Take-down | A suspended site keeps everything; Go answers 410 "This site has been taken down" on every path of its site host, person path and claimed name; nginx (custom domains, content host) and Caddy (event boxes) check the `suspended` marker in the site folder and hand off to `/internal/suspended` (`deploy/prod/nginx-suspended-marker.sh` adds the check to live vhosts; `deploy/compose/Caddyfile`). Deploy, rollback, rename, delete, visibility, address and origin changes, state/list writes and public reads of its data answer 403 `site_suspended`; the owner's key still reads and exports. A suspended person's key, connector token, MCP calls and visitor sessions answer 403 `account_suspended` (with the reason), sign-in is refused, refresh tokens are refused unspent, their sites are down; nothing is deleted and re-enable reverses it (a site taken down on its own stays down). Markers are re-synced from the database at boot. |
| API growth | `api_growth_daily` counts API calls per UTC day twice: by caller country (ISO code, looked up on this box from the shortened address when the call is counted; `XX` = this server or unknown) and by kind of call. No address is stored in it. It is written by the same flush as the traffic tables (`API_METRICS_FLUSH_SECONDS`) and kept `API_GROWTH_RETENTION_DAYS` (400). At every start, and with `simple-host api-growth-backfill`, it adds whatever the traffic tables hold that it lacks (so the 30 days before this release are filled in); the command also reads `/mcp` calls from old nginx access logs for days with none counted. New accounts and sites come from `users.created_at` and `sites.created_at` (a purged site no longer counts). |
| Env | `ADMIN_API_KEY`, `DATA_DIR`, `KEEP_VERSIONS` and `MAX_ARCHIVE_MB` (reported by usage as `keep_versions`, `site_limit_mb`), `MAX_ARCHIVE_MB_OVERRIDES` (usage `site_limit_overrides`, shown in the disk note), `NETWORK_MONTHLY_ALLOWANCE_GB` (10240, Oracle Cloud Always Free's 10 TB outbound; 0 hides the bar), `NETWORK_ALERT_PCT` (75), `NETWORK_SAMPLE_MINUTES` (60), `NETWORK_INTERFACE` (default route's) |
| Network use | `GET /v1/admin/usage` `network`: `box` (`internal/netusage`: the interface's `/proc/net/dev` counters, growth between samples added to `net_usage_daily` per UTC day, last reading and boot id in `net_counter_state`; a reboot or counter reset starts again from zero, so month totals survive both; the page adds the growth since the last sample; first sample counts from boot when the box booted this month; off in a container unless `NETWORK_INTERFACE` is set) and `served` (`traffic_daily`, `site_traffic_daily` from the analytics log's ninth field `$bytes_sent`, every request whatever its status; `bytes_since` the first logged day). Alert: `deploy/prod/sh-network-watch.sh` + `.timer` (daily 15:05 UTC) Signals once a month past `NETWORK_ALERT_PCT`; who to Signal is in `/etc/sh-network-watch.env`, not the repo. Migration `w3-network-usage.sql`. **Status: live.** |

## 12. Analytics and geo

Server-side visitor analytics tailed from the nginx (or Caddy) log into hourly/daily aggregates,
with country from local IP-range data; per-endpoint API metrics for admin. No client script.
Top pages and where visitors came from (2026-09-27): people's views per site-relative page
(query and fragment dropped, percent-escapes decoded once, `//` collapsed, `/index.html` folded
into `/`, at most 200 bytes) and per referring domain (the log carries the referrer's host name
only; a link from the site's own address is not counted), per day. A site keeps at most
`ANALYTICS_PAGES_PER_SITE_DAY` (200) pages and `ANALYTICS_REFERRERS_PER_SITE_DAY` (100) domains a
day; views of further new ones that day are counted as `(other)`, so random paths or referrer spam
cannot grow the tables; `GET /v1/sites/{sitename}/analytics/top?days=` answers the top 20 of each, shown per
site in the owner app's Analytics tab ("Top pages and referrers"), on the per-site analytics
page, and in MCP `site_analytics` (`top_pages`, `top_referrers`).
**Status: live** (`ANALYTICS_LOG` set).

| Surface | Details |
|---|---|
| Routes | `GET /v1/sites/{sitename}/analytics` · `GET /v1/sites/{sitename}/analytics/geo` · `GET /v1/sites/{sitename}/analytics/top` · `GET /v1/analytics/sites` · `GET /v1/admin/api-analytics` · `GET /v1/admin/growth` |
| MCP tools | `site_analytics` |
| Skill | `website-deploy/references/operations.md` §Analytics |
| Pages | `st/analytics.html`, `st/showcase.html` Analytics tab, `st/index.html` site cards, `st/admin.html` API tab (growth and traffic) |
| Go | `internal/analytics/{ingest,classify,geo,countries,rebuild}.go` (attributes views on site hosts, person hosts, claimed names, custom domains; bot/human classes; salted ip_hash), `h/analytics.go`, `h/apimetrics.go` (every `/v1/*` and `/mcp` request; IPs stored as /24 or /48), `h/apigrowth.go` (per-day calls by country and kind, no addresses), `internal/geoip/geoip.go` (DB-IP mmdb, watched), `cmd/analytics-rebuild` (replays the rotated archives oldest first, then the live log), `cmd/ip-country-load`, `web/analytics-parse.js` |
| DB | `site_view_hourly`, `site_visitor_hourly`, `site_geo_daily`, `site_page_daily`, `site_referrer_daily` (`w3-analytics-pages-referrers.sql`), `traffic_daily`, `site_traffic_daily` (bytes and requests per day, pages / API; `w3-network-usage.sql`), `site_view_daily`, `site_visitor_daily` (legacy, pruned after 400 days), `analytics_ingest_state`, `ip_country_ranges`, `api_request_daily`, `api_ip_daily`, `api_growth_daily` |
| Env | `ANALYTICS_LOG`, `ANALYTICS_SALT`, `GEOIP_DIR` |
| External | nginx `log_format shanalytics` (`deploy/prod/nginx-analytics-logformat.conf`, query string stripped, eighth field the referring host only, ninth `$bytes_sent` (2026-09-30); installed by `deploy/prod/nginx-analytics-logformat-apply.sh`, dry run by default; the ingester reads 7-, 8- and 9-field lines), `deploy/prod/logrotate-analytics.conf` (29 archives: raw IPs ≤30 days), DB-IP Lite via `scripts/geoip-refresh.sh` + `deploy/prod/simple-host-geoip-refresh.{service,timer}` |

## 13. Showcase / person index

Public page listing a person's `public` sites (each linked at its own address; sites taken down, offline or with a passcode are left out) at
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
| Pages | `st/showcase.html` (builder chat, attachments, mic; the mic shows when the page data's `voice` is true, set when `TRANSCRIBE_URL` is, so loading the page sends nothing to `/v1/transcribe`) |
| Go | `h/generate.go` (prompt/instructions, attachments ≤18 MB), `h/generate_jobs.go`, `h/transcribe.go` (audio ≤25 MB, ticket signing) |
| Env | `LLM_PROVIDER` (default `grok`), `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL`, `VISION_PROVIDER`, `VISION_API_KEY`, `VISION_BASE_URL`, `VISION_MODEL`, `TRANSCRIBE_URL`, `TRANSCRIBE_TICKET_SECRET` |
| External | Grok via the local CLIProxy sidecar (`/opt/cliproxy`, `127.0.0.1:8102/v1`) only, no fallbacks; Moonshine speech-to-text (`/opt/moonshine`, :8100 HTTP, :8103 stream) |
| Limits | generate 20 burst +1/12 s per IP, 30 burst +1/10 s per user, status 240/4 s⁻¹; transcribe 60 burst +1/3 s per IP and per user |

## 15. Hackathon / event instances (simple-hack.app)

An organiser runs a private instance for an event (their own cloud box, same binary): first-boot
setup page, participant accounts from admin, event hostnames under `simple-hack.app`, entries
list, export. **Status:** setup live; event hostname claims live (`EVENT_DOMAINS=simple-hack.app`;
the Vercel token was re-checked 2026-09-30 and works). Since 2026-09-30 simple-hack.app itself is
the hosted platform below, and self-hosting is its second option.

| Surface | Details |
|---|---|
| Routes | `GET /hackathons` (simple-hack.app `/` is proxied here by nginx) · `GET /v1/events` · `POST /v1/events` (`{name, ip, domain}` → Vercel A records for `<name>.<domain>` and `sites.<name>.<domain>`; 21-day claim, max 5 per account) · `DELETE /v1/events/{name}` · setup mode only (no hostname configured): `/` (`setup.html`), `GET /v1/setup/state`, `POST /v1/setup/verify`, `POST /v1/setup/own-domain`, `GET /v1/setup/dns-check`, `POST /v1/setup/free-name` (proxies to `SETUP_PUBLIC_API` `/v1/events`), `POST /v1/setup/finish` · admin routes in §11 · export in §1 |
| Skill | `sk/run-hackathon/SKILL.md` (hosted path first: sign in, create, stages, join and judge links, teams and the entry, rubric, assignment, conflicts, dashboard, lock, publish and ties, winners or full ranking, CSV, archive cleanup; participant and judge; self-host path second — §What the organiser needs, §The flow 1–8, + references `dns.md`, `install.md`, `providers.md`, `teardown.md`) — served per-skill, excluded from `/skills.zip` and the plugin |
| Pages | `st/hackathons.html` (organiser prompt, swaps in `location.host`), `st/og-hack.png`, `st/setup.html`, `st/admin.html` |
| Go | `h/eventdomain.go` (claim/release/list, hourly sweep), `internal/eventdns/{vercel,inuse}.go`, `h/setup.go` (`InstanceConfigured`, setup restart), `h/chrome.go` (`HackHome` when host is `simple-hack.*`), `h/instancehost.go` (host rewriting for non-canonical instances; `SetInstanceNote`: llms.txt opens with a THIS SERVER block — one shared browser origin, previews need per-site addresses, how visitors sign in or that they cannot, whether email is sent, who to ask) |
| No sign-in, no email | With no email and no Google/GitHub sign-in, Submissions, Personal, Shared boards and making a list private are 409 `visitor_sign_in_unavailable`; with no email, Submissions emails default off and `notify` each/daily is 409 `email_unavailable`. Off simple-host.app, messages, emails, the export README and connector hints name `IDLE_REPLY_TO` or "whoever runs this server" (`auth.SetSupportContact`), and `invalid_api_key` says to ask for a new key where no email sign-in exists. |
| Content host (Caddy) | `deploy/compose/Caddyfile` serves files and hands the app what is not one, as the hosted nginx does: `domain-redirect` marker → `/internal/domain-redirect`, a missing site → `/internal/site-redirect` (a renamed site's old name redirects), `/<handle>/` → `/internal/showcase`, file misses and the root → `/internal/notfound`; `/internal/*` from outside is 404 on every host. Tested by `deploy/compose/Caddyfile_test.sh` (in `make check`, Docker) and the hackathon e2e. The installer re-run on a box set up in the browser reports its hostnames instead of failing. |
| DB | `event_domains`, `instance_config` |
| Env | `EVENT_DNS_TOKEN`, `EVENT_DNS_TEAM_ID`, `EVENT_DOMAINS`, `SETUP_PASSWORD`, `SETUP_PUBLIC_API`, `PERSON_HOSTS` (off on event instances), `MAX_ARCHIVE_MB`, `KEEP_VERSIONS`, `BIND_ADDR` |
| External | Vercel DNS API; Docker Compose + Caddy (`deploy/compose/`, `deploy/install/install.sh`; container logs capped at 3 × 10 MB per service, Caddy access log rolled daily and rolls deleted after 28 days, so raw IPs ≤30 days); live nginx `/etc/nginx/sites-enabled/simple-hack.app`; `scripts/e2e-hackathon.sh`, `scripts/check-fresh-install.sh` |
| Limits | event claims 10 burst, 1/min; setup 5 burst, 1/min |

### Hosted events: simple-hack.app (`EVENTS=hosted`)

A second copy of the binary on the same box (`simple-hack.service`, :8091, database `simplehack`,
`DATA_DIR=/srv/simple-hack/sites`, `/etc/simple-hack.env`) runs as the hosted hackathon platform.
Anyone who signs in creates an event (no approval); the event's public page is
`<event>.simple-hack.app`. Design: `docs/designs/simple-hack-platform.md`. **Status:** M0 and M1
(events, stages, event page, join and judge links, code of conduct, teams, admin Events tab,
product page) live 2026-09-30; M2 (team sites at `<team>.<event>.simple-hack.app`, member keys,
the connector bound to a team, entries, the submission deadline with per-team extensions,
organiser take-down of a team site, the event-page gallery) live 2026-09-30; M3 judging (rubric,
assignment, conflicts of interest, scoring, lock/publish, results, CSV exports) live 2026-10-01;
M4 archive cleanup (a new event starts with Idea, Execution, Design and Demo; after archive, team
sites stay `EVENT_SITES_KEEP_DAYS`, one warning goes out `EVENT_REMOVAL_WARN_DAYS` before they are
removed, and the event page and results stay) live 2026-10-01. The signed-in Account page (`/account`, from the header) sets the name
shown to teams, changes the sign-in email, links or unlinks Google, turns sign-in alerts on or off,
signs out everywhere and deletes the account, through the existing account routes.

For standalone installations using Caddy on-demand HTTPS, the certificate check
accepts the platform apex, real event pages and published team sites. It refuses
invented event/team names, deeper names and deleted projects; archived event pages
and retained projects can still renew their certificates.

**Standalone full-platform package (release candidate).** `deploy/hack/standalone/`
contains a Docker Compose install/upgrade path with a private, persistent environment
file, PostgreSQL and site/certificate volumes, migration step and Caddy ingress.
`docs/platforms/simple-hack-standalone.md` covers installation and verification;
the DigitalOcean and Coolify guides describe their separate paths. The dedicated
`hack-v0.8.0` workflow builds the base and Simple Hack images from one checkout
and packages the installers, schema and guides as ZIP and tar.gz downloads. It
does not change the existing small-box release pins. The installer passed a
disposable live DigitalOcean test; the Packer snapshot and live Coolify deployment
have not been verified.

**Organiser connector (M5).** At `https://simple-hack.app/mcp`, consent offers
Manage my events or a team site. Event management works before the person's first
event and offers ten `hack_*` tools: list, name check, create, get (including the
organiser's invite links), update, stage, get/set rubric, scores/results CSV.
Team connections retain the site tools and cannot manage events; event connections
cannot publish personal sites. Ordinary Simple Host connections are unchanged.
Grants explicitly carry `events` or `team:<id>`; an old teamless grant must reconnect.
The hosted-first `run-hackathon` skill covers both connector and direct API agents.

**Judging (M3).** The organiser builds a rubric before judging opens (`PUT .../rubric`, 1-10
criteria, each with a name, an optional description, a weight and a points scale; weights across
the rubric must sum to 100; refused once judging is locked). Judges are spread either openly
(every judge may score every team) or automatically (`PATCH .../judging/settings`, then
`POST .../assignments/generate`: a deterministic, conflict-aware, as-even-as-possible spread that
reports any team left short of its target). Either side can flag a conflict of interest
(`POST/GET/DELETE .../conflicts`; a judge declares their own, an organiser can declare or remove
one for any judge), which drops that pairing from assignment and from totals even if the judge had
already scored. A judge's queue (`GET .../judge/queue`) lists their eligible, non-conflicted teams
ordered so the least-covered one comes first, and the scoring screen
(`GET/PUT .../judge/scores/{team}`) saves one or several criteria at a time — a retried save of the
same value is a no-op, never a double count — plus one comment per (judge, team) pair. Each judge's
score for a team is Σ(weight/100 × points/max_points) × 100; a team's total is the mean of that
across every judge who scored it, excluding any conflicted judge. The phone scoring screen
shows each criterion’s weight and points range and opens the frozen project using its team
slug, independently of the team’s display name. The organiser watches coverage
and progress on `GET .../judging/dashboard`, locks scoring when judging is done
(`POST .../judging/lock`; every further score or rubric write is refused with 409 `scores_locked`
until `POST .../judging/unlock`, which requires and logs a reason), then publishes
(`POST .../results/publish`): the computed ranking is snapshotted, ties are flagged, and the
organiser may resolve a tie by giving its teams distinct ranks (`rank_overrides`); publishing again
later recomputes from the current scores. `PATCH .../results` switches the public view between
winners-only (the default: just the rank-1 team(s)) and a full ranking, without recomputing.
`GET .../results` is public and never reveals raw scores or comments; each team privately reads its
own total, rank and every judge's comment (never whose) at `GET .../my-results` after
publish. Before publish it returns only `{"published": false}`. The organiser can export the raw judge × criterion × team scores
or the computed results as CSV at any time (`GET .../export/scores.csv`,
`GET .../export/results.csv`; results.csv works even before publishing).

**Archive cleanup (M4).** A new event starts with a rubric of Idea, Execution, Design and Demo
(weight 25 each, scored 1–5, one line each), written once in the transaction that creates the
event. The organiser still replaces it with `PUT .../rubric`; nothing else writes a rubric onto
an event that already has one. After an event is archived, its team sites stay up for
`EVENT_SITES_KEEP_DAYS` (30). `EVENT_REMOVAL_WARN_DAYS` (14) before they are removed, the organiser
gets one email at the event's contact address: the event was archived, the date the sites will be
removed, that the event page and the results stay, and to write to support@simple-host.app for
more time. That warning is saved only after the email is accepted. At the end of the keep window
each team's site moves to Recently deleted; the event page, the results and the judging data stay.
`POST /v1/admin/hack/events/{slug}/keep-sites` `{"keep": bool}` (admin key only) leaves the sites
up or puts them back on that clock, and answers `{"keep_sites": bool}`. The sweep runs once a day
when `EVENTS=hosted`, and logs and does nothing if no mailer is configured.

**Event administration (P1, integrated branch; not yet deployed).** Organisers can issue one seven-day, single-use co-organiser invitation per event; a new one replaces the prior link. Acceptance requires sign-in and code-of-conduct consent, cannot promote an existing member, and stops working when the issuing organiser is removed. The event creator cannot be removed, and even an imported event without its creator keeps its final organiser. Organisers can rename a team without changing its slug, address or member keys. Event-scoped participant, team and entry CSVs, all-project archives, and storage usage are organiser-only; a participant can download their own team's project, including its saved data. CSV cells that spreadsheet software could execute are escaped. Removed organisers lose access.

**Registration, tracks and challenges (P1, branch work; not deployed).** Organisers can ask up to eight text questions at sign-up and require approval. Answers retain their question wording when settings change. Pending and rejected applicants can see their own status but cannot start or join teams, get team keys, submit entries or publish. An organiser sees the private application answers and approves or rejects each pending application once. Existing members and events remain approved by default. Up to twelve tracks each carry a challenge and prize; teams choose one track before their deadline, and the public event page lists tracks and prizes. Track IDs remain stable when a slug is kept, for later judge-panel assignments.

| Surface | Details |
|---|---|
| Routes (API) | `GET/POST /v1/hack/events` · `GET /v1/hack/names/{slug}` · `GET/PATCH/DELETE /v1/hack/events/{slug}` (delete only while nobody else joined) · `POST /v1/hack/events/{slug}/stage` · `POST /v1/hack/events/{slug}/codes/{kind}` (join, judge: new link) · `GET /v1/hack/events/{slug}/people` · `DELETE /v1/hack/events/{slug}/people/{user_id}` · `GET/POST /v1/hack/events/{slug}/teams` · `POST /v1/hack/events/{slug}/teams/join` · `POST /v1/hack/events/{slug}/teams/leave` · `DELETE /v1/hack/events/{slug}/teams/{team}` · `POST /v1/hack/events/{slug}/teams/{team}/members` · `DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}` · `GET/POST /v1/hack/join/{code}` · `GET/POST /v1/hack/judge/{code}` · M2: `GET/POST/DELETE /v1/hack/events/{slug}/key` (my team key, shown once) · `DELETE /v1/hack/events/{slug}/people/{user_id}/key` · `PUT /v1/hack/events/{slug}/teams/{team}/deadline` · `POST /v1/hack/events/{slug}/teams/{team}/takedown` · `POST /v1/hack/events/{slug}/teams/{team}/restore` · `GET/PUT /v1/hack/events/{slug}/entry` · `PUT/DELETE/GET /v1/hack/events/{slug}/entry/screenshot` · `GET /v1/hack/events/{slug}/entries` (organiser, judge; with each team's deadline-version link) · `GET /v1/hack/events/{slug}/teams/{team}/screenshot` · `GET /v1/hack/my-teams` · team sites: `PUT /v1/sites/<team>/files?create=1` and the other deploy routes with a team key · admin: `GET /v1/admin/hack/events`, `POST /v1/admin/hack/events/{slug}/takedown`, `POST /v1/admin/hack/events/{slug}/keep-sites`, `POST /v1/admin/hack/events/{slug}/restore`, `DELETE /v1/admin/hack/events/{slug}` · M3: `GET/PUT /v1/hack/events/{slug}/rubric` · `GET/PATCH /v1/hack/events/{slug}/judging/settings` · `POST /v1/hack/events/{slug}/assignments/generate` · `GET/POST /v1/hack/events/{slug}/conflicts` · `DELETE /v1/hack/events/{slug}/conflicts/{team_id}` · `GET /v1/hack/events/{slug}/judging/dashboard` · `POST /v1/hack/events/{slug}/judging/lock` · `POST /v1/hack/events/{slug}/judging/unlock` · `GET /v1/hack/events/{slug}/judge/queue` · `GET/PUT /v1/hack/events/{slug}/judge/scores/{team}` · `POST /v1/hack/events/{slug}/results/publish` · `GET/PATCH /v1/hack/events/{slug}/results` (GET is public) · `GET /v1/hack/events/{slug}/my-results` · `GET /v1/hack/events/{slug}/export/scores.csv` · `GET /v1/hack/events/{slug}/export/results.csv` · loopback only: `GET /internal/names/taken` (both instances) |
| Registration and track routes | `GET/PUT /v1/hack/events/{slug}/registration` · `GET /v1/hack/events/{slug}/applications` · `POST /v1/hack/events/{slug}/applications/{user_id}/decision` · `GET/PUT /v1/hack/events/{slug}/tracks` · `PUT /v1/hack/events/{slug}/team/track` |
| Administration routes | `POST/DELETE /v1/hack/events/{slug}/organiser-invite` · `DELETE /v1/hack/events/{slug}/organisers/{user_id}` · `GET/POST /v1/hack/organiser/{code}` · `PATCH /v1/hack/events/{slug}/teams/{team}` · `GET /v1/hack/events/{slug}/export/participants.csv` · `GET /v1/hack/events/{slug}/export/teams.csv` · `GET /v1/hack/events/{slug}/export/entries.csv` · `GET /v1/hack/events/{slug}/export/projects.tar.gz` · `GET /v1/hack/events/{slug}/team/export.tar.gz` · `GET /v1/hack/events/{slug}/usage` · page `/organiser/{code}` |
| Pages | apex: `/` (`st/hack-home.html`, Create event first, self-host second), `/signin`, `/account`, `/events`, `/events/new`, `/e/<event>`, `/e/<event>/manage` (Overview, Event page, People, Teams, Projects, **Judging** — rubric, assignment mode and generation, conflicts, coverage dashboard, lock/unlock, publish with tie resolution, the public winners/full-ranking switch, CSV exports — Settings), `/join/<code>`, `/judge/<code>` (`st/hack-app.html`, one client-routed page; a judge's event page shows their queue of teams to score, one-tap-per-criterion phone scoring with Save & next / Previous and an "I have a conflict" button; a participant's team page shows a Results card once published — rank and score, every judge's comment, never which judge); `/dashboard` → `/events`; `/admin` Events tab. `<event>.simple-hack.app/`: server-rendered event page (`st/hack-event.html`, plain text escaped, strict CSP, noindex while draft, 410 when taken down; a Winners or Results section once published; a Projects gallery of live team sites when the organiser opens it, screenshots at `/screenshots/<team>`); every other path (and `/v1/`) and every non-event name is the platform 404. `<team>.<event>.simple-hack.app`: the team's site, its own origin with visitor sign-in and saved data; offered and deployed only once `*.<event>.simple-hack.app` has its certificate. Connector consent (`/oauth/authorize`) asks which team's site the connection publishes to |
| Roles | organiser, participant, judge: one per person per event, checked on the server on every route; a non-member gets 404 `event_not_found`; the platform admin reads read-only (`admin_view`) and cannot join. Stages in the model: draft → open → building → closed → judging → results → archived; offered now: Draft, Open, Building, Submissions closed (the deadline becomes now; back to Building clears a passed deadline), Ended (final; needs a participant, else delete). Joining open/building; judges until results; team changes by participants open/building |
| Rules | code of conduct = default text plus the organiser's, accepted at join; join code 8, judge code 12, team code 8 characters, regenerable; team size cap set by the organiser; team names unique in an event; one team per participant; empty teams deleted; a team's code changes when the organiser takes someone off it; participants never see others' emails; accounts own no personal sites (403 `no_personal_sites`): a team's site belongs to the event's holding account and is deployed only with a member's team key (scope `team`: deploy routes plus the site's saved data as owner, analytics and `GET /v1/me`, on that one site; 403 `team_site_only` / `team_key_scope`; 401 `team_key_inactive` once the person is off the team) or a connector connection bound to the team; at the team's deadline (the event's `submission_deadline` or the team's own later one) deploys, rollbacks, entry edits and team-key changes stop (409 `submissions_closed`, checked inside the deploy transaction) and the live version is pinned as the deadline version (never pruned; judges open it by preview link); the organiser can take a team's site down and put it back (403 `team_site_taken_down`; 409 `platform_takedown` for the platform's own); a removed team's site goes to Recently deleted and its name is never reused; they get a random `u-<hex>` handle that cannot be changed (`handle_fixed`) and the Account page never shows it; sign-in email says Simple Hack |
| Names | event slug 3–39 characters, not reserved, not a handle, and not a self-host claim on simple-host.app: each instance asks the other over loopback (`EVENT_NAME_PEER`), failing closed (503 `name_check_unavailable`) |
| P1 event content | Organisers manage sponsor names, tiers, optional local logo images and HTTPS links; FAQ and schedule in the event time zone; and announcements with optional participant email. Event pages show the schedule with Now/Next, announcements, and a deadline countdown. Participants get one email receipt when their team first submits a complete entry. Members can read content and announcements after archive; writes stop. Routes: `GET/PUT /v1/hack/events/{slug}/content`, `GET/POST /v1/hack/events/{slug}/announcements`. |
| Go | `h/hack.go`, `h/hack_teams.go`, `h/hack_admin.go`, `h/hack_mode.go` (host dispatch, name peer, gates), `h/hack_eventpage.go`, `h/hack_home.go`, `h/hack_ui.go`, `h/hack_wire.go`, `internal/db/hack.go`; M2: `h/hack_sites.go` (team-site gates, cert readiness, take-down, sweep), `h/hack_keys.go` (keys, deadlines, take-down, my-teams), `h/hack_entries.go`, `internal/db/hack_sites.go` (team identity, write state, pinning), `internal/db/hack_entries.go`, `internal/db/hack_gallery.go`, `internal/auth/scope.go` (`teamRoutes`), `h/connector.go` (team-bound grants); M3: `h/hack_judging.go` (rubric, assignment, conflicts, dashboard, lock/unlock, scoring, publish, results, CSV exports), `internal/db/hack_judging.go`; M4: `h/hack_cleanup.go` (the daily archive warning and team-site removal), `internal/db/hack_cleanup.go`; P1 administration: `h/hack_administration.go`, `internal/db/hack_administration.go`; P1 content: `h/hack_content.go`, `internal/db/hack_content.go`; P1 registration: `h/hack_registration.go`, `internal/db/hack_registration.go` |
| DB | `events`, `event_members`, `event_teams`, `event_create_log` (`db/migrations/hack1-events.sql`); M2 `event_entries`, `event_team_keys`, `events.entry_required`/`gallery_open`, `event_teams.deadline_override`/`pinned_version`/`pinned_at`/`site_taken_down_*` (`db/migrations/hack2-team-sites.sql`); M3 `rubric_criteria`, `event_assignments`, `event_conflicts`, `event_scores`, `event_results`, `events.judge_assignment_mode`/`judges_per_team`/`judging_locked_at`/`judging_lock_reason` (`db/migrations/hack3-judging.sql`); M4 uses `events.closed_at`, `removal_warned_at`, `sites_removed_at` and `keep_sites` (already in `hack1-events.sql`); P1 `event_organiser_invites` (`db/migrations/hack4-administration.sql`); P1 content/announcement/delivery/receipt tables (`db/migrations/hack5-content.sql`); `events.signup_questions`/`approval_required`, `event_members.signup_answers`/`approval_status`, `event_tracks`, `event_teams.track_id` (`db/migrations/hack6-registration.sql`); checked at start only in hosted mode |
| Env | `EVENTS`, `EVENT_NAME_PEER`, `EVENT_CREATE_PER_DAY` (3), `EVENT_MAX_ACTIVE_PER_ORGANISER` (2), `EVENT_TEAM_SIZE_DEFAULT` (4), `EVENT_SITES_KEEP_DAYS` (30), `EVENT_REMOVAL_WARN_DAYS` (14), `HACK_INSTANCE_BUDGET_GB` (10); `MAX_ARCHIVE_MB=25`, `KEEP_VERSIONS=2`, `WRITE_AUTH_MODE=on`, `SAVED_DATA_DEFAULT_KIND=declare_first` on that instance |
| Deploy | `deploy/hack/` (unit, env example, `setup-instance.sh`, logrotate), `deploy/prod/nginx-site-base-domain.sh` with `APEX_MODE=app` (vhost `simple-hack`), `deploy/site-certs/simple-host-site-certs-hack.*` (per-event `*.<event>.simple-hack.app` certificates, 30/week, 8/day); DNS `*.simple-hack.app` A record to this box; apex certificate `simple-hack.app` + `*.simple-hack.app` |
| Limits | `RATE_LIMIT_EVENT_CODES_IP` (120, 1/s), `RATE_LIMIT_EVENT_CODES_USER` (20, 1 per 3 s) for join, judge and team codes; `RATE_LIMIT_EVENT_NAMES_USER` (60, 1/s) for address checks |

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
| `GET /setup` | `st/setup-helper.html` + `st/setup/setup.js` (the setup helper, below); `GET /setup/{$}` redirects to it |
| `GET /costs` | `st/costs.html` + `st/costs/calc.js`, `st/costs/costs.js`, `st/costs/prices.json` (the cost calculator, below) |
| `GET /diagrams/{name}.svg` | `st/diagrams/` (file server): one architecture diagram per place Simple Host runs (`fly`, `render`, `upcloud`, `enterprise-aws`), shown at the top of each GitHub guide (`docs/platforms/*.md`, the enterprise repo's `docs/cloud/aws.md`); `diagrams_test.go` checks each is served as `image/svg+xml` with a `<title>` and `<desc>` |

Go: `h/ui.go`, `h/chrome.go`. Assets: `st/og.png`, `st/favicon.svg`, `st/site.css`.

**Setup helper (`/setup`).** Choose **Small box** (one server with Docker Compose) or
**Enterprise** (an existing Kubernetes cluster). Enterprise offers **Helm** or **Kubernetes
YAML**, rendered from the same pinned chart (`ENT_CHART_VERSION`, `ENT_CHART` in
`st/setup/setup.js`). It connects existing Postgres with verified TLS and a supplied CA,
or installs a single persisted Postgres in the cluster for evaluation; volume size and
StorageClass are configurable. It uses an existing S3-compatible bucket (generic provider
and blank endpoint/region by default), company OIDC, ingress class, namespace/context and
existing cert-manager ClusterIssuer or manually supplied certificates. The helper does not
create clusters or cloud infrastructure; old `?cloud=aws` links follow this same flow.

Enterprise output is `values.yaml`, a blank `secrets.env` template, and commands to create
the namespace/Secret and install the chart or render/apply `simple-host.yaml`. Both paths
reference a persistent `secrets.existingSecret`; signing/envelope keys are generated once
and preserved on upgrade. External `DB_PASSWORD` is the existing owning-role password;
`DB_APP_PASSWORD` is distinct. An external database's CA is supplied with
`--set-file postgres.external.caCert=db-ca.crt`. Chart-owned settings map to typed Helm
values, other nonsecret application settings to `extraConfig`, and secret settings to the
existing Secret. The chart's own values also expose resources, replicas and scheduling.
The Enterprise files step offers one browser-generated `simple-host-enterprise-setup.zip`
containing the exact displayed `values.yaml`, blank `secrets.env`, Helm or YAML `install.sh`,
and `simple-host-setup.md` agent instructions. No server receives the bundle. All four
previews start collapsed and can be expanded, copied, or downloaded separately.
`scripts/check-docs-sync.sh` verifies the chart version against `$SH_ENTERPRISE_REPO` during
development or its published chart tag. Browser checks cover both outputs/database modes,
Advanced, the old cloud URL, empty navigation, and small-box regressions.

On both products nothing is required to reach the files: missing answers remain defaults
or clearly marked blanks, named before the commands. Basic covers the essentials and
Advanced offers the other settings. A small box with no domain gets an install command
without `--host`, so first-boot setup asks for it. The checks catch only an obvious typo or what would break a file or command (spaces, quotes, backslashes, shell characters): hostnames may be one label or a Kubernetes service name; the Postgres host also an IPv4/IPv6 address, `host:port` (the port becomes `DB_PORT`) or a `postgres://` URL; emails any `name@domain`, internal ones too; an issuer or bucket endpoint typed without `https://` gets it (scheme and host in any case); Entra ID's multi-tenant issuer is refused as the server refuses it. Every field refuses control and invisible (Unicode format, `\p{Cf}`) characters with a plain reason; the Postgres host refuses a user or password (hostname only) and an unbracketed IPv6 `host:port` (hint `[fd00::1]:5432`); a small box's sites hostname without a domain is refused with why. Otherwise: **Basic** (small box: domain, sites hostname, certificate email,
sign-in by emailed code and/or Google, sender, and **Where it runs**: UpCloud (recommended, the default) or a server
you already have; Enterprise: address, admins, OIDC issuer/client/domains, owner
certificate issuer, SMTP, bucket provider/endpoint/region/name/credentials, Postgres, and the ingress controller's pod
range (`TRUSTED_PROXY_CIDRS`, optional, empty keeps every private range); with UpCloud as the bucket provider the
region is asked (the Object Storage region, such as europe-2, never preset) and the Postgres fields say to use the
managed database's `public-…` hostname, and the port is filled in as 11569 while it is still 5432, back to 5432 when
another provider is picked; picking another identity or bucket provider fills its template issuer, endpoint or region
only where the field is empty or still another provider's template, never over a typed value) or **Advanced** (the basics, then
every other setting area by area, default preselected, a one-line explanation, range checks as you type, Skip
restores the default, progress by step and area). **UpCloud** (small box, "Where it runs"): one line on why ("The
smallest UpCloud server (1 CPU, 1 GB, about $4/month) runs Simple Host comfortably; we test on it."), a **Create your
UpCloud account — $25 in credits** button to the referral link `https://signup.upcloud.com/?promo=JF2WCV` (new tab,
`rel="noopener"`) with "Referral link. New accounts through this link get $25 of UpCloud credit; their
terms apply." under it, and the steps (create the account → create an API token (Account → API tokens; recommended) or an
API user, a sub-account with API access, in the UpCloud control panel → answer the questions → on the files step, set the
token or API user in your terminal and give your agent the prompt); the page has no field for UpCloud credentials and never asks for them. Output: small box → the one-line
`install.sh` command, fetched from the release the installer pins by that release's commit (`INSTALLER_RELEASE`,
`INSTALLER_COMMIT` in setup.js) into a `mktemp` file and run only if its sha256 matches `INSTALLER_SHA256` (tag, commit
and hash agree by test, which also fails once `install.sh`'s `VERSION` is tagged but not pinned), not `main`; every value
in a generated command is shell-quoted when it needs it, and emails must be plain addresses (its flags
for host, sites host, email, `--max-site-mb`, `--keep-versions`) and the `/opt/simple-host/.env` lines (only changed
and needed values; Copy, Download) with where to paste them and the restart line; Enterprise → the Helm values, persistent Secret template and install/render commands described above; both with a
"What you chose" summary, and **Set it up with your AI agent**: one block to copy (or download as `simple-host-setup.md`) into
the agent the person uses in a terminal, with what the machine needs, every step with their files in it (small box: DNS,
the install command, the `.env` lines, `docker compose up -d`; Enterprise: follow `docs/install-kubernetes.md`, fill the values and secrets, select the existing context, then install the chart or render and apply its YAML), checks
(`/healthz` → 200, `docker compose ps`, the sites host's certificate, the release via `docker compose exec -T app simple-host
version`; `/readyz`, an owner host, an admin sign-in, then INSTALL.md's HUMAN STEP D and `make smoke`, with
`CURL_CA_BUNDLE` naming the company CA when owner certificates come from an internal CA) and, where
the assistant is on, "paste the error at `<origin>/setup?product=<p>#help`"; secrets stay blanks for the agent to ask for.
On UpCloud the files step leads with this block, headed **Set it up on UpCloud with your AI agent**: two one-line
commands for the person's own terminal, one or the other: an API token (`read -rs UPCLOUD_TOKEN` under a trap that turns
echo back on after Ctrl-C, `export UPCLOUD_TOKEN`) or an API user (`printf`/`read`, the password with `read -rs` under the
same trap, `export UPCLOUD_USERNAME UPCLOUD_PASSWORD`; bash and zsh), a note to `unset UPCLOUD_TOKEN UPCLOUD_USERNAME
UPCLOUD_PASSWORD` (or close the terminal) when the server is up and to keep them limited (a token with an expiry, an API
user with only server permissions, and where possible the person's own IP), and the prompt, which accepts either (its
check: `UPCLOUD_TOKEN`, or both user variables), tells the agent to use them only from
the environment (never ask for them in chat, print them or write them anywhere), to use `upctl` or the UpCloud API with
curl reading an `Authorization: Bearer` (token) or `Basic` (user) header from standard input,
create an SSH key `~/.ssh/simple-host` if missing, ask for the zone, list plans and take the smallest with 1 CPU and 1 GB
(`STARTER-1xCPU-1GB` today) and tell the person its monthly price first (from `GET /1.3/price`, `server_plan_<plan>` in
cents per hour ×730; no upctl command shows prices), take the plain Ubuntu Server 24.04 LTS template from the API (not
the CUDA one; `upctl storage list --template` can be empty), the plan's disk at tier `standard`, root login with the key,
a public IPv4, and retry SSH for up to two minutes after `started`; DNS A records
for the domain and `*.<domain>` (plus the sites hostname when it is not under the domain), checked with `dig`; run the
pinned installer over SSH (the admin key it prints goes to the person; the server keeps it in `/opt/simple-host/.env`
and re-running prints it again); the `.env` lines; `/healthz`, `docker compose ps` and the sites host's certificate; then
report the admin page `https://<domain>/admin` (paste the admin key there) and the server's UUID, address, plan and zone, and remind the person to unset
the credentials. The by-hand steps follow
("Or do it by hand"), with the DNS records worded exactly as in the prompt. Where it runs is one entry per target in `TARGETS` (setup.js: small box `upcloud`, `server`;
Enterprise `kubernetes`), so another platform is one more entry and one more choice. `/setup?product=enterprise` or `?product=small-box` preselects the first choice (the
links on the enterprise and hosted pages). Runs in the browser (its only requests are its own files, the optional
check and the assistant below; `credentials: 'omit'`), never asks for a secret's value, in the navy document style, light or dark with the site-wide theme. **Check my choices** (optional, where
the server has its model backend): just before the files, when the visitor changed any number, duration, switch,
choice or limit, the helper sends those names and values (never free text such as hostnames or emails, never a secret)
and the product to `POST /v1/setup/check`, showing "Checking your choices" with a **Skip the check** link; each
finding shows as Warning or Note with its settings, a sentence and any suggested values, and **Apply** (only where the
helper writes that setting and the value passes its own range check) or **Ignore**; **Show my files** then writes the
files from the form as always, with a one-line note ("Checked: 1 suggestion applied.", "Checked: nothing to change.").
No backend (404), an error, 30 s without an answer, or Skip shows the files with "Check skipped."; nothing changed
means no request; one check at a time (a new one, or leaving the files step, aborts the one running); the same choices are not checked twice. Its lists are `st/setup/small-box-settings.json` (a copy of
`docs/advanced/settings.json`, which `simple-host settings --json` prints from `internal/config/settings.go`) and
`st/setup/enterprise-settings.json` (a copy of the enterprise repo's), kept equal by `scripts/sync-settings.sh` and
checked by `scripts/settings_docs.py --check` (in `check-docs-sync.sh`) and `h/setuphelper_test.go`; only settings a
Compose box passes through and the installer keeps are offered for a small box. Linked from `install.html` (FAQ),
"Try it in your organization → Set up" on `enterprise.html`, `enterprise-brief.html` and
`enterprise-architecture.html` (`?product=enterprise`), "Run your own → Set up" on `features.html` and
`architecture.html` (`?product=small-box`), both READMEs, both Ask assistants and `docs/advanced/`. **Status: built.**

**Cost calculator (`/costs`).** What Simple Host Enterprise costs to run, for the companies it is sold to. Inputs: people
who sign in, sites, a traffic level (Light 5 pages a person a working day of 0.5 MB, Typical 20 of 1 MB, Heavy 50 of 2 MB,
Very heavy 150 of 3 MB, 21 working days; or Custom page views and MB per view), average site size, saved data per site,
uploaded files; switches **We already have a Kubernetes cluster** (on: no control plane, only the pods' share of a node),
**We already have an ingress and load balancer** (on: only the traffic it adds), **Traffic stays inside the company
network** (on: billed at the provider's private-link rate, Direct Connect / ExpressRoute / Cloud Interconnect; off: internet
rates with the free tiers, which a site-to-site VPN also pays) and **High availability** (a standby database; on a new
cluster at least two nodes, three on Hetzner, and a control plane with an SLA); the first three default on. Output, per
provider side by side: the monthly total, per person, a stacked bar by Cluster and nodes / Database / Storage / Traffic /
Load balancer / Other, and the other view's total (new cluster or yours); AWS, Azure and Google Cloud first (AWS selected),
UpCloud and Hetzner (k3s you run, Postgres on its own server) under "Smaller clouds". The selected provider shows its line
items with unit prices (what to enter in its own calculator, with a link: calculator.aws, the Azure and Google Cloud
calculators, calc.upcloud.com, Hetzner's cloud page), UpCloud our measured trial cost, and **How traffic changes it** (each
level's data out, total, per person and traffic's share). At the top, **Example: 2,000 people** (6,000 sites) per person
and total at Typical and Heavy on AWS, Azure and Google Cloud, with **Use these numbers**. **How we size it** lists every
rule (replicas 2 plus one per 5 million views; memory and CPU from the pods' requests, 1.2 GiB upload room, cluster
overhead; cheapest node mix; database plan by people; storage, bucket and access-log sizes) with its value for the inputs;
**Prices and sources** gives the checked dates ("list prices as of …, excluding tax; your bill may differ") and every
source URL. State is in the address (`?people=&sites=&traffic=&views=&viewMB=&cluster=new&ingress=new&network=internet&ha=1&provider=`).
Every price and constant is in `st/costs/prices.json` (each price: `usd`, `per`, `source`, `checked`); `st/costs/calc.js`
(UMD) does the arithmetic for the page, the setup helper and the tests; `headline()` gives the ranges `/enterprise`,
`/enterprise/brief` and the enterprise Ask pack quote (about 2,000 people on your cluster, internal: about $70–100 a month,
3.5–5 cents a person; about 40 people: $25–45), and a test fails when their text and the prices disagree. Runs in the
browser (its only request is `prices.json`, `credentials: 'omit'`), no Ask widget, light or dark with the site-wide theme
(§10). The setup helper's Enterprise basics show "Running cost on <cloud>: about $… a month" for AWS, Google Cloud or
UpCloud buckets (the calculator's defaults) with a link, else a plain link. Linked from `/enterprise` (hero "What it
costs" → a **What it costs** section with **Estimate your own cost**), `/enterprise/brief`, the setup helper, README and
both Ask assistants (`/costs` is on both link lists). `scripts/check-prices-age.sh` fails when any price was checked more
than 45 days ago (`MAX_AGE_DAYS`); `make check` runs it as a warning. Tests: `h/costs_test.go` (every price has an https
source and a date; calc.js under node against `h/testdata/costs-fixture.json` worked by hand; directions on the real
prices; the page, its files and links; the quoted ranges); `scripts/e2e-costs.js` (390 and 1280 px, no horizontal scroll,
switches, address state). **Status: built.**

**Setup assistant** (where the server has its model backend and `SETUP_ASSIST_DAILY_MAX` > 0; otherwise the page loads
no assistant at all). An **Assistant** button on `/setup` opens a panel with the Ask panel's look (a bottom sheet under
560 px, a floating panel, and from 1180 px a panel beside the form that the page makes room for, so applied changes show;
in the page's theme). It knows the product, the step and area, and the choices. It **answers** questions about settings and setup
(short by default), **fills in the form** from a plain request ("Set this up for a 200-person company with Microsoft
sign-in and stricter security"), and **cleans up** choices ("Clean up my choices": odd values explained, conflicts
flagged, resets to the default offered). The answer streams as text; proposed changes then show as items, "Set
SESSION_TTL to 4h — why" with the current value and area, each with **Apply** / **Ignore**, and **Apply all** when there
are several; basic answers too ("Identity provider: Microsoft Entra ID"). Nothing is applied by itself: Apply goes
through the form's own validation and state (`window.shSetup` in setup.js), the page is drawn again with the field
highlighted, and on the files step the files follow at once ("Updated with the assistant.", no second check); a value
already in force shows "Already set". A basic answer applied past the Basics step runs the Basics checks again, and if they
fail (Google with no company domains, SMTP with no From address, a provider's template address) the page goes back to
Basics with the errors shown and offers no files until they pass. Empty panel: two example requests per product. **Troubleshooting**: "Paste an
error" (or `/setup?product=…#help`, or pasting multi-line output into the field) opens a box for output from the
installer, the person's AI agent, kubectl, docker or Caddy logs; "Review what will be sent" shows it redacted (the
shapes the rules recognise: keys, tokens, passwords, secret assignments and headers, credentials on curl/mysql command
lines, private keys, JWTs, email addresses, long secret-looking strings, after terminal colour codes are stripped;
hostnames and addresses stay; the hint says to check before sending) with how many things were hidden, only the last 8 KB of a long paste, and nothing goes until **Send for
help**; the answer gives the likely cause, a command to confirm, and the fix, with a setting as an Apply item. An
address in an answer never survives with its host, but a command keeps its shape: the server turns it into the same
scheme and path on a placeholder host (`curl -fsS https://<your-host>/healthz`; query and fragment dropped), and the
model is told to write addresses that way. A proposed value that looks like a template (`YOUR-…`, `REPLACE_WITH…`,
`example.com`, `<…>`) is dropped, and the rules forbid basic answers or changes the person did not ask for (a provider
named in passing, "match Okta's session policy", does not change the identity provider). The knowledge covers the
UpCloud API token, the price call, SSH coming up after `started`, signing in at `/admin`, the release command, the
internal-CA `make smoke` failure (curl exit 60/35, "not ready": the checking machine must trust the CA, `CURL_CA_BUNDLE`)
and INSTALL.md's advice to match the IdP's session policy with `SESSION_TTL`/`SESSION_IDLE` (said as the recommended
setup with its leaver caveat; longer than the defaults they are typed in Advanced, never proposed). A typed
message is redacted too. The conversation (last 4 turns per product) lives in the open page only.

| Surface | Details |
|---|---|
| Assist route | `POST /v1/setup/assist` `{product, step: choose\|basics\|advanced\|files, mode?, area?, choices?, basics?, message, pasted?, history?}` → `{answer, changes: [{setting, value, why}], basics: {key: value}}`, or with `Accept: text/event-stream` `data: {"t"}` pieces (stopped before the changes marker, even one arriving in pieces) then `data: {"done":true,"answer","changes","basics"}`. Request: unknown fields 400 `invalid_body`; `choices` exactly as the check takes settings (0–80; `unknown_setting`, `secret_not_accepted`, `setting_not_checkable`, `invalid_value`); `basics` only the answers picked from lists (small box `codes`, `google`; Enterprise `idp`, `certs`, `smtp`, `bucket`, `creds`; else `unknown_basic`/`invalid_value`); `message` 1–500 characters; `pasted` ≤ 8 KB (`paste_too_long`); message, pasted output and earlier questions redacted again on the server (`h/setupredact.go`, the same rules as the page's). Response: every change checked — dropped if the helper does not write that setting (a basic question's, a secret, free text, or on a small box one Compose does not pass through), the value is outside its range, equals the current value, or loosens a security-sensitive setting past both its default and the current value (the check's rules, `strict_order` and `zero_is_never` included; Enterprise's `DB_INCLUSTER_EVALUATION` counts as security-sensitive, `false` first, so it is never proposed on); canonical values; at most 12; `why` ≤ 200 characters with no links; basic answers kept only when `step` is `choose` or `basics` (past them the answer says to go back to Basics); answer plain with no links. Same-origin only, shares Ask's per-IP/per-network buckets, its own in-flight cap `SETUP_ASSIST_MAX_IN_FLIGHT` (1), per-network count per UTC day in memory `SETUP_ASSIST_PER_NETWORK_DAILY` (40), daily count `SETUP_ASSIST_DAILY_MAX` (300; 0 turns it off and hides the panel) in table `setup_assist_daily` (migration `v073-setup-assist-daily.sql`); left out of `h/cors.go` and `h/apimetrics.go` |
| Assist Go | `h/setupassist.go` (prompt = rules, the product's basic questions (proposable ones with their values, typed ones never proposed), the check's FACTS, the product guide `h/askdata/setup-<product>.txt` through the Ask filter, troubleshooting `h/askdata/setup-troubleshoot-<product>.txt` through a lighter filter that keeps install commands, and the settings the helper writes area by area with type, default, range, security flag and description; ASK_MODEL and ASK_REASONING_EFFORT, up to 1200 tokens; one request, never retried; log line: product, step, number of choices, whether output was pasted, the day's count), `h/setupredact.go`, `h/chrome.go` (`<!--sh:setup-assist-->` → the script tag when on) |
| Assist page | `st/setup/assist.js` (panel, streaming, items, redaction and review, `#help`), `st/setup/setup.js` (`window.shSetup`: context, describe, apply, describeBasic, applyBasic, refresh; `<setupBasics>` block), styles in `st/setup-helper.html` |
| UpCloud | `st/setup/setup.js` (`UPCLOUD_SIGNUP`, `upcloudOffer`, `renderWhere`, `TARGETS`, `UPCLOUD_TOKEN_CREDS`, `UPCLOUD_CREDS`, `dnsText`, `handoff`; Enterprise `pickIdp`/`pickBucket`), styles `.cta`/`.fine` in `st/setup-helper.html`; `h/setuphelper_test.go` (`TestSetupHelperInstallerRelease`: the command fetches install.sh by the pinned release's commit and checks its sha256 before running it; tag, commit and hash agree, and a tagged `install.sh` release must be the one pinned; `TestSetupHelperUpCloudReferral`: the exact referral URL); the assistant's knowledge (`h/askdata/setup-small-box.txt`, UpCloud errors in `setup-troubleshoot-small-box.txt`); `docs/advanced/README.md` and the run-hackathon skill's `install.md`/`providers.md` carry the recommendation and the referral note; pasted `curl -u`/`--user` passwords are redacted |
| Assist tests | `h/setupassist_test.go` (validation, dropped changes, no looser changes, reply shapes, prompt contents and no leaks, streaming, caps and own slots, no CORS or metrics, redaction cases in Go and the page's JS against `h/testdata/setup-redact-cases.json`, knowledge filters, basics lists equal the page's, the script only when on); `scripts/e2e-setup-assist.js` with `scripts/e2e-setup-assist-sidecar.py` drives the page in Chromium (ask → items → apply → files reflect it, clean-up, paste → review → send, both products, 390 and 1280; the UpCloud block: the button's text, exact URL, new tab and noopener, the referral note, no credential field on the page; the prompt's pinned installer URL, chosen settings, UpCloud steps and credential rules; a server of your own gets no UpCloud steps) |

| Surface | Details |
|---|---|
| Check route | `POST /v1/setup/check` `{product: small-box\|enterprise, settings: {NAME: "value"}}` → `{findings: [{severity: warn\|info, settings: [NAME…], message, suggest?: {NAME: "value"}}]}` (≤ 8). Request: exactly those two fields (400 `invalid_body`), 1–80 settings of that product's list (`unknown_setting`); a secret is 400 `secret_not_accepted` and free text 400 `setting_not_checkable`, nothing sent upstream; every value must pass the list's type and range (`invalid_value`). Response: a finding naming an unknown, secret or free-text setting, with another severity or no message, or suggesting a value outside the list's range is dropped whole; a suggestion that would make a security-sensitive setting looser than both its default and the value sent (longer lifetime, bigger burst or shorter interval, an `*_INSECURE_ALLOWED`/`*_PLAINTEXT_ALLOWED` switch on, a choice later in the list's `strict_order`) is removed and the message kept; values are printable ASCII only, suggested rates and numbers come back canonical (`10,30s`), messages go through the Ask link filter with no links allowed. Same-origin only like `/v1/ask` (no CORS grant, JSON, `Origin` = the apex; left out of `h/cors.go` and `h/apimetrics.go`); shares Ask's per-IP/per-network rate limits; its own in-flight cap `SETUP_CHECK_MAX_IN_FLIGHT` (1; Ask keeps its own slots), a per-network (/24, /48) count per UTC day in memory `SETUP_CHECK_PER_NETWORK_DAILY` (20), and its own daily count `SETUP_CHECK_DAILY_MAX` (200; 0 turns it off) in table `setup_check_daily` (migration `v061-setup-check-daily.sql`); registered with `/v1/ask` (model backend set, `ASK_ENABLED` on) |
| Check Go | `h/setupcheck.go` (reads the helper's two settings files; prompt = rules, known interactions per product — upload size × concurrency against memory, ingress/proxy body size, session vs idle and key lifetimes, sign-in rate floors, retention and undo promises, insecure switches — and the list with type, default, range, security flag and description, secrets left out; ASK_MODEL and ASK_REASONING_EFFORT, up to 1500 tokens; one request, never retried; log line: product, number of settings, the day's count, never the settings), `h/ask.go` (`call`, the shared streamed request) |
| Check tests | `h/setupcheck_test.go` (validation, origin, caps, own slots and per-network cap, schema, dropped findings, no looser security suggestions, ASCII and canonical values, no links, secrets refused, no CORS or metrics, the page's `setupKind` in setup.js run under node against both lists agrees with the server's `kind`); `scripts/e2e-setup-check.js` with `scripts/e2e-setup-check-sidecar.py` drives the page in Chromium against a fake backend |

**Ask assistants.** Two assistants, each defined once and shown on all its pages: **Simple Host** (features,
architecture) and **Simple Host Enterprise** (`/enterprise`, `/enterprise/brief`, `/enterprise/architecture`). A floating
"Ask" button opens a small panel, "Ask about <assistant>", where a reader types a question and gets a short answer
written by the model from all of that assistant's pages (preferring the one the reader is on, linking to the others),
shown as it is written; follow-ups keep the conversation, which follows the reader across the assistant's pages in
the same tab. **Status: live when the model backend is configured** (`LLM_API_KEY`; off with
`ASK_ENABLED=off`); without it the box is not rendered and the route is not registered.

| Surface | Details |
|---|---|
| Route | `POST /v1/ask` `{assistant, page?, question, history?}` → `{answer}` (`assistant` ∈ `simple-host`, `enterprise`, else 400 `unknown_assistant`; `page`, the page the reader is on, must be one of that assistant's, else 400 `unknown_page`; the older `{question, page, history?}` without `assistant` still works for one release and picks the page's assistant), or with `Accept: text/event-stream` a stream of `data: {"t":…}` pieces then `data: {"done":true,"answer":…}` (the cleaned answer; `{"error","code"}` if it stops part-way; headers go out with the first piece, so an earlier failure is a plain 502; `X-Accel-Buffering: no`; flushed per piece; a reader who leaves cancels the model request). `history` = earlier turns of the conversation, last 4 used, each answer cut to 1500 characters, nothing kept server-side; page keys: `features`, `architecture` (Simple Host), `enterprise`, `enterprise-brief`, `enterprise-architecture` (Enterprise); no sign-in, no cookies (`credentials: 'omit'`). Same-origin only: no CORS grant (left out of `h/cors.go`), `Content-Type: application/json` required (415), `Origin` must be exactly the instance's apex from `PUBLIC_BASE_URL` (403 `forbidden_origin`, also when missing) |
| Pages | one marker naming the assistant: `<!--sh:ask simple-host-->` in `st/features.html` and `st/architecture.html`, `<!--sh:ask enterprise-->` in `st/enterprise.html`, `st/enterprise-brief.html` and `st/enterprise-architecture.html` (a test fails if a page of an assistant lacks it or any other page has one) → the same `st/partials/ask.html` (`data-assistant`, `data-page`, title "Ask about Simple Host" / "Ask about Simple Host Enterprise") and `st/ask.js` for every page: a floating button bottom-right (safe-area aware, the page keeps room below its last line) that opens a panel (bottom sheet under 560 px, 380 px panel on desktop) with the conversation, the question field, a close button (Escape closes, focus goes to the field and back to the button) and the small "Answers are written by AI from the <assistant> pages" note; `st/ask.js` streams the answer as plain text, then shows the cleaned answer with its links; the last 4 turns are kept in the tab's `sessionStorage` under `sh-ask:<assistant>` (every access in try/catch; without storage the conversation lasts for the open page), shown again on the assistant's other pages, sent with a follow-up, and gone when the tab closes; styles in `st/site.css` (`.sh-ask`, page tokens, navy for the enterprise assistant via `data-tone="navy"`; light only) |
| Go | `h/ask.go` (`askAssistants`: each assistant's name, pages, summary file, allowed links and tone, in one place; knowledge packs, prompt, limits, daily count; the Simple Host pack goes through the limits rewriter, `h/limitstext.go`, so a changed undo, saved-data cap or Recently-deleted window reads right, while the enterprise pack keeps its own product's words as the enterprise pages do), `h/chrome.go` (the `<!--sh:ask NAME-->` marker, `AskOn`/`AskPage` in `chromeData`), `cmd/server/main.go` |
| Knowledge | one combined pack per assistant, built at boot: its summary (`h/askdata/hosted.txt` or `h/askdata/enterprise.txt`) plus the visible text of each of its pages under that page's address — except the architecture page, which contributes the curated `h/askdata/architecture.txt` (a product-level summary), not the page. The prompt names the page the reader is on. Sizes (about 4 characters a token, whole prompt): Enterprise ≈ 7.5k tokens, Simple Host ≈ 14.5k (the features page is most of it); a test keeps them under 12k and 16k. Every line, askdata included, goes through one filter that drops machine and repo paths, IPv4/IPv6 addresses, ports, internal routes, secret and key names, phone numbers, email addresses other than @simple-host.app and the operator's details; the test checks every line the model gets against it. Hosted answers fall back to "ask support@simple-host.app"; enterprise answers carry no contact details |
| Privacy | only the question and the knowledge text go to the model (xAI's Grok) — no IP, user agent, cookie or identifier; a follow-up also sends the conversation's last few questions and answers, which live only in the reader's tab (sessionStorage, gone when it closes). The question text is never stored or logged (log line: assistant, page and the day's count; upstream errors log a status code only). `/v1/ask` is left out of the API IP metrics (`h/apimetrics.go`); standard web server logs apply. Disclosed on `privacy.html` |
| Env | `ASK_ENABLED` (on; `on`/`true`/`1`/`yes` or `off`/`false`/`0`/`no`; only matters when `LLM_API_KEY` is set), `ASK_BURST` (5; 1–50), `ASK_EVERY_SECONDS` (20; 1–3600), `ASK_DAILY_MAX` (500; 0–100000; per UTC day across everyone), `ASK_MAX_IN_FLIGHT` (4; 1–32), `ASK_MODEL` (grok-4.7; separate from `LLM_MODEL`, which AI create keeps), `ASK_REASONING_EFFORT` (none; none/low/medium/high, sent as `reasoning_effort`), `ASK_MAX_TOKENS` (300; 50–4000). Read with the other limit knobs in `internal/config/limits.go` (`Limits.Ask`), listed in `docs/configuration.md`, carried by `.env.example`, `compose.yaml` and the installer; any other value is a startup error |
| Tables | `ask_daily` (day, count) — the day's count, so a restart does not reset it (`db/migrations/ask-daily-count.sql`); `setup_check_daily` (day, count) the same for the setup check (`db/migrations/v061-setup-check-daily.sql`); `setup_assist_daily` (day, count) the same for the setup assistant (`db/migrations/v073-setup-assist-daily.sql`) |
| External | the Grok sidecar (`LLM_API_KEY`, `LLM_BASE_URL`), same as §14 but its own model (`ASK_MODEL`), called with `"stream": true`; no fallback. One ask is one request from us, never retried here (the sidecar's own retry setting is global to it) |
| Limits | 5 burst then 1 per 20 s per IP, and 4× that per /24 (IPv6 /48); at most 4 answered at once (503 `busy`, no daily slot used); 500 a day in total (429 `daily_limit`); question ≤ 500 characters; answer ≤ 200 words, links only to the assistant's own list (Simple Host: `/`, `/features`, `/architecture.html`, `/docs.html`, `/install.html`, `/privacy.html`, `/terms`, `/support`, `/enterprise`, `/setup`, `/setup?product=small-box`, `/setup?product=enterprise`, `/costs`; Enterprise: `/enterprise`, `/enterprise/brief`, `/enterprise/architecture`, `/`, `/privacy.html`, `/setup?product=enterprise`, `/costs`; a query is kept only where the list names it), other addresses removed; 1–3 short sentences unless the reader asks for detail (then ≤ ~150 words), a reply cut at `ASK_MAX_TOKENS` ends with "…"; first words within 20 s, whole answer within 45 s (502 `unavailable`, or an error event mid-stream) |

## 17. Legal and support pages

**Status: live.** `GET /terms` → `st/terms.html`; `GET /support` → `st/support.html`;
`/privacy.html` → `st/privacy.html` (file server, no clean route). Linked from plugin
listings; public contact is support@simple-host.app. Go: `h/ui.go`.

The shared policy and terms explicitly cover Simple Hack event/team pages and
its separately scoped organiser/team MCP connection. Privacy describes member
emails, role-based judging access, private team results and archive retention;
support links to `/events` and `/account` on Simple Hack and explains reconnecting
for the right role and revoking connections with Sign out everywhere.

The terms (updated 2026-09-28) cover: acceptable use (incl. terrorism, hate, self-harm,
sexual services, search spam, misinformation: dangerous health claims, misleading voters,
manipulated media; satire, parody and opinion are fine), named regulated goods, selling from a
site (allowed; payment through a provider, the site owner is the seller), what the service and
saved data are not for, children's and special-category data, intimate-image removal within 48
hours of a valid request, trademark/likeness and legal-request channels, evidence kept,
appeals within 30 days (a person reads each, one answer), 14 days' notice of adverse changes,
a security-report line, and the DMCA agent (DMCA-1081064, directory contact support@).

**Report a page.** `GET /report` → `st/report.html` (a form: the page's address, a reason from
eight, optional details, optional email; an emergency-services line; for child sexual abuse
material it says not to include the material). `POST /report` (`h/report.go`) takes JSON,
same-origin only (403 otherwise), 5 per address then one every 10 minutes and 60 an hour in
all (429), accepts only addresses on the platform domain or a bound custom domain, caps every
field (URL 2,048, details 4,000, email 254, body 16 KB), and emails the support contact
through the Resend mailer with the reporter as Reply-To. Nothing is stored; the log line keeps
only the reason and host. Linked from the site-wide footer (so the 404 page too), the home page,
the terms and support. The offline and take-down pages load nothing and carry no link.
`report` is a reserved new name.

## 18. Abuse limits and hardening

| Guard | Where |
|---|---|
| Per-IP token buckets | `h/ratelimit.go`; instances listed in each section (upload 30 burst/0.1 s⁻¹, state 60/1 s⁻¹, auth 20/0.2, email 5/0.02, connector, generate, transcribe, events, setup, reviewer); each user-facing one is a `RATE_LIMIT_*` setting (`docs/configuration.md`) |
| Size caps | per-site archive `MAX_ARCHIVE_MB` (default 100 MB, simple-host.app runs 100 with `MAX_ARCHIVE_MB_OVERRIDES` of 300 for two accounts; per-call caps `tarball.ExtractWithLimit` / `SanitizeFilesWithLimit`, `h/limits.go` `siteLimitFor`; served text states the cap in force via `h/limitstext.go` `archivePhrases`; `h/usage.go`), tarball total/file/path caps (`internal/tarball/extract.go`), state 1 MB, collection item 64 KB, ≤100 PATCH ops, a site's live saved data 50 MB, refusing only growth (`SAVED_DATA_SITE_MAX_MB`, 507 `site_full`) |
| Saved-data reads | `allowRead`, once the site is resolved: 30/s, burst 60 per site and address (never the Host header), or per account for a valid key or connector (`SAVED_DATA_READ_PER_SEC`, `SAVED_DATA_READ_BURST`), 429 `rate_limited` (every 429 now carries that code) |
| List appends | `allowAppend`: items added without the owner's key, 30/min per address, burst 30 (`SAVED_DATA_APPEND_PER_MIN`, `SAVED_DATA_APPEND_BURST`), 429 `rate_limited` |
| Idempotency | `Idempotency-Key` on list POST and state PATCH (`h/saveddata.go` `idemBegin`, table `idempotency_keys`, 24 h `SAVED_DATA_IDEMPOTENCY_HOURS`, at most `SAVED_DATA_IDEMPOTENCY_MAX_PER_SITE` per site), scoped by site, route and a signed-in identity only (no identity, no idempotency); keeps status, ETag, version or item id and a body hash, never a response body; another body under the same key is 409 `idempotency_key_reused` |
| Blocked upload types | `internal/tarball/validate.go` `blockedExtensions` |
| Write auth | `WRITE_AUTH_MODE`, `visitorWriteOK` (shared host key-only when `PERSON_HOSTS=canonical`), admin-only `allow_anonymous_writes` hatch (`PUT /v1/sites/{sitename}/allow-anonymous-writes`) |
| Origin checks | `authorizeStateOrigin`, `allowed_origins` (`PUT /v1/sites/{sitename}/allowed-origins`), `h/cors.go` |
| Headers / CSP | `SecurityHeaders`; apex nonce CSP `adminUICSP`; consent page `consentHeaders` |
| Reserved names | `h/handles.go` (handles), `h/platformsubdomain.go` `reservedSubdomainLabels`, `internal/db/namespace.go` |
| Preview accounts | `PREVIEW_ACCOUNTS`, `PREVIEW_TTL_HOURS` (expiry sweep in `h/site.go`) |
| Country-blocked sign-in | `SIGNUP_BLOCKED_COUNTRIES` (comma ISO-3166 alpha-2 codes, empty = off; `h/signupgeo.go`): every sign-in and sign-up attempt from a listed country is refused, a new account and an existing one alike, resolved from the request's real IP with the local geo database (`internal/geoip`, `GEOIP_DIR`) — never a third-party lookup. Checked at dashboard and visitor email-code sign-in (before a code is ever emailed) and owner/visitor Google and GitHub OAuth (before the provider round-trip), refusing with 403 `signup_unavailable_region` ("Sign-in isn't available from your region.", no country or reason named). An API key keeps working from anywhere — that is not a sign-in — and visitors can still view every site; a country the geo database cannot resolve is always allowed. Admin-issued accounts (`POST /v1/admin/users`) and the fixed plugin-reviewer sign-in are not geo-checked (2026-10-01) |
| Take-down | Operator suspend / restore of a site or a person, nothing deleted (§11) |
| Report form | `GET /report` / `POST /report` (`h/report.go`): same-origin only, 5 per address then 1 per 10 min, 60 an hour in all; emails support, stores nothing (§17) |
| Planned | Public Suffix List entry, brand-name blocklist (INTENT / owner TODO). AUP, report form and DMCA agent (DMCA-1081064) are done (§17) |

## 19. Signals and notifications

No webhooks or signals exist. Outbound email (`internal/email/resend.go`, Resend): sign-in
codes; the confirmation code for a new sign-in email and the notice to the old address (§7);
sign-in alerts after each owner sign-in or app connection, switchable (§7); the account-deleted
confirmation (§7); a custom domain failing for a day (§3); the idle-site warning and removal
notices (§1, reply-to support@simple-host.app, `SendNoticeReplyTo`); new-Submissions digests
("Email me": daily, each at most every 10 minutes, or off) with a signed stop link (§5; the
emailed one-time links carry their token after `#`, so it never reaches a server log). Stale-skill `_notice` in JSON
objects and the `X-Skill-Notice` header (§9; arrays stay bare) is the only in-band notice, sent only to a caller whose `X-Skill-Version` names an older skill (a plain API call without the header gets none).

## 20. Operations (health, schema, CLI)

| Surface | Details |
|---|---|
| Routes | `GET /healthz` · `GET /readyz` (DB ping) — `h/health.go` |
| Hack recovery checks | `scripts/check-hack-restore.sh` restores a trusted custom-format dump into disposable, network-isolated PostgreSQL 16 and reports schema/constraint/cross-event consistency aggregates. `docs/operations/simple-hack.md` records successful populated rehearsal recovery and encrypted production file readback; production project storage is currently empty. The existing nightly backup now preserves legitimate Hack project filenames excluded by other sources' generic filters. Outgoing email/bounce monitoring remains pending. |
| Startup | logs `simple-host <release> (commit <hash>)` (`internal/buildinfo`, stamped by `-ldflags -X` in `Dockerfile`, `.github/workflows/release.yml`, the CLAUDE.md build line); `internal/db/schemacheck.go` `VerifySchema` (fails fast on missing columns, names `simple-host migrate`); never migrates |
| Schema | `db/schema.sql` (new database) + `db/migrations/*.sql`; `db/migrations/migrations.go` embeds them and applies pending files in lexical order, each once in its own transaction, tracked in `schema_migrations`, under a Postgres advisory lock; historical files are a fixed baseline, never run; new files must be idempotent (rule in that file) |
| CLI subcommands | `simple-host migrate` (apply pending; `-status`; `-mark FILE` records without running), `simple-host version` (release, commit, migrations in this build; no DB), `simple-host oauth-client`, `simple-host review-account`, `simple-host geoip-verify`, `simple-host settings --json` (every setting with area, description, type, default, range; `docs/advanced/settings.json` is its output) (`cmd/server/`); `cmd/analytics-rebuild`, `cmd/ip-country-load` |
| DigitalOcean 1-Click | `deploy/digitalocean/droplet/`: Packer `template.json` (Ubuntu 24.04, `s-1vcpu-1gb`), Docker and the pinned release's images baked in; `files/opt/simple-host-setup/first-login.sh` from root's `.bashrc` (added by `001_onboot`) asks address and sites address, checks DNS against the droplet's IPv4 (reserved IP too; warns on a stray AAAA), runs `install.sh` by `INSTALLER_COMMIT` and `INSTALLER_SHA256` (same pin as `setup.js`, `test/pins_test.sh`) detached from the SSH session under a lock, then writes `.done` and removes its hook; `upgrade.sh` re-runs the pinned installer with `SITE_DOMAIN`/`CONTENT_HOST` from `.env`; `test/first-login_test.sh` (container); `marketplace/`; guide `docs/platforms/digitalocean.md`. Not yet built on DigitalOcean |
| Small-box upgrade | re-run `deploy/install/install.sh`: pulls the pinned release, `docker compose up -d db`, `docker compose run --rm app migrate`, then starts the new app; a failed migrate leaves the app as it was |
| Env | `DB_DSN`, `PORT`, `BIND_ADDR`, `DATA_DIR`, `SITE_DOMAIN`, `PUBLIC_BASE_URL`, `CONTENT_HOST`; dev-only `CHROME_SERVE_ADDR`, `CHROME_SERVE_FOR`; migration-only `UNIFY_KEEP` |
| Operational times and limits | 95 env vars (`SIGNIN_CODE_TTL_MINUTES`, `MAX_SITES_PER_ACCOUNT`, `DELETED_RETENTION_DAYS`, `RATE_LIMIT_*`, `SAVED_DATA_*`, `ASK_*`, …), read once at startup with range checks in `internal/config/limits.go` (a bad value stops the server; the sign-in, visitor sign-in and connector OAuth limiters at most 4× looser than default; other rate limits warn past 10×, unknown `RATE_LIMIT_*` names warn), default today's values; promised dates are stored when made (`sites.purge_at`, `idle_remove_at`, `domain_release_at`), so a changed retention or grace applies to new deletions and warnings only; `handler.ApplyLimits` hands db/mcp/tarball their share; copy that states a value follows it (Go text formats it, served pages/docs/skills are rewritten by `h/limitstext.go`, nil at the defaults). Full table, and the issuers' `/etc/simple-host-{domain,site}-certs.conf`: `docs/configuration.md`; by area with recipes: `docs/advanced/` (tables generated from `docs/advanced/settings.json`; `internal/config/settings.go` is the registry, a test fails when a read env var is missing from it) |
| Deploy | `/usr/local/bin/simple-host` as `simple-host.service`, env `/etc/simple-host.env`; `deploy/prod/*` (incl. log retention `logrotate-analytics.conf` and `journald-retention.conf`, 30 days), `Dockerfile`, `compose.yaml`, `Makefile`; checks `scripts/check-{docs-sync,features,html,layering,claude-plugin,reserved-subdomains,fresh-install}.sh` |

## 21. MCP tool index (41 website tools; 10 organiser tools on Simple Hack)

The event-management connection uses this separate inventory (`internal/mcp/hack_tools.go`):

| Tool | REST call | § |
|---|---|---|
| `hack_list_events` | `GET /v1/hack/events` | 15 |
| `hack_check_event_name` | `GET /v1/hack/names/{slug}` | 15 |
| `hack_create_event` | `POST /v1/hack/events` | 15 |
| `hack_get_event` / `hack_update_event` | `GET` / `PATCH /v1/hack/events/{slug}` | 15 |
| `hack_set_event_stage` | `POST /v1/hack/events/{slug}/stage` | 15 |
| `hack_get_rubric` / `hack_set_rubric` | `GET` / `PUT /v1/hack/events/{slug}/rubric` | 15 |
| `hack_export_scores` / `hack_export_results` | `GET /v1/hack/events/{slug}/export/{scores,results}.csv` | 15 |

Website tools (`internal/mcp/tools.go` and `kinds.go`):

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
| `keep_site` | `PUT /v1/sites/{s}/keep` | 1 |
| `set_site_passcode` | `PUT`, `DELETE`, `GET /v1/sites/{s}/lock` or `POST /v1/sites/{s}/lock/sign-out-everyone` | 1 |
| `get_state` | `GET …/state` | 4 |
| `update_state` | `PATCH` or `PUT …/state` | 4 |
| `list_collections` | `GET /v1/sites/{s}/collections` | 5 |
| `read_collection` | `GET …/collections/{c}` | 5 |
| `add_to_collection` | `POST …/collections/{c}` | 5 |
| `set_collection_privacy` | `PUT /v1/sites/{s}/collections/{c}/privacy` | 5 |
| `update_collection_item` | `PATCH …/collections/{c}/items/{id}` | 5 |
| `delete_collection_item` | `DELETE …/collections/{c}/items/{id}` | 5 |
| `clear_collection` | `DELETE …/collections/{c}` | 5 |
| `data_history` | `GET /v1/sites/{s}/state/history[/{id}]` or `…/collections/{c}/history[/{id}]` | 4, 5 |
| `restore_data` | `POST …/state/history/{id}/restore` or `…/collections/{c}/history/{id}/restore` | 4, 5 |
| `list_deleted` | `GET /v1/sites/{s}/collections/{c}/deleted` | 5 |
| `restore_item` | `POST …/collections/{c}/items/{id}/restore` or `…/deleted/restore` | 5 |
| `delete_forever` | `DELETE …/collections/{c}/deleted/{id}`, `DELETE …/collections/{c}/deleted` or `DELETE /v1/sites/{s}/history` | 4, 5 |
| `connect_domain` | `POST /v1/sites/{s}/domain`; `*.<domain>` (no site): `POST /v1/me/address-families` | 3 |
| `domain_status` | `GET /v1/sites/{s}/domain`; `*.<domain>`: `GET /v1/me/address-families/{suffix}` | 3 |
| `remove_domain` | `DELETE /v1/sites/{s}/domain`; `*.<domain>`: `DELETE /v1/me/address-families/{suffix}` | 3 |
| `site_analytics` | `GET /v1/sites/{s}/analytics?days=` + `GET /v1/sites/{s}/analytics/top?days=` | 12 |
| `export_site` | `POST /v1/sites/{s}/export-link` (returns a link to `GET /v1/export?token=`) | 1 |
| `declare_data` | `PUT /v1/sites/{s}/data/{name}/kind` | 5 |
| `list_data` | `GET /v1/sites/{s}/data` | 5 |
| `update_data` | `PUT /v1/sites/{s}/data/{name}` | 5 |
| `set_who_can_save` | `PUT /v1/sites/{s}/savers` | 5 |
| `block_person` | `POST /v1/sites/{s}/savers/block` | 5 |

## 22. Unplaced routes and tools

None. Every `mux.Handle`/`HandleFunc` registration in `cmd/server` and `internal/handler`
(154 distinct method+path patterns, plus the looped `/mcp`, `/skills/{dir}.*` and
`rewrittenAssets` routes) and all 41 MCP tools are placed above. Routes that exist outside
the mux: host-routed site hosts / person hosts / claimed names / custom domains (§2, §3) and the
nginx-only `/v1/transcribe/stream` (§14).
