# Simple Host authentication

All paths below are relative to this server's app origin. These are account-owner
sign-ins. A site's visitors use site-scoped visitor sign-in through auth.js and
never receive an API key. Keep account keys in the agent's own configuration,
never in a hosted page or browser storage.

## Discover

This server supports two methods: OAuth 2.1 with dynamic client registration and
PKCE for MCP clients and chat apps, and direct API-key sign-in for agents whose
human operator can read an email.

Discover OAuth metadata at `/.well-known/oauth-protected-resource/mcp` and
`/.well-known/oauth-authorization-server/mcp`. The API-key method's routes are
documented in `/openapi.json`.

## Pick a method

Use OAuth if you are a registered MCP client or app needing the resource owner's
consent once. Use an API key if you are an agent whose operator can type in a
6-digit email code once.

## Register

For OAuth, POST a JSON client metadata document to `/oauth/register`.
`redirect_uris` is required and must contain 1 to 10 valid redirect URIs:
HTTPS, HTTP on loopback, or an accepted app scheme, without a fragment.
`client_name` is optional. Public clients should explicitly send
`"token_endpoint_auth_method":"none"`. The other supported methods are
`client_secret_post` and `client_secret_basic`; omitting the field defaults to
`client_secret_basic`. Registration returns `client_id` and, for confidential
clients, `client_secret`. Optional `grant_types` may contain `authorization_code`
and `refresh_token`; optional `response_types` may contain only `code`.
Dynamically registered clients must use PKCE S256.

For API keys, there is no separate registration step. The first successful
sign-in creates the account.

## Claim

For OAuth's `authorization_code` grant, open `/oauth/authorize` in a browser with
`response_type=code`, `client_id`, `redirect_uri`, `state`, `code_challenge`, and
`code_challenge_method=S256`. The redirect URI must match registration, with
loopback port variation allowed. The resource owner signs in and approves the
app. The server redirects back with `code`, the supplied `state`, and `iss`.
An optional `resource` selects the token audience: this origin's `/mcp` URL for
MCP only, or this app's origin for both MCP and REST. Omitting it also allows both.

For an API key, POST `/v1/auth` with JSON `{"email":"person@example.com"}` to
send a 6-digit code. Then POST `/v1/auth/verify` with JSON fields `email` and
`code` to claim it. These account sign-in routes answer only on the app's own
address, subject to the existing origin rules.

## Exchange

For OAuth, POST form-encoded fields to `/oauth/token`: `grant_type=authorization_code`,
`code`, `redirect_uri`, `client_id`, and the original PKCE `code_verifier`.
Confidential clients also authenticate using their registered token endpoint
method. The response includes `access_token`, `token_type`, `expires_in`, and
`refresh_token`. Refresh with `grant_type=refresh_token` and `refresh_token` at
the same endpoint, authenticating as the same client. Refresh tokens rotate;
reuse revokes the connection.

For API keys, `/v1/auth/verify` returns `api_key` directly. Send
`"choose_handle":true` with the first verification. For a new account, a
409 response with `code: "choose_handle"`, `suggested_handle`, and `address`
creates nothing and leaves the code usable. Ask the person which handle they
want, then verify again with the same email and code plus `"handle":"<name>"`.
Handles use 1 to 39 lowercase letters, digits, or hyphens, without an end hyphen.
Existing accounts receive their key without choosing a handle.

## Use the access_token

For OAuth, send `Authorization: Bearer <token>` on `/mcp`. An access token issued
without a resource is also accepted on `/v1/...` with exactly the person's
API-key permissions. A token issued for the MCP resource is restricted to `/mcp`.

For API keys, send `X-API-Key: <key>` on every `/v1/...` owner call through the
app origin. The key has only its account's permissions.

## Errors

REST errors carry `error` and, where specified, `code`; branch on `code` rather
than message wording. Owner authentication returns 401 `missing_api_key`,
`wrong_auth_header`, `invalid_api_key`, `key_expired`, or `key_expired_idle`.
For expired keys, sign in again or obtain a new key from the Keys panel; do not
retry the old key. Account sign-in on a site's address or with a foreign origin
returns 403 `account_auth_unavailable`.

Email sign-in can return 400 `invalid_email`, 401 `invalid_code`, 429
`rate_limited`, or 403 `signup_unavailable_region`. Handle selection can return
409 `choose_handle`, `handle_taken`, or `handle_reserved`, or 400 `invalid_handle`.
For refused handles, ask for another and retry with the same code.
Owner calls can also return 403 `deploy_only_key` or `account_suspended`;
request a full key for the former and tell the person about the latter.

OAuth JSON errors use `error` and an optional `error_description`.
Registration returns `invalid_client_metadata`, `invalid_redirect_uri`, or
`server_error`. Authorization can return `invalid_request`, `invalid_client`,
`unsupported_response_type`, or `invalid_target`; declining consent returns
`access_denied` to the app. The token endpoint returns `invalid_request`,
`invalid_client`, `unsupported_grant_type`, `invalid_grant`, `invalid_target`,
`invalid_scope`, or `server_error`. `invalid_grant` includes expired or reused
codes, failed PKCE, and invalid or reused refresh tokens. Revocation can return
`invalid_request`, `invalid_client`, or `temporarily_unavailable`.
Invalid, expired, or revoked access tokens on `/v1/...` return 401
`invalid_token`; `/mcp` reports `invalid_token` in its `WWW-Authenticate`
challenge when a token was presented. Refresh the token or reconnect.

## Revocation

POST `/v1/me/sign-out` with the current API key to end that key.
POST `/v1/me/api-key/rotate` invalidates every older key at once and returns a
new `api_key`. It also disconnects connected apps and ends the account's site
sign-ins.

GET `/v1/me/connections` lists connected OAuth apps. DELETE
`/v1/me/connections/{client_id}` disconnects one. These owner calls require an
account credential. OAuth clients can also POST form-encoded `token` to
`/oauth/revoke`, authenticating using their registered client method.
