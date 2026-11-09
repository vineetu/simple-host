# Hosted events

Hosted events, teams, entries, judging, results and event websites.

## GET /v1/hack/archive

<div data-operation="GET /v1/hack/archive"></div>

The signed token is the whole authorization. It is bound to one event, current organiser or approved participant membership and, for a team archive, the same current team. No API key is sent. The archive streams in full.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| token | query | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Full tar.gz archive |
| 404 | archive_link_invalid |

## GET /v1/hack/demo

<div data-operation="GET /v1/hack/demo"></div>

Hosted mode only, no authentication. HACK_DEMO_EVENT_SLUG defaults to demo. Available only for an open or building event that has not been taken down. Answers with Cache-Control no-store; self-hosted mode answers 404.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Current demo event and invitation links. |
| 404 | No available demo event. |

## GET /v1/hack/directory

<div data-operation="GET /v1/hack/directory"></div>

Public in hosted Hack mode. Lists events by default after Draft; organiser opt-outs and taken-down events are excluded. Each row adds applications_open and, only when true, apply_url.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Groups now, upcoming and past, each with event title, stage, dates, public URL, applications_open and (when open) apply_url. |

## GET /v1/hack/events

<div data-operation="GET /v1/hack/events"></div>

Items: slug, title, stage, role, can, url, manage_url, starts_at, ends_at, ended_at, time_zone, taken_down.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Events the caller is a member of |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## POST /v1/hack/events

<div data-operation="POST /v1/hack/events"></div>

Create an event (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| contact_email | string | yes |  |
| ends_at | string |  |  |
| expected_participants | integer | yes |  |
| organisation | string |  |  |
| organiser_name | string | yes |  |
| purpose | string | yes |  |
| slug | string | yes |  |
| starts_at | string | yes |  |
| time_zone | string |  |  |
| title | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Created (organiser view of the event) |
| 400 | invalid_name, name_reserved, or a field validation error |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 409 | name_taken or too_many_active_events |
| 429 | too_many_events_today |
| 503 | name_check_unavailable |
| 507 | instance_full |

## GET /v1/hack/events/{slug}

<div data-operation="GET /v1/hack/events/{slug}"></div>

The event as the caller's role sees it. event includes submission_deadline, entry_required, gallery_open, team_sites_ready and stages_offered. A participant on a team gets me.team (site, deadline, extended, frozen, pinned_version) and me.team_key (null, or last4, created_at and last_used_at; absent for other roles). admin_view is true when the platform admin reads an event it is not in (read-only).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Event view for the caller's role |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PATCH /v1/hack/events/{slug}

<div data-operation="PATCH /v1/hack/events/{slug}"></div>

Edit page text and settings (organiser) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| about | string |  |  |
| accent_color | string, one of ``, `red`, `teal`, `indigo`, `orange`, `yellow`, `grass`, `blush` |  | Empty selects the stable slug crayon; otherwise choose a palette name. |
| applications_open | boolean |  | Whether people without the join link can apply. On by default; off stops new applications and keeps waiting ones. |
| coc_text | string |  |  |
| contact_email | string |  |  |
| ends_at | string |  |  |
| entry_required | array of string, one of `title`, `tagline`, `description`, `video_url`, `code_url`, `screenshot` |  | Deprecated: use PUT .../entry-rules instead, which replaced this for the fuller rules model (presets, optional/off/one_of, judge_first). Sending this still works: it sets required for exactly the listed fields (everything else optional) and demotes the stored preset to custom. Anything else is 400 invalid_entry_field. |
| expected_participants | integer |  |  |
| gallery_open | boolean |  | Opens the project gallery on the event page. |
| judging_ends_at | string |  | When judging closes, same parsing as submission_deadline. An empty string clears it. Judges with unscored teams are reminded before this time, from the event's reminder offsets (GET/PUT .../reminders); with no judging_ends_at they get no reminder. Not inferred from the schedule. A value that is not a time is 400 invalid_judging_ends_at. |
| organisation | string |  |  |
| organiser_name | string |  |  |
| prizes | string |  |  |
| purpose | string |  |  |
| rules | string |  |  |
| starts_at | string |  |  |
| submission_deadline | string |  | RFC 3339, or YYYY-MM-DDTHH:MM or YYYY-MM-DD in the event's time zone. An empty string clears it. A future deadline while the stage is closed is 409 submissions_closed_stage (move the event back to Building, or give single teams more time). Changing the deadline after the event has ended is 409 event_closed. A value that is not a time is 400 invalid_submission_deadline. |
| tagline | string |  |  |
| team_finding | boolean |  |  |
| team_size_max | integer |  |  |
| theme | string, one of ``, `poster`, `confetti`, `boardroom`, `garden`, `terminal` |  | Whole-page style for the public event page; empty is the classic look. accent_color still drives buttons and links under every style. Invalid value is 400 invalid_theme. |
| time_zone | string |  |  |
| title | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated organiser view |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | submissions_closed_stage (a future deadline while the stage is closed) or event_closed (a deadline change after the event ended) |

## DELETE /v1/hack/events/{slug}

<div data-operation="DELETE /v1/hack/events/{slug}"></div>

Removes the public page, results, team sites, entries, scores, votes, custom website, storage and member access. No undo or deletion email. The name stays reserved after any participant/judge joins or a team exists. Empty, unended events still accept no body.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm | string |  | Exact event address name (slug); required for busy or ended events |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Deleted permanently; the name stays reserved unless nobody ever joined |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | delete_only_empty: send a confirm value matching the event slug to delete a busy or ended event; an explicitly mismatching confirm is always refused |

## GET /v1/hack/events/{slug}/announcements

<div data-operation="GET /v1/hack/events/{slug}/announcements"></div>

Read recent event announcements as a member (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Up to 50 announcements, newest first |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found for a non-member |

## POST /v1/hack/events/{slug}/announcements

<div data-operation="POST /v1/hack/events/{slug}/announcements"></div>

Post an event announcement, optionally email participants (organiser; until archive)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | string | yes |  |
| email_participants | boolean |  |  |
| title | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Posted announcement and number of participant emails queued |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found for a non-organiser |
| 409 | event_ended |
| 503 | email_unavailable when email is requested without a sender |

## PUT /v1/hack/events/{slug}/announcements/{announcement}

<div data-operation="PUT /v1/hack/events/{slug}/announcements/{announcement}"></div>

Editing does not send a new email.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| announcement | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | string | yes |  |
| title | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Announcement changed |
| 404 | Announcement not found |
| 409 | Event ended |

## DELETE /v1/hack/events/{slug}/announcements/{announcement}

<div data-operation="DELETE /v1/hack/events/{slug}/announcements/{announcement}"></div>

Delete announcement

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| announcement | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Announcement changed |
| 404 | Announcement not found |
| 409 | Event ended |

## GET /v1/hack/events/{slug}/applications

<div data-operation="GET /v1/hack/events/{slug}/applications"></div>

List participant applications, status and submitted answers (organiser)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Event-scoped array of user_id, email, display_name, status, answers, joined_at, joined_via, city, country, age_range and gender |
| 404 | Not found |

## POST /v1/hack/events/{slug}/applications/{user_id}/decision

<div data-operation="POST /v1/hack/events/{slug}/applications/{user_id}/decision"></div>

Only pending applications can be decided. Approval permits team, entry and team-key actions; rejection leaves the application visible but inaccessible to the participant's team routes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| decision | string, one of `approved`, `rejected` | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {user_id, status} |
| 404 | Not found |
| 409 | application_not_pending or event_closed |

## GET /v1/hack/events/{slug}/apply

<div data-operation="GET /v1/hack/events/{slug}/apply"></div>

Includes account_name (string), the signed-in account name or an empty string when none is known. Signed out allowed; a valid X-API-Key meters the account instead of the shared network address. 404 event_not_found for a draft, taken-down or unknown event (identical body, so a draft's existence does not leak). fields (city, country, age_range, gender) are fixed product choices, always present. signup_questions, coc_default and coc_text are present only when signed in AND applications are open right now (not when the organiser's switch is off or the stage has moved past it) -- these used to need the join code, and a closed or switched-off event must not become a way to read them with only an account. me is present only when signed in and already a member (null otherwise), with role, joined_via and, for a participant, approval_status; it is returned either way, open or closed. closed_reason is null, applications_off, not_listed (the event is not in Find a hackathon, which takes no applications) or signup_over.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Apply page fields: slug, title, tagline, icon_url, color, organiser_name, organisation, stage, starts_at, ends_at, time_zone, event_url, applications_open, closed_reason, fields |
| 404 | event_not_found |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/hack/events/{slug}/apply

<div data-operation="POST /v1/hack/events/{slug}/apply"></div>

D1: every application is pending until an organiser decides it, whatever approval_required says (that toggle governs join-link sign-ups only). Rate-limited per account (RATE_LIMIT_EVENT_APPLY_USER) and per network address (RATE_LIMIT_EVENT_APPLY_IP). A cap of 20 pending applications per account returns 409 too_many_pending. A separate per-event ceiling (EVENT_APPLY_PENDING_PER_EVENT, default 500) returns 409 event_applications_full when that one event already has that many applications waiting for a decision. Reapplying as an existing participant -- pending, approved or rejected -- is idempotent (200, nothing changes, including after rejection, and whether or not applications are open right now); a different role is 409 already_member.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes |  |
| age_range | string, one of ``, `under_18`, `18_24`, `25_34`, `35_plus` |  | Optional. |
| answers | object |  |  |
| city | string |  | Optional, up to 80 characters. |
| country | string |  | Optional. ISO 3166-1 alpha-2 country code, e.g. ID, GB, US. |
| display_name | string |  | Only used when your account has no name yet; it then becomes your account name. Otherwise ignored. |
| gender | string, one of ``, `woman`, `man`, `non_binary`, `another`, `prefer_not_to_say` |  | Optional. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Applied: same shape as POST /v1/hack/join/{code}, me.approval_status pending |
| 400 | coc_required, invalid_display_name, invalid_city, invalid_country, invalid_age_range, invalid_gender, answer_required or invalid_answers |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | joining_closed, applications_closed, already_member, too_many_pending or event_applications_full |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/hack/events/{slug}/assignments

<div data-operation="GET /v1/hack/events/{slug}/assignments"></div>

Event judge-team assignments (organiser)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Assignments; {assignments:[{judge_id,judge_name,team_id,team_name}]} |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/assignments

<div data-operation="PUT /v1/hack/events/{slug}/assignments"></div>

Requires assignment_mode manual. All judge and team ids must belong to this event; organisers are eligible judges too. Conflicted pairs are refused. Locked or archived events refuse changes. Unlisted assignments are removed.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| assignments | array of object |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The event's assignments with judge and team names |
| 404 | event_not_found |
| 409 | invalid_assignment_mode |

## POST /v1/hack/events/{slug}/assignments/generate

<div data-operation="POST /v1/hack/events/{slug}/assignments/generate"></div>

Only when assignment_mode is "automatic" (409 invalid_request otherwise: switch to automatic first). Spreads judges across every team with members, up to judges_per_team each, skipping any declared conflict; deterministic given the same input. Include organisers only with include_organisers true. Replaces any existing assignment. teams_under_target lists teams that got fewer than judges_per_team because not enough non-conflicted judges were available. 400 too_few_judges when there are no eligible judges or organisers, 400 too_few_teams when no team has a member yet.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_organisers | boolean |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The new assignment |
| 400 | too_few_judges or too_few_teams |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | invalid_request (still in open mode) |

## GET /v1/hack/events/{slug}/assistant

<div data-operation="GET /v1/hack/events/{slug}/assistant"></div>

Read whether the organiser assistant is on and today's remaining questions

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Assistant status for the calling organiser |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | organisers_only |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/assistant

<div data-operation="POST /v1/hack/events/{slug}/assistant"></div>

Read-only. The server answers from the navigation map and its explanation of every organiser setting, option and action, the event's current settings, the organiser's event view and checklist, and the docs, through the configured endpoint. It never changes the event. Each accepted request counts as one question.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| messages | array of object | yes | The chat so far; the last message is the user's question. Each content is at most 2000 characters, 12000 in total. |
| screen | string |  | Optional. The organiser screen the question is asked from, as a navigation screen id (for example organiser-settings). Unknown values, and values over 64 characters, are ignored. |
| tab | string |  | Optional. The tab of that screen (for example judging); over 64 characters is ignored. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Server-sent events: {"delta":"..."} chunks, then {"done":true,"remaining_today":N} or {"error":"provider_error","message":"..."} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | organisers_only |
| 404 | event_not_found or assistant_off |
| 429 | daily_limit or monthly_budget |
| 502 | provider_error |

## GET /v1/hack/events/{slug}/checklist

<div data-operation="GET /v1/hack/events/{slug}/checklist"></div>

Read a role checklist and its next action

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| role | query | string, one of `organiser`, `judge`, `participant` |  | Defaults to actual membership. Organisers may also request judge; synthetic admin inspection is read-only organiser. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Fresh role checklist |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | checklist_role_forbidden or event_taken_down |
| 404 | event_not_found |
| 409 | event_closed or stale_checklist_version; refetch after a stale rubric version |

## POST /v1/hack/events/{slug}/checklist/acknowledgements/{item}

<div data-operation="POST /v1/hack/events/{slug}/checklist/acknowledgements/{item}"></div>

Explicitly acknowledge the current rubric or own conflict review

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| item | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| version | string |  | Current item version for o.rubric-ready or j.rubric. Omit for unversioned j.conflicts. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Fresh role checklist |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | checklist_role_forbidden or event_taken_down |
| 404 | event_not_found |
| 409 | event_closed or stale_checklist_version; refetch after a stale rubric version |

## POST /v1/hack/events/{slug}/checklist/not-needed/{item}

<div data-operation="POST /v1/hack/events/{slug}/checklist/not-needed/{item}"></div>

Actual organiser only; optional organiser catalogue IDs only. Event-wide and idempotent; factual completion wins over a mark. Ended events refuse writes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| item | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Fresh role checklist |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | checklist_role_forbidden or event_taken_down |
| 404 | event_not_found |
| 409 | event_closed or stale_checklist_version; refetch after a stale rubric version |

## DELETE /v1/hack/events/{slug}/checklist/not-needed/{item}

<div data-operation="DELETE /v1/hack/events/{slug}/checklist/not-needed/{item}"></div>

Actual organiser only; optional organiser catalogue IDs only. Event-wide and idempotent; factual completion wins over a mark. Ended events refuse writes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| item | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Fresh role checklist |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | checklist_role_forbidden or event_taken_down |
| 404 | event_not_found |
| 409 | event_closed or stale_checklist_version; refetch after a stale rubric version |

## POST /v1/hack/events/{slug}/codes/{kind}

<div data-operation="POST /v1/hack/events/{slug}/codes/{kind}"></div>

Regenerate the join or judge code (organiser) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| kind | path | string, one of `join`, `judge` | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The new code and URL |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/conflicts

<div data-operation="GET /v1/hack/events/{slug}/conflicts"></div>

The organiser sees every declared conflict by default. With mine=1, organisers and judges see only their own. Judges always see only their own.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| mine | query | integer, one of `1` |  | Restrict to the caller, including an organiser in the judge area. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Declared conflicts |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/conflicts

<div data-operation="POST /v1/hack/events/{slug}/conflicts"></div>

A judge or organiser declares their own conflict with {"team_id"}; an organiser may instead set judge_user_id to record one on a judge's behalf (403 forbidden if a judge tries to set it to anyone but themself). Idempotent: declaring twice returns the same row rather than erroring. 404 team_not_found or judge_not_found when either is not in this event.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| judge_user_id | string |  |  |
| team_id | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The conflict |
| 400 | invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or forbidden |
| 404 | event_not_found, team_not_found or judge_not_found |

## DELETE /v1/hack/events/{slug}/conflicts/{team_id}

<div data-operation="DELETE /v1/hack/events/{slug}/conflicts/{team_id}"></div>

A judge or organiser removes their own when judge_user_id is omitted; an organiser may pass ?judge_user_id= to remove a specific judge's (400 invalid_request if a judge tries to use it for anyone but themself). 404 conflict_not_found if no such conflict exists.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team_id | path | string | yes |  |
| judge_user_id | query | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Removed |
| 400 | invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or conflict_not_found |

## GET /v1/hack/events/{slug}/content

<div data-operation="GET /v1/hack/events/{slug}/content"></div>

Read sponsors, FAQ, schedule and the fixed schedule rows as an event member (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Event content; schedule times are RFC 3339 UTC |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found for a non-member |

## PUT /v1/hack/events/{slug}/content

<div data-operation="PUT /v1/hack/events/{slug}/content"></div>

Replace sponsors, FAQ and schedule (organiser; until archive)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| faq | array of object |  |  |
| fixed_schedule | array of object |  | The event's real times as read-only schedule rows, computed from its settings on every read and never stored (a schedule that acts, 2026-11-02). Ignored on PUT, and a schedule row sent with fixed true is dropped. A participant whose team was given more time sees team_deadline in place of entry_deadline. |
| schedule | array of object |  |  |
| sponsors | array of object |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved content |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found for a non-organiser |
| 409 | event_ended |

## PATCH /v1/hack/events/{slug}/directory

<div data-operation="PATCH /v1/hack/events/{slug}/directory"></div>

Organiser only; works after the event ends. New events are unlisted until opted in. Drafts and taken-down events are never listed regardless of this setting.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| listed | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved listing preference |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/entries

<div data-operation="GET /v1/hack/events/{slug}/entries"></div>

Every team, including a team that has not written an entry (empty fields), sorted by team name. Due teams are pinned before the list is read. pinned_url is a preview link for the deadline version, or null when the team is not pinned or had no version. counts tallies submitted, drafts, not_started, not_submitted (incomplete, still judged) and sites_live. A participant gets 404 event_not_found. The platform admin may read. For a judge (not an organiser), a team's site preview_url and pinned_url are null unless that judge may score the team (its entry submitted or settled, not excluded, assigned to them or on their panel, no declared conflict) and entries are closed. For everyone, preview_url is null whenever the site can't be framed (passcode, named viewers, offline, taken down).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Every team's entry, site and deadline |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/entry

<div data-operation="GET /v1/hack/events/{slug}/entry"></div>

The caller's team's entry. Text fields are empty strings until the team writes them. 409 no_team when the caller is on no team. refusal and editable say whether it can still be changed; this read itself is allowed.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry, what a complete entry needs, and whether it can change |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | no_team |

## PUT /v1/hack/events/{slug}/entry

<div data-operation="PUT /v1/hack/events/{slug}/entry"></div>

Updates any of the fields supplied and returns the entry. title is one line, 0-80 characters; tagline one line, 0-160 (the one-line solution); description 0-5000; problem 0-140; audience 0-100. video_url and code_url are empty or an absolute http or https URL with a host, at most 500 characters, with no spaces or control characters (400 invalid_url). tools is at most 10 items, each 1-30 characters (400 invalid_tools); duplicates, compared case-insensitively, are dropped. maturity is working, partial, concept or empty (400 invalid_maturity). track sets the team's chosen track by slug, or clears it with an empty string (400 invalid_track for an unlisted slug; 409 team_choice_closed once team choices are closed). Saving a draft never checks or changes completeness; use POST .../entry/submit to submit. A save on an already-submitted entry that would make it incomplete is refused with 422 entry_would_be_incomplete (includes missing), changing nothing. Refused inside the write when the team may no longer change its entry.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| audience | string |  | Who it's for |
| code_url | string |  | Same rules as video_url. |
| description | string |  | 0-5000 characters. |
| maturity | string, one of ``, `working`, `partial`, `concept` |  | How far along it is, or empty to clear. Anything else is 400 invalid_maturity. |
| problem | string |  | The problem it solves |
| tagline | string |  | One line, 0-160 characters: the one-line solution. |
| title | string |  | One line |
| tools | array of string |  | What was used. At most 10 items, each 1-30 characters (400 invalid_tools). Duplicates, compared case-insensitively, are dropped, keeping the first spelling. |
| track | string |  | Track slug to set the team's chosen track, or empty to clear it. An unlisted slug is 400 invalid_track. Refused with 409 team_choice_closed once team choices are closed. |
| updated_at | string |  | Timestamp from the entry read, or empty for a new entry. Stale saves return 409 entry_changed with the current entry. |
| video_url | string |  | Empty, or an absolute http or https URL with a host, no spaces or control characters. Otherwise 400 invalid_url. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry after the update |
| 400 | invalid_url, invalid_title, invalid_tagline, invalid_description, invalid_tools, invalid_maturity, invalid_track or invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_site_taken_down |
| 404 | event_not_found or track_not_found |
| 409 | entry_changed (includes current entry), no_team, event_closed, submissions_closed or team_choice_closed |
| 422 | entry_would_be_incomplete (includes missing); the save is rejected and nothing changes |

## GET /v1/hack/events/{slug}/entry-rules

<div data-operation="GET /v1/hack/events/{slug}/entry-rules"></div>

Which entry fields are required, optional, off or one_of, the active preset, which evidence judges see first, and the one-sentence summary participants see. editable is false from Closed onward.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The event's entry rules |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/entry-rules

<div data-operation="PUT /v1/hack/events/{slug}/entry-rules"></div>

Choose a preset (video_or_website, the default -- at least one of a video or a website; or video_website_code -- video, website and code link all required) and/or set individual fields; judge_first picks which evidence judges see first. Setting a field that differs from the active preset's own table demotes the stored preset to custom. An event already on an older preset (build, code, ideas or custom) keeps it and its saved values until a current preset is set or a field is changed; sending build, code, ideas or custom here is refused (400 invalid_preset) naming the two current ones. dry_run:true returns impact and preview without saving anything (HackEntryRulesImpact instead of HackEntryRulesView). Tightening never un-submits an already-submitted entry. Refused with 409 entry_rules_closed once entries have closed (Closed stage onward): rules are fixed so judging sees one definition of complete.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| dry_run | boolean |  | true previews impact and the resulting rules in the response, without saving anything. |
| fields | object |  | Field name to required, optional, off or one_of (one_of only on video_url, site, code_url, screenshot; otherwise 400 invalid_entry_field_mode). Setting any field that differs from the active preset's own table demotes the stored preset to custom. |
| judge_first | string, one of `video`, `site` |  | Which evidence judges see first. Anything else is 400 invalid_judge_first. |
| preset | string, one of `video_or_website`, `video_website_code` |  | Expands to that preset's own field table first; any fields below are then applied on top. video_or_website (the default): video and site one_of, code off. video_website_code: video, site and code all required. An older preset (build, code, ideas, custom) is refused with 400 invalid_preset naming these two -- an event already on one keeps it until a current preset is set here. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry rules after saving, or (dry_run) the impact and preview without saving |
| 400 | invalid_preset, invalid_entry_field, invalid_entry_field_mode or invalid_judge_first |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | entry_rules_closed |

## GET /v1/hack/events/{slug}/entry/screenshot

<div data-operation="GET /v1/hack/events/{slug}/entry/screenshot"></div>

The caller's team's screenshot. 409 no_team when they are on no team. Served with X-Content-Type-Options nosniff, Content-Security-Policy default-src 'none'; sandbox, Cache-Control private, no-store and Content-Disposition inline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The screenshot. Content-Type is the stored type (image/png, image/jpeg or image/webp). |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_screenshot or event_not_found |
| 409 | no_team |

## PUT /v1/hack/events/{slug}/entry/screenshot

<div data-operation="PUT /v1/hack/events/{slug}/entry/screenshot"></div>

Stores the team's screenshot and returns the entry. The body is the raw image, at most 2 MB, image/png, image/jpeg or image/webp. The type is taken from the file's magic bytes, not the header; SVG and anything else is 415 unsupported_image. Over 2 MB is 413 image_too_large. The same refusals as updating the entry.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:** image/jpeg

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry after the screenshot is stored |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_site_taken_down |
| 404 | event_not_found |
| 409 | no_team, event_closed or submissions_closed |
| 413 | image_too_large (over 2 MB) |
| 415 | unsupported_image (not a PNG, JPEG or WebP by its bytes; never SVG) |

## DELETE /v1/hack/events/{slug}/entry/screenshot

<div data-operation="DELETE /v1/hack/events/{slug}/entry/screenshot"></div>

Removes the team's screenshot and returns the entry. The same refusals as updating the entry.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry after the screenshot is removed |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_site_taken_down |
| 404 | event_not_found |
| 409 | no_team, event_closed or submissions_closed |

## POST /v1/hack/events/{slug}/entry/submit

<div data-operation="POST /v1/hack/events/{slug}/entry/submit"></div>

Moves the entry from draft to submitted. Refused with 422 entry_incomplete (includes missing and error, "Add these first: ...") when a required field, or the show-your-work evidence, is missing. Idempotent: submitting an already-submitted entry answers 200 and keeps its first submitted_at. A complete draft is also auto-submitted by the deadline sweep on its own (submit_auto true); an incomplete one stays a draft, is still judged, flagged Incomplete (status not_submitted), and the organiser may hand-exclude it.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| updated_at | string |  | Timestamp from the entry read, optional. A conflict returns 409 entry_changed with the current entry. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry after submitting |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_site_taken_down |
| 404 | event_not_found |
| 409 | entry_changed (includes current entry), no_team, event_closed or submissions_closed |
| 422 | entry_incomplete (includes missing and error) |

## POST /v1/hack/events/{slug}/entry/unsubmit

<div data-operation="POST /v1/hack/events/{slug}/entry/unsubmit"></div>

Puts a submitted entry back to draft. Any current team member may call it. Works only before the deadline; refused with 409 submissions_closed once it has passed. Reversible: submit again anytime before the deadline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The entry back in draft |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_site_taken_down |
| 404 | event_not_found |
| 409 | no_team, event_closed or submissions_closed |

## GET /v1/hack/events/{slug}/export/entries.csv

<div data-operation="GET /v1/hack/events/{slug}/export/entries.csv"></div>

Organiser only, event scoped. Columns: team, team_slug, title, tagline, description, video_url, code_url, has_screenshot, updated_at, status, submitted_at, submit_auto, problem, audience, track, tools, maturity, missing, excluded. New columns are appended at the end so existing spreadsheet imports keep working. Text cells are formula-safe. Participant export excludes organisers and judges.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download entries CSV |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## POST /v1/hack/events/{slug}/export/own-team-link

<div data-operation="POST /v1/hack/events/{slug}/export/own-team-link"></div>

Approved participant member with a team only. The link downloads only that team's complete archive without an API key. It expires after EXPORT_LINK_TTL_MINUTES (default 10, 1–60), and stops working when approval, membership or team changes or the server restarts. Treat it as private.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download URL and expiry |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/export/participants.csv

<div data-operation="GET /v1/hack/events/{slug}/export/participants.csv"></div>

Organiser only, event scoped. Columns: name, email, team, team_slug, joined_at, coc_accepted_at, city, country, age_range, gender, joined_via. Text cells are formula-safe. Participant export excludes organisers and judges, and pending applicants.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download participants CSV |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## POST /v1/hack/events/{slug}/export/projects-link

<div data-operation="POST /v1/hack/events/{slug}/export/projects-link"></div>

Organiser member only. The link downloads the complete archive without an API key. It expires after EXPORT_LINK_TTL_MINUTES (default 10, 1–60), and stops working if the organiser is removed or the server restarts. Treat it as private.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download URL and expiry |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/export/projects.tar.gz

<div data-operation="GET /v1/hack/events/{slug}/export/projects.tar.gz"></div>

Organiser only. Current published files and saved data, including private collections, for this event's teams only; one team-slug directory per project. Does not include deleted sites or other events.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download all event projects |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## GET /v1/hack/events/{slug}/export/results.csv

<div data-operation="GET /v1/hack/events/{slug}/export/results.csv"></div>

Columns team, total, rank, tied, judges_scored. Computed live (not from a stored snapshot), so this works even before results are officially published.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The CSV file |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/export/scores.csv

<div data-operation="GET /v1/hack/events/{slug}/export/scores.csv"></div>

One row per (team, judge, criterion) with a score. Columns team, judge, criterion, points, max_points, comment. Text cells go through the same formula-injection guard as other CSV exports in this codebase.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The CSV file |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/export/teams.csv

<div data-operation="GET /v1/hack/events/{slug}/export/teams.csv"></div>

Organiser only, event scoped. Columns: name, slug, members, site_url, created_at. Text cells are formula-safe.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download teams CSV |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## GET /v1/hack/events/{slug}/icon

<div data-operation="GET /v1/hack/events/{slug}/icon"></div>

Public event icon, including a generated initial when none was uploaded

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | PNG |
| 404 | Event not found or taken down |

## PUT /v1/hack/events/{slug}/icon

<div data-operation="PUT /v1/hack/events/{slug}/icon"></div>

Raw PNG, JPEG or WebP bytes with matching Content-Type, up to HACK_EVENT_ICON_MAX_BYTES (default 128 KiB).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:** image/jpeg

**Responses:**

| Status | Description |
| --- | --- |
| 200 | icon_url, content_type and bytes |
| 400 | invalid_icon |
| 404 | event_not_found for non-organisers |

## DELETE /v1/hack/events/{slug}/icon

<div data-operation="DELETE /v1/hack/events/{slug}/icon"></div>

Return an event to its generated initial icon

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Default initial icon restored |
| 404 | event_not_found for non-organisers |

## GET /v1/hack/events/{slug}/judge/queue

<div data-operation="GET /v1/hack/events/{slug}/judge/queue"></div>

Teams this judge may score (minus any declared conflict), each with its entry card data (same fields as HackEntryListItem, plus team_id), done (every current rubric criterion scored by this judge) and scores_count (how many judges total have scored it so far). Ordered least-covered first, then by team name. Organisers use the same queue without changing their membership role. Platform admin read-only access does not grant judging access. A judge (not an organiser) gets each team's site preview_url and pinned_url only once entries are closed; before that they are null. assignment_mode is the event's judge assignment mode. candidates lists mode-eligible, judgeable teams before subtracting this caller's own conflicts, for the conflict review panel.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The queue |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/judge/scores/{team}

<div data-operation="GET /v1/hack/events/{slug}/judge/scores/{team}"></div>

This judge's current scores for the team (0 for a criterion with no score row yet) and the stored comment. 403 forbidden if conflicted.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes | The team_id UUID returned by the judge queue (not the team slug). |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Scores and comment |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | forbidden |
| 404 | event_not_found or team_not_found |

## PUT /v1/hack/events/{slug}/judge/scores/{team}

<div data-operation="PUT /v1/hack/events/{slug}/judge/scores/{team}"></div>

Scoring is allowed only in closed or judging stages; otherwise 409 scoring_not_open. Upserts whichever criteria are given (a partial list is fine; a retried write of the same value never double-counts). comment, if present (even empty string), is written to every score row this call touches; omit it to leave the stored comment unchanged. A comment with an empty scores array and no existing score row yet is 400 invalid_request ("Score at least one criterion before adding a comment."). 409 scores_locked once judging is locked. 403 forbidden if this judge has a conflict with the team. Returns the full updated score set plus complete (every current rubric criterion has a score).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes | The team_id UUID returned by the judge queue (not the team slug). |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| comment | string |  |  |
| scores | array of object |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The updated scores |
| 400 | invalid_points or invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or forbidden |
| 404 | event_not_found, team_not_found or criterion_not_found |
| 409 | scores_locked or scoring_not_open |

## GET /v1/hack/events/{slug}/judging/dashboard

<div data-operation="GET /v1/hack/events/{slug}/judging/dashboard"></div>

Per-team score coverage (flagged when short of the target — judges_per_team in automatic mode, 2 in open mode) and per-judge progress (done_count, assigned_count). tied_groups is always empty until scores exist (a later release fills this in once totals can be computed). former_judges (SIM-340) lists people who left after scoring: counted_teams is how many of their scores still count (frozen at the moment they left), assigned_count is how many of today's teams they would be assigned under the current mode. They are never in judges. teams lists only the teams that will be judged: an entry not excluded by hand, or a live team site (moving to judging starts an entry for it); a team with neither has no entry and is left out, as it is from automatic assignment and every judge's queue. entries counts the same split (judged, no_entry, excluded) for the Start judging confirm.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Coverage and progress |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## DELETE /v1/hack/events/{slug}/judging/former-judges/{user_id}

<div data-operation="DELETE /v1/hack/events/{slug}/judging/former-judges/{user_id}"></div>

Deletes the scores and comments of a judge (or judging organiser) who left the event, and their former-judge record, so they stop counting. Published results change only on the next publish; the snapshot already taken is untouched. Refused while judging is locked. confirm must equal the event's address name (slug), the same as deleting the event itself.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm | string | yes | Exact event address name (slug), supplied only after the person confirms deletion |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Scores and the former-judge record deleted |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | former_judge_not_found or event_not_found (caller no longer an organiser) |
| 409 | event_closed, scores_locked, or confirm_required (missing or not the slug) |

## POST /v1/hack/events/{slug}/judging/lock

<div data-operation="POST /v1/hack/events/{slug}/judging/lock"></div>

Refuses every further rubric or score write (409 scores_locked). Idempotent: locking an already-locked event is still 200.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The lock state |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/judging/panels

<div data-operation="GET /v1/hack/events/{slug}/judging/panels"></div>

Track panel members (organiser)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {panels:[{track_id,judge_id}]} |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/judging/panels

<div data-operation="PUT /v1/hack/events/{slug}/judging/panels"></div>

Track and judge ids must belong to this event; organisers are eligible judges too. Teams without a track have no panel queue. Conflicts still exclude individual team pairs. Locked or archived events refuse changes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| panels | array of object |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {panels:[{track_id,judge_id}]} |
| 404 | event_not_found |
| 409 | scores_locked or event_closed |

## GET /v1/hack/events/{slug}/judging/preview

<div data-operation="GET /v1/hack/events/{slug}/judging/preview"></div>

Preview only; nothing is published. Returns selected, raw and normalised team totals, tie decision, and track winners.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {results:[...],deciding_mode:raw\|normalised} |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/judging/settings

<div data-operation="GET /v1/hack/events/{slug}/judging/settings"></div>

Assignment mode, result score mode, preselected tie criterion and public score visibility.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The event's judging settings |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PATCH /v1/hack/events/{slug}/judging/settings

<div data-operation="PATCH /v1/hack/events/{slug}/judging/settings"></div>

Any subset of assignment_mode (open, automatic, manual, panel), judges_per_team (1-20), score_mode (raw or normalised), tie_criterion_id, public_ranks, and public_scores. The tie criterion must be chosen before judging starts; score mode can change during judging. A publish stores the chosen mode in its snapshot.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| assignment_mode | string, one of `open`, `automatic`, `manual`, `panel` |  |  |
| judges_per_team | integer |  |  |
| public_ranks | boolean |  |  |
| public_scores | boolean |  |  |
| score_mode | string, one of `raw`, `normalised` |  |  |
| tie_criterion_id | string |  | An event rubric criterion id; empty string clears it before judging |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The settings after the change |
| 400 | invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/judging/unlock

<div data-operation="POST /v1/hack/events/{slug}/judging/unlock"></div>

Requires a reason (1-300 characters, one line); the reason is logged.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| reason | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The lock state |
| 400 | invalid_reason |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/key

<div data-operation="GET /v1/hack/events/{slug}/key"></div>

The caller's team key without the secret (null when they have none), their team and that team's site (both null when they are on no team), and the addresses to publish with. Participant. Anyone else gets 404 event_not_found.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Key metadata, team and site |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/key

<div data-operation="POST /v1/hack/events/{slug}/key"></div>

Makes this participant's key for their team's site and shows the plaintext once. Any earlier key of theirs for this event stops working. Publish with it (PUT /v1/sites/&lt;team slug&gt; or PUT /v1/sites/&lt;team slug&gt;/files, or the connector at mcp_url); it touches only that team's site. Requires a team.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | The new key, shown only here |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | no_team (join or start a team first) or event_closed (the event has ended) |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## DELETE /v1/hack/events/{slug}/key

<div data-operation="DELETE /v1/hack/events/{slug}/key"></div>

Stops the caller's team key for this event.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | The key is off |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/leave

<div data-operation="GET /v1/hack/events/{slug}/leave"></div>

Read-only, for any member in any role or approval status; a non-member (including a platform admin reading an event it never joined) gets 404. case is one of participant, applicant, judge, co_organiser, creator or last_organiser, decided from the caller's own role, approval status and the event's other organisers -- a rejected applicant is the same applicant case as a pending one (owner decision 2026-10-31: leaving a rejected application deletes the row exactly like a withdraw, so there is no separate rejected case or refusal any more); approval_status is how the client tells the two apart for copy (withdraw vs. leave). blocked is null or an object with code (event_closed or event_taken_down) and message; allowed is blocked == null. team is present only for a participant on a team, and its frozen is true exactly when leaving it empty would keep it instead of deleting it -- the same predicate POST .../leave's write path uses, not a separate stage/deadline read: a team's own deadline extension can keep it deletable-if-empty even once the event's stage and own deadline have moved past it. Leaving still works past the freeze point either way (owner decision 2026-10-31), but while frozen it only removes the leaver from the team's member list: the team, its entry, its site and its scores stay exactly as they are, so deleted_on_leave is never true while frozen, even for the team's last member. scored_teams is present for judge and organiser cases. organisers_staying is co_organiser only. successors is creator only (every other organiser, ordered by display name). close_options is last_organiser only. rejoin is join, judge_link, organiser_invite or none, about rejoining the event, not this specific team -- a frozen team never takes a new member again, by code or otherwise, whatever rejoin says. confirm is the event's address name, to send back unchanged.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | This person's leave case and what leaving would do |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/leave

<div data-operation="POST /v1/hack/events/{slug}/leave"></div>

One route, self only; the server decides the case under a lock on the event row, every time. confirm must equal the event's address name. The creator with other organisers sends successor_user_id (another organiser of this event, from the preview). The only organiser sends close ("end" or "delete"); end needs at least one participant. A field that does not belong to the caller's current case is refused with 409 leave_state_changed, which means something changed since the preview was read: read it again. Nothing happens on an ended event. A participant past their team's deadline (or once the event is closed, judging or results) still leaves normally -- only the team's member list changes; its entry, site and scores are untouched, even for the team's last member (owner decision 2026-10-31).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| close | string, one of `end`, `delete` |  | The only organiser only |
| confirm | string | yes | Exact event address name (slug) |
| successor_user_id | string |  | Creator only: another organiser of this event, from the preview |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Left (or, for the only organiser, ended or deleted the event too) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | leave_needs_confirm, event_closed, successor_required, invalid_successor, last_organiser, archive_needs_participants or leave_state_changed |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## PUT /v1/hack/events/{slug}/me/looking

<div data-operation="PUT /v1/hack/events/{slug}/me/looking"></div>

Approved participant on no team opts in or out of the solo list. Team finding must be on.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| intro | string | yes |  |
| looking | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Private team finding response. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## GET /v1/hack/events/{slug}/my-results

<div data-operation="GET /v1/hack/events/{slug}/my-results"></div>

published is false before anything is published (not an error). After publish: this team's selected, raw and normalised totals, score mode, track winner and prize, rank and tie flag, plus every distinct judge comment on this team's entry with no indication of which judge wrote which. 409 no_team when the caller is on no team.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | This team's private results |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | no_team |

## GET /v1/hack/events/{slug}/my-vote

<div data-operation="GET /v1/hack/events/{slug}/my-vote"></div>

Signed-in personal account. Matches the same canonical email identity as vote writes. Also returns eligible, this account's current standing against the event's voting eligibility setting (and the admin-account rule PUT vote itself enforces), so the vote page can explain upfront that someone can't vote instead of only after a PUT vote 403 vote_ineligible.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {team, eligible}: team slug or empty string when no vote. |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/notices/seen

<div data-operation="POST /v1/hack/events/{slug}/notices/seen"></div>

Marks the listed notice ids (the id of a needs_you item with dismissable true, at most 50) seen, or every news line in the event when ids is empty or the body is left out. Only the caller's own lines change. Live to-dos cannot be dismissed, only answered.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| ids | array of string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {ok: true, marked: n} |
| 400 | invalid_id or too_many_ids |
| 404 | Event not found or the caller is not in it |

## POST /v1/hack/events/{slug}/organiser-invite

<div data-operation="POST /v1/hack/events/{slug}/organiser-invite"></div>

Organiser only. Returns a single-use URL and expiry, seven days from creation. Only its hash is stored. Replaces any earlier invitation. Cannot change an archived or taken-down event.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | Create a co-organiser invitation |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or invalid_code |
| 409 | event_closed |

## DELETE /v1/hack/events/{slug}/organiser-invite

<div data-operation="DELETE /v1/hack/events/{slug}/organiser-invite"></div>

Organiser only. Existing members keep their role; removes only the outstanding link.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Revoke the outstanding co-organiser invitation |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or invalid_code |
| 409 | event_closed |

## DELETE /v1/hack/events/{slug}/organisers/{user_id}

<div data-operation="DELETE /v1/hack/events/{slug}/organisers/{user_id}"></div>

Organiser only. Cannot remove the primary event creator or the last organiser. Removed organisers immediately lose management access, and invitations they issued no longer work.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Remove a co-organiser |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or invalid_code |
| 409 | primary_organiser or event_closed |

## GET /v1/hack/events/{slug}/people

<div data-operation="GET /v1/hack/events/{slug}/people"></div>

Every member (organiser) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Members. Participants also carry has_key. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## DELETE /v1/hack/events/{slug}/people/{user_id}

<div data-operation="DELETE /v1/hack/events/{slug}/people/{user_id}"></div>

Remove a participant or judge (organiser) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Removed |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | cannot_remove_organiser |

## DELETE /v1/hack/events/{slug}/people/{user_id}/key

<div data-operation="DELETE /v1/hack/events/{slug}/people/{user_id}/key"></div>

The organiser turns off that person's team key. 204 when they had none. A person who is not in the event is 404 event_not_found.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | The key is off |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |

## GET /v1/hack/events/{slug}/public

<div data-operation="GET /v1/hack/events/{slug}/public"></div>

Cross-origin GET is allowed without credentials. Private join codes, contact addresses, unpublished scores, voter identities, open-vote counts and approval_required are omitted. Adds applications_open and, only when true, apply_url. Gallery items carry tools (the entry's tags). Each shown results row carries entry (HackResultCard) when the results were published with cards; rows the visibility setting hides are not emitted, so their cards never appear. fixed_schedule lists the event's real times as read-only schedule rows (HackFixedScheduleRow, without prompts), and schedule rows carry no prompt. Carries theme (the organiser's whole-page Look style, or empty for Classic) alongside the usual colour fields.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Public event fields, applications_open (and apply_url when open), gallery if open, published results and vote counts only after voting closes. |
| 404 | Event not found or taken down |

## GET /v1/hack/events/{slug}/registration

<div data-operation="GET /v1/hack/events/{slug}/registration"></div>

Read sign-up questions and approval setting (organiser)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {questions: [{id, prompt, required}], approval_required, applications_open}: applications_open is read-only here (People -&gt; Sign-up's one-line summary); change it with PATCH .../events/{slug} |
| 404 | Not found |

## PUT /v1/hack/events/{slug}/registration

<div data-operation="PUT /v1/hack/events/{slug}/registration"></div>

Existing applications retain the exact question prompts and answers submitted. Existing members stay approved. Cannot change an archived event. approval_required and admit_pending govern join-link sign-ups only (D1/D5): an application from POST .../apply always stays pending until decided.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| admit_pending | boolean |  | Approve all pending join-link sign-ups in the same transaction when approval_required is false. Applications never move: they always stay pending until an organiser decides them. |
| approval_required | boolean | yes |  |
| questions | array of object | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved settings |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 404 | Not found |
| 409 | event_closed |

## GET /v1/hack/events/{slug}/reminders

<div data-operation="GET /v1/hack/events/{slug}/reminders"></div>

The minutes-before-deadline offsets that drive both the entry-deadline reminder emails to teams and the judging-closes reminders to judges, the allowed range, and when judging closes (judging_ends_at, set through PATCH .../events/{slug}). Organiser only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Reminder settings |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/reminders

<div data-operation="PUT /v1/hack/events/{slug}/reminders"></div>

Validates, dedupes and sorts offsets_minutes descending: 0 to 5 entries, each 15 to 20160 minutes (14 days), distinct. offsets_minutes is required; send [] (not an omitted field) to turn reminders off for both audiences -- omitting it is 400 invalid_offsets. 409 event_closed once the event is archived. Organiser only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| offsets_minutes | array of integer | yes | Minutes before the deadline, each 15 to 20160. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Reminder settings |
| 400 | invalid_offsets |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | event_closed |

## DELETE /v1/hack/events/{slug}/requests/{id}

<div data-operation="DELETE /v1/hack/events/{slug}/requests/{id}"></div>

Withdraw your own request or cancel an invitation from your team. Team finding must be on.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Private team finding response. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## POST /v1/hack/events/{slug}/requests/{id}/{action}

<div data-operation="POST /v1/hack/events/{slug}/requests/{id}/{action}"></div>

action is accept or decline. Any member handles incoming requests; only the invited person handles an invite. Accept joins with the same approval, stage, capacity and one-team rules as a code join. Joining clears all other pending requests and invites and the solo listing in one transaction.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| id | path | string | yes |  |
| action | path | string, one of `accept`, `decline` | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Private team finding response. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## GET /v1/hack/events/{slug}/results

<div data-operation="GET /v1/hack/events/{slug}/results"></div>

No sign-in required. Winners only (the rank-1 team(s)) by default, or every ranked team when the organiser has switched to a full public listing. Shows track winners and prizes even in winners-only mode. Scores are omitted when public_scores is disabled. Public ranks can be hidden separately with public_ranks; the full list is alphabetical when ranks are hidden; judges' comments remain private. Before publication returns 200 with published false. Returns 404 for a taken-down event (its results are not programmatically readable even though its page may still partially render). Sending the organiser's own API key (or the admin key) instead returns the full computed snapshot regardless of the public full_ranking switch — `{"results":[{"team_id","team_name","total","rank","tied","judges_scored"}],"full_ranking"}` — the same shape `results/publish` returns, so the Judging tab can redraw its own view after a page reload; a missing or non-organiser key falls through to the public `{"winners":[...]}` / `{"ranking":[...]}` view. Each shown row carries entry, the team's public summary card (HackResultCard), when the results were published with cards; a team that is not shown never has a card here. site_url is the team site only while it is live now.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Winners or the full ranking; published false before publication |
| 404 | event_not_found |

## PATCH /v1/hack/events/{slug}/results

<div data-operation="PATCH /v1/hack/events/{slug}/results"></div>

Flips full_ranking on the existing snapshot without recomputing it. 404 no_results_yet if nothing has been published.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| full_ranking | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The snapshot with the new flag |
| 400 | invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or no_results_yet |

## POST /v1/hack/events/{slug}/results/publish

<div data-operation="POST /v1/hack/events/{slug}/results/publish"></div>

Computes eligible weighted judge-entry scores, stores both raw and population-normalised team means, and ranks in the selected score_mode. Equal totals use the preselected criterion mean, then pairwise judge preferences; unresolved ties remain until recorded rank_overrides. The snapshot also keeps track names and prizes and each track winner. rank_overrides may resolve a tie by giving every team in that tied group a distinct integer to order that group (400 invalid_rank_override otherwise). Refused before entries close (409 judging_not_started at draft, open or building). May be called again at any time after that, including after the event has ended (each call recomputes and overwrites; full_ranking is kept from the prior publish, or false on the first one). From closed or judging, publishing moves the event to results and locks judging first (the same transition "Finish judging" performs), so the stage and the event page's own words never keep saying judging is under way once results are out; an already-ended event is never reopened by this.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| rank_overrides | array of object |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The published snapshot |
| 400 | invalid_rank_override or invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | judging_not_started (draft, open or building) |

## GET /v1/hack/events/{slug}/rubric

<div data-operation="GET /v1/hack/events/{slug}/rubric"></div>

The event's scoring criteria in order. New events start with Idea, Execution, Design and Demo, weight 25 each and scored out of 5. PUT replaces that rubric; older events are not retroactively seeded.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The rubric's criteria, in order, with the version to submit when acknowledging the displayed rubric. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/rubric

<div data-operation="PUT /v1/hack/events/{slug}/rubric"></div>

Replaces every criterion at once (position is the array index). 1 to 10 criteria; each name 1-80 characters, one line; each description 0-300 characters; each weight a whole number 0-100, all weights summing to exactly 100; each max_points a whole number 1-10. Refused while judging is locked (409 scores_locked): unlock it first. Existing criterion IDs preserve scores. Omitted criteria and scores above a reduced max_points are deleted. Returns 409 score_deletion_confirmation with counts until confirm_score_deletion is true. Comments on retained scores are preserved.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm_score_deletion | boolean |  |  |
| criteria | array of object | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The rubric after the replace |
| 400 | invalid_rubric or invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | scores_locked or score_deletion_confirmation or tie_rule_closed |

## GET /v1/hack/events/{slug}/site

<div data-operation="GET /v1/hack/events/{slug}/site"></div>

The Website screen's live state, polled while a team waits for its first publish. site is the team site (name, url, ready, exists, live_version, published_at, published_via (the live version's recorded author, often null), taken_down, taken_down_reason, pinned_version, pinned_at). connection is the caller's own state: connected (their Simple Hack connector has this team selected; apps names every distinct app on this team, newest use first, and app is the first), no_team (a connector has not picked a team yet), other_team (it has another team selected; other_team names the event and team), key (no connector, but their team key was used in the last 30 days) or none, plus teammates_connected, the display names of current teammates whose connector has this team selected. Never client ids, grant ids, devices or tokens, and never a teammate's app. Participant on a team only: 409 no_team otherwise; anyone else gets 404 event_not_found.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Team site and connection state |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |
| 409 | no_team |

## PUT /v1/hack/events/{slug}/site

<div data-operation="PUT /v1/hack/events/{slug}/site"></div>

Uses the existing archive checks, versions, deadline and take-down rules. No team key is needed. Static files only, with index.html at the root.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| create | query | integer, one of `0`, `1` |  |  |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated team website version |
| 201 | First team website version |
| 400 | Invalid archive, missing root index.html or blocked file type (invalid_archive, missing_index, blocked_file_type) |
| 404 | Event not found for non-members |
| 409 | No team or publishing closed |
| 413 | Upload exceeds existing size or file count limits |
| 429 | Upload IP rate limit reached |

## POST /v1/hack/events/{slug}/stage

<div data-operation="POST /v1/hack/events/{slug}/stage"></div>

closed ("Submissions closed") is accepted. Setting it makes the deadline now when the deadline is unset or still later; a team extension that is still in the future is kept. Moving from closed back to open or building clears a passed deadline and passed team extensions, so teams can deploy again. Judging and results are supported closed-submission stages. Moving from either back to open or building also clears passed deadlines and extensions.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| stage | string, one of `draft`, `open`, `building`, `closed`, `judging`, `results`, `archived` | yes | closed makes the deadline now when it is unset or still later. Moving from closed back to open or building clears a passed deadline and passed team extensions. Judging and results keep submissions closed; archived ends the event permanently. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated organiser view |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | event_closed (leaving archived), archive_needs_participants (nobody joined: delete instead) |

## PATCH /v1/hack/events/{slug}/team

<div data-operation="PATCH /v1/hack/events/{slug}/team"></div>

Teams redesign, 2026-10-31. Team admin only, acting on the caller's own team. Each field is optional; send only what changes. Renaming is refused once the team is frozen past its deadline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| looking_for | string |  |  |
| name | string |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team as the participant sees it (see createHackEventTeam's 201). |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | team_admin_only |
| 404 | event_not_found (not on a team) |
| 409 | team_name_taken, teams_locked or submissions_closed |

## POST /v1/hack/events/{slug}/team-messages

<div data-operation="POST /v1/hack/events/{slug}/team-messages"></div>

Sends one message to each of 1 to 25 distinct team_ids of this event, in app and by email, to every current member of each team (0003-hack-notices-judges-messages.sql). At most 50 team messages (rows) per event per rolling hour; over that, 429 rate_limited with Retry-After. Organiser only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| team_ids | array of string | yes | Distinct team ids of this event. |
| text | string | yes | 1 to 1000 characters, trimmed. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | One row per team actually sent |
| 400 | invalid_team or invalid_text |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found for a non-organiser |
| 409 | event_closed |
| 429 | rate_limited; Retry-After header |

## GET /v1/hack/events/{slug}/team/export.tar.gz

<div data-operation="GET /v1/hack/events/{slug}/team/export.tar.gz"></div>

Participant personal account only. Exports only their current team's current published files and saved data, including private collections. Does not accept another team selector. Returns 404 no_project if no live project exists.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Download your team project |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## PUT /v1/hack/events/{slug}/team/finding

<div data-operation="PUT /v1/hack/events/{slug}/team/finding"></div>

**Deprecated.**

Deprecated 2026-10-31, kept for one release as an alias of PATCH .../team: team admin only now (was: any approved team member). open is accepted and ignored. Saves only looking_for. Team finding must still be on. Response adds deprecated: "use PATCH /v1/hack/events/{slug}/team".

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| looking_for | string | yes |  |
| open | boolean | yes | Deprecated 2026-10-31: accepted and ignored. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {ok: true, deprecated: "use PATCH /v1/hack/events/{slug}/team"} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or team_admin_only. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## POST /v1/hack/events/{slug}/team/invites

<div data-operation="POST /v1/hack/events/{slug}/team/invites"></div>

Any team member invites an approved opted-in solo participant. Full teams cannot invite. An existing request joins immediately. Repeating an invite returns its pending state.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| user_id | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Existing pending offer, or crossed offers accepted and participant joined. |
| 201 | New pending offer created. Response includes state (created, pending or joined). |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## POST /v1/hack/events/{slug}/team/link

<div data-operation="POST /v1/hack/events/{slug}/team/link"></div>

Teams redesign, 2026-10-31. Team admin only. Destructive: the team's old code and link (https://&lt;hack host&gt;/team/&lt;code&gt;) stop working at once, everywhere.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {code, link} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | team_admin_only |
| 404 | event_not_found (not on a team) |
| 409 | teams_locked or submissions_closed |

## PUT /v1/hack/events/{slug}/team/lock

<div data-operation="PUT /v1/hack/events/{slug}/team/lock"></div>

Teams redesign, 2026-10-31. Team admin only. Locking stops the team taking anyone new by request, link, code, invite or Fill teams, and clears its pending requests and invites (reported in cleared). Unlocking a team kept after its last member left (kept_after_leave) is refused for good.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| locked | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {locked, cleared: {requests, invites}} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | team_admin_only |
| 404 | event_not_found (not on a team) |
| 409 | team_kept, teams_locked or submissions_closed |

## DELETE /v1/hack/events/{slug}/team/members/{user_id}

<div data-operation="DELETE /v1/hack/events/{slug}/team/members/{user_id}"></div>

A team admin removes another teammate while team changes are allowed (teams redesign, 2026-10-31: was any approved team member; team finding no longer needs to be on). Cannot remove yourself; use Leave team. Revokes stale keys, rotates the code, shows the removal notice, and promotes a new admin if this emptied the team of admins.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Removed from the team; they stay in the event. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, team_admin_only, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | teams_locked or submissions_closed. |

## PUT /v1/hack/events/{slug}/team/members/{user_id}/admin

<div data-operation="PUT /v1/hack/events/{slug}/team/members/{user_id}/admin"></div>

Teams redesign, 2026-10-31. Team admin only, acting on the caller's own team. user_id must already be on that team, else 404 (never 403, so this never confirms membership elsewhere). Unmaking the last admin is refused; admins are equal, so an admin may remove another admin or step down themselves as long as someone else stays admin. Gated by the same participant team stages and frozen-team check as PATCH .../team, PUT .../team/lock and POST .../team/link (deviation from the original plan, which left this route ungated; Codex review, 2026-11-01 -- it is still a team composition write, not pure bookkeeping, so it follows the others). The organiser's equivalent (PUT .../teams/{team}/members/{user_id}/admin) keeps no such gate, matching every other organiser team write.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| user_id | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| admin | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {user_id, admin} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | team_admin_only |
| 404 | event_not_found (not on a team, or user_id not on it) |
| 409 | last_team_admin, teams_locked or submissions_closed |

## GET /v1/hack/events/{slug}/team/messages

<div data-operation="GET /v1/hack/events/{slug}/team/messages"></div>

Up to 100 messages sent to the caller's current team, newest first; an empty list when they are on no team. Participant only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Message history |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/team/track

<div data-operation="PUT /v1/hack/events/{slug}/team/track"></div>

Available before the team's deadline while team changes are open. An empty track clears the choice.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| track | string | yes | Stable track slug or empty string |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {track: {id, slug, name, challenge, prize} or null} |
| 403 | approval_pending or approval_rejected |
| 404 | Not found |
| 409 | team_choice_closed or teams_locked |

## GET /v1/hack/events/{slug}/teams

<div data-operation="GET /v1/hack/events/{slug}/teams"></div>

Every team (organiser) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | teams (each with site, deadline, deadline_override, frozen, pinned_version, pinned_url, and since the teams redesign of 2026-10-31: locked, kept, link, members[].admin), team_size_max, on_no_team. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found |

## POST /v1/hack/events/{slug}/teams

<div data-operation="POST /v1/hack/events/{slug}/teams"></div>

The creator becomes the team's admin.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| looking_for | string |  |  |
| name | string | yes |  |
| open | boolean |  | Deprecated 2026-10-31: accepted and ignored for one release. event_teams.open ("who can join") is gone; every unlocked team can be asked to join. Use PUT .../team/lock to stop taking anyone new. |

**Responses:**

| Status | Description |
| --- | --- |
| 201 | The new team as the participant sees it: {slug, name, code, link, admin, locked, kept, looking_for, members[{display_name, you, admin}]}. admin is true (the creator). |
| 400 | invalid_team_name |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | already_in_team, teams_locked or team_name_taken (names are unique in an event, ignoring case, spacing and invisible characters) |

## POST /v1/hack/events/{slug}/teams/fill

<div data-operation="POST /v1/hack/events/{slug}/teams/fill"></div>

Organiser only. Preview with apply:false; confirm before apply:true. Places only approved solo participants into teams with room, topped up to size (people per team, default min(4, team_size_max); teams redesign, 2026-10-31 -- was team_size_max itself, before the cap rose to 20), then new Team 1, Team 2 groups of size, avoiding name and used-site clashes. A locked team is deliberately left alone even with room -- its admin chose to stop taking anyone new -- and is listed in skipped instead, so the caller can explain the gap rather than a stranger being quietly dropped into a new one-person team instead. Returns size, plan [{team,name,new,members:[{user_id,display_name}]}], skipped [{team,name,reason}] (reason is currently always "locked"; a team skipped only because it's already at size isn't listed) and applied. Apply recomputes from current membership. Team finding no longer needs to be on; the event must still allow team changes. Existing teams use their effective deadline including extensions; no new teams are created after the event deadline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| apply | boolean | yes |  |
| size | integer |  | People per team. Default min(4, team_size_max); must be between 1 and the event's team_size_max. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Private team finding response. |
| 400 | invalid_team_size (size must be 1..team_size_max), or see BadRequest. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | teams_locked, team_full, team_kept or already_in_team. |

## POST /v1/hack/events/{slug}/teams/join

<div data-operation="POST /v1/hack/events/{slug}/teams/join"></div>

code accepts either the bare team code or the whole team link (https://&lt;hack host&gt;/team/&lt;code&gt;). Refused when the team is locked (409 team_locked).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | The team's code, or its whole link. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Joined (same team is idempotent 200) |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |
| 409 | team_full, already_in_team, team_locked, teams_locked or submissions_closed |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/hack/events/{slug}/teams/leave

<div data-operation="POST /v1/hack/events/{slug}/teams/leave"></div>

Leave my team (participant) (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Left; an empty team is deleted |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found |
| 409 | teams_locked |

## GET /v1/hack/events/{slug}/teams/open

<div data-operation="GET /v1/hack/events/{slug}/teams/open"></div>

Read private team finding lists. Approved participants and organisers only. Solo participants see every unlocked team (teams redesign, 2026-10-31: "who can join" is gone, so a former code-only team is listed too) with name, track, member display names, member_count, team_size_max, full and looking_for. Team members see opted-in solo people (user_id, display_name, intro). pending contains own requests/invites and the current team requests/invites, with id, team, team_name, user_id, display_name, kind and created_at. Organisers see all. With team_finding off returns enabled:false and empty lists. The caller's own team (when on one) is {looking_for, locked, admin}.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Private team finding response. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## PATCH /v1/hack/events/{slug}/teams/{team}

<div data-operation="PATCH /v1/hack/events/{slug}/teams/{team}"></div>

Organiser only. Changes the display name; the team slug, site address, membership and member keys stay the same. Case-insensitive name must remain unique in the event.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| name | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Rename a team |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or invalid_code |
| 409 | team_name_taken or event_closed |

## DELETE /v1/hack/events/{slug}/teams/{team}

<div data-operation="DELETE /v1/hack/events/{slug}/teams/{team}"></div>

Removes the team. Its members stay in the event on no team, and the team's site moves to Recently deleted.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Team removed; members stay in the event on no team. The team's site moves to Recently deleted. |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |

## PUT /v1/hack/events/{slug}/teams/{team}/deadline

<div data-operation="PUT /v1/hack/events/{slug}/teams/{team}/deadline"></div>

Gives this team its own deadline, which must be after the event's, or takes that extension away. An empty string removes it. The answer is the team as the organiser sees it.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| deadline | string | yes | RFC 3339, or YYYY-MM-DDTHH:MM or YYYY-MM-DD in the event's time zone. An empty string removes the extension. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team |
| 400 | invalid_deadline, or deadline_not_later when the time is not after the event's deadline |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |
| 409 | event_closed (an ended event cannot be changed) |

## POST /v1/hack/events/{slug}/teams/{team}/entry/exclude

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/entry/exclude"></div>

Excludes one team's entry from judging, typically one flagged Incomplete at the deadline (coordinator decision 2026-11-02: an incomplete draft is still judged by default, and the organiser may exclude it by hand instead). The entry and its scores are unaffected; only judging eligibility changes. Reversible with .../entry/include.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team's updated entries-list item |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found, no_entry or event_not_found |

## POST /v1/hack/events/{slug}/teams/{team}/entry/include

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/entry/include"></div>

Puts a hand-excluded entry back into judging.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team's updated entries-list item |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found, no_entry or event_not_found |

## POST /v1/hack/events/{slug}/teams/{team}/link

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/link"></div>

Teams redesign, 2026-10-31. Destructive: the team's old code and link stop working at once, everywhere.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {code, link} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found or event_not_found |
| 409 | event_closed |

## PUT /v1/hack/events/{slug}/teams/{team}/lock

<div data-operation="PUT /v1/hack/events/{slug}/teams/{team}/lock"></div>

Teams redesign, 2026-10-31. Same effect and response as PUT .../team/lock (the team admin's own version); the organiser needs no admin status. Unlocking a team kept after its last member left is refused for good.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| locked | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {locked, cleared: {requests, invites}} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found or event_not_found |
| 409 | team_kept or event_closed |

## POST /v1/hack/events/{slug}/teams/{team}/members

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/members"></div>

The organiser may place someone by hand even on a locked team; never on a team kept after its last member left (409 team_kept).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| user_id | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |
| 409 | team_full, team_kept or event_closed |

## DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}

<div data-operation="DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}"></div>

A team admin may remove another member, never themselves (teams redesign, 2026-10-31: was any teammate), while team changes are allowed and before the team deadline; team finding does not need to be on. Organisers keep their existing team administration access.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |
| user_id | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 204 | Removed from the team; they stay in the event |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down or team_admin_only |
| 404 | team_not_found or event_not_found |

## PUT /v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin

<div data-operation="PUT /v1/hack/events/{slug}/teams/{team}/members/{user_id}/admin"></div>

Teams redesign, 2026-10-31. Same effect and response as PUT .../team/members/{user_id}/admin (the team admin's own version); the organiser needs no admin status. Unmaking the last admin is refused.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |
| user_id | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| admin | boolean | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {user_id, admin} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | team_not_found, event_not_found, or user_id not on the team |
| 409 | last_team_admin or event_closed |

## GET /v1/hack/events/{slug}/teams/{team}/messages

<div data-operation="GET /v1/hack/events/{slug}/teams/{team}/messages"></div>

Up to 100 messages sent to this team, newest first. Organiser only.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Message history |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or team_not_found |

## POST /v1/hack/events/{slug}/teams/{team}/requests

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/requests"></div>

Ask to join an open team with space. Approved participant on no team only. An existing invite joins immediately. Repeating a request returns its pending state.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Existing pending offer, or crossed offers accepted and participant joined. |
| 201 | New pending offer created. Response includes state (created, pending or joined). |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | Not approved, or this action is not yours. |
| 404 | Event, team, member or request not found. |
| 409 | team_finding_off, teams_locked, team_full, already_in_team or submissions_closed. |

## POST /v1/hack/events/{slug}/teams/{team}/restore

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/restore"></div>

Puts this team's site back. 409 platform_takedown when the platform admin took it down; only the platform can undo that. The answer is the team.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |
| 409 | platform_takedown (the platform admin took the site down) or event_closed |

## GET /v1/hack/events/{slug}/teams/{team}/screenshot

<div data-operation="GET /v1/hack/events/{slug}/teams/{team}/screenshot"></div>

That team's screenshot. Organiser, judge, or the platform admin reading. A participant gets 404 event_not_found. The image is served with X-Content-Type-Options nosniff, Content-Security-Policy default-src 'none'; sandbox, Cache-Control private, no-store and Content-Disposition inline.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The screenshot. Content-Type is the stored type (image/png, image/jpeg or image/webp). |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | no_screenshot, team_not_found or event_not_found |

## POST /v1/hack/events/{slug}/teams/{team}/takedown

<div data-operation="POST /v1/hack/events/{slug}/teams/{team}/takedown"></div>

Takes this team's site down. Deploys, saves and entry edits stop, and the site shows its take-down page. reason is optional, one line, at most 300 characters. The body may be omitted. The answer is the team.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| team | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| reason | string |  | Optional. One line |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The team |
| 400 | invalid_reason (not one line, or over 300 characters) or invalid_request |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | team_not_found or event_not_found |
| 409 | event_closed |

## GET /v1/hack/events/{slug}/tracks

<div data-operation="GET /v1/hack/events/{slug}/tracks"></div>

List an event's tracks, challenges and prizes (event member)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Array of id, slug, name, challenge and prize |
| 404 | Not found |

## PUT /v1/hack/events/{slug}/tracks

<div data-operation="PUT /v1/hack/events/{slug}/tracks"></div>

Upserts by stable slug and removes omitted tracks. Removing a track clears teams' choice. Maximum twelve tracks. Public event page shows challenges and prizes.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| tracks | array of object | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved catalog |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 404 | Not found |
| 409 | event_closed |

## GET /v1/hack/events/{slug}/usage

<div data-operation="GET /v1/hack/events/{slug}/usage"></div>

Organiser only. Site and version counts, saved-data/history/screenshot bytes, and regular file bytes for the event holding account. Files include retained versions and Recently deleted; symlinks are not counted twice. Database bytes are content sizes, not physical PostgreSQL disk allocation.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read event storage usage |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |

## GET /v1/hack/events/{slug}/vote

<div data-operation="GET /v1/hack/events/{slug}/vote"></div>

Public for non-draft, non-taken-down events. Vote counts are omitted until the configured closing time.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Event |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/vote

<div data-operation="PUT /v1/hack/events/{slug}/vote"></div>

Signed-in personal account. One vote per canonical verified email per event, with +tags dropped. The same selection is refused as a duplicate; choosing another live team changes it until close. Own-team votes are refused across alias accounts. Approval-restricted members must be approved.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| team | string | yes | Team slug from the public vote options |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Selected team and whether an existing vote changed |
| 403 | vote_ineligible |
| 404 | team_not_found or event_not_found |
| 409 | voting_closed |

## GET /v1/hack/events/{slug}/voting

<div data-operation="GET /v1/hack/events/{slug}/voting"></div>

Organiser only. Returns enabled, opening/closing instants, eligibility, directory listing, current status and votes_to_remove (counts keyed by eligibility for confirmation before narrowing).

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Voting settings |
| 404 | event_not_found |

## PUT /v1/hack/events/{slug}/voting

<div data-operation="PUT /v1/hack/events/{slug}/voting"></div>

Organiser only. A live gallery, opening and closing times, and eligibility are required when enabling. Archived events cannot change voting settings. Changing eligibility deletes earlier votes that no longer qualify and returns votes_removed.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| closes_at | string | yes | RFC3339 or local date/time in event time zone; empty when disabled |
| confirm_vote_removal | boolean |  | Confirm removal of votes that no longer qualify |
| eligibility | string, one of `all_signed_in`, `event_members`, `participants` | yes |  |
| enabled | boolean | yes |  |
| opens_at | string | yes | RFC3339 or local date/time in event time zone; empty when disabled |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved settings |
| 400 | invalid_voting_window or invalid_voting_eligibility |
| 404 | event_not_found |
| 409 | gallery_closed, event_closed or vote_removal_confirmation (includes votes_to_remove count) |

## GET /v1/hack/events/{slug}/website

<div data-operation="GET /v1/hack/events/{slug}/website"></div>

Read the organiser's event website mode and versions (EVENTS=hosted)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | mode, has_custom_page, custom_url, builtin_url, active_version, versions (version_number and created_at), upload_limit_bytes, upload_limits |
| 404 | event_not_found for non-organisers |

## PUT /v1/hack/events/{slug}/website

<div data-operation="PUT /v1/hack/events/{slug}/website"></div>

Uses the same validated, versioned deployment path as team websites. Add ?create=1 for the first upload; a successful upload switches the event address to custom mode.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| create | query | integer, one of `0`, `1` |  |  |

**Request body:** application/gzip

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated website version |
| 201 | First website version |
| 404 | event_not_found for non-organisers |
| 409 | event_closed |

## PATCH /v1/hack/events/{slug}/website

<div data-operation="PATCH /v1/hack/events/{slug}/website"></div>

Switch between built-in and already-published custom event website

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| mode | string, one of `builtin`, `custom` | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Current website state |
| 400 | invalid_request |
| 404 | event_not_found for non-organisers |
| 409 | website_not_published or event_closed |

## PUT /v1/hack/events/{slug}/website/files

<div data-operation="PUT /v1/hack/events/{slug}/website/files"></div>

Uses the same files format and validation as PUT /v1/sites/{sitename}/files; add ?create=1 for the first upload. A successful upload switches the event address to custom mode.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |
| create | query | integer, one of `0`, `1` |  |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| files | object | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Updated website version |
| 201 | First website version |
| 404 | event_not_found for non-organisers |
| 409 | event_closed |

## GET /v1/hack/join/{code}

<div data-operation="GET /v1/hack/join/{code}"></div>

Includes account_name (string): the signed-in account name, or an empty string when none is known.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Join page fields including coc_text |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 404 | invalid_code |
| 410 | event_taken_down |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/hack/join/{code}

<div data-operation="POST /v1/hack/join/{code}"></div>

A new participant supplies answers keyed by question id. An event requiring approval creates a pending application; pending or rejected participants cannot use team or entry routes. 409 already_member for another role.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes |  |
| answers | object |  |  |
| display_name | string |  | Only used when your account has no name yet; it then becomes your account name. Otherwise ignored. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Joined or application pending (rejoin of the same role is idempotent); member display_name is the account name. |
| 400 | coc_required |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | invalid_code |
| 409 | already_member or joining_closed |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/hack/judge/{code}

<div data-operation="GET /v1/hack/judge/{code}"></div>

Includes account_name (string): the signed-in account name, or an empty string when none is known.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Same shape as GET /v1/hack/join/{code} with role judge |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 404 | invalid_code |
| 410 | event_taken_down |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/hack/judge/{code}

<div data-operation="POST /v1/hack/judge/{code}"></div>

Join as a judge (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes |  |
| display_name | string |  | Only used when your account has no name yet; it then becomes your account name. Otherwise ignored. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Joined; member display_name is the account name. |
| 400 | coc_required |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | invalid_code |
| 409 | already_member or judging_closed |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/hack/my-teams

<div data-operation="GET /v1/hack/my-teams"></div>

Every team the signed-in person is on, in an event that has not ended and is not taken down. The connector's consent page uses this list so the person can pick which team's site the connection publishes to.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The caller's teams |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## GET /v1/hack/names/{slug}

<div data-operation="GET /v1/hack/names/{slug}"></div>

Is this event name available (EVENTS=hosted only)

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| slug | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {available, code?, error?, address} |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
| 503 | name_check_unavailable |

## GET /v1/hack/navigation

<div data-operation="GET /v1/hack/navigation"></div>

Read Hack navigation destinations

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Shared navigation map without event or private values |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |

## GET /v1/hack/organiser/{code}

<div data-operation="GET /v1/hack/organiser/{code}"></div>

Includes account_name (string), the signed-in account name or an empty string when none is known. Returns event title, slug, organisation, stage, code of conduct, role organiser and joinable. Invalid, revoked, consumed or expired links return 404. Rate limited.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Preview a co-organiser invitation |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 404 | event_not_found or invalid_code |
| 410 | event_taken_down |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## POST /v1/hack/organiser/{code}

<div data-operation="POST /v1/hack/organiser/{code}"></div>

Signed-in personal account and code of conduct required. Joins with the existing organiser role and consumes the link atomically. Existing event members cannot change roles through this route. Returns the organiser event view.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean, one of `True` | yes |  |
| display_name | string |  | Only used when your account has no name yet; it then becomes your account name. Otherwise ignored. |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Accept a co-organiser invitation |
| 400 | Invalid request. A saved-data write whose JSON holds a NUL character (\u0000), half of a surrogate pair or bytes that are not UTF-8 is code invalid_json. |
| 401 | Missing or invalid API key — send the X-API-Key header, not Authorization Bearer. The body's code is missing_api_key, wrong_auth_header, invalid_api_key, key_expired or key_expired_idle (create a new key from the Keys panel). |
| 403 | event_taken_down |
| 404 | event_not_found or invalid_code |
| 409 | already_member or event_closed |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/hack/preferences

<div data-operation="GET /v1/hack/preferences"></div>

Read account-saved theme and walkthrough completion

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | theme (system, light, dark), organiser_walkthrough_done, judge_walkthrough_done, email_updates (Email me when something waits on me, default true) and read-only email_possible (false for an account whose sign-in is not an email address, which is never emailed) |

## PATCH /v1/hack/preferences

<div data-operation="PATCH /v1/hack/preferences"></div>

Save theme or walkthrough completion across devices

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Request body:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email_updates | boolean |  |  |
| judge_walkthrough_done | boolean |  |  |
| organiser_walkthrough_done | boolean |  |  |
| theme | string, one of `system`, `light`, `dark` |  |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Saved account preferences |
| 400 | invalid_theme |

## GET /v1/hack/team-links/{code}

<div data-operation="GET /v1/hack/team-links/{code}"></div>

Teams redesign, 2026-10-31. Public, signed in or out; same rate limiter as GET .../join/{code}. code accepts either the bare team code or the whole team link. Never writes and never joins (safe to prefetch or unfurl). Never a member's name or email. you is null signed out, else {status}: not_in_event, pending, rejected, judge, organiser, no_team, on_this_team, or on_other_team (+ team_name, the other team's own name). reason is null, locked, full or teams_closed (stage not open/building, or the team's own deadline passed). A taken-down event or an unknown code is 404 team_link_not_found.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| code | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {event: {slug, title, url, stage, applications_open}, team: {name, member_count, team_size_max, track}, joinable, reason, you} |
| 404 | team_link_not_found |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |

## GET /v1/hack/todos

<div data-operation="GET /v1/hack/todos"></div>

One entry per event with something waiting, each with slug, title, role and items. An item is {kind, id, count, text, section, at, dismissable}. Live to-dos (join_request for a team the person admins, invite, application_new for organisers) have id null and clear when answered. News lines (request_answered, application_decided, site_taken_down, site_restored, deadline_changed, team_admin, team_locked, team_removed) have an id and stay until dismissed or 14 days old. Also returns email_updates and email_possible. The same items appear as needs_you on GET /v1/hack/events/{slug}.

**Auth:** an API key (`X-API-Key` header, or a REST-audience connector token as `Authorization: Bearer`).

**Responses:**

| Status | Description |
| --- | --- |
| 200 | {events: [{slug, title, role, items}], email_updates, email_possible} |
