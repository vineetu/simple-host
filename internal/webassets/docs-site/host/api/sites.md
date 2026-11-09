# Sites

List, inspect and manage websites.

## GET /v1/export

<div data-operation="GET /v1/export"></div>

No API key: the signed `token` is the whole authorization, for one site, for 10 minutes. Streams the archive described under `export.tar.gz`. An expired, altered or unknown token, or one for a site since deleted or moved to another account, answers 404 with code `export_link_invalid`.

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

## GET /v1/idle/keep

<div data-operation="GET /v1/idle/keep"></div>

Opened from the idle-site email; no sign-in, the token is the authorization. Shows a page with a "Keep it online" button and changes nothing (mail scanners follow links). 404 page when the link was used, replaced or has expired; 403 page while the site is taken down. Emailed links carry the token in the fragment (`#t=`), so it never reaches a server log: without `t` the GET returns a small page whose script posts the fragment's token back with `peek=1`, which shows this confirmation. Links sent before v0.7.1 carry `?t=` and keep working until they expire.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| t | query | string |  | The token from an older email |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML confirmation page with the button |
| 403 | HTML page; the site is taken down |
| 404 | HTML page; the link was used or has expired |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/idle/keep

<div data-operation="POST /v1/idle/keep"></div>

The confirmation page's button (with `peek=1`, only the confirmation page). The site stays up and its idle clock restarts; the link is then spent. HTML page.

No authentication required.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML page confirming the site stays up |
| 403 | HTML page; the site is taken down |
| 404 | HTML page; the link was used or has expired |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/idle/restore

<div data-operation="GET /v1/idle/restore"></div>

Shows a page with a "Restore it" button for a site the idle-site cleanup moved to Recently deleted, and changes nothing. No sign-in. Emailed links carry the token in the fragment (`#t=`), so it never reaches a server log: without `t` the GET returns a small page whose script posts the fragment's token back with `peek=1`, which shows this confirmation. Links sent before v0.7.1 carry `?t=` and keep working until they expire.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| t | query | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML confirmation page with the button |
| 403 | HTML page; the site is taken down |
| 404 | HTML page; the link was used or has expired |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/idle/restore

<div data-operation="POST /v1/idle/restore"></div>

Brings the site back exactly as it was (versions, saved data, names), with a fresh idle clock, while it is still in Recently deleted; the link is then spent (with `peek=1`, only the confirmation page). HTML page.

No authentication required.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML page confirming the site is back |
| 403 | HTML page; the site is taken down |
| 404 | HTML page; the link was used or has expired |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/sites

<div data-operation="GET /v1/sites"></div>

Returns the authenticated account's own sites, newest edited first (updated_at desc, then created_at desc). all=1 widens an admin's list to every account's sites; refused (403) for a non-admin. errors 403 no_personal_sites, 403 team_site_only, 403 team_key_scope, 401 team_key_inactive, 409 team_sites_not_ready, 409 submissions_closed, 403 team_site_taken_down.

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

One change per request. With {"offline": true} the site is taken offline: every address answers a plain "This site is offline" page (503), and visitor saves (state and collection writes from a page, private lists included), visitors' reads of its state and lists, and visitor sign-in are refused with 403 site_offline; files, versions and saved data are kept, and the owner's key or connector still deploys, reads and writes. {"offline": false} puts it back online. An operator take-down wins over it, and a taken-down site cannot be switched (403 site_suspended). The answer is the site object with `offline` and a `note`. With {"name": "new-name"} it renames the database row and on-disk site directory. The new name uses the same validation and per-owner uniqueness rule as creation. A bound custom domain remains attached. Links to the old name keep working: its site host, person-path and sites.* addresses 302 to the new address (path and query kept), through any chain of renames, until a site with the old name is created again (the new site wins); the response says so with old_url_status "redirects". Deleting the site stops the redirects (restoring brings them back); purging it removes them.

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

Takes the site offline at once, with every version, its saved state and collections. It stays in Recently deleted (GET /v1/me/deleted-sites) for 7 days with its name held, and POST /v1/sites/{sitename}/restore brings it back exactly as it was; after that it is removed for good. Creating a site with a held name answers 409 with recently_deleted true.

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

## GET /v1/sites/{sitename}/access

<div data-operation="GET /v1/sites/{sitename}/access"></div>

Owner only (the owner's key or connector token; a deploy-only key is refused). `access` is `anyone` (anyone with the address, or with the passcode when the site has one) or `specific` (only the owner and the named viewers, each signed in with visitor sign-in on the site's own address). `viewers` lists the named viewers by the email they sign in with. Never cached (Cache-Control: no-store). Hosted Simple Host and small boxes only;

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Who can open the site |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key) |
| 404 | Not found |

## PUT /v1/sites/{sitename}/access

<div data-operation="PUT /v1/sites/{sitename}/access"></div>

Owner only. `{"access": "specific"}`: every address of the site (its own address, a custom domain, a free name, an address family, the person address when it is the home page) then shows a sign-in page (401, noindex, no-store, nothing of the site in it) to anyone not signed in on that address, and a plain "This site is private" page (403) with Switch account and Sign out to someone signed in who is not the owner or a named viewer. The site's saved data (state, collections, storage resources, whatever their site_passcode says) answers 403 `site_private` to them too; the owner's key and connector keep working, and previews still open. With no viewers named only the owner can open it. `{"access": "anyone"}` opens it again and keeps the list. The site leaves the person page while it is specific. A site has named viewers or a passcode, never both (409 passcode_set). Every request is checked against the list as it is then: a removed person is refused on their next request.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| access | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Who can open the site now, with a plain `note` |
| 400 | Body is not {"access": "anyone"} or {"access": "specific"} (invalid_request) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The operator has taken the site down (site_suspended), or a deploy-only key (deploy_only_key) |
| 404 | Not found |
| 409 | passcode_set (remove the passcode first), named_viewers_need_own_address (every site on this server shares one address), sign_in_unavailable (this server has no email codes or Google), or named_viewers_unavailable |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/sites/{sitename}/export-link

<div data-operation="POST /v1/sites/{sitename}/export-link"></div>

Returns a link that downloads the same archive as `export.tar.gz` without an API key, for people who use Simple Host only through a chat app's connector (the `export_site` tool). The link is signed for this owner and this site, expires after 10 minutes, and stops working if the site is deleted or the server restarts. Anyone holding it can download the copy until then, so hand it to the person only.

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

Streams the site's published files (`files/`), its saved JSON state (`state.json`) and its collections (`collections.json`: every list, private ones included, each entry as `{id, created_at, submitted_by, data}`; `submitted_by` only on private lists) as one archive. Exists because an event instance is destroyed when the event ends and nothing is backed up, so the person who built something can take it with them. The saved data comes first: the files usually exist somewhere else, the JSON only lives here.

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

## PUT /v1/sites/{sitename}/keep

<div data-operation="PUT /v1/sites/{sitename}/keep"></div>

Mark the site Keep (or clear the mark). Where the idle-site cleanup is on (simple-host.app), a site with no visits by people and no new version for 90 days gets its owner an email with a "Keep it" link (and a link to their page, where they can sign in and download it); 30 days later with nothing done it moves to Recently deleted (restorable for 7 days, with a link in a second email). A site marked Keep is never flagged, and marking it ends a pending warning. Sites with their own domain or claimed name are never flagged either. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| keep | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The flag as set |
| 400 | Body is not {"keep": true\|false} |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 423 | The site is taken down by the operator |

## PUT /v1/sites/{sitename}/keep-versions

<div data-operation="PUT /v1/sites/{sitename}/keep-versions"></div>

Every deploy stores a full copy of the site as a new version. keep_versions sets how many this site keeps: N from 1 to 1000 keeps the newest N plus any older live version. 0 goes back to the account default (KEEP_VERSIONS_OVERRIDES), else this server's setting (KEEP_VERSIONS; 0 there means keep every version). Setting it removes the older versions at once, for good (their files and their rows; removed_versions names them), and every later deploy removes whatever falls outside it. The value is on the site (keep_versions in GET /v1/sites) when set. Owner only (a full API key; a deploy-only key is refused, since this deletes history). Only accounts in KEEP_VERSIONS_SELF_SET (current or old handles) may set a count when that knob is set. Others get 403 keep_versions_fixed with the plain message "Simple Host keeps your 4 latest versions". Hosted defaults: 4; the chhotabreak, vineetu and jot-transcribe exceptions default to 10.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| keep_versions | integer | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The setting as stored, and what it removed |
| 400 | Body is not {"keep_versions": N} with N from 0 to 1000 |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | A deploy-only key (deploy_only_key), or an account whose version count is fixed (keep_versions_fixed) |
| 404 | Not found |
| 423 | The site is taken down by the operator |
| 429 | Too many site changes from this address; try again shortly |

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

Owner only (the owner's key or connector token; a deploy-only key is refused). Every address of the site (its own address, the person path while its certificate is pending, a free name, a custom domain) then answers a plain "This site is protected" page (401, noindex, no-store; nothing of the site in it; robots.txt answers `Disallow: /`) until the visitor enters the passcode. A right passcode sets an unlock cookie for that one host (HttpOnly, SameSite=Lax, `__Host-` prefixed, about 400 days) that lasts until the passcode changes or the owner signs everyone out; `.app` and `.site` addresses each need their own unlock. Send `{"passcode": "..."}` (any characters, at least PASSCODE_MIN_LENGTH, default 6, at most 128; digits only is fine; spaces around it are trimmed) or `{"generate": true}` for 6 random digits. Setting the same passcode again changes nothing; a different one signs everyone out. The site leaves the person page. The owner's key, connector token and the admin key still read and write its data; previews still open. Take-down (410) and offline (503) win over the passcode. There is no end date: take the site offline or delete it instead. A passcode keeps out people who don't have it: search engines, link previews, and anyone who finds or is forwarded the link without it. Anyone given it can open the site and pass it on. It is not a login, it does not say who visited, and it does not make saved data private per person.

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

Brings a site back from Recently deleted under the same name and address, with every version, its saved data, collections and connected address, live at once.

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

## POST /v1/sites/{sitename}/viewers

<div data-operation="POST /v1/sites/{sitename}/viewers"></div>

Owner only. Adds each email (trimmed and lower-cased; letters, digits and `._%+-` before the `@`; matched exactly, with no folding of dots or `+tags`) to the site's named viewers and sets access to `specific` in the same call. At most SITE_VIEWERS_MAX (default 50) per site. `added` lists the new ones, `already_listed` the ones that were there.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| emails | array of string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Who can open the site now |
| 400 | invalid_request, invalid_email (names the address), or too_many_viewers (more than SITE_VIEWERS_MAX in one call) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | site_suspended or deploy_only_key |
| 404 | Not found |
| 409 | too_many_viewers (the list would pass SITE_VIEWERS_MAX), passcode_set, named_viewers_need_own_address, sign_in_unavailable or named_viewers_unavailable |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## DELETE /v1/sites/{sitename}/viewers/{email}

<div data-operation="DELETE /v1/sites/{sitename}/viewers/{email}"></div>

Owner only. The person's next request is refused. Access does not change: removing the last viewer leaves the site open only to its owner.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| email | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Who can open the site now (`removed` names the email) |
| 400 | invalid_email |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | No such site, or the email is not a named viewer (viewer_not_found) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## PUT /v1/sites/{sitename}/visibility

<div data-operation="PUT /v1/sites/{sitename}/visibility"></div>

Set whether this site is listed on the owner's account page at https://&lt;handle&gt;.simple-host.app/. `public` = listed there; `unlisted` = hidden from it. New sites are created `unlisted` — listing is an explicit act, not a side effect of deploying.

An unlisted site is hidden from the showcase, and from search engines unless the site has its own domain or name (a custom domain, an address-family address, or a claimed free name): every address of an unlisted site without one answers with `X-Robots-Tag: noindex, nofollow`, and its `/robots.txt` says `Disallow: /` unless the site ships its own robots.txt (the site's file wins; the header still applies). A public site, or an unlisted one with its own domain or name, gets neither, on every address. The change applies on the next request. Both values are equally reachable by anyone with the URL: `unlisted` is not privacy and must never be described as such. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| visibility | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Visibility updated |
| 400 | Invalid visibility value |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
