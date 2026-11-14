# Starting evidence for the split and rules tracks (slice D0-S4)

Taken on 2026-11-13 between 00:24 and 02:23 UTC at `origin/main` `ea1f87b1`
(the commit production runs: `hack-v0.8.20-20-gea1f87b1`), in a clean worktree.
Everything here was run; nothing is carried over from an earlier report.
Before this file was pushed the gate was run again on `5723dca3` (main after
D1-S3, D1-S5a and D1-S6): every step passed, the database suite with 4262
tests, 0 failures.

## The one command

```
bash scripts/split/check-all.sh
```

Gate 1. It reads the step list from `make -n check`, runs every step even
after one fails, and exits 0 only when the failing steps and failing test
names equal `scripts/split/known-red.txt` exactly. It puts itself under the
repo's 2G memory cap (`sudo systemd-run ... -p MemoryMax=2G`); `--no-cap`
runs without it and says so in its last line, which is then not a gate
result. Other forms:

| Command | What it is |
|---|---|
| `bash scripts/split/check-all.sh --golden --slice <id>` | gates 1 and 2: adds the golden comparison as D0-S3a/D0-S3b define it (build the candidate, `run.mjs --host ... --out ... --scenario all` for the reference and the candidate, `diff.mjs <ref> <cand> --slice <id>`; the reference skips each scenario that a named slice's approval file adds whole). A slice whose text says "Golden: none" leaves `--slice` out, and any difference then fails. The reference is `$GOLDEN_REF_BIN`, default `/usr/local/bin/simple-host`, the installed program. It refuses to run until the D0-S3 harness is in the tree. Tried once against that harness as it stood, unmerged, in its builder's worktree: installed program against a build of this tree, 676 requests in 2 runs, identical; each replay took about 60 seconds at a load near 10. |
| `bash scripts/split/check-all.sh --browser` | gate 3's scripts through the same wrapper, compared with `browser-known-red.txt`. Do not set `TMPDIR` to a long path: Chromium aborts with "Socket path too long". |
| `bash scripts/split/check-all.sh --skip scripts/test-integration.sh --skip scripts/check-fresh-install.sh` | every step that needs no database |
| `bash scripts/split/check-all.sh --only <step id>` | re-run one step alone, e.g. to see whether a failure is a flake |
| `bash scripts/split/check-all.sh --list` | step ids and commands |

A failure that is not in the list is re-run alone before anything else. If it
passes alone it is a flake: report it, do not list it. `known-red.txt` holds
only failures that reproduce on untouched main.

A failure is named as exactly as the output allows: a Go test as
`<package>:<Test>/<subtest>`, a Node test by its path, a killed or timed-out
step as `exit:<status>`, a Go run that died inside the database suite as
`go-test-exit:<status>`, a step with no test names as `*`. A listed test
never covers a different test, subtest or crash in the same step. One limit
remains: `scripts/check-fresh-install.sh` prints only the last 20 lines of
its Go output, so a second failing test in that step can be cut off before
the gate sees it. Do not keep a known-red entry for that step for long. The script has
its own test, `bash scripts/split/check-all_test.sh` (fake steps only, a few
seconds). It is not wired into `make check`: the Makefile is outside this
slice's files.

## What is red on main today

**Gate 1 (`make check`): nothing.** `known-red.txt` has no entries. All 27
steps passed in four full runs (three through `check-all.sh`, once as plain
`make check`), and the database suite passed six times out of six
(4216 pass, 0 fail, 22 non-database skips, 0 database skips each time).

The candidates named in the brief and the coordination file are all green now:

| Candidate | Result at `ea1f87b1` |
|---|---|
| `TestNumberedGuard` under `scripts/check-fresh-install.sh` | pass (step passed four times) |
| `deploy/hack/standalone/tests/install_test.sh` ("Missing mail provider accepted") | pass (3 runs) |
| `scripts/e2e-hack-checklists.mjs` | pass |
| `scripts/e2e-hack-presets.mjs` | pass |

**Gate 3 (browser): one entry**, in `browser-known-red.txt`:
`scripts/e2e-owner-dashboard.mjs` cannot run as shipped (listed as
`no-fixture`, so any other failure of that script is still a new failure). It needs a local
server holding an account `owner` with 174 sites and a second account
`bee-01`, and nothing in the repo builds that. Owner: pre-existing; whoever
first needs that script writes its fixture and deletes the entry.

**One flaky test, not listed:** `TestMatrixAuthorArchivesGroupPostsOnce`
(`internal/wpimport/matrix_test.go:1183`) asserts a wall-clock limit of 3 s.
It failed in a no-database `make test` while another session pushed the box
to a load of 17 to 49 ("20,000 sparse author archives rescanned all posts:
4.44s"), failed 1 of 3 runs alone at load 47, and passed in all seven full
runs at a load of 4 to 23. It is load, not a defect; a proper fix counts work
instead of time and belongs to no split slice.

## Every step of `make check`

One capped `check-all.sh` run, load average 2.4 to 8.1 (mean 4.1). The same
numbers are in `evidence/check-all.summary.tsv`.

| Step | Result | Seconds |
|---|---|---|
| gofmt | ok | 1 |
| go build | ok | 3 (warm build cache) |
| go vet | ok | 3 (warm) |
| scripts/test-integration.sh | ok | 537 |
| node --test | ok | 2 |
| scripts/check-html.sh | ok | 1 |
| scripts/check-layering.sh | ok | 3 |
| scripts/check-docs-sync.sh | ok | 15 |
| scripts/check-claude-plugin.sh | ok | 0 |
| deploy/cert-issuers/runtime_test.sh | ok | 2 |
| deploy/cert-issuers/requeue_test.py | ok | 0 |
| deploy/cert-issuers/watch_test.sh | ok | 0 |
| deploy/cert-issuers/fallback_test.sh | ok | 3 |
| deploy/cert-issuers/google_pacing_test.py | ok | 0 |
| deploy/domain-certs/issue_test.sh | ok | 11 |
| deploy/family-certs/issue_test.sh | ok | 10 |
| deploy/prod/family-adopt_test.sh | ok | 4 |
| deploy/site-certs/site-certs_test.sh | ok | 4 |
| deploy/prod/nginx-suspended-marker_test.sh | ok | 3 |
| deploy/prod/nginx-analytics-logformat-apply_test.sh | ok | 0 |
| deploy/prod/sh-network-watch_test.sh | ok | 0 |
| deploy/prod/sh-uptime-watch_test.sh | ok | 0 |
| deploy/install/install_test.sh | ok | 1 |
| deploy/hack/standalone/tests/install_test.sh | ok | 1 |
| deploy/digitalocean/droplet/test/pins_test.sh | ok | 0 |
| scripts/check-fresh-install.sh | ok | 16 |
| scripts/check-prices-age.sh | ok (prints its monthly warning; never fails the step) | 0 |
| **Total** | | **620** |

Checks nested inside those steps ran with them: `check-docs-sync.sh` runs
`check-hack-setup.py`, `build-hack-navigation.py --check`,
`tests/docs-site.test.py`, `check-docs-site.py`, `check-openapi-coverage.py`,
`web/analytics-parse.test.js`, `web/auth-base.test.js`, `check-features.sh`,
`settings_docs.py --check --local-only`, `check-profiles.sh`,
`check-features-page.py` and `docs/ux-atlas/build.py --check`;
`check-fresh-install.sh` runs `go test ./db/migrations/` against three
throwaway databases. There is one Go module; no nested module exists yet.

## Checks that `make check` does not run

| Check | Result | Seconds | Note |
|---|---|---|---|
| `make check-release` | pass | 12 | Enterprise settings comparison, nginx and Caddy fixtures. The assistant eval inside it was skipped (no model settings). MCPJam is a separate lane and was not run. |
| golden run, today's harness (`run.mjs BIN OUT.json`) | completes | 10 | 116 requests. Compared with the checked-in `baseline.json` it shows 2887 differences: that file was recorded at `34f68ed2` and D0-S3a deletes it. |
| browser: hack-navigation | pass | 67 | |
| browser: hack-checklists | pass | 68 | writes screenshots to `.shots/slice2` (ignored by git) |
| browser: hack-forms | pass | 56 | |
| browser: hack-presets | pass | 16 | its header names the wrong package: the fixture test is in `./cmd/server` |
| browser: named-viewers (publish, check, dashboard) | pass | 17 | |
| browser: signin-card | pass | 4 | |
| browser: owner-dashboard | cannot run | 0 | no fixture in the repo (see above) |
| `deploy/hack/standalone/tests/first_login_test.sh` | pass | 4 | |
| `deploy/compose/Caddyfile_test.sh` | pass | 3 | |
| `deploy/digitalocean/droplet/test/first-login_test.sh` | pass | 42 | |
| `deploy/family-certs/nginx_fixture_test.sh` | pass | 1 | |
| `deploy/prod/nginx-internal-lock_test.sh`, `nginx-site-base-domain_test.sh`, `nginx-sso-log_test.sh` | pass | 3 | |
| `deploy/hack/setup-instance_test.sh` | flaky | 0.2 | 1 failure in 5 runs ("--apply exited non-zero"), passes alone |
| `node --test scripts/tests/hack-needs.test.mjs`, `scripts/test-photo-decoders.cjs`, `scripts/test-hack-mail-report.py` | pass | <1 | |
| `scripts/check-reserved-subdomains.sh` | fail, every run | 0 | "could not read reservedSubdomainLabels": it reads `internal/handler/platformsubdomain.go`, and the list moved to `internal/rules/sites/sites.go` |
| `scripts/check-hack-event-times.mjs` | fail, 3 of 3 | 31 | `page.waitForFunction: Timeout 30000ms exceeded` |
| `scripts/test-photo-token.cjs` | fail | 0 | needs Playwright on the module path and a fixture path argument; not a standalone check |

The last three are not gates and are not in a known-red file. They are
recorded so nobody mistakes them for a regression later. Not run: the other
`scripts/e2e-*` scripts (about 25; each needs its own fixture or a live
service and no gate names them), `scripts/check-hack-restore.sh` (needs a
dump), `deploy/kubernetes/test-chart.sh`, MCPJam.

The browser checks all need Playwright at `/tmp/tsx/node_modules` and
Chromium at `/usr/local/bin/chromium`; three scripts hard-code those paths.
`/tmp` does not survive a reboot.

## How long the loop takes

The box: 4 cores (Neoverse-N1, aarch64), 19.5 GB RAM, swap 3.6 of 4 GB in
use throughout, shared with other sessions that were building and running
golden replays at the same time. "Load" is the 1-minute load average sampled
every 2 seconds during the run. Peak memory is the run's cgroup
`memory.peak`, in MiB (the "Memory peak" line systemd prints was wrong on every run,
between 0.5 and 12 MB). The throwaway Postgres container is outside that
cgroup and used about 100 MiB more.

| Command | Wall clock | Peak memory | Load min / mean / max |
|---|---|---|---|
| `check-all.sh` (the gate) | 10 min 20 s | 601 MiB | 2.4 / 4.1 / 8.1 |
| `check-all.sh` again, busier box | 10 min 38 s | not sampled | 13 to 17 (load average before and after) |
| `check-all.sh` as committed, box overloaded | 14 min 38 s | not sampled | 2 before, 23 after |
| `make check` | 10 min 23 s | 463 MiB | 3.5 / 5.2 / 8.3 |
| `scripts/test-integration.sh` alone | 8 min 38 s | not sampled | 0.9 before, 2.9 after |
| `scripts/test-integration.sh` alone, busier box | 11 min 15 s | 444 MiB | 3.3 / 11.7 / 34.1 |
| `make test`, nothing cached | 29 s | 538 MiB | 4.8 / 6.1 / 6.7 |
| `make test`, nothing cached, box overloaded | 73 s (and the flaky test failed) | 740 MiB | 17 / 33 / 49 |
| `make test`, results cached | 22 s | 409 MiB | 5.9 / 7.2 / 7.9 |
| every step except the two database ones (`--skip` form above) | 53 s | 334 MiB | 5.1 / 6.2 / 7.7 |
| `go build ./... && go vet ./... && go test -count=1 ./internal/handler/` (no database) | 18 s | 331 MiB | 4.7 / 5.0 / 5.3 |
| one golden run, today's harness | 10 s | 183 MiB | 6.9 / 7.0 / 7.0 |
| one golden run, the D0-S3a harness (unmerged, both products, wider coverage) | about 60 s | not sampled | near 10 |
| `go build ./cmd/server`, warm cache | 3 s | 186 MiB | about 6 |
| all browser checks | 3 min 48 s, 3 min 25 s, 5 min 41 s | not sampled | 21 falling to 9, about 15, about 23 |

Where the time goes: the database suite is 537 of 620 seconds (87%). It runs
its packages one at a time (`-p 1`) and used 3 min 48 s of CPU in 8 min 38 s,
so it is mostly waiting. Everything else together is under 90 seconds.

**The fastest useful loop between edits** is build, vet and the tests of
the package being changed: 18 seconds. Before pushing,
the no-database steps plus `make test` take about 80 seconds. The full gate
is run once per slice, before review.

## X1-S1: bounded package concurrency (2026-11-13)

`scripts/test-integration.sh` now uses `go test -p 3` by default.
`TEST_WORKERS=1 bash scripts/test-integration.sh` restores serial package
scheduling; any positive integer selects another bound. The same single
Postgres server, template schema, JSON summarizer, forced count/JSON flags,
DB-skip rejection and failure identities remain in use. No change to
`check-all.sh`'s output parser was needed; its capped invocation now also
forwards `TEST_WORKERS`, beside `TEST_COUNT` and `TEST_PKGS`.

Before enabling concurrency, the Go test fixtures were audited for database
connections, listeners and filesystem writes, including shared fixture helpers:

- Twelve packages already use `testdb.Main` to clone the template, with
  names containing the template name, process ID and random suffix.
  Migration fixtures use `MIGRATE_TEST_DSN`, which this runner clears.
- Two exceptions were found and fixed: `internal/mcp`'s
  `TestChecklistDatabaseReplayParity` and `internal/testdb`'s
  `TestRecordingDB` connected directly to the template. Even a read-only
  connection there can block another package's `CREATE DATABASE … TEMPLATE`.
  They now use a separate `testdb.Fresh` clone when isolation is enabled;
  `Fresh` also supports packages without `testdb.Main`. Ad-hoc, non-isolated
  DB runs retain their existing behavior.
- HTTP fixtures use `httptest` listeners with allocated ports. The two
  `ListenAndServe` browser helpers require distinct opt-in environment
  variables and do not run in the default suite. Writable files, SQLite
  databases, storage trees and certificate directories use `t.TempDir` or
  other unique temporary directories. Browser/export fixture output paths
  are opt-in. No fixed-port or fixed-temp-path changes were needed.

The WordPress sparse-author test now compares archive rendering with a
measured full rescan of the same 20,000 posts for every author, instead of a
three-second deadline. Rendering must beat even that scan alone; its existing
checks for lost or mixed posts remain. This measures the quadratic regression
under the current machine load without allowing a slower algorithm merely
because the machine is busy. A temporary Go overlay restored the old
per-author rescan: this test rejected it (25.21 seconds to render versus
14.40 seconds for the rescan baseline). The overlay changed no worktree file.

Runner regression tests: `python3 scripts/test-integration_test.py`, using
only `TEST_PG_URL` and temporary fixture packages. They check serial totals,
forced flags, two deliberately failing packages that synchronize to prove
overlap, database-related skip rejection, invalid worker counts, and both
SIGINT and SIGTERM while two clone-owning packages are running. All five
pass. Interrupt cleanup stops the runner's process group and drops only its
own uniquely prefixed clones and template.

Full-suite measurements use the assigned disposable `TEST_PG_URL` and the
existing Go caches (not cleared), with unchanged Go package timeouts and no
cgroup cap. The full gate is run separately by the orchestrator. Load is
sampled every two seconds. Run 1 also overlapped build/vet and the focused
runner checks; later runs briefly overlapped the targeted handler reruns.
The recorded 537-second baseline had load 2.4–8.1, mean 4.1; the runs below
are not at comparable load.

| Run | Workers | Seconds | Load min / mean / max | Pass / fail / skip | DB skips | Result |
|---|---|---|---|---|---|---|
| 1 | 3 | 765.95 | 12.77 / 27.56 / 42.87 | 4133 / 1 / 19 | 0 | Handler package's existing 10-minute timeout |
| 2 | 3 | 708.77 | 11.68 / 23.81 / 32.55 | 4324 / 2 / 21 | 0 | Same package timeout; parent and subtest failure events |
| 3 | 3 | 632.92 | 11.31 / 24.44 / 47.56 | 4173 / 1 / 20 | 0 | Same handler package timeout |
| 4 | 1 | 1274.61 | 17.78 / 30.67 / 56.51 | 4526 / 0 / 22 | 0 | Pass |

Run 1's failure is attributed by Go to `TestSignupSourceRecordedAtCreation`,
which had run for zero seconds when the **package** timeout fired. The timeout
is unchanged. That individual test passes when rerun by itself against a
new disposable database (1 pass, 0 failures, 0 skips). The changed Go
packages (`internal/mcp`, `internal/testdb`, `internal/wpimport`) all passed
in the full run. This run does not meet the required zero-failure totals or the
40% speedup threshold (322.2 seconds). Run 2 hit the same package timeout
during `TestSSORedteamListedConsentAndPending/abandon`, after that subtest
had run for zero seconds; the parent had run for one second. Its failure
count includes both parent and subtest. The test passes separately (6
passes, 0 failures, 0 skips). All four full runs removed their template
and clone databases, including the clone left by the package timeout.
Run 3 hit the same package timeout during `TestStoragePresetsRulesOnSave`,
which had been running for eight seconds. It passes separately (1 pass,
0 failures, 0 skips). All changed Go packages passed in all four full runs.
The serial run completed the whole suite with no
failures and no database-related skips.

**Initial acceptance result (before the review follow-up below):** the three parallel runs timed out and their
partial totals differ from the complete serial run. None establishes the
required 40% reduction from 537 seconds at similar load; comparing truncated
parallel runs with the complete serial run would not be a valid speedup
measurement. Handler assertions and timeouts remain unchanged.
The five runner regression tests, `check-all_test.sh`, `go build ./...`,
`go vet ./...`, and gofmt checks pass. The full gate is left to the
orchestrator as requested; no commit, push, merge or deploy was performed.

### X1-S1 review follow-up (2026-11-13)

The setup-interruption finding was reproduced against the committed runner:
both SIGINT and SIGTERM after a successful `CREATE DATABASE`, before `psql`
returns, left the template behind. The runner now defers signal-triggered
exit just across creation and ownership recording, then exits with 130 or
143 and performs its normal cleanup. A failed creation still does not grant
ownership. The new `test_interrupt_during_database_creation` regression uses
real PostgreSQL creation with a temporary `psql` wrapper that delivers each
signal at that boundary. Both cases fail against the old runner and pass
against the fix; the test also removes its own database if the old runner
leaks it. All six runner tests pass, including the existing two-package
failure-reporting, forced-flag, database-skip and running-test interruption
checks. Build, vet, gofmt and shell syntax checks pass.

The prescribed three parallel runs and one serial run were repeated
sequentially, using the assigned disposable `TEST_PG_URL`, unchanged caches,
assertions and timeouts, and no cgroup cap. Build, vet and focused tests had
finished before these trials. No other checks from this worktree ran beside
them. Wall time and load were sampled every two seconds, so elapsed times
include up to two seconds of completion-detection delay.

| Run | Workers | Seconds | Load min / mean / max | Pass / fail / skip | DB skips | Result |
|---|---|---|---|---|---|---|
| Review 1 | 3 | 705.41 | 14.08 / 23.46 / 51.33 | 4526 / 0 / 22 | 0 | Pass |
| Review 2 | 3 | 464.24 | 12.58 / 20.13 / 22.85 | 4526 / 0 / 22 | 0 | Pass |
| Review 3 | 3 | 564.21 | 12.29 / 19.40 / 25.38 | 4526 / 0 / 22 | 0 | Pass |
| Review 4 | 1 | 694.19 | 5.83 / 7.44 / 13.98 | 4526 / 0 / 22 | 0 | Pass |
| Additional lower-load attempt | 3 | 458.29 | 6.12 / 10.33 / 13.29 | 4526 / 0 / 22 | 0 | Pass |

All five runs exited zero and a catalog query verified that each run's
template and uniquely prefixed clones were gone. The additional attempt
started after load had fallen to 6.30; it used a private temporary directory
to retain the runner's JSON stream for package timing analysis. During that
run, load rose again. `internal/handler` took 441.262 seconds and started
11.979 seconds after the first Go package. `products/hack/hackhttp` took
286.076 seconds and started at 75.147 seconds. At these measured package
durations, starting the handler first would still exceed 322.2 seconds;
package ordering alone cannot close the gap in this run.

**Review status:** the setup leak is fixed and repeatable successful totals
are now demonstrated. The timing finding remains open: no parallel trial
meets 322.2 seconds, and none matches the original baseline's load
2.4–8.1 (mean 4.1). These runs do not establish the required 40% improvement.
No further runner scheduling change was made based on non-comparable load,
and no test assertion or timeout was relaxed. A comparable-load performance
run remains necessary before this slice can be called done. Both review
findings were accepted; neither was rejected. The full gate remains assigned
to the orchestrator, as requested. No commit, push, merge, deploy, production
database access or service change was performed.

### X1-S1 package-isolation follow-up (2026-11-13)

The audit missed `db/migrations/TestHackOneNameBackfill`: it opens `DB_DSN`
directly, so its connection could prevent other packages from cloning the
shared template. `db/migrations/main_test.go` now calls `testdb.Main`, giving
the package its own clone. The separate `MIGRATE_TEST_DSN` path is unchanged.
No assertions or timeouts were weakened.

`TestDatabaseIsolation` checks the actual connected database against the
original template and creates another clone while that connection stays
open. A temporary Go overlay disabling the `TestMain` isolation made this
regression fail at the missing-template assertion; with isolation restored,
`TEST_PKGS='./db/migrations ./internal/testdb' TEST_COUNT=3` passed through
the parallel runner (21 pass, 0 fail, 4 distinct skips, zero database-related
skips; the four migration-only skips repeat in each iteration).
The migration package also passed with all three `MIGRATE_*_DSN` fixtures
pointing to newly created empty databases on the assigned test server;
those databases were dropped afterwards. All six runner regression tests,
`check-all_test.sh`, build, vet, formatting and layering checks also pass.
The full gate is assigned to the orchestrator for this retry;
the full-suite timings above are earlier runs, not remeasurements of this fix.

The X1-S1 ruling in the refactor board's `NOTES.md` supersedes the timing
blocker above: the 40% target is waived for merge under the shared box's
load. Correctness, isolation and comparison with one worker in the same
session remain the acceptance criteria; quiet-box timing is assigned to
the orchestrator.

## Target (a proposal, not yet a rule)

- Between edits: 30 seconds or less. Met today (18 s).
- Everything without a database: 2 minutes or less. Met today (about 80 s).
- The full gate: today's bar is **12 minutes on this box with other sessions
  running** (measured 10 min 20 s at a mean load of 4, 10 min 38 s at a load of 13 to 17, and 14 min 38 s when the load reached 23). A run that takes much
  longer says the box is overloaded, not that the code is slower: re-check
  the load before drawing a conclusion. The goal worth working towards is
  5 minutes, and only the database suite can deliver it.

## What would make it faster for several builders at once

Not built in this slice, except the first line.

- **Own fresh-install database per worktree: done.** `check-fresh-install.sh`
  uses one fixed database name on the box's shared Postgres, so two
  worktrees running `make check` at once drop each other's database.
  `check-all.sh` sets `FRESH_DB` to a name derived from the worktree path.
  Plain `make check` still collides.
- **Run the database suite's packages in parallel.** Each package already
  clones its own database (`TESTDB_ISOLATE=1`), so `-p 1` may no longer be
  needed. Someone has to prove no two packages share a port, a temp path or
  a global before raising it. This is the only change that moves the total
  much; with four cores busy elsewhere the gain will be smaller than 4x.
- **Run the 25 non-database steps beside the database suite** instead of
  after it: saves about 80 seconds.
- **Already safe in parallel worktrees:** the database suite (its own Docker
  Postgres on a random port, or its own database under `TEST_PG_URL`), the
  golden harness (own database names, own ports), the browser fixtures, and
  every other `make check` step (temp dirs only). The Go build and module
  caches are shared between worktrees and that is what keeps build and vet
  at 3 seconds.
- **One long-lived test Postgres** (`TEST_PG_URL`) saves each run the
  container start, a few seconds; it matters more for the browser checks,
  which start one container each.
- **A fixed home for Playwright**, outside `/tmp`, so the browser gate
  survives a reboot.
- **Box load is the biggest variable.** The same `make test` took 29 s and
  73 s within twenty minutes. Three or more full gates at once on four cores
  will each take well over the 12 minutes.

## Production evidence (read-only)

In `evidence/`, taken with the installed program (`ea1f87b1`) and the
`postgres` superuser. Schema and migration names only; no data rows.

| File | What |
|---|---|
| `simple-host.migrate-status.txt`, `simple-hack.migrate-status.txt` | `simple-host migrate -status` under each unit's user and env file: 96 applied, 26 baseline, **0 pending** on both. Last file `0017-hack-submissions-public.sql`; the next free number is `0018`. |
| `simple-host.schema.sql`, `simple-hack.schema.sql` | `pg_dump --schema-only --no-owner --no-privileges` of each database. Both have the same 114 tables. They differ in three ways. Columns sit in a different order inside some tables (added by hand in a different order), so the seed names every column. `api_keys_id_key` is a unique index on Host and a UNIQUE constraint on Hack. Host has a comment on `oauth_states.state` that Hack lacks. |
| `simple-host.schema_migrations.tsv`, `simple-hack.schema_migrations.tsv` | the 96 `schema_migrations` rows (name, applied at) of each database |
| `check-all.summary.tsv`, `browser.summary.tsv` | the per-step results behind the tables above, from the first runs. They were written by the first version of `check-all.sh`, before the review fixes: the browser file shows the missing fixture as `*`, which the current script reports as `no-fixture`. |
| `measurements.tsv` | one row per timed run: command, start, exit, wall-clock seconds, cgroup peak bytes, load. Each was run as `sudo systemd-run --wait --pipe --collect -p MemoryMax=2G ... bash -c '<command>'` from the worktree, with `/sys/fs/cgroup/system.slice/<unit>.service/memory.peak` and `/proc/loadavg` read every 2 seconds. |

## The seeded fixture

`seed/seed.sql` loads into a database built from either dump;
`bash scripts/split/seed/load-check.sh` proves it (both dumps load, the
sanity checks pass, a second load is refused). `seed/README.md` lists what
is in it. One fact a later slice needs: `oauth_grants.selected_team_id` has
no foreign key, so a grant whose selected team was deleted keeps the dead
team's id. The seed reproduces exactly that.

## Verification of D0-S1 and D0-S2

- D0-S1: on `ea1f87b1`, `git grep -n` for the three removed script names
  (the pattern in the brief's D0-S1 note), leaving out `PARITY.md`,
  `docs/history` and `CHANGELOG.md`, is empty. This file avoids spelling the
  names so the same search stays empty after this commit. `systemctl is-enabled` on the parity watch timer reports `disabled`.
- D0-S2: `bash deploy/install/install_test.sh` passes.
  `bash deploy/hack/standalone/tests/install_test.sh` passes too, so it is
  not a known-red entry.
