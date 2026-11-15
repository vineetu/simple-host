# App screens

## hack_app_preview_results

<div data-tool="hack_app_preview_results"></div>

App-only companion of hack_preview_judging_assignments (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_preview_judging_assignments instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_results

<div data-tool="hack_app_get_results"></div>

App-only companion of hack_get_public_results (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_public_results instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_publish_results

<div data-tool="hack_app_publish_results"></div>

App-only companion of hack_publish_results (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_publish_results instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| body | object | yes | rank_overrides [{team_id,rank}], or an empty list for computed ranks. The REST route validates each field and returns its own errors. |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_people

<div data-tool="hack_app_get_people"></div>

App-only companion of hack_get_people (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_people instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_applications

<div data-tool="hack_app_get_applications"></div>

App-only companion of hack_get_applications (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_applications instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_registration

<div data-tool="hack_app_get_registration"></div>

App-only companion of hack_get_registration (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_registration instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_judging_settings

<div data-tool="hack_app_get_judging_settings"></div>

App-only companion of hack_get_judging_settings (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judging_settings instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_conflicts

<div data-tool="hack_app_get_conflicts"></div>

App-only companion of hack_get_conflicts (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_conflicts instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_teams

<div data-tool="hack_app_get_teams"></div>

App-only companion of hack_get_teams (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_teams instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_emails | boolean |  | Set true only when the organiser asks for team members' email addresses. |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_assignments

<div data-tool="hack_app_get_assignments"></div>

App-only companion of hack_get_assignments (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_assignments instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_judging_dashboard

<div data-tool="hack_app_get_judging_dashboard"></div>

App-only companion of hack_get_judging_dashboard (SIM-344): the Judging view calls this on its own, every time it opens. Returns a fixed summary and counts; the real coverage data (team and judge names, former judges) goes to the view only, never to the model. Use hack_get_judging_dashboard instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_app_set_event_stage

<div data-tool="hack_app_set_event_stage"></div>

App-only companion of hack_set_event_stage (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_set_event_stage instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |
| stage | string, one of `draft`, `open`, `building`, `closed`, `judging`, `results`, `archived` | yes | The stage to set. archived ends the event. |

## hack_app_regenerate_code

<div data-tool="hack_app_regenerate_code"></div>

App-only companion of hack_regenerate_code (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_regenerate_code instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| kind | string | yes | REST path parameter: kind. |
| slug | string | yes | REST path parameter: slug. |

## hack_app_create_organiser_invite

<div data-tool="hack_app_create_organiser_invite"></div>

App-only companion of hack_create_organiser_invite (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_create_organiser_invite instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_set_judging_ends_at

<div data-tool="hack_app_set_judging_ends_at"></div>

App-only companion of hack_update_event (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_update_event instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| judging_ends_at | string |  | When judging closes, in the event time zone. Empty clears it. Judges with unscored teams are reminded before this time, once set; with no judging_ends_at they get no reminder. |
| slug | string | yes | The event name. |

## hack_app_get_rubric

<div data-tool="hack_app_get_rubric"></div>

App-only companion of hack_get_rubric (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_rubric instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_app_get_judge_queue

<div data-tool="hack_app_get_judge_queue"></div>

App-only companion of hack_get_judge_queue (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judge_queue instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_app_get_judge_scores

<div data-tool="hack_app_get_judge_scores"></div>

App-only companion of hack_get_judge_scores (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judge_scores instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_app_get_my_conflicts

<div data-tool="hack_app_get_my_conflicts"></div>

App-only companion of hack_get_my_conflicts (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_my_conflicts instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_show_judge

<div data-tool="hack_show_judge"></div>

Open the judge app view in chat: invite (accept a judge invite, needs code), queue (My teams), score (one entry's scoring, optional team_id) or rubric. Only offered to a connection whose app can render it; every judging action also has a plain tool (hack_get_judge_queue, hack_score_team, hack_declare_my_conflict, hack_get_rubric, hack_preview_judge, hack_join_judge) that works the same everywhere.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string |  | Code from the judge invite link. Required for view invite. |
| slug | string |  | Event name. Required for view queue, score or rubric. |
| team_id | string |  | Team ID from the judge queue. Optional for view score: opens that team directly instead of the first unscored one. |
| view | string, one of `invite`, `queue`, `score`, `rubric` | yes | Which judge view to open. |

## hack_show_organiser

<div data-tool="hack_show_organiser"></div>

Open the organiser app view in chat: overview (next step, snapshot, stage), people (applications and the member list) or judging (assignments, progress and former judges; rubric in website Settings). Only offered to a connection whose app can render it; every organiser action also has a plain tool (hack_get_event, hack_set_event_stage, hack_get_applications, hack_decide_application, hack_get_judging_dashboard, hack_set_event_stage, ...) that works the same everywhere.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| view | string, one of `overview`, `people`, `judging` | yes | Which organiser view to open. |
