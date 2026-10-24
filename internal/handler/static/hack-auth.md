---
title: Simple Hack authentication
description: Browser sign-in, connector OAuth, and selected-team publishing.
canonical: https://simple-hack.app/auth.md
---

# Simple Hack authentication

Use the connector at https://simple-hack.app/mcp. Sign in and choose Allow in
the trusted browser window. Never request codes, keys, passwords, or passcodes
in chat. All paths below are relative to this server's app origin.

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
