---
name: run-hackathon
description: Run a hackathon on simple-hack.app (hosted; sign in, create an event, registration, teams, judging, results), or stand up a private Simple Host instance on the organiser's own cloud account. Use when someone wants to run a hackathon, a class project showcase, an internal build day, or any event where people publish what they make. Hosted is the path to take unless they ask to run their own server. Drives sign-in, event content, join and judge links, tracks, announcements, voting, judging and archive; the self-host path is provider, server, DNS, install, accounts, teardown.
---

# Run a hackathon

**Reading this over the web?** The reference documents are served under `/v1/`,
not next to this file. Every mention below carries its full address for that
reason; a relative path only resolves in an installed copy.

There are two ways. **Hosted on simple-hack.app comes first.** Anyone who signs
in can create an event there, free, with no server to stand up. Self-hosting,
a private instance on the organiser's own cloud account, is the second path
and starts at "Self-host: a private instance" below. Take the hosted path
unless the organiser asks to run their own server.

**Ask before anything that goes public or cannot be undone.** On the hosted
path that is: opening the event (the page becomes joinable), the first time a
team site goes online, publishing results, making a new join or judge link
(the old one stops), taking a team site down, replacing the rubric after
anyone has scored, ending the event, and deleting it. Say what you are about
to do and wait for a yes. Creating the event they asked for in this
conversation, and edits they asked for, go ahead.

On direct API calls to simple-hack.app send `X-API-Key` and
`X-Skill-Version: 0.27.10`. A simple-host.app key does not work here, and a
simple-hack.app key does not work on simple-host.app. They are different
servers with different accounts.

The four reference files (`dns.md`, `install.md`, `providers.md`, `teardown.md`)
are the self-host path only.

## Hosted on simple-hack.app

Three roles, one per person per event, checked on the server: **organiser**,
**participant**, **judge**. A person who is not in the event, or who calls a
route for another role, gets 404 `event_not_found`. The platform admin can
read an event (`admin_view: true`) and cannot join or change it.

The API is `https://simple-hack.app`. The person's events are
`https://simple-hack.app/events`. An event is managed at
`https://simple-hack.app/e/<slug>/manage` (the Judging tab is there) and the
public page is `https://<slug>.simple-hack.app/`. Hand people those pages;
use the organiser connector when connected, or the direct API below.

Accounts on simple-hack.app own no personal sites. A site belongs to a team.
Publishing anywhere else answers 403 `no_personal_sites`. The account's
address is a random `u-<hex>` handle the person cannot choose or change, and
the Account page (`https://simple-hack.app/account`) never shows it. That page
sets the name other people see, the sign-in email, Google, sign-in alerts,
sign out everywhere, and deleting the account.

### Organiser connector

In a chat app, add `https://simple-hack.app/mcp`, sign in in the browser and
choose **Manage my events**. You can connect before creating an event. Never
ask for an API key when this connection is available. A connection that chose
a team publishes that team's site; reconnect with Manage my events to manage
events instead.

| Task | Tool |
|---|---|
| Find the person's events and roles | `hack_list_events` |
| Check a name before creating | `hack_check_event_name` |
| Create a draft | `hack_create_event` |
| Read the event and organiser's join/judge links | `hack_get_event` |
| Edit page text, dates and settings | `hack_update_event` |
| Open, close or end the event | `hack_set_event_stage` |
| Read or replace judging criteria | `hack_get_rubric`, `hack_set_rubric` |
| Export judging CSV files | `hack_export_scores`, `hack_export_results` |

The same event roles and permissions apply. Only an organiser can change an
event. Replacing the rubric also clears its existing scores and comments;
explain that before replacing a scored rubric. Ending an event is final.
For operations the connector does not offer, use the event's management page
or the API with a separately authorised key. Do not treat an MCP token as a
REST token. A CSV response marked `truncated` is incomplete; download the
full export from the management page.

### Sign in with a key (API agents)

Skip this when you already hold this person's simple-hack.app key. Do not
reuse a key from `~/.website-deploy/config.json`: that file is simple-host.app.

You never open their email. They read the code and paste it.

```
POST https://simple-hack.app/v1/auth
Content-Type: application/json
{"email": "<them@example.com>"}
```

`202` means a 6-digit code is on its way. Then:

```
POST https://simple-hack.app/v1/auth/verify
Content-Type: application/json
{"email": "<them@example.com>", "code": "<6 digits>", "name": "<where this key came from>"}
```

Do not send `choose_handle` or `handle`. On this server a new account is
created with its random handle; there is no address to pick. Success returns
`api_key` (it starts with `shk_`), shown only here. Keep it, and do not print
it into a transcript, a log, or a committed file. `name` labels the key on
their Account page.

A wrong or expired code is 401. Three wrong codes burn it: start again from
`POST /v1/auth`. They can also sign in with Google at
`https://simple-hack.app/signin`.

`GET https://simple-hack.app/v1/hack/events` lists the events this key is in,
each with `role`, `stage`, `url` and `manage_url`.

### Organiser

#### 1. Create the event

Check the address first. The slug is 3 to 39 lowercase letters, digits or
hyphens, not starting or ending with a hyphen.

```
GET https://simple-hack.app/v1/hack/names/<slug>
X-API-Key: <their key>
```

`available: true` comes with `address` (`https://<slug>.simple-hack.app/`).
`name_taken` and `name_reserved` mean ask for another name. `503`
`name_check_unavailable` means try again; do not create anyway.

```
POST https://simple-hack.app/v1/hack/events
X-API-Key: <their key>
Content-Type: application/json
{
  "slug": "glasgow-2026",
  "title": "Glasgow Hack 2026",
  "organiser_name": "Ada Lovelace",
  "organisation": "Computing Club",
  "contact_email": "ada@example.com",
  "purpose": "A weekend for students to ship a small site.",
  "expected_participants": 80,
  "starts_at": "2026-11-14",
  "ends_at": "2026-11-15",
  "time_zone": "Europe/London"
}
```

Required: `slug`, `title`, `organiser_name`, `contact_email`, `purpose`,
`expected_participants` (1 to 100000), `starts_at`. `organisation` may be
empty. `time_zone` defaults to UTC. Times are RFC 3339, or `YYYY-MM-DD` or
`YYYY-MM-DDTHH:MM` in the event's time zone.

`201` is the organiser's view of the event. It starts as `draft`. The same
view comes back from `GET /v1/hack/events/<slug>`. `organiser.join_url` and
`organiser.judge_url` are the links. `organiser` also has the contact
details, which the platform admin sees, and the counts.

Creating is free. There is no cap on participants, teams or judges. There is
a cap on events: 3 created per day (`429` `too_many_events_today`) and 2 that
are not yet ended (`409` `too_many_active_events`). Ending one frees that
slot. The name of an ended event stays taken, because the page stays.
`507` `instance_full` means the platform is at its disk budget.

A new event already has a rubric: Idea ("How original or useful is it?"),
Execution ("How well does it work?"), Design ("How good is the user
experience?"), Demo ("How clearly was it presented?"), weight 25 each, scored
out of 5. Leave it unless they ask for another one.

Delete with `DELETE /v1/hack/events/<slug>` only while nobody but the
organisers has joined, and not after the event has ended (`409`
`delete_only_empty`). Ask first. The name becomes free.

#### 2. The public page

`PATCH https://simple-hack.app/v1/hack/events/<slug>` with any of `title`,
`tagline`, `about`, `rules`, `prizes`, `coc_text`, `starts_at`, `ends_at`
(an empty string clears the end), `time_zone`, `team_size_max` (1 to 50),
`organiser_name`, `organisation`, `contact_email`, `purpose`,
`expected_participants`.

The code of conduct is a default text plus `coc_text`. Everyone accepts the
whole of it when they join.

Also on that PATCH, when they ask:

- `submission_deadline` — RFC 3339, or `YYYY-MM-DDTHH:MM` or `YYYY-MM-DD` in
  the event's zone. An empty string clears it. A future deadline while the
  stage is Submissions closed is `409` `submissions_closed_stage`: move back
  to Building, or give one team more time (below).
- `entry_required` — which entry fields must be filled. Any of `title`,
  `tagline`, `description`, `video_url`, `code_url`, `screenshot`. The
  default is `title`.
- `gallery_open` — `true` puts a Projects gallery of live team sites on the
  public page.

The page is server-rendered plain text at `https://<slug>.simple-hack.app/`.
While the stage is draft it is `noindex`. A taken-down event answers 410.

#### 2a. Event content and messages

The organiser's management page edits sponsors, FAQ and the schedule. With
the direct API, `GET /v1/hack/events/<slug>/content` reads it and
`PUT` replaces the three arrays together:

```json
{"sponsors":[{"name":"Example Club","tier":"Partner","url":"https://example.org"}],
 "faq":[{"question":"What should I bring?","answer":"A laptop."}],
 "schedule":[{"title":"Demos","description":"Show what you built",
              "start_at":"2026-11-15T15:00","end_at":"2026-11-15T17:00"}]}
```

Sponsor `url` must be HTTPS; `logo_data` can hold a PNG, JPEG or WebP
data URI. Schedule times entered without an offset use the event's time zone;
reads return UTC instants. The public page displays the schedule with
Now/Next labels and a submission-deadline countdown.

`GET /v1/hack/events/<slug>/announcements` lists recent posts for members.
An organiser posts with `POST` and
`{"title":"Demos start soon","body":"Meet in the main room.","email_participants":true}`.
The email flag is optional and defaults to false; the response gives
`emails_queued`, not proof of delivery. Ask before posting or emailing.
The public page also shows the announcements. Both content and announcements
remain readable after archive; edits and new posts then stop. The first
complete entry for a team queues one email receipt automatically.

#### 2b. Sign-up and tracks

`GET /v1/hack/events/<slug>/registration` reads the sign-up questions and
approval setting. An organiser can replace them with `PUT`, for example
`{"questions":[{"id":"experience","prompt":"What would you like to build?","required":true}],"approval_required":true}`.
There may be up to eight questions; answers are private to organisers.
The join page presents the questions. A participant sends answers keyed by
question id in the join request. With approval required, joining creates a
pending application rather than team access. `GET .../applications` lists
answers and status for organisers; `POST .../applications/<user_id>/decision`
with `{"decision":"approved"}` or `{"decision":"rejected"}` decides a
pending application. An organiser should review the answer before deciding.
Existing participants stay approved when this setting changes.

`GET /v1/hack/events/<slug>/tracks` lists tracks, challenges and prizes.
An organiser replaces the catalog with `PUT` and
`{"tracks":[{"slug":"climate","name":"Climate","challenge":"Build a useful climate tool","prize":"Demo slot"}]}`.
There may be up to twelve tracks. Stable slugs preserve team choices;
removing a track clears its teams' choices. The public event page shows
challenges and prizes. An approved participant chooses one for their team
with `PUT .../team/track {"track":"climate"}`, or clears it with an empty
string, before team changes and submissions close.

#### 2c. Public directory and people's-choice vote

`https://simple-hack.app/directory` groups listed events as now, upcoming
and past; `GET /v1/hack/directory` provides the same public data. Drafts
and taken-down events are omitted. An organiser can opt out, even after
archive, with `PATCH /v1/hack/events/<slug>/directory {"listed":false}`.

For a people's-choice vote, open the project gallery first. The organiser
sets `PUT /v1/hack/events/<slug>/voting` with
`{"enabled":true,"opens_at":"2026-11-15T14:00","closes_at":"2026-11-15T18:00","eligibility":"event_members"}`.
Eligibility is `all_signed_in`, `event_members`, or `participants`;
event members subject to approval must be approved. Times use the event's
time zone on input and need an opening before closing. The public
`GET .../vote` lists live project choices; counts appear only after voting
closes. The voting page is `https://simple-hack.app/e/<slug>/vote`.
A signed-in person casts or changes one vote with
`PUT .../vote {"team":"<team slug>"}`; `GET .../my-vote` reads their
choice. They cannot vote for their own team or cast the same choice twice.
The vote is counted once per canonical verified email, including aliases
with a `+tag`. An organiser cannot change voting settings after archive.
These workflows use the browser or REST API; the ten organiser MCP tools
do not include them.

#### 3. Stages

```
POST https://simple-hack.app/v1/hack/events/<slug>/stage
X-API-Key: <their key>
{"stage": "open"}
```

The server does not force an order. Use this order unless they ask otherwise.
Moving to `closed`, `judging`, `results` or `archived` from `draft`, `open`
or `building` makes the deadline now, when it was unset or still in the
future. A team that was given a later deadline keeps it.

| `stage` | On the page | What changes |
|---|---|---|
| `draft` | Draft | Just created. Nobody can join. |
| `open` | Open | People can join and form teams. Open, Building and Submissions closed request the certificate for `*.<slug>.simple-hack.app`, which team sites need. Judging and Results do not. |
| `building` | Building | Same as open: people can still join and change teams. This is the building stage. |
| `closed` | Submissions closed | The deadline becomes now. Deploys, rollbacks, entry edits and team-key changes stop (`409` `submissions_closed`). Each team's live version is pinned for the judges. |
| `judging` | Judging | Submissions stay closed. Judges can still join. |
| `results` | Results | Judges can no longer join (`409` `judging_closed`). |
| `archived` | Ended | Final. See "After the event ends". |

Moving from `closed`, `judging` or `results` back to `open` or `building`
clears a deadline that has already passed, and passed per-team extensions, so
teams can publish again. Say so before you do it.

`archived` needs at least one participant, or it is `409`
`archive_needs_participants`: delete the event instead of ending it. Once it
is ended it cannot be reopened (`409` `event_closed`). Ask before ending it,
and tell them the team-site clock below starts then.

An unknown name is `400` `invalid_stage`.

#### 4. The join link and the judge link

Read them from `organiser.join_url` and `organiser.judge_url`
(`https://simple-hack.app/join/<code>` and
`https://simple-hack.app/judge/<code>`). The join code is 8 characters, the
judge code 12. Give the join link to participants and the judge link only to
judges.

A new link, when they ask (the old code stops working at once):

```
POST https://simple-hack.app/v1/hack/events/<slug>/codes/join
POST https://simple-hack.app/v1/hack/events/<slug>/codes/judge
```

Each returns `{code, url}`.

Participants can join only while the stage is `open` or `building`
(`409` `joining_closed` otherwise). Judges can join until the stage is
`results` or `archived` (`409` `judging_closed`).

`GET /v1/hack/events/<slug>/people` is every member. Participants in that
list carry `has_key`. Emails are on this list for the organiser only;
participants never see another person's email. Remove someone with
`DELETE /v1/hack/events/<slug>/people/<user_id>` (`409`
`cannot_remove_organiser`). Removing a participant turns off their team key.
They can join again with the current join link; make a new link to stop that.

**Co-organisers.** An organiser can create a single-use invitation with
`POST /v1/hack/events/<slug>/organiser-invite`. It returns `{url,expires_at}`;
the link expires after seven days, and a new one replaces the old one. Give it
only to the intended co-organiser. `DELETE` on the same path revokes the
outstanding invitation without removing an existing organiser. The recipient
previews it with `GET /v1/hack/organiser/<code>`, then signs in and accepts it
with `POST` on that path and `{"accept_coc":true,"display_name":"<name>"}`.
An existing event member cannot change roles through the link. An organiser
can remove a co-organiser with
`DELETE /v1/hack/events/<slug>/organisers/<user_id>` after identifying that
person in the event's people list. The event creator and last organiser cannot
be removed. A removed organiser immediately loses management access. These
operations are on the management page or REST API; the organiser connector
does not have invitation or removal tools.

#### 5. Teams, sites and entries

`GET /v1/hack/events/<slug>/teams` is every team (each with `id`, `slug`,
`code`, members, `site`, `deadline`, `frozen`, `pinned_version`, `pinned_url`)
plus `team_size_max` and `on_no_team`.

The organiser can, until the event has ended:

- Move a participant onto a team: `POST .../teams/<team>/members`
  `{"user_id": "<uuid>"}` (`409` `team_full`).
- Take someone off: `DELETE .../teams/<team>/members/<user_id>`. They stay in
  the event. The team's join code changes, so an old team code stops working.
- Remove the team: `DELETE .../teams/<team>`. Members stay in the event on no
  team. The team's site moves to Recently deleted and its name is not reused.
- Give one team a later deadline: `PUT .../teams/<team>/deadline`
  `{"deadline": "<time>"}`. It must be after the event's deadline
  (`400` `deadline_not_later`). `{"deadline": ""}` removes the extension.
- Turn off one person's team key: `DELETE .../people/<user_id>/key`.
- Take the team's site down: `POST .../teams/<team>/takedown` with an optional
  `{"reason": "<one line, at most 300 characters>"}`. The site shows its
  take-down page and deploys, saves and entry edits stop (`403`
  `team_site_taken_down`). `POST .../teams/<team>/restore` puts it back.
  `409` `platform_takedown` means the platform took it down; only the
  platform can undo that. Ask before a take-down.
- Change a team's display name: `PATCH .../teams/<team>` with
  `{"name":"<new display name>"}`. The name must be unique in the event;
  the team slug, site address, members and keys stay the same. Tell the
  organiser that existing links and the published address do not change.

Participants form teams themselves while the stage is `open` or `building`.
That flow is under Participant. One team per participant. Team names are
unique in the event.

**The entry is not the site.** Each team has one entry (title, tagline,
description, a video link, a code link, a screenshot), separate from the
website it deploys. There is nothing to ask them about "which site is the
entry". `GET /v1/hack/events/<slug>/entries` is every team's entry, for the
organiser and the judges, including a team that has not written one, with
`pinned_url` (a preview of the version pinned at the deadline, or null).

**Exports and usage.** An organiser can download
`GET /v1/hack/events/<slug>/export/participants.csv`, `/teams.csv`, and
`/entries.csv` for the event's roster, teams, and project entries. These
files can include private contact details; give them only to the organiser.
`GET /v1/hack/events/<slug>/export/projects.tar.gz` downloads the event's
current published team projects, including saved data in private collections.
Keep the archive private. `GET /v1/hack/events/<slug>/usage` gives site and
version counts plus saved-data, history, screenshot and file byte counts.
These routes are on the management page or REST API, not the ten organiser
MCP tools. The MCP `hack_export_scores` and `hack_export_results` tools still
cover judging CSVs only.

A team site lives at `https://<team>.<event>.simple-hack.app` once
`event.team_sites_ready` is true. Before that a deploy is `409`
`team_sites_not_ready`. The certificate is requested while the stage is open,
building or closed, so open the event first, wait a few minutes and read the
event again. Do not retry in a loop.

On this server a site may be 25 MB and two deploys are kept. Saved data has
to be declared before a page writes it (`409` `declare_first`).

#### 6. The rubric

`GET /v1/hack/events/<slug>/rubric` returns `criteria[]` with `id`,
`position`, `name`, `description`, `weight`, `max_points`. A new event
already has four. Judges can read it too.

Replace it only when the organiser asks for different criteria. Replacing
deletes every score and comment already given, because those rows belong to
the old criteria. Ask first once anyone has scored, and do it before judging
is locked.

```
PUT https://simple-hack.app/v1/hack/events/<slug>/rubric
{"criteria": [
  {"name": "Idea", "description": "Is the problem and the idea clear?", "weight": 25, "max_points": 5},
  {"name": "Execution", "description": "Does it work?", "weight": 25, "max_points": 5},
  {"name": "Design", "description": "Is it clear to use?", "weight": 25, "max_points": 5},
  {"name": "Demo", "description": "Can you show it?", "weight": 25, "max_points": 5}
]}
```

1 to 10 criteria. Each name is one line, 1 to 80 characters. Each description
is 0 to 300 characters. Each weight is a whole number 0 to 100 and the
weights sum to exactly 100. Each `max_points` is a whole number 1 to 10.
`400` `invalid_rubric` names what failed. `409` `scores_locked` means unlock
first.

#### 7. Who judges whom

`GET /v1/hack/events/<slug>/judging/settings` returns `assignment_mode`,
`judges_per_team`, `score_mode`, `tie_criterion_id`, `public_scores` and
`public_ranks`.
A new event is `open` (every judge may score every team) with
`judges_per_team` 2.

```
PATCH https://simple-hack.app/v1/hack/events/<slug>/judging/settings
{"assignment_mode": "automatic", "judges_per_team": 3}
```

`assignment_mode` is `open`, `automatic`, `manual` or `panel`.
`judges_per_team` is 1 to 20, the target per team in automatic mode.
For manual assignments, set `{"assignment_mode":"manual"}` first. Then
`GET /v1/hack/events/<slug>/assignments` reads the pairs and `PUT` with
`{"assignments":[{"judge_id":"<uuid>","team_id":"<uuid>"}]}` replaces them.
Use event-scoped ids from the people and teams lists. Conflicted pairs are
refused. The judge's queue follows these assignments.

For track panels, set `{"assignment_mode":"panel"}`. Then
`GET /v1/hack/events/<slug>/judging/panels` reads panel memberships and
`PUT` with `{"panels":[{"track_id":"<uuid>","judge_id":"<uuid>"}]}`
replaces them. Track ids come from `GET .../tracks`. A judge on a panel
scores teams in that track, except conflicts; a team without a track has
no panel queue. Review track choices and panel coverage before judging.
Changing manual assignments or panels is refused after scores lock or
the event ends.

`score_mode` is `raw` (the usual weighted mean) or `normalised`
(judge-adjusted scoring). The organiser can change it during judging;
the selected mode is stored when results are published. If a particular
criterion should break tied totals, set `tie_criterion_id` to its id from
the rubric before judging begins, or an empty string to clear it. The
organiser can set `public_scores` and `public_ranks` independently to show
numeric totals and ranks on the public results. Both default to true.
Confirm the organiser's desired public visibility before publishing.
`GET /v1/hack/events/<slug>/judging/preview` shows live standings in
both modes, the deciding mode and track winners to the organiser without
publishing anything.

Automatic assignment, after there is at least one judge and one team with a
member:

```
POST https://simple-hack.app/v1/hack/events/<slug>/assignments/generate
```

It spreads judges as evenly as it can, skips declared conflicts, and replaces
any earlier assignment. The same inputs give the same spread.
`teams_under_target` lists teams that got fewer judges than the target
because not enough non-conflicted judges were free: tell the organiser.
`400` `too_few_judges` or `too_few_teams` means wait for people to arrive.
`409` means the mode is still `open`: switch to `automatic` first.

In `open` mode do not generate. Every judge scores every team except their
conflicts.

#### 8. Conflicts of interest

A conflict removes that judge from that team: from assignment, from the
judge's queue, and from the team's total, even if they had already scored.

```
GET  https://simple-hack.app/v1/hack/events/<slug>/conflicts
POST https://simple-hack.app/v1/hack/events/<slug>/conflicts
```

The organiser sees every conflict. A judge sees only their own. A judge
declares their own with `{"team_id": "<the team's id>"}`. The organiser can
declare one for a judge with `{"team_id", "judge_user_id"}`. Declaring twice
returns the same row.

Remove it: `DELETE .../conflicts/<team_id>`. A judge removes their own. The
organiser passes `?judge_user_id=<uuid>` to remove a specific judge's.
`404` `conflict_not_found` when there is nothing to remove.

Team ids come from `GET .../teams` (`id`), not the team slug.

#### 9. Watch judging, then lock it

```
GET https://simple-hack.app/v1/hack/events/<slug>/judging/dashboard
```

`teams[]` has `judges_scored` and `flagged`; `judges[]` has `done_count`
and `assigned_count`. Review this coverage alongside the active assignment
mode before locking. In automatic mode the target is `judges_per_team`; in
open mode it is 2. Tell the organiser who is behind before they lock.

Locking refuses every further score and every rubric write (`409`
`scores_locked`) until an organiser unlocks it. Locking is idempotent.
Publishing does not require the lock. Ask before locking if judges are still
short.

```
POST https://simple-hack.app/v1/hack/events/<slug>/judging/lock
POST https://simple-hack.app/v1/hack/events/<slug>/judging/unlock
{"reason": "<one line, 1 to 300 characters>"}
```

The unlock reason is required and is logged. The event then carries
`judging_locked_at` and `judging_lock_reason`.

#### 10. Publish results, break a tie, choose the public view

A judge's score for a team is the weighted total:
Σ (weight / 100 × points / max_points) × 100. A team's total is the mean of
that across every judge who scored it, excluding a conflicted judge.

```
POST https://simple-hack.app/v1/hack/events/<slug>/results/publish
```

The body may be empty. This computes the ranking, stores a snapshot, and
uses the chosen tie criterion, then pairwise judge preferences, to resolve
equal totals where possible. Any unresolved tie is flagged. The answer is
`{results, full_ranking}`. Each result has `team_id`, `team_name`, `total`,
`raw_total`, `normalised_total`, `deciding_mode`, `tie_decider`, `rank`,
`tied`, `judges_scored`, and track winner fields. Publishing again later
recomputes from the scores as they are now and overwrites the snapshot. `full_ranking` is kept
from the previous publish (false the first time).

Ask before the first publish. The public page then shows global and track
winners. Numeric scores appear when `public_scores` is true and ranks
appear when `public_ranks` is true; both default to true. Judge comments
remain private.

**A tie.** `tied: true` and a shared `rank` mean those teams are level. To
break one tie, send every team in that tie a distinct `rank` and leave other
ties out. The numbers only order that group (a lower number places higher).
The list is then numbered again from 1, so the teams below move down. A tie
you do not mention stays a tie. A team that is not in a tie is `400`
`invalid_rank_override`.

```
POST https://simple-hack.app/v1/hack/events/<slug>/results/publish
{"rank_overrides": [
  {"team_id": "<higher>", "rank": 1},
  {"team_id": "<lower>", "rank": 2}
]}
```

**What the public sees.** Winners only is the default: the rank-1 team or
teams. The organiser can show the full ranking without recomputing:

```
PATCH https://simple-hack.app/v1/hack/events/<slug>/results
{"full_ranking": true}
```

`false` switches back to winners only. `404` `no_results_yet` means nothing
has been published.

`GET https://simple-hack.app/v1/hack/events/<slug>/results` needs no key.
Winners come back as `{"winners": [...]}`, including track winners and
prizes. A full ranking comes back as `{"ranking": [...]}`. Global and track
ranks appear only when `public_ranks` is true; numeric totals appear only
when `public_scores` is true;
comments never appear. The same GET with the organiser's own
key (or the admin key) returns the full snapshot instead,
`{"results": [...], "full_ranking"}`, so a reload of the Judging tab still
has the totals.

Each team reads its own result at `GET .../my-results` (Participant, below).

#### 11. The spreadsheets

Both work at any time, including before results are published. Results are
computed live for the file, not only from the snapshot.

```
GET https://simple-hack.app/v1/hack/events/<slug>/export/scores.csv
GET https://simple-hack.app/v1/hack/events/<slug>/export/results.csv
```

`scores.csv` is one row per team, judge and criterion: `team`, `judge`,
`criterion`, `points`, `max_points`, `comment`. `results.csv` is one row per
team: `team`, `total`, `rank`, `tied`, `judges_scored`, both score
modes, deciding mode, and track placement. Save the file and
tell the organiser where it is.

#### 12. After the event ends

`POST .../stage` `{"stage": "archived"}` is Ended on the page. Ask first.
Tell them all of this:

- It cannot be undone, and the event can no longer be changed.
- Team sites stay up for 30 days (`EVENT_SITES_KEEP_DAYS`). 14 days before
  they are removed (`EVENT_REMOVAL_WARN_DAYS`) the organiser gets one email
  at the event's contact address: the event was archived, the date the sites
  will be removed, that the event page and the results stay, and to write to
  support@simple-host.app for more time. The warning is sent only when that
  email is accepted.
- When the 30 days end, each team's site moves to Recently deleted. The
  event page, the results and the judging data stay.
- The sweep runs once a day. It does nothing when the platform has no mailer,
  so a missing warning email is the thing to check, not a reason to delete
  anything by hand.
- More time is a platform-admin action:
  `POST https://simple-hack.app/v1/admin/hack/events/<slug>/keep-sites`
  `{"keep": true}` with the admin key, which answers `{"keep_sites": true}`.
  `{"keep": false}` puts the sites back on the clock. The organiser does not
  have this call; they write to support@simple-host.app.

Ending the event also closes submissions if they were still open, the same
way Submissions closed does.

### Participant

They sign in (above), then open the join link the organiser sent.

`GET https://simple-hack.app/v1/hack/join/<code>` needs no key and describes
the event, including `coc_text` and whether joining is still open. `404`
`invalid_code` means the link was replaced or mistyped.

```
POST https://simple-hack.app/v1/hack/join/<code>
X-API-Key: <their key>
{"accept_coc": true, "display_name": "Ada", "answers": {"experience": "A transit site"}}
```

`accept_coc` must be true (`400` `coc_required`). `display_name` is one line,
1 to 100 characters, and it is the name their teammates see. Joining again
with the same role is a no-op `200`. A person who is already a judge or the
organiser gets `409` `already_member`. The platform admin gets `409`
`admin_cannot_join`. Their event page is `https://simple-hack.app/e/<slug>`.
If the event has sign-up questions, include `answers` keyed by their ids;
omit it otherwise. If approval is required, wait for approval before creating
or joining a team, editing an entry or getting a team publishing key.

**One team**, only while the stage is `open` or `building` (`409`
`teams_locked` after that):

```
POST https://simple-hack.app/v1/hack/events/<slug>/teams
{"name": "Night Owls"}
```

`201` returns `slug`, `name`, `code` and `members` (display names only, and
`you`). Or join an existing team:

```
POST https://simple-hack.app/v1/hack/events/<slug>/teams/join
{"code": "<the 8-character team code>"}
```

`409` `team_full`, `already_in_team`, or `team_name_taken`. Names are unique
ignoring case and spacing. `POST .../teams/leave` leaves; the team is deleted
when it becomes empty. A participant's view of the team is `me.team` on
`GET /v1/hack/events/<slug>`: the code, the members, `site`, `deadline`,
`extended`, `frozen`, `pinned_version`.

**The team key**, shown once. This is what publishes the site. The account
key cannot.

```
POST https://simple-hack.app/v1/hack/events/<slug>/key
```

`201` returns `key`, `site`, `api_base` (`https://simple-hack.app`) and
`mcp_url` (`https://simple-hack.app/mcp`). `409` `no_team` means join or
start a team first. `409` `event_closed` means the event has ended. Any
earlier team key of theirs for this event stops working. `GET .../key`
returns the key's last 4 characters and the site, never the secret.
`DELETE .../key` turns it off. The key stops the moment they leave the team,
are moved, or are removed (`401` `team_key_inactive`).

Publish with that key, and only to that team's site:

```
PUT https://simple-hack.app/v1/sites/<team-slug>/files?create=1
X-API-Key: <the team key>
X-Skill-Version: 0.27.10
{"files": {"index.html": "<!DOCTYPE html>…"}}
```

Ask once before the first publish. Say the address
(`https://<team>.<event>.simple-hack.app/`) and that anyone with the link can
open it. `403` `team_site_only` means the key was aimed at a different site.
`409` `team_sites_not_ready` means the event's certificate is still being
issued. `409` `submissions_closed` means the deadline has passed; the live
version is pinned and judges open `pinned_url`.

A participant who uses a chat app adds the connector once, at
`https://simple-hack.app/mcp`, not simple-host.app. The consent page asks
which team's site that connection publishes to. After Allow, the chat
publishes to that team. ChatGPT and Grok allow a custom connector only on
paid plans. The same "publish it once I say yes" rule applies.

The prompt to hand a participant who has a team key:

```
You are publishing my team's site for a hackathon on simple-hack.app.
The site name is <team-slug> and its address will be https://<team>.<event>.simple-hack.app/.
My team key is: <the team key>
Ask me what I want to build, then build it and publish it once I say yes.
Do not create any other site. This account cannot own a personal site.
Declare any saved data before the page writes it.
```

**The entry**, one per team, with the account key (not the team key):

```
GET https://simple-hack.app/v1/hack/events/<slug>/entry
PUT https://simple-hack.app/v1/hack/events/<slug>/entry
{"title": "Night bus", "tagline": "When the last bus goes", "description": "…", "video_url": "", "code_url": "https://github.com/example/night-bus"}
```

A field left out stays as it is. `title` is one line, 0 to 80 characters;
`tagline` one line, 0 to 160; `description` 0 to 5000. `video_url` and
`code_url` are empty or an absolute http or https URL (`400` `invalid_url`).
The screenshot is the raw image, at most 2 MB, PNG, JPEG or WebP by its
bytes (never SVG):

```
PUT https://simple-hack.app/v1/hack/events/<slug>/entry/screenshot
Content-Type: image/png
<bytes>
```

`413` `image_too_large`, `415` `unsupported_image`. `DELETE` on that path
removes it. The GET of the entry includes `required` (what a complete entry
must have) and whether it can still be edited. After the deadline, entry
edits are `409` `submissions_closed`.

A participant can download their own team's current published project with
`GET /v1/hack/events/<slug>/team/export.tar.gz` using their personal account
key. It includes saved data, including private collections, so keep it within
the team. The route cannot select another team and returns `404` `no_project`
when there is no live project. It is available through the event page or
REST API, not the team publishing connector.

**Their result.** `GET /v1/hack/events/<slug>/my-results` before anything is
published returns `{"published": false}` and nothing else. After publish it
adds `total`, `rank`, `tied` and `comments` (every judge's comment on this
team, with no indication of which judge). `409` `no_team` when they are on
no team. The same card is on their event page once results are published.

### Judge

They sign in, then open the judge link.

```
GET  https://simple-hack.app/v1/hack/judge/<code>
POST https://simple-hack.app/v1/hack/judge/<code>
X-API-Key: <their key>
{"accept_coc": true, "display_name": "Grace"}
```

Same rules as joining as a participant, and `409` `judging_closed` once the
stage is `results` or `archived`. Their event page
(`https://simple-hack.app/e/<slug>`) lists the teams they score, one
criterion at a time, with Save & next, Previous, and "I have a conflict".

The queue, least-covered team first:

```
GET https://simple-hack.app/v1/hack/events/<slug>/judge/queue
```

In open mode that is every team except their conflicts. In automatic mode it
is the teams they were assigned, except conflicts. Each item has the entry
(title, tagline, description, links, screenshot), `team_id`, `done` (every
current criterion scored by this judge) and `scores_count` (how many judges
have scored that team).

```
GET https://simple-hack.app/v1/hack/events/<slug>/judge/scores/<team-id>
PUT https://simple-hack.app/v1/hack/events/<slug>/judge/scores/<team-id>
{"scores": [{"criterion_id": "<id from the rubric>", "points": 4}], "comment": "Clear demo."}
```

Use `team_id` from the queue, not the team slug. Send one criterion or several. Points are a whole number from 0 to that
criterion's `max_points` (`400` `invalid_points`). A retried save of the same
points is a no-op, never a second score. `comment` is one comment per judge
per team; omit it to leave the stored comment as it is. A comment with an
empty `scores` array and no score saved yet is `400` (score at least one
criterion first). The answer includes `complete`. `409` `scores_locked`
means the organiser has locked judging. `403` means this judge has a
conflict with the team.

A conflict from the judge: `POST .../conflicts` `{"team_id": "<id>"}`, or the
button on the scoring screen. That team leaves their queue.

They can read the rubric and `GET .../entries` (every team's entry and the
pinned preview link). They cannot see another judge's scores, and their
comment is shown to the team later with their name removed.

### Hosted limits worth hitting once

- One role per person per event. Joining as the other role is `409` `already_member`.
- Non-members and the wrong role get `404` `event_not_found`, not a list of who is in it.
- An ended event refuses changes with `409` `event_closed`.
- A taken-down event refuses writes with `403` `event_taken_down`.
- Join, judge and team codes are rate-limited (`429`). A signed-in person has their own limit, separate from anonymous lookups.
- Support is support@simple-host.app.

## Self-host: a private instance

Use this when the organiser wants the event on **their own cloud account**,
paid for by them, that you create and later destroy. Participants get an API
key each and publish with their own coding agent. Nobody signs in.

**Ask the organiser before each step that costs money, goes public or cannot be
undone:** creating the server, claiming or adding the hostnames, and tearing
down. Say what you are about to do and wait for a yes.

**Their credentials go no further than they have to.** You use them to talk to
their provider and to this service, and send them nowhere else. Be straight with
them that an agent running in someone else's cloud is not the same as one running
on their laptop, and let them choose.

The references for this path:

| Step | Reference |
|---|---|
| Create the server | `references/providers.md` · https://simple-host.app/v1/skills/run-hackathon/references/providers.md |
| DNS | `references/dns.md` · https://simple-host.app/v1/skills/run-hackathon/references/dns.md |
| Install | `references/install.md` · https://simple-host.app/v1/skills/run-hackathon/references/install.md |
| Tear down | `references/teardown.md` · https://simple-host.app/v1/skills/run-hackathon/references/teardown.md |

### What the organiser needs before you start

1. **A cloud account with API access.** Ask them to create an API token in their
   provider's console and paste it to you.

   **If they have no account at all, recommend UpCloud.** It is the provider
   tested end to end for this path, and its smallest server is the size this is
   built for. The sign-up link and the referral note are in
   `references/providers.md` (https://simple-host.app/v1/skills/run-hackathon/references/providers.md).
   **If it must cost nothing, Oracle Cloud's always-free x86 shape.** Approval
   usually takes about a day, so an event on Saturday means signing up on
   Thursday. Both UpCloud and Oracle have been run end to end.
2. **A domain, or not.** Either works:
   - **No domain**: they get two free hostnames under a domain we run. You claim
     them; the organiser does nothing. This is the default, and the simplest.
   - **Their own domain**: they add two DNS records themselves. Choose this if
     they want their own name on it, or if they want email or Google sign-in
     later, which need records only they can add.
3. **A Simple Host account**, if they want a free hostname rather than using
   their own domain. Signing in at simple-host.app gives them an account key, and
   the claim is made with it. It takes a minute and needs no card. That account
   is simple-host.app, not simple-hack.app.
4. **Nothing else.** No email provider, no Google project, no Docker.

Tell them the cost before creating anything, and get the number right for the
provider you are actually using. **On Oracle's always-free shape it is nothing.**
Everywhere else it is about five dollars a month, billed hourly, so a weekend is
cents. Confirm before you create the server.

### The flow

#### 1. Pick the two hostnames

An event needs **two**, always:

- `<event>.<their-domain>` — dashboard, sign-in and API
- `sites.<event>.<their-domain>` — every participant's content

They are separate on purpose. A participant's page must never share an origin
with the admin interface, or anything published could script the dashboard of
whoever is viewing it.

#### 2. Create the server

Smallest Ubuntu 24.04 plan with at least 1 GB of memory.
`references/providers.md` (https://simple-host.app/v1/skills/run-hackathon/references/providers.md) has the exact calls per provider.

**Avoid installing a provider CLI where you can.** Hetzner, DigitalOcean,
UpCloud, Vultr and Hostinger are all driven with a bearer token and `curl`, which
is already there. An install on somebody else's laptop is a version, a login and
a new way to fail.

**Oracle is the exception and it is a real one.** It signs every request with an
RSA key pair, so it needs the `oci` tool and a config file in a hidden directory,
which is more than "paste a token". If the organiser cannot face that, point them
at any of the others; five dollars a month buys a much shorter path.

Generate an SSH key locally and pass the public half at creation, so nothing
needs a password. Never overwrite a key that already exists.

#### 3. Point DNS at it

Two A records, both to the server's public IPv4 address.

**If they have no domain**, claim free hostnames with the organiser's own
Simple Host account key:

```
POST https://simple-host.app/v1/events
{"name": "stanford-cs-2026", "ip": "<server IPv4>", "domain": "simple-hack.app"}
```

Always send `domain` explicitly and remember which one you used, because release
needs it too. **Ask the instance which domains it offers rather than assuming**:
a claim with an unconfigured domain is refused, and the error names the ones that
work. Today it is `simple-hack.app`. If a name is already in use, ask the
organiser for a different one rather than guessing at variations. A hosted event
on simple-hack.app holds its slug on the same name list, so a name either side
is using is taken on the other.

The response gives `host` and `content_host`. Names are lowercased, must be 1 to
40 letters, digits or hyphens, and a name someone else holds answers 409, so ask
for another. A claim expires after three weeks and re-claiming the same name
extends it.

**If they have their own domain**, they add the two records at their registrar.
See `references/dns.md` (https://simple-host.app/v1/skills/run-hackathon/references/dns.md).

Either way, wait until both names resolve before continuing. Installing first
works, but certificate issuance fails and the organiser sees browser warnings,
which is far more alarming than waiting.

#### 4. Install

One command over SSH, from `references/install.md` (https://simple-host.app/v1/skills/run-hackathon/references/install.md). It is idempotent: if it
fails halfway, run it again. It installs Docker, pulls the published image,
starts the stack and prints a JSON summary. Re-running it later is also how an
instance moves to a newer release: it applies the release's database changes
before the new version starts (`references/install.md`, Upgrading).

Two optional flags set how big a website may be and how many deploys to keep —
`--max-site-mb` and `--keep-versions`, both covered in `references/install.md`.
The defaults suit nearly every event. Do not work them out from a headcount;
a hackathon website is usually tens of kilobytes.

**The summary contains the admin key and it is shown exactly once.** Give it to
the organiser immediately and tell them to keep it. Nothing else can display it,
and without it they are not the administrator of their own instance.

#### 5. Create participant accounts

With the admin key, from the organiser's list of participants:

```
POST https://<event-host>/v1/admin/users
X-API-Key: <the admin key the install printed>
{"emails": ["ada@example.edu", "alan@example.edu"]}
```

or, for a walk-up event where you do not have names yet:

```
{"count": 30, "prefix": "team"}
```

There is no limit on how many. Do not invent one, and do not try to work out in
advance how many will fit — a hackathon site is usually a few tens of kilobytes,
so any figure derived from the per-site cap is wrong by a factor of thousands.

What to watch instead is the real thing:

```
GET https://<event-host>/v1/admin/usage
X-API-Key: <the admin key>
```

`message` is one sentence to repeat to the organiser, and `status` is `ok`,
`filling` (75% or more) or `full` (90% or more). On `filling`, tell them before
the event rather than during it; the fixes are a bigger disk or removing
whatever `largest` names. `version` and `commit` name the release running, and
`keep_versions` how many deploys of each website are kept. The organiser's admin
page shows the same figures.

The response gives each participant a username, a handle
and an **api_key**. Keys start with `shk_` (an older install issues bare
hexadecimal keys, which keep working); do not reject one for looking wrong.
Existing accounts are skipped and their keys are never re-disclosed, so
re-running is safe. A lost key is replaced per account: `POST
/v1/admin/users/<id>/key` (the `id` comes from `GET /v1/admin/users`), or **New
key** on that account's row of the admin page. It returns one new key, once,
and every earlier key of that account stops working; sites and data stay.

**Create in batches of at most 1000, and save each batch before asking for the
next.** Keys are shown once. 5000 accounts is roughly six seconds and 750 KB of
response on a fast machine and several times that on the smallest plan, and a
connection that drops after the server has committed takes every key in that
batch with it — the retry skips the accounts as already existing, so each of
those accounts then needs **New key**. This is about surviving a dropped connection, not
about a limit: create as many as the event needs.

Offer the organiser the list as CSV so they can paste it into a spreadsheet or a
mail merge.

#### 6. Tell participants what to do

Each participant needs two things: their key, and one instruction.

```
Read https://<event-host>/llms.txt and follow it exactly.
My Simple Host API key is: <their key>
Ask me what I want to build, then build it and publish it once I say yes.
```

That works in any agent that can fetch a URL. The instance's own `llms.txt`
names the event's hostnames, not simple-host.app, so their site lands on the
organiser's server.

A participant who only has a chat app (ChatGPT, Claude or Grok in a browser)
adds the event as a connector instead, once: the address is
`https://<event-host>/mcp` — the instance's own, never simple-host.app. The
event's sign-in window opens and, on an instance with no email provider or
Google sign-in, asks for the key the organiser handed out; after **Allow**,
every chat can build and publish to the event. The steps for each chat app are
on the instance's own `https://<event-host>/install.html`. ChatGPT and Grok
allow custom connectors only on paid plans, so mention that to the organiser;
anyone without one uses the prompt above in an agent that can fetch a URL.

#### 7. Collect the entries

Every entry is a link of the form
`https://sites.<event>.<their-domain>/<handle>/<project>/`.

The organiser's admin page lists them. Open `https://<event-host>/admin` and
paste the admin key where it asks ("Sign in with the admin key"); the browser
keeps it. The key is `ADMIN_API_KEY` in `/opt/simple-host/.env` on the box, and
re-running the install command prints it again. Entries shows every published site newest first, filterable, with
the links copyable and downloadable as a spreadsheet. That is the list for the
judges.

This path has no separate entry. One account can hold several sites, so if a
team built more than one, ask which is the entry — the list shows everything
published, not everything submitted.

#### 8. Tear it down

When the event ends, **delete the server and remove both DNS records**. See
`references/teardown.md` (https://simple-host.app/v1/skills/run-hackathon/references/teardown.md).

Do not skip the DNS records. A record left pointing at a released cloud address
means whoever receives that address next is serving content under that domain
name.

If you claimed free hostnames, release them:

```
DELETE https://simple-host.app/v1/events/<name>?domain=<the domain you claimed>
```

The `domain` must match the claim. Omitting it targets the default domain, which
answers 404 and leaves the real records in place.

If the organiser used their own domain, they delete the two records themselves.

Warn them first: deleting the server destroys every entry. Before you start,
offer to keep a copy of all of them in one archive (every site's files, saved
data and lists; the admin page's "Download all entries" does the same):

```
GET https://<event-host>/v1/admin/export.tar.gz
X-API-Key: <the admin key>
```

Save the response to a file on the organiser's machine and tell them where it is.

### If a site has to come down during the event

A reported or abusive site is taken down without deleting anything, with a
one-line reason the owner sees (the admin page has the same switches):

```
POST https://<event-host>/v1/admin/sites/<site id>/suspend   {"reason": "..."}
POST https://<event-host>/v1/admin/sites/<site id>/restore
POST https://<event-host>/v1/admin/users/<user id>/suspend   {"reason": "..."}
POST https://<event-host>/v1/admin/users/<user id>/enable
```

Ids come from `GET /v1/admin/users`. A taken-down site shows "This site has
been taken down" on every address and refuses changes; a suspended person's key
stops working and all their sites go down. Restore / enable puts it all back.

### What this path does not do

Say these plainly if asked, rather than working around them. They describe the
private instance, not simple-hack.app.

- **No sign-in for participants.** Keys only. Email codes and Google are
  possible on an instance whose domain the organiser controls, but they are
  configured separately and are not part of this flow.
- **Saved data is open.** Anyone who can load a page can change that site's
  stored data. Files change only with the owner's key. If saved data matters for
  judging, have teams copy it before judging starts.
- **No backups.** A disk failure during the event loses the event.
- **Free hostnames expire.** A claim lasts three weeks. Re-claim the same name to
  extend it. After that a sweep removes the records, so a long-running instance
  should use the organiser's own domain.
- **No judging, no rubric, no results page.** The admin page's Entries list is
  what the judges get. Judging, scores and a public winners page are the hosted
  path.
