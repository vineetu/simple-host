# Advanced settings

Simple Host runs with no settings beyond its address, and every default is what
simple-host.app itself runs. This folder explains each advanced feature in plain terms, lists
every setting that changes it, and gives recipes for common changes.

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

For **Simple Host Enterprise** (https://simple-host.app/setup?product=enterprise) nothing is
required either: an empty answer keeps its default (database and role `simplehost`, port 5432) or
is a `# Fill in:` blank in config.env, and on the AWS quick path the one line asks for anything you
left empty when it runs in CloudShell. The Postgres host takes any hostname, Kubernetes service
name (`postgres.db.svc.cluster.local`, `pg-rw.db`) or IP address, with `:port`. The basics also
ask for the ingress controller's pod range (`TRUSTED_PROXY_CIDRS`, optional: read the pods'
addresses with `kubectl -n <ingress namespace> get pod -o wide`; empty keeps every private range).
With UpCloud as the bucket provider the page asks for the Object Storage region (such as
`europe-2`), fills in port 11569 for UpCloud's managed Postgres and says to reach it at its
`public-…` hostname. Picking another identity or bucket provider never writes its template
address over one you typed. The config.env it writes is complete: it includes the lines
INSTALL.md says to leave as in the example (`PORT`, `HTTPS_REDIRECT_PORT`, `OIDC_SCOPES`,
`SESSION_TTL`, `SESSION_IDLE`, `DB_SSLMODE`, `BACKUP_STORAGE_PREFIX`, `BACKUP_SSE`), and anything
not listed keeps its default. The block for your AI agent ends with INSTALL.md's definition of
done: HUMAN STEP D (a Full key) and `make smoke`, run with `CURL_CA_BUNDLE` naming the company
CA when owner certificates come from an internal CA. What Enterprise costs to run on AWS, Azure
or Google Cloud, for your number of people: https://simple-host.app/costs.

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
| [Server and addresses](server-and-addresses.md) | The domain, the separate content host, per-person and per-site addresses, setup mode |
| [Accounts and sign-in](accounts-and-sign-in.md) | Owner sign-in (email codes, Google), API keys, visitor sign-in, AI app connections |
| [Sites and versions](sites-and-versions.md) | Site and upload size, versions kept, preview and download links, sites per account |
| [Saved data](saved-data.md) | What pages save, undo and history, size limits, who may write |
| [Domains and certificates](domains-and-certificates.md) | Custom domains, address families, their checks and release, certificates |
| [Cleanup and retention](cleanup-and-retention.md) | Recently deleted, idle-site cleanup, how long analytics are kept |
| [Email](email.md) | Sending email with Resend, the sender, reply-to |
| [Hosted events](hosted-events.md) | Running the server as a hosted hackathon platform (simple-hack.app): event limits |
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
