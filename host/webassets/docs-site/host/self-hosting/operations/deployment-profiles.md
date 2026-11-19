# Deployment profiles

These are the three existing deployment layouts. Build Host with `go build -o simple-host ./cmd/simple-host`. The settings registry is [settings.json](https://github.com/vineetu/simple-host/blob/main/docs/advanced/settings.json); its only `required: true` entries are `ADMIN_API_KEY` and `DB_DSN` (`docs/advanced/settings.json:585`, `docs/advanced/settings.json:3130`). Addresses, persistence and sign-in still need the profile-specific choices below. The offline check is `bash scripts/check-profiles.sh`.

## Hosted systemd

### What runs

Simple Host at simple-host.app uses `/usr/local/bin/simple-host`. Workers run inside each server process; see the shared worker list below.

### How settings are supplied

systemd loads the respective EnvironmentFile. Neither inspected unit exposes individual setting names or values; the env files were not opened. Each must supply `ADMIN_API_KEY` and `DB_DSN`, with different databases for the two instances. Host refuses `EVENTS=hosted`; the program selects the product, not the environment. Hosted family addresses use `PERSON_HOSTS=canonical` and `SITE_HOSTS=canonical` (`ARCHITECTURE.md`, Deploy).

### Migration ownership

Hosted: the operator owns migrations; startup migration is off. Neither inspected unit sets MIGRATE_ON_START. Before installing, run the candidate's `migrate -status` and then `migrate`, once per database, as that unit's user with its environment. Host uses `/tmp/simple-host.new`. The migration command takes the advisory lock. Use `migrate -mark FILE` only for SQL already applied by hand. A fresh empty database needs the matching program's `migrate -init -record-product`; do not load schema.sql separately. An existing database with no product record needs that program's explicit `migrate -record-product` once. Ordinary startup checks the recorded product and schema (`host/server/migrate.go`, `host/server/build.go`). The shared migration rules below apply.

### Upgrade steps and rollback

Before changing either instance, take paired database and site-directory backups and retain its settings/sealing keys. Build both programs from one commit under the memory cap and migrate each target database with its matching candidate. Back up `/usr/local/bin/simple-host` with a `.bak-<timestamp>` suffix. Install both candidates at their respective paths, and restart both services. See CLAUDE.md for the paired command. Rollback restores both programs from the same timestamp and restarts both services, provided their commit is no older than the coordination file's oldest-safe floor. Binary rollback does not undo SQL or data moves. See the shared compatibility limits below; a data restore includes the matching database, site directory, configuration/keys and program together. Verify `/healthz`, `/readyz`, the changed route and site/person/legacy addresses from a client, and check neighbouring sites and startup logs. Run `/usr/local/bin/simple-host version`. Both programs must print the same release and commit.

## Docker Compose small box

### What runs

The root `compose.yaml` runs Postgres 16, one server container and Caddy.
The server image is the nonroot Go binary, HTTP 8090, `DATA_DIR=/data/sites`;
Caddy publishes 80/443 by default. Named volumes hold the database, sites,
Caddy logs, certificates and config. Caddy proxies APIs and can serve site files
from its read-only sites mount (`compose.yaml:14`, `compose.yaml:37`,
`compose.yaml:276`, `Dockerfile:31`, `deploy/compose/Caddyfile:86`).
The root Dockerfile defaults to its `host` target and starts `simple-host`
directly. Root Compose does not pass `EVENTS` through.
Workers run inside the server process, as listed below.

Its `/data` volume holds sites, logs and Caddy state; the server listens on 127.0.0.1:8090, Caddy on 8081/8443 with health on 8080.

### How settings are supplied

Compose substitutes `.env`/shell values into explicit container environment entries. Root Compose requires `DB_PASSWORD` and `ADMIN_API_KEY`, constructs `DB_DSN`, and passes `SITE_DOMAIN` and `PUBLIC_BASE_URL`. Blank SITE_DOMAIN deliberately enters browser setup; configure the real domain/base URL before normal use. Dockerfile supplies the persistent DATA_DIR and port (`compose.yaml:19`, `compose.yaml:40`, `Dockerfile:37`). Standalone also requires `PASSCODE_ENC_KEY`, `ANALYTICS_SALT` and `SITE_DOMAIN`; it derives PUBLIC_BASE_URL and enables password sign-in by default. With password sign-in off, it requires either complete OIDC settings or `RESEND_API_KEY` plus `MAIL_FROM` for sign-in.

### Migration ownership

Compose: the installer migrates before starting the new app. The root installer pulls the image, starts the database, runs `docker compose run --rm -T app migrate -init -record-product`, then starts the app. Failure leaves the app unrestarted. Compose also gates app startup on the one-shot `migrate` service, which initializes an empty database or records an existing install and applies pending migrations under the advisory lock. Standalone's installer stops the app and runs `release` before starting it; Compose also gates app startup on successful release. Both products keep their site volumes across restarts. The installer rehearsals build local images, sign in, publish a site and check it after restarting the app; they also run the pre-D4-S7b Compose files with the new images (`deploy/install/product_init_test.py`).

### Upgrade steps and rollback

Take paired DB/files backups and keep `.env` and sealing keys. Re-run the versioned installer; root Compose uses the published image, not a local build. SQL stays applied, subject to the compatibility limits below. If restoring data, restore the DB and sites volume together (standalone: DB and `/data`). The database records live versions while files hold those versions and runtime SQLite/uploads (`ARCHITECTURE.md`, Data model; `compose.yaml:290`).

## Kubernetes

### What runs

HTTP is 8090; TLS ends upstream. One replica and `Recreate` stop the old pod before the new one uses the ReadWriteOnce volume. `/data/sites` and `/data/geoip` are on `/data`; `/tmp` is an emptyDir.

### How settings are supplied

envFrom loads a ConfigMap and Secret. Values set `SITE_DOMAIN`, `PUBLIC_BASE_URL`, `EVENTS=hosted`, persistence paths and port; extraConfig and extraSecrets add registry names. Set `host`, a database DSN (or existing Secret), and password sign-in (on by default), OIDC credentials or email sign-in credentials. ADMIN_API_KEY is generated and retained by Helm, or supplied in your existing Secret; DB_DSN is supplied by you.

### Migration ownership

Kubernetes: the server migrates before listening. The default `migrateOnStart: true` renders MIGRATE_ON_START=true. On every start, the server waits for Postgres, initializes schema.sql if `public.users` is absent, applies pending files under the advisory lock, and checks the schema. The database role must own the database.

### Upgrade steps and rollback

Back up Postgres and the `/data` volume as a pair, preferably with the pod scaled to zero, and preserve the Secret, especially PASSCODE_ENC_KEY. Change the image tag with `helm upgrade` (or update/apply rendered YAML), wait for rollout and inspect logs. Recreate means downtime during migration/startup. Use `helm rollback` or restore the previous manifest/image for binary rollback; SQL stays applied. Earlier successful migration files remain committed if a later file fails. Restore DB plus volume together if data rollback is needed (`docs/kubernetes.md:152`, `docs/kubernetes.md:187`, `host/db/migrations/migrations.go:20`).

## Shared worker and migration details

Workers are goroutines in the server, not separate queue services in any profile. Conditions/settings can leave workers inactive; setup mode returns before normal worker wiring (`:155`).

Migrations are forward-only: there is no down command. New files must be
`NNNN-name.sql`, next free number, plain idempotent SQL, additive and safe on a
live DB. The runner uses the frozen legacy order then numbered files, skips
historical baseline files, and commits each file with its schema_migrations row
under a per-database Postgres advisory lock (`host/db/migrations/migrations.go:4`,
`host/db/migrations/migrations.go:302`). Init uses that same lock; manual SQL does not.

An older binary is intended to work on the newer additive schema, but startup does not prove full backwards compatibility. It does not compare schema versions, reject extra columns/newer migration rows, or check column types or index definitions. Missing requirements abort startup; extra schema is accepted (`cmd/server/main.go:131`, `host/db/schemacheck.go:170`, `host/db/schemacheck.go:196`). `/healthz` means HTTP is answering; `/readyz` also pings Postgres (`host/handler/health.go:19`). The version command needs no database and prints release/commit plus embedded migration information (`cmd/server/migrate.go:148`).

| Choice | Hosted systemd | Compose small box | Kubernetes |
|---|---|---|---|
| Server | Two services, one program per product | One app; standalone bundles Caddy | One pod, Recreate |
| Settings | Separate env files | .env substitution, explicit environment | ConfigMap + Secret from values |
| Database | Separate DB per instance | Bundled Postgres 16 | Operator's Postgres 16 |
| Files | Separate /srv site trees | Named sites or data volume | /data persistent volume |
| Migrations | Operator before restart | Installer/release before app | Server on start by default |
| Empty DB | Matching program: migrate -init -record-product | Postgres init or standalone release | Server migrate -init path |
| Rollback | Paired program backups + restart | Previous image/package | Helm rollback/previous image |
| Restore | Paired DB + site tree + keys | Paired DB + files volume + keys | Paired DB + /data + Secret |

## Password sign-in by profile

Hosted systemd: leave `PASSWORD_SIGNIN=off` (binary default); existing email and provider sign-ins continue. No mail or identity provider is required for password sign-in. Kubernetes: `signIn.password.enabled: true` and `signIn.password.selfSignup: true` render both settings as `on`; setting either false renders `off`. Password sign-in satisfies the chart's sign-in requirement without OIDC or Resend. An operator may set `PASSWORD_SELF_SIGNUP=off` and create/reset accounts through Admin Users. People must change temporary passwords before completing sign-in. Existing email accounts can receive passwords by admin reset; existing keys and sessions continue to work.
