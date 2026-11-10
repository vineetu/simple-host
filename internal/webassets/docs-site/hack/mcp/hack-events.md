# Events, teams and members

## hack_get_navigation

<div data-tool="hack_get_navigation"></div>

Read the shared Hack screens and destination map.

## hack_list_events

<div data-tool="hack_list_events"></div>

List every hackathon event this person is part of, with their role (organiser, participant or judge). Call this before changing an event. You can manage an event only when the role is organiser.

## hack_check_event_name

<div data-tool="hack_check_event_name"></div>

Check whether an event name is free, and the address it would have. Names are 3 to 39 lowercase letters, numbers or hyphens, and cannot start or end with a hyphen. Check before hack_create_event.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name to check. |

## hack_create_event

<div data-tool="hack_create_event"></div>

Create a draft event for this person, who becomes its organiser. Ask them first. A draft starts with the starter rubric. The answer includes the join and judge links. This does not publish a website.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| contact_email | string | yes | Contact email for the event. |
| ends_at | string |  | When the event ends, optional. Must not be before starts_at. |
| expected_participants | integer | yes | How many participants are expected, from 1 to 100000. |
| organisation | string |  | The organiser's company, college or community, if they have one. Optional, up to 120 characters; leave this out rather than sending a placeholder. |
| organiser_name | string | yes | Public event organiser name, one line, up to 100 characters. Membership uses the account name; if empty, this fills it once. |
| purpose | string | yes | What the event is for, up to 1000 characters. |
| slug | string | yes | Event name: 3 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen. |
| starts_at | string | yes | When the event starts. A date (2026-10-01) means midnight in time_zone, or send a date and time. |
| time_zone | string |  | IANA time zone, optional. Defaults to UTC. |
| title | string | yes | Event title, one line, up to 120 characters. |

## hack_get_event

<div data-tool="hack_get_event"></div>

Read one event: its page, stage, settings and membership checklist. Use checklist.next for guided work; confirm actions before writing. When this person is the organiser, the answer includes the join and judge links and next_step, the one forward move the website offers (its one button) for the current stage. next_step.cue is set when a schedule row lining up with that move has come (e.g. "Kickoff is now. Open sign-up?"): ask the organiser, never move the stage on your own. Give those links exactly as returned. Records any due submission deadline pins.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_update_event

<div data-tool="hack_update_event"></div>

Change an event's page text and settings. Sends only the fields you set, so omit anything that should stay as it is. Does not change the stage; use hack_set_event_stage for that. Ask the person before overwriting page text. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| about | string |  | About text. Empty clears it. |
| applications_open | boolean |  | Whether people without the join link can apply from Find a hackathon and the event page. On by default; turning it off stops new applications and keeps waiting ones. Ask the organiser before changing it. |
| coc_text | string |  | The organiser's own code of conduct, added to the default. Empty clears it. |
| contact_email | string |  | Contact email for the event. |
| ends_at | string |  | New end. Empty clears it. |
| entry_required | array of string |  | Deprecated: use hack_set_entry_rules instead (presets, per-field required/optional/off/one_of, judge_first). Sending this still works: it sets required for exactly the listed fields and demotes the stored preset to custom. |
| expected_participants | integer |  | How many participants are expected, from 1 to 100000. |
| gallery_open | boolean |  | Whether the public entry gallery is open. |
| judging_ends_at | string |  | When judging closes, in the event time zone. Empty clears it. Judges with unscored teams are reminded before this time, once set; with no judging_ends_at they get no reminder. |
| organisation | string |  | The organiser's company, college or community, if they have one. Empty clears it; leave this out rather than sending a placeholder. |
| organiser_name | string |  | Public event organiser name. Membership uses the account name; if empty, this fills it once. |
| prizes | string |  | Prizes text. Empty clears it. |
| purpose | string |  | What the event is for. |
| rules | string |  | Rules text. Empty clears it. |
| slug | string | yes | The event name. |
| starts_at | string |  | New start. A date means midnight in the event's time zone. |
| submission_deadline | string |  | Entry deadline in the event time zone. Empty clears it. |
| tagline | string |  | Short line under the title, up to 160 characters. Empty clears it. |
| team_finding | boolean |  | Enable private team finding. Codes keep working when off. |
| team_size_max | integer |  | Maximum people on a team, from 1 to 50. |
| theme | string, one of ``, `poster`, `confetti`, `boardroom`, `garden`, `terminal` |  | Page style for the public event page: poster, confetti, boardroom, garden, terminal, or an empty string for the classic look. The organiser's accent_color still drives buttons and links under every style. |
| time_zone | string |  | IANA time zone. Empty means UTC. |
| title | string |  | New title, one line, up to 120 characters. |

## hack_set_event_stage

<div data-tool="hack_set_event_stage"></div>

Set an event's stage: draft, open, building, closed, judging, results or archived. Moving to Judging locks the judging setup and, in Automatic mode, assigns judges. Ask the person first. closed, judging and results close entries. results locks judging; moving back to judging reopens scoring. archived ends the event and cannot be undone. Organiser only. After a successful change, offer a short announcement through hack_post_announcement describing the move, which the person can edit or skip entirely, and only email participants if they ask for that. For archived, offer and post it before you archive, not after: an ended event takes no announcements.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |
| stage | string, one of `draft`, `open`, `building`, `closed`, `judging`, `results`, `archived` | yes | The stage to set. archived ends the event. |

## hack_select_team

<div data-tool="hack_select_team"></div>

Choose or switch the team site this connection can publish. First call hack_get_my_teams to find your current team_id. The server checks your current approved participant membership now and on every later site call; choosing one team never grants access to another. Ask the person before publishing a new site.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| team_id | string | yes | The UUID of your current event team to publish for. |

## hack_list_open_teams

<div data-tool="hack_list_open_teams"></div>

Read every unlocked team (each with a full flag), opted-in solo participants and your pending requests and invites. Approved participant or organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_ask_to_join

<div data-tool="hack_ask_to_join"></div>

Ask the person before sending a join request. An existing invite joins them immediately; a repeated request stays pending. Refused when the team is locked.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team | string | yes | Team slug. |

## hack_invite_to_team

<div data-tool="hack_invite_to_team"></div>

Invite a listed solo participant to your team. Ask first. An existing request joins them immediately; a repeated invite stays pending. Refused when the team is locked.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| user_id | string | yes | Solo participant ID. |

## hack_accept_team_request

<div data-tool="hack_accept_team_request"></div>

Accept an incoming request (team admins only) as a teammate, or your own invitation. This joins the participant. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes | Request or invite ID. |
| slug | string | yes | Event name. |

## hack_decline_team_request

<div data-tool="hack_decline_team_request"></div>

Decline an incoming request (team admins only) or your own invitation. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes | Request or invite ID. |
| slug | string | yes | Event name. |

## hack_withdraw_team_request

<div data-tool="hack_withdraw_team_request"></div>

Withdraw your request, or cancel an invitation sent by your team. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes | Request or invite ID. |
| slug | string | yes | Event name. |

## hack_remove_teammate

<div data-tool="hack_remove_teammate"></div>

Take another member off your team. Admins only. Their team access stops and the code changes. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| user_id | string | yes | Teammate ID. |

## hack_set_looking

<div data-tool="hack_set_looking"></div>

Opt in or out of the solo list with a short intro. Ask the person first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| intro | string | yes | One line, up to 120 characters. |
| looking | boolean | yes |  |
| slug | string | yes | Event name. |

## hack_preview_team_link

<div data-tool="hack_preview_team_link"></div>

Use when the person pastes a team link. Shows the team and whether they can join. Then ask, and call hack_join_team with the event slug and code.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | Team code, or the whole team link. |

## hack_update_my_team

<div data-tool="hack_update_my_team"></div>

Change your team's name or its one-line looking-for note. Send only what changes. Admins only. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| looking_for | string |  | One line, up to 120 characters. |
| name | string |  | New team name, 1 to 80 characters. |
| slug | string | yes | Event name. |

## hack_lock_my_team

<div data-tool="hack_lock_my_team"></div>

Admins only. Locking stops the team taking anyone new by request, link, code or invite, and clears its pending requests and invites. Ask first, and say those are cleared.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| locked | boolean | yes |  |
| slug | string | yes | Event name. |

## hack_set_teammate_admin

<div data-tool="hack_set_teammate_admin"></div>

Admins only. Destructive: it changes who can approve requests, lock, rename, remove members and rotate the link. The last admin can't be unmade until someone else is admin. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| admin | boolean | yes |  |
| slug | string | yes | Event name. |
| user_id | string | yes | Teammate ID. |

## hack_new_my_team_link

<div data-tool="hack_new_my_team_link"></div>

Admins only. Destructive: the old link and code stop working at once. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_preview_join

<div data-tool="hack_preview_join"></div>

Read the event, code of conduct, signup questions, and whether joining is open.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | Code from the join or judge link. |

## hack_join_event

<div data-tool="hack_join_event"></div>

Join as a participant. Needs a join-link code. Without one, use hack_apply_to_event. Ask the person to accept the displayed code of conduct and provide signup answers first. Ask for a name only when the preview account_name is empty.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes | true once the person has read the code of conduct shown by the preview tool and agreed to it. |
| answers | object |  | Answers keyed by signup question ID. |
| code | string | yes | Code from the join or judge link. |
| display_name | string |  | Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise. |

## hack_preview_application

<div data-tool="hack_preview_application"></div>

For an event found with hack_get_directory or by its address, when the person has no join link. Reads whether applications are open, the optional fields (city, country code, age range, gender), the organiser's sign-up questions and code of conduct, and the person's own status if they already applied.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_apply_to_event

<div data-tool="hack_apply_to_event"></div>

Apply as a participant without a join link. Show the code of conduct from hack_preview_application and ask the person to agree, ask for a name only when account_name is empty, ask the sign-up questions, and offer the optional city, country, age range and gender; never guess or fill those in yourself, and leave them empty if the person does not answer. Tell them only the organisers see the answers. If they choose under_18, tell them a parent or guardian must agree to the Terms (https://simple-hack.app/terms). Every application waits for an organiser; check with hack_application_status. Use hack_join_event instead when the person has a join link.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes | true once the person has read and agreed to the code of conduct. |
| age_range | string, one of ``, `under_18`, `18_24`, `25_34`, `35_plus` |  | Optional. |
| answers | object |  | Answers keyed by signup question ID. |
| city | string |  | Optional. City the person is based in, up to 80 characters. |
| country | string |  | Optional. ISO 3166-1 alpha-2 country code, e.g. ID, GB, US. |
| display_name | string |  | Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise. |
| gender | string, one of ``, `woman`, `man`, `non_binary`, `another`, `prefer_not_to_say` |  | Optional. |
| slug | string | yes | Event name. |

## hack_application_status

<div data-tool="hack_application_status"></div>

Read your participant application approval status and team information. Records any due submission deadline pins.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_create_team

<div data-tool="hack_create_team"></div>

Create a team and join it. You become the team's admin. Ask the person before creating a team.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| looking_for | string |  | One line, up to 120 characters. |
| name | string | yes | Team name. |
| slug | string | yes | Event name. |

## hack_join_team

<div data-tool="hack_join_team"></div>

Join a team with its code or team link. Ask the person before joining. Refused when the team is locked or full.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | Team code, or the whole team link. |
| slug | string | yes | Event name. |

## hack_leave_team

<div data-tool="hack_leave_team"></div>

Leave the current team. Confirm first: the team's access and membership change.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_choose_track

<div data-tool="hack_choose_track"></div>

Choose a listed track for the current team; an empty track clears the choice. An organiser can supply team (team slug) before Judging to fix a missing panel track. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team | string |  | Organiser only: team slug to update before Judging. |
| track | string | yes | Track slug, or empty to clear. |

## hack_get_team_status

<div data-tool="hack_get_team_status"></div>

Read this participant's team (its link, admins and whether you are one, and whether it is locked), site status, track and deadline. Records any due submission deadline pins.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_my_teams

<div data-tool="hack_get_my_teams"></div>

List current teams and their site addresses for this person.

## hack_get_my_team_messages

<div data-tool="hack_get_my_team_messages"></div>

Read messages your team got from the organisers, newest first. Empty when you have no team.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_my_todos

<div data-tool="hack_get_my_todos"></div>

List what waits on this person in every event they are in: join requests for a team they admin, invites, applications waiting for an organiser, and news such as an application answer, a team site taken down, a deadline extension or being made team admin. Each item has the same text the website shows. Also says whether they get these by email. Start here when the person asks what needs them.

## hack_dismiss_notices

<div data-tool="hack_dismiss_notices"></div>

Mark this person's own news lines in one event as seen, the given ids from hack_get_my_todos or every line when ids is left out. Live to-dos (requests, invites, applications) cannot be dismissed, only answered. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| ids | array of string |  | Notice ids (the id of an item with dismissable true). Leave out to dismiss every news line in the event. |
| slug | string | yes | Event name. |

## hack_set_email_updates

<div data-tool="hack_set_email_updates"></div>

Turn on or off the emails about join requests, invites, application answers and changes to the person's team or its site (organisers also hear about new applications). At most one email of each kind every 15 minutes. An account without an email address never gets any. Ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| email_updates | boolean | yes |  |

## hack_preview_judge

<div data-tool="hack_preview_judge"></div>

Read the event and code of conduct before joining as a judge.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | Code from the join or judge link. |

## hack_join_judge

<div data-tool="hack_join_judge"></div>

Join as a judge. Ask the person to accept the displayed code of conduct first. Ask for a name only when the preview account_name is empty.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| accept_coc | boolean | yes | true once the person has read the code of conduct shown by the preview tool and agreed to it. |
| code | string | yes | Code from the join or judge link. |
| display_name | string |  | Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise. |

## hack_preview_leave_event

<div data-tool="hack_preview_leave_event"></div>

Read what leaving would do for this person before asking them: their case (participant, applicant, judge, co_organiser, creator, last_organiser), whether it is blocked and why, whether their team would be deleted (empty, before its deadline) or instead kept exactly as it is -- entry, site and scores untouched, just taken off its member list -- once past the team's deadline or the event is closed, judging or results, scores that stay, who can take over, and the end or delete choice for the only organiser.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_leave_event

<div data-tool="hack_leave_event"></div>

Destructive, for the signed-in person only. Call hack_preview_leave_event first, tell the person exactly what happens (team deleted if it would be left empty before its deadline; otherwise, once past the deadline or the event closed/judging/results, the team stays with its entry, site and scores untouched and the person just comes off its member list; access lost; event ended or deleted) and ask them before calling. confirm is the event's address name. The event creator sends successor_user_id from the preview. The only organiser sends close: end or delete, chosen by the person. Not possible on an ended event.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| close | string, one of `end`, `delete` |  | Only organiser: end keeps the page and results, delete removes everything. Ask the person. |
| confirm | string | yes | The event's address name, sent only after the person agreed to leave. |
| slug | string | yes | Event name. |
| successor_user_id | string |  | Creator only: user_id of another organiser from the preview. |

## hack_get_directory

<div data-tool="hack_get_directory"></div>

Read the public event directory. Rows with applications_open true take applications without a join link: use hack_preview_application then hack_apply_to_event.

## hack_get_content

<div data-tool="hack_get_content"></div>

Read sponsors, FAQ and schedule. fixed_schedule lists the event's real times as read-only rows marked fixed: true (event start and end, the entry deadline or the caller's own team extension, the voting window, results published), each with set_in naming where it is changed (basics, entries, results). Show them with the schedule but never write them back. prompt on a row (organisers only) is the move the next step asks for at that time. Organiser only for private drafts.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_content

<div data-tool="hack_set_content"></div>

Replace sponsors, FAQ and schedule with the supplied REST content body. Ask before overwriting. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | Whole content document: sponsors [{name,tier,logo_data,url}], faq [{question,answer}], schedule [{title,description,start_at,end_at,prompt}]. prompt is optional: open, building, closed, judging, results or publish_results makes the organiser's next step ask for that move when the row starts (it never moves the stage itself). Send only the editable schedule rows: fixed_schedule rows and any row marked fixed are ignored, since they come from the event's settings. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_announcements

<div data-tool="hack_get_announcements"></div>

Read event announcements. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_edit_announcement

<div data-tool="hack_edit_announcement"></div>

Edit title and body. Editing does not send a new email. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| announcement | string | yes | REST path parameter: announcement. |
| body | object | yes | title and body. Editing does not send a new email. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_delete_announcement

<div data-tool="hack_delete_announcement"></div>

Delete an announcement. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| announcement | string | yes | REST path parameter: announcement. |
| slug | string | yes | REST path parameter: slug. |

## hack_post_announcement

<div data-tool="hack_post_announcement"></div>

Post an event announcement. Body includes text and optional email flag; email sends it to every approved participant with an email-shaped username, plus a copy to the posting organiser when their own username is email-shaped (one delivery, never duplicated if they are also a participant). The response's emails_queued is the real count actually queued, not an estimate; organiser_copied says whether one of those is the organiser's own copy. Ask the person before posting or emailing. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | title, body, and optional email_participants boolean. When true, email goes to every approved participant with an email-shaped username, plus one copy to the posting organiser if their own username is email-shaped too (never a duplicate if they are also a participant). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_registration

<div data-tool="hack_get_registration"></div>

Read signup questions and approval settings. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_registration

<div data-tool="hack_set_registration"></div>

Replace signup questions and approval_required. approval_required and admit_pending govern join-link sign-ups only; an application (from hack_apply_to_event) always stays pending until decided, whatever this says. When turning approval off, ask whether to approve waiting join-link sign-ups with admit_pending: true in this request or leave them pending. Ask before overwriting existing questions. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | questions [{id,prompt,required}], approval_required boolean (join-link sign-ups only), optional admit_pending boolean (approves join-link pending only; applications always stay pending). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_applications

<div data-tool="hack_get_applications"></div>

Read pending and decided applications, with joined_via, city, country, age_range and gender for each. An application (joined_via application) always needs a decision, even when approval is off for join-link sign-ups. An under_18 applicant deserves the organiser's attention (no extra data is collected). Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_decide_application

<div data-tool="hack_decide_application"></div>

Approve or reject an applicant using body.decision. Ask before rejecting. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | decision: approved or rejected. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_get_tracks

<div data-tool="hack_get_tracks"></div>

Read event tracks. Organiser or participant.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_tracks

<div data-tool="hack_set_tracks"></div>

Replace the full tracks list. Slugs allow 80 characters; duplicate names are allowed. Before removing a track, read teams and warn how many teams will have no track. Ask before overwriting. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | tracks [{slug,name,challenge,prize}]. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_set_directory_listing

<div data-tool="hack_set_directory_listing"></div>

New events are unlisted. Set listed in the REST body to show or hide the event in the public directory. Ask before listing publicly. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | listed boolean. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_regenerate_code

<div data-tool="hack_regenerate_code"></div>

Regenerate a join or judge invitation (kind is join or judge). The old link stops working. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| kind | string | yes | REST path parameter: kind. |
| slug | string | yes | REST path parameter: slug. |

## hack_delete_event

<div data-tool="hack_delete_event"></div>

Permanently delete an event at any stage, its public page, results, team sites, entries, scores and votes. Participants and judges lose access; the name stays reserved unless nobody ever joined. No undo. Always explain this and confirm with the person first, then send body.confirm matching slug. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | confirm: the exact event address name (slug), supplied only after the person confirms deletion. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_people

<div data-tool="hack_get_people"></div>

Read organiser, participant and judge members. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_remove_person

<div data-tool="hack_remove_person"></div>

Remove a member and their event access. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_revoke_person_key

<div data-tool="hack_revoke_person_key"></div>

Revoke a person's event key. Ask first; publishing with it stops. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_fill_teams

<div data-tool="hack_fill_teams"></div>

Preview with body.apply:false. Show the plan and confirm before body.apply:true. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | Set apply:false to preview placements of approved solo participants into teams with room, topped up to size (people per team, default min(4, the event's team size)), then new teams of that size. Locked teams are skipped. Show the plan and confirm before apply:true; the plan is recomputed using current membership. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_teams

<div data-tool="hack_get_teams"></div>

Read event teams and member names and IDs. Emails are omitted unless include_emails is true. Records any due submission deadline pins. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_emails | boolean |  | Set true only when the organiser asks for team members' email addresses. |
| slug | string | yes | REST path parameter: slug. |

## hack_move_member

<div data-tool="hack_move_member"></div>

Move a member to the specified team using the REST body. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | user_id of the participant to move to the team named in the path. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_remove_team_member

<div data-tool="hack_remove_team_member"></div>

Take a person off a team. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_rename_team

<div data-tool="hack_rename_team"></div>

Rename an event team's display name with body.name. Its site slug and address stay the same. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | name for the team's new display name. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_lock_team

<div data-tool="hack_lock_team"></div>

Body.locked true or false. Locking stops the team taking anyone new by request, link, code or invite, and clears its pending requests and invites. Ask first, and say those are cleared. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | locked boolean. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_set_team_admin

<div data-tool="hack_set_team_admin"></div>

Body.admin true or false for the member in the path. Destructive: it changes who can approve requests, lock, rename, remove members and rotate the link. The last admin can't be unmade until someone else is admin. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | admin boolean for the user_id in the path. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_new_team_link

<div data-tool="hack_new_team_link"></div>

Destructive: the team's old link and code stop working at once. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_delete_team

<div data-tool="hack_delete_team"></div>

Delete a team and its event entry. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_set_team_deadline

<div data-tool="hack_set_team_deadline"></div>

Give one team more time with body.deadline. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | deadline in the event time zone, or an empty string to remove the extension. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_take_down_team_site

<div data-tool="hack_take_down_team_site"></div>

Take a team's public site down. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_restore_team_site

<div data-tool="hack_restore_team_site"></div>

Restore a team's public site. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_create_organiser_invite

<div data-tool="hack_create_organiser_invite"></div>

Create a co-organiser invitation link. Ask first before sharing it. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_revoke_organiser_invite

<div data-tool="hack_revoke_organiser_invite"></div>

Revoke the current co-organiser invitation link. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_preview_organiser_invite

<div data-tool="hack_preview_organiser_invite"></div>

Read a co-organiser invitation before accepting it.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string | yes | REST path parameter: code. |

## hack_accept_organiser_invite

<div data-tool="hack_accept_organiser_invite"></div>

Accept the invitation with body.accept_coc. Ask the person first. Ask for body.display_name only when the preview account_name is empty.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | accept_coc true; optional display_name. Only when the person's account has no name yet (the preview shows account_name empty): ask once; it becomes their account name, shown in every event. Leave out otherwise. The REST route validates each field and returns its own errors. |
| code | string | yes | REST path parameter: code. |

## hack_remove_organiser

<div data-tool="hack_remove_organiser"></div>

Remove a co-organiser. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_export_participants

<div data-tool="hack_export_participants"></div>

Read the participant CSV, which adds city, country, age_range, gender and joined_via columns. Organiser only; keep private.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_export_teams

<div data-tool="hack_export_teams"></div>

Read the team CSV. Organiser only; keep private.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_export_projects_archive

<div data-tool="hack_export_projects_archive"></div>

Create a short-lived private download link for the complete team site archive. Organiser only; give the link only to the person.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_export_own_team_archive

<div data-tool="hack_export_own_team_archive"></div>

Create a short-lived private download link for your team's complete archive; give the link only to the person.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_get_usage

<div data-tool="hack_get_usage"></div>

Read event storage and traffic usage. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_get_team_key

<div data-tool="hack_get_team_key"></div>

Read whether your team has a publishing key. Participant only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_message_team

<div data-tool="hack_message_team"></div>

Send a message to one or more teams, in-app and by email, from the organisers. Ask first: it emails people. At most 50 team messages per event per rolling hour. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | team_ids (1 to 25 distinct team ids of this event) and text (1 to 1000 characters). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_team_messages

<div data-tool="hack_get_team_messages"></div>

Read messages sent to one team from the organisers, newest first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_get_reminder_settings

<div data-tool="hack_get_reminder_settings"></div>

Read the deadline reminder offsets (minutes before a team's deadline and before judging closes), the allowed range, and when judging closes. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_reminder_settings

<div data-tool="hack_set_reminder_settings"></div>

Replace the reminder offsets with body.offsets_minutes. Ask before overwriting. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | offsets_minutes: 0 to 5 distinct minute values, each 15 to 20160 (14 days). Teams without a submitted entry, and judges with unscored teams once judging_ends_at is set, are emailed this long before their deadline. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_revoke_team_key

<div data-tool="hack_revoke_team_key"></div>

Turn your team's publishing key off. Ask first. Participant only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_event_icon

<div data-tool="hack_set_event_icon"></div>

Set the event icon from base64 PNG, JPEG or WebP bytes. The server checks actual image type and its configured size limit. Ask before replacing the icon. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| content_type | string, one of `image/png`, `image/jpeg`, `image/webp` | yes | The image's MIME type. |
| image_base64 | string | yes | Base64-encoded image bytes, without a data: prefix. |
| slug | string | yes | Event name. |

## hack_clear_event_icon

<div data-tool="hack_clear_event_icon"></div>

Remove the uploaded event icon and restore its generated initial icon. Ask the organiser first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_public_event

<div data-tool="hack_get_public_event"></div>

Read the credentialless public event feed, including eligible gallery, published results and closed vote ranking. No private codes or contact email are returned.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_preferences

<div data-tool="hack_get_preferences"></div>

Read this account's theme and completed organiser/judge walkthrough flags.

## hack_set_preferences

<div data-tool="hack_set_preferences"></div>

Set this account's theme (system, light or dark) and/or whether each walkthrough is complete. Sends only supplied fields.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| judge_walkthrough_done | boolean |  | true marks the judge walkthrough as seen, so it stops showing. |
| organiser_walkthrough_done | boolean |  | true marks the organiser walkthrough as seen, so it stops showing. |
| theme | string, one of `system`, `light`, `dark` |  | Colour theme for the person's Simple Hack pages: system follows their device. |

## hack_get_checklist

<div data-tool="hack_get_checklist"></div>

Read the membership role's checklist and next action. Organisers may also request judge. Records due entry deadline pins.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| role | string, one of `organiser`, `judge`, `participant` |  |  |
| slug | string | yes | Event slug. |

## hack_set_checklist_not_needed

<div data-tool="hack_set_checklist_not_needed"></div>

Set or restore a shared optional organiser checklist item. Ask first. Ended events refuse changes.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| item_id | string | yes | Optional organiser item ID. |
| not_needed | boolean | yes |  |
| slug | string | yes | Event slug. |

## hack_acknowledge_checklist_item

<div data-tool="hack_acknowledge_checklist_item"></div>

Explicitly confirm o.rubric-ready, your j.rubric, or your j.conflicts review. Ask first. Rubric items need the current checklist version; conflicts are unversioned.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| item_id | string | yes | o.rubric-ready, j.rubric or j.conflicts. |
| slug | string | yes | Event slug. |
| version | string |  | Current rubric item's version; omit for conflicts. |
