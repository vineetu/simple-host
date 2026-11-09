# Events

Hostname claims for self-hosted events.

## GET /v1/events

<div data-operation="GET /v1/events"></div>

Returns every event hostname claimed by the calling account, with the server address it points at and when the claim expires.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Claimed event hostnames |

## POST /v1/events

<div data-operation="POST /v1/events"></div>

Creates &lt;name&gt;.&lt;domain&gt; and sites.&lt;name&gt;.&lt;domain&gt; as A records at the given public IPv4 address, so a hackathon organiser never touches a registrar. The two hostnames are separate on purpose: participant content must not share a browser origin with the admin interface. Re-claiming a name you already own replaces its records and extends the expiry. A name owned by another account answers 409. Claims expire and are swept, because a record left pointing at a released cloud address is a subdomain takeover.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| domain | string |  | One of the instance's configured event domains. Defaults to the first. |
| ip | string | yes | Public IPv4 of the organiser's server. Private and loopback addresses are refused. |
| move | boolean |  | Required to re-point an existing claim at a different address. Without it a re-claim that changes the address is refused, because it would silently move a running event. |
| name | string | yes | 1 to 40 letters, digits or hyphens. Lowercased. Reserved names are refused. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Hostnames created |
| 400 | Invalid name |
| 409 | That event name belongs to another account |
| 502 | The DNS provider refused the record |

## DELETE /v1/events/{name}

<div data-operation="DELETE /v1/events/{name}"></div>

Removes both DNS records and the claim. Safe to retry. Records are deleted before the row, so a partial failure leaves something that still knows what to clean up.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| name | path | string | yes |  |
| domain | query | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Released |
| 404 | Not found |
| 502 | The DNS provider refused the deletion |
