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
