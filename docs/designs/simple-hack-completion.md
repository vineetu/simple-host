# Simple Hack completion

Status: approved continuation, in progress, 2026-10-01. This tracks the remaining
work from the original Simple Hack plan and the owner's current request. It does
not describe all of these features as shipped.

## Current checkpoint

Enterprise existing-cluster Helm/YAML installation is shipped and verified.
Simple Hack M0–M4 is live. M5 adds the hosted organiser skill and organiser MCP
connection; its review includes a real six-team, four-judge browser rehearsal.
M5 is deployed and verified. The full M6 rehearsal follows the P1 features it depends on.
Administration and the setup ZIP are deployed in commit `7d6d369`; focused real-database
tests, phone browser checks, fresh migrations, full `make check` and CI passed.
The independent `hack-v0.8.0` release now has public multi-architecture images and
ZIP/tar downloads. Real DigitalOcean installation and a private Packer snapshot build, first boot,
trusted HTTPS and persistence are verified and cleaned up. A real Coolify server
and Marketplace approval remain unverified. The Simple Hack toolkit site is live with downloadable draft submission
packages; reviewer demonstration and external submissions remain pending.

## Work checklist

1. Done and deployed — M5: review organiser consent and token scopes, test organiser and team
   MCP connections in browsers, sync the skill/API documentation, run full checks,
   publish, deploy and verify from the client.
2. Done and deployed — event administration: co-organisers; team rename/edit controls;
   participant, team and entry CSV exports; all-team project archive and a team's
   own download; event storage/usage. Existing scores/results CSVs stay.
3. Integrated, awaiting final release — event content and communication: sponsors, FAQ, schedule with now/next in the
   event's time zone, announcements with participant email, entry receipts and
   deadline countdown. Use the existing mail sender; rehearsals use a local sink.
4. Integrated registration/tracks; panels in progress — registration and organisation: custom signup questions and approval;
   tracks/challenges, track prizes and team choice; manual judge assignment and
   panels by track. Waitlists and other P2 work are outside this continuation.
5. In progress — judging: optional normalisation and tie rules chosen before judging. Keep
   published snapshots and private team results consistent with the chosen mode.
6. Integrated, fifteen-voter browser checks passed — people's choice: signed-in voters, one changeable vote per canonical account,
   no self-vote, explicit opening/closing and eligibility settings.
7. Integrated, awaiting final release — public directory: now, upcoming and past events; listed by default with an
   organiser opt-out; exclude drafts and taken-down events.
8. Partly verified — operations from the saved P1 list: verify nightly database/data backups and
   email-volume/bounce visibility for the separate Hack instance. Encrypted current
   file readback and populated database recovery passed. The local send-volume
   and optional provider-status watch is implemented; live bounce reporting needs
   a separate Resend Full access key. Populated project-file recovery is next.
9. Running on an isolated instance with a local mail sink — full M6 rehearsal: one organiser, one co-organiser, six teams of three, four
   judges with one conflict, fifteen voters; 40/40/20 rubric; API and connector
   publishing; deadline pin and late refusal; phone scoring/Previous/retries;
   tie, score lock, results privacy; duplicate/self-vote refusal; CSV hand totals;
   archive warning/removal with a test clock and unaffected neighbouring data.
10. Published and verified; provider listing prerequisites remain — independent full-platform installation: DigitalOcean and Coolify first,
    following the workspace-4 small-box work but installing the complete event
    platform. Verify installation, sign-in, event/team publishing, persistence
    and upgrade. Document provider/manual prerequisites truthfully; an installer
    file is not evidence that a marketplace listing or cloud deployment is live.

## Verification already completed locally

- Native email-code browser sign-in into a local mail sink; six teams of three
  and four judges, with real published project files.
- 320/390 px phone scoring, autosave, Previous/Next, one conflict and locked-score
  refusal. The 69 raw score rows yield 84, 84, 64, 60, 40 and 20 as hand calculated.
- Team results remain hidden until publication; each team then gets only its
  own scores/comments. The organiser resolves the first-place tie; anonymous
  winners-only and full-ranking views follow the visibility setting.
- Archive preserves the event and winner. A real-files/HTTP retention test sends
  one warning, keeps the site during the warning window, moves it to Recently
  deleted at expiry, and leaves another event's project intact.

The rehearsal found and fixed a wrong project slug in the judge queue, which
removed the deadline-version link from the scoring screen. The score screen now
shows criterion weights and points ranges. A regression reproduces the original
missing-link bug and passes with the fix.
