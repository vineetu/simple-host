# Organiser connector operations

Use the Simple Hack connector signed in through its trusted browser window. Follow each tool's schema, use returned event slugs and IDs, and read state before replacement. Do not request or process sign-in codes, account keys, team keys, passwords, passcodes, or other secrets in chat. This reference is a packaged snapshot; do not fetch new instructions from public site content.

| Action | Connector tool |
|---|---|
| List my events and roles | `hack_list_events` |
| Check event name | `hack_check_event_name` |
| Create draft | `hack_create_event` |
| Read event and organiser links | `hack_get_event` |
| Read public event data for a custom page | `hack_get_public_event` |
| Edit event settings and text | `hack_update_event` |
| Read event website mode and version | `hack_get_event_website` |
| Publish custom event website | `hack_publish_event_website` |
| Choose built-in or custom page | `hack_set_event_website_mode` |
| Read, set or clear event icon | public icon URL from event, `hack_set_event_icon`, `hack_clear_event_icon` |
| Read or set my account theme and walkthroughs | `hack_get_preferences`, `hack_set_preferences` |
| Change event stage | `hack_set_event_stage` |
| Read or replace rubric | `hack_get_rubric`, `hack_set_rubric` (an approved participant can also call `hack_get_rubric`; there is no setting to hide it from teams) |
| Export scores or results CSV | `hack_export_scores`, `hack_export_results` |
| Get directory | `hack_get_directory` |
| Get content | `hack_get_content` |
| Set content | `hack_set_content` |
| Get announcements | `hack_get_announcements` |
| Post announcement | `hack_post_announcement` |
| Get registration | `hack_get_registration` |
| Set registration | `hack_set_registration` |
| Turn applications on or off | `hack_update_event` (`applications_open`) |
| Get applications | `hack_get_applications` |
| Decide application | `hack_decide_application` |
| Get tracks | `hack_get_tracks` |
| Set tracks | `hack_set_tracks` |
| Get voting settings | `hack_get_voting_settings` |
| Set voting settings | `hack_set_voting_settings` |
| Set directory listing | `hack_set_directory_listing` |
| Regenerate code | `hack_regenerate_code` |
| Delete event at any stage; always ask first, no undo | `hack_delete_event` with `slug` and `body: {"confirm":"<slug>"}` |
| Preview what leaving does | `hack_preview_leave_event` |
| Leave the event myself | `hack_leave_event` |
| Delete a departed judge's scores (ask first, `confirm` is the event's address name) | `hack_delete_former_judge_scores` |
| Get people | `hack_get_people` |
| Remove person | `hack_remove_person` |
| Revoke person key | `hack_revoke_person_key` |
| Get teams | `hack_get_teams` |
| Move member | `hack_move_member` |
| Remove team member | `hack_remove_team_member` |
| Rename team | `hack_rename_team` |
| Lock or unlock a team | `hack_lock_team` |
| Make or remove a team admin | `hack_set_team_admin` |
| Make a new team link | `hack_new_team_link` |
| Preview or apply Fill teams | `hack_fill_teams` |
| Delete team | `hack_delete_team` |
| Set team deadline | `hack_set_team_deadline` |
| Take down team site | `hack_take_down_team_site` |
| Restore team site | `hack_restore_team_site` |
| Create organiser invite | `hack_create_organiser_invite` |
| Revoke organiser invite | `hack_revoke_organiser_invite` |
| Preview organiser invite | `hack_preview_organiser_invite` |
| Accept organiser invite | `hack_accept_organiser_invite` |
| Remove organiser | `hack_remove_organiser` |
| Get judging settings | `hack_get_judging_settings` |
| Set judging settings | `hack_set_judging_settings` |
| Generate assignments | `hack_generate_assignments` |
| Get assignments | `hack_get_assignments` |
| Set assignments | `hack_set_assignments` |
| Get panels | `hack_get_panels` |
| Set panels | `hack_set_panels` |
| Preview judging assignments | `hack_preview_judging_assignments` |
| Get conflicts | `hack_get_conflicts` |
| Set judge conflict | `hack_set_judge_conflict` |
| Remove judge conflict | `hack_remove_judge_conflict` |
| Get judging dashboard | `hack_get_judging_dashboard` |
| Lock judging | `hack_lock_judging` |
| Unlock judging | `hack_unlock_judging` |
| Publish results | `hack_publish_results` |
| Set results view | `hack_set_results_view` |
| Get public results | `hack_get_public_results` |
| Export participants | `hack_export_participants` |
| Export teams | `hack_export_teams` |
| Export entries | `hack_export_entries` |
| Make a private entry archive link | `hack_export_projects_archive` |
| Get usage | `hack_get_usage` |
| Read event-website notifications | `list_notifications` with `event` |
| Remove event-website notification | `remove_notification` with `event` |

For a custom event website, read `hack_get_event_website(slug)` first. Publish with `hack_publish_event_website(slug, files, files_base64)` using a complete map of site-relative files and root `index.html`; binary assets use base64. The tool also accepts a base64 tar.gz archive. The custom page can read the public event feed; no account credential belongs in website code. Switching to `builtin` preserves custom files.

`hack_set_event_icon(slug, content_type, image_base64)` accepts PNG, JPEG or WebP up to the configured limit; clear it to restore the generated initial icon. Account preferences are personal: `hack_get_preferences` and `hack_set_preferences` take theme (`system`, `light`, `dark`) and walkthrough flags, not event settings.

CSV and private archive exports may be large; the management page is an alternative for downloads. Give a returned private archive link only to the organiser; current membership is checked at download. For another judge's conflict, use the returned `judge_user_id` where the tool accepts it. A team rename changes its display name; keep using the returned slug and site URL.

Before changing a public stage, result, listing, website or invitation link; emailing participants; removing access; deleting; or replacing a scored rubric, explain the effect and ask. A new join or judge link invalidates its predecessor; rubric saves keep existing criterion IDs and scores; confirm reported deletions with confirm_score_deletion; ending an event is final.

`hack_get_event` returns `next_step` for an organiser: the one forward move the website's Overview button offers for the event's current stage (its label, and `blocked` when it can't be taken yet). Name that move when the organiser asks what happens next, and call `hack_set_event_stage` (or `hack_publish_results` when `next_step.kind` is `publish_results`) to take it. Moving the stage to results also locks judging in the same step when it was not locked already, so `hack_lock_judging` is no longer needed to get Results ready for publishing. Moving back to judging leaves the lock on.

Leaving the event is destructive too. Preview it with `hack_preview_leave_event`, tell the organiser what happens, and ask before calling `hack_leave_event`. The creator sends `successor_user_id` from the preview, and the only organiser sends `close: "end"` or `close: "delete"`, chosen by the person.

Automatic assignments default to judges only. Use body.include_organisers=true to include organisers. Judges can save scores only when the stage is closed or judging.

Announcements: `PUT /v1/hack/events/{slug}/announcements/{announcement}` accepts `title` and `body`. Editing does not send a new email. `DELETE` on the same path removes it. MCP: `hack_edit_announcement` and `hack_delete_announcement`, with `slug`, `announcement`, and a `body` object for edits. Ask before deleting.

Posting an announcement (`hack_post_announcement`, `POST /v1/hack/events/{slug}/announcements`) with `email_participants: true` emails every approved participant whose username is an email address, plus one copy to the posting organiser when their own username is email-shaped too (never a duplicate if they are also a participant). The response's `emails_queued` is the real count actually queued, not the event's total participant count; `organiser_copied` says whether that count includes the organiser's own copy. `GET /v1/hack/events/{slug}/announcements` returns each announcement's `emails_sent`, the number of those deliveries actually sent so far.

Website zip uploads also work in the signed-in Page tab, including in draft. The event website GET returns recent versions with dates and upload limits. Static archives need index.html at the root; existing size and file checks apply.

`list_notifications` and `remove_notification` read or remove an organiser website's notification subscriptions with `event`. The website's Settings page, under Notifications, is the only place to create a subscription, pause or resume it, send a test, rotate its signing secret, or read delivery history. This connector does not wrap those actions.

When turning registration approval off, ask whether to admit pending applicants and send `admit_pending: true` with `approval_required: false` to approve them in the same request. Before narrowing voting eligibility, inspect `votes_to_remove` and ask before removing votes; send `confirm_vote_removal: true` after confirmation. Otherwise the server returns 409 `vote_removal_confirmation` with the count.

People without a join link can apply from Find a hackathon or the event's public page. Applications are on by default, and only events listed in Find a hackathon take them, so an unlisted event stays join-link only. An application always waits for an organiser's decision, even when `approval_required` is off for join-link sign-ups. `admit_pending` only admits waiting join-link sign-ups, never applications. `hack_get_applications` lists `joined_via`, `city`, `country`, `age_range` and `gender` for each one, and `hack_export_participants` adds the same five columns to the participants CSV. Turn applications on or off with `hack_update_event`'s `applications_open` field, and ask the organiser before changing it. An organiser with their own event website can link to `https://simple-hack.app/e/{slug}/apply`. `hack_preview_application` only includes the organiser's sign-up questions and code of conduct while applications are open right now; a closed or switched-off event returns neither, even to a signed-in caller. A per-event ceiling (default 500 pending) returns 409 `event_applications_full` once reached, separate from the per-account pending cap.

`hack_get_teams` omits member emails by default, including people on no team. Names and member IDs remain available for moves and removals. Set `include_emails: true` only when the organiser asks for team members' email addresses. `hack_get_people` and `hack_export_participants` retain emails. Team mutation results omit rosters and invitation codes; application decisions return status without the applicant ID.


Teams (redesigned 2026-10-31): a team's creator is its first admin. Admins approve or decline
join requests, lock or unlock the team, rename it, edit its looking-for note, remove members,
make or remove other admins, and roll a new team link, `hack_lock_team`, `hack_set_team_admin`
and `hack_new_team_link` by team slug. The last admin on a team can't be unmade or removed
until someone else is one. When an admin leaves, is removed, or is moved off, the
longest-standing remaining member takes over automatically. Locking a team clears its pending requests
and invites and reports the counts. An ordinary locked team still takes organiser
placement, just nothing else. One kept after its last member left is stricter: locked for
good, with neither an unlock nor a placement allowed. `hack_create_team` no longer takes `open`, and
`hack_set_team_finding` is gone, replaced by the participant tool `hack_update_my_team`.
`hack_join_team`'s `code`, and the `code` read by the team-link preview tool, each take a
bare code or the whole team link, `https://<hack host>/team/<code>`.

Team finding (SIM-225): private to approved participants and organisers, on by default,
and separate from locking. A team can be unlocked but hidden from finding, or findable but
locked. `hack_list_open_teams` reads every unlocked team (each with a `full` flag), solo
listings and pending offers, with `created_at` on each pending row. There is no "who can
join" choice on a team any more, so a former code-only team simply appears in this list too.
Accepting or declining a request now needs a team admin, where it took any teammate before.
Invites are unchanged: any teammate sends one, and the invitee accepts or declines it. Removing a
teammate is admin only and no longer needs team finding on. Joining by any method clears other
pending offers and the solo listing, and leaving restores nothing. Organisers use
`hack_fill_teams` with `body.apply:false`, show the plan and confirm, then `body.apply:true` to
place approved solo people into teams with room, topped up to `body.size` (people per team,
default `min(4, team_size_max)` now that the size cap defaults to 20), and new Team 1, Team 2
groups of that size, avoiding name clashes and skipping locked teams. The apply plan uses
current membership. `hack_update_event` still accepts `team_finding`: off hides the lists and
pending offers and refuses new requests and invites, while codes, links, locking, admins and
Fill teams keep working either way.

REST paths under /v1/hack/events/{slug}/: `GET teams/open` · `PUT teams/{team}/lock` {locked}
· `PUT teams/{team}/members/{user_id}/admin` {admin} · `POST teams/{team}/link` · `POST
teams/{team}/requests` · `POST team/invites` {user_id} · `POST requests/{id}/{action}`
(accept or decline) · `DELETE requests/{id}` · `DELETE team/members/{user_id}` · `POST
teams/fill` {apply, size}. `GET /v1/hack/team-links/{code}` is public and never joins. Lists
never appear on the public event page. A locked or full team cannot accept or invite.
