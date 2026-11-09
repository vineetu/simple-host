# Notifications

Signed HTTPS notifications for visitor storage writes on Simple Host.

## GET /v1/notifications

<div data-operation="GET /v1/notifications"></div>

Owner credentials required on Host.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List notifications across the account |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## GET /v1/push/sw.js

<div data-operation="GET /v1/push/sw.js"></div>

Unauthenticated, host-agnostic, and never gated by WEB_PUSH_ACCOUNTS (there is no site or account in this path at all). Served at a reserved platform path under /v1/, which every deployment (custom domains, address families, path-model sites) already proxies or routes to this server for every other /v1/... call, so no site, host or deployment ever needs its own copy or config change for this file. Registered by SH.push.subscribe() (auth.js) with an explicit scope of "/v1/push/", never root -- a push event and a notification click both work from any scope, so this worker never takes the wider scope a site's own root-scoped service worker, if it has one, still exclusively owns. Shows only the payload Simple Host already built server-side (title, body, url; generic by default, see the owner's push rules) and never reads or decides anything on its own.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The service worker's JavaScript source |
| 404 | The embedded file is unexpectedly missing (install misconfiguration). |

## GET /v1/sites/{sitename}/notifications

<div data-operation="GET /v1/sites/{sitename}/notifications"></div>

Owner credentials required on Host.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List notifications for a site. |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/sites/{sitename}/notifications

<div data-operation="POST /v1/sites/{sitename}/notifications"></div>

The owner-entered URL is accepted without an MCP verification challenge. Generates a secret unless one is supplied; the response shows it once. Optional custom header and svix-* aliases are sent only for owner-made subscriptions. MCP and REST share the site subscription cap. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| sitename | path | string | yes |  |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| auth_header | object |  |  |
| events | array of string, one of `record.added`, `record.changed`, `file.uploaded`, `site.deployed`, `domain.status_changed`, `viewer.access_requested`, `visitor.signed_up` | yes |  |
| label | string |  |  |
| secret | string |  |  |
| storage | string |  | Existing site storage resource; empty means all resources. |
| url | string | yes | HTTPS, at most 2048 bytes, no userinfo or fragment. Every resolved address must be public. Redirects are not followed. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Create an owner-made notification |
| 400 | invalid_url, refused_address, invalid_events, invalid_secret, invalid_header, or storage_not_found. |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | too_many_notifications: the site subscription cap is reached. notifications_not_enabled: the server has no PASSCODE_ENC_KEY. |

## PATCH /v1/sites/{sitename}/notifications/{id}

<div data-operation="PATCH /v1/sites/{sitename}/notifications/{id}"></div>

false also resumes suspension and makes held deliveries due now. Owner credentials required. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| paused | boolean | yes | true pauses; false resumes a paused or suspended subscription and makes held deliveries due now. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Pause or resume a notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## DELETE /v1/sites/{sitename}/notifications/{id}

<div data-operation="DELETE /v1/sites/{sitename}/notifications/{id}"></div>

Owner credentials required on Host.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Remove a notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## GET /v1/sites/{sitename}/notifications/{id}/deliveries

<div data-operation="GET /v1/sites/{sitename}/notifications/{id}/deliveries"></div>

Newest first, at most 200 within the configured history window (default 7 days). Pending deliveries are held while suspended. Five attempts by default; HTTP 410/413 and refused addresses are not retried. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List recent notification deliveries |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/sites/{sitename}/notifications/{id}/rotate-secret

<div data-operation="POST /v1/sites/{sitename}/notifications/{id}/rotate-secret"></div>

Shows the new secret once. Deliveries carry signatures for both secrets for 24 hours, until previous_valid_until, or drops the previous secret if it cannot be read. An MCP subscription must refresh events/subscribe with a new secret instead. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Rotate an owner-made signing secret |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | client_secret: the client manages this MCP subscription secret. notifications_not_enabled: the server has no PASSCODE_ENC_KEY. |

## POST /v1/sites/{sitename}/notifications/{id}/test

<div data-operation="POST /v1/sites/{sitename}/notifications/{id}/test"></div>

Sends notification.test with data {v:1, site, site_url, test:true}; webhook-id starts with evt_test_. The response reports the HTTP status and result category, never the receiver body. Test sends require an unexpired owner-made subscription and do not appear in delivery history. Challenge and test sends share limits of 10/minute per owner, host, and resolved IP, with at most 10 running at once. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Send a signed test notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | client_subscription: test sends require an unexpired owner-made subscription. |
| 503 | busy: ten verification or test sends are already running. |

## GET /v1/sites/{sitename}/push/devices

<div data-operation="GET /v1/sites/{sitename}/push/devices"></div>

Owner-only. Never discloses one visitor's device to another; a visitor sees their own devices only through their own subscribe response.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List every device subscribed on a site |
| 401 | Missing or invalid owner credentials. |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |

## DELETE /v1/sites/{sitename}/push/devices

<div data-operation="DELETE /v1/sites/{sitename}/push/devices"></div>

Owner-only. Removes every web-push device on the site at once, the owner's and every visitor's; the per-device unsubscribe route removes just one. WEB_PUSH_ACCOUNTS stays the install-wide flag; this is the dashboard's one-site bulk action.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Turn push off for this site |
| 401 | Missing or invalid owner credentials. |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |

## DELETE /v1/sites/{sitename}/push/devices/{id}

<div data-operation="DELETE /v1/sites/{sitename}/push/devices/{id}"></div>

Owner-only. The generic DELETE /v1/sites/{sitename}/notifications/{id} cannot reach a visitor-role push row (it belongs to that visitor's own account, never the owner's); this route is the owner-authorized way to remove any one push device on their own site, including a visitor's. kind must be web-push; a webhook or MCP-event subscription is out of scope here.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Device ID from the device list or a subscribe response. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Remove one push device |
| 401 | Missing or invalid owner credentials. |
| 404 | not_found (wrong site, not kind web-push, or no such id), or not_offered when the flag does not cover this account. |

## POST /v1/sites/{sitename}/push/mute

<div data-operation="POST /v1/sites/{sitename}/push/mute"></div>

Replaces the device's per-resource mute list without unsubscribing it. Ownership checked the same way unsubscribe is.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes |  |
| resources | array of string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Mute one device for named resources |
| 400 | invalid_request, or too_many_resources. |
| 403 | forbidden (sign in on this site first, for a visitor with no session). |
| 404 | site_not_found, not_offered, or not_found for an id that is not this caller's own device. |

## GET /v1/sites/{sitename}/push/rules

<div data-operation="GET /v1/sites/{sitename}/push/rules"></div>

Owner-only. Which events push and to whom (owner, the row's owner, both, or none) per resource (empty resource is the site default), plus the optional title and one named body column.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List the owner's per-resource push controls |
| 401 | Missing or invalid owner credentials. |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |

## PUT /v1/sites/{sitename}/push/rules

<div data-operation="PUT /v1/sites/{sitename}/push/rules"></div>

Owner-only. The owner's recipients choice narrows (never widens) the hard boundary already enforced in the event fan-out itself (a visitor's device never receives another visitor's data, and nothing fires back to the actor who made the write).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body_column | string |  |  |
| recipients | string, one of `owner`, `row_owner`, `both`, `none` |  |  |
| resource | string |  |  |
| title | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Set one resource's push controls |
| 400 | invalid_request, storage_not_found, or invalid_recipients. |
| 401 | Missing or invalid owner credentials. |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |

## POST /v1/sites/{sitename}/push/subscribe

<div data-operation="POST /v1/sites/{sitename}/push/subscribe"></div>

The owner (API key or an owner cookie) may subscribe site-wide or to one resource, naming the events like a webhook subscription. A signed-in visitor may only subscribe their own device, site-wide or to one optional resource, and is always forced to the record/file events regardless of what is asked for, so it can never be a site-level event; the fan-out itself, not the resource filter, is what stops a visitor's device from ever reaching another visitor's data. Reuses the webhook subscription cap, outbox and signing-secret storage (no parallel engine); needs PASSCODE_ENC_KEY.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| endpoint | string | yes | The push service endpoint from the browser's PushSubscription. |
| events | array of string |  | Owner only; a visitor's device is always the record/file events regardless of this field. |
| keys | object | yes |  |
| label | string |  |  |
| resource | string |  | Required for a visitor; optional for the owner (empty is site-wide). |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Subscribe one device to web push |
| 400 | invalid_request, invalid_events, storage_not_found, invalid_url, refused_address, or invalid_keys. |
| 403 | forbidden (sign in on this site first, for a visitor with no session). |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |
| 409 | too_many_notifications: the site subscription cap is reached. |

## POST /v1/sites/{sitename}/push/unsubscribe

<div data-operation="POST /v1/sites/{sitename}/push/unsubscribe"></div>

A visitor may only ever remove their own device (scoped by site and their own id); the owner may remove any device on their own site. Also happens automatically on a 404/410 from the push service, and on sign-out, viewer removal, account or site deletion.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | End push for one device |
| 400 | invalid_request. |
| 403 | forbidden (sign in on this site first, for a visitor with no session). |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |

## GET /v1/sites/{sitename}/push/vapid-key

<div data-operation="GET /v1/sites/{sitename}/push/vapid-key"></div>

Behind WEB_PUSH_ACCOUNTS; 404 not_offered for an account the flag does not cover. The public key PushManager.subscribe() needs as applicationServerKey, before the browser's own endpoint exists. Passcode-gated like every visitor route on this site.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Get the install's VAPID public key |
| 404 | site_not_found, or not_offered when the flag does not cover this account. |
