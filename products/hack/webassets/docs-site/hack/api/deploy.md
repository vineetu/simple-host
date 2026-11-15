# Deploy

Publish inline JSON files or a tar.gz/zip archive.

## POST /v1/sites/{sitename}

<div data-operation="POST /v1/sites/{sitename}"></div>

Upload a `.tar.gz` or `.zip` (for framework builds, binary assets, large sites). Max 100 MB. On simple-hack.app (EVENTS=hosted) only a team key (or a connector bound to a team) deploys, to its own team's site only; errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down. Choose `access: specific` at creation for only the owner and named viewers from the first request; omitted or `anyone` keeps the default (anyone with the address). Optional `viewers` emails require `specific`, are normalized and deduplicated, and respect SITE_VIEWERS_MAX. Named viewers require sign-in and the site's own address and are unavailable on Simple Hack. Archive options use query parameters `access` and `viewers` (comma-separated or repeated).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |
| access | query | string, one of `anyone`, `specific` |  | Creation only. Default anyone; specific allows only the owner and named viewers from the first request. Simple Hack refuses specific. |
| viewers | query | array of string |  | Optional creation viewer emails, only with access specific. Comma-separated or repeated viewers parameters; SITE_VIEWERS_MAX applies. Not added to existing sites. |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | site_exists, access_differs (create-or-update), named_viewers_unavailable, sign_in_unavailable, named_viewers_need_own_address, or idempotency_key_reused |
| 413 | The request or deployment exceeds its applicable size limit. |

## PUT /v1/sites/{sitename}

<div data-operation="PUT /v1/sites/{sitename}"></div>

Upload a new version of an existing site you own. Creates a new version and activates it; with ?publish=false the version is only stored (visitors keep seeing the live one) and the answer adds unpublished_version, preview_url and preview_expires_at. With ?create=1 a missing site is created instead (201, with the same checks as POST): one call from CI, e.g. `curl -X PUT ".../v1/sites/<name>?create=1" -H "X-API-Key: $KEY" --data-binary @site.tar.gz`. On simple-hack.app (EVENTS=hosted) only a team key (or a connector bound to a team) deploys, to its own team's site only; errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down. Choose `access: specific` at creation for only the owner and named viewers from the first request; omitted or `anyone` keeps the default (anyone with the address). Optional `viewers` emails require `specific`, are normalized and deduplicated, and respect SITE_VIEWERS_MAX. Named viewers require sign-in and the site's own address and are unavailable on Simple Hack. With create=1, access/viewers apply when creating. On an existing site, specific refuses 409 access_differs unless it is already specific; viewers are not added. anyone never changes an existing site's access. Without create=1, access/viewers are 400 invalid_request; later use set_site_access / grant_site_viewer. Archive options use query parameters `access` and `viewers` (comma-separated or repeated).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| publish | query | boolean |  | false stores the new version without making it live (updates only; a new site's first version always goes live: 400) |
| create | query | string, one of `1`, `0`, `true`, `false` |  | 1 creates the site when you have none of that name (reserved names, the site quota and publish=false are refused as on POST), so one PUT deploys from CI whether or not the site exists |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |
| access | query | string, one of `anyone`, `specific` |  | Creation only. Default anyone; specific allows only the owner and named viewers from the first request. Simple Hack refuses specific. |
| viewers | query | array of string |  | Optional creation viewer emails, only with access specific. Comma-separated or repeated viewers parameters; SITE_VIEWERS_MAX applies. Not added to existing sites. |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created (?create=1 and no site of that name) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 409 | site_exists, access_differs (create-or-update), named_viewers_unavailable, sign_in_unavailable, named_viewers_need_own_address, or idempotency_key_reused |
| 413 | The request or deployment exceeds its applicable size limit. |

## POST /v1/sites/{sitename}/files

<div data-operation="POST /v1/sites/{sitename}/files"></div>

The LLM-friendly deploy path — send every file inline in one request, no archiving. Text goes in `files`; base64-encoded binary goes in `files_base64`. `index.html` is required. Relative paths only (`..`/absolute are rejected); secret files (`.env`, `.git/*`, `id_rsa`) are dropped and script extensions (`.sh .py .php …`) are rejected. Use PUT to update an existing site (409 site_exists says so); PUT with ?create=1 creates or updates in one call. On simple-hack.app (EVENTS=hosted) only a team key (or a connector bound to a team) deploys, to its own team's site only; errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down. Choose `access: specific` at creation for only the owner and named viewers from the first request; omitted or `anyone` keeps the default (anyone with the address). Optional `viewers` emails require `specific`, are normalized and deduplicated, and respect SITE_VIEWERS_MAX. Named viewers require sign-in and the site's own address and are unavailable on Simple Hack. JSON body fields: `access` and `viewers`.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| access | string, one of `anyone`, `specific` |  | Creation only (POST or PUT create=1). Default anyone; specific allows only the owner and named viewers from the first request. No access change on existing sites. |
| files | object | yes | relative path -&gt; file contents (text). index.html required unless PUT uses keep=1. |
| files_base64 | object |  | relative path -&gt; base64-encoded binary contents. A path cannot also appear in `files`. |
| remove_files | array of string |  | Paths to drop from the live version; only with PUT keep=1. |
| viewers | array of string |  | Optional emails, only with access specific; normalized, deduplicated, limited by SITE_VIEWERS_MAX. Added only when the site is created. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | site_exists, access_differs (create-or-update), named_viewers_unavailable, sign_in_unavailable, named_viewers_need_own_address, or idempotency_key_reused |

## PUT /v1/sites/{sitename}/files

<div data-operation="PUT /v1/sites/{sitename}/files"></div>

Same as POST, but for an existing site you own. Creates a new version and activates it; with ?publish=false the version is only stored (visitors keep seeing the live one) and the answer adds unpublished_version, preview_url and preview_expires_at. With ?create=1 a missing site is created instead (201, with the same checks as POST), so CI deploys in one call; without it a missing site is 404. With ?keep=1, send only changed files: other files in the live version are retained byte-for-byte, and remove_files drops listed relative paths. remove_files requires keep=1; index.html is not required in a patch. The merged version meets the ordinary file and storage caps. On simple-hack.app (EVENTS=hosted) only a team key (or a connector bound to a team) deploys, to its own team's site only; errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down. Choose `access: specific` at creation for only the owner and named viewers from the first request; omitted or `anyone` keeps the default (anyone with the address). Optional `viewers` emails require `specific`, are normalized and deduplicated, and respect SITE_VIEWERS_MAX. Named viewers require sign-in and the site's own address and are unavailable on Simple Hack. With create=1, access/viewers apply when creating. On an existing site, specific refuses 409 access_differs unless it is already specific; viewers are not added. anyone never changes an existing site's access. Without create=1, access/viewers are 400 invalid_request; later use set_site_access / grant_site_viewer. JSON body fields: `access` and `viewers`.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| publish | query | boolean |  | false stores the new version without making it live (updates only; a new site's first version always goes live: 400) |
| create | query | string, one of `1`, `0`, `true`, `false` |  | 1 creates the site when you have none of that name (reserved names, the site quota and publish=false are refused as on POST), so one PUT deploys from CI whether or not the site exists |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |
| keep | query | integer, one of `1` |  | Keep unchanged files from the live version; merged files use ordinary unpacked-file caps |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| access | string, one of `anyone`, `specific` |  | Creation only (POST or PUT create=1). Default anyone; specific allows only the owner and named viewers from the first request. No access change on existing sites. |
| files | object | yes | relative path -&gt; file contents (text). index.html required unless PUT uses keep=1. |
| files_base64 | object |  | relative path -&gt; base64-encoded binary contents. A path cannot also appear in `files`. |
| remove_files | array of string |  | Paths to drop from the live version; only with PUT keep=1. |
| viewers | array of string |  | Optional emails, only with access specific; normalized, deduplicated, limited by SITE_VIEWERS_MAX. Added only when the site is created. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created (?create=1 and no site of that name) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 409 | site_exists, access_differs (create-or-update), named_viewers_unavailable, sign_in_unavailable, named_viewers_need_own_address, or idempotency_key_reused |

## POST /v1/uploads/{token}

<div data-operation="POST /v1/uploads/{token}"></div>

Use the private link returned by create_upload_link with archive true from a shell (the default on Simple Hack). Works once for one site for 15 minutes, including failed uploads. Sign-in is checked again at upload. Links stop working when the person signs out, disconnects the app or their sign-in expires. Creates the site if missing, else publishes a new version, public at its address on success. Never share the link or put it in a web page. Usual deploy and scope checks apply.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | path | string | yes |  |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Sign-in ended, or deploy scope or team rules refused the upload |
| 404 | Unknown, used or expired link |
| 409 | Conflict: the code says which (site_exists, domain_taken, append_only, custom_domain_required, not_an_object) |
| 413 | The request or deployment exceeds its applicable size limit. |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## PUT /v1/uploads/{token}

<div data-operation="PUT /v1/uploads/{token}"></div>

Use the private link returned by create_upload_link with archive true from a shell (the default on Simple Hack). Works once for one site for 15 minutes, including failed uploads. Sign-in is checked again at upload. Links stop working when the person signs out, disconnects the app or their sign-in expires. Creates the site if missing, else publishes a new version, public at its address on success. Never share the link or put it in a web page. Usual deploy and scope checks apply.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | path | string | yes |  |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Sign-in ended, or deploy scope or team rules refused the upload |
| 404 | Unknown, used or expired link |
| 409 | Conflict: the code says which (site_exists, domain_taken, append_only, custom_domain_required, not_an_object) |
| 413 | The request or deployment exceeds its applicable size limit. |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
