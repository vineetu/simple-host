# Notifications

Signed HTTPS notifications for visitor storage writes on Simple Host and Simple Hack.

## GET /v1/hack/events/{slug}/website/notifications

<div data-operation="GET /v1/hack/events/{slug}/website/notifications"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List notifications for a site; an empty list returns 200, including an authorised Hack website not yet published |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/hack/events/{slug}/website/notifications

<div data-operation="POST /v1/hack/events/{slug}/website/notifications"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| slug | path | string | yes |  |

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

## PATCH /v1/hack/events/{slug}/website/notifications/{id}

<div data-operation="PATCH /v1/hack/events/{slug}/website/notifications/{id}"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |

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

## DELETE /v1/hack/events/{slug}/website/notifications/{id}

<div data-operation="DELETE /v1/hack/events/{slug}/website/notifications/{id}"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Remove a notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## GET /v1/hack/events/{slug}/website/notifications/{id}/deliveries

<div data-operation="GET /v1/hack/events/{slug}/website/notifications/{id}/deliveries"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List recent notification deliveries |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/hack/events/{slug}/website/notifications/{id}/rotate-secret

<div data-operation="POST /v1/hack/events/{slug}/website/notifications/{id}/rotate-secret"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Rotate an owner-made signing secret |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | client_secret: the client manages this MCP subscription secret. notifications_not_enabled: the server has no PASSCODE_ENC_KEY. |

## POST /v1/hack/events/{slug}/website/notifications/{id}/test

<div data-operation="POST /v1/hack/events/{slug}/website/notifications/{id}/test"></div>

Manage your own subscriptions for this custom event website through your current organiser role. Creation and delivery end when the event ends. Lists, history, and deletion remain available. Signing, sealed secrets, limits, tests, and rotation use the shared notification engine.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Send a signed test notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | client_subscription: test sends require an unexpired owner-made subscription. |
| 503 | busy: ten verification or test sends are already running. |

## GET /v1/notifications

<div data-operation="GET /v1/notifications"></div>

Owner credentials required on Host. On Hack, use a personal credential for your approved team membership; subscriptions belong to the person, not the holding account.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List notifications across the account |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## GET /v1/sites/{sitename}/notifications

<div data-operation="GET /v1/sites/{sitename}/notifications"></div>

Owner credentials required on Host. On Hack, use a personal credential for your approved team membership; subscriptions belong to the person, not the holding account.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List notifications for a site; an empty list returns 200, including an authorised Hack website not yet published |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/sites/{sitename}/notifications

<div data-operation="POST /v1/sites/{sitename}/notifications"></div>

The owner-entered URL is accepted without an MCP verification challenge. Generates a secret unless one is supplied; the response shows it once. Optional custom header and svix-* aliases are sent only for owner-made subscriptions. MCP and REST share the site subscription cap. On Hack, new subscriptions require approved membership before the team submission deadline; deliveries and existing refreshes continue afterward. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| sitename | path | string | yes |  |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

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

false also resumes suspension and makes held deliveries due now. Owner credentials required. On Hack, new subscriptions require approved membership before the team submission deadline; deliveries and existing refreshes continue afterward. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

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

Owner credentials required on Host. On Hack, use a personal credential for your approved team membership; subscriptions belong to the person, not the holding account.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Remove a notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## GET /v1/sites/{sitename}/notifications/{id}/deliveries

<div data-operation="GET /v1/sites/{sitename}/notifications/{id}/deliveries"></div>

Newest first, at most 200 within the configured history window (default 7 days). Pending deliveries are held while suspended. Five attempts by default; HTTP 410/413 and refused addresses are not retried. On Hack, new subscriptions require approved membership before the team submission deadline; deliveries and existing refreshes continue afterward. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List recent notification deliveries |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |

## POST /v1/sites/{sitename}/notifications/{id}/rotate-secret

<div data-operation="POST /v1/sites/{sitename}/notifications/{id}/rotate-secret"></div>

Shows the new secret once. Deliveries carry signatures for both secrets for 24 hours, until previous_valid_until, or drops the previous secret if it cannot be read. An MCP subscription must refresh events/subscribe with a new secret instead. On Hack, new subscriptions require approved membership before the team submission deadline; deliveries and existing refreshes continue afterward. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

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

Sends notification.test with data {v:1, site, site_url, test:true}; webhook-id starts with evt_test_. The response reports the HTTP status and result category, never the receiver body. Test sends require an unexpired owner-made subscription and do not appear in delivery history. Challenge and test sends share limits of 10/minute per owner, host, and resolved IP, with at most 10 running at once. On Hack, new subscriptions require approved membership before the team submission deadline; deliveries and existing refreshes continue afterward. Leaving, removal, moves, or loss of approval delete the affected subscriptions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| id | path | string | yes | Subscription ID from the notification list. |
| event | query | string |  | Hack event slug for a team site. Required when the person belongs to same-named teams in multiple events; the connector supplies it from the selected team. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Send a signed test notification |
| 401 | Missing or invalid owner credentials. |
| 403 | forbidden, or notification_access_ended when membership or the creation window no longer permits the operation. |
| 404 | not_found, or not_offered for an event account. |
| 409 | client_subscription: test sends require an unexpired owner-made subscription. |
| 503 | busy: ten verification or test sends are already running. |
