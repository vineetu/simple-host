# Invite-only sites — visitor sign-in for hosted sites

**Status:** design, not built. Written 2026-07-26, revised after security + feasibility + product review.
**Product name:** *Invite-only sites.* `members` is the API/DB noun only.
**Scope:** let a *visitor* of a hosted site sign in with their email, and let the
site show them their own private page and their own private data.

> **Read §0 first.** Three live bugs must be fixed before any of this is built, and
> one of them is a privacy bug affecting users today.

---

## 0. Prerequisites — live bugs found while designing this

All four verified against the running playground (`simple-host.app`) on 2026-07-26.
None of them are caused by this feature; all of them block it.

### P1 — View-lock is inverted in production. Password-protected pages are public. 🔴

A site with a view password set serves its **page publicly**, and its **data not at all**:

```
GET https://sites.simple-host.app/vineetu/jot-transcribe-windows/         -> 200  (real content)
GET .../v1/u/vineetu/sites/jot-transcribe-windows/state  (allowed Origin) -> 403  (forever)
grep -rl auth_request /etc/nginx/sites-enabled/                           -> none
```

Exactly backwards. Cause: the nginx `auth_request` gate was never deployed
(`sites-available/simple-host` has it; the **enabled** file doesn't), so nothing
gates the page. Meanwhile `viewSessionOK` (`viewauth.go:102`) *does* gate the state
API, and the only way to mint the required cookie is `POST /__view_login` on the
legacy per-site host — which `LegacyHostRedirect` now 301s away. So the lock can
never be opened.

Anyone who set a view password believes their page is private. It is not.

### P2 — View-lock is keyed by site *name*, which stopped being unique. 🔴

`viewLocks` is `map[siteName]hash` (`viewauth.go:32-54`), loaded by
`SELECT name, view_password_hash FROM sites` (`queries.go:750`), and the HMAC binds
only `name + exp` (`viewauth.go:71`). But the uniqueness constraint is now
`UNIQUE (user_id, name)` (`schema.sql:29`). So if Bob creates a site called `shop`:

- Bob setting a password **overwrites Alice's `shop` lock** process-wide (`viewauth.go:183`).
- Bob clearing it **unlocks Alice's private site** (`viewauth.go:208`).
- Bob's `shview_shop` cookie **opens Alice's `shop`**.

Must be re-keyed to `site_id` — cache, cookie name, and HMAC payload — before §6
builds on it.

### P3 — `LegacyHostRedirect` 301s any new per-site host, including its API. 🔴

Verified: `https://vineetu--shop.simple-host.app/` → `301 https://simple-host.app/`.
`legacyhost.go:32-48` treats every single-label `*.simple-host.app` as a retired
legacy site host, and it wraps the whole mux (`main.go:110`), so `/v1/*` is
redirected too. **301s are cached by browsers indefinitely** — this is byte-for-byte
the mechanism that took down the ideaflow box on 2026-07-26 and outlived the
rollback. Any per-site-origin work must patch this *first*, and ship 302 during rollout.

### P4 — Core tables have no DDL in the repo.

`collection_items`, `sites.state`, `sites.state_version`, `sites.view_password_hash`
are used by the code but created nowhere in `db/schema.sql` or `db/migrations/`. A
fresh install from `schema.sql` produces a database the binary cannot run against.
Backfill this before writing any member migration, or the divergence gets worse.

---

## 1. What's missing today

A simple-host site can already *collect* data. It cannot *have people*.

The platform's own docs apologise for this. `install.html`'s FAQ: *"the per-site
store is public and unauthenticated… fine for a party, not fine for anything you'd
mind a stranger reading."* The builder skill instructs every agent to ship an
`admin.html` and then **write the apology into the UI**: *"Do NOT build a fake
password gate (the data is public by URL — say so in one small line)."*

That apology ships on every data-collecting site the platform builds.

### The smaller feature that should ship first

The most-wanted thing is **not** visitor sign-in. It's *"only I can see who signed up."*
That needs no visitor identity at all — just **write-only collections**: `POST` stays
public, `GET` becomes owner-only, and the owner reads submissions in the dashboard
(already an authenticated, isolated origin). Roughly 150 lines, no new concepts, and
it deletes the most embarrassing sentence in the docs.

**Ship that as its own release before any of what follows.** It is tracked here
because it changes what v1 of *this* feature has to carry, not because it's part of it.

### What invite-only sign-in is actually for

1. **Client proof / private share** — "only these four addresses can open this page."
2. **Course or content gate** — "only my students can see the lessons."
3. **Saved progress across devices** — a tracker that follows you.

(1) and (2) — the overwhelming majority — need **only a page gate and an allowlist**.
No member list UI, no per-member collections, no profile. That observation drives the
entire v1 cut in §7.

### Two identity namespaces, never mixed

| | **Platform user** (today) | **Site member** (new) |
|---|---|---|
| Who | owns and deploys sites | a visitor of one hosted site |
| Table | `users` | `site_members` |
| Credential | `X-API-Key` | opaque session cookie |
| Scope | all their sites | exactly one `site_id` |
| Can deploy? | yes | **never** |

A member of site A is a different principal from a member of site B *even with the
same email*. No global visitor account. One site can never enumerate another's audience.

---

## 2. The constraint that decides everything: origins, not paths

Every v3 site is served from one shared origin —
`https://sites.simple-host.app/<handle>/<site>/`. So `alice/shop` and
`mallory/free-emoji-pack` are the **same origin**, and Mallory's page can simply do:

```js
fetch('/alice/shop/v1/...', {credentials:'include'}).then(r => r.json())
```

Same-origin: the cookie is attached, CORS never applies, the response is readable.
Cookie `Path=` doesn't help — path scoping controls which *requests* carry a cookie,
not which *pages* may make them.

Every candidate escape was checked and every one fails:

| Mechanism | Why it fails |
|---|---|
| Path-scoped `HttpOnly` cookie | Path is not a boundary; see above. |
| CHIPS / `Partitioned`, storage partitioning | Partition by **top-level site**. Both tenants *are* one top-level site. |
| `Origin-Agent-Cluster` | Controls agent-cluster allocation; explicitly not a security boundary. |
| Service-worker `scope` | Restricts registration, not reach. Mallory needs no SW — `fetch` already works. |
| `document.domain` | Can only widen, never narrow. Deprecated. |
| WebAuthn | RP ID is a host or registrable domain, **never** a path. Both tenants share it. |
| Sandboxed iframe / opaque origin | Opaque origins have no cookie or storage access at all. |
| Session-holding iframe on a distinct origin + `postMessage` | The closest miss. The frame authenticates its parent by `event.origin` — and Alice's and Mallory's parents have the **identical** origin. It cannot tell them apart. Also needs a per-site origin anyway. |

> **A session on the shared content host cannot be secured. A site with sign-in needs
> its own origin.** This is not a preference; it is the whole design.

The server already concedes it: `site.go:417` accepts `contentHost` as a valid Origin
for **every** site and reflects it with `Access-Control-Allow-Credentials: true`
(`site.go:424-427`). Any hosted page can read any other site's state today, by design.

This is also already the stated product rule in the `connect-domain` skill — *"privacy
is a property of a connected domain (its own isolated origin), not of a path on the
shared host."* Invite-only generalises it.

### 2.1 Put site origins on a different registrable domain

The first draft proposed `<handle>--<site>.simple-host.app`. Review killed it:

- **`simple-host.app` is not on the Public Suffix List**, so siblings are *same-site*.
  `SameSite=Lax` therefore provides **zero** CSRF protection between tenants, and a
  sibling can evict other hosts' cookies by writing 200 `Domain=.simple-host.app`
  cookies (Firefox caps ~180 per eTLD+1) — forcing logouts everywhere.
- **Phishing.** Nothing stops handle `secure` + site `simple-host` →
  `secure--simple-host.simple-host.app`: attacker-controlled HTML, valid cert, a host
  that genuinely *is* `simple-host.app`, cloning the platform sign-in page. The target
  is the platform API key in `localStorage` on the apex.
- **`--` genuinely collides.** `validSiteName` is
  `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` (`sitename.go:14`) — **`my--site` is legal
  today**, and `ClaimHandle` (`queries.go:114`) validates nothing at all. Handle `a` +
  site `b--c` and handle `a--b` + site `c` both yield `a--b--c` — an *origin collision
  between two tenants*.
- RFC 5891 reserves hyphens in a label's 3rd–4th characters, so a 2-char handle gives
  the malformed `jo--shop`, which some validators reject.

**Decision: hosted site origins move to their own registrable domain**, e.g.
`*.simple-host.site`, entirely separate from the platform's `simple-host.app`. That
one change retires the phishing vector and the cookie-eviction vector, and makes
tenant↔platform genuinely cross-site. **Submit that domain to the PSL** to make
tenant↔tenant cross-site too — worth doing, but it takes weeks and must not block:
until it lands, carry an explicit CSRF token (§5) rather than relying on `SameSite`.

**Allocate the hostname; never derive it.** `member_origin` is stored on `sites` and
resolved by lookup, so there is nothing to parse and no ambiguity:

- default to `<site>.simple-host.site` when free, else `<site>-<handle>`, else owner picks;
- `UNIQUE`, bound to `site_id` at issue time, **never recomputed** from a mutable name;
- a `retired_origins` tombstone table blocks reuse forever. Without it, renaming or
  deleting a site frees a host that still holds visitors' session cookies, service
  workers and cache entries — whoever claims it next **inherits live sessions**;
- on rename/delete/transfer: revoke all sessions and emit
  `Clear-Site-Data: "cache", "cookies", "storage", "executionContexts"` on the old origin;
- reject `--` in site names and handles at creation (and audit existing rows), extend
  the reserved lists (`handles.go:14`, `sitename.go:22`) with `secure`, `account`,
  `billing`, `verify`, `signin`, `support`, `simple`, `host`.

### 2.2 Phase 0 is a user-visible feature, not plumbing

"Give your site its own address" ships on its own merits: a shorter URL, isolated
`localStorage`, and — because privacy becomes a property of an origin you now own —
**view-lock without having to buy a domain**. The `connect-domain` skill currently
tells people they must own a domain to password-protect a page. Phase 0 deletes that
requirement. Ship it as its own release.

**The old path URL must 301 to the new origin.** People have already shared
`sites.simple-host.app/alice/shop/` links. With the redirect the story is *"your site
gets a shorter address and old links still work"* — an upgrade, not a tax. It's safe:
a `__Host-` cookie never travels back to the shared origin.

⚠️ **This breaks deployed sites' JS.** Every skill and `llms.txt` derives the API base
with `location.pathname.match(/^\/([a-z0-9-]+)\/([a-z0-9-]+)/)`. On the site's own
origin that matches the wrong segments and silently builds a bad URL. Which leads to
the fix that's better than a fix:

### 2.3 On its own origin, a site's API loses its prefix

There is exactly one site on that origin, so the handle and site name are redundant:

```
/v1/state          /v1/collections/{coll}          /v1/members/me
```

Nothing to derive, no regex, nothing an agent can get wrong — and materially nicer on
custom domains. The prefixed routes keep working on the shared host.

---

## 3. Data model (v1)

Deliberately smaller than the first draft. No `provider` columns: **in v1 identity is
a verified email address, full stop.** When Google lands it becomes a *proof method*
resolving to the same member row (§6.1), not a second identity namespace.

```sql
CREATE TABLE site_members (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id       UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  email         TEXT NOT NULL CHECK (email = lower(email)),
  display_name  TEXT,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at  TIMESTAMPTZ,
  CONSTRAINT site_members_site_email UNIQUE (site_id, email)
);

CREATE TABLE site_member_sessions (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  member_id  UUID NOT NULL REFERENCES site_members(id) ON DELETE CASCADE,
  token_hash BYTEA NOT NULL UNIQUE,          -- sha256(token); never the token
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at TIMESTAMPTZ NOT NULL            -- fixed 30 days, no sliding, no rotation
);
CREATE INDEX ON site_member_sessions(member_id);

CREATE TABLE site_member_data (               -- NOT "state" — see §4
  member_id  UUID PRIMARY KEY REFERENCES site_members(id) ON DELETE CASCADE,
  data       JSONB NOT NULL DEFAULT 'null',
  version    INTEGER NOT NULL DEFAULT 0,      -- matches the existing integer-ETag model
  bytes      INTEGER NOT NULL DEFAULT 0,      -- enforced cap, see §8
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE site_member_auth_tokens (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id     UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  email       TEXT NOT NULL,
  code_hash   BYTEA NOT NULL,
  link_hash   BYTEA NOT NULL UNIQUE,
  nonce_hash  BYTEA NOT NULL,                 -- binds the challenge to the browser (§6)
  expires_at  TIMESTAMPTZ NOT NULL,
  used_at     TIMESTAMPTZ,
  attempts    INT NOT NULL DEFAULT 0,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE retired_origins (                -- §2.1: never re-issue a host
  origin     TEXT PRIMARY KEY,
  retired_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

ALTER TABLE sites ADD COLUMN access_mode   TEXT NOT NULL DEFAULT 'public'
  CHECK (access_mode IN ('public','password','invite'));
ALTER TABLE sites ADD COLUMN invite_list   TEXT;           -- emails and @domains, comma-separated
ALTER TABLE sites ADD COLUMN member_origin TEXT UNIQUE;
```

`access_mode` is the single source of truth — no separate `members_enabled` flag.

---

## 4. Endpoints (v1)

Served **only on the site's own origin**, prefix-free. Owner routes stay on the
platform origin under the existing `/v1/sites/{sitename}/...` shape.

**Visitor** — session cookie only, no API key:

| Method + path | Does |
|---|---|
| `POST /v1/members/signin` | `{email}` → mail a code + link. **Always 202**, identical body and timing. |
| `POST /v1/members/verify` | `{email, code}` → set session cookie, return the member |
| `GET /v1/members/me` | current member, or 401 |
| `POST /v1/members/logout` | delete this session |
| `DELETE /v1/members/me` | self-erasure (the one non-negotiable compliance item) |
| `GET \| PUT \| PATCH /v1/members/me/data` | the member's own private JSON |

**Owner** — `X-API-Key`, on the platform origin:

| Method + path | Does |
|---|---|
| `PUT /v1/sites/{sitename}/access` | `{mode, invite_list}` — the whole owner surface |
| `GET /v1/sites/{sitename}/access` | read it back, plus who has opened it |
| `DELETE /v1/sites/{sitename}/members/{id}` | remove + revoke sessions |

### 4.1 The `/state` suffix is a landmine — do not step on it 🔴

`cors.go:30` routes by **suffix**:

```go
if strings.HasSuffix(r.URL.Path, "/state") || strings.Contains(r.URL.Path, "/collections/") {
```

A route named `…/members/me/state` would end in `/state` and silently fall into
`authorizeStateOrigin`, which accepts `contentHost` for **every** site and answers
with `Access-Control-Allow-Origin: <origin>` + `Allow-Credentials: true`. Any hosted
page could then read every member's private data cross-tenant with one `fetch`.

Two independent mitigations, both required:

1. **Name the route `/v1/members/me/data`**, never `/state`.
2. **Fix `cors.go` to match explicit routes, not suffixes**, and give member endpoints
   their own hard same-origin check: `Origin` must equal this site's `member_origin`
   exactly, or be absent. No `contentHost`, no `allowed_origins` widening, and never
   `Allow-Credentials` for a foreign origin.

### 4.2 `me/data` must reuse the state engine, not fork it

The existing ETag/CAS/ops machinery is ~500 lines keyed on `site_id`, using an
**integer version** (`stateETag`, `site.go:517`), not a string etag: conditional GET →
304 (`site.go:485`), `If-Match` → 412 (`site.go:584`), and `PATCH` with five ops in a
`SELECT … FOR UPDATE` transaction (`stateops.go`, 317 lines). `applyStateOps`,
`navigate`, `splitPath` are pure and reusable as-is; the handlers and six `db`
functions are `site_id`-specific.

**Refactor them to take a generic row locator** rather than duplicating. A second,
subtly-divergent state implementation is the worst outcome available here.

---

## 5. Sessions

- **Token:** 32 bytes from `crypto/rand`. Store `sha256(token)` only. Unsalted SHA-256
  is correct here — 256 bits of entropy means no dictionary threat, and it must be
  deterministic to index. HMAC or Argon2 would buy nothing. Add a `key_version` column
  if a server-side pepper might be wanted later.
- **Cookie:** `__Host-shmember`, `Secure; HttpOnly; Path=/`, **no `Domain`**. The
  `__Host-` prefix is load-bearing: it forbids a sibling from overwriting the cookie.
- **Lifetime:** one fixed 30-day expiry. **No sliding window, no rotation-on-refresh.**
  Rotation is where session bugs live: six parallel `fetch`es hit the refresh window,
  one rotates and deletes, the other five 401. It buys theft *detection*, not
  prevention — not worth the bug class at this size.
- **CSRF:** an explicit per-session token in a second `__Host-` cookie, double-submitted
  in a header. **Do not rely on `SameSite=Lax`** — until the PSL listing lands, tenants
  are same-site and Lax protects nothing between them (§2.1). Rejected the first
  draft's mandatory `X-SH-Member: 1` header: an agent-written `fetch` that forgets it
  gets a mystery 403.
- **Revocation:** delete the row. Removing a member, or setting `access_mode` away from
  `invite`, deletes all affected sessions. Site deletion cascades.
- **Logout** must also send `Clear-Site-Data`. Otherwise a service worker registered by
  the tenant survives logout on an origin the tenant fully controls.

---

## 6. Signing in

Email code only in v1. Reuse `user.go`'s proven parts verbatim — `generateNumericCode`
(`user.go:305`), `generateLinkToken` (`:321`), `subtleConstantTimeEqual` (`:331`),
`maxCodeAttempts=3`, `authTokenTTL=15m`, both rate limiters (`:74`). Three changes:

- store `sha256` of the code and link token (the platform stores them plaintext,
  `schema.sql:62`);
- scope **every** lookup by `site_id`;
- always 202. Note the platform version returns 400 on a malformed address
  (`user.go:113`) and **500 when the mailer rejects** (`user.go:150`) — a live
  address-validity oracle. The member version must enqueue and return 202
  unconditionally.

### The three attacks this flow must survive

**Cross-site code replay.** `link_hash` is globally unique, and the platform analogue
looks tokens up by link alone (`user.go:183`). If the member handler resolves a row by
link or by latest-for-email and then trusts the `site_id` from the URL, a code minted
at `mallory--evil` is redeemable at `alice--shop`. Mallory owns a site, so she can mint
a valid challenge for *any* address on demand. **Every query must be
`WHERE id = $1 AND site_id = $2`, with an explicit `tok.SiteID == resolvedSiteID`
assertion before a session is issued.** This is the load-bearing invariant of the
per-site namespace and deserves its own test.

**Login-CSRF.** Nothing otherwise binds a challenge to the browser that asked for it.
Mallory requests a code for *her own* address at Alice's site, gets the victim to load
her magic link, and the victim is silently signed in **as Mallory** — then types their
data into Mallory's account. Fix: `POST /signin` sets a `__Host-shlogin` nonce cookie
and stores `nonce_hash` on the row; verify refuses unless it matches.

**Mail-scanner link burn.** A magic link is a GET, and Outlook SafeLinks, Proofpoint
and Mimecast *fetch every link they see*. A GET that consumes the token is burned
before the human clicks — the single most common real-world magic-link failure. The
link must land on an HTML page that POSTs on a click; **never mutate, and never set
`used_at`, on GET.**

### 6.1 Google, later — and as a proof method, not a second identity

When it lands: OIDC authorization-code + PKCE, one platform-wide client. Verify `iss`,
`aud`, `exp`, `nonce` **and `state`** (the first draft omitted `state`, which is the
control that actually protects the callback). With one shared client the `redirect_uri`
is the platform's and must bounce to the site origin — **that bounce is an open
redirect and a code leak unless validated against the site's registered
`member_origin`.**

Key on `provider_sub`, never on the email string: `provider_sub` is stable, emails are
recycled and mutable. The takeover it prevents — Mallory's Google account creates a
member row, she then changes her Google email to `victim@corp.com`, and on next login
her row claims the victim's address and any allowlist grant that goes with it. Never
auto-update a member's email from an IdP without an independent round-trip to that
mailbox, and require *explicit authenticated linking* rather than implicit merge on
email match. Note Gmail's `+tag` and dot aliasing make email equality a poor key in
general.

`go.mod` currently carries only `lib/pq`, `x/crypto` and `x/net` — no JWT/OIDC library.
Either accept a dependency or hand-roll JWKS fetch/cache + RS256. Budget accordingly.

---

## 7. Gating whole pages — and the bypass that makes it decorative

`access_mode` extends the nginx `auth_request` gate. Per **P1 that gate does not
exist**, so this is *build*, not *extend*.

- `public` — as today.
- `password` — today's shared bcrypt view-lock, re-keyed to `site_id` per P2.
- `invite` — 401 unless a valid member session cookie is present; nginx maps 401 to
  the hosted sign-in page.

Three things must be true or the gate is theatre:

1. **Fail closed.** `viewSessionOK` returns `true` when the site key is missing from
   the in-memory map (`viewauth.go:103`). `siteFromHost()` takes the first DNS label
   (`viewauth.go:56`), so on `sites.simple-host.app` it returns `"sites"` — never a
   real site, so **the gate currently allows everything**. Resolve host → `site_id`
   from the DB with a cache, and deny on unresolvable.
2. **Gate the old URL too.** Files are still served at
   `sites.simple-host.app/<handle>/<site>/`. Unless that block also honours
   `access_mode`, the entire gate is bypassed by using the old link. The shared host
   must `301` to `member_origin` for any non-public site.
3. **No caching.** `Cache-Control: private, no-store` + `Vary: Cookie` on gated
   responses, and verify no proxy cache holds 200s from before the flip.

Also: the hosted sign-in page needs `frame-ancestors 'none'`. Today only the admin UI
sets a CSP (`ui.go`); `SecurityHeaders` (`ratelimit.go:133`) sets `nosniff` and nothing else.

---

## 8. Abuse, cost, privacy

**Invite-only is itself the abuse control.** Because only allowlisted addresses are
ever mailed, v1 has no open sign-up, so the mail-bomb vector and the enumeration-timing
problem largely disappear. That is the main reason `open` mode is deferred.

- **Mail must not be able to take down platform sign-in.** Member mail gets its own
  sending subdomain and its own Resend project/key. If a tenant gets the sending
  reputation suspended, owners can still log in and fix it — with one shared mailer
  (`main.go:68`, `user.go:146`) they could not.
- Caps per *account* and per *recipient globally*, not just per site — site creation is
  free, so a per-site cap is 50× weaker after 50 sites. Note the existing limiters are
  in-memory and per-process (`ratelimit.go`), so they weaken silently on a second instance.
- The existing `emailLimiter` is consumed on the **verify** path too (`user.go:210`),
  so spamming wrong codes locks the victim out. Separate the send counter from the
  verify-attempt counter.
- **Anti-phishing:** sent from the platform's domain, never the site's; names the site
  and its exact origin. From-name `"Alice's Shop via simple-host"` — the Google Groups
  pattern; safe, and a large trust delta. The site name is attacker-influenced and the
  existing template does no escaping (`resend.go:45`) — **escape it.**
- **Storage caps are a v1 requirement, not an open question:** per-member byte cap,
  per-site member cap, per-site total. Otherwise strangers can fill the disk.
- **Ban `member_id` on public collections.** A `/mine` filter is not an ACL — the base
  collection endpoint stays world-readable, so anyone given `/mine` will store private
  orders in a public collection. Hence collections are out of v1 entirely (§9).
- **The tenant is the attacker of its own members.** Any script the owner includes — an
  ad tag, a CDN, analytics — has full session access on that origin. Must be stated in
  `privacy.html` and in the skill.
- **Third-party personal data.** Update `privacy.html`; member self-erasure; owner
  export as a dashboard button; cascade on site deletion.

---

## 9. v1 cut list

**Cut:** bans (delete already revokes) · a separate export endpoint (the list endpoint
returns JSON; export is a button) · `ip_hash`/`ua_hash` and "recent sign-ins" —
hashed IPv4 is not anonymous (2³² is brute-forceable), so it *increases* the personal
data held to render a screen nobody opens · `collection_items.member_id` and
`/collections/{c}/mine` (§8) · Google, `provider`, `provider_sub` (§6.1) ·
`members_enabled` (redundant with `access_mode`) · sliding expiry, rotation,
`absolute_exp` (§5) · the mandatory `X-SH-Member` header (§5) · `open` and `closed`
policies — **v1 is allowlist-only**.

**Keep:** `DELETE /members/me` · `me/data` · the page gate · one owner endpoint.

**Also rejected:** per-site owner-managed passwords (storage, reset, reuse) · stateless
JWTs (unrevocable; Postgres is already here) · members on the shared host (§2,
impossible) · Auth0/Clerk/Firebase (per-MAU cost; the thesis is one Go binary) ·
giving every visitor a platform account (conflates principals, leaks the user list).

---

## 10. The agent story

This platform is driven by agents reading SKILL.md. The smallest thing an agent must
learn is **five calls** — `signin`, `verify`, `me`, `logout`, `me/data` — plus one
sentence: *same-origin fetch sends the cookie automatically; do not add
`credentials:'include'`.*

**Better: ship `members.js`, matching the existing `comments.js` / `feedback.js`
widgets.** It renders the sign-in box and exposes `SH.member` and `SH.onMember(cb)`.
The agent writes zero auth code — the only way auth code inlined by an LLM into a
single `<script>` tag is going to be correct.

And the skill should lead with: **if you just need "only these people can open it",
write no auth code at all** — the owner flips `access_mode` and nginx does the rest.

Docs that become actively wrong and must change: `website-deploy-builder/SKILL.md`
says twice that per-user accounts mean "go use Supabase"; `connect-domain/SKILL.md`
says privacy requires a purchased domain; plus `llms.txt`, `install.html`,
`openapi.yaml` (+ `openapi.json` regen). `scripts/check-docs-sync.sh` demands exact
bidirectional route↔spec equality and uses the literal Go pattern strings — the brace
names are `{sitename}` and `{coll}`, not `{site}` and `{c}`.

---

## 11. What makes it magical

1. **The invite email is the whole feature.** Owner adds `ravi@x.com`; Ravi gets
   *"Alice shared a private page with you"* and the link signs him in. Sign-up,
   sign-in and sharing collapse into one action — the cold "type your email, wait for
   a code" screen never appears on the invited path.
2. **No code is generated.** *"Make this page private, only mum@x.com and dad@y.com"* →
   one API call, zero lines written. The feature is invisible.
3. **The sign-in page looks like the site** — inherit title, favicon and accent
   (already done for `SH_COMMENTS`). Zero config, looks bespoke.
4. **Tell the owner what they'd actually ask:** *"3 of the 4 people you invited have
   opened this."* That's the good version of "recent sign-ins" — no IPs, no user agents.
5. **The URL upgrade as a reward**, with old links still working.

---

## 12. Phasing and honest effort

Estimates are developer-days including verification on the playground. The first
draft's implicit ~2 weeks was about half of reality.

| Phase | Work | Days |
|---|---|---|
| **P — prerequisites** | Fix P1–P4: deploy the gate, re-key view-lock to `site_id`, exempt member origins in `legacyhost.go`, backfill missing DDL | **3–4** |
| **A — write-only collections** | `GET` owner-only + dashboard submissions view. Independent, ship first | **1–2** |
| **0 — own address** | New registrable domain + wildcard cert; nginx block ordered *before* the legacy regex; `member_origin` + `retired_origins`; 5th branch in `authorizeStateOrigin`; `/v1/` proxy on the new block; prefix-free routes; analytics host attribution (`ingest.go:435`); `--` charset enforcement; 301 from the old path | **3–4** |
| **1 — invite-only sign-in** | Tables; session middleware; 6 visitor + 3 owner endpoints; `me/data` via a *refactored* state engine; hosted sign-in page; invite email template + `Sender` method; `access_mode` gate; `members.js`; openapi + llms.txt + 2 skills; `privacy.html` | **8–11** |
| **2 — Google** | JWKS/RS256 or a new dep; PKCE, `state`, nonce; the redirect-bounce validation; identities table | **4–6** |
| **3 — open mode, per-member collections, webhooks, BYO OAuth client** | | **5–7** |

**≈ 24–34 days total**, of which **≈ 15–21** reaches a shippable invite-only feature.

**Deployment reality:** this ships to the **playground (`simple-host.app`) only.** The
ideaflow box runs an older binary against an older schema (`users` has no `handle`;
`sites` has no `custom_domain`) and cannot run a current-source binary at all —
migrating it is a separate, larger project. Additive DDL must land **before** the
binary, as it did on the v3 cutover, or every authenticated request 500s.

---

## 13. Open questions

- Which registrable domain to buy for site origins, and who submits the PSL request.
- If a site has both `member_origin` and a custom domain, they are two origins with two
  independent session stores — logout on one leaves the other live. **Proposal:** once a
  custom domain exists it is canonical; the platform host 301s to it and its sessions
  are revoked.
- Free-tier cap on members per site.
- Does the AI builder learn to scaffold an invite-only area, or is it skill-only at first?
