# Design: OAuth2 / OIDC Social Login (GitHub, Google, …)

Status: Proposed
Author: design doc
Date: 2026-06-25
Scope: `simple-host` (one Go binary + Postgres + disk; `internal/handler`, `internal/auth`, `internal/db`, `internal/config`)

---

## 1. Summary

Add OAuth 2.0 / OIDC social sign-in (GitHub and Google at minimum, easily extensible to
Microsoft/Apple) **alongside** the existing email magic-link + 6-digit-code auth. A user clicks
"Sign in with GitHub/Google" in the admin UI, completes the provider's consent screen, and lands
back on simple-host already signed in — with the *same durable `api_key` credential* the rest of
the system already understands.

The implementation uses the OAuth 2.0 **Authorization Code flow with PKCE** as a **confidential
(server-side) client**, with `state` for CSRF and (for Google/OIDC) `nonce` for ID-token replay
protection. It is built on `golang.org/x/oauth2` + `github.com/coreos/go-oidc/v3` — the standard,
narrow building blocks — not an all-in-one framework. New routes mount on the existing single
`http.ServeMux` under `/v1/auth/oauth/...`, deliberately **avoiding the proxy-reserved `/api*`
prefix** and using **server-side redirect flow** (no browser `fetch`, so the broken CORS preflight
at the proxy is irrelevant).

The single highest-stakes design decision is **account linking**: an OAuth identity is linked to an
existing `users` row **only on a provider-asserted *verified* email**, to avoid the classic
**pre-account-takeover / email-collision** attack (Section 8).

---

## 2. Goals / Non-goals

### Goals
- Sign in with GitHub and Google from the admin UI at `/`.
- Reuse the existing identity model: an OAuth login resolves to a `users` row and yields that user's
  `api_key`, the credential every other endpoint already checks (`internal/auth/middleware.go`).
- Providers individually **toggleable** by config — set the client ID/secret pair to enable a
  provider; absent config = provider hidden/disabled.
- A documented, low-risk path to add Microsoft, Apple, GitLab, etc. (all OIDC-or-OAuth2, same shape).
- Keep the "one binary, no router lib, no migrations framework, env-driven config" conventions intact.

### Non-goals
- Replacing or deprecating email auth. Both coexist permanently.
- A full session framework / SSO / SAML / enterprise IdP federation.
- Changing the `X-API-Key` model for the REST/agent surface. Agents keep using `api_key`. OAuth is a
  *human, browser* on-ramp to obtaining that key.
- OAuth where simple-host is the **provider** (issuing tokens to third parties). Out of scope; this
  doc is simple-host as a **client/relying party**.

---

## 3. Current auth (grounded in the code)

| Concern | Where | Notes |
|---|---|---|
| Credential check | `internal/auth/middleware.go:32-68` | Reads `X-API-Key`; `ADMIN_API_KEY` short-circuits to synthetic `&db.User{ID:"admin"}`; otherwise `db.GetUserByAPIKey`. |
| Request sign-in | `internal/handler/user.go:85-156` (`POST /v1/auth`) | Lower-cases email, **creates the `users` row up-front** with a freshly minted `api_key` (never disclosed here), stores a 6-digit `code` + 24-byte `link_token` in `auth_tokens` (15-min TTL), emails both via Resend. |
| Verify | `internal/handler/user.go:164-230` (`POST /v1/auth/verify`) | Accepts `{token}` (magic link) **or** `{email,code}`; constant-time code compare; `attempts` capped at `maxCodeAttempts=3`; on success returns the user's `api_key`. |
| Key generation | `internal/auth/middleware.go:23-30` | `GenerateAPIKey()` = 32 random bytes, hex. |
| Routes | `internal/handler/user.go:72-76` | `POST /v1/auth`, `POST /v1/auth/verify`, `GET /v1/me`. Registered in `cmd/server/main.go:63`. |
| Users schema | `README.md:57-63` / `internal/db/models.go:5-11` | `users{id uuid, username(=email) unique, api_key unique, is_admin, created_at}`. |
| Auth tokens schema | `README.md:82-91` | `auth_tokens{id, email, code, link_token unique, expires_at, used_at, attempts, created_at}`. |
| Config | `internal/config/config.go` | Env-driven; `DB_DSN` + `ADMIN_API_KEY` required, rest defaulted. |
| Admin UI | `internal/handler/ui.go:67-80`, `internal/handler/static/` | `GET /` serves the embedded admin UI; login is "paste your api_key". HTML is embedded → UI changes need a rebuild. |

**Key invariant to respect:** today, `api_key == identity`. There are no sessions and no
password — possession of the `api_key` *is* being the user. The magic-link verify endpoint is
literally a way to retrieve your own `api_key` after proving mailbox control. OAuth should slot into
exactly that role: a new way to prove "I am the human who owns email X" and then receive X's `api_key`.

**Platform constraints (from `/root/workspace/CLAUDE.md`):**
- The fronting nginx proxy **reserves `/api` and `/api/*`** — those never reach the app. New routes
  MUST live under `/v1/...` (or another non-`/api` prefix). The existing email routes already do.
- **CORS preflight is broken at the proxy:** an `OPTIONS` preflight gets a 204 with no
  `Access-Control-Allow-Origin`. Anything that triggers a browser preflight fails platform-wide.
  → The OAuth flow is **server-side redirect-based** (full-page `302` navigations + a server-to-server
  token exchange). The browser only follows redirects; it never does a cross-origin `fetch` with a
  custom content-type, so this constraint does not apply to the OAuth flow at all. This is an
  argument *for* the redirect-based Authorization Code flow over any browser-fetch / SPA-token variant.

---

## 4. Background: OAuth2 vs OIDC, and PKCE (concise)

**OAuth 2.0** is an *authorization* framework: it gets you an `access_token` to call an API on the
user's behalf. It does not, by itself, tell you *who* the user is in a standardized way.

**OpenID Connect (OIDC)** is a thin identity layer on top of OAuth 2.0. The authorization server
additionally returns an **`id_token`** — a signed JWT containing identity claims (`sub`, `email`,
`email_verified`, `name`, `picture`, `iss`, `aud`, `nonce`, `exp`). You verify its signature against
the provider's published JWKS and check `iss`/`aud`/`exp`/`nonce`. This is the clean way to learn
identity without an extra API round-trip.

Two flavors we must support, because the major providers differ:

- **Google = OIDC.** Discovery doc at `https://accounts.google.com/.well-known/openid-configuration`;
  returns an `id_token`; `email_verified` is a first-class claim. Use `go-oidc` to verify the
  `id_token` and read claims. (Google Identity / OpenID Connect:
  https://developers.google.com/identity/openid-connect/openid-connect)
- **GitHub = plain OAuth 2.0 (no OIDC `id_token`).** After the code exchange you call the **userinfo
  API**: `GET https://api.github.com/user` for profile, and `GET https://api.github.com/user/emails`
  for the list of emails with `primary`/`verified`/`visibility`. You must pick the `primary &&
  verified` email yourself; `GET /user`'s `email` field is often `null` for users with a private
  email. (GitHub OAuth apps: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps ;
  scopes: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps ;
  emails endpoint: https://docs.github.com/en/rest/users/emails)

**Authorization Code flow** (the only flow we use): the browser is redirected to the provider with our
`client_id`, `redirect_uri`, `scope`, `state`, and PKCE `code_challenge`; the user consents; the
provider redirects back to our `redirect_uri` with a short-lived `code`; **our server** exchanges that
`code` (plus our `client_secret` and PKCE `code_verifier`) for tokens over a back-channel HTTPS call.
The tokens never touch the browser.

**PKCE (Proof Key for Code Exchange, RFC 7636), `S256` method.** We generate a random
`code_verifier`, send `code_challenge = BASE64URL(SHA256(verifier))` in the authorize request, and
present the raw `verifier` in the token exchange. The provider checks they match.

> **Why PKCE even though we're a *confidential* client with a secret?** Per **RFC 9700 — Best Current
> Practice for OAuth 2.0 Security** (published Jan 2025), PKCE is recommended for **confidential**
> clients too, not just public ones: it defends against **authorization-code injection** — an attacker
> can't redeem a code they intercepted/injected without the matching verifier. OAuth 2.1 will make
> PKCE mandatory for all authorization-code clients. So: PKCE always, `S256` always.
> (RFC 9700: https://datatracker.ietf.org/doc/rfc9700/)

The three anti-forgery values, and what each prevents:
- **`state`** — opaque random value echoed back on the callback. Prevents **CSRF / login-fixation**:
  we only accept a callback whose `state` matches one we issued for this browser. **Required for all
  providers.**
- **PKCE `code_verifier`/`code_challenge`** — prevents **auth-code interception/injection.** Required.
- **`nonce`** — random value embedded in the request and echoed inside the OIDC `id_token`. Prevents
  **id_token replay.** Required for OIDC providers (Google); GitHub has no `id_token`, so no nonce —
  its `state` + PKCE + back-channel `/user` lookup play the equivalent role.

Everything runs over HTTPS (terminated at the proxy); the back-channel token exchange is server→provider.

---

## 5. Proposed design

### 5.1 Routes (mounted on the existing `http.ServeMux`)

Add a new handler `internal/handler/oauth.go` exposing a `Register(mux, ...)` method, wired in
`cmd/server/main.go` next to the other handlers. Two routes per the Go 1.22 method+path mux syntax
already in use (`internal/handler/user.go:73`):

```
GET /v1/auth/oauth/{provider}            -> start   (302 to provider authorize URL)
GET /v1/auth/oauth/{provider}/callback   -> callback (handles the provider's redirect back)
```

- `{provider}` ∈ {`github`, `google`, …} — looked up in a registry; unknown/disabled → `404`.
- Both are **GET** and **unauthenticated** (they *establish* auth). Wrap with `noticeMW` for
  consistency, like the email routes — though the notice is irrelevant on a redirect, it's harmless.
- **Not** under `/api*` (proxy-reserved) and **not** browser-`fetch`ed (no preflight). Good.
- The provider dashboards' **Authorized redirect URI** is configured to the *public* URL, e.g.
  `https://simple-host.app/v1/auth/oauth/github/callback`. This is derived from
  `PUBLIC_BASE_URL` (Section 7) so it's correct behind the proxy.

The admin UI (`internal/handler/static/index.html`, embedded) gains "Sign in with GitHub / Google"
buttons that are plain `<a href="/v1/auth/oauth/github">` links (full-page navigation — no fetch, no
CORS). Buttons render only for providers the server reports as enabled (e.g. via a tiny
`GET /v1/auth/providers` JSON endpoint, or injected at template/build time).

### 5.2 Sequence (text diagram)

```
Browser                         simple-host (Go)                     Provider (GitHub/Google)
   |                                   |                                      |
   |  GET /v1/auth/oauth/google        |                                      |
   |---------------------------------->|                                      |
   |                                   |  mint state(rand), nonce(rand),      |
   |                                   |  pkce verifier+challenge(S256);      |
   |                                   |  persist {state-> verifier,nonce,    |
   |                                   |    provider,return_to,exp} in        |
   |                                   |    oauth_states (or signed cookie);  |
   |   302 to provider authorize URL   |  set short-lived __Host-oauth cookie |
   |<----------------------------------|  bound to the state                  |
   |                                                                          |
   |  follows redirect: GET /authorize?client_id&redirect_uri&scope&state&    |
   |     code_challenge&code_challenge_method=S256&nonce  (OIDC)              |
   |------------------------------------------------------------------------->|
   |                user authenticates + consents at provider                 |
   |   302 back to redirect_uri?code=...&state=...                            |
   |<-------------------------------------------------------------------------|
   |                                   |                                      |
   |  GET /v1/auth/oauth/google/callback?code&state                          |
   |---------------------------------->|                                      |
   |                                   | validate state == cookie/db row,     |
   |                                   | not expired, not used; load verifier |
   |                                   |                                      |
   |                                   |  POST /token (code, client_id,       |
   |                                   |    client_secret, redirect_uri,      |
   |                                   |    code_verifier)  [back-channel]    |
   |                                   |------------------------------------->|
   |                                   |   { access_token, id_token (OIDC) }  |
   |                                   |<-------------------------------------|
   |                                   | OIDC: verify id_token sig (JWKS),    |
   |                                   |   iss/aud/exp/nonce; read email,     |
   |                                   |   email_verified, sub                |
   |                                   | GitHub: GET /user + /user/emails,    |
   |                                   |   pick primary&&verified email       |
   |                                   |                                      |
   |                                   | resolve/link user (Section 6),       |
   |                                   | fetch that user's api_key            |
   |                                   |                                      |
   |   302 to /  (admin UI), set       | (option A) set session cookie, OR    |
   |   __Host-session cookie  AND/OR   | (option B) redirect to /#token=...    |
   |   hand api_key to the UI          |   that the UI stores                  |
   |<----------------------------------|                                      |
```

### 5.3 Schema (DDL, consistent with existing style)

Two new tables. Same conventions as `README.md`'s schema block (UUID PKs, `TIMESTAMPTZ DEFAULT
now()`), and since there's no migrations framework, this DDL is added to the README "Schema" section
and applied with `psql`, exactly like the existing tables.

```sql
-- One row per (provider, provider account). The durable mapping from an
-- external identity to a local users row.
CREATE TABLE oauth_identities (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  provider          TEXT NOT NULL,                 -- 'github' | 'google' | ...
  provider_user_id  TEXT NOT NULL,                 -- the provider's STABLE id: GitHub numeric id, Google 'sub'
  email             TEXT,                          -- email as asserted by provider at link time (informational)
  email_verified    BOOLEAN NOT NULL DEFAULT FALSE,
  created_at        TIMESTAMPTZ DEFAULT now(),
  updated_at        TIMESTAMPTZ DEFAULT now(),
  UNIQUE (provider, provider_user_id)              -- the real identity key; NEVER key on email
);
CREATE INDEX oauth_identities_user_id_idx ON oauth_identities (user_id);

-- Short-lived CSRF/PKCE/nonce state for in-flight authorizations.
-- (Alternative: a signed __Host- cookie instead of a table — see 5.5.)
CREATE TABLE oauth_states (
  state          TEXT PRIMARY KEY,                 -- random, opaque
  provider       TEXT NOT NULL,
  code_verifier  TEXT NOT NULL,                    -- PKCE verifier (raw)
  nonce          TEXT,                             -- OIDC only
  return_to      TEXT,                             -- where to send the user after success (allow-listed)
  created_at     TIMESTAMPTZ DEFAULT now(),
  expires_at     TIMESTAMPTZ NOT NULL,
  used_at        TIMESTAMPTZ                        -- single-use
);
CREATE INDEX oauth_states_expires_idx ON oauth_states (expires_at);
```

Notes:
- **Identity key is `(provider, provider_user_id)`, never email.** GitHub usernames *and* emails can
  change; the numeric account id is stable. Google's stable id is the `sub` claim. Keying on email is
  the root cause of most OAuth account-takeover bugs.
- `users` itself is **unchanged**. `username` stays = email; a social-only first-time user gets a
  `users` row whose `username` is the verified provider email (same as how email auth pre-creates the
  row). `api_key` is still the durable credential.
- `oauth_states` rows are single-use and short-TTL (e.g. 10 min). A periodic `DELETE FROM oauth_states
  WHERE expires_at < now()` (or prune-on-write) keeps it small. If you'd rather not add a table at
  all, see the stateless cookie alternative in 5.5.

### 5.4 Go sketch (chosen libraries)

Dependencies (add to `go.mod`):
```
golang.org/x/oauth2                       // core flow, google + github endpoints
golang.org/x/oauth2/google                // google endpoint + scopes
golang.org/x/oauth2/github                // github endpoint
github.com/coreos/go-oidc/v3/oidc         // OIDC provider discovery + id_token verification
```

A small provider abstraction keeps GitHub (OAuth2 + userinfo) and Google (OIDC) behind one interface,
and makes adding Microsoft/Apple a matter of registering another entry.

```go
// internal/handler/oauth.go (sketch — not final)

// userInfo is the normalized identity we extract from any provider.
type userInfo struct {
    ProviderUserID string // stable id: GitHub numeric id (as string), Google sub
    Email          string
    EmailVerified  bool
}

type oauthProvider interface {
    name() string
    cfg() *oauth2.Config                          // client id/secret/redirect/scopes/endpoint
    isOIDC() bool
    // exchangeAndIdentify runs the token exchange + identity extraction.
    identify(ctx context.Context, tok *oauth2.Token) (userInfo, error)
}

// --- Google (OIDC) ---
type googleProvider struct {
    oauthCfg *oauth2.Config
    verifier *oidc.IDTokenVerifier  // built once from oidc.NewProvider(ctx, "https://accounts.google.com")
}

func (g *googleProvider) identify(ctx context.Context, tok *oauth2.Token) (userInfo, error) {
    rawID, ok := tok.Extra("id_token").(string)
    if !ok {
        return userInfo{}, errors.New("google: no id_token in response")
    }
    idt, err := g.verifier.Verify(ctx, rawID) // checks sig (JWKS), iss, aud, exp
    if err != nil {
        return userInfo{}, fmt.Errorf("google: id_token verify: %w", err)
    }
    var c struct {
        Sub           string `json:"sub"`
        Email         string `json:"email"`
        EmailVerified bool   `json:"email_verified"`
        Nonce         string `json:"nonce"`
    }
    if err := idt.Claims(&c); err != nil {
        return userInfo{}, err
    }
    // CALLER also checks c.Nonce == the nonce we issued for this state.
    return userInfo{ProviderUserID: c.Sub, Email: c.Email, EmailVerified: c.EmailVerified}, nil
}

// --- GitHub (plain OAuth2 + userinfo) ---
type githubProvider struct{ oauthCfg *oauth2.Config }

func (gh *githubProvider) identify(ctx context.Context, tok *oauth2.Token) (userInfo, error) {
    hc := gh.oauthCfg.Client(ctx, tok) // auto-attaches the bearer token
    // 1) stable account id
    var u struct {
        ID    int64  `json:"id"`
        Login string `json:"login"`
    }
    if err := getJSON(ctx, hc, "https://api.github.com/user", &u); err != nil {
        return userInfo{}, err
    }
    // 2) emails — must pick primary && verified; /user.email is often null
    var emails []struct {
        Email    string `json:"email"`
        Primary  bool   `json:"primary"`
        Verified bool   `json:"verified"`
    }
    if err := getJSON(ctx, hc, "https://api.github.com/user/emails", &emails); err != nil {
        return userInfo{}, err
    }
    var email string
    var verified bool
    for _, e := range emails {
        if e.Primary { email, verified = e.Email, e.Verified; break }
    }
    return userInfo{
        ProviderUserID: strconv.FormatInt(u.ID, 10),
        Email:          strings.ToLower(email),
        EmailVerified:  verified,
    }, nil
}
```

Authorize URL construction (PKCE + nonce) uses `x/oauth2` helpers:

```go
func (h *OAuthHandler) start(w http.ResponseWriter, r *http.Request) {
    p, ok := h.providers[r.PathValue("provider")]
    if !ok { http.NotFound(w, r); return }

    state    := randToken(32)
    nonce    := randToken(32)
    verifier := oauth2.GenerateVerifier()           // x/oauth2 PKCE helper

    // persist {state -> provider, verifier, nonce, return_to, exp}; single use
    h.saveState(r.Context(), state, p.name(), verifier, nonce, sanitizeReturnTo(r))

    opts := []oauth2.AuthCodeOption{
        oauth2.S256ChallengeOption(verifier),       // code_challenge + method=S256
        oauth2.AccessTypeOffline,                   // (optional) only if you ever want a refresh token
    }
    if p.isOIDC() {
        opts = append(opts, oidc.Nonce(nonce))      // adds &nonce=...
    }
    http.Redirect(w, r, p.cfg().AuthCodeURL(state, opts...), http.StatusFound)
}
```

Callback (validate `state`, exchange with verifier, identify, link, mint/return key):

```go
func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
    p, ok := h.providers[r.PathValue("provider")]
    if !ok { http.NotFound(w, r); return }

    state := r.URL.Query().Get("state")
    code  := r.URL.Query().Get("code")
    st, err := h.consumeState(r.Context(), state) // looks up, checks provider/exp, marks used (single-use)
    if err != nil { http.Error(w, "invalid or expired state", http.StatusBadRequest); return }

    tok, err := p.cfg().Exchange(r.Context(), code, oauth2.VerifierOption(st.CodeVerifier))
    if err != nil { http.Error(w, "token exchange failed", http.StatusBadGateway); return }

    info, err := p.identify(r.Context(), tok)
    if err != nil { /* 502 */ }
    if p.isOIDC() && info /* nonce */ != st.Nonce { /* reject */ }

    user, err := h.resolveUser(r.Context(), p.name(), info) // Section 6 — the security-critical part
    if err != nil { /* 403 if unverified email, else 500 */ }

    // Bridge to the existing credential model (Section 7): set a session cookie
    // and/or surface user.APIKey to the admin UI, then redirect.
    h.completeLogin(w, r, user, st.ReturnTo)
}
```

> `oauth2.GenerateVerifier`, `oauth2.S256ChallengeOption`, and `oauth2.VerifierOption` are the
> first-class PKCE helpers in `golang.org/x/oauth2` (added 2023+), so we don't hand-roll SHA-256/base64.
> `oidc.Nonce(...)` / `IDTokenVerifier.Verify` come from `go-oidc/v3`. (Refs:
> https://pkg.go.dev/golang.org/x/oauth2 , https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc)

### 5.5 Stateless alternative for `oauth_states`

Instead of the `oauth_states` table you can carry the per-flow state in a short-lived **`__Host-`
prefixed, `Secure`, `HttpOnly`, `SameSite=Lax`** cookie holding an HMAC-signed (or AEAD-encrypted)
blob of `{state, verifier, nonce, provider, exp}` — keyed by a server secret. The callback compares
the `state` query param to the signed cookie. Pros: no DB writes, no cleanup job, naturally
browser-bound. Cons: needs a signing key in config and careful cookie hygiene. **Recommendation:**
either is fine; the cookie approach fits the "minimal moving parts" ethos slightly better, but the
table is more debuggable. This doc keeps the table in the DDL for clarity and notes the cookie option.

---

## 6. Account model & linking

`resolveUser(provider, info)` is the heart of the design. Algorithm:

```
1. Look up oauth_identities by (provider, provider_user_id).
   -> Found: this exact external account is already linked. Load users row, done.
      (Update stored email/email_verified if changed. This path is takeover-proof:
       the stable provider id matched.)

2. Not found: this provider account has never signed in here. We may want to LINK it
   to an existing local user by email, OR create a brand-new user.

   2a. If info.EmailVerified == FALSE:
         -> DO NOT link by email and DO NOT trust the email.
         Options: reject with a clear message ("verify your email with <provider> first"),
         or create a brand-new user with no email-link. RECOMMENDED: reject + ask the user
         to use email magic-link or verify their provider email. (Simplest + safest.)

   2b. info.EmailVerified == TRUE:
         look up users by username == info.Email (lower-cased).
         -> Existing user found: LINK — insert oauth_identities(provider, provider_user_id,
            user_id=existing.id, email, email_verified=true). Now they can use either method.
         -> No user: CREATE users row (username=info.Email, api_key=GenerateAPIKey(), is_admin=false)
            AND insert the oauth_identities row, in ONE transaction.

3. Return the resolved users row (and thus its api_key).
```

Why this is safe and consistent with the existing model:
- The local user's `username` is already an email, and the email magic-link flow already
  *pre-creates* a `users` row on `POST /v1/auth`. So an OAuth user and an email user for the same
  address converge on the **same row** — exactly what we want ("link by verified email").
- We only ever auto-link when the provider **asserts the email is verified** (Google
  `email_verified=true`; GitHub `primary && verified` from `/user/emails`). See Section 8 for why this
  single condition is load-bearing.

Adding more providers (Microsoft, Apple, GitLab, …): register another `oauthProvider`. Microsoft and
Apple are **OIDC** (use the OIDC path with discovery / `IDTokenVerifier`; Apple has quirks — it only
returns the user's name on *first* consent and may relay a private `@privaterelay.appleid.com`
email). GitLab/Bitbucket are OAuth2+userinfo like GitHub. No schema change — `provider` is just a
new string value, `(provider, provider_user_id)` stays unique.

---

## 7. Session vs `api_key` bridge — recommendation

After a successful callback, the user is authenticated but the rest of simple-host speaks
**`X-API-Key`**. How do we hand off?

Today `api_key == identity`, there are no cookies/sessions, and the admin UI's "login" is literally
pasting an `api_key` it then sends as `X-API-Key` on XHRs. Two viable bridges:

- **Option A — mint/surface the existing `api_key`** (keep the current model). On callback success,
  fetch `user.api_key` and get it into the admin UI the same way the magic-link UI does today.
  Concretely: redirect to `/#oauth=ok` and have the server *also* set a short-lived, `HttpOnly=false`?
  — no. Better: redirect to the UI and expose the key via a **one-time, server-set readable cookie**
  the UI immediately reads then clears, **or** add a tiny authenticated-by-session `GET /v1/me/api-key`.
  This keeps agents/CLI unchanged (still `X-API-Key`) and avoids inventing a session system.

- **Option B — introduce a real browser session cookie** for the admin UI only. On callback, set a
  `__Host-session` cookie (`Secure; HttpOnly; SameSite=Lax`) carrying a signed/opaque session id;
  `auth.Middleware` learns to accept *either* `X-API-Key` (agents) *or* the session cookie (browser).
  The `api_key` becomes a machine credential the UI can still reveal on demand.

**Recommendation: a hybrid leaning on Option A now, with the cookie as a thin transport.**
1. Keep `api_key` as the one durable credential — do **not** fork identity into a separate session
   store. The whole system is built on "you are your `api_key`," and email auth already returns it.
2. On OAuth callback success, set a short-lived (`~10 min`), `Secure`, `HttpOnly`, `SameSite=Lax`,
   `__Host-`-prefixed cookie that the **server** treats as a browser session, and **redirect to `/`**.
   Add `auth.Middleware` support for this cookie *for the admin-UI/browser routes only* so the user is
   "logged in" without pasting anything.
3. Provide `GET /v1/me/api-key` (cookie- or key-authenticated) so a human can still copy their durable
   `api_key` for CLI/agent use. The agent surface and `X-API-Key` semantics are **unchanged**.

Rationale: this gives a seamless browser experience (no copy-paste) *and* preserves the
`api_key`-is-identity contract that every other endpoint, the MCP server, and the deploy plugin rely
on. It avoids building a full session subsystem (refresh, revocation lists, server-side store) for ~30
sites and a handful of users — the cookie is just a thin, short-lived browser convenience over the
existing key. **Never** put the raw `api_key` itself in a cookie value or URL fragment; the cookie
carries an opaque session reference, and the key is fetched over an authenticated request.

(We do **not** need to store provider `access_token`/`refresh_token` at all — we use the provider
token exactly once, in-request, to learn identity, then discard it. `oauth2.AccessTypeOffline` is
unnecessary unless we ever call provider APIs later. Scope minimization, Section 9.)

---

## 8. Account-linking security: the email-collision / pre-account-takeover attack

This is the attack to design against, stated explicitly.

**The attack.** Suppose linking were done by email **without** checking that the email is verified:
1. Attacker, before the victim ever signs up, registers an account with **the victim's email**
   (`victim@example.com`) via a path that doesn't verify ownership — e.g. a password signup that
   leaves the account in a "pending/unverified" state, or any flow that creates the row first.
2. The attacker now controls a local account *keyed to the victim's email*.
3. Later, the **real victim** clicks "Sign in with Google" using `victim@example.com`. The app looks
   up by email, finds the attacker-seeded row, and **links the victim's verified Google identity to
   the attacker's account** — or hands the victim the attacker-controlled account.
4. Both now share the account. Depending on direction, the attacker reads the victim's data, or
   retains a credential into the victim's account → **pre-account takeover.**

A symmetric variant: a provider that returns an **unverified** email lets an attacker set their
provider email to the victim's address, sign in, and get auto-linked to the victim's existing local
account. This is exactly why GitHub's `/user/emails` exposes `verified`, and why blindly trusting
`GET /user.email` is dangerous.

**How this design avoids it:**
1. **Only ever auto-link on a provider-asserted *verified* email.** Google: `email_verified == true`
   in the id_token. GitHub: the chosen email is `primary && verified` from `/user/emails` (we never
   trust `/user.email` alone, which is often null/unverified). If unverified → **refuse to link**
   (reject, or create an isolated new account — recommendation: reject and tell the user, see 6.2a).
2. **The durable identity key is `(provider, provider_user_id)`, not email.** Re-logins match the
   stable provider id, so email changes on the provider side can't cause cross-account bleed.
3. **simple-host's own email path is already verification-gated.** A `users` row exists, but its
   `api_key` is *only* disclosed after the magic-link/code round-trips through the mailbox
   (`internal/handler/user.go:85-156` pre-creates the row but **never returns the key**; only
   `verify` does). There is no password and no "pending unverified password account" to seed, which
   structurally removes the classic vector — but the *verified-email* rule above is what makes the
   OAuth-to-existing-row link safe, and must not be relaxed.
4. **Single-use, expiring `state`** (and `nonce` for OIDC) prevents CSRF login-fixation, where an
   attacker tricks the victim's browser into completing a flow bound to the attacker's account.

(References: pre-account-takeover write-ups and mitigations:
https://book.hacktricks.xyz/pentesting-web/oauth-to-account-takeover ; GitHub private/unverified email
caveat: https://docs.github.com/en/rest/users/emails ; the general guidance — *never link accounts on
an unverified email* — is the consistent OWASP-aligned recommendation across these.)

---

## 9. Security checklist

- **Secrets:** `*_CLIENT_SECRET` and the session/state signing key come **only** from env (Section
  10), never committed — same posture as `ADMIN_API_KEY` (no source default; see
  `internal/config/config.go:45-47`). Don't log tokens, codes, or secrets.
- **Redirect URI allow-listing:** the `redirect_uri` we send and that's registered in each provider
  dashboard is **fixed**, derived from `PUBLIC_BASE_URL` — never built from request `Host`/headers
  (which an attacker can spoof). `return_to` (post-login landing) is independently **allow-listed** to
  same-origin paths (`sanitizeReturnTo`) to prevent open-redirect.
- **Token validation:** OIDC `id_token` signature verified against the provider JWKS via `go-oidc`,
  with `iss`/`aud`/`exp`/`nonce` checked. GitHub: token used only server-side over HTTPS to GitHub's
  own API.
- **`state` + PKCE(S256) + `nonce`:** all enforced as in Section 4; `state`/`nonce` are
  cryptographically random (`crypto/rand`), single-use, short-TTL.
- **Scope minimization:** request the *least* identity scope.
  - Google (OIDC): `openid email profile` only.
  - GitHub: `read:user user:email` (read-only profile + email list). No `repo`, no write scopes.
  - We don't request offline access / refresh tokens (we don't store provider tokens at all).
- **Cookies:** `__Host-` prefix, `Secure`, `HttpOnly` (session) / signed (state), `SameSite=Lax`
  (Lax is correct: the callback is a top-level GET navigation initiated by the provider redirect).
- **HTTPS everywhere** (proxy-terminated). Reject callbacks over plain HTTP in prod.
- **Rate-limit / abuse:** the start endpoint mints DB rows / cookies; prune expired `oauth_states`,
  and consider a soft cap, consistent with the existing `maxCodeAttempts` posture on email auth.

---

## 10. Config (env vars, `internal/config` conventions)

Extend `config.Config` and `config.Load()` (`internal/config/config.go`). Per-provider client
credentials; **a provider is enabled iff both its ID and secret are set** (mirrors the
`RESEND_API_KEY`-gates-email-auth pattern). All new vars are **optional** — absent = that provider is
simply off; nothing else breaks.

| Env Var | Required? | Default | Description |
|---|---|---|---|
| `GITHUB_OAUTH_CLIENT_ID` | optional | *(unset)* | Enables GitHub login when set with the secret. |
| `GITHUB_OAUTH_CLIENT_SECRET` | optional | *(unset)* | GitHub OAuth app secret. |
| `GOOGLE_OAUTH_CLIENT_ID` | optional | *(unset)* | Enables Google login when set with the secret. |
| `GOOGLE_OAUTH_CLIENT_SECRET` | optional | *(unset)* | Google OAuth client secret. |
| `OAUTH_REDIRECT_BASE_URL` | optional | falls back to `PUBLIC_BASE_URL` | Public base used to build each provider's `redirect_uri` (`<base>/v1/auth/oauth/<provider>/callback`). Usually equals `PUBLIC_BASE_URL`. |
| `OAUTH_STATE_SIGNING_KEY` | optional | *(required only if using the stateless cookie variant, 5.5)* | HMAC/AEAD key for the signed state/session cookie. |
| `SESSION_SIGNING_KEY` | optional | *(required only if using the browser session cookie, Section 7)* | Signs the `__Host-session` cookie. |

Loading sketch:
```go
type OAuthProviderConfig struct{ ClientID, ClientSecret string }
func (c OAuthProviderConfig) Enabled() bool { return c.ClientID != "" && c.ClientSecret != "" }

type Config struct {
    // ...existing...
    GitHubOAuth      OAuthProviderConfig
    GoogleOAuth      OAuthProviderConfig
    OAuthRedirectBase string // default: PublicBaseURL
}
```
At boot, build the provider registry from whichever are `Enabled()`; log which providers are active
(like the `RESEND_API_KEY` warning at `cmd/server/main.go:58-60`). Redirect URIs registered in the
GitHub/Google dashboards must match `<OAuthRedirectBase>/v1/auth/oauth/<provider>/callback` exactly.

---

## 11. Migration / rollout

1. **Schema:** apply the two `CREATE TABLE`s (Section 5.3) via `psql` and add them to the README
   schema block (no migrations framework — same as today). Backwards-compatible: no change to
   `users`/`auth_tokens`; existing email auth untouched.
2. **Code:** add `internal/handler/oauth.go` + provider registry; extend `internal/config`; add the
   optional cookie/session support in `internal/auth/middleware.go` (browser routes only); add
   "Sign in with …" links to the embedded admin UI (`internal/handler/static/index.html`) — **remember
   HTML is embedded, so this needs a rebuild** (`CLAUDE.md`).
3. **Provider apps:** create a GitHub OAuth app and a Google OAuth client; set the redirect URIs; put
   the four credentials into the port-8090 runports env (these are owned by the runports process, not
   the repo — same caveat as `DB_DSN`/`ADMIN_API_KEY` in `/root/workspace/CLAUDE.md`).
4. **Build & deploy** the single binary per the runbook (`go124`, swap, `cortex-runports repair
   --port 8090`), then verify `/healthz`/`/readyz` and walk the GitHub + Google flows end-to-end (no
   tests in repo — verify live).
5. **Rollout:** dark-launch by leaving the env vars unset (providers off, zero behavior change), then
   enable GitHub first, then Google. Each provider is independently toggleable, so rollback = unset its
   env vars and restart; no schema rollback needed.

---

## 12. Alternatives considered

### Library choice

| Option | What it is | Verdict |
|---|---|---|
| **`golang.org/x/oauth2` + `coreos/go-oidc/v3` (CHOSEN)** | The official low-level OAuth2 client (with `google`/`github` endpoint sub-packages and built-in PKCE helpers) + the de-facto OIDC verifier. | **Recommended.** Minimal, standard, no framework lock-in, plays naturally with one `http.ServeMux` and raw `database/sql`. We control the routes, cookies, linking logic, and the security-critical verified-email check directly. Matches this repo's "no router lib, no ORM, small surface" ethos. (https://pkg.go.dev/golang.org/x/oauth2 , https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc) |
| `markbates/goth` | All-in-one social-login with ~many providers behind a `Provider`/`Session` interface; defaults to gorilla `CookieStore`. | Convenient for greenfield apps that want a dozen providers fast. But it brings its own session/cookie machinery and provider abstractions we'd have to bend around our `api_key`-is-identity model and our verified-email linking rule. For *two* providers and a deliberately tiny codebase, it's more abstraction than payoff. (https://github.com/markbates/goth) |
| `dexidp/dex` | A full standalone **OIDC provider/federation** server. | Wrong layer. Dex is for *being* an IdP / federating many upstreams as a separate service. We just need a relying-party client in-process. Massive operational overcomplication for ~30 static sites on one box. |
| Hand-roll raw `net/http` calls | Build the authorize URL, exchange, and JWT verification by hand. | Don't. PKCE base64url, JWKS fetch+cache+rotation, and id_token signature/claims verification are exactly the parts you must not get subtly wrong. `x/oauth2` + `go-oidc` are small and correct. |

### Flow choice
- **Authorization Code + PKCE (CHOSEN)** — server-side confidential client; tokens stay server-side;
  redirect-based so it sidesteps the proxy's broken CORS preflight. Aligns with RFC 9700 / OAuth 2.1.
- **Implicit flow** — deprecated; returns tokens in the URL fragment to the browser. Rejected.
- **Browser-fetch / SPA token handling** — would hit the proxy's broken `OPTIONS`/ACAO behavior and
  expose tokens to JS. Rejected in favor of full-page redirects.

---

## 13. Open questions

1. **Unverified provider email:** reject (recommended, 6.2a) vs. create an isolated email-less account?
   Rejecting is simplest/safest but slightly worse UX for the rare GitHub user with no verified email.
2. **Session bridge depth:** ship the thin short-lived cookie now (Section 7 recommendation), or
   invest in a proper server-side session table with revocation? For current scale, thin cookie.
3. **`oauth_states`: table vs signed cookie** (5.5). Pick one; both are acceptable. Leaning cookie to
   avoid a cleanup job, table for debuggability.
4. **Multiple emails / multiple providers on one user:** the schema supports many `oauth_identities`
   per `user_id`. Do we expose a "linked accounts" management UI, and allow *manual* linking from an
   already-signed-in session (which must itself require re-auth to avoid linking-CSRF)? Future work.
5. **Admin bootstrap:** should a specific GitHub/Google identity be grantable `is_admin`, or stays
   `ADMIN_API_KEY`-only? Recommend keeping admin = `ADMIN_API_KEY` for now; OAuth users are non-admin.
6. **Apple/Microsoft specifics:** Apple's first-consent-only name + private-relay email, and MS tenant
   config, need per-provider handling when those are added.

---

## 14. Sources

- RFC 9700 — Best Current Practice for OAuth 2.0 Security (Jan 2025): https://datatracker.ietf.org/doc/rfc9700/
- Google Identity — OpenID Connect: https://developers.google.com/identity/openid-connect/openid-connect
- GitHub — Building OAuth apps: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps
- GitHub — Scopes for OAuth apps: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/scopes-for-oauth-apps
- GitHub — REST: user emails (`primary`/`verified`): https://docs.github.com/en/rest/users/emails
- `golang.org/x/oauth2` (PKCE helpers, google/github endpoints): https://pkg.go.dev/golang.org/x/oauth2
- `coreos/go-oidc/v3` (provider discovery + id_token verification): https://pkg.go.dev/github.com/coreos/go-oidc/v3/oidc
- `markbates/goth` (alternative considered): https://github.com/markbates/goth
- Pre-account-takeover / OAuth account-takeover patterns: https://book.hacktricks.xyz/pentesting-web/oauth-to-account-takeover
