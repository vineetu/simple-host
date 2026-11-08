# Simple Hack forms

`internal/webassets/static/hack/forms.js` owns snapshots, dirty hints, Save/Discard/Stay,
field errors, status replacement, and draft preservation. `ui.forms.bind(ctx, buttonOrForm,
save, options)` registers a section with its existing async save callback. `register` also
supports custom snapshots for tags, rubric rows, rules, questions, and reminder chips.
Return `false` (or `{ok:false}`) when saving fails or its confirmation is cancelled.
When a successful save callback navigates, pass its handle as `ctx.go(path, {saved: handle})`;
the guard still checks the other forms and guards user navigation during an in-flight save.
`ui.toast`/`ui.live` route form messages into that form's one status slot. API refusals
use their existing field, code, or missing list to mark a control when possible.

A registration expires when its ctx dies or its root is detached. A keyed registration
replaces its previous render. Save/Discard handles dialogs first and rechecks current
registrations after each save or repaint. Local repaints use `preserve` to retain other forms' drafts.
Changes made during an awaited save remain dirty until a later save. Screen navigation
uses `app.js:go`; Back/Forward restores the history cursor while the guard asks.
Sign-out uses the same guard before ending the session. `beforeunload` uses the browser's
own warning because browsers do not permit a custom Save/Discard/Stay dialog there.

Form dialogs use `ui.dialog` and `forms.dialogForm`, including the few sheets with a
dynamic footer. Cancel, Escape, and a scrim click share the guard. A dialog with a save
callback offers Save; Send-only dialogs, choices, and typed confirmations without a separate save offer
Discard/Stay. A successful explicit confirmation still runs its existing action.

## Input inventory

This list comes from searching `screens/*.js` for input, textarea, select,
contenteditable, DOM-created controls, and save/submit bindings. Helper files are listed
because they own several forms rendered by other screens.

| Source under `static/hack/screens/` | Coverage |
|---|---|
| `participant-entry.js` | One shared form for summary, links, description, tags, track, and maturity. Save draft and Submit clear old errors. Missing-item links focus marked fields. Screenshot upload/removal saves immediately and retains typed entry text. |
| `participant-website.js` | ZIP publish and notification creation use separate registered forms. Polling and instruction tabs preserve drafts. Copy-only addresses and keys are read-only. |
| `organiser-event-page.js` | Basics and About use registered forms. Schedule, Sponsors, and FAQ use shared form dialogs. Look's icon, colour, and gallery controls, and Website upload/mode controls save on change and share errors/status without dirty hints. |
| `_event-tracks.js` | Add/Edit track uses the shared form dialog. |
| `_settings-entries.js` | Deadline, entry rules/presets, and reminder chips use registered forms. Their baselines survive row/card repaints. |
| `_settings-judging.js` | Rubric rows, tie criterion, judging mode, judges per team, and judging close time use registered forms. Rubric reorder/add/remove participates in the shared snapshot. |
| `_settings-people.js` | Registration questions use a keyed form across Add/Remove. Approval saves immediately. The saved question payload retained for the approval request is server data, not separate dirty tracking. |
| `_settings-results.js` | Ranking, visibility, voting window, voter eligibility, and directory listing each have their own registration. Voting forms include only their own fields. |
| `_settings-teams.js` | Team size uses the shared hint and discard. Team finding saves immediately. |
| `_settings-admin.js` | Organiser details and notification creation use registered forms. Application availability saves immediately. Typed delete confirmation offers Discard/Stay when abandoned. |
| `account.js` | Name, new email, verification codes, and password use registered forms. Preferences save immediately. Abandoned email/code/delete sections and typed confirmations are guarded. |
| `participant-team.js` | Team creation, join by code, looking-for details, and team name/details use registered forms. Track changes save immediately; local repaints preserve the other drafts. |
| `organiser-team-detail.js` | Rename uses a registered native form. Add someone and team messaging use guarded dialogs. Lock/link/member actions preserve the rename draft. |
| `organiser-create-event.js` | The native creation form is registered; both submit buttons use its save. |
| `organiser-announcements.js` | New announcement is a registered form; Edit uses a shared form dialog. |
| `_team-message-dialog.js` | Message body and Send use the shared form dialog. |
| `_stage-announce-dialog.js` | Suggested title, message, email choice, and Send use the shared form dialog. |
| `organiser-entries.js` | Team/deadline extension inputs use a registered form. |
| `organiser-entry-detail.js` | The optional takedown reason and action use a registered form. |
| `organiser-judging.js` | Unlock reason, conflict selectors, and assignment generation choices use registered forms. |
| `organiser-judging-assignments.js` | Manual and panel grids use one registration with Save judging access. |
| `organiser-results.js` | Tied-place inputs use a registered Save order and publish form. |
| `judge-scoring.js` | Score choices save immediately. The debounced comment is guarded until its save succeeds, including failed saves and edits made during a request. |
| `public-join.js`, `public-judge.js`, `public-organiser-invite.js`, `public-apply.js` | Name, conduct, application details, and registration answers use registered native forms. |
| `organiser-teams.js` | Team message selection is a list selection, not unsaved form data. Message, Fill teams, and placement dialogs use the shared dialog guard. |
| `_orgA-util.js` | `formDialog` and the dynamic Change stage picker use the shared dialog lifecycle. |
| `organiser-settings.js` | The section select navigates through `ctx.go` and restores its selection on Stay. |
| `organiser-people.js` | Inputs are list filters/sort controls. They do not edit saved data and need no dirty state. |
| `organiser-overview.js`, `_settings-links.js` | Inputs are read-only copy fields. |
| `public-team-link.js` | No editable inputs; the Join button is an immediate action. |
| `judge-rubric.js` | Read-only rubric with an immediate acknowledgement button; no editable inputs. Rubric editing is in `_settings-judging.js`. |

The remaining screens have no editable controls. `leave.js` and `ui.confirmTyped`
are outside `screens/`; their input dialogs also use `forms.dialogForm`.

## Review and verification

The [atlas new-screen checklist](../ux-atlas/README.md#new-screen-checklist) is the review
checklist for forms. `scripts/tests/hack-forms.test.mjs` checks the pure state helpers.
`scripts/e2e-hack-forms.mjs` drives the opt-in `TestServeHackFormsBrowser` fixture with real
Hack API writes and current source modules. Its header shows the fixture commands; set
`PLAYWRIGHT_DIR` to a node_modules directory containing Playwright when the default
`/tmp/tsx/node_modules/` is unavailable. Use Chromium at `/usr/local/bin/chromium`.
