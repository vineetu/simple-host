# Sites

List, inspect and manage websites.

## GET /v1/export

<div data-operation="GET /v1/export"></div>

Download an owner-authorised archive of website files and stored data. It may include historical private data from before the previous storage model was retired. Keep the archive and any download link private.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | A gzipped tar archive |
| 404 | The link is not valid or has expired |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/sites

<div data-operation="GET /v1/sites"></div>

Returns the authenticated account's own sites, newest edited first (updated_at desc, then created_at desc). all=1 widens an admin's list to every account's sites; refused (403) for a non-admin. On simple-hack.app (EVENTS=hosted) only a team key (or a connector bound to a team) deploys, to its own team's site only; errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| query | query | string |  | Every word must match the name, domain or summary (case-insensitive substrings). |
| created_after | query | string |  | Created on or after RFC3339 or YYYY-MM-DD (UTC midnight). |
| created_before | query | string |  | Created on or before RFC3339 or YYYY-MM-DD (UTC whole day). |
| updated_after | query | string |  | Updated on or after RFC3339 or YYYY-MM-DD (UTC midnight). |
| updated_before | query | string |  | Updated on or before RFC3339 or YYYY-MM-DD (UTC whole day). |
| order | query | string |  | Sort key; default updated (last edited first). |
| limit | query | integer |  | Maximum results, clamped to 500; default 200 when any filter or paging parameter is present. |
| offset | query | integer |  | Skip this many matching sites. |
| all | query | string |  | Admin only: every account's sites instead of just this one. 403 for a non-admin. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | List of sites |
| 400 | Invalid date (bad_date), order (bad_order), or non-negative integer required for limit/offset (bad_pagination). |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | all=1 from a non-admin. |

## PATCH /v1/sites/{sitename}

<div data-operation="PATCH /v1/sites/{sitename}"></div>

Change the website's offline status or name. While offline, visitor storage reads and writes are refused; versions and stored resources remain available to the owner.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| name | string |  | The new name (renames the site). |
| offline | boolean |  | true takes the site offline; false puts it back online. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Renamed (old_name, name, old_url, site_url, old_url_status) or switched (the site object with offline and note) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The operator has taken the site down (code site_suspended) |
| 404 | Not found |
| 409 | Conflict: the code says which (site_exists, domain_taken, append_only, custom_domain_required, not_an_object) |

## DELETE /v1/sites/{sitename}

<div data-operation="DELETE /v1/sites/{sitename}"></div>

Take the website offline and move it to Recently deleted, retaining its versions and stored resources until the restoration deadline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Site deleted (restorable for 7 days) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## POST /v1/sites/{sitename}/export-link

<div data-operation="POST /v1/sites/{sitename}/export-link"></div>

Download an owner-authorised archive of website files and stored data. It may include historical private data from before the previous storage model was retired. Keep the archive and any download link private.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The link |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 409 | Idempotency-Key reused with a different body (idempotency_key_reused). |

## GET /v1/sites/{sitename}/export.tar.gz

<div data-operation="GET /v1/sites/{sitename}/export.tar.gz"></div>

Download an owner-authorised archive of website files and stored data. It may include historical private data from before the previous storage model was retired. Keep the archive and any download link private.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | A gzipped tar archive |
| 404 | Not found |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/sites/{sitename}/lock

<div data-operation="GET /v1/sites/{sitename}/lock"></div>

Owner only (the owner's key or connector token; a deploy-only key is refused). Says whether the site asks visitors for a passcode and, when it does, the passcode itself and when it was set. The passcode is stored sealed with a server key (AES-256-GCM under PASSCODE_ENC_KEY), so the owner can read it back; the admin's routes never read it. If the server's key changed, `passcode` is absent and `note` says to set a new one. Never cached (Cache-Control: no-store).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The site's passcode state |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key) |
| 404 | Not found |

## PUT /v1/sites/{sitename}/lock

<div data-operation="PUT /v1/sites/{sitename}/lock"></div>

Set or generate the owner's website passcode through this trusted REST route. Visitors must unlock each host before viewing the site. A passcode is a site gate, not per-person privacy for a storage resource.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| generate | boolean |  | true: the server picks 6 random digits |
| passcode | string |  | At least PASSCODE_MIN_LENGTH characters (default 6) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The passcode is set (the answer carries it) |
| 400 | Body is not {"passcode": "..."} or {"generate": true} (invalid_request), or the passcode is too short or too long (invalid_passcode) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The operator has taken the site down (site_suspended), or a deploy-only key (deploy_only_key) |
| 404 | Not found |
| 409 | passcodes_not_enabled (SITE_PASSCODES is off or PASSCODE_ENC_KEY is unset on this server), or passcode_needs_own_address (every site on this server shares one address, as on a small box or an event box, so an unlock would cover every site) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## DELETE /v1/sites/{sitename}/lock

<div data-operation="DELETE /v1/sites/{sitename}/lock"></div>

Owner only (the owner's key or connector token; a deploy-only key is refused). Every address of the site opens to everyone again. Removing a passcode the site does not have is not an error.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The site has no passcode |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key) |
| 404 | Not found |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/sites/{sitename}/lock/sign-out-everyone

<div data-operation="POST /v1/sites/{sitename}/lock/sign-out-everyone"></div>

Owner only (the owner's key or connector token; a deploy-only key is refused). Ends every unlock at once, on every address: each visitor must enter the passcode again. The passcode stays the same.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Everyone is signed out |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key) |
| 404 | Not found |
| 409 | The site has no passcode (no_passcode) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/sites/{sitename}/restore

<div data-operation="POST /v1/sites/{sitename}/restore"></div>

Restore a website from Recently deleted with its versions, stored resources and connected address.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The restored site |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Site quota reached |
| 404 | No site with that name in Recently deleted |
| 409 | That site is live |
