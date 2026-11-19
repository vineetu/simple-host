# Teams

Accounts that own sites, which people join by accepting an invitation. Members act on a team's sites with their own key and owner=&lt;team&gt;.

## POST /v1/me/team-invitations/{invitation}

<div data-operation="POST /v1/me/team-invitations/{invitation}"></div>

For the person the invitation is addressed to (their handle, or their verified sign-in email). Accepting makes them a member, checked at that moment: the team still exists, the member who invited them still belongs to it, and they are in fewer than 10 teams.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| invitation | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| answer | string, one of `accept`, `decline` | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Answered |
| 400 | invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | invitation_not_found (answered, cancelled, expired, not yours, or its sender left the team) |
| 409 | team_limit (you are in 10 teams) |

## GET /v1/teams

<div data-operation="GET /v1/teams"></div>

The teams you belong to, and the invitations to join a team that wait for your answer (POST /v1/me/team-invitations/{invitation}).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Your teams and invitations |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## POST /v1/teams

<div data-operation="POST /v1/teams"></div>

A team is an account that owns sites, which people join; you become its first member. Its name is its handle and address (https://&lt;site&gt;.&lt;name&gt;.simple-host.app/): 1 to 39 lowercase letters, numbers or hyphens, no dots, and it cannot be renamed. One role: every member may do everything to the team's sites (owner=&lt;team&gt; on /v1/sites routes, with their own key), invite people and remove members, and delete the team once it owns no sites. A person belongs to at most 10 teams. Needs a person's own full key or connection.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| name | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | The new team |
| 400 | invalid_name (dots, or not an address label) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | forbidden (the admin key, a team) or deploy_only_key |
| 409 | name_taken (a person's or team's handle), name_conflict (a reserved or claimed address) or team_limit (10 teams) |

## DELETE /v1/teams/{team}

<div data-operation="DELETE /v1/teams/{team}"></div>

Any member may delete a team that owns no sites (sites in Recently deleted count). Its name is retired, never given out again.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| team | path | string | yes | The team's handle |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Deleted |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found: no team of that name that you belong to |
| 409 | team_has_sites |

## DELETE /v1/teams/{team}/invitations/{invitation}

<div data-operation="DELETE /v1/teams/{team}/invitations/{invitation}"></div>

Any member may cancel one of the team's invitations that waits for an answer.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| team | path | string | yes | The team's handle |
| invitation | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Cancelled |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found or invitation_not_found |

## GET /v1/teams/{team}/members

<div data-operation="GET /v1/teams/{team}/members"></div>

For members only. Sign-in emails are never shown; invitations show the email they were sent to.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| team | path | string | yes | The team's handle |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found |

## POST /v1/teams/{team}/members

<div data-operation="POST /v1/teams/{team}/members"></div>

Any member may invite a person, by email. This makes nobody a member: it records an invitation, which only the account whose verified sign-in email it is sees when signed in (GET /v1/me team_invitations, GET /v1/teams) and accepts or declines; an account whose username is no email matches none. Nothing is sent to them. No account is looked up: the answer is the same, 202 with the same body, whether or not anyone has that email. Invitations lapse after 14 days, or when the member who sent them leaves; a team may have 20 waiting.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| team | path | string | yes | The team's handle |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email | string | yes | The person's sign-in email |

**Responses:**

| Status | Description |
| --- | --- |
| 202 | Recorded. Always the same body: {"invited": true, "expires_in_days": 14, "note": ...} |
| 400 | invalid_email (not an email address) |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found |
| 409 | invitation_limit (20 waiting) |

## DELETE /v1/teams/{team}/members/{member}

<div data-operation="DELETE /v1/teams/{team}/members/{member}"></div>

Any member may remove any member, themselves included. Access stops on the removed person's next request, and invitations they sent are withdrawn. The last member cannot leave (last_member); to end the team, delete its sites, then the team.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| team | path | string | yes | The team's handle |
| member | path | string | yes | The member's handle or account id |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Removed |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found or member_not_found |
| 409 | last_member |
