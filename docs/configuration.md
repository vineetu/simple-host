# Configuration

The settings registry (`internal/config/settings.go`) lists the environment
settings this binary reads, their defaults, allowed values and area. The single
reference is [Advanced settings](advanced/README.md); its tables are generated
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
[Deployment profiles](operations/deployment-profiles.md).

## Settings by area

- [Server and addresses](advanced/server-and-addresses.md)
- [Accounts and sign-in](advanced/accounts-and-sign-in.md)
- [Sites and versions](advanced/sites-and-versions.md)
- [Saved data](advanced/saved-data.md)
- [Domains and certificates](advanced/domains-and-certificates.md)
- [Hosted events](advanced/hosted-events.md)
- [Cleanup and retention](advanced/cleanup-and-retention.md)
- [Email](advanced/email.md)
- [Storage and backups](advanced/storage-and-backups.md)
- [Observability](advanced/observability.md)

See also [Rate limits](advanced/rate-limits.md) for the format and all rate limiters,
and [Operational times and limits](advanced/README.md#operational-times-and-limits)
for startup validation, promised dates and served documentation.

Password accounts: `PASSWORD_SIGNIN=on` enables username/password sign-in; the binary default is `off`. `PASSWORD_SELF_SIGNUP=on` (default) permits public account creation; `off` leaves creation to the admin. `EMAIL_SIGNIN=off` accepts either company sign-in or password sign-in as the alternative. See [accounts and sign-in](advanced/accounts-and-sign-in.md).
