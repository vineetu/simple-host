# Deployment profiles

These are the three existing deployment layouts for the same server binary.
The settings registry is [settings.json](https://github.com/vineetu/simple-host/blob/main/docs/advanced/settings.json); its only
`required: true` entries are `ADMIN_API_KEY` and `DB_DSN`
(`docs/advanced/settings.json:585`, `docs/advanced/settings.json:3130`).
Addresses, persistence and sign-in still need the profile-specific choices below.
The offline check is `bash scripts/check-profiles.sh`.

## Hosted systemd

### What runs


### How settings are supplied


### Migration ownership

Hosted: the operator owns migrations; startup migration is off.
Neither inspected unit sets MIGRATE_ON_START. Apply release SQL before installing
the binary, then record each hand-applied file with `simple-host migrate -mark FILE`;
alternatively run the release's `simple-host migrate` against each database.
The migration command takes the advisory lock; hand-applied SQL does not acquire
that lock automatically. A fresh empty database needs `db/schema.sql`, or
`simple-host migrate -init`; ordinary startup only checks the schema
(`CLAUDE.md:46`, `db/migrations/migrations.go:38`, `cmd/server/migrate.go:17`,
`cmd/server/main.go:119`). The shared migration rules below apply.

### Upgrade steps and rollback

Before changing either instance, take paired database and site-directory backups
and retain its settings/sealing keys. Build under the memory cap, apply schema
changes to each target database, back up the installed binary, install and restart
the target services. The installed path is shared, so the next restart of either
service uses that binary (`CLAUDE.md:62`, unit ExecStart lines above).
Rollback installs the saved `.bak-<timestamp>` binary and restarts the affected
services; it does not undo SQL. See the shared compatibility limits below.
Verify `/healthz`, `/readyz`, the changed route and site/person/legacy addresses
from a client, check neighbouring sites and startup logs, and run
`/usr/local/bin/simple-host version` (`CLAUDE.md:93`, `CLAUDE.md:98`,
`internal/handler/health.go:14`).

## Docker Compose small box

### What runs

The root `compose.yaml` runs Postgres 16, one server container and Caddy.
The server image is the nonroot Go binary, HTTP 8090, `DATA_DIR=/data/sites`;
Caddy publishes 80/443 by default. Named volumes hold the database, sites,
Caddy logs, certificates and config. Caddy proxies APIs and can serve site files
from its read-only sites mount (`compose.yaml:14`, `compose.yaml:37`,
`compose.yaml:277`, `Dockerfile:31`, `deploy/compose/Caddyfile:86`).
Edition comes from settings: root Compose passes `EVENTS` through.
Workers run inside the server process, as listed below.


### How settings are supplied


### Migration ownership


### Upgrade steps and rollback


## Kubernetes

### What runs


### How settings are supplied


### Migration ownership


### Upgrade steps and rollback


## Shared worker and migration details


Migrations are forward-only: there is no down command. New files must be
`NNNN-name.sql`, next free number, plain idempotent SQL, additive and safe on a
live DB. The runner uses the frozen legacy order then numbered files, skips
historical baseline files, and commits each file with its schema_migrations row
under a per-database Postgres advisory lock (`db/migrations/migrations.go:4`,
`db/migrations/migrations.go:302`). Init uses that same lock; manual SQL does not.


| Choice | Hosted systemd | Compose small box | Kubernetes |
|---|---|---|---|
| Server | Two services, shared binary | One app; standalone bundles Caddy | One pod, Recreate |
| Settings | Separate env files | .env substitution, explicit environment | ConfigMap + Secret from values |
| Database | Separate DB per instance | Bundled Postgres 16 | Operator's Postgres 16 |
| Files | Separate /srv site trees | Named sites or data volume | /data persistent volume |
| Migrations | Operator before restart | Installer/release before app | Server on start by default |
| Empty DB | schema.sql or migrate -init | Postgres init or standalone release | Server migrate -init path |
| Rollback | Saved binary + restart | Previous image/package | Helm rollback/previous image |
| Restore | Paired DB + site tree + keys | Paired DB + files volume + keys | Paired DB + /data + Secret |

## Password sign-in by profile
