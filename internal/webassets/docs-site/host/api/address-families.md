# Address families

Connect a wildcard address family for your sites.

## GET /v1/me/address-families

<div data-operation="GET /v1/me/address-families"></div>

Every address family of the account, pending or verified. `available` says whether this server offers address families. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The account's families |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## POST /v1/me/address-families

<div data-operation="POST /v1/me/address-families"></div>

An address family: the account connects `*.<suffix>` once (e.g. `*.trips.example.com`), and every site of the account named `<site_prefix><label>` then answers at `https://<label>.<suffix>/`, including sites created later. `site_prefix` is optional (with `voucher-`, `meera.voucher.example.com` is the site `voucher-meera`). All the account's matching sites answer; another account's sites never do. The label is one DNS label; names in ADDRESS_FAMILY_RESERVED_LABELS (default `www`) never name a site.

Two DNS records prove it: the wildcard record `dns` (CNAME `*.<suffix>` to the CNAME target; `dns_a` is the A-record alternative) and the ownership record `dns_txt` (TXT `_simple-host.<suffix>` holding the family's token, kept in place). Nothing is served before both are seen. A pending family expires after 24 hours (ADDRESS_FAMILY_UNPROVEN_HOURS). Several accounts may wait on the same name: the first to prove it wins, and the others are dropped and emailed. Once verified the family is exclusive: no other account can connect it, a family overlapping it, or a custom domain under it (409 domain_taken). The same account may put a custom domain under its own family; the exact domain wins.

Certificates: the operator sets up a wildcard certificate for `*.<suffix>` (`cert_mode` `wildcard`, the only mode now). Until then `certificate.status` is `waiting_for_operator` and nothing is served; write to support@simple-host.app. `per_host` (a certificate per site name) is not available yet (400 cert_mode_unavailable).

Main address (`canonical`, default true): a site's main address is its custom domain or free name, else its most specific canonical family address (longest site_prefix, then lowest rank, then suffix), else its own address. When a family address is the main one, the site's own address redirects there (302) and sign-in and saves happen there (the own address refuses writes with use_custom_domain). Every family address of a site keeps working either way. `canonical: false` keeps the site's own address as the main one.

A verified family that fails its checks (the TXT record gone, the wildcard moved) is emailed about after 24 hours and disconnected after 72; connecting it again needs the TXT record again. Answers 200 with the existing family when the account already has it. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| canonical | boolean |  | A family address is the site's main address (unless the site has a domain of its own); false keeps the site's own address as the main one. |
| cert_mode | string, one of `wildcard`, `per_host` |  | wildcard: the operator's wildcard certificate. per_host is not available yet (400 cert_mode_unavailable). |
| family | string |  | Same as suffix |
| rank | integer |  | Tie-break between families with the same site_prefix: the lowest rank is the main address. |
| site_prefix | string |  | Sites named &lt;site_prefix&gt;&lt;label&gt; answer at &lt;label&gt;.&lt;suffix&gt;. Lowercase letters, digits and hyphens, not at the start, up to 40 characters. Default none. |
| suffix | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The account already has this family; it is returned unchanged |
| 201 | Connected; pending until both DNS records are seen |
| 400 | invalid_domain, invalid_site_prefix (lowercase letters, digits and hyphens, not at the start, up to 40 characters), invalid_rank (0 to 1000), cert_mode_unavailable (per_host), invalid_cert_mode. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | account_suspended |
| 409 | domain_taken (the name, or one over or under it, is connected to another account), address_family_limit (ADDRESS_FAMILIES_PER_ACCOUNT reached), address_families_unavailable (this server does not offer address families). |

## GET /v1/me/address-families/{suffix}

<div data-operation="GET /v1/me/address-families/{suffix}"></div>

The family, with its DNS records, status and certificate. `?sites=1` adds `sites`, every matching site and its address under the family. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| suffix | path | string | yes | The family: trips.example.com or *.trips.example.com (URL-escaped) |
| sites | query | string, one of `1` |  | 1: list every matching site and its address |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The family |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_address_family |

## PATCH /v1/me/address-families/{suffix}

<div data-operation="PATCH /v1/me/address-families/{suffix}"></div>

Send any of `site_prefix`, `rank` and `canonical`; the others stay. Changing the prefix changes which sites answer at once. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| suffix | path | string | yes | The family: trips.example.com or *.trips.example.com (URL-escaped) |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| canonical | boolean |  |  |
| rank | integer |  |  |
| site_prefix | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The family after the change |
| 400 | invalid_site_prefix or invalid_rank |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_address_family |

## DELETE /v1/me/address-families/{suffix}

<div data-operation="DELETE /v1/me/address-families/{suffix}"></div>

Every site stops answering under the family at once and goes back to its own address (or its domain). Connecting it again needs the TXT record again. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| suffix | path | string | yes | The family: trips.example.com or *.trips.example.com (URL-escaped) |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Disconnected |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_address_family |

## POST /v1/me/address-families/{suffix}/check

<div data-operation="POST /v1/me/address-families/{suffix}/check"></div>

Proves the family now (the TXT record, a random name under the wildcard resolving only to this server and, once live, one site answering over HTTPS) instead of waiting for the background check (every 10 minutes), then answers like GET with the new `status` and `last_error`. Owner only (API key); rate-limited per address and per account (429 rate_limited).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| suffix | path | string | yes | The family: trips.example.com or *.trips.example.com (URL-escaped) |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The family after the check |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_address_family (none, or it was let go) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
