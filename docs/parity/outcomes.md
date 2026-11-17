# Outcome parity

Decision 27 compares outcomes on the website, REST API and MCP. This matrix
records the current checks and their gaps; an exception does not claim that an
outcome was tested. A missing browser or replay check does not imply that the
feature is unavailable. No product behaviour changes here.

Run `make parity` with `TEST_PG_URL` set to a bare disposable Postgres server
URL on an explicit test port. It validates the matrix, builds both programs,
runs every golden scenario for both products, checks the exact recorded request
names and successful HTTP statuses, rejects JSON-RPC errors and MCP `isError`
results for referenced MCP calls, and runs the existing browser gate for both
products. The replay and browser fixtures create and remove their own databases
and test accounts.
The existing browser gate's explicit exceptions still apply: the owner-dashboard
fixture is unavailable. The command fails on any other browser failure.
It does not compare against a reference binary; that remains the split track's
separate golden comparison gate.

`scripts/parity/outcomes.json` is the machine-readable matrix. Each step occurs
once per product and each surface has either a `check` or a nonempty `exception`.
API and MCP checks include the golden `scenario`. API labels expanded by loops
also give the exact `source` template from `run.mjs`. The offline checker verifies
source references and the browser runner inventory; the replay check verifies
concrete request names and successful responses under the specified product and
scenario. MCP checks name actual replay calls, not just tools that happen to be
registered.

`bash scripts/check-parity-outcomes.sh` checks references without starting a
program. `bash scripts/parity/test.sh` runs the missing-surface, dangling-check
and valid-exception fixtures, plus replay fixtures for successful responses,
failed HTTP statuses, JSON-RPC errors and MCP `isError` results. Both checks, plus
shared deployment rendering, run in `make check`.

In the tables, `base/name` denotes a golden request in the base scenario;
other scenario names are explicit. Browser links identify existing scripts.
Every exception states whether the surface is deliberately absent or its
current automated check is missing.

## Simple Host

| Outcome | Website | API | AI app (MCP replay) |
|---|---|---|---|
| sign-in | Exception: No disposable owner sign-in fixture is wired into the browser gate; the shared sign-in card check stubs verification. | Exception: The Host replay mints its owner via the admin API; owner sign-in is not replayed. | Exception: Sign-in happens in the browser through connector authorization; agents do not handle sign-in codes. |
| publish | Exception: Publishing files is an API or agent operation; the website has no file editor. | `base/site1-create-tarball` | Exception: create_site exists, but the golden replay does not call it for this outcome. |
| update | Exception: Updating files is an API or agent operation; the website has no file editor. | `base/site1-update-files-v2` | Exception: update_site exists, but the golden replay does not call it for this outcome. |
| roll-back | Exception: The existing browser fixtures do not exercise the owner version rollback control. | `base/site3-set-active-v1` | Exception: rollback_site exists, but the golden replay does not call it for this outcome. |
| set-passcode | Exception: The existing browser fixtures do not exercise setting a passcode through the owner dialog. | `base/site1-lock-generate` | Exception: The replay calls passcode_screen begin only; it does not finish setting a passcode. |
| name-viewers | [e2e-named-viewers.mjs](../../scripts/e2e-named-viewers.mjs) | `base/private-create-named-viewer` | Exception: The replay calls list_site_viewers only; it does not add or remove viewers. |
| connect-domain | Exception: The existing browser fixtures do not exercise connecting a domain. | Exception: The replay uses reserved .invalid hosts and does not perform external DNS verification or certificate issuance. | Exception: connect_domain exists, but the golden replay does not call it for this outcome. |
| save-data | Exception: Hosted pages supply their own forms; the existing storage browser fixture edits resource rules, not application data. | `base/storage-kv-put` | Exception: storage_put_kv exists, but the golden replay does not call it for this outcome. |
| read-data | Exception: Hosted pages supply their own data views; the existing storage browser fixture inspects resource rules, not saved records. | `base/storage-kv-get` | `base/tools/call storage_get_kv` |
| delete-site | Exception: The existing browser fixtures do not exercise deleting a site from the owner dashboard. | `base/moderation-site-delete` | Exception: delete_site exists, but the golden replay does not call it for this outcome. |
| restore-site | Exception: The existing browser fixtures do not exercise restoring a site from Recently deleted. | `base/moderation-site-restore` | Exception: restore_site exists, but the golden replay does not call it for this outcome. |

## Simple Hack

| Outcome | Website | API | AI app (MCP replay) |
|---|---|---|---|
| sign-in | Exception: The navigation fixture starts with signed-in accounts; it checks routing after sign-in, not credential verification. | `base/account-organiser-signin-verify` | Exception: Sign-in happens in the browser through connector authorization; agents do not handle sign-in codes. |
| publish | Exception: Hack has held event and team sites rather than personal sites; team publishing has its own row below. | Exception: Hack has held event and team sites rather than personal sites; team publishing has its own row below. | Exception: Hack has held event and team sites rather than personal sites; team publishing has its own row below. |
| update | Exception: Held-site updates are supported through the API and agent; the replay does not update an existing held site and there is no website file editor. | Exception: Held-site updates are supported through the API and agent; the replay does not update an existing held site and there is no website file editor. | Exception: Held-site updates are supported through the API and agent; the replay does not update an existing held site and there is no website file editor. |
| roll-back | Exception: The replay does not roll back a held site; the existing browser fixtures do not exercise version rollback. | Exception: The replay does not roll back a held site; the existing browser fixtures do not exercise version rollback. | Exception: The replay does not roll back a held site; the existing browser fixtures do not exercise version rollback. |
| set-passcode | Exception: Hack team sites use event gates; personal-site passcode management is not offered. | Exception: Hack team sites use event gates; personal-site passcode management is not offered. | Exception: Hack team sites use event gates; personal-site passcode management is not offered. |
| name-viewers | Exception: Hack team sites use event membership; personal-site named-viewer management is not offered. | Exception: Hack team sites use event membership; personal-site named-viewer management is not offered. | Exception: Hack team sites use event membership; personal-site named-viewer management is not offered. |
| connect-domain | Exception: Hack uses event address setup; external DNS verification and certificate issuance are outside the local replay and browser fixtures. | Exception: Hack uses event address setup; external DNS verification and certificate issuance are outside the local replay and browser fixtures. | Exception: Hack uses event address setup; external DNS verification and certificate issuance are outside the local replay and browser fixtures. |
| save-data | Exception: The existing Hack storage browser fixture configures resources; it does not save application records. | `mixed-store/file-set` | Exception: Hack storage tools exist, but the golden replay only sets/lists resources; it does not save/read application records through MCP. |
| read-data | Exception: The existing Hack storage browser fixture inspects configuration; it does not read application records. | `mixed-store/file-get` | Exception: Hack storage tools exist, but the golden replay only sets/lists resources; it does not save/read application records through MCP. |
| delete-site | Exception: Held-site lifecycle belongs to the event. The replay covers operator deletion for the take-down regression, not ordinary owner deletion. | Exception: Held-site lifecycle belongs to the event. The replay covers operator deletion for the take-down regression, not ordinary owner deletion. | Exception: Held-site lifecycle belongs to the event. The replay covers operator deletion for the take-down regression, not ordinary owner deletion. |
| restore-site | Exception: The replay tests refusals when team credentials try Recently deleted restore; it does not prove a successful ordinary owner restore. | Exception: The replay tests refusals when team credentials try Recently deleted restore; it does not prove a successful ordinary owner restore. | Exception: The replay tests refusals when team credentials try Recently deleted restore; it does not prove a successful ordinary owner restore. |
| create-event | Exception: The existing browser fixtures do not exercise the new-event form through successful creation. | `base/event-create` | Exception: hack_create_event exists, but the golden replay does not call it for this outcome. |
| join | Exception: The existing browser fixtures do not exercise a new participant accepting the event join form. | `base/participant-join-0` | Exception: hack_join_event exists, but the golden replay does not call it for this outcome. |
| form-team | Exception: The existing browser fixtures do not exercise creating a participant team. | `base/team-a-create` | Exception: hack_create_team exists, but the golden replay does not call it for this outcome. |
| publish-team-site | Exception: Publishing team files is an API or agent operation; the website has no file editor. | `base/team-a-site-deploy` | Exception: The replay reads the team site with get_site but does not publish it through an MCP call. |
| submit | [e2e-hack-checklists.mjs](../../scripts/e2e-hack-checklists.mjs) | `base/entry-a-submit` | Exception: hack_submit_entry exists, but the golden replay does not call it for this outcome. |
| judge | Exception: The browser fixtures open the scoring screen but seed scores through the API; they do not save a score through the website. | `base/judge-score-team-a` | Exception: hack_score_team exists, but the golden replay does not call it for this outcome. |
| publish-results | [e2e-hack-checklists.mjs](../../scripts/e2e-hack-checklists.mjs): republishes through “Publish again,” completes the notification choice, and verifies the updated checklist state. Gap: initial publication is seeded through the API and is not exercised through the website. | `base/results-publish` | Exception: hack_publish_results exists, but the golden replay does not call it for this outcome. |
