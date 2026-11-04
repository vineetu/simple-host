# Contributor tests and deployment

Read [CLAUDE.md](../../CLAUDE.md), [INTENT.md](../../INTENT.md),
[FEATURES.md](../../FEATURES.md) and [ARCHITECTURE.md](../../ARCHITECTURE.md)
before changing code. CLAUDE.md remains the working rules; this guide maps
them to the current checks and deployment layouts. It does not authorize a
deployment. Use the [deployment profiles](../operations/deployment-profiles.md)
for settings, migration ownership and recovery per layout.

## Where to put a change

[Target architecture §9](target.md#9-where-to-add-a-feature) describes the
feature entry points. Check the current package map before assuming a
planned extraction is already complete.

| Change | Current direction |
|---|---|
| Hack endpoint | Registration/HTTP mapping in `internal/hackhttp`; workflows in `internal/app/hack/*`; pure decisions in `internal/rules/*`; SQL in `internal/repository/*` |
| MCP tool | Its capability family registrar and the shared registry in `internal/mcp`; retain service/REST authorization and error mapping |
| Host sites/storage | `internal/app/{sites,storage}`, rules/repositories and `internal/storage` primitives; old contracts through `app/legacy` |
| Browser screen / shared MCP screen | [Frontend guide](frontend.md); update the atlas before implementing Hack UX |
| Background worker | An explicit owner; preserve start conditions, stop/join ordering and transaction boundaries |

`scripts/check-layering.sh` checks imports against
`scripts/layering/rules.txt`, including declared transitional exceptions.
It is not proof that every workflow has reached the target layout.

For a new route or tool, include these documentation changes:

1. Place it under the appropriate FEATURES.md capability. Reference stable
   anchors from `docs/feature-ids.txt`; add a new ID there for a new anchor.
2. Run `make inventory`. Commit generated `docs/generated/routes.md`,
   `mcp-tools.md` and `inventory.json` with the registrations/registry change.
3. Update `internal/handler/static/openapi.yaml` for an HTTP contract change,
   including `x-audience`; regenerate `openapi.json` as in [frontend.md](frontend.md).
4. Update applicable llms.txt, skills and dashboard/showcase presentation.
   Skill edits require a plugin version bump and the sync script.
5. Update FEATURES.md, CHANGELOG.md (newest first) and PARITY.md for feature
   changes. New public feature sections/bold leads also need a
   `static/features.json` row and regenerated `features.html`.

Enforcement is specific: `scripts/check-features.sh` runs inventory checking
and OpenAPI coverage. `scripts/inventory/main.go:run` rejects stale generated
files, unplaced routes/tools and invalid feature-ID registries.
`check-docs-sync.sh` checks registered `/v1` paths against OpenAPI, parsed
YAML/JSON equality, feature-page coverage, settings/profile docs, local parity
coverage and atlas consistency. Its llms/skill capability checks are warnings.
CHANGELOG prose and the relevance of UI/skill edits still require review;
these checks do not infer a complete feature description from code.

## Fast tests and local prerequisites

Use the Go version in `go.mod`. Build/test tools also include Python 3 with
PyYAML, Node (CI uses 22), and PostgreSQL client tools for DB checks.
The server build itself needs no Node; checked-in assets are embedded.
The Makefile is the executable list of targets.

| Command | Purpose / database behaviour |
|---|---|
| `make test` | Fast `go test ./...`; explicitly clears DB_DSN and MIGRATE_TEST_DSN, so DB-backed fixtures skip |
| `go test ./internal/handler/` | Narrow package loop; no DB coverage unless a test DSN is supplied |
| `make test-integration` | Disposable DB, all packages, fails on DB-related skips |
| `make test-db PKGS=./internal/handler RUN='^TestNotify' COUNT=5` | Same disposable helper, selected packages/tests/repeats |
| `make check` | Formatting, build, vet, integration and local script/asset checks |
| `make check-release` | Publication, cross-repository and HTTP/server fixtures |

A direct `go test` inherits environment DSNs. `make test` deliberately clears
them. Never point destructive fixtures at a production database.
For a named no-DB flake, for example:

```sh
DB_DSN= MIGRATE_TEST_DSN= go test ./internal/handler -run '^TestNotify' -count=5
```

Substitute the real test name and package. `-count=N` repeats execution
rather than reusing Go's successful test cache. For DB-backed tests use
`make test-db` above; packages belong in PKGS/TEST_PKGS, not positional
arguments to `scripts/test-integration.sh`.

### Measured on this box

On 2026-11-04, in the cleanup 5.3-D worktree on aarch64, Bash `time make test`
completed successfully in **105.965 seconds real**, **242.523 seconds user**,
**27.264 seconds system**. This was one normal invocation with the existing
Go caches, not a clean-cache benchmark and not database coverage. The DB
run overlapped part of it, so it is not an isolated performance measurement.

The full `make test-db` run (default PKGS=./..., COUNT=1) also passed:
**384.455 seconds real**, **107.189 seconds user**, **31.408 seconds system**,
using the already-present local `postgres:16-alpine` image. It created and
removed its disposable container/database; no external service was needed.
This run overlapped the unit run and later build/docs checks. Timings
describe this machine/run; they are not release gates.

## Disposable database fixtures

`scripts/test-integration.sh` chooses a bare TEST_PG_URL server URL first;
otherwise it starts `postgres:16-alpine` in Docker on a random localhost port.
Without either it fails. The server URL must have no path or query. The
helper owns the unique database name and appends connection parameters.
It never defaults to the box's shared production Postgres or invokes sudo.

The helper creates a unique DB, applies `db/schema.sql`, then invokes
`go test -p 1 -count=COUNT -json` with DB_DSN and TESTDB_ISOLATE=1.
`scripts/lib/summarize-go-test.py` fails if a test skips for an unset or
unreachable DB; unrelated skips are counted separately. Exit cleanup drops
the DB and removes only the Docker container/volume this run created.

`internal/testdb/testdb.go:Main` clones the schema template per test process,
points DB_DSN at that clone, and drops it on exit. `Fresh(t)` creates a
further per-test clone when global queues cannot be scoped to a test's rows.
Without isolation enabled, Main runs tests normally and Fresh skips.

Migration tests need empty databases, so the helper clears MIGRATE_TEST_DSN.
`scripts/check-fresh-install.sh` owns the fresh-schema and real prior-release
upgrade fixtures; it needs a local Postgres superuser, or PSQL/PGPASSWORD
configured for the disposable CI service. Do not substitute a production DSN.

### Workers must finish before fixtures disappear

Prefer a worker's single-pass/RunOnce operation when that tests the behaviour.
If a fixture starts a loop, retain its cancellation and join handle. Register
the stop-and-wait cleanup after database/file cleanup registration, because
`t.Cleanup` runs in reverse order. Cancel first, then Wait, then allow the DB
connection/clone to close. Cancellation alone does not prove a goroutine has
finished reading the fixture.

For notification fixtures the existing pattern is
`t.Cleanup(func() { cancel(); s.a.sites.WaitNotifications() })`
(`internal/handler/notify_test.go:TestNotifyVisitorRoutes`). Other workers use
their own join method. Do not assume WaitNotifications joins every worker.
The lifecycle ownership contract is in [target.md §5](target.md#5-transactions-locks-and-workers).

## What the aggregate checks and CI run

`make check` is the default goal. It checks gofmt under internal/cmd/db,
builds all Go packages, runs vet and the disposable integration suite, then
runs Node UI fixtures, HTML nesting, import layering, docs-sync and plugin
freshness. It also exercises certificate issuers, nginx configuration
scripts, network/uptime/parity watcher scripts, installer/standalone fixtures,
DigitalOcean pin checks and fresh-install/upgrade SQL. The price-age check
warns without failing. See `Makefile:check` for the current complete list.

The checks embedded in Go tests include the MCP bundle freshness tests and
frontend/theme assertions. `make check` is local/offline in the sense that
publication and another repository's source are not required; it still
needs local tools/Postgres, and Docker can need an initial image download.

`make check-release` runs `scripts/check-release.sh`:

- Verify the setup page's Enterprise chart pin against the published chart,
  or an explicitly supplied SH_ENTERPRISE_REPO checkout.
- Compare Enterprise settings from the published source, or ENTERPRISE_REPO;
  compare parity with another checkout only when PARITY_OTHER is supplied.
- Run nginx HTTP fixtures using nginx/curl/openssl/Python, and the Caddy
  Docker fixture after fetching its required images.

Missing prerequisites or skipped release fixtures fail the release check.
These checks do not deploy the application.

| Workflow | Trigger / work |
|---|---|
| `.github/workflows/ci.yml` | Push/PR: Go from go.mod, Node 22, PyYAML, nginx, Postgres 16 service; `make check`, then `make check-release` |
| `mcpjam.yml` | Main push/PR: local server/DB fixture and `check-release.sh --mcpjam`; weekly/manual main-only evals use the configured Anthropic key |
| `release.yml` | `v*` tags: installer-pin check, stamped cross-platform binaries and multiarch image publication |
| `hack-release.yml` | `hack-v*` tags: chart checks, versioned installation archives/chart and multiarch images |

## Golden API comparisons

[scripts/golden/README.md](../../scripts/golden/README.md) documents the
cleanup's checked-in REST/MCP snapshots. The harness starts Host and Hack
instances of a candidate binary against two disposable databases, seeds via
API and captures normalized status/headers/bodies and request sequence.
It needs Node 20+, psql and a reachable disposable Postgres server configured
as that README describes. It is separate from the Makefile targets above.

Run `node scripts/golden/run.mjs BINARY OUT.json`, then
`node scripts/golden/diff.mjs scripts/golden/baseline.json OUT.json`, using
actual local binary/output paths. A difference requires investigation;
changing the baseline to hide a regression is not a passing comparison.
The harness normalizes specified nondeterministic values, not arbitrary
response changes. Its README describes coverage and normalization limits.

## Migration rules

Update both `db/schema.sql` and a new `db/migrations/NNNN-name.sql`, using
the next free four-digit number. Use plain, idempotent SQL, additive defaults
and short locks; follow `db/migrations/migrations.go` rather than renaming an
already applied file. Keep the frozen Legacy/Baseline names unchanged.

`TestMigrationNames` rejects malformed names and duplicate numbers. At run
time `migrations.Apply` refuses any pending numbered file at or below the
highest recorded applied number, even when that applied file is absent from
this build. `TestNumberedGuard` covers the branch/order collision. Renumber
an unapplied new file before release when another branch took its slot.

Tracked legacy files run in their frozen order before numbered files; this
is not a global lexical sort. Each migration and its schema_migrations row
commit together under an advisory lock. A later failure leaves earlier files
committed. The runner has no down command (`migrations.go:Apply`).

[Deployment profiles](../operations/deployment-profiles.md#shared-worker-and-migration-details)
explain schema checks and compatibility limits. Hosted systemd operators
migrate before restart; Compose installers/release steps migrate before
starting the app; Kubernetes defaults to MIGRATE_ON_START=true. Do not copy
one profile's migration ownership into another.

## Hosted build, deploy and rollback

Before a build/deploy, follow the shared coordination file named by
CLAUDE.md. Use your own worktree, fetch/fast-forward before work, hold
`flock /tmp/simple-host-deploy.lock` throughout deployment, and rebase onto
latest origin/main before pushing. Do not revert another session's commits.

The hosted machine is aarch64 with limited RAM. CLAUDE.md's exact build
command uses a transient systemd unit with MemoryMax=2G, preserves Go cache
settings, sets CGO_ENABLED=0 and builds `./cmd/server`. Use that memory cap
for heavy local runs. Embedded HTML changes require the same rebuild.

Release `-ldflags -X` fields are:

| Package variable under `github.com/vsriram/simple-host/internal/buildinfo` | Value |
|---|---|
| `Version` | `git describe --tags --always --dirty` |
| `Commit` | `git rev-parse --short HEAD` |
| `BuildTime` | UTC `date -u +%Y-%m-%dT%H:%M:%SZ` |

`simple-host version` needs no DB/config. Its first line is
`simple-host VERSION (commit COMMIT)`; the second reports embedded migration
count/last file and points to `migrate -status`. Startup/admin show the same
build identity (`internal/buildinfo/buildinfo.go:String`,
`cmd/server/migrate.go:runVersionCommand`). A checkout is not evidence of
what binary is running.

For hosted systemd, migrate each affected database before installing the
new binary. Back up `/usr/local/bin/simple-host` to its timestamped `.bak`,
install the new binary with root ownership and executable mode, then restart
**both simple-host and simple-hack**: they share the installed path. CLAUDE.md
has the copy/build/install command; the coordination file requires paired
restarts, while that command's final restart names only simple-host.
Rollback reinstalls the saved `.bak` and restarts both services. It does not
undo SQL; paired DB/site-tree restore and retained keys are a separate
recovery operation described in the deployment profiles.

Keep hosted family settings (`PERSON_HOSTS=canonical`, SITE_HOSTS=canonical),
certificate paths, WRITE_AUTH_MODE, BIND_ADDR, analytics and GeoIP settings in
the per-instance environment. Their defaults and meaning are in
[configuration.md](../configuration.md) and [advanced settings](../advanced/README.md).
Never print `/etc/*.env` contents. nginx edits require a dated backup,
`nginx -t`, then reload; never open the token-bearing content-host config.

Verify as a client after restart: public `/healthz` (200), `/readyz`, the
changed page/API, a site host, a person host and the old content-path redirect.
Check Simple Hack and neighbouring sites named in CLAUDE.md, then startup
logs for errors. `/healthz` only proves HTTP is responding; `/readyz` also
pings Postgres (`internal/handler/health.go`). Refresh owner product knowledge
through the documented sh-brain-sync operation after a hosted deploy.

Analytics logrotate uses create/delaycompress rather than copytruncate;
retention is the live day plus 29 archives. Journald's 30-day setting is
box-wide. The installed templates are `deploy/prod/logrotate-analytics.conf`
and `journald-retention.conf`; `analytics-rebuild` can replay retained
archives. These are operator configuration, not files rewritten by go build.
