# Auth

Account sign-in by emailed code or Google, for Simple Host account owners and the agents working for them. It ends in an API key. It is never for a site's visitors: these routes answer only on the app's own address (403 account_auth_unavailable elsewhere), and an API key never belongs in a page. A page signs its visitors in with the Visitor sign-in routes through auth.js.

## GET /auth.md

<div data-operation="GET /auth.md"></div>

Read the authentication reference

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Instance-aware sign-in documentation. Email-code steps are omitted when unavailable. |

## POST /v1/auth

<div data-operation="POST /v1/auth"></div>

For Simple Host account owners and their agents, never for a site's visitors. Answers only on the app's own address: on a site's address, a person's address, a connected domain, or from a page's origin it is 403 account_auth_unavailable, and no code is sent. A page signs its visitors in with POST /v1/sites/{sitename}/visitor/auth through auth.js instead. Emails a 6-digit code (and a magic link) to the address. No user row is created here — the account is created lazily on the first successful verify, so this can't be used to enumerate users.

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes |  |
| nonce_hash | string |  | Browser pages only. base64url SHA-256 of a random nonce the page keeps. With it the email also carries a sign-in link, redeemable only together with that nonce (so only in that browser). Without it the email carries the code alone. |

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Code sent |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 403 | account_auth_unavailable: account sign-in answers only on the app's own address; a page's visitors use visitor sign-in (auth.js SH.mount). Or email_signin_off (EMAIL_SIGNIN=off): sign in with the company account button instead. |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/auth/oauth/providers

<div data-operation="GET /v1/auth/oauth/providers"></div>

Public discovery for pages and the dashboard: auth.js reads it from a site to offer Google; it is not account sign-in and sets nothing. Google, GitHub and a company's own identity provider (oidc). Returns the enabled provider names so pages never hardcode a provider. Empty when sign-in is not configured. Sign-in creates or links a `users` row; a hosted-page session still does not disclose an API key.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Enabled providers |

## GET /v1/auth/oauth/{provider}

<div data-operation="GET /v1/auth/oauth/{provider}"></div>

Account sign-in starts only on the app's own address. On any other host (a site's address, a connected domain) the same path answers 302 to GET /v1/visitor/oauth/{provider} on that host with the same return_to, so a hand-written link still reaches visitor sign-in and never an account key; from a page's origin on the apex it is 403 account_auth_unavailable. A page offers Google to its visitors with auth.js SH.signIn(), which starts the SSO hop on the site's host when configured, or GET /v1/visitor/oauth/{provider} on instances without an apex. Apex start. Query `return_to` is required and must be an absolute URL. Owner return addresses include the apex `/v1/sso/hop` with its state and browser nonce hash. The other shapes are the dashboard origin `/` (owner purpose — callback hands off via `/?token=`; success 302s to the provider with PKCE S256) or a site's own address — a site host `https://<site>.<handle>.simple-host.app/...` (or, while that person's certificate is pending, the person address `https://<handle>.simple-host.app/<site>/...`), a claimed name or a DNS-proven bound domain (site purpose). A site-purpose start is not begun here: it 302s to `GET /v1/visitor/oauth/{provider}` on that site's own host, which binds the sign-in to the browser. Unknown or disabled provider is 404. No cookie is set here. The Legacy path-address (`sites.<domain>/<handle>/<site>/`) return_to URLs are rejected with 400 invalid return_to, as are apex URLs outside the dashboard and SSO hop. The caller cannot set `purpose`; it is derived from `return_to`.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| provider | path | string, one of `google`, `github`, `oidc` | yes |  |
| return_to | query | string | yes | Absolute URL of the page to return to after sign-in |

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Redirect to the identity provider |
| 400 | Invalid or missing return_to |
| 403 | account_auth_unavailable: on the app's address but from a page's origin. |
| 404 | Unknown or disabled provider |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
| 503 | oidc_unavailable: company sign-in's identity provider could not be reached yet (lazy discovery); retry shortly. |

## GET /v1/auth/oauth/{provider}/callback

<div data-operation="GET /v1/auth/oauth/{provider}/callback"></div>

Answers only on the app's own address (403 account_auth_unavailable elsewhere). Provider redirect URI on the apex. Consumes the one-time `state`. Identifies via userinfo, then `resolveUser` (link or create a `users` row on a verified email; refuse missing/unverified email, and refuse an address that already has a PASSWORD_SIGNIN password account, a themed HTML page naming neither account). Site purpose 302s to `https://<return-host>/v1/visitor/establish?once=…`. Owner purpose 302s to `{PUBLIC_BASE_URL}/?token=…` for the dashboard to redeem via `POST /v1/auth/verify`. Neither redirect contains an API key. Failures are a short no-store HTML page on the apex with no Location and no cookie.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| provider | path | string, one of `google`, `github`, `oidc` | yes |  |
| code | query | string |  |  |
| state | query | string |  |  |
| error | query | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Site purpose: redirect to /v1/visitor/establish on the stored host. Owner purpose: redirect to /?token= on PUBLIC_BASE_URL. |
| 400 | Bad, replayed, or expired state, or provider error |
| 403 | account_auth_unavailable (JSON): not on the app's own address. For oidc only, also a business-rule refusal (unverified email, domain not on ALLOWED_EMAIL_DOMAINS, Google hd not allowed): a themed no-store HTML page naming the reason in plain words, no internals. |
| 404 | Unknown or disabled provider |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
| 502 | Token or userinfo failure |

## POST /v1/auth/password

<div data-operation="POST /v1/auth/password"></div>

Available only when PASSWORD_SIGNIN=on. Passwords are 8–256 characters. Temporary passwords require a different new_password at sign-in.

No authentication required.

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| login | string | yes |  |
| name | string |  |  |
| new_password | string |  |  |
| password | string | yes |  |
| sso_state | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Authenticated |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Wrong username or password (bad_credentials), including unknown and passwordless accounts. |
| 403 | password_change_required: current password verified; resubmit with a different new_password. Or account_suspended. |
| 404 | not found: PASSWORD_SIGNIN is off. |

## POST /v1/auth/password/signup

<div data-operation="POST /v1/auth/password/signup"></div>

Available only when PASSWORD_SIGNIN=on. Passwords are 8–256 characters. Temporary passwords require a different new_password at sign-in. Username accepts any value, an email is suggested but not required; it is trimmed and lower-cased, 1–254 characters, no control characters.

No authentication required.

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| password | string | yes |  |
| username | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Authenticated |
| 400 | invalid_username or invalid_password |
| 403 | signup_unavailable_region |
| 404 | not found: PASSWORD_SIGNIN off or PASSWORD_SELF_SIGNUP off |
| 409 | username_taken |
| 429 | rate_limited; Retry-After header |

## POST /v1/auth/verify

<div data-operation="POST /v1/auth/verify"></div>

Answers only on the app's own address (403 account_auth_unavailable elsewhere), so no hosted page can mint an account key. The apex visitor sign-in card also uses this endpoint, keeping the account key on the apex and returning only a site cookie through the SSO hop. After success, browser sign-in calls POST /v1/sso/session with the returned key to establish the apex SSO cookie. Exchange `{email, code}` (CLI/agent) or a magic-link `{token}` (browser) for an API key. The first successful email-code verify creates the account. Dashboard Google also redeems through this endpoint (`/?token=&cn=`). A link token is redeemed only with the `nonce` whose hash it was issued with, so a sign-in link works only in the browser that asked for it. Visitor-issued codes are not accepted here. The code expires in 15 minutes and allows 3 attempts.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| from | query | string |  | Optional same-visit attribution label (letters, digits, hyphens); recorded only for a new browser/connector account, never a permission or cookie. |
| utm_source | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |
| utm_medium | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |
| utm_campaign | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |
| utm_term | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |
| utm_content | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |
| referrer | query | string |  | Optional same-visit attribution; UTM values are bounded plain text, referrer is an outside hostname only. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| choose_handle | boolean |  | When this sign-in would create a new account: create nothing yet and answer 409 with code choose_handle and a suggested_handle, without using up the code. Ask the person which address they want, then verify again with the same code and handle. Ignored for an existing account. |
| code | string |  |  |
| email | string |  |  |
| handle | string |  | The address (&lt;handle&gt;.simple-host.app) a NEW account takes; ignored for an existing account. Refused as 400 invalid_handle, 409 handle_reserved or 409 handle_taken without using up the code, so another can be tried. It can be changed later (PATCH /v1/me), not more than once in 30 days after publishing. Without handle or choose_handle a new account is given one. |
| name | string |  | Optional label for the key this sign-in issues, shown in the Keys list (GET /v1/me/keys). Defaults to "dashboard sign-in" for a link or Google, "agent sign-in" for a typed code. |
| nonce | string |  | required with token — the nonce whose hash was sent when the link was requested |
| sso_state | string |  | Optional server-issued hop state for visitor signup attribution |
| token | string |  | magic-link token (alternative to email+code) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Authenticated |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Invalid code. `code` stays invalid_code. `reason` is code_wrong, code_used_up, code_expired, or link_other_browser; `tries_left` is set on code_wrong. |
| 403 | account_auth_unavailable: account sign-in answers only on the app's own address; a page's visitors use visitor sign-in (auth.js SH.mount). Or email_signin_off (EMAIL_SIGNIN=off), for the email+code shape only — a magic-link token (from the company sign-in or Google flow) still redeems normally. |
| 409 | code choose_handle (a new account: choose its address first; carries suggested_handle and address), handle_taken / handle_reserved (the handle sent cannot be had; error names the address), or password_account_exists (this address already has a password account from PASSWORD_SIGNIN; the code is spent, sign in with the password instead). The code is not used up except for password_account_exists. |

## GET /v1/handles/check

<div data-operation="GET /v1/handles/check"></div>

Whether &lt;handle&gt;.simple-host.app can be taken as an account's address: at sign-up (POST /v1/auth/verify with handle) or as a change (PATCH /v1/me). Handles, old handles that still redirect, free &lt;name&gt;.simple-host.app addresses claimed by sites, other accounts' live site names (an unclaimed &lt;name&gt;.simple-host.app still redirects to that site) and reserved names share one namespace. With X-API-Key the account's own handle counts as available. Rate limited per address (RATE_LIMIT_HANDLE_CHECK); no CORS, and a cross-site browser request gets 403. A claim still decides.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| handle | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The verdict |
| 403 | Cross-site browser request |
| 429 | Too many checks from this address |
