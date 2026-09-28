# Advanced settings

Simple Host runs with no settings beyond its address, and every default is what
simple-host.app itself runs. This folder explains each advanced feature in plain terms, lists
every setting that changes it, and gives recipes for common changes.

**Easiest start: the setup helper at https://simple-host.app/setup?product=small-box.** It asks a
few questions (or every question, in Advanced mode), keeps the default for anything you skip, and
writes the one-line install command and the `.env` for a small box. It runs in your browser and
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

For **Simple Host Enterprise** (https://simple-host.app/setup?product=enterprise) the basics also
ask for the ingress controller's pod range (`TRUSTED_PROXY_CIDRS`, optional: read the pods'
addresses with `kubectl -n <ingress namespace> get pod -o wide`; empty keeps every private range).
With UpCloud as the bucket provider the page asks for the Object Storage region (such as
`europe-2`) and says to reach UpCloud's managed Postgres at its `public-…` hostname on port 11569.

## Where to run a small box

Any fresh Ubuntu server with a public IPv4 address works. We recommend **UpCloud**: the smallest
UpCloud server (1 CPU, 1 GB, about $5/month) runs Simple Host comfortably; we test on it.
[Create your UpCloud account — $25 in credits](https://signup.upcloud.com/?promo=JF2WCV)
(referral link. New accounts through this link get $25 of UpCloud credit; their terms
apply.)

1. Create your UpCloud account.
2. In the UpCloud control panel, create an API user: a sub-account with API access allowed.
   Give it only the server permissions it needs and, if you can, allow only your own IP
   address in its API settings.
3. Answer the questions at https://simple-host.app/setup?product=small-box, with **UpCloud** as
   where it runs.
4. On the files step, run the one line it gives in your own terminal (it asks for the API user's
   name and password and keeps them there as `UPCLOUD_USERNAME` and `UPCLOUD_PASSWORD`), start
   your AI agent in that terminal and give it the prompt. The agent creates the smallest Ubuntu
   24.04 server with `upctl` or the UpCloud API and your SSH key, has you add the DNS records
   (your domain and `*.<domain>`), runs the installer from its pinned release with your choices,
   checks `/healthz` and HTTPS, and tells you the admin page. If anything fails, paste the error
   at https://simple-host.app/setup?product=small-box#help.
5. When the server is up, run `unset UPCLOUD_USERNAME UPCLOUD_PASSWORD` in that terminal (or
   close it): until then every program started there can read the API user.

The setup page never asks for or accepts UpCloud credentials.

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
