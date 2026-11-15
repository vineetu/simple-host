# Advanced settings

For where to run a small box and where settings go, see [deployment profiles](/docs/self-hosting/operations/deployment-profiles.md).

The installer supplies the required database connection and admin key; you choose
the address and sign-in methods. The tables state the binary defaults. This folder
explains each advanced feature in plain terms, lists every setting that changes
it, and gives recipes for common changes.

**Easiest start: the setup helper at https://simple-host.app/setup?product=small-box.** It asks a
few questions (or every question, in Advanced mode), keeps the default for anything you skip, and
writes the one-line install command and the `.env` for a small box. Nothing is required: with no
domain yet, the command installs the box in setup mode, which asks for the domain in the browser
afterwards, and any value only you can know is left in the `.env` as a blank under a `# Fill in:`
line, named in one line above it. It runs in your browser and
never asks for a password or key. Just before your files it can **check your choices** for likely
mistakes (a setting that weakens sign-in, a retention or undo promise cut short, an upload size the
box cannot hold): it sends only the names and values of the numbers, durations, switches, choices and limits
you changed, never hostnames, emails or secrets, and you Apply or Ignore each suggestion. You can
skip it; where the server has no model backend it is skipped by itself. Where the server has
one, an **assistant** on the page answers questions about settings, fills in the form from a plain
request ("a hackathon for 150 people with emailed codes"), cleans up choices, and helps with an
error you paste (redacted first, and sent only after you review it); you apply each change it
suggests. The files step also gives one block to hand to **your own AI agent**: what the server
needs, every step with your files in it, and how to check the result.

For **Simple Host Enterprise** (https://simple-host.app/setup?product=enterprise), choose
Helm or Kubernetes YAML for a cluster you already run. Both use the same chart and its
`values.yaml`; the page does not create cloud infrastructure. Connect existing Postgres
with its CA certificate or install one persisted Postgres in the cluster for evaluation.
The latter has no automatic backups. Connect your existing S3-compatible bucket, company
OIDC provider, ingress and certificate issuer. The bucket starts with no provider assumed.

Nothing must be filled in to reach the files: defaults stay, and the page names the blanks
you must complete before installation. Secrets are filled locally in `secrets.env` and put
in a persistent Kubernetes Secret; the browser never receives them. Generate signing and
envelope keys once and retain them for upgrades. With external Postgres, use its existing
owning-role password and a distinct application-role password. Its CA goes in `db-ca.crt`,
passed to Helm with `--set-file postgres.external.caCert=db-ca.crt`.

Advanced settings map to the chart's typed values or `extraConfig`; secrets go in the
existing Secret. The chart also exposes resources, replicas, scheduling and optional
annotations for an existing workload identity. Bucket identity uses the AWS SDK credential
chain; other S3-compatible providers need S3/HMAC keys. Select the existing kubeconfig
context and namespace, then run the install or render/apply commands. After `/readyz`
answers, finish the Enterprise guide's admin sign-in and `make smoke` check. When owner
certificates come from an internal CA, the browser and the machine running that check must
trust it (`CURL_CA_BUNDLE` for curl). Cost estimates: https://simple-host.app/costs.

## Where to run a small box

Any fresh Ubuntu server with a public IPv4 address works. We recommend **UpCloud**: the smallest
UpCloud server (1 CPU, 1 GB, about $4/month) runs Simple Host comfortably; we test on it.
[Create your UpCloud account — $25 in credits](https://signup.upcloud.com/?promo=JF2WCV)
(referral link. New accounts through this link get $25 of UpCloud credit; their terms
apply.)

1. Create your UpCloud account.
2. In the UpCloud control panel, create an **API token** (Account → API tokens; recommended, it
   can have an expiry and an IP allow-list) or an **API user** (a sub-account with API access
   allowed, with only the server permissions it needs). If you can, allow only your own IP
   address.
3. Answer the questions at https://simple-host.app/setup?product=small-box, with **UpCloud** as
   where it runs.
4. On the files step, run one of the two lines it gives in your own terminal: the first asks for
   the token and keeps it there as `UPCLOUD_TOKEN` (which `upctl` reads), the second asks for the
   API user's name and password and keeps them as `UPCLOUD_USERNAME` and `UPCLOUD_PASSWORD`.
   Start your AI agent in that terminal and give it the prompt. The agent tells you the plan and
   its price (from `GET /1.3/price`) before creating anything, creates the smallest Ubuntu 24.04
   server with `upctl` or the UpCloud API and your SSH key, has you add the DNS records (A records
   for your domain and `*.<domain>`, pointing at the server), runs the installer from its pinned
   release with your choices, checks `/healthz`, HTTPS and the release, and tells you the admin
   page. If anything fails, paste the error at
   https://simple-host.app/setup?product=small-box#help.
5. When the server is up, run `unset UPCLOUD_TOKEN UPCLOUD_USERNAME UPCLOUD_PASSWORD` in that
   terminal (or close it): until then every program started there can read them.

The setup page never asks for or accepts UpCloud credentials.

**After the install.** The installer prints the admin key; it is also kept on the server as
`ADMIN_API_KEY` in `/opt/simple-host/.env`, and re-running the installer prints it again. Open
`https://<domain>/admin` and paste it there to sign in as the admin. The admin page names the
release the box runs; on the server this says the same (`simple-host` is not a command on the
server itself):

```
cd /opt/simple-host && sudo docker compose exec -T app simple-host version
```

The admin's own sites live under the domain's first label (`sites.<domain>/<label>/<site>/`), or
`organiser` when that label is reserved.

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
| [Server and addresses](/docs/self-hosting/settings/server-and-addresses.md) | The domain, the separate content host, per-person and per-site addresses, setup mode |
| [Accounts and sign-in](/docs/self-hosting/settings/accounts-and-sign-in.md) | Owner sign-in (email codes, Google), API keys, visitor sign-in, AI app connections |
| [Sites and versions](/docs/self-hosting/settings/sites-and-versions.md) | Site and upload size, versions kept, preview and download links, sites per account |
| [Saved data](/docs/self-hosting/settings/saved-data.md) | What pages save, undo and history, size limits, who may write |
| [Storage migration plan](/docs/self-hosting/settings/storage-migration.md) | Shipped KV, SQLite and files resources; exact legacy behavior that needs redesign before any owner-directed migration |
| [Domains and certificates](/docs/self-hosting/settings/domains-and-certificates.md) | Custom domains, address families, their checks and release, certificates |
| [Cleanup and retention](/docs/self-hosting/settings/cleanup-and-retention.md) | Recently deleted, idle-site cleanup, how long analytics are kept |
| [Email](/docs/self-hosting/settings/email.md) | Sending email with Resend, the sender, reply-to |
| [Hosted events](/docs/self-hosting/settings/hosted-events.md) | Running the server as a hosted hackathon platform (simple-hack.app): event limits |
| [Storage and backups](/docs/self-hosting/settings/storage-and-backups.md) | The database, the site files, what to back up |
| [Observability](/docs/self-hosting/settings/observability.md) | Health, the startup log, the admin page, analytics |
| [Rate limits](/docs/self-hosting/settings/rate-limits.md) | Every rate limit in one table |

## Keeping this in sync

[settings.json](https://github.com/vineetu/simple-host/blob/main/docs/advanced/settings.json) is generated from the code (`simple-host settings --json`); the
tables on these pages and the setup helper are checked against it by `make check`.
[Configuration](/docs/self-hosting/configuration.md) is the entry page for the ten registry areas.
After changing a setting in code, run
`bash scripts/sync-settings.sh` and commit what it changes. The setup helper's copy of the
enterprise settings comes from an enterprise checkout
(`ENTERPRISE_REPO=/path/to/simple-host-enterprise bash scripts/sync-settings.sh`).

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](https://github.com/vineetu/simple-host/blob/main/docs/your-home-page.md).

Defaults in `<angle brackets>` are derived from other settings. Server, sign-in,
email and other non-knob settings are read by `internal/config/config.go`;
`MAX_ARCHIVE_MB` and `KEEP_VERSIONS` are read by the handler.

## Operational times and limits

Every operational time and limit Simple Host enforces can be changed per install with an
environment variable. Unset (or blank) means the value in the generated Default column.
Hosted installations may override these defaults.

- **Where they are read.** Once, at startup, in `internal/config/limits.go` (`Knobs()` is the
  list). A value that is not a whole number, is out of range, or does not fit with another
  setting stops the server with an error naming the variable, rather than running with a value
  nobody chose. Every knob that differs from its default is printed once in the startup log
  (`limits changed from the defaults: ...`).
- **Where to set them.** simple-host.app: `/etc/simple-host.env`, then restart. Docker Compose
  and `deploy/install/install.sh` boxes: `/opt/simple-host/.env` (`.env.example` lists them
  commented out; `compose.yaml` passes each one through; the installer keeps them on a re-run),
  then `docker compose up -d`.
- **What they change.** The enforcement and every sentence that states the value: emails, the
  owner app, the landing and feature pages, the privacy page, the admin page, `llms.txt`, the
  OpenAPI spec, the skills served from this install, and the connector's MCP instructions,
  tool descriptions and results. Served pages and documents are written with the defaults and
  rewritten when served (`internal/handler/limitstext.go`, the same way the hostnames are
  rewritten for an instance on another domain); with every knob at its default nothing is
  rewritten. The skills published to the Claude and ChatGPT plugin directories describe
  simple-host.app and keep its values.
- **Units.** Units are in the setting names (`_MINUTES`, `_HOURS`, `_DAYS`); counts are
  whole numbers. See [Rate limits](/docs/self-hosting/settings/rate-limits.md) for the `<burst>,<every>` format.
- **Changes apply to new deletions and warnings.** The date a person was promised is stored
  when the promise is made: a deleted site keeps its purge date (`DELETED_RETENTION_DAYS`), an
  idle-warned site its removal date (`IDLE_GRACE_DAYS`), a failing domain whose owner was
  emailed its disconnect date (`DOMAIN_LAPSE_HOURS`). Shortening one never removes anything
  earlier than its email said; lengthening one does not extend what was already promised.
  Likewise, tightened token and session lifetimes (`OAUTH_*_TTL_*`, `VISITOR_SESSION_*`,
  `PREVIEW_LINK_TTL_MINUTES`, `EXPORT_LINK_TTL_MINUTES`, `SIGNIN_CODE_TTL_MINUTES`) don't
  shorten ones already issued: each carries the expiry it was issued with.
- **Skills and cached copies.** The skills served from this install state its limits, but their
  advertised version (`/skills/version`) follows the skills' text, not the settings, so an
  agent that already installed them keeps the old numbers until it reinstalls. Reinstall the
  skills (or tell people to) after changing a limit they state.
