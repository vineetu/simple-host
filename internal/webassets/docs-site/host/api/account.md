# Account

Account profile, credentials and connected apps.

## GET /v1/me

<div data-operation="GET /v1/me"></div>

Current user

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Current user |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## PATCH /v1/me

<div data-operation="PATCH /v1/me"></div>

Changing the handle changes every address (https://&lt;handle&gt;.simple-host.app/ and https://&lt;site&gt;.&lt;handle&gt;.simple-host.app/). Before anything is published it changes freely. After that (even once every site is gone) it can change once every 30 days: the old handle stays reserved for this account and every old address redirects to the new one. A new handle gets its own certificate; until it is issued, sites briefly use the fallback https://&lt;handle&gt;.simple-host.app/&lt;site&gt;/. What pages kept in visitors' browsers starts empty at the new address, and visitors sign in again.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| display_name | string |  | Trimmed; empty clears the name. Never used in URLs. |
| handle | string |  | The &lt;handle&gt; in your address https://&lt;handle&gt;.simple-host.app/. Must not be reserved or already taken (handles and free &lt;name&gt;.simple-host.app addresses share one namespace). |
| signin_alerts | boolean |  | Sign-in alert emails (default true): one email after each sign-in or app connection, at most one per browser or app a day. Needs one of the account's own API keys (a connected app or the admin key gets 400 not_an_account_key). |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated account profile |
| 400 | Invalid profile, or signin_alerts sent without an account key (code not_an_account_key) |
| 401 | API key required |
| 409 | The handle cannot be had: code handle_taken (another account's handle or old handle, or a site's claimed free name; error says "That address is taken: &lt;handle&gt;.simple-host.app is already in use. Try another.") or handle_reserved. A malformed handle is 400 invalid_handle. |
| 429 | The handle already changed within the last 30 days after publishing |

## DELETE /v1/me

<div data-operation="DELETE /v1/me"></div>

Deletes the account at once and for good; nothing goes to Recently deleted and nothing can be restored. Removed: every site (including ones in Recently deleted) with its files, versions, saved data, lists and analytics; keys; connected apps; visitor sign-ins; and every entry this person sent to other people's lists while signed in; their address is taken off the changes they made to other people's page data (writes made without signing in and older public-list entries are not linked to anyone and stay; support@simple-host.app helps with those). The handle, old handles and claimed &lt;name&gt;.simple-host.app addresses are retired, never given to anyone else; custom domains are released. A confirmation email goes to the account's address. Offer GET /v1/me/export.zip first. Refused for a suspended account or one holding a site the operator took down (the operator handles those), an admin account, or one still holding event hostnames.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm | string | yes | The account's handle, or its email when it has no handle. Case-insensitive. |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Deleted |
| 400 | confirm missing (code confirm_required) or does not match (code confirm_mismatch) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Suspended account (code account_suspended), a site of the account taken down (code site_suspended) or admin account (code admin_account) |
| 409 | The account still holds event hostnames (code event_hostnames) |
| 429 | Too many requests from this address |

## POST /v1/me/api-key/rotate

<div data-operation="POST /v1/me/api-key/rotate"></div>

The "Sign out everywhere" button. Replaces all of the authenticated owner's API keys with one new key and returns it. Every older key stops working immediately, and every connected app (see /v1/me/connections) is disconnected. Keys are stored only as SHA-256 hashes, so this response is the only time the new key is shown.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | API key rotated |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## GET /v1/me/bio

<div data-operation="GET /v1/me/bio"></div>

Owner key or connector only, never deploy-only keys. Available on hosted Simple Host and small-box path installs, outside hosted events.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Plain-text bio and character limit |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Showcase settings unavailable |

## PUT /v1/me/bio

<div data-operation="PUT /v1/me/bio"></div>

Trimmed plain text, empty clears. SHOWCASE_BIO_MAX_LENGTH controls the character limit (default 280, range 1–2000). Used by both public HTML and JSON feed. Owner key or connector only, excluding deploy-only keys and hosted events.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| bio | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved plain-text bio and configured limit |
| 400 | Missing bio or bio_too_long |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Showcase settings unavailable |

## GET /v1/me/connections

<div data-operation="GET /v1/me/connections"></div>

The AI apps (ChatGPT, Claude, Grok, a GPT Action) this account has connected through the Simple Host connector (OAuth at /oauth/authorize, MCP at /mcp). Each is listed once, however many times it was connected.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Connected apps, newest first |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## DELETE /v1/me/connections/{client_id}

<div data-operation="DELETE /v1/me/connections/{client_id}"></div>

Revokes every token this account granted to the app. It has to be connected again, with a fresh sign-in, to be used.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| client_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Disconnected |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | No connected app with that id |

## GET /v1/me/deleted-sites

<div data-operation="GET /v1/me/deleted-sites"></div>

Sites this account deleted in the last 7 days. Each keeps its files, versions, saved data, collections and addresses, and its name stays taken, until purge_at; POST /v1/sites/{sitename}/restore brings it back until then.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Recently deleted sites, most recently deleted first |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## POST /v1/me/email

<div data-operation="POST /v1/me/email"></div>

Emails a 6-digit code to the new address and another to the current one (15 minutes, 3 tries). Send both to POST /v1/me/email/verify to finish. Asking looks the same for every address. Needs one of the account's own API keys: a connected app or the admin key gets 400 not_an_account_key. Refused for the plugin reviewer account (and for moving any account to its address), admin, event and preview accounts, and accounts whose sign-in name is not an email address. Rate-limited per address and per account, like sign-in.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Codes sent |
| 400 | Not an account key (code not_an_account_key), not an email (code invalid_email), already the sign-in email (code same_email), or the reviewer's address (code reserved_email) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The reviewer, an admin, event or preview account, or one with no email to confirm from (codes reviewer_account, admin_account, event_account, preview_account, no_current_email) |
| 429 | Too many requests |

## GET /v1/me/email/undo

<div data-operation="GET /v1/me/email/undo"></div>

Shows a page with an Undo button and changes nothing. No sign-in; the token is the authorization. 404 page when the link was used or is older than 7 days. Emailed links carry the token in the fragment (`#t=`), so it never reaches a server log: without `t` the GET returns a small page whose script posts the fragment's token back with `peek=1`, which shows this confirmation. Links sent before v0.7.1 carry `?t=` and keep working until they expire.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| t | query | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML confirmation page with the button |
| 404 | HTML page; the link was used or has expired |
| 429 | Too many requests |

## POST /v1/me/email/undo

<div data-operation="POST /v1/me/email/undo"></div>

The confirmation page's button (with `peek=1`, only the confirmation page). Puts the account back on the old address, signs out every API key and every sign-in on sites, removes Google/GitHub sign-ins and connected apps added since the change, and spends every undo link of the account. HTML page.

No authentication required.

**Request body:** application/x-www-form-urlencoded

**Responses:**

| Status | Description |
| --- | --- |
| 200 | HTML page; the account signs in with the old address again |
| 404 | HTML page; the link was used or has expired |
| 409 | HTML page; another account signs in with the old address now |
| 429 | Too many requests |

## POST /v1/me/email/verify

<div data-operation="POST /v1/me/email/verify"></div>

Moves the account to the new address: from now on sign-in codes go there. The handle, sites and connected apps stay; every other API key of the account is signed out (the one making the call stays); linked Google/GitHub sign-ins stay (GET /v1/me/identities lists them). Open sign-in codes for the old address stop working, the account's emailed idle-site links end, and the old address gets a notice naming the new one masked (n***@example.com) with an undo link valid for 7 days. An address another account signs in with is refused here, after the codes are verified (409 email_taken). Needs one of the account's own API keys.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | The 6-digit code emailed to the new address |
| current_code | string | yes | The 6-digit code emailed to the current address |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Changed |
| 400 | Not an account key (code not_an_account_key), a code missing (code codes_required), no change waiting (code no_pending_change), or the reviewer's address (code reserved_email) |
| 401 | Wrong or expired code, or too many tries (code invalid_code) |
| 403 | The reviewer, an admin, event or preview account (see step 1) |
| 409 | Another account signs in with that address (code email_taken) |
| 429 | Too many requests |

## GET /v1/me/export.tar.gz

<div data-operation="GET /v1/me/export.tar.gz"></div>

**Deprecated.**

The address Download my data had before it became a .zip. Serves exactly what GET /v1/me/export.zip serves: the same .zip, with Content-Type application/zip and a .zip filename, under the same rules. Use /v1/me/export.zip.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The same .zip as GET /v1/me/export.zip |
| 400 | Not one of the account's own API keys (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The account is suspended (code account_suspended) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/me/export.zip

<div data-operation="GET /v1/me/export.zip"></div>

Streams one .zip of everything held about the account: README.txt; account.json (email, handle, display name, created, earlier handles, claimed names including retired ones, custom domains, linked Google/GitHub sign-ins); keys.json (name, last 4 characters, created, last used; never keys or hashes); connected_apps.json (name, connected, last used); visitor.json (sites signed in to as a visitor with first sign-in and last seen, every entry sent to other people's lists while signed in, and `changed`: every change made to other people's page data or list entries while signed in, as site, what, change and when); sites/&lt;name&gt;/ for every live site in the same layout as GET /v1/sites/{sitename}/export.tar.gz (files/, state.json, collections.json); and recently-deleted/&lt;name&gt;/ for every site in Recently deleted, in the same layout plus deleted.json (deleted_at, removed_for_good_at). Visitor analytics hold no personal data (salted hashes) and are not included. Needs one of the account's own API keys: a connected app or the admin key gets 400 not_an_account_key. A suspended account gets 403 account_suspended and writes to support@simple-host.app for a copy of its data.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | A .zip file (Content-Disposition names it simple-host-&lt;handle&gt;-&lt;date&gt;.zip) |
| 400 | Not one of the account's own API keys (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | The account is suspended (code account_suspended); write to support@simple-host.app for a copy of the data |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/me/home

<div data-operation="GET /v1/me/home"></div>

Owner key or connector only; deploy-only keys refused. Available on hosted Simple Host and small-box path installs, outside hosted events. Null selects the showcase. Rename follows the site ID; deleting it clears the setting.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Home-page choice |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Personal homes unavailable in hosted events mode |

## PUT /v1/me/home

<div data-operation="PUT /v1/me/home"></div>

The hosted person address serves the chosen site at root, with its passcode, sign-in and storage. On a small-box path install the public person page redirects to the chosen site’s existing URL; the owner dashboard keeps its address. Normal site address stays available. Offline, taken-down or suspended homes fall back to the showcase. A missing path beginning with another site name redirects to that site. No changes to other accounts.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved choice |
| 400 | Invalid choice |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Site not owned or deleted |
| 409 | Site requires the legacy content host |

## GET /v1/me/identities

<div data-operation="GET /v1/me/identities"></div>

Own API key only (400 not_an_account_key otherwise).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The linked sign-ins |
| 400 | Not an account key (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## DELETE /v1/me/identities/{id}

<div data-operation="DELETE /v1/me/identities/{id}"></div>

That Google or GitHub account no longer signs in to yours. Own API key only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Unlinked |
| 400 | Not an account key (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | No such sign-in on this account |

## GET /v1/me/keys

<div data-operation="GET /v1/me/keys"></div>

Every key on the account, newest first. `current` marks the key this request was made with. Keys issued before names existed have no `name` or `last4` (shown as "Earlier key"). `last_used_at` is updated at most every 5 minutes. A key unused for 180 days stops working. `idle_expires_at` says when (KEY_IDLE_EXPIRY_DAYS); one minted with `expires_in_days` stops at `expires_at`. A key that has stopped stays listed with `expired` until revoked.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The account's keys |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## POST /v1/me/keys

<div data-operation="POST /v1/me/keys"></div>

Mints a new key with the given name (for a CI secret or another machine) and returns it once; it is stored only as a hash. `scope: deploy` makes a deploy-only key: it may create, update, roll back and list sites, read their versions and files, and make preview links; every other route (delete, rename, domains, keys, saved data and lists, analytics, the account, connecting apps, and the same through /mcp) answers 403 deploy_only_key. Use it for a CI secret. `expires_in_days` (1-3650) gives the key a fixed expiry; without it the key stops only when it goes unused (KEY_IDLE_EXPIRY_DAYS). Needs one of the account's own keys (the admin env key or a connected app gets 400 not_an_account_key). An account holds at most 50 keys (409 key_limit; keys that have stopped working do not count, and are deleted 30 days after they stop). A deploy key never carries admin powers. A deploy key can publish code that runs when you open your own site (signed in there, that code acts with your powers over its saved data); treat it like the site itself; a key revoked or rotated away mid-request gets 401 invalid_api_key.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| expires_in_days | integer |  | Optional fixed expiry, in days from now |
| name | string | yes | Any label except the names Simple Host gives its own keys (dashboard sign-in, agent sign-in, event account, reviewer sign-in, replacement key; 400) |
| scope | string, one of `full`, `deploy` |  | deploy: create, update, roll back and list sites and make preview links, nothing else |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Key created. `api_key` is shown only here. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | The account already holds 50 keys (code key_limit); revoke one first. |

## DELETE /v1/me/keys/{id}

<div data-operation="DELETE /v1/me/keys/{id}"></div>

The key stops working at once; the account's other keys and connected apps are untouched. Revoking the key this request uses signs this caller out. Needs one of the account's own keys.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Revoked |
| 400 | Not one of the account's own keys (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | No key with that id on this account |

## POST /v1/me/password

<div data-operation="POST /v1/me/password"></div>

Available only when PASSWORD_SIGNIN=on, for an account that already has a password (404 for one that does not). current_password is verified against the same limiters as sign-in (passwordLoginLimiter, keyed by the normalized username, and passwordIPLimiter); a wrong current password counts against both and refunds neither, and answers 403. new_password must be 8-256 characters and differ from current_password. Only a real account API key may call this; a connector's OAuth access token, even one otherwise valid for the REST API, is refused. Does not revoke keys, sessions or connector tokens; every other sign-in stays valid.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| current_password | string | yes |  |
| new_password | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Password changed |
| 400 | invalid_password (too short, or same as current_password) |
| 401 | Missing or invalid API key |
| 403 | Wrong current password (code bad_credentials), or a connector access token (code connector_not_allowed): sign in with an account API key instead. |
| 404 | not found: PASSWORD_SIGNIN is off, or this account has no password |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/me/sign-out

<div data-operation="POST /v1/me/sign-out"></div>

Deletes the API key sent with this request and cascades to its apex SSO passes and every visitor session minted from them, then clears the apex cookie; the account's other keys and connected apps are untouched. The dashboard's Sign out calls this before it clears the key from the browser. Needs one of the account's own keys (the admin env key or a connected app gets 400 not_an_account_key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Signed out; the key no longer works |
| 400 | Not one of the account's own keys (code not_an_account_key) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## GET /v1/sites/{sitename}/showcase

<div data-operation="GET /v1/sites/{sitename}/showcase"></div>

Owner key or connector only, never deploy-only keys. Available on hosted Simple Host and small-box path installs, outside hosted events.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Pin and manual order |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Site not owned or deleted |

## PUT /v1/sites/{sitename}/showcase

<div data-operation="PUT /v1/sites/{sitename}/showcase"></div>

Send pinned and/or order; omitted fields stay unchanged. Pinned sites come first, then ascending order, with creation time breaking ties. Unlisted and unavailable sites stay hidden in HTML and JSON, even when pinned. Preferences follow rename and restore. Owner key or connector only, never deploy-only keys; hosted events excluded.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| order | integer |  |  |
| pinned | boolean |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved pin and manual order |
| 400 | Missing or invalid pinned/order |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Deploy-only key refused |
| 404 | Site not owned or deleted |
