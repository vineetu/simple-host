# Simple Hack completion

Status: core P1 is deployed and the isolated M6 rehearsal is complete, 2026-10-01.
External provider, monitoring and directory-submission prerequisites below remain
open. This document tracks the approved continuation; completed fixture work is
recorded in [the M6 rehearsal](../history/simple-hack-m6-rehearsal-2026-10-01.md).

## Deployed product checkpoint

Simple Hack M0–M5 and the administration release are live. On October 1 the
combined P1 binary reached production at `98f0f52` with 44 recorded migrations
in both databases. Independent client checks passed the Hack home, directory
page and API, event and team host routing, health and readiness, the neighboring
Simple Host site/person hosts, and the legacy redirect. Both services were active
with no error journal entries in that verification. The local M6 fixture did not
send production mail or create production events.

The deployed P1 work includes event content, announcements and entry receipts;
custom signup questions, approval and tracks; manual judging and track panels;
raw/normalised scoring with a preselected tie rule and recorded organiser choice;
optional signed-in people's-choice voting; and the public event directory. The
ten organiser MCP tools and separate team publishing connection remain scoped as
before. Browser and REST workflows extend the product beyond those ten tools.

## Verification completed

- The isolated M6 run used one creator, a retained co-organiser, six teams of
  three, four judges with one conflict, and 15 canonical voters. The cast signed
  in through actual browser email-code flows backed by a local mail sink. Core
  join, team, entry, score, tie, visibility and archive actions used the browser
  UI; the first six site publishes used scoped API calls, and Team 1 was also
  published through a real same-event browser OAuth/MCP consent flow.
- The 40/40/20 rubric produced 69 raw criterion rows and independent weighted
  team totals of **84, 84, 64, 60, 40, 20**. Design and judge preferences left the
  top two tied; the organiser recorded Team 2 first. Deadline pinning, late
  publish refusal, score autosave and retry, Previous/Next, lock refusal,
  private team comments, normalised preview, and public rank/score controls
  passed. Event, directory and final result pages were checked at 320/390 px
  in light and dark themes; sponsor spacing and narrow winner names were fixed.
- Fifteen unique votes survived a change and a plus-tag alias attempt. Same
  choice, own-team and alias-own-team votes were refused. Pending/rejected
  members failed the member-only eligibility gate. Counts stayed hidden while
  open, and the final public ranking retained 15 votes after archive and site
  removal. Directory draft exclusion, default listing, opt-out, Upcoming and
  Past transitions passed.
- The archive warning ran on the fixture's actual startup timer: one message
  reached the local sink while sites remained live. After the keep period, all
  six team sites moved to Recently deleted. The event page, published results,
  69-row export and vote ranking remained; a separate neighbor's project and
  nested file stayed live. See the [M6 report](../history/simple-hack-m6-rehearsal-2026-10-01.md)
  for the UI/API split and precise limits.
- A quiescent, populated pre-removal M6 database and file tree restored with
  44 migrations, 69 score rows, 15 votes, a published result, seven live site
  directories, ten versions and six pinned team versions. Encrypted scratch
  transfer and private readback matched the dump, 25 regular files, two relative
  handle links and unusual uploaded filenames. This proves fixture recovery,
  not an atomic future production nightly snapshot. The [operations record](../operations/simple-hack.md#populated-m6-fixture-recovery-october-1)
  distinguishes that proof from the current production mirror and older dump.
- The independent full-platform `hack-v0.8.1` image and ZIP/tar downloads are
  public. A real published v0.8.0-to-v0.8.1 local upgrade applied five migrations
  (39 to 44), preserving the event, team key, project, settings and TLS certificate.
  The v0.8.0 DigitalOcean installation, trusted HTTPS, first-login Packer snapshot,
  persistence and same-image upgrade were verified and cleaned up. Local Traefik/Coolify routing with TLS passthrough and persistent data
  passed; deployment through a real Coolify UI/API remains unverified. The
  enterprise existing-cluster Helm/YAML installation was verified separately.

The dedicated reviewer password sign-in and sample draft/results events are
prepared on production. The account is ordinary, both events stay out of the
directory, and the setup queued no email. Credentials stay in a root-private
file for later secure portal entry.

## External work still open

1. The Hack-only daily mail watch is installed and its first report passed.
   It reports recipient-free accepted/send-failure counts. Provider
   bounce status needs a **separate Resend Full access monitoring key**; the
   current send key returned 403 on the read endpoint. Unavailable is not zero
   bounces. Check a later production backup containing real project versions
   against the mirrored files; the fixture proof does not establish atomic
   nightly production capture.
2. Deploy and test the full-platform package through a **real Coolify UI/API**
   with public certificates and persistence. The local Traefik routing proof is
   narrower. A Coolify catalog template and a DigitalOcean Marketplace listing
   each have separate provider requirements and approvals; neither is live.
3. Finish the Simple Hack plugin listing prerequisites: choose country targeting,
   record an exact-package reviewer demo, provide the prepared dedicated reviewer
   account through secure portal fields, run the declared review cases, and
   complete portal upload and checks under the intended verified publisher.
   The toolkit downloads and Claude GitHub install path are public, but no
   OpenAI/Claude directory approval or marketplace publication follows from a
   ZIP or repository alone. See [submission preparation](../../hack-toolkit/SUBMISSION.md).

Do not describe these external steps as shipped until provider or portal
evidence exists. Keep future production checks separate from the local M6 cast.
