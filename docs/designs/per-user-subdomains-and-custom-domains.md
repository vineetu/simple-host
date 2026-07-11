# Design: Path-Based User Sites + Custom Domains (v3)

**Status:** Draft v3 (2026-07-11) · **Author:** Claude (for Vineet)
**Supersedes v2's addressing** (per-user *subdomains*) with a simpler, safer **path-based**
model on a dedicated content subdomain. Review rounds 1–3 (2 subagents + Grok ×3) from v2
are retained where still applicable; a fresh spike + review round targets v3 (§16–17).

---

> **Correction (review round 4):** the domain is **`simple-host.app`** (hyphenated) — earlier
> drafts wrote `simple-host.app`, now fixed throughout. The content subdomain is
> **`sites.simple-host.app`**, which *is* covered by the existing `*.simple-host.app` wildcard
> (so zero provisioning holds). NOTE it is **same-site** with the dashboard `simple-host.app`
> (same registrable domain) — NOT the cross-site github.io pattern — so the isolation story is
> about **origin** (JS can't read the other origin's localStorage/DOM/HttpOnly cookies), not
> SameSite. The load-bearing CSRF defense is that the management API is **`X-API-Key`
> header-auth with no `Allow-Credentials`** and **no auth cookie** — that invariant is
> preserved (see §3); we do NOT add a session cookie. "Forever login" = a persistent api_key
> in `localStorage` on the dashboard origin, which content JS (different origin) can't read.

## 0. The model (final, decided by Vineet)

Two origins, on purpose:

| Surface | URL | Origin |
|---|---|---|
| Login, dashboard, management API | `https://simple-host.app/…` | `simple-host.app` |
| A user's home page (open/public) | `https://sites.simple-host.app/<username>/` | `sites.simple-host.app` |
| A user's site | `https://sites.simple-host.app/<username>/<sitename>/…` | `sites.simple-host.app` |
| A site on the user's own domain | `https://yourbrand.com/…` | `yourbrand.com` |
| Legacy (deprecated, still served) | `https://<site>.simple-host.app/…` | per-site |

- **No subdomains are minted per user or per site.** A username and a site name are just
  **path segments** under the single content subdomain `sites.simple-host.app`.
- **The content subdomain is separate from the login/dashboard domain** so user-uploaded
  JavaScript can never touch the login session (different browser origin) — this is the
  key security property and the reason we don't serve sites on the apex.
- **Login = email code + long-lived cookie, no passwords.** Viewing is open; only managing
  sites needs sign-in.
- **Sites must use relative asset links** (`style.css`, not `/style.css`) because they're
  served under a path prefix. Enforced by guidance in the deploy skill/llms.txt (and a
  best-effort `<base>`-tag safety net, see §6).
- **Custom domains** let any single site get its **own fully-isolated origin** (the escape
  hatch from the shared `sites.simple-host.app` origin).
- **Legacy `<site>.simple-host.app`** stays working, deprecated; all new sites use the path model.

### Goals / non-goals
Goals: one simple, safe hosting shape; login isolated from user code; per-site custom
domains with zero-touch TLS; no per-user DNS/cert provisioning; existing sites keep working.
Non-goals: we don't run DNS for users; no per-user or per-site subdomains; no email/MX.

---

## 1. Why this shape

- **Simplicity:** `sites.simple-host.app` is **one** name — already covered by the existing
  `*.simple-host.app` wildcard DNS + wildcard cert, so there is **zero per-user
  provisioning, zero per-user cert, no rate limit.** (v2's whole handle-provisioning +
  per-user-subdomain machinery disappears.)
- **The critical security boundary is free:** login/dashboard on `simple-host.app`, all
  user content on `sites.simple-host.app`. Arbitrary user JS runs on the content origin and
  **cannot read the dashboard session cookie or script the dashboard** (cross-origin). This
  is the standard "user content on a sacrificial domain" pattern (github.io, etc.).
- **Custom domains give per-site isolation** when a site actually needs it.

Accepted tradeoff (see §7): all sites on `sites.simple-host.app` **share one origin**, so
they're not isolated **from each other** in the browser (any site's JS can read/write any
site's per-site *state*, localStorage, etc.). That's acceptable because per-site state is
already public scratch (curl can write it; sites are versioned + backed up), and the
*login* — the thing that actually matters — is on a different origin. A site that needs
real isolation uses a custom domain.

---

## 2. Current architecture (as-is) — unchanged facts from v2

- Static serving is **nginx off disk**; the Go app (`:8090`) does only `/v1` API + the
  view-lock `auth_request` (`/internal/view-auth`) + the apex admin UI.
- DNS is Vercel-managed; `*.simple-host.app` is a **wildcard A + wildcard cert** (confirmed
  `DNS:*.simple-host.app`). So `sites.simple-host.app` needs **no new DNS/cert**.
- Disk: `/srv/simple-host/sites/<name>/current`, flat, keyed by **globally-unique** `sites.name`.
- **Everything is name-keyed** and must move to `site_id` (§5): all state/collections/
  allowed-origins queries, `DiskStorage.*`, `lockSite`/`uploadLocks`, `site_url`,
  `versions.disk_path`, `sweepExpiredSites`, the view-lock cache + cookie **name + HMAC
  payload**, `siteFromHost` (×3 handlers), and the handler-layer `viewSessionOK`/
  `authorizeStateOrigin`. (Authenticated `GetSiteByUser(user_id,name)` paths are already safe.)
- `db/schema.sql` is out of sync with code (missing `state`, `state_version`,
  `view_password_hash`, and the whole `collection_items` table) → `pg_dump` live first.
- ideaflow.page is the same binary, config-gated by `SITE_DOMAIN`/`DEPLOY_SCRIPT`; it must
  stay on the legacy per-site model (§11).

---

## 3. Username & user model

- **Username** = a path segment under `sites.simple-host.app`. Charset: lowercase
  `[a-z0-9-]`, 1–39 chars (git-style), unique. **Reserved list** must include our own
  top-level content-path words and anything that could collide with a system route if we
  ever add apex paths: `api, v1, u, user, users, admin, app, www, static, assets, internal,
  login, dashboard, account, settings, help, about, _`.
- **Assignment:** at first sign-in (lazy `CreateUser`), auto-assign from the email
  local-part (slugified, uniquified), **renameable** later. Reserved/local-part collisions
  fall back to `name-N`.
- **Rename:** updates the username; old username → 301 for a grace window (30 days) then
  released; disk is keyed by immutable `user_id` so a rename is a pure DB update + symlink
  swap (§6). Scrub `allowed_origins`/tombstone on release (takeover hygiene).
- **Home page** `sites.simple-host.app/<username>/`: open/public; v1 = a minimal
  auto-generated index of the user's sites (or 404 if none) — not locked.

### Auth (login) — CORRECTED (review round 4)
- Magic-link mechanics unchanged (`POST /v1/auth` → 6-digit code → `POST /v1/auth/verify`),
  **on `simple-host.app`**. "Forever login" is a **persistent `api_key` in `localStorage` on
  the dashboard origin** (`simple-host.app`) — exactly what the dashboard does today. Content
  JS on `sites.simple-host.app` is a **different origin** and cannot read it. **We do NOT add
  a session cookie.** Reason (Grok G-sec P0): `sites.simple-host.app` is *same-site* with
  `simple-host.app`, so a `Domain=`-scoped cookie would ride to the content host and
  `SameSite=Lax` wouldn't stop same-site cross-origin POSTs — a session cookie that authorized
  `/v1/*` would be a **CSRF landmine**. The load-bearing invariant stays: **management API =
  `X-API-Key` header only, CORS `*` with NO `Allow-Credentials`, no cookie auth.**
- **`username` column note:** `users.username` is *already the email login key* in code. The
  path handle must be a **new, separate column** (`handle`), not a second `username`. Reserved
  handles ⊇ `reservedSiteNames` + `sites, cname, u, healthz, readyz, comments, feedback, llms,
  openapi, install, www, api, admin, app, static, assets, internal`.

---

## 4. Provisioning — there is essentially none

- `sites.simple-host.app` resolves + is TLS-covered by the existing wildcard. **No per-user
  DNS, no per-user cert, no rate limit, no Vercel call.** A user is live the instant their
  DB row + on-disk symlink exist.
- The only per-user local step is a **symlink** `sites/handles/<username> → sites/by-id/<user_id>`
  (or a `map`), written transactionally at username assign/rename; a background reconciler
  repairs drift; admin `POST /v1/admin/users/{id}/relink` forces a repair. (No `add-site.sh`.)
- The only thing that still needs real per-name provisioning is **custom-domain TLS** (§8),
  where the Let's Encrypt limits and the fallback/admin-fix actually apply.

---

## 5. Routing & serving

`sites.simple-host.app` is one static server block; the Go app stays **out of the static
hot path** (nginx serves bytes off disk).

```nginx
server {
  listen 443 ssl;  server_name sites.simple-host.app;
  ssl_certificate /etc/letsencrypt/live/simple-host.app/fullchain.pem;   # wildcard, unchanged
  # /<username>/<sitename>/<rest>  ->  /srv/simple-host/sites/by-id/<user_id>/<sitename>/current/<rest>
  location ~ ^/(?<username>[a-z0-9-]{1,39})/(?<site>[a-z0-9-]{1,63})(?<rest>/.*)?$ {
    # username -> user_id via the symlink farm (no per-request app call, no reload):
    #   alias resolves through sites/handles/<username> -> by-id/<user_id>
    alias /srv/simple-host/sites/handles/$username/$site/current$rest;   # spike-proven pattern
    # view-lock: fail-open — auth_request ONLY for locked sites (see below), so an app
    # outage never 5xx's unlocked pages (preserves the "app off static path" uptime property)
  }
  location = / { return 404; }                 # bare content-subdomain root
  location ~ ^/(?<username>[a-z0-9-]{1,39})/?$ { … user home index (open) … }
}
```

- **username → user_id** is resolved by the **symlink farm** (`handles/<username>` →
  `by-id/<user_id>`), swapped atomically on rename. No nginx reload (the `simplehost`
  service user can't reload nginx under the systemd sandbox), no per-request app hop.
- **Site capture is charset-constrained** (`[a-z0-9-]`) so `..`/junk can't appear.
- **Fail-open view-locks (Grok G1):** nginx keeps a generated `map $… $locked` of only the
  currently-locked `user/site` paths; `auth_request /internal/view-auth` runs **only when
  `$locked`**. Unlocked pages are pure disk → an app outage doesn't take sites down. The map
  is regenerated on lock set/clear.
- **Relative-path requirement:** because sites live under `/<username>/<sitename>/`, absolute
  `/asset` links resolve to `sites.simple-host.app/asset` (wrong). Mitigations: (a) the deploy
  skill/llms.txt instruct **relative links only**; (b) a best-effort safety net — on serve,
  inject `<base href="/<username>/<sitename>/">` into `index.html` when we detect root-absolute
  links (opt-in; can break some pages, so guidance is primary).
- **Login/dashboard/API** stay on `simple-host.app` (existing `default_server` + admin UI),
  with `server_name` made **exact** so unknown hosts don't hit the admin UI (Grok G11).

---

## 6. Data model + re-key (adapted from v2, still required)

Usernames make site names **per-user**, so the same name→site_id re-key from v2 applies.

```sql
ALTER TABLE users ADD COLUMN username text UNIQUE, ADD COLUMN username_changed_at timestamptz;
ALTER TABLE sites DROP CONSTRAINT sites_name_key;              -- verify real name via \d sites
ALTER TABLE sites ADD CONSTRAINT sites_user_name UNIQUE (user_id, name);
ALTER TABLE sites ADD COLUMN custom_domain text UNIQUE, ADD COLUMN domain_status text,
  ADD COLUMN domain_verified_at timestamptz, ADD COLUMN domain_last_error text;
CREATE TABLE legacy_hostnames (hostname text PRIMARY KEY, user_id uuid, site_id uuid, created_at timestamptz DEFAULT now());
```

Re-key surface (complete list, from Grok G9/§completeness): all state/collections/
allowed-origins queries; `DiskStorage.WriteFiles/UpdateCurrent/DeleteSite`;
`lockSite`/`uploadLocks`; `site_url` writer; `versions.disk_path`; `sweepExpiredSites`;
`GetSite(name)`; the CAS existence probe; the view-lock cache + **cookie name + HMAC
payload**; the three `siteFromHost` handlers (resolve site from the **path** now); the
handler-layer `viewSessionOK`/`authorizeStateOrigin`. `pg_dump` the live schema (incl.
`collection_items`) into `schema.sql` before authoring migrations.

**View-locks** (retained per-site password feature): cache/cookie-name/HMAC keyed by
`site_id`; `viewAuth`/`viewLogin`/`viewLoginPage` resolve the site from `X-Original-URI`
(`/<username>/<site>/…`); `viewLogin` redirects back to the original URI (not `/`); the
login cookie is set on `sites.simple-host.app` (host-only), separate from the login session.

---

## 7. Security model + loophole register

**The win:** login session (on `simple-host.app`) is a different origin from user content
(on `sites.simple-host.app`), so user JS cannot read the session cookie or script the
dashboard. The management API is header-authed (`X-API-Key`) and CORS-scoped, so ambient
cookies don't authorize it.

**Accepted, documented loopholes:**
- **L0 — curl can read/write any site's state** (spoofed Origin). Pre-existing, accepted.
- **L1 — same-origin co-tenancy across ALL sites on `sites.simple-host.app`.** Any site's JS
  can read/write any other site's per-site *state*/collections, and read siblings'
  localStorage/IndexedDB on that origin. Accepted because state is public scratch + backed
  up; **a site needing isolation uses a custom domain** (its own origin). *Never* claim
  site-to-site isolation in product copy.

**DECISION (2026-07-11, Vineet): view-lock (password-protected private pages) is REMOVED for
normal path-hosted sites; it exists ONLY for a site bound to a custom domain** (its own origin,
where the lock is actually enforceable and the cookie is properly scoped). On the shared
`sites.simple-host.app` origin a "locked" page is not isolated from co-tenant JS (same origin →
iframe DOM read), so the promise can't be honestly kept. Big **simplification** — this DELETES:
the fail-open lock map (which Grok proved doesn't build on nginx 1.18, G1), all `auth_request`/
view-login wiring on the content subdomain, and every view-cookie SameSite/scoping headache on
the shared origin. The content subdomain becomes **pure static serving, app fully out of the
request path** (best uptime). `viewSessionOK` is always public for path sites; the
`view-password` API is rejected unless the site has an active `custom_domain`; `forward_auth`
lives ONLY on the custom-domain vhost (§8). Update `llms.txt` + `private-sites-spec` to say
privacy = custom domain, not a password on a normal page.

**Must-fix (remaining):**
- **Origin gate for per-site state:** the public state API accepts `Origin:
  https://sites.simple-host.app` (all sites) + the site's active `custom_domain` + owner
  `allowed_origins`. It **cannot** distinguish sites on the shared origin (that's L1). It
  MUST still reject the dashboard origin and arbitrary origins, require `https`, lowercased
  host, reject `Origin: null` (keep the `parsed.Host==""` reject).
- **Custom-domain view-lock + state:** a `yourbrand.com` page calling the state API is
  cross-site; serve the per-site state API **same-origin on the custom domain** (via the
  Caddy edge → app) so cookies/isolation work and view-locks hold (Grok G5/G7). Custom-domain
  vhost runs the same `forward_auth`; fail closed.
- **Compat shim** for legacy `<site>.simple-host.app` state: resolve only within
  `legacy_hostnames`; 409 on ambiguity after the constraint drop; never bare `WHERE name`.
- **Reserved usernames** ⊇ system words (§3); block on assign + rename.
- **Login cookie hardening:** `HttpOnly; Secure; SameSite=Lax`, `Domain=simple-host.app`
  (NOT `.simple-host.app` — must not be sent to legacy content subdomains). Long TTL is a
  product choice; rotate on demand; consider idle-absolute caps later.
- **Custom-domain TLS `ask` endpoint:** allow only `active` or `pending`+`dns_ok`
  (resolved CNAME to us); per-user domain cap; localhost-only; never serve an unbound host;
  reject `*.simple-host.app`/apex.

---

## 8. Custom domains + TLS topology

- **`:443` topology (Grok G2 — decide before Phase 1):** **Caddy is the sole TLS edge.**
  Caddy terminates 443/80 for everything; it reverse-proxies `simple-host.app` +
  `sites.simple-host.app` (+ legacy `*.simple-host.app`) to nginx/the app on localhost, and
  does **on-demand TLS** for custom domains gated by the ask endpoint. This removes the
  nginx-vs-Caddy :443 conflict and gives one place for ACME. (Alt: keep nginx on 443 for the
  wildcard + Caddy on a second IP for custom domains — more moving parts.) *Spike this (§16).*
- **Lifecycle:** `POST /v1/sites/{site}/domain {domain}` → normalize, store `pending` →
  user points **CNAME → `cname.simple-host.app`** (apex via A/ALIAS deferred to v2 of the
  feature) → `dns_ok` precheck → Caddy on-demand issues real LE cert → `active`. Removal
  clears it; reconciler + admin reprovision for stuck/failed.
- Custom domain serves the one bound site at its root, with the same view-lock `forward_auth`
  and a **same-origin** per-site state API.

### 8b. Custom-domain skill / agent flow (NEW deliverable — Vineet)
The feature is invisible unless an agent can drive it. Ship a **dedicated `connect-domain`
skill** and reference it from BOTH the **website-deploy** skill (the deploy-for-users flow)
and the **website-deploy-builder** skill (the meta/plugin builder), plus an `llms.txt` section
and the MCP server. The agent flow the skill teaches:
1. Deploy the site normally → live at `sites.simple-host.app/<username>/<site>`.
2. Bind: `POST /v1/sites/<site>/domain {"domain":"recipes.brand.com"}` (X-API-Key) →
   `{status:"pending", dns:{type:"CNAME", name:"recipes", value:"cname.simple-host.app"}}`.
3. Relay the exact DNS record to the human: **subdomain → CNAME `<label>` → `cname.simple-host.app`**
   (apex `brand.com` → A record, deferred to a later phase). The human's only job is adding it
   at their registrar.
4. Poll `GET /v1/sites/<site>/domain` until `status:"active"` (DNS verified + Caddy on-demand
   issued the LE cert). `DELETE …/domain` to unbind.
5. Confirm live at `https://recipes.brand.com` — the site's own origin (real isolation), and
   the **only** place password-privacy / view-lock is offered (per the §7 decision).
The website-deploy skill's copy must also state: **sites need relative links**, and **private
pages require a custom domain** (not a password on a normal path site).

### 8c. Agent-first custom-domain connection — incl. apex (synthesized: Grok + 2 independent designs + adversarial critique)
**Thesis:** a custom domain is an **agent-driven state machine with a few human consent gates**,
not a DNS docs page. The agent owns discovery, path selection, our-side writes, polling, and the
cert wait; the human owns **authority only** (one click / one approve / one explicit "yes").

**Default path (non-technical user):**
1. Server-side **probe** (source of truth): Public-Suffix-List apex-vs-subdomain (not dot-counting);
   **NS lookup → the DNS *operator*** (which is NOT necessarily the registrar — the #1 manual-setup
   trap); `_domainconnect` TXT for Domain Connect support; scan existing A/MX/TXT; **CAA precheck
   for `letsencrypt.org`** (fail fast).
2. **Bind `pending`**: mint a per-site TXT token; compute `desired_records`; **auto-include the
   `www`↔apex sibling** (apex canonical, `www`→apex 301 at the edge, one cert covers both).
3. If the DNS operator supports **Domain Connect** → default path: one browser approval at the
   provider; the automated tier sets the ownership TXT invisibly; poll → HTTP-01 → `active`
   (~30s human time). Apex on a DC provider → template writes A/AAAA → the floating edge IP (+`www`);
   prefer ALIAS/flattening if offered.

**Fallback ladder (probe-then-descend; BUILD IN THIS ORDER):**
`manual A/CNAME + floating IP + HTTP-01` (ship FIRST — universal floor, every domain works) →
the connect-domain skill + `next_action` state → **Domain Connect one-click** (default common case)
→ **Domain Connect OAuth** (best apex default when supported; scoped token, can't touch MX) →
**registrar-API / computer-use** (agent computes an ADD-ONLY diff, human commits the Save) →
**nameserver delegation + DNS-01** (opt-in only; most automated after consent, highest authority —
NOT the default: whole-zone blast radius, can silently kill the user's email).

**Consent model:**
- *Autonomous:* probe, bind pending, poll, explain, retry-verify, cert wait, set TXT via an
  automated tier. ("connect mybrand.com" implies consent to create the pending bind + DC URL.)
- *Explicit human gate — show the exact diff first:* change nameservers; registrar API/computer-use
  **Save**; grant an OAuth token; anything that would **overwrite/delete an existing record**.
- **Add-only invariant. MX/SPF/DKIM/DMARC is sacred — never touched.** Least-authority default
  (one-click > scoped token > delegation). The agent never asks for a registrar password in chat.

**Apex / single-IP:** never advertise the box's primary/ephemeral IP as an apex A target (mass
outage on rebuild); use a **floating/elastic edge IP** (reattach on failover, user's A never
changes); prefer **ALIAS/CNAME-flattening > raw A**; low TTL; delegation only on explicit opt-in
after showing an imported-record (MX/TXT) diff the human confirms.

**Verification & reconciler:** verified = account bind **+** DNS-points-at-us **+** per-site TXT
token (all three; TXT set invisibly by automated tiers, surfaced only in the manual tier).
`custom_domain` UNIQUE; contested domains escalate to **TXT-or-NS strong proof only**. A
**continuous reconciler** re-checks observed-vs-desired: drift → `dangling` → **stop serving** →
`suspended` (dangling DNS is a live takeover vector — ships WITH the feature). The reconciler also
owns **renewal-health** (cert expiring AND dns-drifted/CAA-blocked → proactively alert) and
**never silently re-pushes** a record the user changed by hand on the OAuth tier.

**Lifecycle edges the critique caught (must be in v1, both original designs missed these):**
- **`www`↔apex pairing** decided at bind time (default: bind both, apex canonical, edge 301).
- **Move/reassign** a domain between two sites of the same owner without downtime (preserve cert+TXT).
- **Account deletion** proactively **revokes the DC-OAuth grant at the provider** (not just deletes
  our row → else a live grant is orphaned); token-refresh failure degrades to re-consent, doesn't
  silently drop DNS management.
- **GDPR:** store only binding-necessary DNS; redact/expire `observed_dns` + RDAP PII; on delegation,
  don't retain the imported zone beyond the binding.
- **Manual-tier UX is a first-class deliverable** (the ~50% of users on non-DC registrars land here):
  per-provider screenshot-level steps, apex "no CNAME on root — use these A records" warning, and a
  live "I see it / still seeing the old value from resolver X" readout.

**Do-not-ship-until gate:**
1. Caddy `/ask` mints a cert **only for a verified DB binding** (blocks cert farming); rejects
   `*.simple-host.app`/apex reserved names.
2. **Floating/elastic edge IP** exists before any apex A record is documented.
3. **Add-only writes proven** — no code path can overwrite/delete a user record or touch MX/SPF/DKIM/DMARC.
4. **Server-side ACME rate-limit governor:** a GLOBAL token-bucket on the shared ACME account (LE
   limits are per-eTLD+1 **and** per-account — one bad retry loop or a signup burst can starve cert
   issuance for *all* tenants); honor `Retry-After`; staging-first for tests; per-user domain cap (5)
   + pending-bind expiry (7d) + per-IP/-/24 bind limits; consider sharding ACME accounts per tenant.
5. **Reconciler flips dangling → suspended and stops serving** (ships with the feature).
6. **CAA precheck fails fast**; the state machine surfaces CAA/DNSSEC/registrar-lock/nxdomain/
   cname-to-wrong-target instead of spinning to 72h.
7. **OAuth tokens** encrypted at rest, template-scoped, per-domain, revocable, never returned to the
   agent or written to chat logs; revoked at the provider on unbind **and** account deletion.

_(Full designs: `/tmp/grok-domain.md` (Grok) + the independent subagent design + critique — archive
into the repo if kept. This §8c is the synthesized, opinionated result.)_

---

## 9. Contract surfaces (all must change with the flip)

- **The embedded widgets** `comments.js`/`feedback.js` (highest traffic) derive the site
  from `host.split('.')[0]` — now that's `sites`, wrong. **Phase 0** must ship them parsing
  the site from `location.pathname` (`/<username>/<site>/`) and calling the new state path;
  bump `plugin.json` for `X-Skill-Version`.
- **The deploy skill(s) + `llms.txt`:** new flow (deploy under your username at a path; the
  URL is `sites.simple-host.app/<username>/<site>`); **relative-links rule**; new state/
  collections snippet path; custom-domain step.
- **OpenAPI** (`site_url` shape, new `/v1/u/<username>/sites/<site>/state` public path,
  username + domain endpoints), **admin UI** `index.html` URL derivation, **README**,
  and the **MCP server**, and `check-docs-sync.sh`.
- **The public marketing/docs pages** must be rewritten to the new model — **`architecture.html`**
  (the "How Simple Host works" page: path hosting, the two origins, custom domains, view-lock =
  custom-domain-only, the public database), **`docs.html`**, **`install.html`**, **`privacy.html`**.
  (Vineet: update the architecture page once everything is built.)

---

## 10. Migration & backward-compat

- Backfill **usernames** for all users; backfill `legacy_hostnames`
  (`INSERT SELECT name||'.simple-host.app', user_id, id FROM sites`).
- **Legacy `<site>.simple-host.app`** keeps serving (deprecated) via the existing regex
  block; optionally 301 → the new path. Old state clients keep working via the hardened
  compat shim during a deprecation window.
- **Disk:** re-key the write path to `by-id/<user_id>/<name>/` and ship it **before**
  dropping `UNIQUE(name)` (Grok G3 — never lazy-migrate after the drop). Legacy flat dirs
  dual-read via `by-id` symlinks.
- **ideaflow** stays on the legacy per-site model, gated by `ADDRESSING_MODE=per-site`
  (controls uniqueness, URL minting, disk layout, Origin formula, username requirement);
  boot-fail if `per-user` + `DEPLOY_SCRIPT` (Grok G12).

---

## 11. Rollout phases (0a→0e, re-key first / drop-unique last)

- **0a** internal re-key to `site_id` (behaviour-identical; names still globally unique; CI grep-gate).
- **0b** dual-Origin gate (legacy host + new `sites.simple-host.app`) + `legacy_hostnames` backfill.
- **0c** disk re-key + symlink farm + the `sites.simple-host.app` nginx block (fail-open lock
  map, relative-path base-tag net) + username backfill/assignment.
- **0d** new `/v1/u/<username>/sites/<site>/state|collections` + **widgets/openapi/skills/
  llms.txt** + admin UI URL derivation (contracts ship WITH the flip).
- **0e** DROP `UNIQUE(name)` → `UNIQUE(user_id,name)` (simple-host.app only) + `site_url` backfill.
- **1** Caddy sole-edge topology + custom domains (on-demand TLS, ask, forward_auth, same-origin state).
- **2** polish (home-index, rename UI, apex custom domains, deprecation of legacy subdomains).

---

## 12. Effort

- Phase 0 (0a–0e): **~1.5–2.5 weeks** — dominated by the `site_id` re-key (incl. disk/locks/
  site_url) + widget/contract updates + the nginx/Caddy edge. Simpler than v2 (no per-user
  DNS/handle machinery) but the re-key surface is unchanged.
- Phase 1 (custom domains + Caddy edge): **~4–6 days**. Phase 2: ~1 week.
Total ~**3 weeks**. Biggest risk: the re-key/compat sequencing (as v2) + the Caddy-sole-edge
cutover (new).

---

## 13. Open questions
Essentially none — Vineet decided the model. Remaining implementation choices (Caddy sole
edge vs second IP; base-tag safety net on/off; exact reserved-username list) are mine to
make and validate in the spike/review loop.

---

## 14. Prior review log (v2, still applicable)
The v2 adversarial rounds (2 subagents + Grok ×3) are archived in git history / the v2 draft;
their still-relevant findings (re-key completeness, disk-before-drop ordering, fail-open
locks G1, dual-:443 G2, Caddy-bypasses-view-lock G5, SameSite custom-domain state G7, widget
contracts G8, ideaflow gating G12, schema drift, reserved names) are **incorporated above**.
v3 *removes* the v2 findings that were specific to per-user subdomains (handle provisioning,
dual-server-block collision G10, handle-as-DNS-label). The scariest v2 issue — same-origin
co-tenancy incl. the login session — is **structurally fixed** by putting content on a
separate origin from the dashboard; the residual (site-to-site co-tenancy on the content
origin) is the accepted L1.

## 15. Spike (v3) — DONE (isolated on the workspace box, torn down)
- **Path routing:** `sites.<box>/alice/blog/` vs `/bob/blog/` served different users' content
  via the symlink farm through nginx (per-user path namespacing off one content subdomain). ✅
- **Caddy sole edge:** one Caddy on `:443` terminated TLS and reverse-proxied `sites.*` → nginx
  and `simplehost.*` → a dashboard stub (proving the login/content split), AND did on-demand TLS
  for a custom host → its bound site; a disallowed custom host was **refused** at handshake. ✅
- **Relative-path proof:** an absolute `/style.css` → **404** (resolved to the content root),
  while the relative `style.css` worked — concretely validating §16's contract-break warning.
(This proves the topology; the *ops cutover* — X-Forwarded trust, ACME ownership, view-auth on
Caddy, legacy wildcard vs on-demand — is the real Phase-1 spike, TBD on a throwaway public IP.)

## 16. The database-saving backend feature under v3 (state + collections)
The per-site JSON **state** + append-only **collections** stores are the platform's flagship
"little backend." How they change under v3 (from the dedicated backend review + Grok):

- **They break on day one of the path flip unless shipped together.** `comments.js`/`feedback.js`/
  `llms.txt`/starter-templates all derive the site from `host.split('.')[0]`; on
  `sites.simple-host.app` that is the literal string `"sites"` → every widget POSTs to one wrong
  backend. **Fix (Phase 0d, with the flip):** derive `(handle, site)` from `location.pathname`
  (`/<handle>/<site>/…`) and call the new route; keep a legacy-subdomain branch; bump `plugin.json`.
- **Serve the public state/collections API SAME-ORIGIN on `sites.simple-host.app`** (edge routes
  `sites…/v1/u/<handle>/sites/<site>/state` → app). Same-origin ⇒ no CORS preflight AND the
  view-lock cookie actually flows (a host-only cookie won't reach an apex API under SameSite).
- **The Origin gate degenerates to "is this the content origin".** All sites share
  `https://sites.simple-host.app`, so the gate can't tell sites apart — **any co-tenant page can
  read/overwrite/`removeWhere`-clear any other site's state/collections** (this is L1). Accepted
  because state is public scratch, versioned + backed up. The gate must still reject the
  dashboard origin, require `https`, lowercase the host, and reject `Origin: null`.
- **View-lock ≠ privacy on the shared origin — and `llms.txt` currently claims it IS private.**
  A co-tenant page can hit an unlocked-in-this-browser locked site's state with the ambient
  `shview_*` cookie (no iframe needed), and can iframe + read its DOM. **Must-fix:** correct
  `llms.txt`'s "private" claim; **real per-site privacy = a custom domain** (own origin, same-origin
  state, cookie scoped to it). Re-key the view cookie **name + HMAC payload + lock cache** to
  `site_id`, and scope the view cookie `Path=/<handle>/<site>/` so co-tenants don't receive it.
- **The store engine itself is sound** — the row-locked atomic PATCH (`set/inc/append/remove/
  removeWhere`, `SELECT FOR UPDATE`) + opt-in `If-Match` CAS + ETag/304 is well-built; keep it.
  Small hardening: add a **max path-depth cap** in PATCH ops (deep `a.b.c…` auto-vivifies maps),
  and consider a **per-site write quota** (the per-IP limiter doesn't bound cross-site vandalism
  under L1). Re-key the name-keyed state/collection queries to `site_id` before the UNIQUE drop.

## 17. Round 4 adversarial review — Grok ×3 (auth/routing/migration) + a dedicated backend review
All four converged; corrections above (§0 note, §3, §16) reflect them. **Do-not-ship gate:**
1. **Domain frozen** `simple-host.app` (hyphen); content = `sites.simple-host.app` (wildcard-
   covered ✓). `simplehost.app` (no hyphen) is a *different* domain (Porkbun→AWS) — don't mix.
2. **No auth cookie.** Header-auth invariant preserved; "forever login" = localStorage on the
   dashboard origin (§3). A `Domain=` session cookie would be a same-site CSRF landmine.
3. **Same-origin state/collections on the content host** (and custom domains) so view-locks work
   and no preflight (§16).
4. **New `handle` column ≠ the email `username`** column (§3).
5. **Full `site_id` re-key** (exact code locations enumerated by Grok: `queries.go` state/CAS/
   `LoadViewLocks`, `collections.go` ×2, `originHostForSite`, `site_url` writer, `versions.disk_path`,
   `disk.go` path key, `sweepExpiredSites`, `lockSite`, view cookie name+HMAC+cache) + CI grep-gate
   for `sites WHERE name`; drop `UNIQUE(name)` LAST.
6. **`ADDRESSING_MODE` implemented** (doesn't exist yet); ideaflow stays per-site/flat-disk/
   subdomain/deploy-queue; boot-fail `per-user`+`DEPLOY_SCRIPT`.
7. **Contracts ship WITH the flip:** widgets + starter templates + both skills + MCP + OpenAPI +
   dashboard URL fallback + `site_url` mint change.
8. **`pg_dump` → `schema.sql` first** (missing `state`, `state_version`, `view_password_hash`,
   `collection_items`).
9. **View-lock edge is NOT in the repo** (`vps-setup` nginx has no `auth_request`) — write it; and
   the **fail-open lock map with variable `auth_request` does NOT build on nginx 1.18** (Grok
   spiked it → 500). Real options: always-on `auth_request` (app-down ⇒ *locked* pages 5xx, unlocked
   cheap), OR a privileged reload of an include of only-locked `location` blocks, OR an njs/sidecar/
   marker-file check. **Must spike on the prod nginx binary before Phase 0c.**
10. **RELATIVE PATHS are a contract break, not guidance (biggest product consequence):** the
    current skill teaches root-absolute `/asset` links "work as-is," and Vite/Next/CRA/SvelteKit +
    most AI HTML emit `/assets/…`. Under path serving these **break**. `<base>` injection is a
    partial, dangerous bandage (doesn't fix JS `fetch('/…')`, routers, service workers; and pulls
    mutation into the serve path). **Fix:** set framework `base`/`homepage`/`assetPrefix` in the
    skill, and **reject/rewrite root-absolute paths at upload time**. Custom domains + legacy
    subdomains (served at root) don't have this problem.
11. **Caddy sole-edge is a real cutover** (X-Forwarded trust, ACME single-owner, legacy wildcard
    vs on-demand, HSTS, view-auth Host/path) — spike on a throwaway IP; rollback = DNS/IP flip.

**Net:** the v3 direction is right and *simpler+safer* than v2 (no per-user DNS; login origin
structurally separated from content), and the store engine is solid. The remaining work is
well-understood but larger than a first estimate — dominated by the `site_id` re-key, the
contract/widget updates, the view-lock edge + fail-open spike, and the relative-path enforcement.
