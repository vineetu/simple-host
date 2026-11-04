---
title: Simple Hack authentication
description: Browser sign-in, connector OAuth, and selected-team publishing.
canonical: https://simple-hack.app/auth.md
---

# Simple Hack authentication

Use the connector at https://simple-hack.app/mcp. Sign in and choose Allow in
the trusted browser window. Never request codes, keys, passwords, or passcodes
in chat. All paths below are relative to this server's app origin.

## Password accounts on an install

Read `GET /v1/auth/oauth/providers`: when `password.enabled` is true the install supports
`POST /v1/auth/password` with `{login,password,name?,sso_state?,new_password?}`.
Login is the username or account email, trimmed and case-insensitive. Success returns the
same `{id,username,api_key,is_admin,created,handle}` as email verification. Use the key for
`/v1/me` and the existing SSO/connector consent flow. Wrong credentials return 401
`bad_credentials`; repeated failures return 429 with `Retry-After`.
A 403 `password_change_required` asks for a different `new_password` (8–256 characters).
If `password.self_signup` is true, `POST /v1/auth/password/signup` with
`{username,password}` creates an account and signs in. Public usernames are 3–64
lowercase letters, digits, dots, underscores or hyphens, never email addresses. Taken usernames return 409 `username_taken`.
The admin key can call `POST /v1/admin/password-users` with `{username,password?}`
or `POST /v1/admin/users/{id}/password` with `{password?}`. Each returns
`{id,username,handle,temporary_password,message}` once and requires a password change at sign-in.
Omit `password` to generate a temporary one. Admin usernames may be emails; admin accounts
cannot be reset here. All password routes return 404 when `PASSWORD_SIGNIN=off`;
sign-up also returns 404 when `PASSWORD_SELF_SIGNUP=off`. Hosted services leave password sign-in off.
A signed-in password account changes its own password with `POST /v1/me/password`
`{current_password,new_password}` (own API key); `GET /v1/me` adds `has_password` while
`PASSWORD_SIGNIN=on`. Wrong `current_password` returns 401 `bad_credentials` on the same
limiters as sign-in; a `new_password` too short or equal to the current one returns 400
`invalid_password`. Success is 204 and does not revoke any key, session or connector token.


## Browser sign-in

{sign_in}

Self-hosted packages can use company OpenID Connect when OIDC_ISSUER, OIDC_CLIENT_ID, and OIDC_CLIENT_SECRET
are configured; OIDC_LABEL names the button, and ALLOWED_EMAIL_DOMAINS can limit
access. GET /v1/auth/oauth/providers reports available providers and email sign-in.

## Connector OAuth

Discover `/.well-known/oauth-protected-resource/mcp` and
`/.well-known/oauth-authorization-server/mcp`. Register at `/oauth/register` with
`redirect_uris` and `token_endpoint_auth_method: "none"` for a public client.
Open `/oauth/authorize` in the browser with `response_type=code`, `client_id`,
`redirect_uri`, `state`, `code_challenge`, and `code_challenge_method=S256`.
Use this server's `/mcp` URL as `resource`. After consent, exchange the code at
`/oauth/token` with `grant_type=authorization_code`, `code`, `redirect_uri`,
`client_id`, and `code_verifier`. Confidential clients also authenticate using
their registered method. Send `Authorization: Bearer <access_token>` to `/mcp`.
Refresh at `/oauth/token` using `grant_type=refresh_token`; refresh tokens rotate.
Company OIDC signs the person in; the connector uses this server's OAuth tokens.

## Roles and selected team

Start with `hack_list_events`. Event calls use the person's organiser,
participant, or judge role. Use `hack_get_my_teams`, then `hack_select_team` before
website or storage calls. Selection changes the publishing target, not the event
role. Every call rechecks membership, stage, and deadline; leaving a team removes
publishing access. Older team connections stay scoped to their original team.
Organisers use `hack_event_storage_*` with an event slug for event websites.

## Errors and revocation

Relay permission errors; do not retry with another role or key. On an expired
or revoked token, refresh or reconnect. OAuth errors use `error` and optional
`error_description`; REST errors use `error` and, where specified, `code`.
Revoke a token at `/oauth/revoke`, authenticating as the registered client, or
disconnect the app from the account's connections. See `/openapi.json` and
`/docs.html` for the local API reference.
