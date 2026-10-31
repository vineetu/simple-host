# Run the small box on DigitalOcean

Verified 2026-10-31 in local Docker containers (no DigitalOcean token configured on the release host) with the published v0.7.22 image: a v0.7.21 box with a site and a KV value upgraded to v0.7.22 by a bare `migrate` re-run, kept its site, KV value and admin key, and applied the 7 new migrations once. `/healthz`, the home page and `/.well-known/mcp/server-card.json` answered correctly, `docker compose config` parsed, and the published image's binary reports `v0.7.22 (commit 3a097ad)`. The droplet-specific 1-Click/Packer image itself was not rebuilt or re-verified live in this pass — only its pins were updated to match.

Verified 2026-10-24 on a real DigitalOcean droplet (sfo2, `s-1vcpu-2gb`, a `<ip>.nip.io` name, a Let's Encrypt certificate) with the published v0.7.21 image: a v0.7.20 box with a site, a KV value and a records SQLite resource upgraded to v0.7.21 by re-running the installer with no flags (no new migrations: this release is a Go 1.27.3 toolchain and dependency bump only). The box kept its addresses, admin key, site, KV value and records row. `/healthz`, the home page and `/.well-known/mcp/server-card.json` answered correctly, `docker compose config` parsed, and the published image's binary reports `go1.27.3`. The droplet was deleted.

Verified 2026-10-24 on two real DigitalOcean droplets (sfo2, `s-1vcpu-2gb`, `<ip>.nip.io` names, Let's Encrypt certificates) with the published v0.7.20 image: a fresh install, and a v0.7.19 box with a site, a public KV value and a records SQLite resource upgraded to v0.7.20 by re-running the installer with no flags. Both boxes kept their addresses, admin key, site and KV value, applied `zg-oidc-signin.sql`, `zh-site-notifications.sql`, `zi-site-search-summary.sql` and `zj-hack-notifications.sql` once, and generated `PASSCODE_ENC_KEY` on the upgrade, then kept the same key byte-for-byte on a second bare re-run. `/healthz`, the home page, `/.well-known/mcp/server-card.json`, `/auth.md` and an MCP `initialize` call all answered correctly, and `docker compose config` parsed. Both droplets were deleted.

Verified 2026-10-15 on two real DigitalOcean droplets (sfo2, `s-1vcpu-2gb`, `<ip>.nip.io` names, Let's Encrypt certificates) with the published v0.7.19 image: a fresh install; a v0.7.17 box with a site, a public KV value and a records SQLite resource upgraded straight to v0.7.19 by re-running the installer with no flags; and a v0.7.17 box taken through v0.7.18 to v0.7.19 the same way. Every box kept its addresses, the site, the KV value and the presets, applied `ze-spam-checked-posts.sql` and `zf-signup-detail.sql` once, refused the comments preset with `spam_check_unavailable` until Turnstile keys were added to `.env` (then accepted it, and a re-run kept the keys), and named itself, never simple-host.app, in robots.txt, Get started, llms.txt and the connector's instructions. Both droplets were deleted.

Verified 2026-10-10 with the published v0.7.17 image in disposable native arm64 containers: fresh install and an actual v0.7.16 upgrade through the installer's compose file (the image's `migrate`, as install.sh runs it) preserve the site list, KV values and resource policies across the swap; the storage-presets migration applies once and nothing is left pending; existing resources show their preset (an open write-anyone resource as `legacy`), and a preset with per-table rules, the row edit route and an R1 refusal answer as designed.

Verified 2026-10-10 with the published v0.7.16 image in disposable native arm64 containers: fresh install and an actual v0.7.15 upgrade through the installer's compose file (the image's `migrate`, as install.sh runs it) preserve the site list, KV values and resource policies across the swap; the new migration applies once and nothing is left pending, and the named-viewers and visitor-lookup routes answer. All four binary checksums pass, and both CPU images are published (arm64 run). This check used localhost HTTP against the app, without Caddy; earlier evidence below remains dated to its tested versions.

Verified 2026-10-10 with the published v0.7.15 image in disposable native arm64 containers: fresh install and an actual v0.7.14 upgrade through the installer's compose file preserve the site list, KV values and resource policies across the swap, and the click-to-play landing and its film files (206 ranges, video/mp4, video/webm, image/jpeg) serve through Caddy. Both Linux binary checksums pass, and both CPU images are published (arm64 run, amd64 binary inspected). This check used localhost HTTP against the API; earlier cloud and TLS evidence below remains dated to its tested versions.

Verified 2026-10-09 with the published v0.7.12 image in disposable native arm64 containers: fresh install and an actual v0.7.11 upgrade through Caddy preserve site files, KV, SQLite, file objects and existing full policies across restart. Add/own schemas, a 150-photo gallery, NFC uploads and the separate file pool pass. Generate and status return 404, and the AI API tag and model settings are absent. All four published binary checksums and anonymous pulls of both CPU images pass. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to its tested versions.


Verified 2026-10-09 with the published v0.7.11 image in disposable native arm64 containers: fresh install and an actual v0.7.10 upgrade through Caddy preserve site files, KV, SQLite, file objects and existing full policies across restart. Add/own schemas, a 150-photo gallery, NFC uploads and the separate file pool pass. Removed endpoints return 404; setup generators and settings remain available. All four binary checksums and anonymous pulls of both CPU images pass. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to its tested versions.


Verified 2026-10-06 with the published v0.7.10 image in disposable local containers: fresh installation and an actual v0.7.9 upgrade through Caddy preserved site files, KV, SQLite, file objects and full-mode policies; add/own order schemas, 150 public photos, NFC upload names, the separate file pool and restart persistence passed. This check used native arm64 and localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

Verified 2026-10-05 with the published v0.7.9 image in disposable local containers: a fresh schema and an actual v0.7.4 upgrade through Caddy preserved site content and passed home selection/clearing, showcase fallback, bio, pins/order, live feed and restart persistence. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

![Simple Host on UpCloud or any Ubuntu server, a DigitalOcean droplet included: two DNS A records point at the server; one Ubuntu server runs the installer's docker compose, with Caddy on ports 80 and 443 fetching certificates on demand from Let's Encrypt, the Simple Host server, Postgres, and Docker volumes for the sites, the log, the certificates and the database](https://simple-host.app/diagrams/upcloud.svg)

Tested 2026-10-01 on DigitalOcean (nyc3): the 1-Click image from
[`deploy/digitalocean/droplet/`](../../deploy/digitalocean/droplet/) built in
about 7.5 minutes (3.2 GB snapshot) and passed DigitalOcean's image check; on a
$6 droplet, first-login setup installed v0.7.4 in under a minute, a site deployed
and served over Let's Encrypt, and `upgrade.sh` kept the addresses. Until it is
listed in the Marketplace, build it into your own account (below).

A droplet is an Ubuntu server like any other, so the small box runs on it with
the standard installer: the same layout as on UpCloud. Start from the Simple
Host 1-Click droplet, which asks for your domain when you first log in and
runs the installer for you, or from a plain Ubuntu droplet where you run the
installer yourself.

## Cost

| Item | Size | Per month |
|---|---|---|
| Droplet `s-1vcpu-1gb` | 1 CPU, 1 GB, 25 GB disk, one IPv4 address | $6 |
| Snapshot of the 1-Click image (only if you build it yourself) | a few GB | $0.06/GB a month |

The database, the web server and the sites all run on that one droplet. The
$4 droplet has 512 MB, which is below what the small box needs. Weekly backups
are optional and add 20%.

## With the 1-Click droplet

1. In the DigitalOcean control panel, create a droplet from **Simple Host**
   (Marketplace), or from your own snapshot (**Snapshots** tab): size
   `s-1vcpu-1gb` or larger in any region, with your SSH key.
2. At your DNS provider, add two A records pointing at the droplet's public
   IPv4 address: your domain, and `*.<your domain>`. If the sites address is
   not under your domain, add a third for it.
3. Log in as root: `ssh root@<droplet IP>`. The setup starts by itself. It asks
   for the address (your domain) and the address sites are served from
   (`sites.<your domain>` unless you change it), checks that DNS points at the
   droplet, then runs the installer from its pinned release after checking its
   sha256. A dropped SSH connection does not stop the install. Setup runs until
   an install succeeds: if you stop it or the install fails, it runs again at
   your next login, or now with `/opt/simple-host-setup/first-login.sh`.
4. Open `https://<your domain>/admin` and paste the admin key the installer
   printed (also kept as `ADMIN_API_KEY` in `/opt/simple-host/.env`).

The droplet's firewall (UFW) allows only SSH, HTTP and HTTPS. A port you later
publish from a Docker container bypasses UFW.

## Build the 1-Click image yourself

You need [Packer](https://developer.hashicorp.com/packer/install) and a
DigitalOcean API token with an expiry (API → Tokens). From the repository
root:

```sh
make -C deploy/digitalocean/droplet validate
```

```sh
read -rsp 'DigitalOcean API token: ' DIGITALOCEAN_API_TOKEN && echo && export DIGITALOCEAN_API_TOKEN && make -C deploy/digitalocean/droplet build; unset DIGITALOCEAN_API_TOKEN
```

The build provisions a temporary `s-1vcpu-1gb` droplet in `nyc3` with Docker
and the release's images, runs DigitalOcean's cleanup and image checks on it,
and saves it as a snapshot named `simple-host-24-04-snapshot-<time>`. Packer
deletes the droplet afterwards. No admin key, password, certificate or database is in the image; the
installer makes them on the droplet at first login.

## By hand

1. In the DigitalOcean control panel, create a droplet: image **Ubuntu 24.04
   (LTS) x64**, size `s-1vcpu-1gb`, any region, and your SSH key.
2. At your DNS provider, add two A records pointing at the droplet's public
   IPv4 address: your domain, and `*.<your domain>`.
3. Answer the questions at https://simple-host.app/setup?product=small-box
   with **a server I already have**, then run the install command it gives on
   the droplet over SSH. It fetches the installer from its pinned release and
   checks its sha256 before running it.
4. Open `https://<your domain>/admin` and paste the admin key the installer
   printed (also kept as `ADMIN_API_KEY` in `/opt/simple-host/.env`).

If anything fails, paste the error at
https://simple-host.app/setup?product=small-box#help.

## Everyday operations

- **Admin key lost:** `grep ^ADMIN_API_KEY= /opt/simple-host/.env` on the droplet.
- **Upgrade (1-Click):** `/opt/simple-host-setup/upgrade.sh`. It runs the
  installer that https://simple-host.app/setup pins (checked against its
  sha256) with the addresses from `/opt/simple-host/.env`, so they are kept,
  along with your data, key and settings.
- **Upgrade (by hand):** answer the setup page again with the same domain and
  sites address, and run the command it gives. It has to carry `--host` and
  `--content`: without them the installer empties both in `.env`.

## Removing it

Destroy the droplet in the DigitalOcean control panel (Destroy → Destroy
Droplet), with its backups and snapshots if you made any, then remove the DNS
records. Nothing else was created.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).
