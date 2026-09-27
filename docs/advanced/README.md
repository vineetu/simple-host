# Advanced settings

Simple Host runs with no settings beyond its address, and every default is what
simple-host.app itself runs. This folder explains each advanced feature in plain terms, lists
every setting that changes it, and gives recipes for common changes.

**Easiest start: the setup helper at https://simple-host.app/setup.** It asks a few questions (or
every question, in Advanced mode), keeps the default for anything you skip, and writes the
one-line install command and the `.env` for a small box. It runs entirely in your browser and
sends nothing anywhere.

## Where settings go

- **Small box (Docker Compose, `deploy/install/install.sh`):** `/opt/simple-host/.env`, then
  `cd /opt/simple-host && sudo docker compose up -d`. Re-running the installer (which is also
  the upgrade) keeps what you added. The installer's flags set the address (`--host`,
  `--content`), the certificate email (`--email`), the largest site (`--max-site-mb`) and
  versions kept (`--keep-versions`).
- **Your own build:** the server's environment (a systemd `EnvironmentFile`, for example), then
  restart.

A value that does not parse, is out of range, or does not fit another setting stops the server
at startup with a message naming it. Every setting changed from its default is printed once in
the startup log. Emails, pages, the served skills and the connector's descriptions state the
value in force.

## Areas

| Area | What it covers |
|---|---|
| [Server and addresses](server-and-addresses.md) | The domain, the separate content host, per-person and per-site addresses, setup mode |
| [Accounts and sign-in](accounts-and-sign-in.md) | Owner sign-in (email codes, Google), API keys, visitor sign-in, AI app connections |
| [Sites and versions](sites-and-versions.md) | Site and upload size, versions kept, preview and download links, sites per account |
| [Saved data](saved-data.md) | What pages save, undo and history, size limits, who may write |
| [Domains and certificates](domains-and-certificates.md) | Custom domains, their checks and release, certificates |
| [Cleanup and retention](cleanup-and-retention.md) | Recently deleted, idle-site cleanup, how long analytics are kept |
| [Email](email.md) | Sending email with Resend, the sender, reply-to |
| [AI features](ai-features.md) | "Ask about this page" and the model backend |
| [Storage and backups](storage-and-backups.md) | The database, the site files, what to back up |
| [Observability](observability.md) | Health, the startup log, the admin page, analytics |
| [Rate limits](rate-limits.md) | Every rate limit in one table |

## Keeping this in sync

[settings.json](settings.json) is generated from the code (`simple-host settings --json`); the
tables on these pages, the setup helper and [../configuration.md](../configuration.md) (the full
reference) are checked against it by `make check`. After changing a setting in code, run
`bash scripts/sync-settings.sh` and commit what it changes. The setup helper's copy of the
enterprise settings comes from an enterprise checkout
(`ENTERPRISE_REPO=/path/to/simple-host-enterprise bash scripts/sync-settings.sh`).
