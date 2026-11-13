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
| `bash scripts/split/check-all.sh --golden --slice <id>` | gates 1 and 2: adds the golden comparison as D0-S3a/D0-S3b define it (build the candidate, `run.mjs --host ... --out ...` for the reference and the candidate, `diff.mjs <ref> <cand> --slice <id>`). A slice whose text says "Golden: none" leaves `--slice` out, and any difference then fails. The reference is `$GOLDEN_REF_BIN`, default `/usr/local/bin/simple-host`, the installed program. It refuses to run until the D0-S3 harness is in the tree. Tried once against that harness as it stood, unmerged, in its builder's worktree: installed program against a build of this tree, 676 requests in 2 runs, identical; each replay took about 60 seconds at a load near 10. |
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
