# Versions

Inspect retained deployments, preview and roll back.

## PUT /v1/sites/{sitename}/active-version

<div data-operation="PUT /v1/sites/{sitename}/active-version"></div>

Re-point the live site at an existing version number: an earlier one, or one stored with publish=false (which then counts as live). errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| version_number | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Active version changed |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## GET /v1/sites/{sitename}/versions

<div data-operation="GET /v1/sites/{sitename}/versions"></div>

The versions this instance still holds, newest first. An instance may be configured to keep only the last few deploys, or only the live one, in which case older versions are absent here and from disk. KEEP_VERSIONS controls it; event boxes are installed with 1. Every item carries author (who made it, as given with ?author= on the deploy; empty when none was given). ?pending=1 lists only the versions stored with publish=false that have never been live; DELETE /v1/sites/{sitename}/versions/{version} discards one of those.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| pending | query | string |  | 1 lists only the versions stored with publish=false that have never been live (status ready, not the live one) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Versions (newest first) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## DELETE /v1/sites/{sitename}/versions/{version}

<div data-operation="DELETE /v1/sites/{sitename}/versions/{version}"></div>

Removes a version stored with publish=false that nobody has made live since: its files go for good and the number is never reused, under the same per-site lock as deploys. The live version and any version that has been live before stay (retention prunes those, discard does not). Owner only (a full API key or the connector): a deploy-only key or a team key is refused. Rate-limited like the other site operations (30 burst, 0.5/s). Connector: discard_version.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| version | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Discarded |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key), a team key (team_key_scope), or a site that is taken down (site_suspended) |
| 404 | Site not found, or no such kept version (version_not_found, including an upload that never finished) |
| 409 | The version is live (version_live: only a version that was never live can be discarded), or has been live before (version_published) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/sites/{sitename}/versions/{version}/files

<div data-operation="GET /v1/sites/{sitename}/versions/{version}/files"></div>

Owner only. Walks the version directory; paths are relative, use forward slashes, and are sorted. Pruned versions return 404.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| version | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Version metadata and files |
| 400 | Invalid version |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Site not found or version not found (including pruned versions) |

## GET /v1/sites/{sitename}/versions/{version}/files/{path}

<div data-operation="GET /v1/sites/{sitename}/versions/{version}/files/{path}"></div>

Owner only. Streams raw bytes with a Content-Type based on the file extension (application/octet-stream fallback; HTML uses UTF-8). Directories return 404. Paths containing '..', leading slashes, backslashes, NUL, or symlinks escaping the version directory are rejected. Pruned versions return 404.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| version | path | integer | yes |  |
| path | path | string | yes | Relative file path (may contain forward slashes) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Raw file bytes |
| 400 | Invalid version |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Site not found |

## POST /v1/sites/{sitename}/versions/{version}/preview-link

<div data-operation="POST /v1/sites/{sitename}/versions/{version}/preview-link"></div>

Owner only. Mints a link, working for anyone who has it for one hour, that shows one kept version (live, earlier, or stored with publish=false) exactly as visitors would see it, on the site's own host: https://&lt;site&gt;.&lt;handle&gt;.simple-host.app/__preview/&lt;n&gt;/&lt;token&gt;/ (or briefly, as a fallback while the person's certificate is not ready, &lt;handle&gt;.simple-host.app/&lt;site&gt;/__preview/...). The token is an HMAC over site, version and expiry; it stops working after an hour or a server restart. Preview pages are noindex and never cached, and saves made from them are refused (403 preview_read_only; told apart by the Referer, so a page that suppresses its Referer is not stopped: this guards against accidents, and the pages are the owner's own). Make the version live with PUT .../active-version.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| sitename | path | string | yes |  |
| version | path | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The preview link |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The operator has taken the site down (code site_suspended) |
| 404 | Site or version not found (including pruned versions) |
| 409 | No preview link (code preview_unavailable): the site has no address of its own yet, or the server gives sites none (every site on one shared address, as on a small box), which the message says |
