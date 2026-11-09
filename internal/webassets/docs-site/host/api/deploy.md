# Deploy

Publish inline JSON files or a tar.gz/zip archive.

## GET /import/{token}

<div data-operation="GET /import/{token}"></div>

Does not consume the link. No-store and no-referrer; token authority, no cookies or API key on the page.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Shared-theme HTML upload page |
| 404 | Link expired |

## GET /v1/imports/status/{import_id}

<div data-operation="GET /v1/imports/status/{import_id}"></div>

Public by a random 128-bit ID; same-origin browsers only. Returns stages, counts and at most 10 plain warnings, with no owner, report, credentials or filenames. Finished imports expire after 24 hours; accounts retain at most 20 records. Usable links and receiving/queued/running imports retain their records. Expired unused links are swept. Status reads have a separate per-IP limiter (60 burst, one per second); retry after the Retry-After header on 429.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| import_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Current progress |
| 404 | Not found |
| 429 | Too many status reads from this address |

## POST /v1/imports/{token}

<div data-operation="POST /v1/imports/{token}"></div>

Same-origin browser upload, authorized by the one-time token from create_import_link. Checks the creating connection's sign-in again. Streams up to 10 files to disk, sniffs XML, ZIP, TAR and gzip TAR, validates archive paths and byte limits, then converts and publishes asynchronously. Invalid files and a full queue leave the link usable. A reserved link returns 409. Publishing can replace the live version.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | path | string | yes |  |

**Request body:** multipart/form-data

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Files validated and import queued |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 403 | Sign-in ended (import_link_signed_out) or another browser origin |
| 404 | Import link expired |
| 409 | Already uploading |
| 413 | Payload too large (on saved data, code item_too_large; on a deploy: site_too_large (per-upload limit), site_total_too_large (whole website footprint for sites created from SITE_TOTAL_CAP_FROM), or account_storage_full (all account website files including Recently deleted). Totals count the separate live copy and kept versions after pruning; backend storage and saved data are excluded. The error names the limit and current size for total caps, and hint gives photo-compression and file-cleanup tips) |
| 429 | Too many uploads from this address |
| 503 | Busy; try again in a few minutes with the same link |

## GET /v1/photo-links

<div data-operation="GET /v1/photo-links"></div>

The create_upload_link photos_url opens /photos?site=&lt;name&gt;#t=&lt;token&gt;. The page sends the fragment token in X-Photo-Token, never in a request URL or Referer, and immediately removes the fragment from browser history. No browser sign-in is needed. The link lasts 60 minutes for OAuth callers; API-key links are capped at the key’s known expiry (reported in expires_at), and permits up to 10 successful saves to one site. The creating OAuth grant (not the original access token) or API key is checked again on every request; only the site name is returned, with no account data or keys. The binding is to the site name for the link’s lifetime: renaming and recreating that name sends later photos to the replacement site within the same account. Apex Host and Origin checks and IP rate limits apply.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| X-Photo-Token | header | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The site this link can add photos to |
| 403 | Origin refused or creating sign-in revoked |
| 404 | This photo link has expired. Ask your chat for a new one. |
| 409 | A save is in progress |
| 429 | Rate limit exceeded |

## POST /v1/photo-links

<div data-operation="POST /v1/photo-links"></div>

Uses the same X-Photo-Token, sign-in recheck, Origin policy, and rate limit as GET. Proxies only to POST /v1/sites/{sitename}/photos for the site fixed when the link was created. Query parameters cannot change the destination. The same multipart, body, photo, and storage caps apply, and all other live files are kept. Successful saves consume one of 10 uses; failed saves can be retried until expiry. X-Photo-Reconcile checks do not spend a use and remain available after the tenth save until link expiry. Short-lived credentials are revoked after each request and never leave the server. In-memory links expire on server restart.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| X-Photo-Token | header | string | yes |  |
| X-Photo-Reconcile | header | string |  | Set to 1 after an uncertain save to check matching live photo bytes without writing a version. A missing match returns 409, never another upload. |

**Request body:** multipart/form-data

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Photos added in one new live version, or matching photos confirmed without a write |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Origin or deploy permission refused |
| 404 | Site missing, or this photo link has expired. Ask your chat for a new one. |
| 409 | A save is in progress or a file/directory conflicts with the photo path |
| 413 | Photo |
| 429 | Rate limit or photo save capacity exceeded; Retry-After advises when to retry |

## POST /v1/sites/{sitename}

<div data-operation="POST /v1/sites/{sitename}"></div>

Upload a `.tar.gz` or `.zip` (for framework builds, binary assets, large sites). Max 100 MB. errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | Conflict: the code says which (site_exists, domain_taken, append_only, custom_domain_required, not_an_object) |
| 413 | Payload too large (on saved data, code item_too_large; on a deploy: site_too_large (per-upload limit), site_total_too_large (whole website footprint for sites created from SITE_TOTAL_CAP_FROM), or account_storage_full (all account website files including Recently deleted). Totals count the separate live copy and kept versions after pruning; backend storage and saved data are excluded. The error names the limit and current size for total caps, and hint gives photo-compression and file-cleanup tips) |

## PUT /v1/sites/{sitename}

<div data-operation="PUT /v1/sites/{sitename}"></div>

Upload a new version of an existing site you own. Creates a new version and activates it; with ?publish=false the version is only stored (visitors keep seeing the live one) and the answer adds unpublished_version, preview_url and preview_expires_at. With ?create=1 a missing site is created instead (201, with the same checks as POST): one call from CI, e.g. `curl -X PUT ".../v1/sites/<name>?create=1" -H "X-API-Key: $KEY" --data-binary @site.tar.gz`. errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| publish | query | boolean |  | false stores the new version without making it live (updates only; a new site's first version always goes live: 400) |
| create | query | string |  | 1 creates the site when you have none of that name (reserved names, the site quota and publish=false are refused as on POST), so one PUT deploys from CI whether or not the site exists |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created (?create=1 and no site of that name) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 413 | Payload too large (on saved data, code item_too_large; on a deploy: site_too_large (per-upload limit), site_total_too_large (whole website footprint for sites created from SITE_TOTAL_CAP_FROM), or account_storage_full (all account website files including Recently deleted). Totals count the separate live copy and kept versions after pruning; backend storage and saved data are excluded. The error names the limit and current size for total caps, and hint gives photo-compression and file-cleanup tips) |

## POST /v1/sites/{sitename}/files

<div data-operation="POST /v1/sites/{sitename}/files"></div>

The LLM-friendly deploy path — send every file inline in one request, no archiving. Text goes in `files`; base64-encoded binary goes in `files_base64`. `index.html` is required. Relative paths only (`..`/absolute are rejected); secret files (`.env`, `.git/*`, `id_rsa`) are dropped and script extensions (`.sh .py .php …`) are rejected. Use PUT to update an existing site (409 site_exists says so); PUT with ?create=1 creates or updates in one call. errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

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
| files | object | yes | relative path -&gt; file contents (text). index.html required unless PUT uses keep=1. |
| files_base64 | object |  | relative path -&gt; base64-encoded binary contents. A path cannot also appear in `files`. |
| remove_files | array of string |  | Paths to drop from the live version; only with PUT keep=1. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Site created |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | Conflict: the code says which (site_exists, domain_taken, append_only, custom_domain_required, not_an_object) |

## PUT /v1/sites/{sitename}/files

<div data-operation="PUT /v1/sites/{sitename}/files"></div>

Same as POST, but for an existing site you own. Creates a new version and activates it; with ?publish=false the version is only stored (visitors keep seeing the live one) and the answer adds unpublished_version, preview_url and preview_expires_at. With ?create=1 a missing site is created instead (201, with the same checks as POST), so CI deploys in one call; without it a missing site is 404. With ?keep=1, send only changed files: other files in the live version are retained byte-for-byte, and remove_files drops listed relative paths. remove_files requires keep=1; index.html is not required in a patch. The merged version meets the ordinary file and storage caps. errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| publish | query | boolean |  | false stores the new version without making it live (updates only; a new site's first version always goes live: 400) |
| create | query | string |  | 1 creates the site when you have none of that name (reserved names, the site quota and publish=false are refused as on POST), so one PUT deploys from CI whether or not the site exists |
| author | query | string |  | Who made this version: free text, trimmed, control characters removed, at most 80 characters. Stored on the version and returned as author by GET /v1/sites/{sitename}/versions. |
| keep | query | integer |  | Keep unchanged files from the live version; merged files use ordinary unpacked-file caps |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| files | object | yes | relative path -&gt; file contents (text). index.html required unless PUT uses keep=1. |
| files_base64 | object |  | relative path -&gt; base64-encoded binary contents. A path cannot also appear in `files`. |
| remove_files | array of string |  | Paths to drop from the live version; only with PUT keep=1. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Site updated |
| 201 | Site created (?create=1 and no site of that name) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## POST /v1/sites/{sitename}/photos

<div data-operation="POST /v1/sites/{sitename}/photos"></div>

The Add photos page at /photos prepares images on the person's device. Upload 1 to 30 WebP or JPEG files as repeated photos multipart parts. The endpoint accepts X-API-Key, including the dashboard's stored owner key and account-wide deploy-only keys (not site-scoped), with the normal deploy permissions and rate limit. Visitor cookies do not authorize it. Browser origins must match the apex. Default limits are 1,000,000 bytes per photo (PHOTO_MAX_BYTES) and 2048 pixels on the longest side (PHOTO_MAX_DIMENSION). The server validates headers and container structure without decoding pixels or re-encoding. This is header-only validation, not a full decode of the compressed image. JPEG allows JFIF APP0, ICC_PROFILE APP2 (16 KB total), and Adobe APP14; comments and other APP markers are refused. WebP ICCP is limited to 16 KB; EXIF, XMP, animation, unknown chunks and trailing data are refused. ICC contents are not inspected; browser canvas output carries no profile or a standard profile. Signed-in and photo-token saves share PHOTO_MAX_CONCURRENT slots (default 3), acquired before body reads. Both entry points set a body-read deadline of 60 seconds (PHOTO_BODY_TIMEOUT_SECONDS) on the real response writer; unsupported writers continue normally. Full capacity returns 429 with error "Busy, try again in a moment" and Retry-After of 2 seconds, without queueing. Filenames must match ^[a-z0-9][a-z0-9-]{0,59}\.(webp|jpg)$ and the extension must match the contents. Files go under images/, with -2, -3, and further suffixes for collisions. Every other live file is kept. After an uncertain response, resend the batch with X-Photo-Reconcile: 1. This only checks for distinct live files with matching names (including collision suffixes) and identical bytes. A full match returns the current version and reconciled: true; it confirms presence, not which request wrote the files. A missing match returns 409 photo_save_uncertain. Checks never create a version. Ordinary saves create one normal published version, with normal file quotas and retention. Hosted and small-box Simple Host only;

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |
| X-Photo-Reconcile | header | string |  | Set to 1 after an uncertain save to check matching live photo bytes without writing a version. A missing match returns 409, never another upload. |

**Request body:** multipart/form-data

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Photos added in one new live version, or matching photos confirmed without a write |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Origin or deploy permission refused |
| 404 | Site not found or feature not offered |
| 409 | A file or directory conflicts with the photo path |
| 413 | Photo |
| 429 | Deploy rate limit or photo save capacity exceeded; Retry-After advises when to retry |

## POST /v1/uploads/{token}

<div data-operation="POST /v1/uploads/{token}"></div>

Use the private link returned by create_upload_link with archive true from a shell . Works once for one site for 15 minutes, including failed uploads. Sign-in is checked again at upload. Links stop working when the person signs out, disconnects the app or their sign-in expires. Creates the site if missing, else publishes a new version, public at its address on success. Never share the link or put it in a web page. Usual deploy and scope checks apply.

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
| 413 | Payload too large (on saved data, code item_too_large; on a deploy: site_too_large (per-upload limit), site_total_too_large (whole website footprint for sites created from SITE_TOTAL_CAP_FROM), or account_storage_full (all account website files including Recently deleted). Totals count the separate live copy and kept versions after pruning; backend storage and saved data are excluded. The error names the limit and current size for total caps, and hint gives photo-compression and file-cleanup tips) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## PUT /v1/uploads/{token}

<div data-operation="PUT /v1/uploads/{token}"></div>

Use the private link returned by create_upload_link with archive true from a shell . Works once for one site for 15 minutes, including failed uploads. Sign-in is checked again at upload. Links stop working when the person signs out, disconnects the app or their sign-in expires. Creates the site if missing, else publishes a new version, public at its address on success. Never share the link or put it in a web page. Usual deploy and scope checks apply.

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
| 413 | Payload too large (on saved data, code item_too_large; on a deploy: site_too_large (per-upload limit), site_total_too_large (whole website footprint for sites created from SITE_TOTAL_CAP_FROM), or account_storage_full (all account website files including Recently deleted). Totals count the separate live copy and kept versions after pruning; backend storage and saved data are excluded. The error names the limit and current size for total caps, and hint gives photo-compression and file-cleanup tips) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
