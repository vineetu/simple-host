# Entries, judging and results

## hack_get_rubric

<div data-tool="hack_get_rubric"></div>

Read an event's judging rubric: each criterion's name, description, weight and maximum points, never scores. Organiser, judge, or an approved participant reading their own event's rubric.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_set_rubric

<div data-tool="hack_set_rubric"></div>

Save the whole rubric. Keep existing criterion IDs to preserve scores. Removed criteria and scores above a reduced max_points are deleted only after confirmation. On score_deletion_confirmation explain the counts and ask before retrying with confirm_score_deletion. 1 to 10 criteria. Each weight is a whole number from 0 to 100 and the weights add up to 100. max_points is a whole number from 1 to 10. Organiser only. Refused while judging is locked.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm_score_deletion | boolean |  | Set true only after the person confirms the reported score deletions. |
| criteria | array of object | yes | The criteria, in display order. This list replaces the current rubric. |
| slug | string | yes | The event name. |

## hack_export_scores

<div data-tool="hack_export_scores"></div>

Download the event's scores as CSV (team, judge, criterion, points, max points, comment). Organiser only. Give the file to the person; do not post it publicly.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_export_results

<div data-tool="hack_export_results"></div>

Download the event's results as CSV (team, total, rank, tied, judges scored). Uses the published results when there are some, otherwise the current totals. Organiser only. Give the file to the person; do not post it publicly.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_get_entry

<div data-tool="hack_get_entry"></div>

Read the entry, the event's entry rules, completeness, submit status, the Home progress checklist and entry deadline.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_update_entry

<div data-tool="hack_update_entry"></div>

Set only the entry fields supplied. Empty strings clear fields. tagline is the one-line solution shown on the summary card; description is the longer write-up. Saving a draft never checks or changes completeness and never submits it -- call hack_submit_entry when it's ready. tools lists what was used, up to 10 items, each 1-30 characters, case-insensitive duplicates dropped. maturity is how far along it is: working, partial, concept, or empty to clear. track sets the team's chosen track by its slug, or clears it with an empty string. A save on an already-submitted entry that would make it incomplete is refused with `missing`. Ask before overwriting team text.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| audience | string |  | Who it's for, up to 100 characters. |
| code_url | string |  | Code URL. |
| description | string |  | Project description. |
| maturity | string, one of ``, `working`, `partial`, `concept` |  | How far along it is, or empty to clear. |
| problem | string |  | The problem it solves, up to 140 characters. |
| slug | string | yes | Event name. |
| tagline | string |  | Short summary: the one-line solution. |
| title | string |  | Project title. |
| tools | array of string |  | What was used: up to 10 items, each 1 to 30 characters. |
| track | string |  | Track slug to set the team's chosen track, or empty to clear it. |
| updated_at | string |  | updated_at from the entry you read, or empty for a new entry. A conflict returns the newer entry; ask before retrying. |
| video_url | string |  | Video URL. |

## hack_submit_entry

<div data-tool="hack_submit_entry"></div>

Move the entry from draft to submitted. Refused with `missing` (422 entry_incomplete, "Add these first: ...") when a required field or the show-your-work evidence is missing. Idempotent: submitting an already-submitted entry just keeps it submitted. A complete draft is also auto-submitted on its own at the deadline; an incomplete one stays a draft, is still judged, and is flagged Incomplete -- organisers can exclude it by hand with hack_set_entry_excluded.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| updated_at | string |  | updated_at from the entry you read, optional. A conflict (a teammate changed it) returns the newer entry; ask before retrying. |

## hack_unsubmit_entry

<div data-tool="hack_unsubmit_entry"></div>

Put a submitted entry back to draft. Works only before the deadline; refused once it has passed. Reversible: call hack_submit_entry again anytime before the deadline.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_delete_entry_screenshot

<div data-tool="hack_delete_entry_screenshot"></div>

Remove the team's entry screenshot. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_voting

<div data-tool="hack_get_voting"></div>

Read voting status, eligible teams and published vote ranking when available.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_my_vote

<div data-tool="hack_get_my_vote"></div>

Read this person's current vote.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_vote

<div data-tool="hack_vote"></div>

Vote for an eligible team. This can replace the person's previous vote. Confirm their choice first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team | string | yes | Team slug from voting options. |

## hack_get_my_results

<div data-tool="hack_get_my_results"></div>

Read this participant's published result and judge comments.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_judge_queue

<div data-tool="hack_get_judge_queue"></div>

List eligible entries, entry, pinned website link, conflicts and scoring status. Records any due submission deadline pins.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_get_judge_scores

<div data-tool="hack_get_judge_scores"></div>

Read this judge's scores and comment for one queue team.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_score_team

<div data-tool="hack_score_team"></div>

Save one or more criterion scores and optionally a comment. Confirm the scores with the judge before saving.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| comment | string |  | Judge comment; an empty string clears it. |
| scores | array of object |  | One entry per criterion being scored: its criterion_id from the rubric and the points. |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_get_my_conflicts

<div data-tool="hack_get_my_conflicts"></div>

List this judge's declared conflicts.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_declare_my_conflict

<div data-tool="hack_declare_my_conflict"></div>

Declare that this judge has a conflict with a team; scores for it will be unavailable. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_remove_my_conflict

<div data-tool="hack_remove_my_conflict"></div>

Remove this judge's conflict with a team. Confirm first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_get_entry_screenshot

<div data-tool="hack_get_entry_screenshot"></div>

Read my team's screenshot

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_set_entry_screenshot

<div data-tool="hack_set_entry_screenshot"></div>

Upload my team's screenshot. Ask the person before replacing the current image.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| base64 | string | yes | Base64 encoded PNG, JPEG or WebP, at most 2 MB decoded. |
| slug | string | yes | Event name. |

## hack_get_voting_settings

<div data-tool="hack_get_voting_settings"></div>

Read event voting settings. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_voting_settings

<div data-tool="hack_set_voting_settings"></div>

Replace voting settings in the REST body. Narrowing eligibility removes earlier ineligible votes and returns votes_removed. Check votes_to_remove in hack_get_voting_settings and ask before removing votes; send confirm_vote_removal: true after confirmation or receive 409 vote_removal_confirmation with the count. Opening voting is public; ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | enabled, opens_at, closes_at, eligibility (all_signed_in, event_members or participants). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_entries

<div data-tool="hack_get_entries"></div>

Read all team entries and pinned website links, plus counts by status (submitted, drafts, not_started, not_submitted, sites_live). Records any due submission deadline pins. Organiser or judge.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_get_entry_rules

<div data-tool="hack_get_entry_rules"></div>

Read which entry fields are required, optional, off or one of (for the show-your-work evidence: video, website, code or screenshot), the active preset (video_or_website, video_website_code, or a legacy build/code/ideas/custom an older event may still carry), which evidence judges see first (judge_first: video or site), the one-sentence rules summary participants see, and whether rules can still change (editable, false from Closed on). Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_entry_rules

<div data-tool="hack_set_entry_rules"></div>

Choose a preset -- video_or_website (the default: at least one of a video or a website) or video_website_code (video, website and code link all required) -- and/or set individual fields to required, optional, off, or one_of (video_url, site, code_url and screenshot only; exactly one such field needs to be met). judge_first picks which evidence (video or site) judges see first. Setting a field that differs from the active preset's own table demotes the stored preset to custom. An event already on an older preset (build, code, ideas or custom) keeps it and its saved values until you set one of the two current presets or change a field; preset build, code, ideas or custom is refused (400 invalid_preset) -- it names the two you can choose. dry_run:true returns the impact (how many submitted entries would newly read as missing something, how many drafts would be newly blocked, and which teams) and a preview, without saving anything. Tightening never un-submits an already-submitted entry. Refused once entries have closed (Closed stage onward): rules are fixed so judging sees one definition of complete. On a new event this is also the organiser's first move (next_step.kind entry_rules on hack_get_event): ask which preset before opening sign-up, even if they want the default. Ask before narrowing rules on a live event. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | Any subset: preset (video_or_website or video_website_code), fields (map of field name to required/optional/off/one_of -- one_of only on video_url, site, code_url, screenshot), judge_first (video or site), dry_run boolean (true previews impact and the resulting rules, without saving). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_team_screenshot

<div data-tool="hack_get_team_screenshot"></div>

Read one team's entry screenshot as base64 image bytes. Organiser or judge.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |
| team | string | yes | REST path parameter: team. |

## hack_get_judging_settings

<div data-tool="hack_get_judging_settings"></div>

Read assignment and scoring settings. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_judging_settings

<div data-tool="hack_set_judging_settings"></div>

Change assignment_mode, judges_per_team, score_mode, tie_criterion_id or public visibility flags. Ask before changing public flags. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | Any changed field: assignment_mode, judges_per_team, score_mode, tie_criterion_id, public_scores, public_ranks. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_generate_assignments

<div data-tool="hack_generate_assignments"></div>

Generate assignments from current settings. Judges only unless body.include_organisers is true. Replaces previous automatic assignments; ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | Optional include_organisers boolean, default false. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_assignments

<div data-tool="hack_get_assignments"></div>

Read manual assignments. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_assignments

<div data-tool="hack_set_assignments"></div>

Replace manual assignments with body.assignments. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | assignments [{judge_id,team_id}], replacing the whole manual list. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_panels

<div data-tool="hack_get_panels"></div>

Read track judging panels. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_panels

<div data-tool="hack_set_panels"></div>

Replace track panel memberships with body.panels. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | panels [{track_id,judge_id}], replacing the whole panel list. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_preview_judging_assignments

<div data-tool="hack_preview_judging_assignments"></div>

Preview assignment coverage without changing it. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_get_conflicts

<div data-tool="hack_get_conflicts"></div>

Read declared judge/team conflicts. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_judge_conflict

<div data-tool="hack_set_judge_conflict"></div>

Record a conflict for any event judge using body.team_id and body.judge_user_id. Organisers may also omit judge_user_id to record their own conflict. Existing scores for that pairing leave current totals; ask first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | team_id and optional judge_user_id (omit for your own conflict). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_remove_judge_conflict

<div data-tool="hack_remove_judge_conflict"></div>

Remove a conflict; use query judge_user_id to choose another judge, or omit it for yourself. Ask before removing. Organiser only for another judge.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| judge_user_id | string |  | Optional judge user ID. Omit to remove your own conflict. |
| slug | string | yes | REST path parameter: slug. |
| team_id | string | yes | REST path parameter: team_id. |

## hack_get_judging_dashboard

<div data-tool="hack_get_judging_dashboard"></div>

Read judging coverage and totals, plus former_judges (people who left after scoring: their counted_teams still count toward totals until deleted). Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_delete_former_judge_scores

<div data-tool="hack_delete_former_judge_scores"></div>

Delete the scores and comments of a judge who left the event, so they stop counting. user_id from former_judges in hack_get_judging_dashboard. confirm is the event's address name, sent only after the person agreed. Refused while judging is locked. Published results change only when results are published again. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | confirm: the exact event address name (slug), supplied only after the person confirms deletion. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |
| user_id | string | yes | REST path parameter: user_id. |

## hack_lock_judging

<div data-tool="hack_lock_judging"></div>

Freeze scores and comments. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_unlock_judging

<div data-tool="hack_unlock_judging"></div>

Reopen scoring with body.reason. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | reason explaining why scoring is reopened. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_publish_results

<div data-tool="hack_publish_results"></div>

Publish results using body.rank_overrides for any tie breaks. This replaces the previous public snapshot: show the preview and ask first. Refused before entries close (409 judging_not_started). From Closed or Judging it moves the event to the Results stage and locks judging first, the same as Finish judging, so the stage and the public page never keep saying Judging once winners are out. Safe to call again after the event has ended, to fix a result (for example a disqualified team); it never reopens an ended event. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | rank_overrides [{team_id,rank}], or an empty list for computed ranks. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_set_results_view

<div data-tool="hack_set_results_view"></div>

Set body.full_ranking to show all ranks or winners only. Ask first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | full_ranking boolean (true for every rank, false for winners only). The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_get_public_results

<div data-tool="hack_get_public_results"></div>

Read published results. Available publicly after publication.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_export_entries

<div data-tool="hack_export_entries"></div>

Read the entries CSV. Organiser only; keep private.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_set_entry_excluded

<div data-tool="hack_set_entry_excluded"></div>

Hand-exclude a team's entry from judging, or put it back in. Organisers typically do this for an entry flagged Incomplete at the deadline (coordinator decision 2026-11-02: an incomplete draft is still judged by default, and organisers can exclude it by hand instead; the entry and its scores are unaffected, only judging eligibility changes). Ask the person first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| excluded | boolean | yes | true excludes the entry from judging; false includes it again. |
| slug | string | yes | The event name. |
| team | string | yes | Team slug. |
