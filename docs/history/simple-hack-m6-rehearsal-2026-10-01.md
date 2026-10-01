# Simple Hack M6 isolated rehearsal — 2026-10-01

This was an isolated local fixture on `hack.test`, with its own database and a local email sink. It did not use production accounts, mail, or event data. Keys, mail contents, and account artifacts are excluded from this report. The release received separate client verification after the rehearsal; the fixture results below do not claim that the test event existed in production.

## Cast and entry

- One creator and one retained co-organiser; six teams of three approved participants; four judges; 15 distinct verified voter mailboxes. Two extra applicants were kept pending/rejected and did not count toward the 18 participants.
- Native browser email-code sign-in for the cast, event creation, rubric and signup settings, participant join and required answers, six team formation/join flows, entry saves, judge joins, one-time co-organiser invite acceptance, scoring, conflict, lock, tie order, results visibility, and archive.
- Organiser approval decisions, tracks and track choices, content setup, and six initial site publishes used authenticated browser API calls. Team 1 was also published through a real same-event browser OAuth/MCP connection; organiser tools were unavailable to that team connection.
- Team 1 retained `node_modules/fixture.txt` and `nested/entry.tmp` alongside its MCP index marker. The separate neighbor event had its own published project and `nested/keep.txt`.

## Pre-judging and voting

- Two tracks, a 40/40/20 rubric, required custom signup question, approval gate, public schedule/sponsor/FAQ/announcement, six complete entries, and six team sites. The local mail ledger recorded 18 announcement deliveries and six entry receipts.
- Pending and rejected members could not create teams or vote under member eligibility. An outsider could not vote under member eligibility. All-signed-in voting then accepted exactly 15 distinct voters. Same-choice repeat returned 409; changing a vote returned 200. A plus-tag alias could not create a second vote; own-team and own-team-alias votes returned 409. Cross-event team voting returned 404.
- Public gallery exposed six projects but no count while open. Upcoming/closed windows rejected writes. The closed public ranking counted 15 canonical votes and remained available after archive and cleanup. Directory excluded drafts, respected opt-out, showed the live main event under Now, the future neighbor under Upcoming, and archived main under Past.
- Co-organiser invite was single-use; removal revoked management access while the creator and original co-organiser remained.
- Private exports contained 18 approved participant rows, six team rows, six entry rows, and project files. Participant own-team archive worked; all-project archive and usage were denied to participants.

## Judging and publication

- Four judges were assigned to both track panels before judging, and Design was selected as tie criterion before judging. A late team site update returned 409 after the submission deadline; judge links pointed to the pinned deadline project.
- Phone judge screens at 320/390 px saved individual scores, supported Previous/Next, and recorded one conflict. Score retry before lock returned 200; after lock, score edit returned 409.
- Independent calculation of the 69 exported criterion rows (23 judge-team pairs) gave raw weighted means **84, 84, 64, 60, 40, 20**. Team 6 had three judges because of the conflict. Team 1 `[5,4,3]` and Team 2 `[4,5,3]` stayed tied after Design and pairwise rules; organiser chose Team 2 through the UI and that decision was recorded.
- Normalised preview returned both raw and normalised totals. The organiser restored raw before publication. Published CSV retained all 69 rows and six hand-checked totals. Before publication, team results exposed no scores/comments; afterward Team 1 saw its own 84 and four judge comments.
- Public winners-only view showed Team 2 overall and Team 1 as civic track winner. Full ranking showed six places; public rank and score toggles hid/revealed those fields. Final setting returned to winners-only with ranks and scores enabled.
- A late-stage UI bug initially sent an unchanged tie criterion when switching score modes, returning 409. The integrated UI was fixed and retested on the locked event: normalised/raw switching worked and the Design selector stayed frozen.

## Archive, responsive layout, retention

- Archive was performed in the organiser phone UI. The event could not reopen. Team sites remained live during retention; archived event page, results, vote ranking, and directory listing remained public.
- Event page, directory, and final results were checked at 320/390 px in light/dark. No horizontal overflow. Sponsor spacing and narrow winner-name wrapping found during rehearsal were fixed and verified in the final screenshots.
- A quiescent pre-removal database and site-file checkpoint was restored in a networkless PostgreSQL 16 container and a separate restored fixture database. The checks found 44 migrations, 69 score rows, 15 votes, six receipts, one published result snapshot, seven active site directories, ten version records, and six pinned team versions resolving to captured files. An encrypted scratch transfer with `--links` passed download/check/readback: the dump and all 25 regular files matched, including two relative handle links, the two unusual Team 1 filenames, and the neighbor's nested file. The scratch prefix was removed afterward. See [Simple Hack operations](../operations/simple-hack.md#populated-m6-fixture-recovery-october-1) for method and limits.
- With only the archived main event clock moved to warning age, the actual startup cleanup sweep sent exactly one notice to the local mail sink and left its sites live. With its clock then moved past the 30-day keep period, a second actual sweep moved all six main sites to Recently deleted (HTTP 404). The main event page, six-team published results, 69-row score CSV, and 15-vote ranking remained. The separate neighbor project and nested file still served HTTP 200. The source fixture is intentionally post-removal. Restore verification used a pre-removal checkpoint; temporary sensitive copies were removed after the proof.

## Evidence files

The local fixture retained screenshots at 320/390 px in light/dark, raw and results CSVs, archives, and browser scripts. These private local artifacts are not shipped with the product. This report records the checks without including sign-in codes, API keys, mail contents, or private application answers.
