# Visitor sign-in

Site-scoped browser sessions for website visitors (customers, guests, members), on the site's own address, through https://simple-host.app/auth.js (SH.mount, SH.requireSignIn, SH.me, SH.signOut). The only sign-in a page ever uses. Never returns an API key.

## POST /v1/site-signin

<div data-operation="POST /v1/site-signin"></div>

The form on a named-viewers site's sign-in page posts here, on the site's own host; no key, no JavaScript. With `email` alone it sends a sign-in code (to any address, listed or not, so the form never says who is on the list) and answers the code page (401, text/html). With `email` and `code` it signs the visitor in to this site on this host (the same visitor session as auth.js) and answers 303 to `next`. Only a form from a page on the same host is accepted (403 origin_not_allowed otherwise). On a site that is not specific it answers 303 to `next`. Rate limited per address and per email like visitor sign-in.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 303 | Signed in; go back to `next` |
| 400 | Not a form post |
| 401 | The code page, or the sign-in page again with the reason (text/html, noindex, no-store) |
| 403 | Origin/Referer is not one of the site's own addresses (code origin_not_allowed). An agent or script sends the site owner's X-API-Key and no Origin or Referer, which needs no page. |

## POST /v1/site-signout

<div data-operation="POST /v1/site-signout"></div>

The Switch account and Sign out buttons on "This site is private" post here, on the site's own host. Ends this host's visitor session and answers 303 to `next`, which then shows the sign-in page. Only a form from a page on the same host is accepted.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 303 | Signed out; go back to `next` |
| 400 | Not a form post |
| 403 | Origin/Referer is not one of the site's own addresses (code origin_not_allowed). An agent or script sends the site owner's X-API-Key and no Origin or Referer, which needs no page. |

## POST /v1/site-unlock

<div data-operation="POST /v1/site-unlock"></div>

The form on the "This site is protected" page posts here, on the site's own host; no key. The site is the one the host names (on a person host, the first path segment of `next`). Only a form from a page on the same host is accepted (another origin gets 403 origin_not_allowed as JSON). A right passcode answers 303 to `next` with the unlock cookie for this host. A wrong one answers the gate page again (401). Wrong tries are limited per site and address (RATE_LIMIT_PASSCODE_IP, default 5 per 3 minutes, then PASSCODE_LOCKOUT_MINUTES, default 15) and per site from everyone together (RATE_LIMIT_PASSCODE_SITE, default 60 a minute, then PASSCODE_SITE_LOCKOUT_MINUTES, default 15, when every try is refused; visitors already let in are not affected): 429 with Retry-After.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 303 | Right passcode; go back to `next` |
| 400 | Not a form post |
| 401 | Wrong passcode: the gate page again, saying so (text/html, noindex, no-store) |
| 403 | Origin/Referer is not one of the site's own addresses (code origin_not_allowed). An agent or script sends the site owner's X-API-Key and no Origin or Referer, which needs no page. |
| 429 | Too many wrong tries: the gate page saying when to try again (text/html) |

## GET /v1/sites/{sitename}/me

<div data-operation="GET /v1/sites/{sitename}/me"></div>

Pages call it same-origin on the site's own address — its address `https://<site>.<handle>.simple-host.app/` (only that site answers there), or its domain if it has one. Origin/Referer-gated. When the site has a connected domain and the call comes through another address, it returns signed_in false, sign_in_available false and code use_custom_domain with `domain` naming it, so the page can send the visitor to sign in there. Where sign-in is not offered (the legacy path address) the code is custom_domain_required. Checks the host, site and session expiry without extending the session. No CSRF header required. Anonymous visitors receive 200.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Signed-in identity or anonymous visitor |
| 403 | Origin/Referer is not one of the site's own addresses (code origin_not_allowed). An agent or script sends the site owner's X-API-Key and no Origin or Referer, which needs no page. |
| 404 | Not found |
| 500 | Internal server error |

## POST /v1/sites/{sitename}/visitor/auth

<div data-operation="POST /v1/sites/{sitename}/visitor/auth"></div>

Origin-gated; sign-in works on the site's own address (its address `https://<site>.<handle>.simple-host.app/`, or its domain if it has one), not on the apex or the legacy path address. Uses the shared account email-code limiter and site IP limiter. Normalizes and validates email, then sends a 6-digit Resend code valid for 15 minutes. Does not create the account yet.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Code sent; no dashboard magic link. |
| 400 | Invalid request, missing fields, apex host, or the legacy path address (custom_domain_required) |
| 401 | Invalid code. `code` stays invalid_code. `reason` is code_wrong, code_used_up, code_expired, or link_other_browser; `tries_left` is set on code_wrong. Verification only. |
| 403 | Origin forbidden or missing CSRF (verification) |
| 404 | Site not found |
| 429 | IP or shared per-email rate limit exceeded |
| 500 | Internal error or email delivery failed |

## POST /v1/sites/{sitename}/visitor/auth/verify

<div data-operation="POST /v1/sites/{sitename}/visitor/auth/verify"></div>

Origin-gated; sign-in works on the site's own address (its address `https://<site>.<handle>.simple-host.app/`, or its domain if it has one), not on the apex or the legacy path address. Uses the shared account email-code limiter and dedicated visitor auth IP limiter. Codes are bound to this site and the visitor purpose; dashboard codes are not accepted. Checks the latest unused, unexpired token, caps attempts, and creates the account lazily. Never returns an API key. Session has 30-day absolute and 14-day idle lifetimes. Send X-SH-CSRF: 1 is required explicitly; application/json does not satisfy this requirement.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| X-SH-CSRF | header | string, one of `1` |  | Required on cookie-authenticated writes |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | 6-digit email code |
| email | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Signed in; sets the existing host-only visitor session cookie. Email/provider are included; requests through the legacy path address return 400 custom_domain_required. |
| 400 | Invalid request, missing fields, apex host, or the legacy path address (custom_domain_required) |
| 401 | Invalid code. `code` stays invalid_code. `reason` is code_wrong, code_used_up, code_expired, or link_other_browser; `tries_left` is set on code_wrong. Verification only. |
| 403 | Origin forbidden or missing CSRF (verification) |
| 404 | Site not found |
| 429 | IP or shared per-email rate limit exceeded |
| 500 | Internal error or email delivery failed |

## POST /v1/sso/consent

<div data-operation="POST /v1/sso/consent"></div>

Same-origin form POST with Sec-Fetch-Site validation and a one-use form token bound to the current SSO pass and server-stored hop. No account API accepts the SSO cookie.

No authentication required.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Consent remembered; navigate to the site's establish endpoint |
| 400 | Invalid or expired hop |
| 401 | Sign in on the apex first |
| 403 | Invalid form token or origin |
| 429 | IP rate limit exceeded |

## DELETE /v1/sso/consents/{site}

<div data-operation="DELETE /v1/sso/consents/{site}"></div>

Same-origin account-key request. The next hop asks again, including for named viewers. Does not revoke existing sessions. The apex SSO cookie alone cannot authorize this operation. No session-list UI.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| site | path | string | yes | Site UUID |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Consent forgotten or already absent |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Wrong origin or not a full account key |

## GET /v1/sso/hop

<div data-operation="GET /v1/sso/hop"></div>

Uses only the apex SSO cookie. Shows the shared account sign-in card if absent, asks consent once per person and site including named viewers, or redirects with a 60-second host- and nonce-bound one-time establish code. The return address is read only from server state. No-store, no-referrer, and frame-ancestors none.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| state | query | string | yes |  |
| token | query | string |  | Browser-bound owner provider token |
| cn | query | string |  | Provider browser nonce hash |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Apex sign-in or consent card |
| 302 | Navigate to the site's establish endpoint |
| 400 | Missing |
| 403 | Wrong origin or non-document request |
| 429 | IP rate limit exceeded |

## POST /v1/sso/session

<div data-operation="POST /v1/sso/session"></div>

Same-origin apex POST authenticated by a live stored full account key. Sets a pass cookie if the browser does not already hold a live pass for this key. Browser sign-in calls this after verification; the dashboard also calls it after loading. Replacing a different browser pass revokes its old parent key and site sessions. Issuance is limited per IP and key and retains at most one pass per parent key. Deploy keys and connector credentials cannot bootstrap it.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Apex SSO cookie ready |
| 401 | Missing |
| 403 | Wrong host or origin |
| 429 | Pass issuance rate limit exceeded |

## GET /v1/sso/signed-out

<div data-operation="GET /v1/sso/signed-out"></div>

Rate-limited top-level navigation after site sign-out. Consumes the one-use ticket silently only when its live requesting site session's parent hash matches this browser's apex cookie. Otherwise shows a shared-card confirmation with a form token and Cancel link. On completion, clears the cookie and serves an apex page that revokes and removes the browser's localStorage dashboard key before navigating to the server-stored return address, even when the pass row has been swept.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Confirmation form or final dashboard-key sign-out page |
| 400 | Invalid |
| 403 | Wrong host or non-document request |
| 429 | IP rate limit exceeded |

## POST /v1/sso/signed-out

<div data-operation="POST /v1/sso/signed-out"></div>

Same-origin apex form POST with Origin and Fetch Metadata checks, a one-use form token bound to the cookie that opened the confirmation, and a server-stored return address. Revokes the current family, clears the cookie, and serves the final dashboard-key sign-out page. Cannot be framed.

No authentication required.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Final apex page revokes the stored dashboard key and returns to the site |
| 400 | Invalid |
| 403 | Wrong host or origin |
| 429 | IP rate limit exceeded |

## GET /v1/sso/start

<div data-operation="GET /v1/sso/start"></div>

Stores the validated return address and nonce hash server-side, sets the host-only nonce cookie, and redirects to the account apex. Requires an unlocked passcode site and refuses offline or suspended sites. Top-level navigation only. Fetch metadata must indicate navigate and document when present; prefetch and prerender are refused. Rate-limited per IP.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| return_to | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Nonce cookie set; navigate to the apex hop |
| 400 | Invalid site return address or navigation |
| 403 | Site locked |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/turnstile/frame

<div data-operation="GET /v1/turnstile/frame"></div>

Served on the app's own address only (PUBLIC_BASE_URL's host; 404 elsewhere, and 404 when Turnstile is not set up). A small HTML page with Cloudflare Turnstile that any origin may frame (frame-ancestors *). auth.js embeds it when a save answers spam_check_required. It starts only when its parent window posts {type: "sh-turnstile-start", resource}, renders the widget with action sh-post and a cdata that binds the token to the parent's origin (the browser's event.origin) and the resource, and posts {type: "sh-turnstile-token", token} to that origin only. auth.js accepts messages only from this frame's window and the app's origin. The server then checks the token with siteverify: success, action sh-post, this app's hostname, that cdata, a challenge under 300 seconds old, and single use. The visitor's address is never sent to Cloudflare.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The frame page |
| 404 | Not the app's own address |

## GET /v1/u/{handle}/sites/{sitename}/me

<div data-operation="GET /v1/u/{handle}/sites/{sitename}/me"></div>

Pages call it same-origin on the site's own address — its address `https://<site>.<handle>.simple-host.app/` (only that site answers there), or its domain if it has one. Origin/Referer-gated. When the site has a connected domain and the call comes through another address, it returns signed_in false, sign_in_available false and code use_custom_domain with `domain` naming it, so the page can send the visitor to sign in there. Where sign-in is not offered (the legacy path address) the code is custom_domain_required. Checks the host, site and session expiry without extending the session. No CSRF header required. Anonymous visitors receive 200.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| handle | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Signed-in identity or anonymous visitor |
| 403 | Origin/Referer is not one of the site's own addresses (code origin_not_allowed). An agent or script sends the site owner's X-API-Key and no Origin or Referer, which needs no page. |
| 404 | Not found |
| 500 | Internal server error |

## POST /v1/u/{handle}/sites/{sitename}/visitor/auth

<div data-operation="POST /v1/u/{handle}/sites/{sitename}/visitor/auth"></div>

Origin-gated; sign-in works on the site's own address (its address `https://<site>.<handle>.simple-host.app/`, or its domain if it has one), not on the apex or the legacy path address. Uses the shared account email-code limiter and site IP limiter. Normalizes and validates email, then sends a 6-digit Resend code valid for 15 minutes. Does not create the account yet.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| handle | path | string | yes |  |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Code sent; no dashboard magic link. |
| 400 | Invalid request, missing fields, apex host, or the legacy path address (custom_domain_required) |
| 401 | Invalid code. `code` stays invalid_code. `reason` is code_wrong, code_used_up, code_expired, or link_other_browser; `tries_left` is set on code_wrong. Verification only. |
| 403 | Origin forbidden or missing CSRF (verification) |
| 404 | Site not found |
| 429 | IP or shared per-email rate limit exceeded |
| 500 | Internal error or email delivery failed |

## POST /v1/u/{handle}/sites/{sitename}/visitor/auth/verify

<div data-operation="POST /v1/u/{handle}/sites/{sitename}/visitor/auth/verify"></div>

Origin-gated; sign-in works on the site's own address (its address `https://<site>.<handle>.simple-host.app/`, or its domain if it has one), not on the apex or the legacy path address. Uses the shared account email-code limiter and dedicated visitor auth IP limiter. Codes are bound to this site and the visitor purpose; dashboard codes are not accepted. Checks the latest unused, unexpired token, caps attempts, and creates the account lazily. Never returns an API key. Session has 30-day absolute and 14-day idle lifetimes. Send X-SH-CSRF: 1 is required explicitly; application/json does not satisfy this requirement.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| handle | path | string | yes |  |
| sitename | path | string | yes |  |
| X-SH-CSRF | header | string, one of `1` |  | Required on cookie-authenticated writes |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | 6-digit email code |
| email | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Signed in; sets the existing host-only visitor session cookie. Email/provider are included; requests through the legacy path address return 400 custom_domain_required. |
| 400 | Invalid request, missing fields, apex host, or the legacy path address (custom_domain_required) |
| 401 | Invalid code. `code` stays invalid_code. `reason` is code_wrong, code_used_up, code_expired, or link_other_browser; `tries_left` is set on code_wrong. Verification only. |
| 403 | Origin forbidden or missing CSRF (verification) |
| 404 | Site not found |
| 429 | IP or shared per-email rate limit exceeded |
| 500 | Internal error or email delivery failed |

## GET /v1/visitor/establish

<div data-operation="GET /v1/visitor/establish"></div>

One-time hop on the site's own address (its `<site>.<handle>` host or domain). Consumes `once`, host-matches, requires the `__Host-sh_vnonce` cookie set by `GET /v1/visitor/oauth/{provider}` in this browser (its hash must match the one bound to the code; the code is burned on any failure), sets `__Host-sh_vsess` (or `sh_vsess` on local HTTP), clears the nonce cookie, then 302s to the stored `return_to` with no query mutation. Never sets a cookie on the apex or the legacy path address. Failures are HTML, no cookie, no redirect.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| once | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Cookie set; redirect to the stored return_to |
| 400 | Bad, replayed, expired, host-mismatch, missing or wrong nonce cookie, or apex Host |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/visitor/logout

<div data-operation="POST /v1/visitor/logout"></div>

For a hop session, deletes its parent dashboard key, apex pass, and all linked site sessions. Returns redirect_url for a top-level navigation through the apex. A matching family signs out silently; expired, missing, legacy, or mismatched sessions require confirmation. The final apex page revokes and removes the browser's stored dashboard key before returning. Instances without an account apex end the local session with 204. Requires same-origin. Requires `X-SH-CSRF: 1` or `Content-Type: application/json` (not a simple form post).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| X-SH-CSRF | header | string, one of `1` |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Navigate the top-level window to redirect_url to clear the apex cookie |
| 204 | Session deleted and cookie cleared |
| 403 | Missing CSRF header (and not a JSON request) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/visitor/oauth/{provider}

<div data-operation="GET /v1/visitor/oauth/{provider}"></div>

Visitor sign-in start, served on the site's own address (its `<site>.<handle>` host, person address, claimed name or bound domain). `return_to` must be a page on this same host that belongs to the site served here. Sets a short-lived host-only nonce cookie (`__Host-sh_vnonce`, HttpOnly, Secure, SameSite=Lax, 10 minutes; `sh_vnonce` on local HTTP), stores only its SHA-256 with the OAuth state, and 302s to the provider with PKCE S256. `GET /v1/visitor/establish` later sets the session only in the browser holding that nonce, so a sign-in finished elsewhere cannot be completed in another browser (login CSRF). auth.js `SH.signIn()` navigates here.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| provider | path | string, one of `google`, `github`, `oidc` | yes |  |
| return_to | query | string | yes | Absolute URL of a page on this host to return to after sign-in |

**Responses:**

| Status | Description |
| --- | --- |
| 302 | Nonce cookie set; redirect to the identity provider |
| 400 | Missing return_to, or one that is not a page of the site on this host |
| 404 | Unknown or disabled provider |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
