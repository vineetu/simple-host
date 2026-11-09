# Custom domains

Claim a free address or connect a custom domain.

## GET /v1/sites/{sitename}/domain

<div data-operation="GET /v1/sites/{sitename}/domain"></div>

Returns the bound domain and status, or `{domain:null,status:null}` when none is set. Includes the two DNS records to add when a custom domain is bound (`dns` and the TXT ownership record `dns_txt`), the certificate's progress, the earlier address still serving while the domain is pending, and `failing_since` when a verified domain has stopped passing its checks. Owner only (API key).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Current domain binding |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |

## POST /v1/sites/{sitename}/domain

<div data-operation="POST /v1/sites/{sitename}/domain"></div>

Optional. Every site already lives at its own address `https://<site>.<handle>.simple-host.app/`, with sign-in and private lists. This gives it a nicer address; the site moves there and its old address redirects. A free address: send `{"domain": "<name>.simple-host.app"}` (one DNS label: lowercase letters, digits and hyphens, not at the start or end, up to 63 characters). It is active at once with no DNS step — the answer has `status: active` and `verified_at`, and no `dns`. First come, first served: 409 domain_taken if another site holds it or it is already someone's handle (handles and free names share one namespace); reserved names (www, sites, api, admin, mail, cname and the platform's own hosts) get 400 name_reserved; anything else malformed gets 400 invalid_name. Once claimed it behaves exactly like a custom domain: the site is served at its root with a same-origin /v1 API and visitor sign-in, its person-address URL 302s there, and private collections take submissions there. A site has one own address: claiming one replaces a custom domain and vice versa. A free name the site lets go (switching, DELETE, or deleting the site) stays with it: it redirects to the site's current address and nobody else can claim it. A custom domain: bind ONE domain to the site (status starts as `pending`). Returns the two DNS records the owner must create: `dns` (CNAME for a subdomain, A for an apex) and `dns_txt`, the ownership record (TXT `_simple-host.<domain>` holding this site's token), which proves the domain is theirs and must stay in place. Nothing is verified or certified without it. A name another site on this server already serves is refused by the certificate issuer (failed, "already served here"). Once both records are seen, its certificate is requested automatically (at most 5 new domain certificates per account per day) (`certificate_status`: pending → issuing → live, or failed with the reason in `last_error`, retried) and the domain is served at its root, usually within minutes. If the site already has a working own address (a free name or an earlier domain), it keeps serving there (`previous_domain`) until the new domain is verified; then the old address redirects to it. Binding the site's current working domain again changes nothing; binding its `previous_domain` drops the pending one. Until first verified, a binding is provisional: another site can take it over until it has passed its ownership proof, and it expires 24 hours after binding unless DNS already points here. A verified domain stays exclusive while it works; if it fails every check for 24 hours the owner is emailed, and after 72 hours it is disconnected (the site's own address serves again) and can be connected afresh, with the TXT record, by whoever holds it; while it is being released, binding it answers 409 domain_releasing. The platform's own zones are refused (400 invalid_domain). A domain under the account's own address family may be connected; the exact domain wins. `*.<domain>` answers 400 use_address_family (see POST /v1/me/address-families). Owner only (API key).

www and the bare domain (where the certificate issuer runs, as on simple-host.app; absent elsewhere): binding `example.com` also sets up `www.example.com` (and binding `www.example.com` sets up `example.com`) as a redirect-only host that forwards (301) to the name chosen. The answer names it (`partner_domain`) with the record it needs (`dns_partner`); the TXT record on the chosen name covers both. It goes on the same certificate only if it points here and no other site on this server answers it; otherwise the chosen name goes live alone and `partner_status` is `not_set_up` with `partner_note` saying why.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| Idempotency-Key | header | string |  | Opaque key for a create request. For 24 hours, the same authenticated account, POST path and query, key and exact request body replay the original handler status and body, including errors, without executing it again. A different body under that key returns 409 idempotency_key_reused. Concurrent identical requests wait for the first response. Stored only in this server's memory; a restart clears it. Authentication and rate limiting still apply to replays. A replay does not extend a minted link's expiry; use a new key to request a new link. Storage row creates support owner credentials and signed-in visitors; anonymous writes ignore this header. Storage replay also checks current resource permissions and is scoped to the resolved site. |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| domain | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Domain bound — pending DNS for a custom domain, active at once for a free &lt;name&gt;.simple-host.app address |
| 400 | Invalid domain. For a &lt;name&gt;.simple-host.app address, `code` is name_reserved or invalid_name. A wildcard (`*.<domain>`) is use_address_family (connect it with POST /v1/me/address-families). |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 409 | domain_taken: the domain is verified (or past its ownership proof) for another site, it is under another account's address family, or the &lt;name&gt;.simple-host.app address is held by another site or is someone's handle. domain_releasing: the domain was just disconnected and the certificate issuer is still letting go of it; try again in 10 minutes. |

## DELETE /v1/sites/{sitename}/domain

<div data-operation="DELETE /v1/sites/{sitename}/domain"></div>

Disconnects the domain GET shows. If it was still pending and the site kept serving at an earlier address (`previous_domain`), that address becomes the site's domain again; otherwise the site is served at its own address. A disconnected free &lt;name&gt;.simple-host.app stays with the site and redirects to its current address; links to a disconnected custom domain stop working. Owner only (API key). Pass `domain` (the address being disconnected, as GET showed it) so a domain changed in the meantime is never dropped by mistake.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |
| domain | query | string |  | The address you mean to disconnect. If the site's domain is something else by now, nothing changes and the answer is 409 `domain_changed`. |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Domain unbound |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | Not found |
| 409 | `domain_changed`: the site's domain is not the one named in `domain`; nothing was changed. |

## POST /v1/sites/{sitename}/domain/check

<div data-operation="POST /v1/sites/{sitename}/domain/check"></div>

Re-proves the site's own domain at once (its TXT ownership record holds the site's token, it resolves here and serves the site over HTTPS; the check connects only to this server's public address and never follows a redirect), instead of waiting for the background check every two minutes, then answers exactly like GET .../domain with the new status and `last_error`. A free &lt;name&gt;.simple-host.app address needs no proof and is answered as is. Owner only (API key); rate-limited per IP and per account (a few at once, then one per 30 seconds; 429 with code rate_limited).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| sitename | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The domain binding after the check (same shape as GET .../domain) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | No such site, or the site has no domain connected (no_domain) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
