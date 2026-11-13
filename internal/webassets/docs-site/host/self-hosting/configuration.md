# Configuration

The settings registry (`internal/config/settings.go`) lists the environment
settings this binary reads, their defaults, allowed values and area. The single
reference is [Advanced settings](/docs/self-hosting/settings.md); its tables are generated
from the registry, with behavior notes and recipes beside them.

List the settings supported by your binary:

```sh
simple-host settings
simple-host settings --json
```

## Start here

The registry marks `DB_DSN` and `ADMIN_API_KEY` as required; the installer supplies
them. Its basic settings are `SITE_DOMAIN` (without it the server starts in setup
mode), `GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET` for Google
sign-in, and `RESEND_API_KEY` and `MAIL_FROM` for email. Choose the sign-in methods
your install needs; see the linked areas for details.

For migration ownership, required settings and rollback, see
[Deployment profiles](/docs/self-hosting/operations/deployment-profiles.md).

## Settings by area

- [Server and addresses](/docs/self-hosting/settings/server-and-addresses.md)
- [Accounts and sign-in](/docs/self-hosting/settings/accounts-and-sign-in.md)
- [Sites and versions](/docs/self-hosting/settings/sites-and-versions.md)
- [Saved data](/docs/self-hosting/settings/saved-data.md)
- [Domains and certificates](/docs/self-hosting/settings/domains-and-certificates.md)
- Hosted events
- [Cleanup and retention](/docs/self-hosting/settings/cleanup-and-retention.md)
- [Email](/docs/self-hosting/settings/email.md)
- [Storage and backups](/docs/self-hosting/settings/storage-and-backups.md)
- [Observability](/docs/self-hosting/settings/observability.md)

See also [Rate limits](/docs/self-hosting/settings/rate-limits.md) for the format and all rate limiters,
and [Operational times and limits](/docs/self-hosting/settings.md#operational-times-and-limits)
for startup validation, promised dates and served documentation.

Password accounts: `PASSWORD_SIGNIN=on` enables username/password sign-in; the binary default is `off`. `PASSWORD_SELF_SIGNUP=on` (default) permits public account creation; `off` leaves creation to the admin. `EMAIL_SIGNIN=off` accepts either company sign-in or password sign-in as the alternative. See [accounts and sign-in](/docs/self-hosting/settings/accounts-and-sign-in.md).

## SQLite row admission

`SITE_STORAGE_SITE_INFLIGHT` limits concurrent SQLite table-row operations per site
across all its resources (default `1`, allowed `1`–`4`). Each row operation takes
its site slot before the process-wide `SITE_STORAGE_SQL_CONCURRENCY` gate.
`SITE_STORAGE_ACQUIRE_WAIT_MS` covers both waits together; exhaustion returns
503 `storage_busy` with `Retry-After`. Writes take the site write lock first.
Set the limit in the Compose `.env` file and restart the app, or set
`storageSiteInflight` in the Helm values file and run `helm upgrade`.

A recovered SQLite out-of-memory panic or `SQLITE_NOMEM` error returns 422
`query_too_expensive`; another panic before commit returns 500 `storage_failed`.
After a successful commit, a checkpoint or close panic is logged and the success
response is retained, including the inserted row ID. Recovery cannot catch Go
fatal errors such as concurrent map writes or the Go runtime running out of memory.

## SQLite connection memory

Each connection uses a cap calculated at open. For a read, `D` is the database
file size plus its WAL size. For a write, `D` is the database size plus the
remaining pooled storage budget, capped at the site limit. Export checkpoints
use the existing database plus WAL size because they add no logical data.

- `cache = clamp(D / 4, 256 KiB, 2 MiB)`
- `temp = max(2 * D, 1 MiB)`
- `cap = min(ceil_MiB(3 MiB + cache + temp), 4096 MiB)`

The 4,096 MiB ceiling is the maximum accepted by the pinned
`go-sqlite3 v0.35.6` driver. SQLite's page cache uses the calculated cache size
and temporary tables stay in memory (`temp_store=MEMORY`).

This bounds SQLite's own allocations for one connection. The sum across open
connections is at most `SITE_STORAGE_SQL_CONCURRENCY` times the largest cap
(`0` concurrency means the CPU count). It does not bound the Go heap, result
buffers, exports or other program memory. It is not the whole-install memory
target measured separately for the self-hosted install.
