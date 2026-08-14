# UNIFY: one account, two credential strengths

Status: implementable design. Do not implement from this file in this worktree.  
Date: 2026-08-14  
Audience: an implementer who will not be asked questions  
Product decision (settled, not to be relitigated): there is no visitor principal and no owner principal. One `users` row is the person. They sign in with an emailed code or with Google/GitHub. If they have sites they own those sites; if they reply on someone else's site they are a visitor to it. Same account.

This reverses `SPEC.md` §3 / §13.1 ("Visitors ≠ users. New `visitors` table. Magic-link remains the only owner path."). The live visitor stack (`internal/handler/oauth.go`, `internal/handler/visitorsession.go`, `internal/db/visitors.go`, `internal/oauth/`) is deployed and unused (`visitors` has 0 rows). `users` has 14 real rows with real API keys and real sites. Those 14 must keep working.

The constraint that is not negotiable: **a browser session earned by signing in on a hosted site must not carry owner powers.** One identity, two credential strengths.

---

## Recommendation

One `users` row is the only principal. Email-code (`POST /v1/auth` + `/v1/auth/verify` in `internal/handler/user.go`) and Google/GitHub all resolve to it. The empty `visitors` table is dropped. Provider accounts live in `oauth_identities`, keyed by `(provider, provider_user_id)`. A provider-asserted **verified** email may link to an existing `users.username`; a missing or unverified email is refused and creates no row.

Two credentials, one identity. `X-API-Key` is the owner credential — the only thing `auth.Middleware` (`internal/auth/middleware.go`) accepts. Every deploy/delete/rollback/owner-data route is wrapped with it. A site sign-in mints the host-and-site-scoped `__Host-sh_vsess` cookie that `visitorWriteOK` (`internal/handler/visitorsession.go`) accepts for state/collections writes only. That cookie is never set on the apex and never read by `auth.Middleware`. Dashboard OAuth reuses `/?token=` + `verifySignIn` (`index.html` already redeems it) to disclose the API key. A wedding guest has an account but is not handed a key, a handle, or a dashboard session.

The 14 existing owners and their keys are untouched. We now store emails for anyone who signs in; one person is correlatable across every site they use. Site-scoped sessions still stop acting-as. They do not restore unlinkability.

---

## Exact DDL

Live visitor tables (`db/schema.sql` as shipped on `feat-visitor-auth`, applied via `db/migrations/visitor-oauth.sql`): `visitors`, `oauth_states`, `visitor_sessions`, `visitor_establish_tokens`, plus `sites.allow_anonymous_writes`. `visitors` has 0 rows, so sessions and establish tokens are empty by FK. `oauth_states` may hold in-flight 10-minute rows; do not drop it.

`users` does **not** grow provider columns. `users.username` stays the email and stays `NOT NULL`. A Google-first signup uses Google's verified email as `username`. If a provider ever gives no email, or gives one it has not verified: **refuse the sign-in**. No row, no synthetic `google:<sub>` username, no nullable-username carve-out. That person uses `POST /v1/auth` (mailbox proof) or verifies the address at the provider and retries.

### Disposition of the four deployed visitor tables

| Table | Fate | Why |
|---|---|---|
| `visitors` | **DROP** | Unused identity table. The principal is `users`. 0 rows, nothing to migrate. |
| `oauth_states` | **KEEP, ALTER** | Still the in-flight authorization-code row (`OAuthHandler.start` / `callback` in `internal/handler/oauth.go`). `site_id` becomes nullable so a dashboard (owner-purpose) flow can exist. Add `purpose`. |
| `visitor_sessions` | **DROP and recreate** | Empty. Recreate with `user_id REFERENCES users(id)` instead of `visitor_id REFERENCES visitors(id)`. Keep the table name: the HTTP cookie and `/v1/visitor/*` paths stay, so the code change is the FK, not a rename cascade. |
| `visitor_establish_tokens` | **DROP and recreate** | Empty. Recreate unchanged except the session FK now points at the new `visitor_sessions`. Keep the name. |

`sites.allow_anonymous_writes` stays. It is an operator hatch, not a principal.

Apply as `db/migrations/unify-identities.sql` and append the same statements to `db/schema.sql`. Hand-applied, `IF EXISTS` / `IF NOT EXISTS`, same style as `db/migrations/visitor-oauth.sql`.

```sql
-- unify-identities.sql
-- Safe to re-run. Destructive only to empty visitor identity/session tables.

-- 1. Empty identity + session tables. visitors has 0 rows; the two children
--    are empty by FK. oauth_states is NOT dropped (may hold a live 10-minute
--    flow).
DROP TABLE IF EXISTS visitor_establish_tokens;
DROP TABLE IF EXISTS visitor_sessions;
DROP TABLE IF EXISTS visitors;

-- 2. Provider account ↔ users. Durable key is (provider, provider_user_id),
--    never email. email / email_verified are the snapshot at last successful
--    identify, for audit and for the first-link decision; they are not a
--    uniqueness key.
CREATE TABLE IF NOT EXISTS oauth_identities (
  id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id          UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider         TEXT NOT NULL CHECK (provider IN ('google', 'github')),
  provider_user_id TEXT NOT NULL,
  email            TEXT,
  email_verified   BOOLEAN NOT NULL DEFAULT FALSE,
  created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (provider, provider_user_id)
);
CREATE INDEX IF NOT EXISTS oauth_identities_user_id_idx
  ON oauth_identities (user_id);

-- 3. Site-scoped browser sessions. Cookie value is id (32 raw bytes, hex
--    on the wire), same as today. user_id is the one principal.
CREATE TABLE IF NOT EXISTS visitor_sessions (
  id              BYTEA PRIMARY KEY,
  user_id         UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  site_id         UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  host            TEXT NOT NULL,
  created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  expires_at      TIMESTAMPTZ NOT NULL,
  idle_expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS visitor_sessions_expires_idx
  ON visitor_sessions (expires_at);
CREATE INDEX IF NOT EXISTS visitor_sessions_user_idx
  ON visitor_sessions (user_id);

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

-- 4. In-flight flows now serve two entry points. purpose='site' is today's
--    hosted-page flow (site_id required). purpose='owner' is dashboard
--    sign-in (site_id NULL; callback issues a /?token= handoff, not a cookie).
ALTER TABLE oauth_states
  ALTER COLUMN site_id DROP NOT NULL;

ALTER TABLE oauth_states
  ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'site';

-- Tighten the CHECK if a previous apply already added the column without it.
ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_provider_check;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_provider_check
  CHECK (provider IN ('google', 'github'));

ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_purpose_check;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_purpose_check
  CHECK (purpose IN ('site', 'owner'));

ALTER TABLE oauth_states
  DROP CONSTRAINT IF EXISTS oauth_states_purpose_shape;
ALTER TABLE oauth_states
  ADD CONSTRAINT oauth_states_purpose_shape
  CHECK (
    (purpose = 'site'  AND site_id IS NOT NULL AND host <> '')
    OR
    (purpose = 'owner' AND site_id IS NULL)
  );
```

No `ALTER` on `users`. No `ALTER` on `auth_tokens`. No backfill. The 14 owner rows stay exactly as they are, including `api_key` and `handle`.

`SweepVisitorAuth` (`internal/db/visitors.go`) keeps deleting expired `oauth_states`, `visitor_establish_tokens`, and `visitor_sessions`. It gains `oauth_identities` is durable — do not sweep it. Drop `UpsertVisitor`; replace with `GetOAuthIdentity` / `InsertOAuthIdentity` / `TouchOAuthIdentity` against `oauth_identities`, plus the existing `CreateUser` / `GetUserByUsername`.

If a future provider has no email at all (Apple private relay with hide-my-email is still an email; a hypothetical email-less IdP is not): that provider cannot create a `users` row until `users.username` is allowed to be something other than an email. That is a later schema change. Do not invent a placeholder now.

---

## The credential boundary

### What the two credentials are

| Credential | How it is issued | What it names | Where it is stored |
|---|---|---|---|
| **API key** | `verifySignIn` (`internal/handler/user.go`) after mailbox proof, **or** the owner-purpose OAuth callback minting a one-time `auth_tokens.link_token` that the dashboard redeems the same way. `CreateUser` always generates one (`users.api_key` is `NOT NULL UNIQUE`) but site-purpose OAuth **never discloses it**. | The `users` row. Possession is owner power. | `X-API-Key` request header. Dashboard keeps it in `localStorage` (`internal/handler/static/index.html`). MCP / CLI send the header. Never a cookie, never a URL after the `/?token=` hop is redeemed. |
| **Browser session** | Site-purpose OAuth callback → `visitor_sessions` + `visitor_establish_tokens` → `GET /v1/visitor/establish` (`establishVisitor` in `internal/handler/visitorsession.go`) sets `__Host-sh_vsess` (or `sh_vsess` on local HTTP). | That `users` row, **on one `site_id` + one `host`**, for 14 idle / 30 absolute days. | Host-only cookie on the content host or the custom domain. **Never on the apex** (`isVisitorApexHost`). |

`ADMIN_API_KEY` is unchanged: `auth.Middleware` still constant-time-compares it and attaches the real admin UUID from `EnsureAdminUser` (`internal/db/collections.go`). OAuth must never link an identity to the `username = 'admin'` row.

### The single enforcement point

**`auth.Middleware` in `internal/auth/middleware.go` is the owner-power boundary.** It is the only function that may put a `*db.User` into request context (`userContextKey` / `auth.GetUser`). It reads **only** `X-API-Key`. It does not look at cookies, `Authorization`, or query params.

Every route that can deploy, delete, roll back, bind a domain, set a view password, read analytics, generate a site, or read `/v1/me` is wrapped with `authMiddleware(...)` in a `Register` method. A session-only request never gets a user in context. Handlers that call `auth.GetUser` then 401.

This must not grow a cookie path. Custom domains already reverse-proxy `/v1/` to the same binary (`SPEC.md` §1.1). A `__Host-sh_vsess` cookie on `recipes.brand.com` **is sent** to `https://recipes.brand.com/v1/sites/{name}` (create/delete/…). Today that is 401 because middleware ignores the cookie. Teaching middleware to accept the session cookie is how a guestbook page escalates against its visitors. Do not do it. Do not add `auth.MiddlewareFromSession`. Do not make `GetUser` fall through to `visitorCookieValue`.

`visitorWriteOK` is a **different** gate: identified writes to public scratch (state/collections). It may accept the cookie. It must not write `userContextKey`. It already prefers `X-API-Key` via `resolveWriterKey` and must keep doing so — a presented-but-wrong key is 403 `writer_forbidden`, not a fallthrough to the cookie (`SPEC.md` §4.4 step 1).

### Route groups × accepted credential

| Route group | API key | Browser session | Notes |
|---|---|---|---|
| Owner management (list below) | **required** | **reject** | Wrapped with `auth.Middleware`. Session is invisible. |
| `PUT`/`PATCH` `…/state`, `POST` `…/collections/{coll}` (both `/v1/sites/…` and `/v1/u/{handle}/sites/…`) | accept, if key is this site's owner or admin | accept, if session `site_id` + `host` match and `X-SH-CSRF: 1` | `visitorWriteOK`. Origin + view-lock still first. |
| `GET` `…/state`, `GET` `…/collections/{coll}` | not required | not required | Origin + view-lock only. Unchanged. |
| `OPTIONS` on state/collections | none | none | CORS preflight. Must keep `X-SH-CSRF` in Allow-Headers. |
| `POST /v1/auth`, `POST /v1/auth/verify` | none | none | Establish owner identity. Verify **returns** the API key. |
| `GET /v1/auth/oauth/providers`, `GET /v1/auth/oauth/{provider}`, `GET /v1/auth/oauth/{provider}/callback` | none | none | Start/callback. Callback issues a session **or** a `/?token=` handoff; it never returns an API key in the 302. |
| `GET /v1/visitor/establish` | none | none (one-time `once`) | Sets the session cookie on a non-apex host. |
| `POST /v1/visitor/logout` | none | this cookie | Deletes that session row. CSRF header or JSON content-type. |
| Public catalog / health / static / skills / templates / showcase / view-lock / tls-ask | none | none | Not owner powers. Session irrelevant. |

### Owner routes that must reject a session-only caller

Exhaustive against every `authMiddleware(` wrap in this tree (`cmd/server/main.go`, `internal/handler/user.go`, `internal/handler/site.go` on `feat-visitor-auth`, `internal/handler/generate.go`). A missed wrap is the vulnerability; adding a new owner route means adding it to this list and wrapping it.

| Method | Path | Wrapper | Why session must not pass |
|---|---|---|---|
| `GET` | `/v1/me` | `UserHandler.Register` | Discloses id, username (email), handle, admin bit. |
| `GET` | `/v1/sites` | `SiteHandler.Register` | Lists the caller's sites (admin: all sites). |
| `POST` | `/v1/sites/{sitename}` | same | Create / first deploy. |
| `PUT` | `/v1/sites/{sitename}` | same | Redeploy. |
| `DELETE` | `/v1/sites/{sitename}` | same | Destroy the site. |
| `POST` | `/v1/sites/{sitename}/files` | same | JSON create. |
| `PUT` | `/v1/sites/{sitename}/files` | same | JSON redeploy. |
| `GET` | `/v1/sites/{sitename}/versions` | same | Version history. |
| `PUT` | `/v1/sites/{sitename}/active-version` | same | Rollback. |
| `GET` | `/v1/sites/{sitename}/analytics` | same | Owner-only traffic counts. |
| `PUT` | `/v1/sites/{sitename}/visibility` | same | Showcase listing. |
| `PUT` | `/v1/sites/{sitename}/view-password` | same | Lock the site. |
| `DELETE` | `/v1/sites/{sitename}/view-password` | same | Unlock the site. |
| `PUT` | `/v1/sites/{sitename}/allowed-origins` | same | Backend-anywhere ACL. |
| `PUT` | `/v1/sites/{sitename}/allow-anonymous-writes` | `auth.Middleware` then `auth.RequireAdmin` | Operator hatch. |
| `POST` | `/v1/sites/{sitename}/domain` | `SiteHandler.Register` | Bind a custom domain. |
| `GET` | `/v1/sites/{sitename}/domain` | same | Read the bind. |
| `DELETE` | `/v1/sites/{sitename}/domain` | same | Drop the bind. |
| `POST` | `/v1/generate` | `GenerateHandler.Register` | Spends model credits as the user. |
| `GET` | `/v1/generate/status` | same | Reads that job. |

There is no `/v1/u/{handle}/…` twin of any owner route. There is no collection-delete or collection-admin route. `GET /internal/*` is proxy-gated, not owner-keyed.

`auth.RequireAdmin` is a second check **after** middleware. It is not an alternative to middleware.

---

## Account-matching rules

`SPEC.md` §3 refused email matching for visitors because a provider-asserted address, used as a join onto a **different** principal, is an account-takeover. That refusal does not survive "one account," but the **attack** does. The defense is not "never match email." The defense is: match email only when the provider **asserts verified**, and even then only to **link an identity onto an already-verified `users` row**. Re-logins never use email.

`users` rows are themselves verification-gated. `requestSignIn` does not create a user (`internal/handler/user.go`); `verifySignIn` lazy-creates only after the code or magic link comes back through the mailbox. There is no unverified `users` row an attacker can pre-seed with the victim's address. The `username = 'admin'` row is not an email and is not linkable.

### Identify

Still HTTPS userinfo, still no `go-oidc`, still discard the access token. Extend `oauth.Identity` (`internal/oauth/provider.go`) with `Email string` and `EmailVerified bool`. Stable id is still Google `sub` / GitHub numeric `id` as decimal text. No `sub`/`id` → 502, same as today (`OAuthHandler.callback`).

Google (`internal/oauth/google.go`): scopes become `openid email` (drop `profile`; we do not store name or picture). Parse `sub`, `email`, `email_verified` from userinfo. `EmailVerified` is true only when the JSON boolean is true.

GitHub (`internal/oauth/github.go`): scopes become `read:user user:email`. Keep `GET /user` for the numeric id. Add `GET /user/emails`. Pick the first `primary && verified` address, lowercased. Do **not** trust `GET /user`'s `email` field (often null, not a verified flag). If no primary+verified email, `EmailVerified` is false and `Email` is empty.

### Resolve (`resolveUser`, new, called from `OAuthHandler.callback` in place of `db.UpsertVisitor`)

Run inside the same transaction that will insert the session or the handoff token.

1. **Lookup `oauth_identities` by `(provider, provider_user_id)`.**  
   Hit: that is the account. Load `users` by `user_id`. Update the snapshot (`email`, `email_verified`, `updated_at`) if the provider sent new values. **Do not** compare emails. **Do not** change `users.username` (it may collide with another row if the person changed the address at the provider). Done.

2. **No identity row.** Require a usable email:  
   `Email != ""` AND `EmailVerified == true` AND the address matches `validEmail` in `user.go` after `strings.ToLower`.  
   Otherwise: **stop**. 400 HTML on the apex, no redirect, no row, no session, no token. Message stays the generic "Sign-in failed…" page (`writeOAuthHTMLError`). Log `oauth: refused unverified or missing email provider=…` without the address if it was missing; with the redacted local-part if it was present but unverified.

3. **Usable email.** `GetUserByUsername(lower(email))`.  
   - **Found, and `user.Username == "admin"` or `user.IsAdmin`:** refuse (same 400 HTML). Admin is `ADMIN_API_KEY` only.  
   - **Found, otherwise:** `INSERT oauth_identities (user_id, provider, provider_user_id, email, email_verified)`. This is the link. Same human, same account.  
   - **Not found:** `CreateUser(email, GenerateAPIKey(), false)` then insert the identity. This is a new person. `created = true`.

4. **Handle assignment is owner-intent only.**  
   - Site-purpose (hosted page): **do not** call `assignHandle`. `users.handle` stays NULL. A wedding guest does not get `sites.simple-host.app/<local-part>` as a side effect of RSVPing.  
   - Owner-purpose (dashboard) or a later `verifySignIn`: call `assignHandle` if `handle` is NULL, same as today (`UserHandler.verifySignIn`).  
   - First `createSite` / `createSiteFiles`: if the owner key's user still has a NULL handle, assign before building `siteURL` (`commitNewSite` already special-cases a missing handle and falls back to the legacy `name.siteDomain` URL). Assigning here keeps new deploys on the path model.

5. **Never rotate `api_key` on link or re-login.** The 14 existing keys, and any key generated for a guest-created row, stay.

### What this allows and what it refuses

| Situation | Result |
|---|---|
| Email-code owner `x@y.com`, later Google as `x@y.com` with `email_verified=true` | **Same account.** Step 3 links. Key unchanged. |
| Google-first RSVP as `x@y.com` (verified), later dashboard Google as same `sub` | **Same account.** Step 1, no email involved. Dashboard handoff discloses the key that was generated at RSVP and never shown. |
| Google-first RSVP as `x@y.com`, later `POST /v1/auth` + verify for `x@y.com` | **Same account.** `verifySignIn` finds the existing username. Discloses the key. Assigns handle. |
| Google `email_verified=false`, or no `email` | **Refuse.** No row, no link. |
| GitHub with no `primary && verified` email | **Refuse.** |
| Two providers, same verified email | **Same account.** Each `(provider, sub)` is its own `oauth_identities` row pointing at one `users` id. `SPEC.md` cut this for visitors; one-account requires it. |
| Same Google `sub`, email at Google later changes to `z@y.com` | **Same account** (step 1). `users.username` stays `x@y.com`. Snapshot on the identity row updates. No steal of a `z@y.com` user if one exists. |
| Attacker sets an unverified GitHub email to the victim's address | **Refuse** (step 2). This is the SPEC takeover, closed. |
| Attacker tries to pre-create `users` via `POST /v1/auth` with the victim's email | No row is created until verify. Attacker cannot complete verify. No seed. |

Email matching is safe here, and was not safe in `SPEC.md`, because (a) the local username is only minted after mailbox proof or a verified provider assertion, (b) we require the provider's verified flag, (c) the durable key is still `(provider, provider_user_id)`, and (d) a site session still cannot deploy even after a correct link.

---

## Does a visitor get an API key?

**They get a `users` row. They do not get the key handed to them.**

`users.api_key` is `NOT NULL UNIQUE`, so `CreateUser` still mints one. Site-purpose OAuth (`OAuthHandler.callback` when `oauth_states.purpose = 'site'`) writes a `visitor_sessions` row and an establish token, then 302s to `/v1/visitor/establish`. It does not put the key in the 302, the cookie, the establish token, or any JSON the hosted page sees.

They do **not** get a handle, so they have no showcase and no `/v1/u/{handle}/…` URL of their own. They do **not** get a dashboard session. Opening `https://simple-host.app/` still shows the email form; the apex has no visitor cookie (`SPEC.md` §2.2.4, kept).

Owner-intent is explicit and is the only disclosure:

- Dashboard email-code / magic-link (`verifySignIn`) — already returns `api_key`.
- Dashboard Google/GitHub (owner-purpose callback → `/?token=` → the same `verifySignIn`).
- After that they can deploy, and `assignHandle` runs.

Why this side of the tradeoff: the product upside (a guest who later wants a site is already a person, already linked to their Google `sub`) does not require handing a deploy credential to everyone who RSVPs. Issuing the key into `localStorage` from a page we do not control — or into a cookie that page can cause the browser to send to `/v1/sites/…` on a custom domain — is the escalation the constraint forbids. A key that exists only in Postgres, never returned, is not a credential the hosted page can use.

Do not email them the key. Do not show a "your Simple Host account is ready" banner on the RSVP page.

---

## Sign-in surfaces

Same start and callback. Two `return_to` shapes. `purpose` is **derived from `sanitizeReturnTo`**, never taken from a query param the caller can set.

### Hosted site (already shipped)

Unchanged sequence (`SPEC.md` §4.2–4.3, `OAuthHandler.start` / `callback`, `establishVisitor`):

1. Page navigates to `{PUBLIC_BASE_URL}/v1/auth/oauth/{provider}?return_to={page URL}`.
2. `sanitizeReturnTo` accepts content-host `/{handle}/{sitename}` (real site) or a DNS-proven custom domain. Sets `purpose = 'site'`, stores `site_id` + `host`.
3. Callback `resolveUser`s, inserts `visitor_sessions` (`user_id`, not `visitor_id`) and `visitor_establish_tokens`, 302s to `https://<host>/v1/visitor/establish?once=…`.
4. Establish sets `__Host-sh_vsess`, 302s to stored `return_to`. No query mutation. No API key.

### Apex dashboard (new)

The admin UI (`internal/handler/static/index.html`) already redeems `/?token=` via `verifyMagicLink` → `POST /v1/auth/verify` → `localStorage.apiKey`. Reuse that. Do not invent an owner session cookie.

1. Dashboard renders "Sign in with Google/GitHub" as a plain `<a href="/v1/auth/oauth/google?return_to={PUBLIC_BASE_URL}/">` (same pattern as the existing magic-link). Buttons only for names returned by `GET /v1/auth/oauth/providers`.
2. `sanitizeReturnTo` gains an apex branch: host **exact** `publicBaseHost(PUBLIC_BASE_URL)`, scheme https (or http only when `PUBLIC_BASE_URL` itself is http), no userinfo, default port only, `path.Clean` is `/`, query string **empty**. Reject `/install.sh`, `/v1/me`, `/?token=…` supplied by the caller, fragments-as-paths, `www.`, legacy `*.siteDomain`. Sets `purpose = 'owner'`, `site_id = NULL`, `host` unused.
3. Callback `resolveUser`s (owner-intent: `assignHandle` if needed), inserts an `auth_tokens` row via `CreateAuthToken` (`internal/db/queries.go`) for `user.username` with a fresh `link_token` and a random unused `code` (the column is `NOT NULL`; `GetAuthTokenByLink` never compares it). 15-minute TTL, same as email magic-link. **Does not** insert a session or establish token. 302s to `{PUBLIC_BASE_URL}/?token={link_token}`.
4. Existing `verifyMagicLink` redeems it and stores the API key. `created` is true only when `resolveUser` just inserted the `users` row; if the row already existed (email-code owner, or prior RSVP), `created` is false and the key is the existing one.

`isRejectedPlatformHost` currently rejects the apex so a visitor flow cannot bounce there. Keep that rejection for `purpose = 'site'`. The new apex branch is the only way `return_to` may name the apex, and that branch never sets a cookie.

### What changes in start / callback

| Function | Change |
|---|---|
| `OAuthHandler.start` | `sanitizeReturnTo` returns `purpose` as well as `return_to, site_id, host`. `InsertOAuthState` writes `purpose`. Site path: `site_id` required. Owner path: `site_id` NULL. |
| `sanitizeReturnTo` | New apex allow-list above. Existing content-host and custom-domain rules (`SPEC.md` §2.1) stay, including live DNS proof. Apex is still rejected as a **site** `return_to`. |
| `OAuthHandler.callback` | After `resolveUser`: if `st.Purpose == "owner"` → auth-token handoff + 302 to `/?token=`. If `"site"` → today's session + establish 302. Never both. Never `Set-Cookie` on the apex. |
| `db.InsertOAuthState` / `ConsumeOAuthState` | Carry `purpose`; `site_id` is `sql.NullString`. |
| `db.InsertVisitorSession` | `user_id` instead of `visitor_id`. |
| `db.UpsertVisitor` | Deleted. |
| Widgets / templates | No URL change. They already start at `/v1/auth/oauth/{provider}?return_to=…`. |

PKCE S256, single-use state, 10-minute TTL, rate limit on start/callback/establish/logout, HTML-on-apex-no-redirect on failure: all kept.

---

## Scope change and PII

Deployed Google scopes (`NewGoogle` in `internal/oauth/google.go`): `openid profile`. No email stored. That was `SPEC.md` §3 "Storing provider email — CUT."

| Provider | Deployed scopes | New scopes | Stored |
|---|---|---|---|
| Google | `openid profile` | `openid email` | `sub` → `oauth_identities.provider_user_id`; verified email → `users.username` (on create) and `oauth_identities.email`; `email_verified` (must be true or we refuse). Not stored: name, picture, locale, access token. |
| GitHub | `read:user` | `read:user user:email` | numeric id; primary+verified email the same way. Not stored: login, avatar, non-primary emails, access token. |

The old property — "we don't hold visitor PII" — is **gone**. Anyone who signs in on any site leaves an email in `users.username` (and a snapshot on `oauth_identities`). The operator can join every site that person has a `visitor_sessions` row for, and can join them to every site they own. Update the privacy page (`internal/handler/static/privacy.html`) in the same implementation PR; it is not optional documentation.

`profile` is dropped because we were never going to store a display name in this pass. If a later attribution feature wants a public name, that is a new scope conversation.

---

## Route changes

Paths stay. Semantics change where noted. Not behind `auth.Middleware`. Not behind `noticeMW` (browser bodies). Always registered, even with no provider enabled.

| Method | Path | Auth | What changed |
|---|---|---|---|
| `GET` | `/v1/auth/oauth/providers` | Public | Unchanged body `{"providers":[…]}`. Now used by the dashboard as well as widgets. |
| `GET` | `/v1/auth/oauth/{provider}` | Public | `return_to` may be the apex `/` (owner) or a hosted-site URL (site). Everything else in `sanitizeReturnTo` stays. |
| `GET` | `/v1/auth/oauth/{provider}/callback` | Public | Site purpose: 302 to `/v1/visitor/establish?once=…` (same). Owner purpose: 302 to `/?token=…` on `PUBLIC_BASE_URL`. Identity is `resolveUser`, not `UpsertVisitor`. Failure: HTML on the apex, no redirect (same). |
| `GET` | `/v1/visitor/establish` | Public, one-time `once` | Same cookie spec (`SPEC.md` §8). Session row now has `user_id`. Still 400 on apex host. |
| `POST` | `/v1/visitor/logout` | Cookie + CSRF/JSON | Same. Deletes that session, not every session for the user. |
| `POST` | `/v1/auth` | Public | Unchanged. Still does not create a user. |
| `POST` | `/v1/auth/verify` | Public | Unchanged contract. Now also redeems owner-purpose OAuth handoffs. On success, `assignHandle` if NULL (already true). |
| `GET` | `/v1/me` | API key | Unchanged. Session must not satisfy it. |
| Owner management routes | see table above | API key | Unchanged. Do not wrap any of them with cookie auth. |
| `PUT` `PATCH` | `…/state` (both prefixes) | key **or** site session | `visitorWriteOK` looks up `visitor_sessions.user_id` instead of `visitor_id`. Gate logic unchanged. |
| `POST` | `…/collections/{coll}` (both prefixes) | key **or** site session | Same. |
| `GET` / `OPTIONS` | state + collections | Origin (+ view-lock on GET) | Unchanged. |
| `PUT` | `/v1/sites/{sitename}/allow-anonymous-writes` | Admin API key | Unchanged. |

Dashboard HTML (`internal/handler/static/index.html`, embedded — needs a rebuild): add provider buttons. Hosted widgets do not change URLs.

`scripts/check-docs-sync.sh` after the OpenAPI edits. No new `/v1` path is required; the existing OAuth paths stay. Update descriptions in `internal/handler/static/openapi.yaml` so they no longer say "visitor-only" / "does not create a users row."

---

## Migration plan

Live: 14 `users` rows, real keys, real sites. 0 `visitors` rows. Visitor HTTP is mounted and unused.

Ordered. Each step is independently deployable. Nothing in steps 1–3 is destructive to owner data.

1. **Schema (`unify-identities.sql`).** Drop the three empty tables, recreate sessions/establish with `user_id`, create `oauth_identities`, alter `oauth_states`. `users` / `auth_tokens` / `sites` / keys untouched.  
   **Reversible:** drop `oauth_identities`; drop the new session tables; recreate the old `visitors` + `visitor_sessions(visitor_id)` + establish DDL from `visitor-oauth.sql`; `ALTER oauth_states DROP COLUMN purpose`, restore `site_id NOT NULL` (only if no owner-purpose row exists — there cannot be any until step 2).

2. **Identity + callback branch, no dashboard UI yet.** `resolveUser`, Google/GitHub scope + userinfo change, `InsertVisitorSession(user_id)`, owner-purpose handoff. Site-purpose path behaves as today except the session names a `users` row. Existing 14 keys keep working because nothing reads them differently. `auth.Middleware` is not edited.  
   **Reversible:** revert the binary. In-flight site sessions become unreadable after revert (0 of them in prod today). Owner rows created by a guest Google sign-in during this window **remain** (a `users` row with a key nobody was shown). Do not delete them on rollback; they are valid accounts.

3. **Dashboard buttons + privacy page + OpenAPI/llms/skills wording.** Rebuild because HTML is embedded.  
   **Reversible:** revert the binary. Schema can stay.

4. **Do not** rotate the 14 keys. **Do not** assign handles to anyone who has one. **Do not** flip `WRITE_AUTH_MODE` as part of this work (that is still the independent `SPEC.md` §10 step 6 operator decision).

Nothing here is a data backfill. Nothing here rewrites disk. Deploy is a SQL file + a binary swap.

---

## What we lose

`SPEC.md` bought an identity that was **not** a `users` row, held **no** email, and could not be joined to an owner or to the same human on another site except by the operator correlating `(provider, provider_user_id)` across `visitor_sessions` — and even that join was useless to site A, because sessions were site-scoped and pairwise public ids were cut.

One account gives that up, plainly: **a person is correlatable across every site they sign into, and to every site they own, by `users.id`.** We also now store their email. The "we don't hold visitor PII" sentence is false after this ships.

What can be preserved cheaply:

- **Site-scoped, host-scoped sessions — keep.** They are a write boundary (`visitorWriteOK`), not a privacy boundary. Signing into Alice's RSVP still must not write Bob's guestbook. That is cheap and already built.
- **No cookie on the apex — keep.** That is the other half of "session ≠ owner powers."
- **No `actor` column, no public stable id on writes — keep cut.** We will not publish `users.id` (or the email) into `collection_items` or state. Correlation exists in Postgres for the operator; it is not handed to every co-tenant page.
- **Pairwise HMAC public ids — do not add.** They do not survive "one account" as a privacy property (the operator, and any future "my comments across sites" feature, can join on `user_id`). They also do not stop acting-as. Not cheap enough to be worth a lie.

If we later want public attribution that is not a global join key, that is a new design. It is not recovered by this one.

---

## Rejected alternatives

### 1. Teach `auth.Middleware` to accept the session cookie

Looks like "one sign-in, the dashboard just works." It breaks the non-negotiable constraint. Custom domains proxy `/v1/` to this binary. A session cookie set on `recipes.brand.com` for the RSVP would be sent to `POST /v1/sites/{name}` on that host. Middleware that treats the cookie as a user would let the hosted page deploy or delete that person's sites. `CORS` on management routes is `*` specifically because it assumes header auth and no ambient cookie (`internal/handler/cors.go`). A cookie-accepting middleware also makes those routes CSRF-able. Rejected.

### 2. Keep `visitors` and join to `users` at request time

A shadow principal with an email-merge step is the design the owner just reversed. It reintroduces two codes of identity, two matching policies, and a "promote visitor to user" ritual. The unused table has 0 rows, so there is no data reason to keep it. Rejected.

### 3. Disclose the API key from the site-purpose callback

"They have an account, hand them the key, they are an owner." Any hosted page — including a malicious co-tenant on `sites.simple-host.app`, or an XSS on a custom domain — would then be in a position to exfiltrate a deploy credential from people who only meant to RSVP. The establish `once` already travels in a URL (`SPEC.md` residual risk 9). Putting a key behind it is how RSVP sign-in becomes account takeover. Rejected. The key is minted (schema) and disclosed only on owner-intent.

### 4. Link on email without the verified flag

The classic pre-account-takeover (`docs/designs/oauth2-authentication.md` §8). GitHub in particular will serve unverified addresses. `SPEC.md` was right to fear this; it was wrong only in treating that fear as a reason to never match a **verified** Google email to an existing owner. We require `email_verified` / `primary && verified` and refuse otherwise. Silent merge of unverified mail is rejected.

---

## Open questions

1. **Privacy page and transactional email.** We will hold guest emails. Does the existing `privacy.html` / any "we sent you a code" copy need legal review, or is a factual update enough? Implementation should update the page either way; whether to mail guests anything (we say no) is settled here.

2. **Admin census.** There is no "list users" route today. After this, `users` will grow a row per signed-in guest. Does the operator want a count or a purge of handle-NULL, zero-site rows? Not needed to ship. Do not add a user-delete API in this pass (it would cascade sites if pointed at a real owner).

3. **Handle on first deploy vs. first owner-intent sign-in.** This doc assigns on owner-purpose OAuth, on `verifySignIn`, and on first create if still NULL. That is enough. A guest who never comes to the dashboard and never deploys stays handle-less. Confirm that is the product, not "give everyone a showcase."

4. **GitHub in v1 of unification.** The deployed visitor stack already registers GitHub. This doc keeps it, with `user:email`. If the operator would rather ship Google-only until GitHub email-matching has been watched in production, that is an env-var decision (`GITHUB_OAUTH_*` unset), not a schema decision.

5. **Unlink / account-chooser.** `oauth_identities` allows many providers per user. There is no UI to unlink, and no "this email is already a different Google `sub`" recovery story beyond "sign in with the original method." Fine for 14 owners and empty visitors. Not designed here.

6. **A lint that every new `/v1/sites/{sitename}` route (except `state` / `collections` / `OPTIONS`) is wrapped with `auth.Middleware`.** The exhaustive table above is the current list; the next missed wrap is the next hole. A `scripts/check-docs-sync.sh`-style grep is worth doing in the implementation PR. Not a runtime check.

7. **`WRITE_AUTH_MODE=on` timing.** Unrelated to unification, still an operator flip. Unification does not require it and must not silently turn it on.

---

## File map (for the later implementer; do not write this code now)

| Area | Files |
|---|---|
| DDL | `db/schema.sql`, `db/migrations/unify-identities.sql` |
| Stores | `internal/db/visitors.go` (replace `UpsertVisitor`; session insert takes `user_id`), new helpers on `oauth_identities` |
| Identify | `internal/oauth/provider.go`, `google.go`, `github.go` |
| HTTP | `internal/handler/oauth.go` (`sanitizeReturnTo`, `callback` branch), `internal/handler/visitorsession.go` (`visitorWriteOK` unchanged in policy) |
| Owner handoff | `internal/handler/user.go` (`verifySignIn` already sufficient), `internal/db` `CreateAuthToken` |
| Boundary | `internal/auth/middleware.go` — **do not change the credential it accepts** |
| Dashboard | `internal/handler/static/index.html` |
| Docs | `openapi.yaml` + regenerated `openapi.json`, `llms.txt`, skills, `privacy.html`, `CLAUDE.md` one-line note that visitors are users |
| Sync | `bash scripts/check-docs-sync.sh` |
