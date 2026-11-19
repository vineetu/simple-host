# Organiser connector operations

Follow each tool's schema, use returned slugs and IDs, and read state before replacing it. Never handle sign-in codes, keys, passwords or passcodes in chat.

| Action | Connector tool |
|---|---|
| List my events and roles | `hack_list_events` |
| Checklist and next action | `hack_get_checklist` |
| Check name; create draft | `hack_check_event_name`, `hack_create_event` |
| Read event, links and `next_step` | `hack_get_event` |
| Edit settings and text | `hack_update_event` |
| Change stage | `hack_set_event_stage` |
| Public event data for a custom page | `hack_get_public_event` |
| Event website | `hack_get_event_website`, `hack_publish_event_website`, `hack_set_event_website_mode` |
| Event icon | `hack_set_event_icon`, `hack_clear_event_icon` |
| Event website storage | `hack_event_storage_*`, `hack_get_page_recipe` |
| Event website notifications | `list_notifications`, `remove_notification` with `event` |
| Content and schedule | `hack_get_content`, `hack_set_content` |
| Announcements | `hack_get_announcements`, `hack_post_announcement`, `hack_edit_announcement`, `hack_delete_announcement` |
| Message teams (emails them; ask first) | `hack_message_team`, `hack_get_team_messages` |
| Reminder times | `hack_get_reminder_settings`, `hack_set_reminder_settings` |
| Registration | `hack_get_registration`, `hack_set_registration` |
| Applications | `hack_get_applications`, `hack_decide_application` |
| Tracks | `hack_get_tracks`, `hack_set_tracks` |
| Voting | `hack_get_voting_settings`, `hack_set_voting_settings` |
| Directory listing | `hack_get_directory`, `hack_set_directory_listing` |
| New join or judge code | `hack_regenerate_code` |
| Entry rules | `hack_get_entry_rules`, `hack_set_entry_rules` |
| Exclude or include an entry | `hack_set_entry_excluded` |
| People | `hack_get_people`, `hack_remove_person`, `hack_revoke_person_key` |
| Teams | `hack_get_teams`, `hack_move_member`, `hack_remove_team_member`, `hack_rename_team`, `hack_lock_team`, `hack_set_team_admin`, `hack_new_team_link`, `hack_fill_teams`, `hack_delete_team`, `hack_set_team_deadline` |
| Team sites | `hack_take_down_team_site`, `hack_restore_team_site` |
| Organisers | `hack_create_organiser_invite`, `hack_revoke_organiser_invite`, `hack_preview_organiser_invite`, `hack_accept_organiser_invite`, `hack_remove_organiser` |
| Judging | `hack_get_judging_settings`, `hack_set_judging_settings`, `hack_get_rubric`, `hack_set_rubric`, `hack_generate_assignments`, `hack_get_assignments`, `hack_set_assignments`, `hack_get_panels`, `hack_set_panels`, `hack_preview_judging_assignments`, `hack_get_judging_dashboard` |
| Judge conflicts | `hack_get_conflicts`, `hack_set_judge_conflict`, `hack_remove_judge_conflict` |
| Results | `hack_publish_results`, `hack_set_results_view`, `hack_get_public_results` |
| Exports | `hack_export_participants`, `hack_export_teams`, `hack_export_entries`, `hack_export_scores`, `hack_export_results`, `hack_export_projects_archive` |
| Usage | `hack_get_usage` |
| Leave; delete | `hack_preview_leave_event`, `hack_leave_event`, `hack_delete_former_judge_scores`, `hack_delete_event` |

Notes:

- `hack_get_teams` omits member emails unless `include_emails: true`; set it only when the organiser asks. `hack_get_people` and `hack_export_participants` include emails.
- Turning approval off: ask whether to admit pending join-link sign-ups and send `admit_pending: true` with `approval_required: false`. `admit_pending` never admits applications.
- Narrowing voting eligibility: inspect `votes_to_remove`, ask, then send `confirm_vote_removal: true`.
- `hack_fill_teams`: `body.apply:false` to preview, confirm, then `body.apply:true`; `body.size` is people per team. It skips locked teams.
- `hack_post_announcement` with `email_participants: true` emails approved participants whose username is an email address. Editing an announcement sends no new email. One announcement can be pinned (`body.pinned`).
- Private archive links and exports go only to the organiser. A new join or judge code invalidates the old one.
