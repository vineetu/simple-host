# Analytics

Owner-scoped website traffic.

## GET /v1/analytics/sites

<div data-operation="GET /v1/analytics/sites"></div>

One call returning the class split for all of the caller's sites, ordered by `person.views` descending — the "which of my sites are actually being read" view. Exists so a dashboard can sort by traffic without one request per site.

Not under `/v1/sites/...` because that would collide with a site named "analytics". Pass `all=1` (admins only) to widen the scope to every site on the instance; non-admins get 403 rather than a silently narrowed list.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| owner | query | string |  | Act on the sites of this team (its handle, from GET /v1/teams) instead of your own: every member may do here whatever the owner may. Checked on every request, so a removed member is refused on their next one. 404 owner_not_found when you are not a member of a team of that name (the analytics routes keep their older meaning for any other handle); 403 deploy_only_key for a deploy-only key. Your own handle, or the admin key, changes nothing. Query string only: an "owner" in a JSON body is ignored. |
| days | query | integer |  | Number of UTC days to include (default 30, clamp 1..365) |
| all | query | string, one of `1` |  | Admins only — include every site on the instance. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Per-site totals, highest `person.views` first. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | `all=1` requested by a non-admin |

## GET /v1/sites/{sitename}/analytics

<div data-operation="GET /v1/sites/{sitename}/analytics"></div>

Owner-scoped view and visitor counts derived server-side from the access log (no client beacon). Every bucket is split four ways by who was asking: `person` (no automation signature, and not the owner's own visits), `bot` (crawlers, AI scrapers, SEO tools, security scanners, HTTP libraries), `infra` (uptime probes and health checks aimed at the site) and `owner` (the site owner's own visits, see Site.views). Read `person` as the audience number — a total that folds in `infra` is dominated by monitoring, not people.

`visitors` are unique visitors: COUNT(DISTINCT hashed IP) over the window asked for. The salt is stable, so `totals.*.visitors` is genuine uniques for the range and is NOT the sum of the `daily` figures — someone who visited on five days is one unique in `totals` and five in `daily`. IP-based, so a shared office NAT reads as one visitor and a phone switching wifi→cellular reads as two.

`daily` is zero-filled dense for the requested window (oldest → newest). `hourly` is always 24 zero-filled buckets ending with the hour in progress. Only successful document responses (200/304, non-asset paths) count as views, for every class alike. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| owner | query | string |  | Act on the sites of this team (its handle, from GET /v1/teams) instead of your own: every member may do here whatever the owner may. Checked on every request, so a removed member is refused on their next one. 404 owner_not_found when you are not a member of a team of that name (the analytics routes keep their older meaning for any other handle); 403 deploy_only_key for a deploy-only key. Your own handle, or the admin key, changes nothing. Query string only: an "owner" in a JSON body is ignored. |
| sitename | path | string | yes |  |
| days | query | integer |  | Number of UTC days to include (default 30, clamp 1..365) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Analytics for the site |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## GET /v1/sites/{sitename}/analytics/geo

<div data-operation="GET /v1/sites/{sitename}/analytics/geo"></div>

Per-country traffic for one site over the last `days` UTC days, split by the same `person` / `bot` / `infra` / `owner` classes as the rest of analytics and ordered by `person.views` descending (ties broken by country code).

Country is resolved on the server from a local IP-range table refreshed from a public dataset — no visitor address is ever sent to a geolocation service, and the raw address is still never stored. Country data: "IP Geolocation by DB-IP" (https://db-ip.com), CC BY 4.0.

`country` is ISO-3166 alpha-2. `"XX"` / `"Unknown"` is traffic the table could not place (an unlisted block, or a server whose ranges have not been loaded); it is listed rather than dropped, so the countries still add up to the site's totals.

`visitors` is COUNT(DISTINCT hashed IP) over the whole window, exactly as in the main analytics endpoint — genuine uniques for the range, not the sum of the daily figures. Someone whose address changes country mid-range counts once in each, so the columns can total slightly more than the site's own figure. `unknown` is always zero here: the pre-classifier history has no addresses left to place. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| owner | query | string |  | Act on the sites of this team (its handle, from GET /v1/teams) instead of your own: every member may do here whatever the owner may. Checked on every request, so a removed member is refused on their next one. 404 owner_not_found when you are not a member of a team of that name (the analytics routes keep their older meaning for any other handle); 403 deploy_only_key for a deploy-only key. Your own handle, or the admin key, changes nothing. Query string only: an "owner" in a JSON body is ignored. |
| sitename | path | string | yes |  |
| days | query | integer |  | Number of UTC days to include (default 30, clamp 1..365) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Per-country traffic, most real people first. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## GET /v1/sites/{sitename}/analytics/top

<div data-operation="GET /v1/sites/{sitename}/analytics/top"></div>

The 20 most viewed pages (path on the site, without the query string or fragment, percent-escapes decoded once and `//` collapsed) and the 20 top referring domains over the last `days` UTC days, people only, most views first. A site keeps at most ANALYTICS_PAGES_PER_SITE_DAY pages and ANALYTICS_REFERRERS_PER_SITE_DAY domains a day; the views of further new ones that day are listed as `(other)`. A referrer is recorded as its domain alone — never a full address, path or query — and a link from the site's own address is not counted. Owner only (API key); the platform admin may pass `owner`.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| owner | query | string |  | Act on the sites of this team (its handle, from GET /v1/teams) instead of your own: every member may do here whatever the owner may. Checked on every request, so a removed member is refused on their next one. 404 owner_not_found when you are not a member of a team of that name (the analytics routes keep their older meaning for any other handle); 403 deploy_only_key for a deploy-only key. Your own handle, or the admin key, changes nothing. Query string only: an "owner" in a JSON body is ignored. |
| sitename | path | string | yes |  |
| days | query | integer |  | Number of UTC days to include (default 30, clamp 1..365) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Top pages and referring domains |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 429 | Rate limited (code rate_limited). |
