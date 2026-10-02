# Organiser connector and REST operations

Base URL for REST: `https://simple-hack.app`. Authenticated calls use the person's
simple-hack.app key in `X-API-Key` and `X-Skill-Version: 0.27.12`. Connector
calls use the signed-in person's grant. One connection covers all event roles.

For many organiser writes, the tool input is `slug` plus `body`, where `body`
is exactly the JSON object the REST route accepts. Read a tool's input schema
and current state before sending it. Values in `{braces}` are path arguments;
use IDs and slugs returned by the API, not guesses. The REST handler enforces
the same permission and validation rules on both paths. Public reads marked
public below need no REST key.

| Action | Connector tool | REST method and path |
|---|---|---|
| List my events and roles | `hack_list_events` | `GET /v1/hack/events` |
| Check event name | `hack_check_event_name` | `GET /v1/hack/names/{slug}` |
| Create draft | `hack_create_event` | `POST /v1/hack/events` |
| Read event and organiser links | `hack_get_event` | `GET /v1/hack/events/{slug}` |
| Edit event settings and text | `hack_update_event` | `PATCH /v1/hack/events/{slug}` |
| Change event stage | `hack_set_event_stage` | `POST /v1/hack/events/{slug}/stage` |
| Read or replace rubric | `hack_get_rubric`, `hack_set_rubric` | `GET`, `PUT /v1/hack/events/{slug}/rubric` |
| Export scores or results CSV | `hack_export_scores`, `hack_export_results` | `GET /v1/hack/events/{slug}/export/scores.csv`, `GET …/results.csv` |
| Get directory | `hack_get_directory` | `GET /v1/hack/directory` |
| Get content | `hack_get_content` | `GET /v1/hack/events/{slug}/content` |
| Set content | `hack_set_content` | `PUT /v1/hack/events/{slug}/content` |
| Get announcements | `hack_get_announcements` | `GET /v1/hack/events/{slug}/announcements` |
| Post announcement | `hack_post_announcement` | `POST /v1/hack/events/{slug}/announcements` |
| Get registration | `hack_get_registration` | `GET /v1/hack/events/{slug}/registration` |
| Set registration | `hack_set_registration` | `PUT /v1/hack/events/{slug}/registration` |
| Get applications | `hack_get_applications` | `GET /v1/hack/events/{slug}/applications` |
| Decide application | `hack_decide_application` | `POST /v1/hack/events/{slug}/applications/{user_id}/decision` |
| Get tracks | `hack_get_tracks` | `GET /v1/hack/events/{slug}/tracks` |
| Set tracks | `hack_set_tracks` | `PUT /v1/hack/events/{slug}/tracks` |
| Get voting settings | `hack_get_voting_settings` | `GET /v1/hack/events/{slug}/voting` |
| Set voting settings | `hack_set_voting_settings` | `PUT /v1/hack/events/{slug}/voting` |
| Set directory listing | `hack_set_directory_listing` | `PATCH /v1/hack/events/{slug}/directory` |
| Regenerate code | `hack_regenerate_code` | `POST /v1/hack/events/{slug}/codes/{kind}` |
| Delete event | `hack_delete_event` | `DELETE /v1/hack/events/{slug}` |
| Get people | `hack_get_people` | `GET /v1/hack/events/{slug}/people` |
| Remove person | `hack_remove_person` | `DELETE /v1/hack/events/{slug}/people/{user_id}` |
| Revoke person key | `hack_revoke_person_key` | `DELETE /v1/hack/events/{slug}/people/{user_id}/key` |
| Get teams | `hack_get_teams` | `GET /v1/hack/events/{slug}/teams` |
| Move member | `hack_move_member` | `POST /v1/hack/events/{slug}/teams/{team}/members` |
| Remove team member | `hack_remove_team_member` | `DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}` |
| Rename team | `hack_rename_team` | `PATCH /v1/hack/events/{slug}/teams/{team}` |
| Delete team | `hack_delete_team` | `DELETE /v1/hack/events/{slug}/teams/{team}` |
| Set team deadline | `hack_set_team_deadline` | `PUT /v1/hack/events/{slug}/teams/{team}/deadline` |
| Take down team site | `hack_take_down_team_site` | `POST /v1/hack/events/{slug}/teams/{team}/takedown` |
| Restore team site | `hack_restore_team_site` | `POST /v1/hack/events/{slug}/teams/{team}/restore` |
| Create organiser invite | `hack_create_organiser_invite` | `POST /v1/hack/events/{slug}/organiser-invite` |
| Revoke organiser invite | `hack_revoke_organiser_invite` | `DELETE /v1/hack/events/{slug}/organiser-invite` |
| Preview organiser invite | `hack_preview_organiser_invite` | `GET /v1/hack/organiser/{code}` |
| Accept organiser invite | `hack_accept_organiser_invite` | `POST /v1/hack/organiser/{code}` |
| Remove organiser | `hack_remove_organiser` | `DELETE /v1/hack/events/{slug}/organisers/{user_id}` |
| Get judging settings | `hack_get_judging_settings` | `GET /v1/hack/events/{slug}/judging/settings` |
| Set judging settings | `hack_set_judging_settings` | `PATCH /v1/hack/events/{slug}/judging/settings` |
| Generate assignments | `hack_generate_assignments` | `POST /v1/hack/events/{slug}/assignments/generate` |
| Get assignments | `hack_get_assignments` | `GET /v1/hack/events/{slug}/assignments` |
| Set assignments | `hack_set_assignments` | `PUT /v1/hack/events/{slug}/assignments` |
| Get panels | `hack_get_panels` | `GET /v1/hack/events/{slug}/judging/panels` |
| Set panels | `hack_set_panels` | `PUT /v1/hack/events/{slug}/judging/panels` |
| Preview judging assignments | `hack_preview_judging_assignments` | `GET /v1/hack/events/{slug}/judging/preview` |
| Get conflicts | `hack_get_conflicts` | `GET /v1/hack/events/{slug}/conflicts` |
| Set judge conflict | `hack_set_judge_conflict` | `POST /v1/hack/events/{slug}/conflicts` |
| Remove judge conflict | `hack_remove_judge_conflict` | `DELETE /v1/hack/events/{slug}/conflicts/{team_id}` |
| Get judging dashboard | `hack_get_judging_dashboard` | `GET /v1/hack/events/{slug}/judging/dashboard` |
| Lock judging | `hack_lock_judging` | `POST /v1/hack/events/{slug}/judging/lock` |
| Unlock judging | `hack_unlock_judging` | `POST /v1/hack/events/{slug}/judging/unlock` |
| Publish results | `hack_publish_results` | `POST /v1/hack/events/{slug}/results/publish` |
| Set results view | `hack_set_results_view` | `PATCH /v1/hack/events/{slug}/results` |
| Get public results | `hack_get_public_results` | `GET /v1/hack/events/{slug}/results` |
| Export participants | `hack_export_participants` | `GET /v1/hack/events/{slug}/export/participants.csv` |
| Export teams | `hack_export_teams` | `GET /v1/hack/events/{slug}/export/teams.csv` |
| Export entries | `hack_export_entries` | `GET /v1/hack/events/{slug}/export/entries.csv` |
| Make a private project archive link | `hack_export_projects_archive` | `POST /v1/hack/events/{slug}/export/projects-link` |
| Get usage | `hack_get_usage` | `GET /v1/hack/events/{slug}/usage` |

The public directory and public results reads are available without an account;
private drafts and organiser details remain role-gated. CSV exports can be
large; use the management page when a file download is easier. The archive
tool returns a private link with an `expires_at` time. Give it only to the
organiser; downloading it checks their current event membership again.

For a conflict involving another judge, pass that judge's `judge_user_id`
where the tool or REST query accepts it. Omit the ID for the organiser's own
conflict. Rename changes a team's display name; keep using the returned team
slug and site URL.

Before changing a public stage, result, listing, site or invitation link,
posting an email announcement, removing access, deleting, or replacing a scored
rubric, explain the effect and ask the organiser. A new join or judge link
immediately invalidates its predecessor. Replacing a scored rubric clears
existing scores and comments. Ending an event is final. Published team sites
remain temporarily after ending according to the hosted retention settings;
check the returned event for its current dates.

Event entry and team-site publishing belong to participants. An organiser who
judges may use the same judge queue and scoring tools as a judge. A site
publishing call is always limited to the connected person's current team,
selected with `hack_select_team(team_id)` where needed.
