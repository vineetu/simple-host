# SPEC: Visitor Google/GitHub sign-in, then every write requires it

**2026-09-05 — Owner decision:** Added `GET /v1/sites/{sitename}/me` and
`GET /v1/u/{handle}/sites/{sitename}/me`, superseding section 1.5's rejection
of a session endpoint. These Origin-gated reads report the current site session
without extending it. `/auth.js` is the hosted `window.SH` auth and storage helper. Review (Codex, Grok, same day): on the shared content host `/me` returns only `signed_in` and `expires_at`; `email`/`provider` are sent only on a custom domain, because co-tenant pages share the origin and the host-only cookie and a Referer/path check is forgeable via `history.replaceState`.


Status: implementable  
Date: 2026-08-14  
Audience: an implementer working in this worktree who will not be asked questions  
Product decision (settled): every `PUT`/`PATCH` `/state` and every `POST` `/collections/{coll}` requires a signed-in visitor. Not per-site opt-in. This document is how to ship that without lying to guests or opening a redirect/session hole.

Do not implement owner/admin OAuth. Do not put visitors in `users`. Implement against this file, not the three source designs.

---

## 1. Verdicts on load-bearing claims

### 1.1 `session.md`: sites are same-origin with the API via nginx `/v1/` proxy

**Confirmed** for the content host. **Confirmed as the intended custom-domain shape**, with one naming nit.

`deploy/prod/nginx-sites-content-host.conf` lines 14–22:

```nginx
# API + internal endpoints proxy to the app (same-origin state/collections).
# ^~ so these win over the regex path-serving locations below.
location ^~ /v1/ {
    proxy_pass http://127.0.0.1:8090;
    proxy_set_header Host $host;
    ...
}
```

`server_name` is `sites.simple-host.app` (line 9). A page at `https://sites.simple-host.app/{handle}/{site}/` calling `/v1/...` is same-origin. A `__Host-` cookie set on that host is sent. There is no cross-origin problem for the path model.

Custom domains: `deploy/prod/nginx-customdomain.example.conf` lines 2–16 is an **example** file, not a live `server` block, but it is explicit:

```nginx
# Same-origin state API.
location ^~ /v1/ {
    proxy_pass http://127.0.0.1:8090;
    proxy_set_header Host $host;
    ...
}
```

The other committed custom-domain path is Caddy on-demand TLS (`deploy/Caddyfile.v3-content-host.example` lines 33–41; `tlsAsk` in `internal/handler/domains.go:289–331`). It also reverse-proxies `/v1/*` to `:8090`. **Same-origin `/v1/` is the design for every host we actually serve.**

`session.md` is therefore right that a host-only cookie on the host the page already calls works. The cookie model does **not** collapse.

What `session.md` slightly overclaims: production custom domains are not proven to be that nginx file. They are Caddy-or-nginx, both proxying `/v1/`. Implement against the same-origin invariant, not against a specific nginx filename.

### 1.2 `session.md`: `comments.js` uses `location.origin`, not the apex

**Confirmed**, and the same is true of `feedback.js`.

`internal/handler/static/comments.js:54–60`:

```javascript
if (host.split(".")[0] === "sites" && m) {
  API = location.origin + "/v1/u/" + m[1] + "/sites/" + m[2] + "/state";
} else {
  var sub = host.split(".")[0];
  API = location.origin + "/v1/sites/" + sub + "/state";
}
```

`feedback.js:60–66` is the same derivation. Backend-anywhere (`SH_COMMENTS.base`, default `https://simple-host.app`, lines 36–45) is the only apex call, and only when the embedder set `site`.

### 1.3 `blast.md`: widgets write `/state` (not collections) and already send `credentials: "include"`

**Confirmed.**

- Comments: `PATCH` `{ops:[{op:"append", path:"_comments", ...}]}` and `{op:"inc", path:"_votes.<id>"}` — `comments.js:146–158`.
- Feedback: `PATCH` append to `_comments` — `feedback.js:240–246`.
- Both: `credentials: "include"` on GET and PATCH (`comments.js:136, 141, 147`; `feedback.js:228, 235, 243`).
- Zero `collection` usage in either file.

`blast.md` is also right that widget adoption is invisible in `collection_items`.

### 1.4 `oauth.md`: `docs/designs/oauth2-authentication.md` is owner-OAuth prior art and is not implemented

**Confirmed. Do not follow that doc. Do not assume a half-built provider layer.**

The 2026-06-25 doc (`docs/designs/oauth2-authentication.md:10–16, 35–37`) signs **owners** into the admin UI and yields a `users.api_key`. It wants `golang.org/x/oauth2` **plus** `go-oidc`, account linking on verified email, and `oauth_identities` rows that point at `users`.

In this tree:

- `go.mod` is `lib/pq`, `x/crypto`, `x/net`. No `x/oauth2`, no `go-oidc`.
- Grep of `**/*.go` finds no OAuth client, no `/v1/auth/oauth` handler, no `oauth_states` table.
- The only `oauth_*` strings live inside that design doc.

`oauth.md` is also right that the owner doc is stale on current auth: `UserHandler.requestSignIn` no longer pre-creates the user (`internal/handler/user.go:96–100`); `verifySignIn` lazy-creates. `CLAUDE.md` is stale on admin identity: `EnsureAdminUser` inserts a real `users` row (`internal/db/collections.go:11–24`) and `auth.Middleware` attaches that UUID (`internal/auth/middleware.go:33–66`), not `ID:"admin"`.

Implement visitor OAuth from this spec, not from `docs/designs/oauth2-authentication.md`.

### 1.5 Sign-in URL: `oauth.md` and `blast.md` disagree

**Pick the OAuth routes. Reject `/v1/visitor/session`.**

| Designer | URL | Fate |
|---|---|---|
| `oauth.md` | `GET /v1/auth/oauth/{provider}` start, `.../callback` | **Keep.** Matches the owner-doc prefix, is a real OAuth start, is what Google/GitHub register as `redirect_uri`. |
| `blast.md` | `sign_in: "/v1/visitor/session"` | **Reject as a route.** It is a placeholder for an HTML session page that does not exist and is v1 scope creep. |
| `session.md` | `GET /v1/visitor/establish` | **Keep.** That is the cookie-issuance hop on the content host / custom domain, not the IdP start. |

Error-body `sign_in` (the field widgets branch on) is the **providers discovery** URL, so pages never hardcode a provider:

`sign_in` = `/v1/auth/oauth/providers`

Widgets fetch that, then navigate to `{PUBLIC_BASE_URL}/v1/auth/oauth/{name}?return_to=...`.

### 1.6 Other convenient claims, checked

| Claim | Verdict |
|---|---|
| `authorizeStateOrigin` accepts `parsed.Host == contentHost` for **every** site, so co-tenant pages already share an Origin (`session.md`) | **Confirmed.** `internal/handler/site.go:424–437`. README line 68 ("only a site's own subdomain can read or write") is leftover from the legacy `<name>.<siteDomain>` model and is false today. |
| `SetDomainStatus` is dead; `domain_status = 'active'` never happens (`oauth.md`) | **Confirmed.** `SetDomainStatus` is defined in `internal/db/domains.go:116–128` and has **zero callers**. `bindDomain` writes `pending` (`domains.go:27–28`, `handler/domains.go:191`). `originIsBoundDomainID` (`site.go:376–381`) does **not** check status. Requiring `active` for `return_to` would reject every custom domain. |
| Starter templates already call same-origin `/v1/` like the widgets (`implied` if you only read `session.md`'s happy path) | **Refuted.** `event-rsvp`, `waitlist`, and `landing` build `BASE = ${protocol}//${apex}/v1/sites/${sub}` (`event-rsvp/index.html:522–524`, `waitlist/index.html:342–344`, `landing/index.html:367–369`). That is **cross-origin to the apex**. A host-only cookie on `sites.simple-host.app` is **not sent**. `session.md` does call this out later; treat it as a hard v1 fix, not a footnote. |
| `llms.txt` never checks `res.ok` (`blast.md`) | **Confirmed.** `internal/handler/static/llms.txt:17–19`. Generated pages will hide the form after a 401. |
| RSVP template also silently confirms on 401 (`blast.md` row 2) | **Refuted.** `event-rsvp/index.html:595–611, 634–643` throws on non-OK POST **and** non-OK PATCH, and only then `paintThanks`. A 401 shows an error. The remaining RSVP bug is **partial write + retry-duplicates**, not silent success. |
| Waitlist / landing ignore PATCH failure (`blast.md` implied) | **Confirmed.** Both check POST, then `await fetch(PATCH)` with no `ok` check, then mark success (`waitlist/index.html:417–432`, `landing/index.html:420–434`). |
| `generate.go` emits anonymous writes (`blast.md` row 10) | **Mostly refuted.** `generateSystemPrompt` (`generate.go:469–487`) allows the hosted `comments.js` widget and does **not** teach `POST /collections` or `PATCH /state`. Update it only so it does not tell the model that writes are anonymous. |
| Bundled MCP writes state/collections (`blast.md` row 16) | **Refuted for the bundled server.** `simple-host-website/mcp-server/src/index.ts` uses `X-API-Key` for deploy/list, not state. Owner `curl -H Origin:… -X PATCH` still exists and **must** keep working via `X-API-Key`. |
| Owner-doc "CORS preflight is broken at the proxy" (`oauth2-authentication.md:75–80`; `session.md` Q7) | **Refuted for `/v1/` on the content host.** nginx `location ^~ /v1/` proxies everything, including `OPTIONS`. `optionsSiteState` / `optionsCollection` handle preflight in-process (`site.go:450–467`, `collections.go:120–129`). Widgets already preflight today (`Content-Type: application/json` on PATCH). Adding `X-SH-CSRF` does not newly invent preflight. |
| `db/schema.sql` is behind live (`session.md`) | **Confirmed.** No `collection_items`, no `sites.state`, no `view_password_hash`. Live writes go through `internal/db/queries.go` / `collections.go`. New tables still go in `schema.sql` **and** a hand-applied migration file, matching `db/migrations/analytics.sql`. |
| `blast.md` live counts (53 sites, 19 collection rows) | **Not re-queried.** This review did not touch Postgres. Treat those numbers as directional evidence that the anonymous-write pattern is live, not as a gate on implementation. |
| Apex `CORS` is `*` without credentials (`session.md`) | **Confirmed.** `internal/handler/cors.go:13–33`. State/collections are excluded and run `authorizeStateOrigin`. A visitor cookie on `simple-host.app` would be same-site with `sites.simple-host.app` and would break the v3 "management API is header-auth only" invariant (`docs/designs/per-user-subdomains-and-custom-domains.md` §0–§3). **No visitor cookie on the apex.** |

---

## 2. Attacks on the merged design, and the fixes

### 2.1 Open redirect via `return_to` — real hole if you only "allow-list a real site"

`oauth.md`'s `sanitizeReturnTo` is the right *shape* (parse, store, callback never rereads the query string) and is **not** sufficient as written.

What it checks: scheme, no userinfo, host is `ContentHost` with a real `/{handle}/{sitename}` **or** a `sites.custom_domain` with `domain_status = 'active'`.

Holes:

1. **`domain_status = 'active'` matches nothing in production** (claim 1.6). Custom-domain sign-in would 400 for everyone, or a later engineer would "fix" it by accepting any bound domain.
2. **Any bound domain, no proof of control, is an open redirect.** `bindDomain` is a UNIQUE insert (`handler/domains.go:174–191`). Anyone can bind `paypal.com` while it is free. A 302 from `https://simple-host.app/v1/auth/oauth/google/callback` to `https://paypal.com/...` is phishing via our origin. `tlsAsk` allowing a cert is irrelevant — the 302 never hits our box.
3. **Content-host `return_to` to a *different* real site** (`https://sites.simple-host.app/attacker/phish/`) is not an open redirect off-platform, but combined with a host-wide cookie it is a **login-CSRF / session handoff** onto the attacker's page (see 2.3).
4. Path tricks (`/alice/blog/../../../`, `%2e%2e`, `//evil`, userinfo, scheme-relative). `oauth.md` already requires `url.Parse`, `User == nil`, `path.Clean` keeping the `/{handle}/{sitename}` prefix. Keep those. Store `parsed.String()`, not the raw query value.

**Fix (this is the rule, not an option):**

- Callback 302s **only** to  
  `https://<return-host>/v1/visitor/establish?once=<token>`  
  where `<return-host>` is the host of the **already-stored** `return_to`. Never to `return_to` itself from the apex. Apex sets **no** visitor cookie.
- `sanitizeReturnTo` at **start** (before insert). Callback does not re-parse caller input.
- Content-host `return_to`: scheme `https` (or `http` only when `PUBLIC_BASE_URL` itself is `http`), host **exact** `cfg.ContentHost`, no userinfo, no port other than the scheme default, path `/{handle}/{sitename}` or deeper, `path.Clean("/"+path)` still has that prefix, handle `^[a-z0-9-]{1,39}$` (`showcase.go:20`), sitename `^[a-z0-9-]{1,63}$` (nginx content-host regex), and `(handle, sitename)` resolves through `GetUserByHandle` + `GetSiteByUser` to a real `site_id`. That `site_id` is stored on `oauth_states`.
- Custom-domain `return_to`: host **exact** equals `sites.custom_domain` for one row (case-insensitive), **and** a live DNS check at start time proves control:
  - If `cfg.CustomDomainIP != ""` and the host is an apex (existing `isApexDomain` in `domains.go:85–90`): `net.LookupIP` contains that IPv4.
  - Otherwise: CNAME chain ends at `cfg.CNAMETarget` (trim trailing dots, case-insensitive), **or** an A record equals `cfg.CustomDomainIP` when set.
  - Fail closed (400, no flow) on lookup error, NXDOMAIN, or mismatch.
- Reject: apex (`cfg.SiteDomain`, `www.`, host of `PUBLIC_BASE_URL`), legacy `<name>.<siteDomain>`, any `allowed_origins` entry, pending binds that fail DNS, unknown hosts.
- Do **not** wait for `SetDomainStatus`. Do **not** implement domain-status promotion in this project.

A visitor-supplied redirect that lands on an attacker page **with a session cookie already valid for the victim's intended site** is the takeover. Site-scoping the session (2.3) plus the establish hop (cookie only set by **our** `/v1/visitor/establish` on a host we serve) is what stops it.

### 2.2 Session fixation and CSRF — real hole the moment the cookie authorizes writes

Today the accidental CSRF defense is "no ambient credential" plus `authorizeStateOrigin` (`site.go:395–447`). View cookies (`shview_*`, `viewauth.go:136–141`) authorize viewing a locked site, not writing as a person.

The moment `__Host-sh_vsess` authorizes writes:

- Cross-site POST from another eTLD+1 can ride `SameSite=Lax` only on top-level navigations, not on `fetch`. Still require more.
- `evil.com` is already stopped by `authorizeStateOrigin` (not `contentHost`, not bound domain, not `allowed_origins`) → 403 `{error:"forbidden"}` (`collections.go:145`).
- Co-tenant JS on `sites.simple-host.app` **can** set any header and send the cookie. That is the shared-origin tradeoff (`per-user-subdomains-and-custom-domains.md` §1). Site-scoping the session (2.3) is what stops them writing to *another* site.

**Fix (all required):**

1. **Keep `authorizeStateOrigin` on every state/collections method, including GET.** Defense in depth, CORS `ACAO` reflection, `allowed_origins`, custom-domain binding. Do not drop it. Do not make it a fallback that lets unsigned writes through.
2. **Require `X-SH-CSRF: 1` on every write that is authenticated by the visitor cookie.** Constant header, not a synchronized token (a JS-readable token is stealable by co-tenants on the shared origin). Simple HTML form posts cannot set it. `optionsSiteState` / `optionsCollection` must list it. Owner `X-API-Key` writes do **not** need it (no ambient cookie).
3. **`SameSite=Lax`** on the cookie. Blocks cross-site POST from other eTLD+1s.
4. **Never set the visitor cookie on the apex.** Content-host JS is same-site with `simple-host.app`; Lax would send it to the management API. `cors.go` is `*` without credentials today because there is no cookie to ride.
5. **New session row at OAuth success.** Do not accept a caller-supplied session id. `establish` consumes a one-time server-minted handle bound to that row.
6. **No side effects on GET** except `GET /v1/visitor/establish` (one-time, 60s, host-checked, then 302).

If someone later weakens the Origin check, (2)+(3) still hold. If someone later drops `X-SH-CSRF`, (1)+(3) still hold. Do not ship with only one of them.

### 2.3 Cookie scope leakage — pairwise does **not** stop acting-as; cut pairwise, site-scope the session

Many sites share `sites.simple-host.app`. `__Host-` forbids `Domain=` but still shares the cookie across **all paths** on that host. Site A's script can `fetch('/v1/u/bob/sites/other/state', {credentials:'include', headers:{'X-SH-CSRF':'1'}})` and the cookie goes. `session.md` admits this (`Cross-site leakage` / "What we do not prevent").

Pairwise HMAC ids (`HMAC-SHA256(viewSecret, visitor||site)`) stop site A from **joining** "this guestbook signer is that other site's signer" via a stable identifier. They do **not** stop site A from **acting as** the signed-in visitor on site B, and they do not stop A from reading B's public state (already public). Pairwise is identity *obscurity*, not a write boundary.

Pairwise plus a `/visitor` endpoint plus an `actor` column is gold-plating for a v1 whose product requirement is "signed in to write," not "attributed authors." Cut it.

**Fix:** `visitor_sessions.site_id` is NOT NULL. `visitorWriteOK` accepts the cookie only when `session.site_id` equals the target site **and** `session.host` equals the request host (port stripped). Signing into Alice's guestbook does not authorize writes to Bob's. Signing in on the path-model URL does not authorize writes on the custom domain (different `host`; a second establish is required). One cookie name on the host: signing into site B replaces the cookie; site A's row is orphaned until sweep. Accepted.

Custom domain remains the origin-isolation product. Path-model sites share an origin; they do not share a write-capable session.

### 2.4 Partial writes — real, client-side, do not invent a server transaction

RSVP / waitlist / landing / `llms.txt` do `POST collections` then `PATCH state`. These are two resources, two handlers, two rate-limit tokens (`site.go:264–280`). There is no cross-resource transaction today and this project will not add one.

Under enforcement, an **unsigned** browser fails the POST first (401). `event-rsvp` then shows an error (good). `llms.txt` still hides the form (bad; fix the teacher). Waitlist/landing throw on POST 401 (good).

The actual partial-write cases:

- POST 201, PATCH 401 (session vanished, or PATCH omitted `X-SH-CSRF`, or waitlist/landing ignore PATCH `ok`). Guest exists, headcount did not increment.
- Client retries the whole submit → **duplicate collection row**.

**Fix:**

- Server: both writes go through the same `visitorWriteOK`. No new combined endpoint.
- First-party templates: if POST is 2xx and PATCH is not, show "saved, count will update" and **do not re-POST**. Retry PATCH only.
- `llms.txt` / skills: same rule, plus a 401 branch that does not hide the form.
- Hosted widgets only PATCH, so they have no two-step partial write.

### 2.5 `WRITE_AUTH_MODE=off|log|on` — keep it; `log` is the right **unset** default; `on` is an operator flip

A same-day 401 on `llms.txt`-shaped pages is a silent RSVP outage (`llms.txt:17–19` never reads `ok`). First-party widgets at least `alert` (`comments.js:148`). Already-deployed tarballs do not hot-reload.

`log` first is not a product retreat from "everywhere." It is how you ship everywhere without discarding wedding RSVPs into a thank-you page. Rollback is the env var; no data migration to undo.

`allow_anonymous_writes` as an **owner-facing product flag** is a back-door opt-out the owner already rejected. Cut the API/UI. Keep a boolean column defaulting false, settable only with `ADMIN_API_KEY` (or hand SQL), so one remaining wedding is not a global `WRITE_AUTH_MODE=log` rollback.

Unset `WRITE_AUTH_MODE` = `log`. The source default is never `on`. Production enforcement is `WRITE_AUTH_MODE=on` in the unit file after the checklist in §10 step 6.

---

## 3. Scope cuts

| Item | v1 | Reason |
|---|---|---|
| Pairwise per-site visitor ids | **CUT** | Obscures a join key we will not expose; acting-as is stopped by site-scoped sessions. |
| `GET .../visitor` | **CUT** | Only existed to hand out pairwise ids. |
| `actor` column / attested author | **CUT** | Attribution is a later product; v1 is a write gate. |
| Storing provider email | **CUT** | Not a lookup key; wedding-RSVP PII we do not need; drop email scopes. |
| Account linking across providers | **CUT** | Same human, two providers, two `visitors` rows. Silent email-merge is the takeover `oauth.md` correctly refuses. |
| Per-site `allow_anonymous_writes` as an owner product | **CUT** | Conflicts with "everywhere." |
| `allow_anonymous_writes` column + admin-only setter | **KEEP** | Operator escape hatch so one site is not a global rollback. Default false. |
| PKCE S256 | **KEEP** | One column + two `x/oauth2` helpers; RFC 9700; stops authorization-code injection. |
| Disk census of `DATA_DIR` | **CUT** from code | Operator grep, not a PR. Do it before flipping `on` if you have prod disk. |
| Session revocation UI | **CUT** | `POST /v1/visitor/logout` for the current cookie is enough. |
| Backend-anywhere bearer / `SameSite=None` apex cookie | **CUT** | GitHub Pages writes 401 until a later design. Do not punch a cross-site cookie hole. |
| Owner Google/GitHub login | **CUT** | Different principal, different table, different linking policy. Magic-link stays the only owner path. |
| `go-oidc` / `id_token` verification | **CUT** | Identify via HTTPS userinfo. |
| Popup OAuth | **CUT** | Full-page 302. Widgets redirect `location.href` and retry on return. |
| Magic-link / email-code visitors | **CUT** | Google and GitHub only. |
| Automatic override expiry | **CUT** | |
| `noticeMW` on state/collections/OAuth | **CUT** (keep off) | Browser pages parse the body as the document (`site.go:225–228`). |
| Dedicated `VISITOR_PAIRWISE_SECRET` | **CUT** | Pairwise is cut. |
| Rewriting third-party deployed HTML | **CUT** | Cannot. `log` window + owner mail + hosted-widget update is the mitigation. |

---

## 4. Merged design

### 4.1 Principals

| Principal | Table | Credential | Writes state/collections? |
|---|---|---|---|
| Owner / admin | `users` | `X-API-Key` (`ADMIN_API_KEY` short-circuits to the real admin UUID from `EnsureAdminUser`) | **Yes**, if the key is the site owner or admin. Still Origin-gated. No CSRF header. No visitor row. |
| Visitor | `visitors` | `__Host-sh_vsess` (or `sh_vsess` on local HTTP) | **Yes**, if session row is unexpired, `host` matches, `site_id` matches, and `X-SH-CSRF: 1` is present. |
| Nobody | — | spoofable `Origin` only | **Read** public state/collections if Origin/Referer passes (and view-lock). **Not write** when `WRITE_AUTH_MODE=on`. |

Visitors are not `users`. They get no `api_key`, no handle, no showcase. `auth.Middleware` is untouched and is **not** wrapped around state/collections.

### 4.2 Sign-in flow (path-model)

1. Page on `https://sites.simple-host.app/{handle}/{name}/…` shows "Sign in" after a 401 or up front. That control is an ordinary top-level navigation (no `fetch`):  
   `https://simple-host.app/v1/auth/oauth/google?return_to=https%3A%2F%2Fsites.simple-host.app%2Falice%2Fblog%2F`  
   (provider from `/v1/auth/oauth/providers`).
2. `GET` start on the **apex**. Public. Rate-limited. Unknown/disabled provider → 404. `sanitizeReturnTo` or 400. Insert `oauth_states` (32-byte hex `state`, PKCE verifier, validated `return_to`, `site_id`, `host`, `expires_at = now()+10m`). Prune expired states in the same request. 302 to the provider `AuthCodeURL` with `state`, `redirect_uri = {PUBLIC_BASE_URL}/v1/auth/oauth/{provider}/callback` (never `r.Host`), PKCE S256. Google scopes: `openid profile`. GitHub scope: `read:user`.
3. Provider 302s to the registered apex callback with `code` and `state`.
4. Callback **consumes** the state row in one `UPDATE … RETURNING` (`used_at IS NULL AND expires_at > now()`). Zero rows → 400 HTML on the apex, **no redirect**. Provider `error` / missing `code` with a valid state → consume, 400 HTML, no redirect. Token or userinfo failure → consume, 502 HTML, no redirect.
5. Identify via userinfo (Google `sub`; GitHub numeric `id` as decimal text). No `sub`/`id` → 502. Access token discarded, never stored. Upsert `visitors` on `(provider, provider_user_id)`, bump `last_login_at`.
6. Insert `visitor_sessions` (`id` = 32 random bytes, `visitor_id`, `site_id` from the state row, `host` from the state row, `expires_at = now()+30d`, `idle_expires_at = now()+14d`). Insert `visitor_establish_tokens` (`once` = 32 random bytes hex, 60s TTL, FK to the session, copy of `host` and `return_to`).
7. 302 to `https://<host>/v1/visitor/establish?once=<once>`. **No cookie on the apex.**
8. Content-host nginx/Caddy proxies `/v1/` to Go with `Host` preserved. `establish` consumes the token (`used_at` CAS, `expires_at > now()`, `Host` header port-stripped equals `host`). Mismatch → 400 HTML on that host, no cookie, no redirect. Success → `Set-Cookie` `__Host-sh_vsess`, 302 to stored `return_to` with **no query mutation**.
9. Page reloads. Subsequent `PATCH`/`POST` send the cookie + `X-SH-CSRF: 1` + `credentials: "include"`.

### 4.3 Custom-domain flow

Same as 4.2, except `return_to` host is `recipes.brand.com`, DNS check passed at start, session.`host` is `recipes.brand.com`, establish runs on that origin, cookie is host-only there. The content-host cookie is not sent (different host, `__Host-` forbids `Domain=`). A visitor who signed in on the path-model URL is signed out on the custom domain until they establish there. Accepted.

### 4.4 Write path

Applies to **both** URL prefixes (`site.go:263–281`):

`/v1/sites/{sitename}/…` and `/v1/u/{handle}/sites/{sitename}/…`

Order, after today's checks:

```
putSiteState / patchSiteState / appendCollection:
  authorizeStateOrigin          → 403 {error:"forbidden"}
  viewSessionOK                 → 403 {error:"this site is private — view it first to unlock its data"}
  visitorWriteOK                → see §7
  existing body / INSERT
```

`collectionGate` gains the write check only for `POST` (the gate is shared with GET; do not 401 GET). Cleaner: call `visitorWriteOK` from `appendCollection`, `putSiteState`, `patchSiteState` only — not from `listCollection` / `getSiteState`.

`GET` stays Origin + view-lock. Poll loops (`comments.js:139–144`, `llms.txt:23`) keep working.

`visitorWriteOK`:

1. If `X-API-Key` is present: resolve like `auth.Middleware` (admin key → admin UUID; else `GetUserByAPIKey`). If the user is admin **or** `sites.user_id` equals that user for this `site_id`, allow. If the key is present but not allowed for this site, **403** `{error:"forbidden", code:"writer_forbidden"}` — do not fall through to the cookie.
2. Else if `WRITE_AUTH_MODE=off`: allow.
3. Else look up `__Host-sh_vsess` (or `sh_vsess` on HTTP). Valid means row exists, `now <= expires_at`, `now <= idle_expires_at`, `host` equals request host, `site_id` equals target site. On valid cookie **and** `X-SH-CSRF: 1`: slide `idle_expires_at` to `min(now+14d, expires_at)`, allow. On valid cookie and missing CSRF: **403** `{error:"missing CSRF header", code:"csrf_required"}` when mode is `on` (in `log`/`off`, treat as anonymous so we do not break old widgets during measurement).
4. Else this is an anonymous write. Emit the structured log line in `log` and `on` (never log the body). If mode is `log`, or mode is `on` and `sites.allow_anonymous_writes` is true: allow (log `outcome=overridden` when the column fired). If mode is `on` and the column is false: **401** with the error contract in §7.

### 4.5 Disabled OAuth

If neither provider pair is set: boot succeeds, log  
`warning: no OAuth providers configured; visitor Google/GitHub sign-in disabled`  
(same tone as the Resend warning, `cmd/server/main.go:69–70`). Routes still mounted. `GET /v1/auth/oauth/providers` → `{"providers":[]}`. Start → 404. Magic-link and `ADMIN_API_KEY` unchanged.

### 4.6 What first-party clients do

**Hosted widgets** (`comments.js`, `feedback.js`): on `code === "visitor_auth_required"`, disable the composer, show "Sign in to post", `location.href` to `{apex}/v1/auth/oauth/{provider}?return_to={location.href}` after fetching providers. Send `X-SH-CSRF: 1` on every PATCH. Never `alert(status)` as the only UX. Never claim success on non-2xx. Derive apex: if `location.hostname` starts with `sites.`, use `location.protocol + '//' + hostname.replace(/^sites\./,'')`; else `https://simple-host.app` (overridable via `SH_COMMENTS.authBase` / `SH_FEEDBACK.authBase` for self-host).

**Templates** (`event-rsvp`, `waitlist`, `landing`): stop using `${apex}/v1/sites/${sub}`. Derive same-origin like `llms.txt:12–15` / widgets (`location.origin` + `/v1/u/{handle}/sites/{name}` on the content host; on a custom domain, `/v1/sites/{name}` only if `SH_*` provides the name — default content-host path model). Send credentials + CSRF. 401 → keep the form, show `error`, offer sign-in. POST-ok / PATCH-fail → do not re-POST.

**`llms.txt`, `backend.md`, builder skill, OpenAPI, README, `CLAUDE.md`:** writes require a visitor session or the owner's `X-API-Key`. Reads stay Origin-gated. Self-check includes a 401 branch. Bump `simple-host-website/.claude-plugin/plugin.json` `version` in the same PR as the skill text.

**`generate.go`:** one sentence in `generateSystemPrompt`: if the page embeds `comments.js`, the hosted widget handles sign-in; do not invent anonymous `fetch` writes.

---

## 5. Exact DDL

Append to `db/schema.sql` and add `db/migrations/visitor-oauth.sql` (hand-applied, `CREATE TABLE IF NOT EXISTS` / `ADD COLUMN IF NOT EXISTS`, same style as `db/migrations/analytics.sql`). Do not touch `users` or `auth_tokens`.

```sql
-- Operator escape hatch. Default FALSE. No owner API in v1; set via
-- ADMIN_API-Key endpoint or SQL.
ALTER TABLE sites
  ADD COLUMN IF NOT EXISTS allow_anonymous_writes BOOLEAN NOT NULL DEFAULT FALSE;

-- Durable visitor identity. Not a users row. Not an owner.
-- Same human on Google and GitHub => two rows. No email column.
CREATE TABLE IF NOT EXISTS visitors (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  provider         TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  provider_user_id TEXT NOT NULL,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_login_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id)
);

-- In-flight authorization-code flows. Pattern-match of auth_tokens:
-- opaque secret, short TTL, single-use.
CREATE TABLE IF NOT EXISTS oauth_states (
  state          TEXT PRIMARY KEY,
  provider       TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  code_verifier  TEXT NOT NULL,
  return_to      TEXT NOT NULL,
  host           TEXT NOT NULL,
  site_id        UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS oauth_states_expires_idx ON oauth_states (expires_at);

-- Server-side session. Cookie value is id (32 bytes, stored raw).
CREATE TABLE IF NOT EXISTS visitor_sessions (
  id              BYTEA PRIMARY KEY,
  visitor_id      UUID NOT NULL REFERENCES visitors(id) ON DELETE CASCADE,
  site_id         UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host            TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  idle_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS visitor_sessions_expires_idx
  ON visitor_sessions (expires_at);
CREATE INDEX IF NOT EXISTS visitor_sessions_visitor_idx
  ON visitor_sessions (visitor_id);

-- One-time bounce from the apex callback onto the host that will Set-Cookie.
CREATE TABLE IF NOT EXISTS visitor_establish_tokens (
  once         TEXT PRIMARY KEY,
  session_id   BYTEA NOT NULL REFERENCES visitor_sessions(id) ON DELETE CASCADE,
  host         TEXT NOT NULL,
  return_to    TEXT NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at   TIMESTAMPTZ NOT NULL,
  used_at      TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS visitor_establish_tokens_expires_idx
  ON visitor_establish_tokens (expires_at);
```

No `actor` column on `collection_items`. Existing anonymous rows stay. Do not invent a `CREATE TABLE collection_items` — the live table is missing from `schema.sql` and is out of scope to backfill.

Sweep: a goroutine sibling of `startExpirySweep` (`site.go:172`) every hour:

```sql
DELETE FROM oauth_states WHERE expires_at < now();
DELETE FROM visitor_establish_tokens WHERE expires_at < now();
DELETE FROM visitor_sessions WHERE expires_at < now() OR idle_expires_at < now();
```

Each `start` also runs `DELETE FROM oauth_states WHERE expires_at < now()`.

---

## 6. Exact route table

Mount OAuth from `OAuthHandler.Register` in `internal/handler/oauth.go` (new), next to `NewUserHandler` in `cmd/server/main.go`. Mount establish/logout from `SiteHandler.Register` (they must run on the content host / custom domain, which already proxy `/v1/`). Provider interface in `internal/oauth` so HTTP does not own userinfo.

Always register the string literals (even when no provider is enabled) so `scripts/check-docs-sync.sh` stays honest. Not behind `auth.Middleware`. Not behind `noticeMW`.

Go 1.22 method+path. Register the static `providers` path **and** the `{provider}` path; ServeMux prefers the literal.

| Method | Path | Auth | Purpose |
|---|---|---|---|
| `GET` | `/v1/auth/oauth/providers` | Public | `{"providers":["google","github"]}` for enabled pairs, else `{"providers":[]}`. Widgets use this; 401 bodies point here. |
| `GET` | `/v1/auth/oauth/{provider}` | Public | Start. Query `return_to` (required, absolute URL). `{provider}` ∈ {`google`,`github`} and enabled, else 404. Success: 302 to the provider. Failure: 400/404 JSON or HTML. |
| `GET` | `/v1/auth/oauth/{provider}/callback` | Public | Provider redirect URI. Query `code`+`state` or `error`. Success: 302 to `/v1/visitor/establish?once=…` on the stored host. Failure: HTML on the apex, no redirect. |
| `GET` | `/v1/visitor/establish` | Public, one-time token | Query `once`. Consume token, host-match, `Set-Cookie`, 302 to stored `return_to`. |
| `POST` | `/v1/visitor/logout` | Cookie | Delete **that** session row, `Set-Cookie` Max-Age=0. Require `X-SH-CSRF: 1` or `Content-Type: application/json` (not a simple form post). 204. |
| `PUT` | `/v1/sites/{sitename}/allow-anonymous-writes` | `X-API-Key` **admin only** (`auth.RequireAdmin`) | Body `{"allow":true\|false}`. Sets the column. 200 `{"site":"…","allow_anonymous_writes":true}`. Same prefix as `PUT /v1/sites/{sitename}/view-password` (`site.go:240`). Do **not** add a `/v1/u/{handle}/…` twin. |

Existing write routes — semantics only, no new paths:

| Method | Path | After `WRITE_AUTH_MODE=on` |
|---|---|---|
| `GET` | `/v1/sites/{sitename}/state` and `/v1/u/{handle}/sites/{sitename}/state` | Unchanged (Origin + view-lock) |
| `PUT` `PATCH` | same | + `visitorWriteOK` |
| `GET` | `…/collections/{coll}` (both prefixes) | Unchanged |
| `POST` | same | + `visitorWriteOK` |
| `OPTIONS` | state | Allow-Headers: `Content-Type, If-Match, If-None-Match, X-SH-CSRF` |
| `OPTIONS` | collections | Allow-Headers: `Content-Type, X-SH-CSRF` |

Do **not** add `Authorization` to Allow-Headers in v1 (no bearer).

Owner routes `POST /v1/auth`, `POST /v1/auth/verify`, `GET /v1/me` are unchanged.

Rate-limit start, callback, establish, and logout with a limiter in the same shape as `UserHandler.ipLimiter` (`user.go:72–76`: burst 20, 0.2/s).

---

## 7. Exact error contract

These routes are **not** behind `noticeMW`. A 401 body must not look like a state document (no `total`, no `_comments`).

### Missing visitor session (`WRITE_AUTH_MODE=on`, no valid session, no owner key, no override)

**401**

```json
{
  "error": "sign-in required to write",
  "code": "visitor_auth_required",
  "sign_in": "/v1/auth/oauth/providers",
  "retry": true
}
```

| Field | Rule |
|---|---|
| `error` | Stable human string. Safe to show in a banner. |
| `code` | Machine enum. **The only field clients switch on.** Never reuse `forbidden`. |
| `sign_in` | Relative URL of providers discovery. Pages must not hardcode a provider. |
| `retry` | Always `true` for POST append and PATCH ops. |

### Other statuses (unchanged or new)

| Situation | Status | Body |
|---|---|---|
| Origin/Referer fail | 403 | `{ "error": "forbidden" }` (`collections.go:145`, `site.go:557`) |
| View-lock fail | 403 | `{ "error": "this site is private — view it first to unlock its data" }` (`collections.go:149`) |
| Cookie present, `X-SH-CSRF` missing, mode `on` | 403 | `{ "error": "missing CSRF header", "code": "csrf_required" }` |
| `X-API-Key` present but not owner/admin of this site | 403 | `{ "error": "forbidden", "code": "writer_forbidden" }` |
| OAuth start: bad/missing `return_to` | 400 | `{ "error": "invalid return_to" }` |
| OAuth start: unknown/disabled provider | 404 | `{ "error": "not found" }` |
| OAuth callback: bad/replayed/expired state, or provider `error` | 400 | HTML on the apex, **no redirect** |
| OAuth callback: token/userinfo failure | 502 | HTML on the apex, **no redirect** |
| Establish: bad/replayed/expired/host-mismatch `once` | 400 | HTML on that host, **no cookie**, **no redirect** |
| Mode `log` or `off`, anonymous write | 2xx as today | |

Do not use 403 for "not signed in." 403 is already origin-fail and view-lock, with different recovery.

OAuth HTML errors are a short, no-store page: "Sign-in failed. Close this tab and try again from the site." No `return_to` link.

---

## 8. Exact cookie spec

### Session cookie (set only by `GET /v1/visitor/establish`)

| Attribute | HTTPS (prod, and anything with `X-Forwarded-Proto: https` or `r.TLS != nil`) | Local HTTP (`PUBLIC_BASE_URL` is `http`) |
|---|---|---|
| Name | `__Host-sh_vsess` | `sh_vsess` |
| Value | 32-byte session id, lowercase hex (64 chars) | same |
| Domain | **omitted** | omitted |
| Path | `/` | `/` |
| Secure | `true` | `false` |
| HttpOnly | `true` | `true` |
| SameSite | `Lax` | `Lax` |
| Max-Age | `1209600` (14 days) | same |

`__Host-` is browser-enforced: Secure + Path=/ + no Domain. Do not invent a `Domain=simple-host.app` cookie. That is what `viewLogin` does (`viewauth.go:136–141`) and it is **wrong** for identity.

Never set this cookie on the apex (`cfg.SiteDomain` / host of `PUBLIC_BASE_URL`). `establish` must 400 if `Host` is the apex.

Idle 14 days (cookie Max-Age + `idle_expires_at`). Absolute 30 days in the row even if the browser keeps the cookie. Logout: `DELETE` that row, Max-Age=0.

Do not reuse `shview_*`. View-lock is "knows the password," not "is this person."

### One-time establish token

Not a cookie. Query param `once` on the 302 from callback. 32 bytes hex, 60s, single-use, bound to `session_id` + `host` + `return_to`.

### CSRF request header

`X-SH-CSRF: 1` on cookie-authenticated writes and on `POST /v1/visitor/logout`.

---

## 9. Config

Extend `internal/config/config.go`. `Load` still fatals only on empty `DB_DSN` or `ADMIN_API_KEY`.

| Env var | Required | Unset behaviour |
|---|---|---|
| `GOOGLE_OAUTH_CLIENT_ID` | no | Google off unless **both** Google vars are non-empty. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | no | Google off. |
| `GITHUB_OAUTH_CLIENT_ID` | no | GitHub off unless **both** GitHub vars are non-empty. |
| `GITHUB_OAUTH_CLIENT_SECRET` | no | GitHub off. |
| `WRITE_AUTH_MODE` | no | **`log`**. Values: `off` \| `log` \| `on`. Anything else → log a warning and treat as `log`. |

A provider with only one of the two vars set is a config mistake: log a warning naming the provider, treat as off.

`redirect_uri` is **not** an env var. It is  
`strings.TrimRight(cfg.PublicBaseURL, "/") + "/v1/auth/oauth/" + name + "/callback"`.  
`PUBLIC_BASE_URL` already exists (`config.go:20, 98`, default `https://simple-host.app`). Self-hosters register that exact callback on their Google/GitHub apps.

Boot, after the Resend warning (`main.go:69–70`):

- 0 providers: warning quoted in §4.5.
- N providers: `log.Printf("visitor OAuth enabled: %s", names)`.
- Always: `log.Printf("write auth mode: %s", cfg.WriteAuthMode)`.

No `OAUTH_STATE_SIGNING_KEY`. No session signing key. No `OAUTH_REDIRECT_BASE_URL` (the owner-OAuth doc invented one; ignore it).

---

## 10. Implementation order

Each step is one PR-sized, independently shippable change in **this** worktree. Each has a verification command the implementer runs against `go run ./cmd/server` (local Postgres, no production writes). Do not flip `WRITE_AUTH_MODE=on` until step 6.

### Step 1 — Schema + config + stores (no HTTP behaviour change)

**Files:** `db/schema.sql`, `db/migrations/visitor-oauth.sql`, `internal/config/config.go`, `internal/db/visitors.go` (upsert visitor; insert/get/touch/delete session; consume/prune states and establish tokens), `internal/oauth/provider.go`, `internal/oauth/google.go`, `internal/oauth/github.go`, `cmd/server/main.go` (log providers + mode; do not mount new routes yet).

**Change:** tables exist. `WRITE_AUTH_MODE` parses. `x/oauth2` is the only new module. Identify functions are unit-testable against canned JSON; no live Google needed. Boot with vars unset still succeeds.

**Verify:**

```bash
# apply migration against the local DB you already use
psql "$DB_DSN" -f db/migrations/visitor-oauth.sql
# boot with no OAuth vars
DB_DSN='…' ADMIN_API_KEY='…' go run ./cmd/server
# expect: "no OAuth providers configured" and "write auth mode: log"
# curl /healthz → 200
# existing PUT /v1/sites/{s}/state with Origin still 200
```

### Step 2 — OAuth start/callback + establish/logout (writes still anonymous)

**Files:** `internal/handler/oauth.go`, `internal/handler/visitorsession.go`, `cmd/server/main.go` (Register), `internal/handler/site.go` (two new mux lines only).

**Change:** routes in §6 except the admin override. `sanitizeReturnTo` + DNS check as specified. `CompleteVisitorAuth` is not a hook — callback writes the session + establish token and 302s. Establish sets the cookie. Logout clears it. `WRITE_AUTH_MODE` is still `log` and **not consulted**.

**Verify:**

```bash
# 1. providers with vars unset
curl -sS http://localhost:8090/v1/auth/oauth/providers
# → {"providers":[]}

# 2. start with vars unset
curl -sS -D- 'http://localhost:8090/v1/auth/oauth/google?return_to=http://localhost:8090/x/y/'
# → 404

# 3. with Google vars set, bad return_to
curl -sS -D- 'http://localhost:8090/v1/auth/oauth/google?return_to=https://evil.example/phish'
# → 400 {"error":"invalid return_to"}  (no Location)

# 4. with a real local site at ContentHost path, start
curl -sS -D- 'http://localhost:8090/v1/auth/oauth/google?return_to=http://<content-host>/<handle>/<site>/'
# → 302 to accounts.google.com, Location contains state= and code_challenge=

# 5. callback with garbage state
curl -sS -D- 'http://localhost:8090/v1/auth/oauth/google/callback?code=x&state=dead'
# → 400 HTML, no Location

# 6. hand-insert a visitor_sessions row + establish token, hit establish on the
#    matching Host, confirm Set-Cookie and 302 to return_to; replay once → 400
# 7. POST /v1/visitor/logout with that cookie + X-SH-CSRF:1 → 204, cookie cleared
# 8. bash scripts/check-docs-sync.sh → ok
```

OpenAPI for the five new paths is part of this step. `scripts/check-docs-sync.sh` greps `mux.Handle` strings against `openapi.yaml` and hard-fails on drift. Regenerate `openapi.json` with the one-liner in `CLAUDE.md`.

### Step 3 — Measure, don't reject; CORS headers; error-body type exists

**Files:** `internal/handler/site.go` (`putSiteState`), `internal/handler/stateops.go` (`patchSiteState`), `internal/handler/collections.go` (`appendCollection`, `optionsCollection`), `optionsSiteState`, `internal/handler/static/openapi.yaml` + regenerated `openapi.json` (401 examples on writes), `CLAUDE.md` one-line note on `WRITE_AUTH_MODE`.

**Change:** `visitorWriteOK` is called. Default mode `log`: anonymous writes still 2xx; one structured log line per attempt (`site_id`, `name`, `route` ∈ {`state_put`,`state_patch`,`collection_post`}, `collection` or empty, `mode`, `outcome` ∈ {`allowed`,`rejected`,`overridden`}, `origin_class` ∈ {`content_host`,`custom_domain`,`allowed_origin`}, `has_cookie` bool — never the cookie value, never the body). Allow-Headers gain `X-SH-CSRF`. The 401 JSON shape is implemented but not returned unless an operator sets `WRITE_AUTH_MODE=on` locally.

**Verify:**

```bash
# anonymous PATCH still 200 with mode unset/log
curl -sS -D- -X PATCH -H 'Origin: https://sites.simple-host.app' \
  -H 'Content-Type: application/json' \
  -d '{"ops":[{"op":"inc","path":"t","by":1}]}' \
  http://localhost:8090/v1/u/<h>/sites/<s>/state
# process log contains anon_write outcome=allowed

WRITE_AUTH_MODE=on  # local only
# same curl → 401, body has code=visitor_auth_required and sign_in
# GET state still 200
# PUT with X-API-Key of the owner + Origin → 200
# OPTIONS → Allow-Headers includes X-SH-CSRF
```

### Step 4 — First-party clients + teachers (server still accepts anonymous writes in prod)

**Files:** `internal/handler/static/comments.js`, `feedback.js`, `llms.txt`, `templates/event-rsvp/index.html`, `templates/waitlist/index.html`, `templates/landing/index.html`, `generate.go` (`generateSystemPrompt`), `README.md`, `simple-host-website/skills/website-deploy/SKILL.md`, `references/backend.md`, `website-deploy-builder/SKILL.md`, `simple-host-website/.claude-plugin/plugin.json` (bump `version`), `internal/handler/static/architecture.html` if it still claims anonymous public writes.

**Change:** widgets and templates behave as §4.6. `llms.txt` Step 1 checks `res.ok` / 401, sends `credentials` + `X-SH-CSRF`, does not hide the form on failure, and does not re-POST after a partial write. Skill text matches. Docs sync warn-only capabilities may mention visitor sign-in; add a `visitor-sign-in` line to `scripts/check-docs-sync.sh` caps if you want the nudge.

**Verify:**

```bash
# hosted widget: open a local page that loads comments.js, submit, with
# WRITE_AUTH_MODE=on and no cookie → "Sign in to post", composer disabled
# after a fake establish cookie + CSRF header → PATCH 200
# event-rsvp: BASE is location.origin /v1/u/..., 401 keeps the form
# llms.txt contains visitor_auth_required and credentials
# bash scripts/check-docs-sync.sh  → ok
```

There are no browser tools required to prove the JS if you grep the shipped strings (`X-SH-CSRF`, `visitor_auth_required`, `location.origin`) and hit the widget's PATCH with curl. If a browser is available, click the composer.

### Step 5 — Admin override endpoint

**Files:** `internal/handler/site.go` (one `PUT`, `auth.RequireAdmin`), OpenAPI path.

**Change:** admin can flip `allow_anonymous_writes`. Non-admin → 403. Using the override is logged when a write hits it.

**Verify:**

```bash
curl -sS -X PUT -H "X-API-Key: $ADMIN_API_KEY" \
  -H 'Content-Type: application/json' -d '{"allow":true}' \
  http://localhost:8090/v1/sites/<s>/allow-anonymous-writes
WRITE_AUTH_MODE=on
# anonymous PATCH that site → 200, log outcome=overridden
# another site → 401
```

### Step 6 — Operator flip (not a code default)

**Executed 2026-09-05: `WRITE_AUTH_MODE=on`.**

**Do not change the source default to `on`.** After step 4 has been deployed (hosted `comments.js` / `feedback.js` come from the embedded `FileServer` in `internal/handler/ui.go`; they are not long-cache-busted — next page load picks them up) and after owners have been told:

1. Optional disk census: `grep -lR -E 'comments\.js|feedback\.js|/collections/|method:.PATCH' "$DATA_DIR"/*/current | wc -l`
2. Set `WRITE_AUTH_MODE=on` in the unit file. Restart the process (the operator does this; this spec's implementer does not restart production).
3. Watch 401 rate vs collection insert rate for 48 hours. Rollback: `WRITE_AUTH_MODE=log`.
4. Seed `allow_anonymous_writes` on any site that is a known holdout.

**Verify (prod, operator):** one successful credentialed widget POST, one expected 401 on a curl without a cookie, GET state still public.

---

## 11. Explicitly out of scope for v1

Everything in the CUT column of §3, plus:

- Wiring `SetDomainStatus` / promoting `domain_status` to `active`.
- Rewriting already-deployed third-party `index.html`.
- Making reads require sign-in.
- Per-item moderation or deleting old anonymous rows.
- A combined POST+PATCH transaction.
- Microsoft / Apple / email visitors.
- A session on `simple-host.app`.
- Changing `auth.Middleware` or `EnsureAdminUser`.

---

## 12. Residual risks (still dangerous after all this)

1. **Already-deployed generated pages will lie.** `llms.txt` pages in the wild `await fetch` and hide the form. After `on`, those guests see a thank-you for an RSVP that was discarded. We cannot patch their tarball. The `log` window plus an owner email is the only mitigation. Plan as if most of the live sites are in this set until a disk census says otherwise.

2. **Identity wall on RSVP/waitlist is a conversion cliff.** There is no design that both demands Google/GitHub and preserves today's fill-in-the-form completion. Guests will bounce. That is the product decision, not a bug in this spec.

3. **Custom-domain DNS check can 400 a legitimate sign-in** if DNS is slow, not yet propagated, or uses an unexpected ALIAS/flattened CNAME. Fail closed. The guest retries. Do not "fix" this by accepting any bound domain — that reopens the paypal.com redirect.

4. **Backend-anywhere (`allowed_origins` + `SH_COMMENTS.base`) writes die** when mode is `on`. The cookie is not on the apex and we are not shipping a bearer. Hosted GitHub Pages comments stop accepting posts. Document it. Do not silently accept those writes.

5. **Starter templates in already-deployed tarballs still call the apex.** Step 4 fixes the catalog and new deploys. Old RSVP pages will 401 even after the guest signs in, because the Lax host-only cookie is not sent cross-origin to `simple-host.app`. Same class of problem as (1).

6. **Co-tenant pages can still *read* each other's public state** (today's contract, `site.go:424–437`). Sign-in does not make RSVP lists private. Do not tell owners this "secures" collected PII.

7. **A determined abuser completes OAuth.** This stops casual anonymous spam. It does not stop a script that can click Google.

8. **`__Host-` cookies need HTTPS.** Local HTTP uses the `sh_vsess` fallback. If that fallback is accidentally used behind a TLS-terminating proxy that does **not** set `X-Forwarded-Proto: https`, the cookie will be accepted on HTTP and will not get `__Host-` protection. nginx in this repo sets that header (`nginx-sites-content-host.conf:21`). Do not deploy a proxy that doesn't.

9. **Establish `once` travels in a URL** (Referer to first-party assets on the return page, 60s window). Single-use + host bind + short TTL. Do not lengthen the TTL.

10. **Admin key writes as the site.** A leaked `ADMIN_API_KEY` was already game-over. This spec does not make that worse. A leaked **owner** `api_key` can write state; that was also already true for every owner-authenticated route.

11. **`WRITE_AUTH_MODE=on` with no OAuth vars configured** 401s every anonymous write and offers a `sign_in` URL that returns `{"providers":[]}`. Do not flip `on` until at least one provider pair is set. The implementer should refuse to document a prod `on` without that.

---

## 13. Key decisions (no alternatives)

1. Visitors ≠ users. New `visitors` table. Magic-link remains the only owner path.
2. One apex OAuth callback; cookie is issued on the host the page already calls, via `establish`. No visitor cookie on `simple-host.app`.
3. Site-scoped + host-scoped sessions, not pairwise ids.
4. Keep `authorizeStateOrigin`; add cookie + `X-SH-CSRF` on writes. Reads stay public scratch.
5. Owner/admin `X-API-Key` is a valid writer. Bundled MCP does not need it for state, but owner curl does.
6. `sign_in` URL is `/v1/auth/oauth/providers`. Start is `/v1/auth/oauth/{provider}`. There is no `/v1/visitor/session`.
7. Do not follow `docs/designs/oauth2-authentication.md`. Do not add `go-oidc`. Do not link on email.
8. PKCE S256 on. Email scopes off. Email not stored.
9. Custom-domain `return_to` requires a live DNS proof of control, not `domain_status = 'active'`.
10. Unset `WRITE_AUTH_MODE` is `log`. Enforcement is an operator env flip after first-party clients ship.
11. `allow_anonymous_writes` is an admin hatch, not a product default.
12. Backend-anywhere signed-in writes are out. Those pages 401.

---

## 14. File map for the implementer

| Area | Files |
|---|---|
| Config | `internal/config/config.go`, `cmd/server/main.go` |
| DDL | `db/schema.sql`, `db/migrations/visitor-oauth.sql` |
| Stores | `internal/db/visitors.go` (new) |
| Providers | `internal/oauth/provider.go`, `google.go`, `github.go` (new) |
| HTTP OAuth | `internal/handler/oauth.go` (new) |
| Cookie / establish / logout / `visitorWriteOK` | `internal/handler/visitorsession.go` (new) |
| Write gate + CORS | `internal/handler/site.go`, `stateops.go`, `collections.go` |
| Widgets | `internal/handler/static/comments.js`, `feedback.js` |
| Teachers | `llms.txt`, `openapi.yaml` + `openapi.json`, templates, skills, `plugin.json`, `README.md`, `CLAUDE.md` |
| Docs sync | `bash scripts/check-docs-sync.sh` after every new `/v1` route |
