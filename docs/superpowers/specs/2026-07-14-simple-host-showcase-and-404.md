# Per-user Showcase (`sites.simple-host.app/<handle>`) + Branded 404 pages

**Status:** PROPOSAL — needs owner review before implementation (2026-07-14)

**Scope:** `simple-host.app` (the public box) ONLY. The `ideaflow.page` deployment
is explicitly excluded — it is being deprecated and nothing here should touch it.

---

## Summary

Two intertwined features for the public path-model host
(`https://sites.simple-host.app/<handle>/<site>/…`):

1. **Per-user showcase** at `sites.simple-host.app/<handle>` — a public-facing
   profile page listing that user's sites. The **owner** (logged in as that handle)
   sees all their sites plus management actions, mirroring today's apex dashboard;
   a **public visitor** sees a read-only list of only that user's PUBLIC sites.
2. **Branded 404 pages** replacing the current ugly nginx defaults, with
   case-aware "back" links (back to the user's showcase when the handle exists;
   back to the main page when it doesn't).

Both features hinge on the same mechanism: today `sites.simple-host.app` is served
**entirely by nginx** (`alias` file_server) with no Go involvement for content
paths (`deploy/prod/nginx-sites-content-host.conf:35-38`). Distinguishing "handle
exists but site missing" from "unknown handle" from "missing asset" needs a **DB
lookup**, which nginx can't do. So both features require routing the relevant
misses — and the new single-segment `/<handle>` path — **to the Go app**, which
already has all the DB queries and the branded UI templates embedded.

---

## Goals / Non-goals

**Goals**
- A public profile page per user at `sites.simple-host.app/<handle>` and `/<handle>/`.
- Owner vs. visitor rendering off the existing login mechanism, "lightly guarded."
- Branded, on-theme 404 pages with correct back-links for each miss case.
- Reuse the existing dashboard UI/branding (JetBrains Mono, dark/cyan;
  `internal/handler/static/index.html`) and existing analytics/site queries.
- Deploy incrementally and reversibly on a box the owner actively uses.

**Non-goals**
- No changes to `ideaflow.page` or any non-`simple-host.app` deployment.
- No new auth system — reuse the existing `X-API-Key`/localStorage flow.
- No global cross-user directory ("all public sites everywhere") in V1.
- Not deeply designing the V2 published/unlisted split (noted, not built here).
- Not touching the legacy `<name>.simple-host.app` subdomain behavior beyond
  leaving its current 301-to-path / 301-to-home redirect intact
  (`internal/handler/legacyhost.go:45-54`).

---

## Verified facts this spec builds on (with file:line refs)

- **Content host is pure nginx.** `sites.simple-host.app` is served by
  `deploy/prod/nginx-sites-content-host.conf` (live copy:
  `/etc/nginx/sites-enabled/sites-content-host`). Locations today:
  - `^/([a-z0-9-]{1,39})/([a-z0-9-]{1,63})$` → `308` add trailing slash (line 30-32).
  - `^/(?<h>…)/(?<s>…)(?<rest>/.*)?$` → `alias /srv/simple-host/sites/handles/$h/$s/current$rest` (line 35-38).
  - `^~ /v1/` and `^~ /internal/` → proxy to `127.0.0.1:8090` (lines 16-27).
  - `location = /` → `return 404` (line 41).
  - **Gap:** a bare single-segment `/<handle>` or `/<handle>/` matches **no
    location** → nginx default 404. This is exactly the showcase URL.
  - **Gap:** every miss (unknown handle, missing site, missing asset) falls to
    nginx's default 404 — no way to brand or differentiate without Go.
- **Apex + legacy subdomains** proxy to the Go app on `127.0.0.1:8090`; the Go
  server's outermost handler is `LegacyHostRedirect(…SecurityHeaders(CORS(mux)))`
  (`cmd/server/main.go:103`). Routing is one stdlib `http.ServeMux` built in
  `main.go:42-99`; adding routes = adding `mux.Handle` lines
  (`internal/handler/site.go:191-257` is the pattern).
- **Auth is `X-API-Key` only** (`internal/auth/middleware.go:33-83`); the admin key
  short-circuits to a synthetic user (line 55-66). The browser stores the key in
  **localStorage** (`static/index.html:1152`, set at `:1275`) and sends it as
  `X-API-Key` on every fetch (`:1368`); it verifies via `GET /v1/me` on load
  (`:1307`). **There is no server-set session cookie** except the per-site
  view-lock cookie `shview_<site>` (`internal/handler/viewauth.go:135-143`), which
  is unrelated to identity. Consequence: **a plain browser navigation to
  `sites.simple-host.app/<handle>` carries NO identity** — the key lives only in
  JS/localStorage on the apex origin and is never sent as a header on a top-level
  GET. See "Owner-vs-visitor auth" for how we handle this.
- **Handle model.** `users.handle TEXT UNIQUE` (`db/schema.sql:10`), regex
  `^[a-z0-9-]{1,39}$`, nullable until claimed. Lookups:
  `db.GetUserByHandle(ctx, db, handle)` (`internal/db/queries.go:75-92`) and
  `db.GetHandleBySiteName` (`:98`). Sites per user:
  `db.ListSitesByUser(ctx, db, userID)` (`:415-428`) selects
  `id, user_id, name, active_version, site_url, created_at, updated_at,
  custom_domain, domain_status`.
- **Sites table** (`db/schema.sql:15-30`): `id, user_id, name, active_version,
  site_url, expires_at, allowed_origins, custom_domain, domain_status,
  domain_verified_at, domain_last_error, created_at, updated_at`. **There is a
  `state jsonb` column plus `state_version int`** on `sites` (used by the per-site
  state store: `queries.go:533` `SELECT COALESCE(state,'null'::jsonb),
  state_version FROM sites WHERE name=$1`). It is **not shown in the committed
  `db/schema.sql`** excerpt (added by a later migration), but it exists in prod.
  **There is currently NO visibility / public / unlisted field anywhere** — it
  must be added (see Feature 1 data model and V2).
- **Analytics.** `GET /v1/sites/{sitename}/analytics` (auth-gated,
  `site.go:198`; handler `internal/handler/analytics.go:15`) returns
  `{range_days, totals:{views,visitors}, daily:[{day,views,visitors}]}` from
  `db.GetSiteAnalytics` (`internal/db/analytics.go`). The ingester
  (`internal/analytics/ingest.go`) tails the nginx analytics log and **only counts
  `status == 200 || 304`** (`ingest.go:405`) on a **two-segment `<handle>/<site>`
  content-host path** (`ingest.go:434-462`, needs `clean[0], clean[1]`).
  **Therefore proxied 404s and single-segment `/<handle>` showcase hits will NOT
  pollute per-site analytics as the code stands** — see Blast radius for the
  caveat.

---

## Feature 1 — Per-user Showcase at `sites.simple-host.app/<handle>`

### Routes
Add to nginx `sites.simple-host.app` server block (both repo file and live box):
```nginx
# Single-segment user root -> showcase (proxied to Go). ^~ beats the regex farm.
location ~ "^/(?<h>[a-z0-9-]{1,39})/?$" {
    proxy_pass http://127.0.0.1:8090/internal/showcase/$h$is_args$args;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    # Forward the browser cookie so Go can read an (optional) owner session — see auth.
    proxy_set_header Cookie $http_cookie;
}
```
Both `/<handle>` and `/<handle>/` hit this (the `/?` makes the trailing slash
optional). The existing two-segment 308-add-slash and the alias farm are
**unaffected** because they require a second path segment.

New Go route (register in `SiteHandler.Register`, alongside the existing
`/internal/*` routes at `site.go:230-234`):
```
GET /internal/showcase/{handle}        -> h.showcase   (returns 200 HTML)
```
`{handle}` is taken from the path nginx rewrote to; the real public URL the page
should render/link to is `https://sites.simple-host.app/<handle>` (derive from
`X-Forwarded-Proto` + `Host` + original handle).

### Two audiences
- **Owner** (viewer is authenticated as this exact handle): render the full
  dashboard — overview stats (total sites, viewed today, visits today, 7-day
  views) + Site Inventory table with visibility badge, per-site today views /
  visits / 7-day views, expandable sparkline, and management actions (Open /
  Make public|unlisted / Delete). This is the same data the apex UI already
  fetches from `GET /v1/sites` + `GET /v1/sites/{name}/analytics`.
- **Public visitor** (not logged in, or logged in as a different handle): render a
  read-only showcase of **only that user's PUBLIC sites** — no management actions,
  no unlisted sites, no visibility toggles, no Delete. Show site name/title, link
  to open (`/<handle>/<site>/`), and optionally public view counts (owner
  decision — see Open Questions).

### Owner-vs-visitor auth ("lightly guarded")
The core constraint: a top-level browser GET to `sites.simple-host.app/<handle>`
carries the `X-API-Key` **only if we arrange for it**, because today the key lives
in localStorage on the apex origin and is sent as a header only by `fetch()`
(`static/index.html:1368`), never on navigations, and localStorage is per-origin
(apex ≠ `sites.` subdomain). Chosen approach, in order of preference:

1. **Server-rendered shell + client hydration (recommended).** `GET
   /internal/showcase/{handle}` always returns the **public** read-only view as
   fully-rendered HTML (correct for visitors, safe default). The page then runs a
   small script: if `localStorage.getItem('apiKey')` exists *and* is on the
   `sites.` origin, it calls `GET /v1/sites` (same-origin — `/v1/*` already proxies
   to Go from the content host, `nginx…conf:16`) with the `X-API-Key` header; if
   `/v1/me`'s handle matches the page's handle, it swaps in the owner view
   (management actions, unlisted sites). This keeps the guard entirely in the
   existing header-auth path and needs no new cookie. **Caveat:** localStorage is
   per-origin, so a user logged in on the apex is **not** automatically logged in
   on `sites.`. Options: (a) accept that owners "sign in" once on the showcase too
   (paste key / magic-link, reusing `/v1/auth`), or (b) later unify both pages
   under one origin (see Transition note). For V1, (a) is acceptable and matches
   "lightly guarded."
2. **Cookie-based session (heavier, optional later).** Introduce a real
   domain-wide identity cookie (`Domain=.simple-host.app`, HttpOnly, Secure,
   SameSite=Lax) set at login so both apex and `sites.` recognize the owner on a
   plain navigation, letting the Go handler render the owner view server-side. This
   is a genuine new auth surface (the only cookie today is the unrelated
   `shview_<site>` view-lock cookie) — defer unless the owner wants server-side
   owner rendering without a JS hop.

**Decision for V1:** approach (1). Server renders the public view; JS upgrades to
owner view when a matching key is present same-origin. No new cookie, no new auth
model. The Go handler stays a pure read of `GetUserByHandle` + public-site list.

### UI
Reuse `internal/handler/static/index.html` styling (embedded via
`internal/handler/ui.go` `//go:embed all:static`). Extract the dashboard card /
inventory-table markup into a template or a second embedded HTML file the showcase
handler renders. Keep branding identical (JetBrains Mono, dark/cyan). The public
view is the same table minus the action column and minus unlisted rows.

### Data
- Look up user: `db.GetUserByHandle` (`queries.go:75`). Unknown handle → this is a
  **404 case** (Feature 2), not an empty showcase.
- Owner site list: existing `db.ListSitesByUser` (`queries.go:415`).
- Public-only list: `ListSitesByUser` filtered to `visibility='PUBLIC'`, or a new
  `ListPublicSitesByUser(userID)` query. **Visibility field must be added** — see
  next.

### Visibility data model (needed even for V1's public filter)
Two viable stores; pick one and be consistent:

- **Option A (recommended): dedicated column.**
  `ALTER TABLE sites ADD COLUMN visibility TEXT NOT NULL DEFAULT 'public'
   CHECK (visibility IN ('public','unlisted'));`
  Pros: indexable, trivially filterable in `ListSitesByUser`/showcase queries,
  self-documenting. Backfill default `'public'` matches today's behavior (every
  existing site is publicly reachable by URL, so listing it is not a new leak —
  but see Open Questions on whether default should instead be `unlisted`).
- **Option B: reuse `sites.state` jsonb** (e.g. `state->>'visibility'`). Pros: no
  migration. Cons: `state` is the **public per-site scratch store** — it's
  browser-writable via the Origin-gated state API (`stateops.go`), so putting an
  access-control-relevant flag there is unsafe (a page could flip its own
  visibility). **Reject Option B for anything security-relevant.**

**Decision:** Option A, new `visibility` column, default `'public'` in V1 to
preserve current behavior; the meaningful public/unlisted UX is V2. Add a toggle
endpoint `PUT /v1/sites/{sitename}/visibility` (auth-gated, owner-only via existing
`resolveSiteID` ownership check at `site.go:313-320`) and surface the
badge/toggle in the inventory table.

### Transition note (apex → showcase)
The apex homepage keeps showing the logged-in owner's dashboard for now (no forced
change). Long-term the canonical "your sites" page moves to
`sites.simple-host.app/<handle>`, and the apex dashboard can become a redirect to
it once owners routinely have handles and the same-origin login story is unified.
Spec notes the migration; it does not force it.

---

## Feature 2 — Branded 404 pages (simple-host.app only)

Replace nginx default 404s with on-theme pages whose back-link depends on **why**
the request missed. The differentiation requires a DB lookup, so misses are routed
to Go via nginx `error_page`/fallback.

### The nginx → Go fallback mechanism
In the `sites.simple-host.app` server block, on the alias farm location, send file
misses to a named fallback that proxies to Go:
```nginx
location ~ "^/(?<h>[a-z0-9-]{1,39})/(?<s>[a-z0-9-]{1,63})(?<rest>/.*)?$" {
    alias /srv/simple-host/sites/handles/$h/$s/current$rest;
    index index.html;
    error_page 404 = @notfound;          # missing site dir OR missing asset
}
location @notfound {
    internal;
    proxy_pass http://127.0.0.1:8090/internal/notfound;
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_set_header X-Original-URI $request_uri;   # Go learns the real path
    proxy_set_header Cookie $http_cookie;
}
# Bare content-host root: still not a site, but brand it.
location = / {
    proxy_pass http://127.0.0.1:8090/internal/notfound;
    proxy_set_header Host $host;
    proxy_set_header X-Original-URI $request_uri;
    proxy_set_header X-Forwarded-Proto $scheme;
}
```
New Go route: `GET /internal/notfound -> h.notFound`. It reads `X-Original-URI`
(preferred) / `X-Forwarded-Proto` / `Host`, parses the original path into
segments, and decides:

| Case | Original path | Go logic | Response |
|---|---|---|---|
| Handle exists, site/asset missing | `/<h>/<s>/…` where `GetUserByHandle(h)` succeeds | 404 branded page | **Back-link → the user's showcase `https://sites.simple-host.app/<h>`** (owner's stated priority) |
| Unknown handle | `/<h>/…` where `GetUserByHandle(h)` = `sql.ErrNoRows` | 404 branded page | Back-link → `https://simple-host.app` (main page) |
| Bare content root | `/` | 404 branded page | Back-link → `https://simple-host.app` |
| Bare `/<handle>` or `/<handle>/` | single segment | **NOT a 404** — routed to showcase (Feature 1) before reaching `@notfound` | 200 showcase |

The Go handler **must set HTTP status 404** for the miss cases (nginx `error_page
… = @notfound` without an explicit override would otherwise inherit; set it in Go
explicitly and, if needed, `proxy_intercept_errors off` on the fallback so Go's 404
passes through verbatim). It must **not** distinguish "missing site" vs "missing
asset" for the back-link — both go back to the showcase, which is the desired
behavior ("you're at a real user, here's their stuff").

### Apex unknown paths
Apex (`simple-host.app`) already proxies everything to Go
(`main.go:103` chain). Any unmatched apex path currently hits the ServeMux's
default (stdlib `http.ServeMux` returns a plain `404 page not found`). Add a
catch-all `GET /` fallback in the mux that, when the path isn't a known UI route,
renders the **branded 404 linking to the main page**. (Careful: `/` is already the
admin UI — the catch-all must only fire for genuinely unknown subpaths, e.g. via a
`http.NotFoundHandler` replacement or an explicit `mux.HandleFunc("GET /",
h.apexRoot)` that serves the UI for `/` and the branded 404 otherwise. Verify
against existing `RegisterUIRoutes` in `ui.go` so we don't shadow real routes.)

### Legacy subdomain unknown
`internal/handler/legacyhost.go:45-48` already 301s an unknown
`<name>.simple-host.app` to `https://simple-host.app/`. Leave as-is (a redirect to
the branded main page is acceptable). Optionally later swap the 301-to-home for a
branded 404, but not required.

### Interaction between the two features (call-out)
`sites.simple-host.app/<handle>/` (single segment, trailing slash) is the
**showcase (Feature 1)**, NOT a 404 — the new single-segment `location` handles it
and proxies to `/internal/showcase/{handle}`. Only a **known handle with a missing
second segment target** (`/<h>/<s>/…`) or an **unknown handle** yields a 404. The
showcase handler and the notfound handler share the same `GetUserByHandle` lookup;
keep them in one file (`internal/handler/showcase.go`) for coherence.

---

## Blast radius & edge cases (the big section)

**nginx config changes (repo `deploy/prod/nginx-sites-content-host.conf` AND live
`/etc/nginx/sites-enabled/sites-content-host`):**
- New single-segment `^/(?<h>…)/?$` location → proxy to `/internal/showcase/$h`.
  Must be ordered so it does NOT swallow `/v1/…`, `/internal/…`, `.well-known`, or
  the two-segment farm. The `^~ /v1/` and `^~ /internal/` prefix locations already
  win over regex locations, so those are safe. Verify `.well-known` (ACME / skills
  discovery) is not a single-segment casualty — add an explicit `location
  ^~ /.well-known/` proxy or file rule if needed.
- `error_page 404 = @notfound` on the alias farm + the `@notfound` internal
  location + branded bare-root. Set `proxy_intercept_errors off` on `@notfound` so
  Go's 404 status is preserved.
- Both files must be kept in sync; deploy plan reloads nginx with `nginx -t` first.

**Go routing changes (`cmd/server/main.go` + a new `internal/handler/showcase.go`):**
- Register `GET /internal/showcase/{handle}` and `GET /internal/notfound` (mirror
  the `/internal/*` registration style at `site.go:230-234`).
- Handlers learn the original request via `X-Original-URI` + `Host` +
  `X-Forwarded-Proto` (nginx must forward these — included above). Do **not** trust
  `r.URL.Path` for the notfound handler, since nginx rewrites it to
  `/internal/notfound`.
- Distinguish cases via `db.GetUserByHandle` (`queries.go:75`): success → showcase
  / "handle exists" 404; `sql.ErrNoRows` → "unknown handle" 404.
- The new routes sit **inside** the existing outer chain
  (`LegacyHostRedirect(SecurityHeaders(CORS(mux)))`, `main.go:103`).
  `LegacyHostRedirect` passes `sites.simple-host.app` straight through (host ==
  contentHost, `legacyhost.go:32`), so no interference. `SecurityHeaders`/`CORS`
  wrap the response — fine for HTML.

**Existing route interactions:**
- `/v1/*` and `/internal/*`: untouched; `^~` prefix locations already outrank the
  new regex. The new Go `/internal/showcase|notfound` routes are new leaves.
- 308 trailing-slash redirect (`nginx…conf:30-32`): only matches two-segment
  `/<h>/<s>` — unaffected by the single-segment showcase location.
- Custom domains: served by a separate server block
  (`deploy/prod/nginx-customdomain.example.conf`) — out of scope, not touched.
- Apex dashboard: unchanged; showcase is additive. Migration is deferred.
- **Analytics pollution:** the ingester counts only `200/304` on two-segment
  content paths (`ingest.go:405,434-462`). Showcase hits are single-segment and,
  when proxied, still logged under host `sites.simple-host.app` — but they won't
  match the two-segment attribution, so they're dropped (good). Branded 404s are
  status 404 → dropped by the `status != 200/304` filter (good). **Caveat:**
  confirm the nginx `access_log` for the analytics format still fires on the
  `@notfound`/showcase proxied requests; if it logs the *rewritten* internal URI
  or a 200 from Go, re-verify the filter. Recommendation: exclude `@notfound` and
  `/internal/showcase` from the analytics `access_log` explicitly
  (`access_log off;` in those locations) to be safe.

**Auth/session reuse:** owner detection uses the existing `X-API-Key` +
`localStorage` + `/v1/me` flow (`middleware.go`, `static/index.html:1152,1307,1368`),
upgraded client-side. No new cookie in V1. Public view needs no auth.

**Visibility model (V1):** new `sites.visibility` column, default `'public'`,
`CHECK IN ('public','unlisted')`; toggle via `PUT /v1/sites/{sitename}/visibility`
(owner-only). Migration is additive + backfilled by the default.

**Caching / CDN / status codes:** showcase → `200`, `Cache-Control: no-store` or a
short TTL (owner view must not be cached and served to a visitor). 404 pages →
must stay `404` (set explicitly in Go; `proxy_intercept_errors off`). Add
`X-Robots-Tag: noindex` on 404 responses and on the owner view; the public
showcase MAY be indexable (owner decision).

**Failure modes:**
- **DB down:** `GetUserByHandle` errors. Showcase → serve a generic branded
  "temporarily unavailable" (503) rather than leaking a 500 stack; notfound →
  fall back to a static branded 404 with the main-page link (never hard-fail into
  nginx's ugly default). Handler must treat DB error distinctly from
  `sql.ErrNoRows`.
- **Weird casing:** handles are lowercase `^[a-z0-9-]{1,39}$`. nginx regex already
  enforces lowercase; Go should `strings.ToLower`/validate and 404 on invalid
  shapes rather than querying.
- **Reserved handles:** `sites`, `www`, `v1`, `internal`, `.well-known`, `cname`,
  `api` must never be treated as user handles. The exact-match `server_name
  sites.simple-host.app` already prevents "sites" from being a site
  (`nginx…conf:5-6`); still, the single-segment location must exclude reserved
  words (add a `location = /v1` style guard or check in Go and 404). Ensure handle
  **claiming** already denylists these (verify `ClaimHandle`; if not, add to the
  denylist — separate small change).
- **Handle colliding with a real single-segment asset:** today no single-segment
  asset is served (the farm requires two segments), so there's no collision. Keep
  it that way — do not add single-segment file serving.

**Rollout safety (live box the owner uses — "don't break it"):**
- Phase changes so each is independently reversible; `nginx -t` before every
  reload; keep a copy of the current `sites-content-host` to restore.
- Ship the Go handlers first (embedded, inert until nginx routes to them), verify
  via `curl -H 'X-Original-URI: /nope/nope' localhost:8090/internal/notfound`.
- Add nginx locations second; verify real URLs; roll back nginx by restoring the
  saved file + reload if anything regresses.
- The visibility migration is additive with a safe default — no data loss, and the
  column is ignored until the showcase/toggle ship.

---

## Open questions for the owner

1. **Default visibility & indexing.** Should new (and backfilled) sites default to
   `public` (listed on the showcase, current behavior — every site is already
   URL-reachable) or `unlisted` (reachable by link, hidden from the showcase until
   the user opts in)? And should the **public showcase be search-indexable**
   (`noindex` or not)?
2. **Owner login on the `sites.` origin.** localStorage is per-origin, so being
   logged in on the apex does NOT log you in on `sites.simple-host.app`. Accept a
   one-time sign-in on the showcase (V1, simplest), or invest in a domain-wide
   identity cookie so owners are recognized on a plain navigation across both
   origins?
3. **What public visitors see per site.** Just name + open-link, or also public
   view/visitor counts? (Counts are currently an auth-gated endpoint; exposing them
   publicly is a small but deliberate disclosure choice.)

---

## Suggested build order (phased, safe on a live box)

1. **Migration + model:** add `sites.visibility` column (default `'public'`,
   CHECK), `Visibility` field on the `Site` struct, `ListPublicSitesByUser` query,
   and `PUT /v1/sites/{sitename}/visibility` (owner-only). Ships inert.
2. **Go handlers, unrouted:** `internal/handler/showcase.go` with
   `GET /internal/showcase/{handle}` (public view, server-rendered) and
   `GET /internal/notfound` (case-aware branded 404 reading `X-Original-URI`).
   Register in `Register`/`main.go`. Verify with `curl` against `:8090`. Still
   inert (nginx doesn't route to them yet).
3. **Branded 404 first (lower risk):** add `error_page 404 = @notfound` +
   `@notfound` + branded bare-root to nginx (repo file + live, `nginx -t`, reload).
   Verify each miss case returns the right branded page + 404 status + back-link.
   This alone is a visible win and easy to roll back.
4. **Showcase route:** add the single-segment `^/(?<h>…)/?$` location proxying to
   `/internal/showcase/$h`. Verify public view for a known handle; 404 for unknown.
5. **Owner upgrade (client hydration):** add the small JS in the showcase page that
   detects a same-origin `X-API-Key`, calls `/v1/sites` + `/v1/me`, and swaps to
   the owner view with management actions + visibility badge/toggle.
6. **Apex branded 404:** add the mux catch-all for unknown apex paths (careful not
   to shadow `/` UI or `/v1`). Verify.
7. **Analytics hardening:** confirm proxied showcase/404 requests don't pollute
   per-site analytics; add `access_log off;` to the new locations if needed.
8. **Docs sync:** update `internal/handler/static/openapi.yaml` (+ regenerate
   `openapi.json`) and `llms.txt`/skills for the new `/v1/sites/{name}/visibility`
   route; run `bash scripts/check-docs-sync.sh` (required before deploy per repo
   CLAUDE.md).

Each phase is independently deployable and reversible (restore the saved nginx file
or roll back the binary to a `simple-host.bak-*`), which is the safety property the
owner needs on a box they actively use.

---

## V2 notes (later — not designed deeply here)

- **Published/unlisted distinction "so no one would know."** V1 already introduces
  the `visibility` column; V2 makes it a first-class UX: a site can be `unlisted`
  (reachable by direct link `/<handle>/<site>/`, but omitted from the public
  showcase and, optionally, `noindex`) vs `public` (listed). No schema change
  beyond V1's column; V2 is the UI + the default-visibility decision (Open
  Question 1) + optional per-site `noindex` injection.
- **Global public directory** (all public sites across users) — explicitly out of
  scope; only per-user showcases are in scope now.
- **Apex → showcase migration:** once the same-origin login story is settled
  (Open Question 2), redirect the apex dashboard to `sites.simple-host.app/<handle>`
  and retire the duplicate dashboard rendering on the apex.
