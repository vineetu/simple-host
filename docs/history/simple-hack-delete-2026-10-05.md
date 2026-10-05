# Simple Hack event deletion

Status: implemented, checked, deployed and released 2026-10-05.

The owner asked for a destructive option in Settings. Every current organiser
can delete in any stage, including Ended, through the existing typed in-page
prompt. Busy or ended API calls need matching `confirm`; empty, unended no-body
calls retain their behavior. An explicitly wrong confirmation is always refused.

Team sites use the existing trash path, followed by the existing holding-account
and file erasure. Recently deleted does not reconstruct an event's memberships,
entries, scores or results after account erasure; deletion therefore has no undo.
No email is added. Name reservations use the existing `event_used_names` table
on participant/judge join, on removal of an older imported member, and at deletion
of a populated older event. No schema change is needed.

The permission and connector review checked the existing organiser role gate,
nonmember/wrong-role 404 behavior, the existing take-down restriction, and the
current-member recheck inside the deletion transaction. The event row stays
locked against joins until confirmation and deletion complete. Connector calls
use the same REST route and preserve its confirmation and role refusals;
`destructiveHint` remains true. Personal accounts survive deletion; only the
event's holding account is erased. This was an in-session code and test review,
not an independent external review.

Database tests use only a disposable Docker Postgres with DB_DSN set. The busy
fixture contains a co-organiser, participant, judge, team, deployed team site,
custom event website, entries, criterion scores, published results, a vote, KV
resources and runtime file bytes. They verify refusal without confirmation,
wrong confirmation, outsiders/participants/judges refused, co-organiser deletion
after archive, name reservation, deleted hosts, private/public API access,
Your events/directory exclusion, account/row/file removal, invalid old invitations
and an HTML Event not found page. Separate tests cover each stage and a
participant/judge removed before any team existed, including imported members.
The existing admin delete and empty-event compatibility tests still pass.

Playwright used the specified local installation and Chromium at 390 and 1280 px
in light and dark. Each state created an event through the browser, joined a
participant, formed a team, checked the danger zone while busy and ended, refused
a mistyped slug, cancelled with Escape and checked focus return, then deleted
through Settings. Your events, directory, participant access and the old public
link were checked after deletion. All four states passed without page errors or
horizontal overflow. Screenshots were inspected. Only throwaway accounts/events
were used and removed.

Assumptions: “joined” means a participant or judge, matching the old non-organiser
rule. Existing platform take-down restrictions still apply. Historic removed
people without a team or surviving reservation cannot be reconstructed; their
old behavior is retained. Standalone installers share the feature; Simple Host
and Enterprise do not gain an event-delete surface. No marketplace or directory
submission is authorized by this task.

`make check` passed with the disposable Postgres, including database-backed
handler and connector tests; check-docs-sync and check-features passed. The
Postgres container was removed. Canonical skills are 0.27.26 and the first-party
Hack toolkit/reader is 0.2.9; no marketplace or plugin-directory submission was
made. The existing public toolkit site was updated through the Simple Host
connector, preserving every previous download.

Feature commit d6b718a was rebased onto origin/main and fast-forward pushed while
holding the deployment lock as ubuntu. Both services passed health, readiness,
public pages, hosted-site routes, missing-event HTML and startup-journal checks.
The live Settings flow created, joined, formed a team and deleted only a
throwaway event; its organiser and participant accounts were also removed. An
initial deployment verification failed because the journal checker treated an
empty no-match count as failure; rollback restored the previous binary. The
corrected check and redeployment passed. Five binary backups are kept.

The hack-v0.8.6 tag/release points at d6b718a. Public ZIP/tar downloads match
SHA256SUMS and each other's file bytes; both images pull anonymously. Native
arm64 and the extracted amd64 binary report the release version and commit.
The installer input/config tests pass. A fresh published-package install records
52 migrations. A real published v0.8.5 package/image upgrade applies two
migrations and retains event/member/entry/team-site data, domain, credentials,
settings and local TLS CA. Own-domain presentation, Settings deletion at 390 and
1280 px in light/dark and real local Coolify Traefik routing pass. Test containers,
volumes and fixture accounts were removed. Installer pins were changed only
then. Cloud deployment evidence remains dated to its earlier verified releases.
