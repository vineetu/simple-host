# App screens

## hack_app_get_people

<div data-tool="hack_app_get_people"></div>

**App: load people**

App-only companion of hack_get_people (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_people instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_applications

<div data-tool="hack_app_get_applications"></div>

**App: load applications**

App-only companion of hack_get_applications (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_applications instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_registration

<div data-tool="hack_app_get_registration"></div>

**App: load sign-up settings**

App-only companion of hack_get_registration (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_registration instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_judging_settings

<div data-tool="hack_app_get_judging_settings"></div>

**App: load judging settings**

App-only companion of hack_get_judging_settings (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judging_settings instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_conflicts

<div data-tool="hack_app_get_conflicts"></div>

**App: load conflicts**

App-only companion of hack_get_conflicts (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_conflicts instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_teams

<div data-tool="hack_app_get_teams"></div>

**App: load teams**

App-only companion of hack_get_teams (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_teams instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_emails | boolean |  | Set true only when the organiser asks for team members' email addresses. |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_assignments

<div data-tool="hack_app_get_assignments"></div>

**App: load assignments**

App-only companion of hack_get_assignments (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_assignments instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_get_judging_dashboard

<div data-tool="hack_app_get_judging_dashboard"></div>

**App: load judging progress**

App-only companion of hack_get_judging_dashboard (SIM-344): the Judging view calls this on its own, every time it opens. Returns a fixed summary and counts; the real coverage data (team and judge names, former judges) goes to the view only, never to the model. Use hack_get_judging_dashboard instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_app_set_event_stage

<div data-tool="hack_app_set_event_stage"></div>

**App: change event stage**

App-only companion of hack_set_event_stage (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_set_event_stage instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |
| stage | string | yes | The stage to set. archived ends the event. |

## hack_app_regenerate_code

<div data-tool="hack_app_regenerate_code"></div>

**App: make a new join or judge link**

App-only companion of hack_regenerate_code (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_regenerate_code instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| kind | string | yes | REST path parameter: kind. |
| slug | string | yes | REST path parameter: slug. |

## hack_app_create_organiser_invite

<div data-tool="hack_app_create_organiser_invite"></div>

**App: create a co-organiser invitation**

App-only companion of hack_create_organiser_invite (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_create_organiser_invite instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | REST path parameter: slug. |

## hack_app_set_judging_ends_at

<div data-tool="hack_app_set_judging_ends_at"></div>

**App: set when judging closes**

App-only companion of hack_update_event (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_update_event instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| judging_ends_at | string |  | When judging closes, in the event time zone. Empty clears it. Judges with unscored teams are reminded before this time, once set; with no judging_ends_at they get no reminder. |
| slug | string | yes | The event name. |

## hack_app_get_rubric

<div data-tool="hack_app_get_rubric"></div>

**App: load the rubric**

App-only companion of hack_get_rubric (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_rubric instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | The event name. |

## hack_app_get_judge_queue

<div data-tool="hack_app_get_judge_queue"></div>

**App: load the judging queue**

App-only companion of hack_get_judge_queue (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judge_queue instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_app_get_judge_scores

<div data-tool="hack_app_get_judge_scores"></div>

**App: load my scores for a team**

App-only companion of hack_get_judge_scores (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_judge_scores instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| team_id | string | yes | Team ID from the judge queue. |

## hack_app_get_my_conflicts

<div data-tool="hack_app_get_my_conflicts"></div>

**App: load my conflicts**

App-only companion of hack_get_my_conflicts (SIM-344): the view calls this on its own, every time a screen opens, with nobody having asked for this data in chat. Returns a fixed summary and small counts; the real data goes to the view only, never to the model. Use hack_get_my_conflicts instead for an explicit chat request.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_show_judge

<div data-tool="hack_show_judge"></div>

**Show the judge view**

Open the judge app view in chat: invite (accept a judge invite, needs code), queue (My teams), score (one entry's scoring, optional team_id) or rubric. Only offered to a connection whose app can render it; every judging action also has a plain tool (hack_get_judge_queue, hack_score_team, hack_declare_my_conflict, hack_get_rubric, hack_preview_judge, hack_join_judge) that works the same everywhere.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| code | string |  | Code from the judge invite link. Required for view invite. |
| slug | string |  | Event name. Required for view queue, score or rubric. |
| team_id | string |  | Team ID from the judge queue. Optional for view score: opens that team directly instead of the first unscored one. |
| view | string | yes | Which judge view to open. |

## hack_show_organiser

<div data-tool="hack_show_organiser"></div>

**Show the organiser view**

Open the organiser app view in chat: overview (next step, snapshot, stage), people (applications and the member list) or judging (assignments, progress and former judges; rubric in website Settings). Only offered to a connection whose app can render it; every organiser action also has a plain tool (hack_get_event, hack_set_event_stage, hack_get_applications, hack_decide_application, hack_get_judging_dashboard, hack_lock_judging, ...) that works the same everywhere.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |
| view | string | yes | Which organiser view to open. |
