# Contributor tests and deployment

Read [CLAUDE.md](../../CLAUDE.md), [INTENT.md](../../INTENT.md),
[FEATURES.md](../FEATURES.md) and [ARCHITECTURE.md](../../ARCHITECTURE.md)
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
| Hack endpoint | Registration/HTTP mapping in `products/hack/hackhttp`; workflows in `products/hack/app/*`; pure decisions in `products/hack/rules/*`; SQL in `products/hack/repository/*` |
| MCP tool | Its capability family registrar and the shared registry in `host/mcp`; retain service/REST authorization and error mapping |
| Host sites/storage | `host/app/{sites,storage}`, rules/repositories and `host/storage` primitives; old contracts through `app/legacy` |
| Browser screen / shared MCP screen | [Frontend guide](frontend.md); update the atlas before implementing Hack UX |
| Background worker | An explicit owner; preserve start conditions, stop/join ordering and transaction boundaries |

`scripts/check-layering.sh` checks imports against
`scripts/layering/rules.txt`. The product split lists remaining platform-to-product imports as transitional edges,
each naming its removal slice. Shared server fixtures live in
`host/server/servertest`; Hack server tests live in `products/hack/servertest`.
It is not proof that every workflow has reached the target layout.

For a new route or tool, include these documentation changes:

1. Place it under the appropriate FEATURES.md capability. Reference stable
   anchors from `docs/feature-ids.txt`; add a new ID there for a new anchor.
2. Run `make inventory`. Commit generated `docs/generated/routes.md`,
   `mcp-tools.md` and `inventory.json` with the registrations/registry change.
3. Update `host/webassets/static/openapi.yaml` for an HTTP contract change,
   including `x-audience`; regenerate `openapi.json` as in [frontend.md](frontend.md).
4. Update applicable llms.txt, skills and dashboard/showcase presentation.
   Skill edits require a plugin version bump and the sync script.
5. Update FEATURES.md and CHANGELOG.md (newest first) for feature
   changes (PARITY.md is frozen history since 2026-11-12). New public feature sections/bold leads also need a
   `static/features.json` row and regenerated `features.html`.

Enforcement is specific: `scripts/check-features.sh` runs inventory checking
and OpenAPI coverage. `scripts/inventory/main.go:run` rejects stale generated
files, unplaced routes/tools and invalid feature-ID registries.
`check-docs-sync.sh` checks registered `/v1` paths against OpenAPI, parsed
YAML/JSON equality, feature-page coverage, settings/profile docs and atlas consistency. Its llms/skill capability checks are warnings.
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
| `go test ./host/handler/` | Narrow package loop; no DB coverage unless a test DSN is supplied |
| `make test-integration` | Disposable DB, all packages, fails on DB-related skips |
| `make test-db PKGS=./host/handler RUN='^TestNotify' COUNT=5` | Same disposable helper, selected packages/tests/repeats |
| `make check` | Formatting, build, vet, integration and local script/asset checks |
| `make check-release` | Publication, cross-repository and HTTP/server fixtures |

A direct `go test` inherits environment DSNs. `make test` deliberately clears
them. Never point destructive fixtures at a production database.
For a named no-DB flake, for example:

```sh
DB_DSN= MIGRATE_TEST_DSN= go test ./host/handler -run '^TestNotify' -count=5
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

The helper creates a unique DB, applies `host/db/schema.sql`, then invokes
`go test -p 1 -count=COUNT -json` with DB_DSN and TESTDB_ISOLATE=1.
`scripts/lib/summarize-go-test.py` fails if a test skips for an unset or
unreachable DB; unrelated skips are counted separately. Exit cleanup drops
the DB and removes only the Docker container/volume this run created.

`host/testdb/testdb.go:Main` clones the schema template per test process,
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
(`host/handler/notify_test.go:TestNotifyVisitorRoutes`). Other workers use
their own join method. Do not assume WaitNotifications joins every worker.
The lifecycle ownership contract is in [target.md §5](target.md#5-transactions-locks-and-workers).

## What the aggregate checks and CI run

`make check` is the default goal. It checks gofmt under host/cmd/db,
builds all Go packages, runs vet and the disposable integration suite, then
runs Node UI fixtures, HTML nesting, import layering, docs-sync and plugin
freshness. It also exercises certificate issuers, nginx configuration
scripts, network/uptime watcher scripts, installer/standalone fixtures,
DigitalOcean pin checks and fresh-install/upgrade SQL. The price-age check
warns without failing. See `Makefile:check` for the current complete list.

The checks embedded in Go tests include the MCP bundle freshness tests and
frontend/theme assertions. `make check` is local/offline in the sense that
publication and another repository's source are not required; it still
needs local tools/Postgres, and Docker can need an initial image download.

`make check-release` runs `scripts/check-release.sh`:

- Check the release workflow with `actionlint` when available and check local settings.
- Test both Helm charts.
- Run nginx HTTP fixtures using nginx/curl/openssl/Python, and the Caddy
  Docker fixture after fetching its required images.

Missing prerequisites or skipped release fixtures fail the release check.
These checks do not deploy the application.

Separately, `bash scripts/check-reserved-subdomains.sh` compares the reserved
label list with nginx's server names (NGINX_CONF_DIR selects the config
directory; SITE_DOMAIN/SITE_BASE_DOMAIN select the domains). It is outside
both aggregate targets and skips if the directory is unreadable.

| Workflow | Trigger / work |
|---|---|
| `.github/workflows/ci.yml` | Push/PR: Go from go.mod, Node 22, PyYAML, nginx, Postgres 16 service; `make check`, then `make check-release` |
| `mcpjam.yml` | Main push/PR: local server/DB fixture and `check-release.sh --mcpjam`; weekly/manual main-only evals use the configured Anthropic key |
| `release.yml` | On `v*` tags, checks boundaries and charts, then builds two stamped programs, three multiarchitecture images, two Helm charts, and installation, toolkit and plugin downloads |

## Cut a release

Both products use one tag. `hack-v0.8.20` was the last tag of the separate Hack
release family. The Railway, Render, Fly and Coolify platform recipes still
install the earlier platform release until they are reworked.

Run the release checks and rehearse with an approved `vX.Y.Z-rc.1` tag first.
The release workflow builds all assets from the tagged commit and verifies the
tag and commit by running every downloadable binary on its native Linux or
macOS runner. It also checks each binary's build settings and runs `version`
in each image on both Linux architectures. It pushes candidate image tags under
`ghcr.io/<owner>/<repo>/dry-run/` and retains the other files as a workflow
artifact for the install and upgrade rehearsals. For either Compose product,
follow `docs/upgrades.md` in the unpacked RC archive. For a fresh Host install,
use the tagged installer command there with its explicit RC source ref and dry-run
image. The upgrade helper checks the full candidate version, including the RC suffix.

GHCR creates new packages as private by default. Grant the rehearsal operator
Read access to the candidate Host and Hack packages. For Compose, run
`docker login ghcr.io --username YOUR_GITHUB_LOGIN` as the OS account that
runs the installer (root for a sudo install). At the password prompt, enter
a GitHub personal access token (classic) scoped to `read:packages`.
On DigitalOcean, log in on the target droplet and use the archive's standalone
installer. Building a Packer snapshot requires an image its temporary droplet can pull.
For Kubernetes, create a `kubernetes.io/dockerconfigjson` Secret named
`ghcr-read` in the install namespace with those read-only credentials. Pass
`--set-string 'image.pullSecrets[0]=ghcr-read'` to Helm. The packaged manual
migration command copies the pull secrets and security contexts from the Deployment.

After approval and **before pushing the stable tag**, save the approved release
notes in a GitHub draft release for the exact `vX.Y.Z` tag. The authored notes
live in `docs/releases/vX.Y.Z.md`; create the draft from that file with
`gh release create vX.Y.Z --draft --title vX.Y.Z --notes-file docs/releases/vX.Y.Z.md`.
The notes say to install fresh: there is no upgrade path from an earlier release.
State that Static ships switched off and is not for production use yet.
Leave the draft unpublished.
Push the matching tag at the approved commit with both products' installer pins
set to that release. The workflow attaches the verified downloads to the draft
and publishes its authored notes.
The workflow publishes the root Host image at `ghcr.io/<owner>/<repo>:X.Y.Z`,
the root Hack image at `ghcr.io/<owner>/<repo>:hack-X.Y.Z`, and the
standalone Hack image at `ghcr.io/<owner>/simple-hack:X.Y.Z`. The GitHub release
contains both programs, both charts, the standalone archives, the toolkit and
plugin bundles, and their checksums. Bundle filenames and the Host Claude
manifest carry the tagged release version. The bundled skills send the
`X-Skill-Version` embedded in the Host server.
Stable runs use the GitHub Actions queue and hold the same lock through
promotion. To resume an interrupted publication, rerun the original workflow
run within its artifact retention period. It reuses the verified OCI archives
and downloads, skips image tags with the expected digest, and stops on a
conflicting digest. A matching draft resumes with its missing assets and is
published after checksum verification and a fresh check of the tag's commit.
An older release leaves GitHub's latest designation as it stands. The promotion job updates Host
`:latest` only when GitHub's latest stable release tag still points to the built commit.

To build from a Linux checkout without publishing, start with two empty
directories. Reuse the populated image directory for verification.

```bash
bash scripts/release/build.sh artifacts v0.9.0-rc.1 /tmp/release-assets
bash scripts/release/build.sh images v0.9.0-rc.1 /tmp/release-images
bash scripts/release/build.sh verify-images v0.9.0-rc.1 /tmp/release-images
```

The artifact build uses Go, Helm, jq, Python with Pillow and PyYAML, and ZIP tools.
On another target platform, run `bash scripts/release/build.sh verify-binaries v0.9.0-rc.1 /tmp/release-assets`
with the same checkout and built files to check that platform's downloads.
The image build uses a Docker Buildx builder with OCI export and emulation for
the other architecture. Set `RELEASE_PLATFORMS` to the native Linux platform
for both image commands in a single-architecture rehearsal. Verification uses
Skopeo and Docker.

## Migration rules

Update both `host/db/schema.sql` and a new `host/db/migrations/NNNN-name.sql`, using
the next free four-digit number. Use plain, idempotent SQL, additive defaults
and short locks; follow `host/db/migrations/migrations.go` rather than renaming an
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
settings, sets CGO_ENABLED=0 and builds `./cmd/simple-host` and
`./cmd/simple-hack` from the same commit. Use that memory cap
for heavy local runs. Embedded HTML changes require the same rebuild.

Release `-ldflags -X` fields are:

| Package variable under `github.com/vsriram/simple-host/host/buildinfo` | Value |
|---|---|
| `Version` | `git describe --tags --always --dirty` |
| `Commit` | `git rev-parse --short HEAD` |
| `BuildTime` | UTC `date -u +%Y-%m-%dT%H:%M:%SZ` |

`simple-host version` and `simple-hack version` need no DB/config. Their first line is
`simple-host VERSION (commit COMMIT)`; the second reports embedded migration
count/last file and points to `migrate -status`. Startup/admin show the same
build identity (`host/buildinfo/buildinfo.go:String`,
`host/server/migrate.go:runVersionCommand`). A checkout is not evidence of
what binary is running.

For hosted systemd, run each candidate's `migrate -status` and then `migrate`
against its own database, as that unit's user with its environment, before
installing: `/tmp/simple-host.new` for Host and `/tmp/simple-hack.new` for Hack.
Back up both installed programs, `/usr/local/bin/simple-host` and
`/usr/local/bin/simple-hack`, with the same `.bak-<timestamp>` suffix. Install
both candidates with root ownership and executable mode, then restart
**both simple-host and simple-hack**. Each unit uses its own program path;
replacing Host alone does not update Hack. CLAUDE.md has the paired
build/backup/install command. Check that both `version` commands print the
same release and commit.

Rollback restores both programs from the same backup timestamp and restarts
both services, provided their commit is at or above the coordination file's
oldest-safe floor. To undo the initial split, restore the previous Hack unit
(which used `/usr/local/bin/simple-host`), reload systemd, restore the saved
combined program and restart both services. Binary rollback does not undo SQL
or data moves; a data rollback needs the matching database, site tree,
configuration/keys and program together, as described in the deployment profiles.

The checked-in Host systemd unit runs as `simplehost`, with
`ProtectSystem=strict` and `/srv/simple-host/sites` as its writable site
boundary (`deploy/prod/simple-host.service`). Certificate drop-ins separately
permit request writes under hosted `SITE_CERT_DIR=/var/lib/simple-host-site-certs`
and `DOMAIN_CERT_DIR=/var/lib/simple-host-domain-certs`; other issuer markers
stay read-only (`deploy/{site-certs,domain-certs}/simple-host.service.d-*.conf`).
Empty BIND_ADDR listens on all interfaces; empty ANALYTICS_SALT derives the
visitor salt from ADMIN_API_KEY (`host/config/config.go:Config`,
`host/analytics/ingest.go:visitorSalt`, `WithSalt`).

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
pings Postgres (`host/handler/health.go`). Refresh owner product knowledge
through the documented sh-brain-sync operation after a hosted deploy.

Analytics logrotate uses create/delaycompress rather than copytruncate;
retention is the live day plus 29 archives. Journald's 30-day setting is
box-wide. `deploy/prod/logrotate-analytics.conf` installs as
`/etc/logrotate.d/simple-host-analytics`; `deploy/prod/journald-retention.conf`
installs as `/etc/systemd/journald.conf.d/30-retention.conf` (destinations
specified in each template). `analytics-rebuild` can replay retained archives.
These are operator configuration, not files rewritten by go build.
