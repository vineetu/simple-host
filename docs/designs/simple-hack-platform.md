# simple-hack.app: hosted hackathon platform

Status: M0 (instance), M1 (events, teams, join links) and M2 (team sites, member keys, entries,
deadline freeze, organiser moderation, gallery) built 2026-09-30. M3 (judging, results) and M4
(cleanup) shipped 2026-10-01. M5 adds the hosted-first skill and organiser connector. The core
M6 browser rehearsal covers six teams, four judges, a conflict, deadline pinning, phone scoring,
tie resolution and exports; the complete rehearsal follows P1 co-organisers and voting.
Plan approved by the owner 2026-09-30.

## What it is

simple-hack.app is a second copy of Simple Host on the same box (`simple-hack.service`, port
8091, the same binary, database `simplehack`) running with `EVENTS=hosted`. Anyone who signs in
creates an event and runs the whole hackathon there. Everything is free.

| Address | What it is |
|---|---|
| `simple-hack.app` | The app: product page, sign-in, your events, create, manage, join and judge links, `/admin` |
| `<event>.simple-hack.app` | The public event page, rendered by the server from plain text, no user HTML or script |
| `<team>.<event>.simple-hack.app` | A team's site: its own origin, visitor sign-in and saved data |

Signed-in pages live only on the apex, so no team page ever shares an origin with them.

## Owner decisions (2026-09-30)

- Anyone who signs in can create an event, with no approval. Creation collects who is running
  it: organiser name, organisation or community, contact email (prefilled), what the event is
  for, expected dates, expected number of participants. The platform admin sees these.
- No event size limits: no participant, team or judge caps. Abuse safety stays:
  `EVENT_CREATE_PER_DAY`, `EVENT_MAX_ACTIVE_PER_ORGANISER`, the per-site size cap
  (`MAX_ARCHIVE_MB=25`, `KEEP_VERSIONS=2` on this instance), `HACK_INSTANCE_BUDGET_GB` and the
  disk alert. All env settings.
- Everything is free.
- Team pages live under simple-hack.app.
- After an event closes, team sites stay up `EVENT_SITES_KEEP_DAYS` (30) and are then removed,
  with warning emails to the organiser. The event page and its results stay long-term.
- Results: the public sees winners only; each team privately sees its own scores and the
  judges' comments; the organiser can switch to a full public ranking (M3; fields exist now).

## Data model (db/migrations/hack1-events.sql)

- `events`: one row per event. `slug` is the event's name and address label. `account_id` is an
  internal **holding account** (a `users` row, `event_account = TRUE`, `handle = slug`, username
  `event+<event id>@events.invalid`, no keys, cannot sign in). `<slug>.<SITE_DOMAIN>` is that
  account's person host, which in hosted mode renders the event page. From M2 each team's site is
  a site of the holding account named after the team.
- `event_members`: one row per (event, person), one role each: `organiser`, `participant` or
  `judge`. Organisers are also eligible to judge without a second membership (owner decision
  2026-10-02). `team_id` only for participants. `coc_accepted_at` records the code of conduct.
- `event_teams`: `slug` (one DNS label, unique in the event), `name`, `code` (join-by-code).
- `event_create_log`: every creation, kept after deletion, for `EVENT_CREATE_PER_DAY`.
- Fields for later milestones already on `events`: `submission_deadline` (M2),
  `results_visibility` (`winners` | `ranking`) and `results_published_at` (M3), `closed_at`,
  `removal_warned_at`, `sites_removed_at`, `keep_sites` (M4), `taken_down_at`,
  `taken_down_reason` (platform admin).

### Stages

`draft` → `open` (sign-up open) → `building` → `closed` (submissions closed) → `judging` →
`results` → `archived`.

- In M1 the organiser can set `draft`, `open`, `building` and `archived` (shown as Ended);
  `closed`, `judging` and `results` arrive with the features they stand for and are 409
  `stage_not_available` until then. Any offered stage can follow any other, except that nothing
  follows `archived` (ending is final for the organiser; 409 `event_closed`). Setting `archived`
  stamps `closed_at` and needs at least one participant (409 `archive_needs_participants`: an
  event nobody joined is deleted instead, so ending cannot hold a name for good).
- Delete: while nobody but the organisers has joined (no participants, no judges) and the event
  has not ended; otherwise 409 `delete_only_empty`. It frees the name, unless the event ever had a
  team (M2: `event_used_names` keeps such a name, and every team name, for good).
- Joining as a participant: `open`, `building`. Otherwise 409 `joining_closed`.
- Joining as a judge: every stage but `results` and `archived`. Otherwise 409 `judging_closed`.
- Participants creating, joining or leaving a team: `open`, `building`. Otherwise 409
  `teams_locked`. The organiser's team changes: every stage but `archived`.
- A taken-down event refuses every write by anyone but the platform admin (403
  `event_taken_down`), and its public page shows the take-down page.
- A `draft` event's public page shows "Not open yet"; it is still served (noindex), because the
  organiser has no session on the event host to preview it with.

### Rate limits

Settings, per network address and per account: `RATE_LIMIT_EVENT_CODES_IP` (120, 1 a second:
a venue shares one address) and `RATE_LIMIT_EVENT_CODES_USER` (20, 1 every 3 s) for join, judge
and team codes; `RATE_LIMIT_EVENT_NAMES_USER` (60, 1 a second) for address checks.

### Codes

Alphabet `abcdefghjkmnpqrstuvwxyz23456789` (no 0/o/1/i/l). Join code 8 characters, judge code 12,
team code 8. Input is lowercased and spaces and dashes are removed before lookup. Codes are
stored as-is (the organiser re-shares them); every lookup route is rate limited by IP and by
account. Regenerating a code makes the old link stop working at once.

### Names

An event slug is 3–39 characters, `^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`, not `xn--`, and refused when:
it is reserved (`labelReservedForNew`, plus `www api admin app judge judges vote results e
events event join signin sign-in sites site team teams manage new help docs status mail blog
support report static cdn mcp auth`), it is a handle or handle alias on this instance
(`judgeHandle`), or the peer instance says it is taken (below). A team slug is derived from the
team name (lowercase, runs of anything but `a-z0-9` become one `-`, trimmed, at most 30), not
reserved, unique within the event (`-2`, `-3`, ... appended).

### One name list with simple-host.app's self-host claims

simple-host.app's `/v1/events` hands out `<name>.simple-hack.app` DNS records pointing at an
organiser's own box. The two instances ask each other over loopback before taking a name:
`GET /internal/names/taken?zone=<zone>&name=<name>` → `{"taken": bool}`. nginx never forwards
`/internal/`, and the route answers only a loopback peer with no `X-Forwarded-For`. `EVENT_NAME_PEER`
names the other instance. If the peer is set and does not answer, the name is refused (fail
closed, 503 `name_check_unavailable`).

## HTTP API (hosted mode only; all JSON; key in `X-API-Key`)

Errors are `{"error": "...", "code": "..."}`. Someone who is not a member of an event gets 404
`event_not_found` for every route of that event (existence is not revealed). The admin key sees
every event read-only through the member routes and acts through the admin routes.

| Route | Who | What |
|---|---|---|
| `GET /v1/hack/events` | signed in | Events I am in: `[{slug,title,stage,role,url,manage_url,starts_at,ends_at,taken_down}]` |
| `GET /v1/hack/names/{slug}` | signed in | `{available, code?, error?, address}` |
| `POST /v1/hack/events` | signed in | Create (below). 201 with the organiser view |
| `GET /v1/hack/events/{slug}` | member | The event as my role sees it (below) |
| `PATCH /v1/hack/events/{slug}` | organiser | Edit page text and settings |
| `POST /v1/hack/events/{slug}/stage` | organiser | `{stage}` |
| `POST /v1/hack/events/{slug}/codes/{kind}` | organiser | `kind` = `join` or `judge`: regenerate; returns the new code and URL |
| `DELETE /v1/hack/events/{slug}` | organiser | Only while nobody else has joined and the event has not ended; frees the name. Otherwise 409 `delete_only_empty` |
| `GET /v1/hack/events/{slug}/people` | organiser | Every member: `user_id, email, display_name, role, team (slug,name) or null, joined_at, coc_accepted_at` |
| `DELETE /v1/hack/events/{slug}/people/{user_id}` | organiser | Remove a participant or judge from the event (never an organiser) |
| `GET /v1/hack/events/{slug}/teams` | organiser | Every team: `slug, name, code, created_at, members[{user_id,email,display_name}]`, plus `team_size_max` and the participants on no team |
| `POST /v1/hack/events/{slug}/teams` | participant | `{name}`: create a team and join it. 409 `already_in_team`, 409 `team_name_taken` (names are unique in an event, any case). Answers the team as the participant sees it: `{slug,name,code,members[{display_name,you}]}` |
| `POST /v1/hack/events/{slug}/teams/join` | participant | `{code}`: 404 `team_not_found`, 409 `team_full`, 409 `already_in_team` (same team: 200) |
| `POST /v1/hack/events/{slug}/teams/leave` | participant | Leave my team. A team left with nobody is deleted |
| `POST /v1/hack/events/{slug}/teams/{team}/members` | organiser | `{user_id}`: move a participant into this team (from any team or none); 409 `team_full` |
| `DELETE /v1/hack/events/{slug}/teams/{team}/members/{user_id}` | organiser | Take a participant off the team (they stay in the event). The team gets a new code, as it does when the organiser moves someone off it or removes them from the event |
| `DELETE /v1/hack/events/{slug}/teams/{team}` | organiser | Remove the team; its members stay in the event on no team |
| `GET /v1/hack/join/{code}` | anyone | Join page info: `{slug,title,tagline,organiser_name,organisation,stage,joinable,role:"participant",coc_default,coc_text,starts_at,ends_at,time_zone}`; 404 `invalid_code` |
| `POST /v1/hack/join/{code}` | signed in | `{accept_coc:true, display_name}` → participant. 400 `coc_required`, 409 `already_member` (another role), 409 `joining_closed` |
| `GET /v1/hack/judge/{code}` | anyone | Same shape, `role:"judge"` |
| `POST /v1/hack/judge/{code}` | signed in | Same, → judge. 409 `judging_closed` |
| `GET /v1/admin/hack/events` | admin | Every event with the organiser details, stage, counts, creator's email, take-down |
| `POST /v1/admin/hack/events/{slug}/takedown` | admin | `{reason}` (required) |
| `POST /v1/admin/hack/events/{slug}/restore` | admin | Undo a take-down |
| `DELETE /v1/admin/hack/events/{slug}` | admin | Delete an event in any stage |

### Create

`POST /v1/hack/events` with `slug, title, organiser_name, organisation, contact_email, purpose,
expected_participants, starts_at, ends_at, time_zone`.

- `title` 1–120, `organiser_name` 1–100, `organisation` 0–120, `contact_email` a valid address
  (the page prefills the account's), `purpose` 1–1000 ("what the event is for"),
  `expected_participants` 1–100000, `starts_at` required and `ends_at` optional (RFC 3339 or
  `YYYY-MM-DD`, `ends_at` not before `starts_at`), `time_zone` an IANA name (default `UTC`).
- Refused: 429 `too_many_events_today` (`EVENT_CREATE_PER_DAY` in a rolling 24 hours), 409
  `too_many_active_events` (`EVENT_MAX_ACTIVE_PER_ORGANISER`, every stage but `archived`), 507
  `instance_full` (team sites use 80% or more of `HACK_INSTANCE_BUDGET_GB`), 409 `name_taken`,
  400 `invalid_name` / `name_reserved`.
- One transaction: the holding account (handle = slug, `event_account`), the event with fresh
  codes and `team_size_max = EVENT_TEAM_SIZE_DEFAULT`, the organiser's member row (CoC accepted,
  display name = organiser name), a create-log row.

### The event as each role sees it

`GET /v1/hack/events/{slug}` →

```
{
  "event": {slug, title, tagline, about, rules, prizes, coc_text, coc_default, stage, time_zone,
            starts_at, ends_at, team_size_max, url, taken_down, results_visibility,
            judging_locked_at, judging_lock_reason},
  "role": "organiser" | "participant" | "judge",
  "me": {display_name, coc_accepted_at, team: {slug, name, code, members: [{display_name, you}]} | null},
  "organiser": {            // only for the organiser (and the admin key)
    organiser_name, organisation, contact_email, purpose, expected_participants,
    join_code, join_url, judge_code, judge_url,
    counts: {participants, teams, judges, on_no_team}
  }
}
```

Participants never see other people's email addresses or account ids. Judges see no teams in
M1. The platform admin reading an event it is not in gets `admin_view: true` (every write
through these routes is refused; it acts through the admin routes) and cannot join an event
(409 `admin_cannot_join`). `GET /v1/hack/events` items carry `time_zone`, and dates are shown in
the event's zone. `GET /v1/admin/hack/events` answers `{"events": [...]}` with `participants`,
`teams` and `judges` on each.

Text: one-line fields (title, tagline, names, team names) refuse line breaks; every field refuses
control characters and invisible formatting characters (bidi overrides, zero-width marks), except
the zero-width joiner and non-joiner. `"ends_at": ""` clears the end date.

### Edit

`PATCH /v1/hack/events/{slug}` takes any of: `title` (1–120), `tagline` (0–160), `about`
(0–10000), `rules` (0–10000), `prizes` (0–5000), `coc_text` (0–10000), `time_zone`,
`starts_at`, `ends_at`, `team_size_max` (1–50), `organiser_name`, `organisation`,
`contact_email`, `purpose`, `expected_participants`. Lowering `team_size_max` below a team's size
leaves the team as it is and stops new joins to it.

## Pages

All on the shared chrome (`<!--sh:head-->`, header, footer, light and dark from site.css tokens,
no page-level theme code), in-page dialogs only (`shConfirm`, `shPrompt`, `shAlert`), phone first
(320 px and up, 40 px touch targets).

| Path (apex) | Page |
|---|---|
| `/` | Product page. Main call to action "Create event"; self-hosting second |
| `/signin` | Email code (and Google when configured); `?next=` a same-origin path |
| `/events` | Your events, with Create event |
| `/events/new` | The create form |
| `/e/<event>` | A participant's or judge's view: my team, create or join a team, leave |
| `/e/<event>/manage` | The organiser: overview and links, page text, people, teams, settings |
| `/join/<code>`, `/judge/<code>` | Join as a participant or judge: code of conduct, name, Join |

`<event>.simple-hack.app/` is rendered by the server: title, tagline, dates in the event's time
zone, about, rules, prizes, a gallery placeholder, organiser. Plain text only, escaped, newlines
kept; a strict CSP with no script beyond the shared theme. Every other path on the event host is
our 404, except `/screenshots/<team>` while the gallery is open. `/v1/` on the event host stays
404: nothing is served by path there, and each team site answers `/v1/` for itself.

## Security rules

- Role checks on the server on every route; nothing trusts the page.
- No cross-event access: every query is keyed by the event resolved from the slug and the
  caller's membership in that event.
- Signed-in pages only on the apex. The event host serves one server-rendered page.
- Codes are rate limited by IP and by account; wrong codes cost the same as right ones.
- Organiser text never becomes HTML anywhere (server templates escape; pages use textContent).
- CSV exports (M3) neutralise formula injection.

## M2: team sites, keys, entries and the deadline (db/migrations/hack2-team-sites.sql)

### Team sites

A team's site is the site named after the team (`event_teams.slug`) of the event's holding
account, at `https://<team>.<event>.simple-hack.app/`: its own browser origin, with visitor
sign-in, sessions and saved data bound to that host. Person-path serving
(`<event>.simple-hack.app/<team>/`) never happens: the event host answers only its own page, and
preview links are only ever minted on the team's host.

- **Certificates.** Opening an event (stage `open`, `building` or `closed`) drops a request for
  `*.<event>.simple-hack.app` for the site-certs issuer (`simple-host-site-certs-hack`); a sweep
  every minute asks again for such events without one, and so does a team key's deploy. Nothing
  else asks (page views never do), so drafts and deleted events spend none of the weekly budget. Team sites are offered (`team_sites_ready`,
  `me.team.site.ready`) and deployed (409 `team_sites_not_ready`) only once the certificate is
  served, so no team's work ever lands on another origin.
- **Names.** A team's slug is never a reserved site name, and never a name the holding account
  had a site under (live, in Recently deleted, or renamed): a new team never inherits an old
  team's files, data or visitors.
- **Removal.** Deleting a team (or its last member leaving, or the organiser emptying it) moves
  its site to Recently deleted and deletes its members' keys; the sweep catches any site whose
  team is gone. Deleting an event deletes its sites' files with the holding account.
- **Saved data.** Visitors sign in on the team's host and save as on any site; the instance runs
  `WRITE_AUTH_MODE=on` and `SAVED_DATA_DEFAULT_KIND=declare_first`, so nothing is saved under a
  name the team has not declared (Page info, Submissions, Personal or a Shared board). The "saved
  data is open" note of self-hosted event boxes does not apply.

### Member keys (scope `team`)

Each participant makes their own key on their event page (`POST /v1/hack/events/{slug}/key`, shown
once; one per person per event; making another replaces it). The key belongs to the person
(`api_keys.user_id`, `scope = 'team'`, bound in `event_team_keys`) and acts as the holding
account on one site: the person's current team's. Every request re-checks that the person is
still a participant on that team; moved, taken off or removed, the key answers 401
`team_key_inactive` at once (and is deleted). The organiser can turn any person's key off
(`DELETE .../people/{user_id}/key`).

`internal/auth/scope.go` holds a team key to the deploy routes plus `teamRoutes` — the site's
saved data as its owner (declaring kinds, reading what visitors sent, Page info, removing an
item, who may save), its visit counts and `GET /v1/me` (which answers `{team: {event, team,
site}}`, never the holding account) — and every `{sitename}` in them must be the team's (403
`team_site_only`). Everything else is 403 `team_key_scope`: deleting or renaming the site,
domains, passcodes, version retention, visibility, open writes, the events API, keys, the
account. `GET /v1/sites` lists the team's own site only. A personal key owns no sites (403
`no_personal_sites`). A team key cannot connect an app.

**The connector** (`simple-hack.app/mcp`): a connection made without a team acts for nobody on
this instance (reconnect). On the consent page the person picks which team's site
the connection publishes to (`GET /v1/hack/my-teams`; the decision carries `team_id`, 400
`team_required`, 403 `not_on_team`). The grant's scope records it (`sites team:<id>`, written
only by the server) and each request runs with an in-process credential bound to that team, so
the same gate and the same live membership check apply.

### Entries

One entry per team (`event_entries`), separate from what is published: title, tagline,
description, video link, code link and a screenshot (PNG, JPEG or WebP by its bytes, at most
2 MB, at most 8000 pixels on a side; never SVG). Entry writes are limited per account (30, then
1 every 2 s); the event host's `/screenshots/<team>` per address (120, then 5/s) with an ETag and a
60-second cache. An empty `entry_required` means every entry is complete. The organiser picks which fields a complete entry needs (`entry_required`,
default title). Members edit it until their deadline (routes: `GET`/`PUT
/v1/hack/events/{slug}/entry`, `PUT`/`DELETE`/`GET .../entry/screenshot`); the organiser and
judges read every team's (`GET .../entries`, `GET .../teams/{team}/screenshot`).

### The deadline

`events.submission_deadline` (the organiser's, in the event's zone), and per team
`event_teams.deadline_override` (only later than the event's, and only when the event has one;
400 `deadline_not_later`, 409 `no_event_deadline`). A team's effective deadline is the later of
the two (none while the event has none). A bare date means 00:00 that day in the event's zone;
the pages send a date and time. The deadline is an instant: changing the event's time zone never
moves it. At a team's effective deadline its site, entry and saved-data settings freeze: every
deploy, upload, rollback, entry edit and change made with a team key is refused (409
`submissions_closed`), no new team key is made, and its members cannot leave (the organiser still
can move or remove people). A team whose deadline has passed is kept even when it empties, so its
submission stays for judging; the organiser can still delete it. What visitors save on a team's
site is not frozen: it is the site's live data, not part of the submission. The check runs
inside the deploy's own transaction against the database clock, holding the team row FOR SHARE;
the pin (`pinned_version`, the site's live version at the deadline) takes the row FOR UPDATE, so
a deploy let in before the deadline finishes and is what gets pinned, and nothing after it
lands. A sweep pins due teams every minute, and every read of pins pins first. Pinned versions
are never pruned by `KEEP_VERSIONS`. Moving a deadline (the event's or a team's) back into the
future clears the pins it reopens.

The organiser and judges open each team's deadline version through a preview link on the team's
own host (`pinned_url`, minted on each read; it opens only that version and saves nothing).

Stage `closed` ("Submissions closed") is offered now: setting it makes the deadline now unless it
already passed (teams given more time keep it). While closed, a future event deadline is refused
(409 `submissions_closed_stage`): move the event back to Open or Building (from any other stage,
this clears a passed deadline and passed extensions) or extend single teams.

### Organiser moderation

- Take a team's site down (`POST .../teams/{team}/takedown`, optional reason) and put it back
  (`.../restore`): the site's own take-down page on every address, saves and deploys stop, the
  entry freezes. The organiser can undo only their own take-down (409 `platform_takedown`).
- Remove a person (M1): their key stops at once.

### Gallery

`gallery_open` (organiser, default off). When open, the stage is past draft, the event is up and
its certificate is ready, the event page lists a card per team whose site is live and not taken
down: screenshot (served from the event host, `/screenshots/<team>`), title (else the team
name), tagline, and a link to the team's site. Plain text only; the page's CSP is unchanged.

### M2 API (hosted only)

| Route | Who | What |
|---|---|---|
| `GET/POST/DELETE /v1/hack/events/{slug}/key` | participant | My team key (POST shows it once) |
| `DELETE /v1/hack/events/{slug}/people/{user_id}/key` | organiser | Turn a person's key off |
| `PUT /v1/hack/events/{slug}/teams/{team}/deadline` | organiser | `{deadline}`; `""` removes the extension |
| `POST /v1/hack/events/{slug}/teams/{team}/takedown`, `/restore` | organiser | Team site down / back |
| `GET/PUT /v1/hack/events/{slug}/entry` | participant | My team's entry |
| `PUT/DELETE/GET /v1/hack/events/{slug}/entry/screenshot` | participant | Its screenshot |
| `GET /v1/hack/events/{slug}/entries` | organiser, judge | Every team's entry, site, deadline and deadline-version link |
| `GET /v1/hack/events/{slug}/teams/{team}/screenshot` | organiser, judge | A team's screenshot |
| `GET /v1/hack/my-teams` | signed in | My teams (the connector's choice) |

`PATCH /v1/hack/events/{slug}` also takes `submission_deadline`, `entry_required` and
`gallery_open`; the event view gains them plus `team_sites_ready` and `stages_offered`, and
`me.team` gains `site`, `deadline`, `extended`, `frozen` and `pinned_version`; `me.team_key`
says whether I have a key. `GET .../teams` items gain `site`, `deadline`, `deadline_override`,
`frozen`, `pinned_version` and `pinned_url`; `GET .../people` items gain `has_key`.
