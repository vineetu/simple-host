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
| Read or replace rubric | `hack_get_rubric`, `hack_set_rubric` |
| Export scores or results CSV | `hack_export_scores`, `hack_export_results` |
| Get directory | `hack_get_directory` |
| Get content | `hack_get_content` |
| Set content | `hack_set_content` |
| Get announcements | `hack_get_announcements` |
| Post announcement | `hack_post_announcement` |
| Get registration | `hack_get_registration` |
| Set registration | `hack_set_registration` |
| Get applications | `hack_get_applications` |
| Decide application | `hack_decide_application` |
| Get tracks | `hack_get_tracks` |
| Set tracks | `hack_set_tracks` |
| Get voting settings | `hack_get_voting_settings` |
| Set voting settings | `hack_set_voting_settings` |
| Set directory listing | `hack_set_directory_listing` |
| Regenerate code | `hack_regenerate_code` |
| Delete event at any stage; always ask first, no undo | `hack_delete_event` with `slug` and `body: {"confirm":"<slug>"}` |
| Get people | `hack_get_people` |
| Remove person | `hack_remove_person` |
| Revoke person key | `hack_revoke_person_key` |
| Get teams | `hack_get_teams` |
| Move member | `hack_move_member` |
| Remove team member | `hack_remove_team_member` |
| Rename team | `hack_rename_team` |
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

For a custom event website, read `hack_get_event_website(slug)` first. Publish with `hack_publish_event_website(slug, files, files_base64)` using a complete map of site-relative files and root `index.html`; binary assets use base64. The tool also accepts a base64 tar.gz archive. The custom page can read the public event feed; no account credential belongs in website code. Switching to `builtin` preserves custom files.

`hack_set_event_icon(slug, content_type, image_base64)` accepts PNG, JPEG or WebP up to the configured limit; clear it to restore the generated initial icon. Account preferences are personal: `hack_get_preferences` and `hack_set_preferences` take theme (`system`, `light`, `dark`) and walkthrough flags, not event settings.

CSV and private archive exports may be large; the management page is an alternative for downloads. Give a returned private archive link only to the organiser; current membership is checked at download. For another judge's conflict, use the returned `judge_user_id` where the tool accepts it. A team rename changes its display name; keep using the returned slug and site URL.

Before changing a public stage, result, listing, website or invitation link; emailing participants; removing access; deleting; or replacing a scored rubric, explain the effect and ask. A new join or judge link invalidates its predecessor; rubric saves keep existing criterion IDs and scores; confirm reported deletions with confirm_score_deletion; ending an event is final.

Automatic assignments default to judges only. Use body.include_organisers=true to include organisers. Judges can save scores only when the stage is closed or judging.

Announcements: `PUT /v1/hack/events/{slug}/announcements/{announcement}` accepts `title` and `body`. Editing does not send a new email. `DELETE` on the same path removes it. MCP: `hack_edit_announcement` and `hack_delete_announcement`, with `slug`, `announcement`, and a `body` object for edits. Ask before deleting.

Website zip uploads also work in the signed-in Page tab, including in draft. The event website GET returns recent versions with dates and upload limits. Static archives need index.html at the root; existing size and file checks apply.

When turning registration approval off, ask whether to admit pending applicants and send `admit_pending: true` with `approval_required: false` to approve them in the same request. Before narrowing voting eligibility, inspect `votes_to_remove` and ask before removing votes; send `confirm_vote_removal: true` after confirmation. Otherwise the server returns 409 `vote_removal_confirmation` with the count.

`hack_get_teams` omits member emails by default, including people on no team. Names and member IDs remain available for moves and removals. Set `include_emails: true` only when the organiser asks for team members' email addresses. `hack_get_people` and `hack_export_participants` retain emails. Team mutation results omit rosters and invitation codes; application decisions return status without the applicant ID.


Team finding (SIM-225): private to approved participants and organisers, on by default.
Use hack_list_open_teams to read open team rosters, solo listings and pending offers;
hack_ask_to_join, hack_invite_to_team, hack_accept_team_request,
hack_decline_team_request and hack_withdraw_team_request handle requests/invites.
hack_set_team_finding sets open and looking_for (one line, max 120 characters);
hack_set_looking sets looking and intro (one line, max 120 characters).
hack_create_team accepts optional open (default true) and looking_for.
hack_remove_teammate removes another teammate after confirmation. Use hack_leave_team for yourself.
Joining by any method clears other pending offers and the solo listing; leaving restores nothing.
Organisers use hack_fill_teams with body.apply:false, show the plan and confirm,
then body.apply:true to place approved solo people into open teams with room and
new Team 1, Team 2 groups up to the event maximum, avoiding name clashes.
The apply plan uses current membership. hack_update_event accepts team_finding.
Off hides stored pending offers and refuses finding actions; team codes still work.
REST paths under /v1/hack/events/{slug}/: GET teams/open; PUT team/finding
{open,looking_for}; PUT me/looking {looking,intro}; POST teams/{team}/requests;
POST team/invites {user_id}; POST requests/{id}/{action} (accept or decline);
DELETE requests/{id}; DELETE team/members/{user_id}; POST teams/fill {apply}.
Lists never appear on the public event page. Full teams cannot accept or invite.
