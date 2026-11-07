# Simple Hack navigation and checklists — SIM-388 / SIM-393

Status: 2026-11-07 · slice 2 server implemented in this worktree; navigation and checklist UI remain separate slices.

## Decisions after review (2026-11-07)

| Decision | Final choice | Reason |
|---|---|---|
| Organiser pane | Home; Set up: Event page, Settings; Run: People, Teams, Entries, Announcements, Judging; Wrap up: Results. Event page keeps Basics/About/Schedule/Tracks/Sponsors/FAQ/Look/Website; Tracks is new. | Preserve seven working editors before the demo; headings are labels, not links. |
| Exports | Entries, Results, People and Teams keep their current exports/downloads. | Exports are activities; neither ticket asks to move them. |
| Results announcement | Remove o.results-announcement. | Announcement purpose cannot be inferred from a timestamp; task was not requested. |
| Ties | No unresolved current tied places, or a published snapshot with chosen order; revalidate against changed inputs. Todo opens Results override-and-publish. | There are no separately saved rank overrides; no new storage. |
| Every judge done | Actual judge members with eligible teams, plus organisers with any score or a non-open assignment; JudgeDoneForTeam for every eligible team; N of M judges done. | Judging permission is not a scoring obligation; former judges are not required. |
| Judge guidance | Organisers omit invite/CoC; judges use coc_accepted_at only. Conflicts: any declaration or unversioned “I don't know any of these teams”; real queue review destination. Rubric stays versioned. Organiser: Rubric final / Mark as final. | Reuse existing acceptance and controls; avoid unnecessary fingerprints and endpoints. |
| Membership labels | Organiser / Participant / Judge. | Abilities belong in actions, not repeated identity labels. |
| Sign-in arrival | Absent return: one non-archived, non-taken-down membership opens its Home; otherwise /events. Preserve explicit safe-return provenance. | An explicit /events return must differ from the old implicit fallback. |
| Personal acknowledgement cleanup | Shared membership deletion clears personal rows in the same transaction; account deletion also clears text subjects. | Leave, person removal and co-organiser removal must all reset a rejoin. |
| Contracts and migration | Keep GET /v1/hack/navigation, hack_get_navigation, hack_get_checklist, hack_set_checklist_not_needed, hack_acknowledge_checklist_item; add can/checklist to event view and hack_get_event; 0009-hack-checklists.sql, two tables. | One additive website/AI contract with deterministic server facts. |
| Slice 1 integrations | Update organiser tours.js and MCP App frame adapters in slice 1. | New targets and moved settings must work in browser tours and embedded Apps. |

## Contract and source findings

| Keep | Design consequence |
|---|---|
| INTENT: organisers, participants and judges can complete the same work on the website or through AI apps | Deterministic server checklists, additive REST and MCP, shared navigation targets. SIM-394 consumes these later; no assistant in these slices. |
| Atlas-first, light app, existing kit and screen modules | Change atlas before implementation. Move existing section renderers; do not rewrite forms, scoring or entry flows. |
| BUILD.md routes, invite returns, pending applications, leave/handover, stage confirmations | Preserve paths, queries and permissions. Add aliases, not dead links. |
| Participant Home works | Preserve its wording, four rows, countdown, submit action, tours and visual states. |
| Stages in `shell.js` and server | `draft → open → building → closed → judging → results → archived`; user-facing final label **Ended**, never a new `ended` stored stage. |
| `eventView` already supplies `role`, `me`, `needs_you`, organiser `next_step`, participant `progress` | Keep their shape and meaning. New `checklist` drives the new Home button; legacy `next_step` remains the existing forward-move contract. |
| `completeSignIn` currently falls back to `/events`, despite the reported Account landing | Audit all sign-in completion paths and implicit Account defaults; do not change an explicit return to `/account`. |
| `CountEligibleJudges` counts `role IN ('judge','organiser')`; judging endpoints use `loadMember(...,"organiser","judge")` | An actual organiser can enter judging without changing membership. Assignment inclusion is separate from area access. |
| Notices and schedule already work | Keep `needs_you`/notice dismissals separate from checklist marks. Keep fixed schedule rows and manual stage prompts; no automatic stage changes. |
| Atlas contains older statements about no judging deadline and excluding incomplete drafts | Update those touched statements to current `judging_ends_at` and settled-entry eligibility; use the current server queue/criterion completion, not those obsolete statements. |

## 1. Arrival and event switching

**Default sign-in destination:** preserve whether a safe invite/`next` return was explicit through readNext, storage, consumeNext and every authentication method. Honour an explicit safe return first (including `/events` or `/account`); do not store an absent return as explicit `/events`. Only for an absent return load the membership event list. Exactly one current, accessible event → its role Home; zero or two-plus → `/events`. This applies once on completion, not whenever `/events` is visited. Brand and **Your events** always open `/events`.

Arrival eligibility means stage other than `archived`, not taken down, and an actual membership. List grouping is independent: Current = all non-archived memberships (including taken-down); Past = archived memberships. A passed end date does not make an event past: judging or results may still need work. Pending/rejected participants remain memberships and open their existing status card. A synthetic admin view is not a membership.

| Person | One current event, default destination | Multiple current / only past / no events |
|---|---|---|
| Organiser | `/e/{slug}/manage?s=overview` | `/events` |
| Participant | `/e/{slug}/team?s=home` (existing approval card if needed) | `/events` |
| Judge | `/e/{slug}/judge?s=home`, even if scoring is open | `/events` |
| Different roles across events | Role of the sole current event; no account-wide preferred role | `/events`, role on each row |
| Organiser who also judges | Organiser Home; judging is an ability, not a second membership | Same count rule; never duplicate the event |
| New person | — | `/events`: existing Create event, Find a hackathon, invite-link empty state |

### Left pane, on every signed-in app screen

| Position | Contents and behaviour |
|---|---|
| Top | Existing brand/name → `/events`; **Your events** → `/events`; **Find a hackathon** → `/directory`. Rename the current global “Home” to avoid two Homes. |
| Your events list | Current event first, then remaining current events by title (case-insensitive), slug as tie-break. Five event rows maximum; if current is outside the first five it replaces the fifth. **All events (N)** always leads to the complete `/events` list. No stage-based guesses that an event needs attention. |
| Event row | Colour mark, title, membership label and stage. Membership labels: **Organiser**, **Participant**, **Judge**. Pending/rejected status replaces the stage subline where necessary. No invented multi-membership model. |
| Current event | Its row expanded with the area's sections below. All other event rows collapsed. Clicking another event opens its membership Home and expands that event; no automatic jump to queue/results. Current section gets `aria-current`. |
| Past events | Closed native toggle **Past events (N)**, containing the five most recently ended, then **All past events** → `/events?past=1`. Add `ended_at` to list rows by projecting existing `events.closed_at` (stamped on archive), without a schema change; legacy missing dates sort after dated rows, then title/slug. A current past-event route stays expanded above the toggle and is excluded from its five rows. |
| Taken down | Still listed with **Taken down**; organiser can open existing management view; other roles retain the current non-link row. Exclude from default arrival, not silently hide. |
| Foot | Existing Account, Sign out, Help. Pane middle scrolls; foot stays reachable. On Account, Create event and `/events`, show the same event list without an expanded event. |

`/events` shows current events and a closed Past events toggle, with the existing real Needs you list above them. It has no five-row cap; all memberships remain reachable. `?past=1` opens the past group. Preserve row menus, leave/withdraw and disabled ended-event actions. Refetch list after membership/stage changes and on window focus; no permanent initial-data cache after these actions.

The event switcher uses the same grouping/order/caps and All events links as the pane. Keep it in the current event header for quick switching and in the collapsed rail. In the rail, event marks have full title/role accessible names and tooltips; expanding restores labels and groups.

**390px:** keep participant tabs (Overview, Team, Entry, Website, More) and judge tabs (Overview, My teams, Rubric, More) unchanged. Organisers keep Menu, no new tab bar. Menu/More opens the existing full-screen pane with the event list; selecting a link closes it. Current-event switcher stays in the phone header. Outside an event, keep the brand plus Menu, whose first group is Your events. Switcher and pane have scrollable lists; closing restores focus to the opener. No horizontal tab strip for the many Settings sections: use a labelled native **Settings section** select on phone.

## 2. Organiser navigation and destination map

**Event page** keeps public page content in its working tabbed screen; **Settings** holds event rules and administration. Activities have their own screens. Do not scatter “Settings” tabs among activities. Home guides the stage; the pane does not reshuffle as stages change.

All routes below are relative to `/e/{slug}/manage`. Home is labelled **Home** for organisers only; its module and `overview` query remain. Participant/judge Overview labels stay unchanged.

| Group | Label | Route | Existing module reused |
|---|---|---|---|
| — | Home | `?s=overview` | `organiser-overview.js` |
| Set up | Event page | `?s=event-page` | Existing tabbed editor, plus new Tracks tab |
| Set up | Settings | `?s=settings` | `organiser-settings.js`, hosting extracted section renderers below |
| Run | People | `?s=people` | `organiser-people.js`, applications and member list |
| Run | Teams | `?s=teams&tab=teams` | `organiser-teams.js`, teams and solo list |
| Run | Entries | `?s=entries` | `organiser-entries.js`, operational list |
| Run | Announcements | `?s=announcements` | Existing announcements module |
| Run | Judging | `?s=judging` | Existing judging dashboard/conflicts/assignment/lock renderers |
| Wrap up | Results | `?s=results` | Existing preview/ties/publish/download renderers |

**Settings secondary navigation:** Sign-up, Teams, Entries (deadline + what teams hand in), Reminders, Judging (rubric, mode, judges per team, tie-break, judging closes), Results (ranking/visibility, voting), then Organiser details, Organisers, Links, Notifications, Leave or delete. No Event page settings group. Desktop: existing sub-page layout, one selected section; phone: labelled native **Settings section** select. Each section keeps its save convention and stage lock. Settings remains visible after Ended; existing edit exceptions remain.

### Every current item and sub-control → new home

Map IDs are stable. A row with several IDs shares a section but targets individual anchors. Each named anchor is part of the map, not a fragment invented by a checklist.

| Current item / sub-control | New home and canonical query | Map IDs / disposition |
|---|---|---|
| Overview stats, Needs you, next step, stage track | Home `?s=overview` | `o.home`; add current-stage checklist |
| Overview Change stage, forward move, End event | Home, same confirmations | `o.stage`, `o.end`; End stays last/dangerous, not an automatic checklist action |
| Overview share cards and shell copy join/judge links | Keep quick copy on Home/shell; settings owns regeneration | `o.links` → `?s=settings&tab=links` |
| Event page / Basics: title, tagline, start/end date and time, zone | Event page → Basics `?s=event-page&tab=basics` | `o.basics`; preserve date-only support |
| Event page / About: About, prizes, rules, standard CoC and additions | Event page → About `?s=event-page&tab=about` | `o.about`, `o.prizes`, `o.rules`, `o.coc` |
| Event page / Schedule: typed rows, start/end, prompts, edit/remove | Event page → Schedule `?s=event-page&tab=schedule` | `o.schedule`; fixed rows keep linking to their source settings |
| Event page / Sponsors: name, tier, link, logo, order/remove | Event page → Sponsors `?s=event-page&tab=sponsors` | `o.sponsors` |
| Event page / FAQ: question, answer, order/remove | Event page → FAQ `?s=event-page&tab=faq` | `o.faq` |
| Event page / Look: icon upload/remove, colour, gallery switch | Event page → Look `?s=event-page&tab=look` | `o.look`, `o.gallery` |
| Event page / Website: address copy/open, built-in/custom, ZIP upload, versions, storage explanation | Event page → Website `?s=event-page&tab=website` | `o.website`; keep upload/mode confirmations and storage links |
| Event page public preview / Open | Home and Event page header → public event URL | `o.public-page`; public URL unchanged |
| Announcements list, post/edit/delete, email participants | Announcements `?s=announcements` | `o.announcements`; preserve stage announcement dialog |
| People / Applications: filters, approve/reject | People `?s=people&tab=applications` | `o.applications` |
| People / People: role filter, remove, revoke publishing key; participant CSV discovery link | People `?s=people&tab=people` | `o.people`; destructive confirmations unchanged |
| People / Sign-up: approval toggle, pending-admit choice, questions | Settings → Sign-up `?s=settings&tab=signup` | `o.signup`; People links here |
| Settings Applications toggle; Results Directory listing | Settings → Sign-up, same section | `o.applications-setting`, `o.directory`; independent saves and current listing/application rules |
| People / Co-organisers: current list, full-control invitation create/copy/revoke, remove | Settings → Organisers `?s=settings&tab=organisers` | `o.organisers`; People organiser rows link here for organiser administration |
| Teams / Teams: list, messaging, rename, admins, lock, members/place/remove/delete, finding-board view, CSV discovery link | Teams and existing `?s=team&team={team}` detail | `o.teams`, `o.team-detail`; preserve detail links and actions |
| Teams / Not on a team, Fill teams preview/apply, manual placement | Teams `?s=teams&tab=solo` | `o.solo`; operational action, not a setting |
| Teams / Settings: size maximum, team finding | Settings → Teams `?s=settings&tab=teams` | `o.team-rules` |
| Teams / Tracks: name, challenge, prize, edit/remove | Event page → Tracks `?s=event-page&tab=tracks` | `o.tracks`; team track selection stays with team/entry |
| Entries: counts, entry list, completeness, extensions/clear, storage usage, CSV, entry detail, site moderation, exclude/include incomplete entries | Entries / existing `?s=entry&team={team}` | `o.entries`, `o.entry-detail`; extension is an exception for one team, stays an activity |
| Entries / Deadline save/clear | Settings → Entries `?s=settings&tab=entries#deadline` | `o.deadline` |
| Entries / What teams hand in: presets, field modes, one-of, judge-first order, SIM-382 confirmation | Settings → Entries `?s=settings&tab=entries#entry-rules` | `o.entry-rules`; retain impact preview and existing rules-chosen marker |
| Entries / Reminder times | Settings → Reminders `?s=settings&tab=reminders` | `o.reminders`; one list for participant and judge reminders |
| Judging / Rubric: criteria, description, weight, max points, order, score-deletion confirm | Settings → Judging `?s=settings&tab=judging#rubric` | `o.rubric` |
| Judging / Mode, judges per team, tie criterion, Judging closes | Settings → Judging, anchors `mode`, `tie-break`, `closes` | `o.judging-mode`, `o.tie-break`, `o.judging-closes` |
| Judging / Generate assignments, include organisers, manual/panel grid | Judging and `?s=assignments` | `o.assignments`; include-organisers is an input to generation, not a new access rule |
| Judging / Progress, conflict declare/remove, former-judge score deletion | Judging `?s=judging` | `o.judging`, `o.conflicts`, `o.former-judges` |
| Judging / Lock/unlock; Score as a judge | Judging; judge Home respectively | `o.lock`, `j.home`; show scoring link from `can.judge` |
| Results / ranking score mode, public visibility, scores, ranks | Settings → Results `?s=settings&tab=results` | `o.result-rules`; current save and publication semantics unchanged |
| Results / People's choice enable, window, voter eligibility | Settings → Results `#voting` | `o.voting`; remains available before Results stage |
| Results / standings preview, manual tied ranks, publish/republish, publication status, Results downloads | Results `?s=results` | `o.results`, `o.ties`, `o.publish`, `o.downloads`; preserve snapshot semantics |
| Settings / Your details: public organiser name, organisation, contact, private purpose, expected participants | Settings → Organiser details `?s=settings&tab=details` | `o.details`; account display name still belongs to Account |
| Join/judge link new-link buttons | Settings → Links `?s=settings&tab=links` | `o.links`; preserve confirms and refresh copies |
| Settings / website notifications: receiver create, secret copy/rotate, pause/resume/test/delete, deliveries | Settings → Notifications `?s=settings&tab=notifications` | `o.notifications`; existing integration only |
| Settings / Leave, creator handover, only-organiser close choice, Delete | Settings → Leave or delete `?s=settings&tab=danger` | `o.leave`, `o.handover`, `o.delete`; same shared leave flow |

Exports stay where they are today: Entries keeps `export-entries`, Results keeps its downloads, and People/Teams keep theirs. They are not Settings controls.

**Tracks belong beside About and Schedule.** They describe the challenges and prizes people enter, not how team membership works. This moves the editor only; it does not add a new public-page feature or change track eligibility. Teams retains track labels and per-team assignment.

Reuse means extract the existing tab/card renderers into small modules consumed by Settings and the existing activity screens. Share their loaders/save handlers; do not render an old full page hidden inside Settings. Keep existing screen IDs for atlas/module ownership, even where only their operational sections remain. `organiser-event-page` stays the working content screen; only Tracks is added there.

### URL compatibility

Normalise with `history.replaceState` before mounting, so Back does not walk through alias hops. Preserve unrelated query parameters (`team`, `walkthrough`, etc.). Native server app routes continue serving the same shell. Unknown section/tab still falls back to its documented default.

| Old URL query / fragment | Canonical destination |
|---|---|
| `s=overview`, bare `/manage` | Same, organiser Home |
| `s=event-page` and `tab=basics/about/schedule/tracks/sponsors/faq/look/website` | Unchanged; missing/unknown tab defaults to Basics |
| Event-page `#basics/#schedule/#sponsors/#faq/#appearance/#address` | Existing Basics/Schedule/Sponsors/FAQ/Look/Website resolution; recognised explicit tab wins |
| Event-page `tab=announcements` or `#announcements` | `s=announcements` (explicit recognised content tab wins over hash) |
| `s=teams&tab=tracks` | `s=event-page&tab=tracks` |
| `s=teams&tab=settings` | `s=settings&tab=teams` |
| `s=teams`, `tab=teams/solo` | Same |
| `s=people#signup` or `s=people&tab=signup` | Settings signup |
| `s=people#coorg` or `s=people&tab=coorg/co-organisers/organisers` | Settings organisers |
| `s=settings` | Default Sign-up; `#applications` → signup; `#notify-list/#notify-form/#notify-advanced` → notifications; `#set-name/#set-org/#set-email/#set-purpose/#set-n` → details; `#leave-open/#delete-open` → danger |
| `s=entries#deadline/#entry-rules/#handin/#reminders` (including live Overview/SIM-382 `#handin`) | Settings entries `#deadline` / entries `#entry-rules` (both entry-rules and handin) / reminders |
| `s=judging#jg-rubric-card/#jg-tie-card/#jg-mode-card/#jg-closes-card` | Settings judging, anchor rubric/tie-break/mode/closes |
| `s=results#res-score-mode/#res-score-mode-save/#res-visibility/#res-public-scores/#res-public-ranks/#res-visibility-save/#res-visibility-hint` | Settings results, `#ranking` |
| `s=results#res-vote-status/#res-vote-enabled/#res-vote-opens/#res-vote-closes/#res-vote-window-save/#res-vote-eligibility/#res-vote-eligibility-save` | Settings results, `#voting` |
| `s=results#res-directory-listed/#res-directory-save` | Settings signup, `#directory` |
| `s=people`, `announcements`, `entries`, `judging`, `assignments`, `results`, `team&team=…`, `entry&team=…` without a moved-control fragment | Same operational screen; short link to its settings replaces removed configuration |
| Every participant/judge query and every standalone BUILD.md invite/account/create route | Unchanged |

The concrete alias inventory above is mirrored in navigation.json. Recognised canonical Settings tab wins over a legacy hash; aliases preserve unrelated parameters and replace only section/tab/anchor. Compatibility tests enumerate every alias, precedence case and malformed/unknown hash. No removed `?s=` value may resolve accidentally to Home.

## 3. One navigation map

Canonical file: `internal/rules/hackevents/navigation.json`, embedded by that package. It contains data, no transport imports. A small generator reads it to produce the navigation data export used by `internal/webassets/static/hack/nav.js` and `docs/ux-atlas/prototype/nav.js`; keep route-resolution code handwritten. Atlas `roles[].nav` and settings destinations are generated projections, not separately maintained link lists. `build.py` uses the same source. No runtime frontend build or fetch is required to route.

```json
{
  "version": 1,
  "screens": [{"id":"organiser-settings","role":"organiser","ability":"manage","path":"/e/{slug}/manage","module":"organiser-settings"}],
  "targets": [{"id":"o.rubric","kind":"setting","label":"Rubric","role":"organiser","screen":"organiser-settings","section":"settings","tab":"judging","anchor":"rubric","href_pattern":"/e/{slug}/manage?s=settings&tab=judging#rubric","group":"set-up","settings_group":"rules","order":40,"nav":false}],
  "aliases": [{"section":"teams","tab":"tracks","target":"o.tracks"}]
}
```

Target kinds: `screen`, `section`, `setting`, `action`. Nav entries additionally have icon, phone-tab/short label, order and existing visibility/badge rules by ID. Action targets identify a destination/action in a screen; map data never grants permission. Include global targets `events`, `account`, `directory`; participant targets `p.home/team/entry/website/announcements/schedule/results`; judge `j.home/queue/scoring/rubric/invite/conduct/conflicts`. `j.scoring` accepts an encoded team ID; `j.invite` resolves to judge Home’s accepted-invitation record (the standalone `/judge/{code}` remains the actual invite flow); `j.conduct` resolves to judge Home `#conduct`, and `j.conflicts` to judge queue `?s=queue&tab=conflicts#conflicts`, selecting a real panel listing all candidate teams and each existing **Know this team?** control (not only declared conflicts). Include checklist-ID aliases `o.times/mode/announcement/judges/rubric-ready/scores`, `p.summary/evidence/submit`, `j.coc/scores/closes` as explicit targets. No secret invite code is added to judge output. Tables above define every `o.*` target, with anchors for multiple controls. Map `o.lock` and `o.stage` to their existing controls on Judging/Home (`#jg-lockzone`, `#ov-change-stage`); `o.end` targets `#ov-end`.

Checklist definitions store `target_id`, never literal URLs. A shared resolver supplies `screen`, `section`, `tab`, `href` (and anchor) from IDs plus slug/team. Navigation, fixed-schedule “Set in…” links, Needs you links, checklist links and assistant links resolve through this map. No assistant-specific list of settings.

Serve `GET /v1/hack/navigation` (static, no event/private values), returning version, screens, targets and aliases. MCP `hack_get_navigation {}` returns that same map; a future assistant can filter by ability and resolve targets with the event slug. Checklist results already return resolved links, so a caller need not reconstruct a URL.

Offline check: generator `--check` compares committed client/prototype data and atlas projections; validates unique target IDs, every checklist target/alias, every registered screen module, every role-nav section, and every mapped path against the app-route inventory. REST route/tool inventory checks cover new endpoints separately (HTTP registry does not enumerate `?s=`). `build.py --check` still checks prototypes/features. Add this map check to existing offline checks; no new validation framework. Focused browser destination tests resolve every moved-control target, mount the screen and assert its tab/section/control is selected and visible; include alias precedence and parameter preservation.

## 4. Abilities and organiser judging

Add top-level `can: {manage:boolean, judge:boolean, take_part:boolean}` to event view and membership list rows. Keep `role` as the stored membership label. Compute abilities in the events service from actual membership, not the organiser projection used for admin inspection.

| Actual membership | manage | judge | take_part |
|---|---:|---:|---:|
| Organiser | true | true | false |
| Judge | false | true | false |
| Participant (including pending/rejected, whose status gate remains) | false | false | true |
| Synthetic `admin_view` without event membership | false | false | false |

`judge=true` follows the existing eligible-judge membership rule exactly: organisers are included regardless of assignment-generation's include-organisers checkbox. It means “may open this area”, not “may score this team now”. Existing stage, lock, conflict, assignment, team and approval checks remain authoritative on reads/writes. An admin who is also an actual member gets that membership's abilities; inspection alone grants none.

Replace `if (match.role && match.role !== role)` with route-required-ability lookup: manage URL → `manage`, judge URL → `judge`, team URL → `take_part`. On allowed route, set `ctx.role` to the **area role from the URL**, retaining `event.role`/`membershipRole` for actual membership. This is necessary for shell, judge tour and scoring UI. Denied route → membership Home with replace navigation; no ability → existing inspection/not-member view, no bounce loop. Server endpoints continue authorising independently.

**Score as a judge** appears when `can.judge`, opens `/e/{slug}/judge?s=home`, and selects judge navigation/checklist. An organiser in judge area gets **Manage event** → organiser Home and their **Organiser** identity label. Do not show judge-leave wording to an organiser; their leave link opens the existing organiser-aware leave preview. Scoring still uses their own user ID; never create/change membership to judge. Archived/locked events allow area access but keep read-only scoring.

Judge-area conflicts must use an actor-scoped read, filtering by caller user ID for both judges and organisers. Preserve the organiser dashboard’s event-wide response. Judge Home, queue, conflict review and checklist facts all use the scoped read; changing ctx.role is insufficient. The entries service exposes candidate teams under existing mode/judgeability rules **before own-conflict subtraction**; eligible teams subtract the caller’s conflicts. All-conflicted means a nonempty candidate set with zero eligible teams, never merely conflicts.length > 0.

## 5. Checklist rules

States: `done` if the factual predicate holds; otherwise `not_needed` if a permitted mark exists; otherwise `optional` for an optional item; otherwise `todo`. Real completion wins over a mark. Required means “expected for this stage's guidance”, **not a new API stage-transition restriction**. Existing hard checks remain, including SIM-382's rules choice and publication locks. Only optional organiser items allow Not needed. No judge or participant item is dismissible.

### Organiser item catalogue

`R` = required, never dismissible. `O` = optional, Not needed allowed. “Valid” means existing server validation, not just a non-null field. Read timestamps using event zone/date-only semantics.

| ID | Label | Done rule, computed from server data | Kind | Target |
|---|---|---|---|---|
| `o.entry-rules` | Choose what teams hand in | Existing SIM-382 rules-chosen marker is set, including its legacy backfill; loaded rules validate | R | `o.entry-rules` |
| `o.basics` | Add event basics | Nonblank title and tagline; start and end present, valid zone; use existing patchEvent boundary validation and EventOver (date-only end lasts the whole local day, including equality) | R | `o.basics` |
| `o.times` | Add start and end times | Both timestamps present and neither classified as date-only by the existing local-midnight convention | O | `o.basics` |
| `o.about` | Describe the event | Trimmed About nonempty | R | `o.about` |
| `o.schedule` | Add a schedule | At least one valid typed schedule row; fixed rows alone do not complete this | O | `o.schedule` |
| `o.faq` | Add common questions | At least one saved question with nonempty answer | O | `o.faq` |
| `o.sponsors` | Add sponsors | At least one saved sponsor with name; logo/link not mandatory | O | `o.sponsors` |
| `o.tracks` | Add tracks | At least one track passing existing validation; challenge and prize may be empty | O | `o.tracks` |
| `o.rubric` | Set the rubric | Nonempty criteria pass existing name/weight/max-points validation | R | `o.rubric` |
| `o.mode` | Choose how judges score | Saved/effective assignment mode is one of open/automatic/manual/panel; judges-per-team valid when used. Valid defaults count | R | `o.judging-mode` |
| `o.tie-break` | Choose a tie-break | Saved tie criterion references a current rubric criterion | O | `o.tie-break` |
| `o.judging-closes` | Set when judging closes | Valid `judging_ends_at` present | O | `o.judging-closes` |
| `o.reminders` | Set reminder times | Effective reminder settings contain at least one valid offset; existing default 24h/1h counts. Empty list stays optional | O | `o.reminders` |
| `o.announcement` | Send an announcement | At least one saved announcement exists; no claim that email was delivered. Label detail gives posted count | O | `o.announcements` |
| `o.deadline` | Set the entry deadline | Valid submission deadline present (not inferred from event end); a past deadline still counts as set | R | `o.deadline` |
| `o.judges` | Have judges ready | Eligible-judge count > 0, using `CountEligibleJudges` including organisers; detail separately counts invited judges joined and organisers able to judge | R | `o.people` (judge filter) |
| `o.rubric-ready` | Rubric final | Current rubric fingerprint explicitly acknowledged by an organiser; no additional rubric lock flag. Changing criteria invalidates acknowledgement | R | `o.rubric` |
| `o.assignments` | Assign judges | Open mode: omit item. Other modes: every judgeable team has at least one eligible assigned judge after conflicts, with mode's existing coverage warnings in detail; panel also needs team tracks and panel membership | R | `o.assignments` |
| `o.scores` | Every judge done | Population: actual judge members with ≥1 eligible team, plus organisers with any score or (non-open mode) any assignment. Former judges are not required. Each must satisfy JudgeDoneForTeam for every eligible team; M>0 and judgeable teams>0, otherwise todo. Detail “N of M judges done” | R | `o.judging` |
| `o.lock` | Finish judging | `judging_locked_at` set | R | `o.lock` |
| `o.ties` | Resolve tied places | Current standings have no unresolved tied places OR results are published (snapshot carries chosen order). On relevant input change, compare snapshot against current standings as specified below; no standings → todo “No standings yet” | R | `o.ties` |
| `o.publish` | Publish results | Existing results snapshot is published | R | `o.publish` |
| `o.end` | End the event | Stage is `archived` | R | `o.end` |

The separate optional time item is deliberate: date-only events are already supported, and midnight is not distinguishable from an explicitly selected 00:00. Show the times item so timed events are guided, but do not invent a new date representation or block old events. A timed event can choose midnight and mark this recommendation Not needed.

**Rubric final** is the item label; a quiet **Mark as final** button sits beside Save rubric in Settings → Judging. It records an acknowledgement of the current rubric, not an irreversible freeze. Existing rubric edit/score-deletion and judging-lock rules remain. A successful Save rubric in Closed also acknowledges that saved version. This fulfils “rubric locked in” without locking scoring early.

### Organiser stage → list and next button

| Stored stage | Current-stage items, in order | Forward action once required work is complete |
|---|---|---|
| `draft` | entry-rules, basics, times, about, schedule, faq, sponsors, tracks, rubric, mode, tie-break, judging-closes, reminders | Existing Open sign-up |
| `open` | announcement, deadline | Existing Start building |
| `building` | announcement, deadline | Existing Close entries |
| `closed` | judges, rubric-ready, assignments (conditional) | Existing Start judging |
| `judging` | scores, lock | Existing Finish judging/lock workflow |
| `results` | scores, lock, ties, publish, end | Publish if unpublished; otherwise existing End event |
| `archived` | publish (only if published), end; expandable Earlier work history | No next button; event ended |

Earlier **unfinished required** items precede the current list in original stage/order, deduplicated by ID. Earlier optional items stay in a collapsed **Earlier optional items** list, never lead the button. Keep completed current-stage rows visible; do not flood Home with all completed earlier rows. After a setting becomes immutable, unfinished work remains visible in Earlier work with its factual state, `actionable:false` and reason; it cannot lead the next button. Reopening a stage recomputes applicability and actionability. Archived has history only, no corrective next step. Never suggest changing a locked entry rule merely to clear a checklist.

Next-button priority: existing hard blocker (notably entry-rules choice), then first actionable required todo in displayed order, then existing forward action/blocked reason from `next_step`. Optional rows never lead. In Judging, an `o.lock` todo uses the existing confirmed **Finish judging** stage move to Results, which locks in the same transaction; do not insert an extra lock-only step. In Results with judging unexpectedly unlocked it links to the existing lock control. An `o.end` todo uses the existing End confirm and only-organiser/empty-event rules. `o.publish` leads to Results preview/confirmation, not a GET that publishes. Preserve schedule cue on the forward-action card when that move is next; if a required item leads, show the due cue as a secondary line. Manual Change stage remains available with current checks. No automatic moves or new blanket readiness gate.

Existing `next_step` fields and meaning remain unchanged; `checklist.next` is the new server-selected Home action. New MCP descriptions tell callers to use it for guided work while keeping legacy clients functional. Avoid two competing next-step buttons on Home.

Tie resolution is the existing combined **Save order and publish** action on Results `#res-override`, not a separately saved override. Before publication, unresolved ties link there. After publication, recompute current standings on every read. Compare against existing snapshot fields by team ID: team set, total, raw_total, normalised_total, deciding_mode, tie_decider, judges_scored and track ID. Scores (including retained former-judge scores), rubric, score mode, tie criterion, exclusions and conflict/eligibility changes matter when they change that ranking projection. Ignore snapshot rank/tied/track_rank/track_winner in the comparison: those carry the chosen publication order. If the projection matches, publication completes o.ties; otherwise recompute without snapshot overrides and require no unresolved current ties or a fresh override-and-publish. Legacy snapshots missing comparison fields require republishing if current ties remain. No override table or new stored fingerprint.

### Judge checklist

Judge checklist is available through every stage (including an organiser's judge area). Invitation acceptance normally completes before Home is reachable; still show it as done. Never fabricate an accepted invitation for an organiser.

| ID | Label | Done rule / stage behaviour | Required; dismissible | Target |
|---|---|---|---|---|
| `j.invite` | Accept the judge invite | Actual judge membership joined timestamp exists; omit for an organiser judging by existing authority | Yes; no | `j.invite` |
| `j.coc` | Accept the code of conduct | Existing membership `coc_accepted_at` present; no new acceptance endpoint or conduct fingerprint. Omit for organisers; legacy missing acceptance remains todo with the existing conduct record, no invented write action | Yes; no | `j.conduct` |
| `j.rubric` | Read the rubric | Explicit **I've read it** acknowledgement matching current rubric fingerprint; opening a page alone does not tick it. No rubric: todo, “The organiser hasn't added a rubric yet” | Yes; no | `j.rubric` |
| `j.conflicts` | Check who you know | At least one own declared conflict OR explicit **I don't know any of these teams** personal acknowledgement. Unversioned; no candidate-set fingerprint or invalidation on assignment changes | Yes; no | `j.conflicts` |
| `j.scores` | Score each team | Authoritative own queue done count N equals total M, with M>0. Detail “N of M teams scored”. Score next links to first unfinished team. Zero teams: todo, non-actionable waiting message; all candidates conflicted: done with explicit nothing-to-score detail | Yes; no | `j.scoring` or `j.queue` when no scoreable team |
| `j.closes` | Judging closes | Informational optional row while open: detail actual `judging_ends_at`, or “No time set; the organiser closes judging”. Done only when scoring is actually locked/results published/archived; passing time alone never means success | Optional; no | `j.home` |

| Stage | Presentation |
|---|---|
| Draft / Open / Building | Invite, conduct, rubric; conflicts and scores shown as non-actionable waiting rows if queue is not available. Keep current Not open yet message. |
| Closed / Judging | All applicable rows. First actionable required todo leads, otherwise Score next / Review my scores as today. Scoring is already possible in Closed under existing rules. |
| Results / Archived, or any earlier stage with scoring locked | Keep checklist and counts as record; rubric may still be read, but never offer Score next when scoring is closed. Preserve distinct “locked before you finished”, all-scored, nothing-assigned, all-conflicted and results-out messages. |

Rubric/conflict acknowledgements guide the judge, adding no mandatory interstitial or scoring refusal. Conflict declaration/removal uses existing endpoints. No per-team “I don't know them” records: one review acknowledgement plus actual conflict rows.

### Participant adapter (all stages)

| Shared item ID | Source and target | Preservation |
|---|---|---|
| `p.team` | Existing membership/team or `progress.steps[team]` → `p.team` | Same Join or start a team / Find a team row |
| `p.summary` | `progress.steps[summary]` → `p.entry` | Same filled/of, missing labels and Edit action |
| `p.evidence` | `progress.steps[evidence]` → `p.entry` / `p.website` per existing evidence action | Same event-rule summary, one-of and required-evidence rules |
| `p.submit` | `progress.steps[submit]` → `p.entry` | Submitted → done; draft → todo; `not_submitted` → todo plus `settled:true`, dashed `.opt`, no submit action after deadline. Never call an incomplete settled entry done |

Keep the current before-team fallback four rows, pending/rejected gate, stage-specific hero, effective team deadline/extension, server clock offset, read-only states, published results and no-team messaging. No participant Not needed controls, new prerequisites or acknowledgements. Preserve the exact current `showEntryCard` predicate: false in judging/results/archived or whenever results are published, including backwards-stage/published combinations; Draft is included by the existing visibility predicate. Preserve each stage’s heroes and fallbacks. Server checklist availability does not imply Home renders it. `checklist.next` mirrors the existing participant next action, including Home submission eligibility; do not replace its working journey with organiser stage rules. Keep old `progress` byte-compatible in meaning and fields.

## 6. Server, persistence, REST and MCP

| Layer / location | Responsibility |
|---|---|
| `internal/rules/hackevents/checklist.go` | Pure catalogue, stage selection, item state/next rules; explicit input facts and `now`, no SQL, ambient clock or HTTP. Embed/read navigation data here. Entry predicates stay in/reuse `rules/hackentries`; pass typed entry-rule facts into hackevents pure rules, never import rules/hackentries from rules/hackevents. |
| `internal/app/hack/events/checklist.go` | Load member/event/content/settings/marks; obtain judging/entry progress facts through existing entries service; assemble organiser/judge/participant result and abilities. Own acknowledgement/dismissal transactions. Fits target.md's allowed events → entries dependency. |
| `internal/app/hack/entries` | Reuse EntryProgress, judge queue, actor-scoped conflicts, pre-conflict candidates, eligibility, current standings and tie computation. Assemble scoring-completion facts using JudgeDoneForTeam, not ScoreCoverage (which counts even partially scored pairs). Return typed facts; do not duplicate queue SQL in checklist code. |
| `internal/repository/hackevents/checklists.go` | SQL for marks/acknowledgements and missing read facts. Reuse existing area repositories for content/settings. No orchestration here. |
| `internal/hackhttp/hack_checklists.go`, eventView | Authenticate/decode, call app service, resolve DTO links, encode. Thread actual actor/admin context into ability computation. No new computation in handler maps. |
| `internal/mcp` | Existing REST replay and output schemas, no checklist inference. Map output carries the same resolved links. |

Add this top-level field, retaining every existing event-view field:

```json
{
  "can": {"manage":true,"judge":true,"take_part":false},
  "checklist": {
    "role":"organiser", "stage":"closed", "map_version":1,
    "items":[{
      "id":"o.rubric-ready", "label":"Rubric final",
      "state":"todo", "optional":false, "can_dismiss":false,
      "origin_stage":"closed", "group":"current", "actionable":true,
      "target_id":"o.rubric",
      "link":{"screen":"organiser-settings","section":"settings","tab":"judging","href":"/e/demo/manage?s=settings&tab=judging#rubric"},
      "detail":"", "count":null, "total":null,
      "settled":false, "blocked_reason":null
    }],
    "next":{"kind":"link","item_id":"o.rubric-ready","target_id":"o.rubric","label":"Rubric final","link":{"screen":"organiser-settings","section":"settings","tab":"judging","href":"/e/demo/manage?s=settings&tab=judging#rubric"}}
  }
}
```

`next` is null when no action; kinds `link`, `stage`, `lock_judging`, `publish_results`, `submit_entry`, `end_event` carry the existing action inputs and confirmation requirement. They describe an action, never execute one on read. Row groups `earlier_required`, `current`, `earlier_optional`, `history` allow one shared renderer to fold earlier work. Default event-view checklist is the membership role; an organiser receives organiser checklist there. Judge area fetches the judge checklist explicitly. Do not put both roles' lists into one mixed array.

| REST | Input / output and authority |
|---|---|
| `GET /v1/hack/navigation` | Static shared map; no private values |
| Existing `GET /v1/hack/events/{slug}` | Add `can`, default-role `checklist`; no new required request parameter |
| `GET /v1/hack/events/{slug}/checklist?role=judge` | Optional role defaults to membership role; verify requested ability. Return `{can,checklist}`. Organiser may request organiser or judge, participant only participant, judge only judge. Synthetic admin inspection gets read-only organiser projection with no personal acknowledgements or abilities |
| `POST /v1/hack/events/{slug}/checklist/not-needed/{item}` | Empty body; actual organiser only, optional organiser ID only; idempotent event-wide mark. Return fresh `{can,checklist}` |
| `DELETE /v1/hack/events/{slug}/checklist/not-needed/{item}` | Same authority; remove mark, idempotent; fresh result |
| `POST /v1/hack/events/{slug}/checklist/acknowledgements/{item}` | Explicit acknowledgement, body `{version:"<fingerprint from current item>"}` for rubric items; `j.conflicts` accepts `{}` for the unversioned personal acknowledgement. No `j.coc` write here. Only own judge acknowledgements or organiser rubric-ready acknowledgement. Reject stale version, refetch; no arbitrary completion endpoint |

Read fresh on every event/checklist request, after the existing due-entry settling/pinning path. Use the same eligibility and effective-deadline reads as the queue. No cached “done” booleans, AI or checklist worker. A new assignment, conflict, rubric edit, score change, deletion or setting update changes the next read. Rubric acknowledgement rows include opaque `version`; server computes and verifies it. Conflict acknowledgement is unversioned. Read actions do not auto-acknowledge.

**Migration `0009-hack-checklists.sql`**, also reflected in `db/schema.sql` at implementation:

| Table | Columns / key / ownership |
|---|---|
| `event_checklist_dismissals` | `event_id` FK cascade, `item_id` text; PK `(event_id,item_id)`; `marked_by` nullable user FK set null, `marked_at` timestamptz default now. Shared across organisers, retained on handover. Only catalogue optional IDs accepted by app service |
| `event_checklist_acknowledgements` | `event_id` FK cascade, `subject_id` text, `item_id` text, `version` text, `acknowledged_by` nullable user FK set null, `acknowledged_at`; PK `(event_id,subject_id,item_id)`. `subject_id='event'` for organiser final-rubric confirmation; actual user ID for judge rubric/conflict review. Delete personal rows in the shared membership-deletion path, in the same transaction, covering leave, teams.Service.RemovePerson and co-organiser removal; explicit account-deletion cleanup handles text subject IDs. Rejoining never reuses personal acknowledgements |

For rubric fingerprints hash the ordered criterion IDs, names, descriptions, weights and max points; conflict acknowledgement stores an empty version (unversioned). CoC uses only existing coc_accepted_at and has no acknowledgement row or fingerprint. Use server facts, not a client-supplied hash as proof.

One migration, plain additive idempotent `CREATE TABLE IF NOT EXISTS`, no backfill of claims that somebody read anything. Reuse membership joined/CoC timestamps; do not duplicate them. Dismissal marks persist across stages. Adding actual content makes a row done despite its mark; deleting it reveals Not needed again until **Use this item** clears the mark. Existing explicit archived-event write restrictions apply: history readable, marks/acks immutable after Ended. No notices or reminder offsets are changed by Not needed; clearing a reminder list remains a separate setting action.

| MCP tool / existing tool change | Inputs and outputs |
|---|---|
| `hack_get_navigation` | `{}` → static map |
| `hack_get_checklist` | `{slug, role?}` → same `{can,checklist}` as REST |
| `hack_set_checklist_not_needed` | `{slug,item_id,not_needed:boolean}` → POST/DELETE above, fresh result; organiser only |
| `hack_acknowledge_checklist_item` | `{slug,item_id,version?}` → explicit acknowledgement endpoint, fresh result; caller's acknowledgement only except event-wide final rubric |
| `hack_get_event` | Add `can` and default-role `checklist` to its existing flattened result and output schema; `hackEventView`/`hackEventOutput` currently explicitly select fields, so changing REST alone is insufficient |
| `hack_get_team_status` | Keep its existing REST replay and secret minimisation; pass through additive `can`/participant checklist and declare schema fields. Preserve existing `me`, site and progress semantics |

Expose new tools through existing Hack family/discovery/inventory. Classify writes as explicit idempotent changes; read tools keep existing settling semantics. No MCP-only completion state. Update OpenAPI YAML/JSON, route/tool inventories, public docs and Hack tool descriptions in the implementation slices, preserving existing names and output fields.

## 7. Shared checklist component

Module `internal/webassets/static/hack/checklist.js` exports `renderChecklist({ui, checklist, title, renderAction?})` returning a DOM node, and `bindChecklist(node,{api,slug,refresh,alive})` for Not needed / Use this item controls. It receives server-selected state and links; no role/stage predicates. `renderAction` lets participant Home retain its existing Submit callback and labels. Screens retain their countdown/hero wrapper.

| Part | Existing kit markup / behaviour |
|---|---|
| Card/list | `.card`, heading, `<ul class="steps">`; `<li>` with `.tick`, `.num`, `.what > b`, `.sub`, `.act`; same participant spacing |
| Done | `.done`, check tick and text; not colour alone |
| Todo | Number; `.now` for next item; action `.btn.sm.act` using the existing primary/quiet pattern |
| Optional | `.opt`, visible “Optional”; quiet linked label/action |
| Not needed | `.opt`, text “Not needed”, quiet **Use this item**; no success tick |
| Incomplete settled participant | `.opt` and current text; no green tick and no write action |
| Dismiss | Beside optional organiser row, `.btn.quiet.sm` **Not needed**, no modal. POST then refresh. Restore uses DELETE. Failure uses existing `ui.live`; keep row and focus |
| Earlier work | Native `<details>` plus same steps component; expanded Earlier required group only for actionable unfinished required rows |

On every navigation/Back to Home fetch event/checklist fresh, including after a form save, scoring change, acknowledgement or Not needed. `ctx.refresh()` handles actions performed on Home; guard abandoned requests with `ctx.alive()`. Focus returns to the changed row/control; announce saved state with `ui.live`. Refresh on window focus handles changes made in another tab/MCP; no new polling loop. No repeating help under every label; detail contains only missing facts, counts, time or a lock reason.

## 8. Atlas and two build slices

| Slice | Atlas first | Application/server work | Exit |
|---|---|---|---|
| 1: navigation | Update shell desktop/rail/phone/switcher, `roles[].nav`, Settings sections, moved feature homes, aliases and arrival decisions | Shared map/generator/check, additive `can` and event-list metadata, tours.js and MCP App frame adapters/links, abilities routing, sign-in default, persistent event list, reuse/move section renderers | Existing flows and every BUILD route pass; organiser enters judge area |
| 2: checklists | Home prototypes/states and checklist feature records | Migration, app/rules/repository computation, REST/MCP, shared component, organiser/judge Home; participant adapter only | Same facts and next links on browser, REST and MCP; all checklist rule tests pass |

Atlas files changed during implementation: `spec.json`, `features.json`, `prototype/shell.js` and affected screen/state HTML, generated navigation/files, `changelog.json`. This revision updates those atlas files and the JSON map; Go embed/generator, endpoints, migration, tours and frame adapters remain implementation work.

| Atlas screen | Required changes / prototype states |
|---|---|
| Shell, landing-your-events | Global Your events, current expanded row, five-row cap, no events, one event, multiple roles across events, Organiser label with Score as a judge action, past collapsed/open, long history, taken down, pending application, rail, 390px menu/switcher |
| organiser-overview | Home label; one prototype per seven stored stages; unfinished earlier required; optional/not-needed/restored; schedule cue; hard blocker; all required done; ended history; admin read-only |
| organiser-settings / organiser-event-page | Grouped Settings subpages and aliases; phone select; valid/locked/after-End exceptions; Tracks on Event page; final-rubric acknowledgement in Settings → Judging |
| organiser-people/teams/entries/judging/results and detail/assignments screens | Remove moved settings, point to map target; preserve operational controls and confirmations; organiser Score as a judge route |
| judge-home, judge-rubric, judge-queue | Invite/conduct done or legacy missing; rubric read/not read/changed; conflict review zero/declared/new assignment; partial/all scores; zero assigned/all conflicted; closes set/unset/passed; locked unfinished; results/ended; organiser in judge area |
| participant-home | Same baseline, no team, summary/evidence missing, complete draft, submitted, deadline-settled incomplete, extension, locked, pending/rejected; shared renderer screenshot must match baseline |
| account | Same content; events visible in pane; explicit return still respected |

New atlas feature IDs: `persistent-event-navigation`, `role-aware-signin-landing`, `shared-hack-navigation-map`, `organiser-judge-access`, `organiser-stage-checklist`, `judge-stage-checklist`, `dismiss-optional-checklist-item`, `acknowledge-checklist-item`. Extend existing `follow-entry-checklist`, do not replace its ID. Move existing feature IDs with their controls (including `add-tracks-and-challenges`, `set-reminder-times`, `set-judging-closes`, `entry-rules-first-step`, `choose-entry-preset`, signup/co-organiser/result-setting IDs). Associate new records with valid stable FEATURES anchors during implementation; no orphan atlas features. Add one plain changelog entry per slice and update BUILD's route table with aliases, not replacements.

Slice 1 includes `internal/webassets/static/hack/tours.js`, `internal/mcp/apps/frame/organiser-nav.js`, affected frame links and `internal/mcp/apps/bundlegen` integration. Shared resolver imports must bundle successfully; absent in-frame destinations retain website links.

## 9. Verification required for implementation

| Test | Concrete assertions |
|---|---|
| Organiser browser | Bare sign-in with one event → organiser Home; two events → `/events`; events visible from Account/Create/Home; switch event, Back, past cap/all-events; visit each stage's checklist; save About/rubric/rules/deadline and return → expected row/button updates; dismiss FAQ in one organiser session, see it in another, restore |
| Judge browser | One event sign-in → judge Home, multi-event → events; pane persists; rubric acknowledge, declare conflict/check teams, score one team → fresh counts; stage waiting/open/locked/results/ended states; no score button after lock; scoring remains available after mere close-time passage when existing stage/lock checks allow |
| Participant browser | Landing/count rules and event switching; no-team/pending/approved cases; each stage's existing Home state; filling summary/evidence and submitting updates shared rows; extension/settled incomplete state; baseline screenshots unchanged at desktop and 390px |
| Mixed roles/new user browser | Organiser A, judge B, participant C all listed with correct labels/routes; no-event creation/directory state; bare sign-in versus explicit `next=/events`, explicit Account and invite returns tested separately across email-code/OAuth/password/magic-link methods; safe returns preserved |
| Organiser judging regression | Click Score as a judge → judge Home with own queue, then scoring; no `/manage` bounce. Manage event returns. Test open/automatic/manual/panel permissions, another judge’s conflict with organiser having none (never shown as own), conflict, lock, archived, real-member admin and synthetic `admin_view` |
| Compatibility browser | Enumerate every BUILD `?s=`/`&tab=` and legacy fragment; aliases land on correct control, preserve `team`/tour arguments; Back/forward and 390px Menu/More work; unknown/malformed queries retain fallback |
| Pure rule unit tests | Table case for every catalogue row: done/todo, optional/not-needed precedence, absent/invalid/boundary facts, stage membership/carry/dedupe, immutable earlier items, next priority and schedule cue; all can combinations |
| DB/service tests | Every rule's real data path: content add/delete; default vs explicitly empty reminders; rules marker/legacy presets; dates/zones/date-only/midnight; track deletion; rubric fingerprint change; all four assignment modes; missing team tracks/panels; excluded/site-only/settled incomplete entries; conflicts/removed scores/former judges; one scored criterion out of several remains todo; organisers with no Open-mode scores are not required; former judges not required; same-day date-only dates and timed start/date-only end; zero queues/all-conflicted via candidate subtraction; override-and-publish, snapshot input change and republish; archived and backwards stages |
| Marks/ack DB tests | Migration fresh schema + rerun; event-wide dismissals shared after handover; factual done outranks mark; restore; required/unknown IDs refused; participants/judges/synthetic admin_view cannot dismiss; actual organiser members who are platform admins can dismiss; own-only acknowledgements, stale rubric version refusal, unversioned conflict acknowledgement; leave/rejoin, remove-person/rejoin, remove-co-organiser/rejoin and account deletion clear personal acks; archived writes refused |
| REST/MCP contract | Event fields unchanged plus additions; default vs judge-area checklist; `hack_get_event` projection/schema includes additions; `hack_get_team_status` preserves minimisation; REST and MCP item state/link/next equality; no hidden permission increase |
| Tours / embedded Apps (slice 1) | Update tours.js targets/copy for Event page, Settings and checklist-led next action; automatic/replayed organiser tours on desktop and phone, preserve participant anchors. Update apps/frame/organiser-nav.js resolver exports and frame links; destinations absent in-frame open the website. Embedded organiser App regression checks next action and Settings links, not just bundle freshness |
| Existing offline/release checks | Map/atlas freshness, layering, inventory/OpenAPI/doc sync, generated MCP bundle freshness and existing relevant integration/browser suites; no extra testing framework |

Use existing browser fixtures and `testdb` DB integration runner. Every checklist rule needs both a pure predicate case and a DB-backed fact-loading assertion; server truth must not be tested only with mocked browser JSON. Run appropriate checks during each implementation slice. This document itself does not claim those changes or tests are implemented.

## 10. Demo risks and owner-adjustable choices

| Risk for Nov 10–13 demo | Design limit |
|---|---|
| Old deep links / tours stop reaching forms | Aliases and stable screen IDs, preserved action handlers; compatibility matrix before either slice ships |
| New readiness blocks a rehearsed event | Guidance adds no new stage gate; keep manual Change stage and existing confirms; legacy entry-rules marker honoured |
| Organiser scoring breaks shell/leave behaviour | Ability-based area routing plus distinct membership identity; organiser-aware leave preview and real queue authorisation |
| Checklists miscount incomplete entries or former judges | Existing service eligibility, criterion completion and snapshot facts, no parallel SQL approximation |
| Optional ticks change email/scoring | Marks only affect guidance. Reminder settings, stage moves, locks and permissions stay explicit actions |
| Settings move is too broad to review at once | Reuse renderers with original save/lock semantics; first slice can be validated independently before checklist migration |
| Partial checklist rollout / stale client | Additive fields and migration before server release; keep participant progress and legacy next_step; absence of checklist retains current Home rendering during rollout, no fabricated completion |

Owner-adjustable choices (chosen here; none blocks implementation):

1. One arrival-eligible event opens its membership Home; multiple or zero open Your events. Explicit safe returns take precedence.
2. Five current rows and five past rows in the pane; archived alone means past, with full lists one click away.
3. Event page owns public content and Tracks; Settings owns rules and administration; activity screens keep operational actions.
4. Readiness is advisory; date-only events remain valid and explicit times are an optional recommendation.
5. Not needed is event-wide; rubric reading is versioned and personal; conflict acknowledgement is unversioned.
