# Self-hosting release history

Release and test records moved from the installer guides. Excerpts whose links
use the old source directory are shown as Markdown source to preserve their text.
Working guide links follow those excerpts.

## coolify.md

Verified 2026-10-31 in local Docker containers (no DigitalOcean token configured on the release host) with the published v0.7.22 image: a v0.7.21 box with a site and a KV value upgraded to v0.7.22 by a bare `migrate` re-run, kept its site, KV value and admin key, and applied the 7 new migrations once. `/healthz`, the home page and `/.well-known/mcp/server-card.json` answered correctly, `docker compose config` parsed, and the published image's binary reports `v0.7.22 (commit 3a097ad)`.

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

Tested 2026-10-01 on a DigitalOcean droplet (2 vCPU, 4 GB) with Coolify 4.3.23.
The resource was created through Coolify's API with the same settings as the steps
below, and the screens were not clicked through, so the button names follow
Coolify's source (v4.4.0). Coolify built the images, ran `release` (schema and 28
migrations), started the app healthy, and got Let's Encrypt certificates for both
names. A site published and loaded on `sites.<domain>`, `/internal/*` returned 404,
the visitor's address in the access log was the real one, no API key was in the
log, and a redeploy kept the site and had nothing to migrate.

Tested 2026-10-19 on Coolify 4.4.6 (a DigitalOcean `s-2vcpu-4gb` droplet, sslip.io
names): the schema and 57 migrations applied, both addresses got Let's Encrypt
certificates, the admin sign-in page loaded, an account was issued, a site was
published and served on the sites address, a visitor's form entry was saved on
the site's own address, and a restart had nothing to migrate.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).

## upcloud.md

Tested end to end on 2026-09-28 from the setup page's own output: server
created in about a minute, installer done in 50 seconds, about 3.5 minutes from
prompt to a working box with HTTPS. Admin sign-in, publish, saved data and
`/internal/*` hidden all checked. Then everything was deleted.

UpCloud is where we recommend running a small box, and where we test it. The
smallest server (1 CPU, 1 GB, Ubuntu 24.04) runs Simple Host comfortably with
the standard installer: no special image and no changes.

## simple-hack-digitalocean.md

**The full platform installer and a private Packer snapshot were tested on
DigitalOcean on 2026-10-01.** A Marketplace listing has not been submitted or
approved. This package is separate from the existing Simple Host small-box
snapshot. The packaged release download and pinned application image are
published.

The live test used one Ubuntu 24.04 `s-1vcpu-2gb` droplet in `nyc3`, with an
amd64 candidate built from source commit `7a1180b`. The platform, event and
team addresses used the temporary IP's `sslip.io` names and received trusted
Let's Encrypt certificates. A desktop organiser and mobile participant signed
in through the browser, joined an event, created a team and published its
project. Email-code messages were captured by an isolated mail transport sink
inside the test network; external email delivery was not tested.

Fresh installation applied all 38 migrations. Restart and a second-image
redeploy preserved the event, team, project, credentials, settings and existing
certificate. The second image contained the same candidate binary; this was
an upgrade/persistence check, not a migration between released versions.
Unknown/deeper hostnames were refused certificates, and the public internal
certificate-check route returned 404. The disposable droplet, SSH key and tag
were deleted after verification; no snapshot or backup was retained.

The published `hack-v0.8.0` ZIP and tarball matched their `SHA256SUMS` and
GitHub asset digests, extracted to identical 30-file trees, and the ZIP's
default installer passed its configuration and upgrade tests. Packer built a
private snapshot from that ZIP and the public amd64 image in 8 minutes 53
seconds. DigitalOcean's image checker passed all eight checks with no
warnings. The snapshot held 3.03 GB before deletion. On a new droplet from
the snapshot, the first root login asked for the domain, hidden mail key and
sender; installation continued after an intentional SSH disconnect. The
published image applied 39 migrations and served trusted HTTPS. Restart and
same-image upgrade retained the settings, a database row, a data-volume file
and the certificate. The test used a fake mail key and sent no email. The
build and first-boot droplets, private snapshot, temporary SSH key and tag
were deleted, then verified absent. Both droplets existed for about 15 minutes
combined; at the $0.01786/hour rate returned by DigitalOcean's API, compute
is about $0.0045 prorated or $0.03572 if each incurs a full-hour minimum, plus a small
short-lived snapshot storage charge. Billing was not independently confirmed.


The full platform uses the same hand-drawn landing film, ink theme, local
Caveat/Kalam fonts and per-event crayon colours as the hosted service. At
`https://<your-domain>/get-started`, “Pick your AI” leads to the instance's own
`https://<your-domain>/mcp`, role prompts and the FAQ; skill folders and plugin
ZIPs are under “Other ways to install” and also name your instance. Use those
instance downloads rather than the hosted GitHub skills when self-hosting.
Organisers share a join link and a private judge link; participants form teams,
publish a team site and complete an entry. No account keys are handed out as
part of that onboarding.

After startup, sign in at your domain, create an event and publish a team
site. Verify trusted certificates and the event and team hostnames from a
separate client. The private snapshot passed first-login and persistence
checks; the earlier live event walkthrough verified the installer on a plain
droplet. Neither is a Marketplace listing or a test of external email delivery.


## digitalocean.md

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

Tested 2026-10-01 on DigitalOcean (nyc3): the 1-Click image from
[`deploy/digitalocean/droplet/`](../../deploy/digitalocean/droplet/) built in
about 7.5 minutes (3.2 GB snapshot) and passed DigitalOcean's image check; on a
$6 droplet, first-login setup installed v0.7.4 in under a minute, a site deployed
and served over Let's Encrypt, and `upgrade.sh` kept the addresses. Until it is
listed in the Marketplace, build it into your own account (below).

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).


## render.md

The small box (the installer's Postgres + server + Caddy) runs on Render as one
web service with a disk, plus a Render Postgres database. Tested end to end on
2026-09-28 with v0.7.4, on a Starter service with a 1 GB disk and the smallest
paid Postgres: first deploy (the Pre-Deploy Command loaded the schema and
applied every migration), both custom domains with HTTPS, first admin and admin
sign-in, a participant account, publish, update, rollback, saved data, the
Caddy routes, `/internal/*` hidden, a redeploy (migrations: nothing to apply)
and a restart with the site and data intact, and the visitor's real address in
visitor analytics (a spoofed `X-Forwarded-For` is ignored). Then everything was
deleted. The service and database were created through Render's API with the
exact settings in `render.yaml`, and Render's Blueprint validator accepts that
file as it is.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).


Prices from render.com/pricing on 2026-09-28. The server and Caddy used about
75 MB on Fly, so 512 MB is plenty. The test box used 16 MB of its 1 GB disk.

Why not free: a free web service has no disk, so published sites live in the
container and are gone after a deploy, a restart, or the sleep that follows
15 minutes without traffic (seen in a free-plan test: after a redeploy the
site was a 404 while its database rows were still there). Free services also
have no Pre-Deploy Command, and a free Postgres expires after 30 days.

   Render verifies each name and issues its certificate by itself; in the test
   both were live within a minute. (An apex name, `example.com`, takes an A
   record instead; the service's Settings page shows it.)

- **Upgrade:** set the new `SIMPLE_HOST_VERSION` in the `Dockerfile` and push.
  The Pre-Deploy Command runs the new release's migrations first; if they fail,
  the deploy stops and the old version keeps running. A redeploy with nothing
  new logs `database is up to date; nothing to apply`. With a disk there is no
  zero-downtime deploy: the old instance stops before the new one starts. In
  the test, sites answered 502 for about 30 seconds.
- **Restart:** **Manual Deploy > Restart service** on the service page. Sites,
  saved data and accounts all survive; about 30 seconds of 502 in the test.
- **Settings:** anything in [configuration.md](../configuration.md) goes in the
  service's Environment tab. Email sign-in codes need `RESEND_API_KEY` and
  `MAIL_FROM`; without them participants use the keys the admin issues, which
  is how a small box runs by default.
- **Backups:** Render snapshots the disk daily and keeps at least 7 days (the
  disk's page restores one). Paid Postgres has point-in-time recovery (3 days
  on Hobby). "Download my data" and the admin export work as on any small box.
- **Delete everything:** delete the Blueprint's service and database in the
  dashboard (deleting the service takes its disk and custom domains with it),
  then remove the two DNS records.

- **A card is required.** Without one, Render refuses the disk, the Pre-Deploy
  Command and paid Postgres ("Payment information is required to complete
  this request"). Do not try to run it on the free plan: sites vanish.
- **The admin page's disk figure is the Render disk** (973 MB usable on a
  1 GB disk). Grow the disk on its page if it fills; it cannot shrink.
- **Health checks use your domain.** Once the service has custom domains,
  Render sends its checks with one of them as the Host, which can be
  `sites.<domain>`. There the content routes answer 404, so the first deploy
  after adding the domains never went live and failed after 15 minutes. The
  Caddyfile sends `/healthz` to the server on every host.
- **Visitor addresses.** Render's proxy reaches Caddy over loopback (`::1`)
  and sits behind Cloudflare. The first `X-Forwarded-For` entry is whatever the
  visitor sent (a test with `X-Forwarded-For: 6.6.6.6` came through as the
  visitor), so the Caddyfile reads `CF-Connecting-IP`, which Cloudflare sets
  and refuses (403) from a visitor, and sends the server that address alone
  in `X-Forwarded-For`. Without this, rate limits would share a few buckets
  (Render's internal proxies) and analytics could be spoofed.
- **Caddy's file capability.** Render will not start a program that carries
  file capabilities (`caddy: Operation not permitted`), so the Dockerfile
  removes Caddy's (it only lets Caddy bind ports below 1024).
- **Root and the disk.** A new disk belongs to root, and in Render's
  containers root cannot signal another user's processes. `start.sh` hands
  `/data` to the server's user and then runs everything as that user.
- **The release image cannot run alone.** It is the server only. Sites are
  served from disk by Caddy, and Caddy also hides `/internal/*` and writes the
  access log that visitor analytics reads. Pointing Render straight at the
  server leaves every site a 404 and exposes `/internal/*`.
- **A new database is empty.** `simple-host migrate` only adds to an existing
  schema. `start.sh release` loads `db/schema.sql` from the same release tag
  first, only when the `users` table is missing.
- **CMD, not ENTRYPOINT.** The image starts with `CMD`, so the Pre-Deploy
  Command (`/usr/local/bin/start.sh release`) runs on its own rather than
  being handed to an entrypoint.

## railway.md

Built 2026-10-27 and verified locally first; a real end-to-end pass against the existing
Railway account ran 2026-11-06 (see "What was tested" below): build, migrations, sign-in,
a published site, KV, and a restart all worked. The template itself is not published yet —
the Railway connector and CLI used for the test cannot run **Generate Template from Project**
or **Publish**; that step needs a signed-in dashboard or CLI session.
[SIM-212](https://linear.app/simple-hack/issue/SIM-212) ("Grok bot: create the Railway account,
publish the Simple Host template, and test one deploy") tracks publishing the template.

## What was tested

Built and run locally on 2026-10-27 with the exact `deploy/platforms/railway` Dockerfile,
`PORT` set the way Railway injects it, and a volume at `/data`, against a local Postgres
container (no Railway account was available to this agent yet): `start.sh release` (the
Pre-Deploy Command) loaded `schema.sql` into an empty database and applied all 61
migrations cleanly; the admin key signed in at `/v1/me`; `POST /v1/admin/users` issued a
participant account and key; that key published a site (`POST /v1/sites/{name}` with the
archive as the raw body, `Content-Type: application/zip`), which answered 200 on the
`sites.`-prefixed host and correctly 404 on the dashboard host (content only ever serves
on `sites.*`, matching production); a KV resource and key written through
`/v1/sites/{name}/storage/kv/...` read back correctly; and a container restart (`docker
restart`) kept the site, the KV value and the admin account, with `/healthz` answering 200
before and after. One bug this test caught before it could ship: `start.sh` was first
committed without its executable bit, which would have made the container fail to start
entirely (`permission denied`) the moment it shipped on Railway's `CMD`-based image, exactly
as it does locally without `chmod 755`.

**Real Railway deploy, 2026-11-06 (SIM-212):** the project above was built for real on the
existing Railway account (Hobby workspace, no new account or card), Root Directory
`deploy/platforms/railway`, following the Commands steps exactly. Build and healthcheck
passed; Pre-Deploy logged the same `schema.sql` load and 61 migrations, then the app started;
the admin key signed in at `/v1/me`; `POST /v1/admin/users` issued a participant account; a
test site published (`POST /v1/sites/{name}`, raw zip body) and answered 200 on the
`sites.`-prefixed host; a KV resource (`PUT /v1/sites/{site}/storage/resources/{name}` with
`{"kind":"kv",...}`) and key (`PUT /v1/sites/{site}/storage/kv/{name}/keys/{key}` with
`{"value": <json>}`) wrote and read back correctly; a service restart kept the site, KV
value and admin account, with `/healthz` 200 before and after. Custom DNS (step 7) was not
tested, no spare domain was available. One limit found: a generated `*.up.railway.app`
domain is a single label, so the `sites.`-prefixed host cannot be reached over the public
internet on it; that needs a real custom domain (step 7) or testing over Railway's private
network with the `sites.` Host header set by hand. **Generate Template from Project** and
**Publish** are still not done: neither the Railway connector nor Railway's own agent can
run them, and no CLI or dashboard session was signed in from this box. That is the one step
left before the slug and "Deploy on Railway" button exist.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).


Rates from railway.com/pricing on 2026-10-27, billed per second (stopped services cost
nothing). This is an estimate, not a measured bill: no Railway account was available to run
it for a full month. The server and Caddy used about 75 MB on Fly's equivalent test, so a
small always-on service like this one stays well under Hobby's 48 vCPU/48 GB per-service
ceiling; the question is whether it fits inside the plan's included credit.

   Railway verifies each name and issues its certificate once the CNAME resolves; in
   earlier tests of this same image on other platforms that took under a minute.

````markdown
- **Check `DB_DSN`'s resolved value before deploying.** The Variables tab previews what
  `${{Postgres.DATABASE_URL}}?sslmode=disable` resolves to. Confirmed on a live Railway
  deploy 2026-11-06: `DATABASE_URL` carries no query string of its own, so the appended
  `?sslmode=disable` is valid as written. If that ever changes, use `&sslmode=disable`
  instead (appending a second `?` would otherwise produce an invalid DSN that lib/pq
  rejects rather than a silently insecure connection).
- **Railway's edge does not forward a client-supplied `X-Forwarded-For` or `X-Real-IP`
  unchanged; it drops both and sets its own.** Confirmed on a live deploy 2026-11-06
  (`curl -H "X-Forwarded-For: 1.2.3.4" ...` to a test site): the edge connects to the
  container from `100.64.0.0/10` (CGNAT, not an RFC 1918 private range), and forwards
  `X-Forwarded-For: <real client>, <Railway edge public IP>` and
  `X-Real-IP: <real client>`, both already correct. The Caddyfile now trusts
  `100.64.0.0/10` and reads `X-Real-IP` directly, so a visitor cannot spoof the address
  used for rate limits and analytics. (Before this fix, `trusted_proxies` only listed
  `private_ranges`, so Caddy trusted neither header at all and every visitor recorded as
  the same address.)
- **A card is required**, even for the trial credit.
- **No committed template file.** Unlike `render.yaml` or Coolify's `compose.yaml`, nothing
  here is "pasted into" Railway. The dashboard project built from the steps above, once
  published, is the template; `deploy/platforms/railway/README.md` is what lets it be
  rebuilt from scratch if the published copy is ever lost or needs a fix.
- **`PASSCODE_ENC_KEY` is not auto-generated.** It must be exactly 32 random bytes in
  standard base64 (`openssl rand -base64 32`). Railway's template `secret()` function
  generates a random string of a given length from a given alphabet, which cannot be made
  to reliably produce that exact byte-aligned, padded format. Rather than ship a key that
  looks generated but might not decode, this template leaves it unset: site passcodes stay
  off until you add the variable by hand. That is a real limit of this template, not an
  oversight — the feature works fully once you set the key, or is not offered at all.
- **Canonical per-person and per-site subdomains are not offered.** `PERSON_HOSTS` and
  `SITE_HOSTS` stay at their installer default (`off`, path-style addresses:
  `sites.hack.example.com/<handle>/<site>/`). Turning them to `canonical` would need an
  unbounded number of arbitrary subdomains two levels deep
  (`<site>.<handle>.hack.example.com`) provisioned on the fly. Railway's wildcard custom
  domains cover one fixed subdomain level (`*.sites.example.com`), not an arbitrary second
  level created per account, and Railway's edge always terminates TLS itself with no raw
  TCP/SNI passthrough an app could use to issue its own certificates for that pattern (the
  trick Simple Hack's Coolify/Traefik recipe uses). Path-style addresses are not a
  downgrade of a feature; they are what every other small-box platform guide (Fly, Render,
  Coolify) also ships by default.
- **Simple Hack standalone is not offered on Railway**, for the same reason, one level
  worse: it needs event sites AND team sites nested two wildcard levels deep
  (`<team>.<event>.hack.example.com`), which the same TLS-termination limit rules out
  entirely. Use the [Coolify](simple-hack-coolify.md) or
  [DigitalOcean](simple-hack-digitalocean.md) guide for Simple Hack.
- **Volumes cap at 5 GB on Hobby.** `MAX_ARCHIVE_MB` defaults the same as the installer
  (100). `KEEP_VERSIONS` defaults to 3 here, same deliberate deviation as the Render and
  Fly recipes — the installer itself defaults to 1 (no rollback); a hackathon-sized box
  with many large sites may need Pro (volumes up to 1 TB) sooner than on a VPS with its
  own disk.
- **The release image cannot run alone.** It is the server only. Sites are served from the
  volume by Caddy, and Caddy also hides `/internal/*` and writes the access log that
  visitor analytics reads. Pointing Railway straight at the server image leaves every site
  a 404 and exposes `/internal/*`.
- **A new database is empty.** `simple-host migrate` only adds to an existing schema.
  `start.sh release` loads `db/schema.sql` from the same release tag first, only when the
  `users` table is missing.
- **CMD, not ENTRYPOINT.** The image starts with `CMD`, so the Pre-Deploy Command
  (`/usr/local/bin/start.sh release`) runs on its own rather than being handed to an
  entrypoint.
- **Root and the volume.** A new volume is owned by root, and Railway's containers, like
  Render's, do not let root signal another user's processes. `start.sh` hands `/data` to
  the server's user and then runs everything as that user.
````

Guide links: [simple-hack-coolify.md](../platforms/simple-hack-coolify.md), [simple-hack-digitalocean.md](../platforms/simple-hack-digitalocean.md).

## README.md

````markdown
| Where | How | Status |
|---|---|---|
| UpCloud (recommended) | [this guide](upcloud.md): the installer on the smallest server | tested 2026-09-28, about $4/month; [$25 in credits](https://signup.upcloud.com/?promo=JF2WCV) (referral link) |
| Any other Ubuntu VPS | the installer, [simple-host.app/setup](https://simple-host.app/setup) | the same installer |
| Hostinger VPS | the installer | an Ubuntu VPS like any other |
| DigitalOcean | [this guide](digitalocean.md): the 1-Click droplet, or the installer on the $6 droplet | tested 2026-10-01, about $6/month |
| Fly.io | [this guide](fly.md) | tested 2026-09-28, about $4.50/month |
| Render | [this guide](render.md) | tested 2026-09-28, about $13.55/month (needs a card) |
| Coolify | [this guide](coolify.md) | tested 2026-10-01 on Coolify 4.3.23 (resource made through its API) |
| Railway | [this guide](railway.md): a GitHub-repo template, Railway Postgres and a volume | built and run locally 2026-10-27 with the exact image, env vars and volume layout (no Railway account available); the real deploy is tracked in a Linear ticket, about $5–15/month (needs a card) |
| Vercel, Netlify, shared PHP hosting | | not as-is: a 4.5 MB request limit (smaller than a site upload), no disk that keeps files, no always-on process |
````

Guide links: [upcloud.md](../platforms/upcloud.md), [digitalocean.md](../platforms/digitalocean.md), [fly.md](../platforms/fly.md), [render.md](../platforms/render.md), [coolify.md](../platforms/coolify.md), [railway.md](../platforms/railway.md).

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).

The shared app pages include Simple Host’s approved film landing, blue ink theme and readable selected controls in the verified v0.7.22 small-box release. Light/dark still follows the system and shared override; uploaded sites keep their own styles. Existing pinned releases keep their shipped UI.


## simple-hack-standalone.md

````markdown
**Release status: v0.8.20 is published.** Its default image and ZIP/tar downloads
are available from the [hack-v0.8.20 release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.20).
It carries the redesigned Simple Hack web app with Home as the place to run an
event, no active-event limit by default on self-hosted installs, and the
install fixes found by a clean install from the published downloads. The ZIP,
tarball and Helm chart match their checksums, and the image is published for
arm64 and amd64 (its binary reports `v0.8.20 (commit 6851b9f)`). Checked on
2026-11-11 in local Docker Compose: a fresh install from the downloaded
archive following this page (93 migrations, through
`0014-hack-overall-prizes.sql`), then a whole event in a browser with password
accounts and no email: create the event in the America/Los_Angeles time zone,
tracks, sign-up, two participants on one team, an entry, a team site published
from a zip, a judge, judging, published results and the public winners page. A
published v0.8.17 install with an account and an event was upgraded to v0.8.20
with `upgrade.sh`, applying 10 migrations and keeping its data;
[release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.20.md).
v0.8.13 carried the new default Simple Hack web app, private team finding and
applying without a join link
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.13.md)).
v0.8.12 was the same Go 1.27.3 toolchain and dependency bump as v0.7.21 (no
behavior change), checked on a real DigitalOcean droplet
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.12.md)).
v0.8.11 added Hack server and agent cards with discovery headers and resource
URNs, opt-in MCP Apps My sites and dashboard for selected teams and organiser
event websites, and MCP Events and signed webhooks for team and organiser
sites; it was checked on two real DigitalOcean droplets (sfo2, `s-1vcpu-2gb`,
`<ip>.nip.io` names, Let's Encrypt certificates): a fresh published-package
install records 61 migrations; a published v0.8.10 install with an event, a
member, a team key and a published team site upgraded to v0.8.11, applied the
three new migrations once and kept the event, team, team site, settings and
credentials
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.11.md)).
v0.8.10 added company sign-in through your own OpenID Connect provider,
`EMAIL_SIGNIN=off`, the `--tls internal`, `--tls acme`, `--tls certs` and `--ca-cert`
options for running inside a company network, and `/companies`
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.10.md)).
v0.8.9 added upload links and partial updates for team sites, the red product icon
and judging-first Manage
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.9.md)).
v0.8.8 added storage access presets to team sites and custom event websites
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-v0.8.8.md)).
v0.8.7 added the eight-scene landing film with narration, verified with native
arm64 and emulated amd64 version checks
([release record](https://github.com/vineetu/simple-host/blob/main/docs/history/simple-hack-release-2026-10-10.md)).
Earlier v0.8.6 checks covered event deletion through Settings at 390 and 1280 px
in light and dark, own-domain presentation, and local Coolify Traefik routing,
with 52 migrations.
The earlier v0.8.5 release passed its 42 browser states with 50 migrations.
An actual v0.8.3 package/image upgrade to v0.8.4 retains the database, membership,
entry, team key, team site files, settings and TLS CA. The earlier v0.8.1-to-v0.8.2
upgrade applied six migrations (44 to 50) and retained the same data. The local
checks cover the film, ink theme, event colours, own-domain connector/examples
and local fonts at 320, 390 and 1280 px in light and dark, including a real
take-down state with no broken icon requests. The current Coolify
Compose routing also passed through real local Traefik. The live DigitalOcean installation and private snapshot
tests used
v0.8.0; [those test details](simple-hack-digitalocean.md) distinguish a private
snapshot from Marketplace approval. This package is separate from the older
Simple Host small-box installer.
````

Guide links: [simple-hack-digitalocean.md](../platforms/simple-hack-digitalocean.md).

The full platform uses the same hand-drawn landing film, ink theme, local
Caveat/Kalam fonts and per-event crayon colours as the hosted service. At
`https://<your-domain>/get-started`, “Pick your AI” leads to the instance's own
`https://<your-domain>/mcp`, role prompts and the FAQ; skill folders and plugin
ZIPs are under “Other ways to install” and also name your instance. Use those
instance downloads rather than the hosted GitHub skills when self-hosting.
Organisers share a join link and a private judge link; participants form teams,
publish a team site and complete an entry. No account keys are handed out as
part of that onboarding.

All four `MCP_APP_*` flags are passed to the container, default off, and kept in
`.env` across reruns. Supply them through `--config`, or export them for the
first install. Hack's product gates still apply; these flags alone do not offer
Host screens to organisers or teams. The Coolify and DigitalOcean packages
carry the same settings.

After upgrading to a release with `zi-site-search-summary.sql`, populate old
site summaries after migration with this idempotent command:

```sh
docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env run --rm --no-deps -T app site-summary-backfill
```

## fly.md

The small box (the installer's Postgres + server + Caddy) runs on Fly.io as two
apps: one for Simple Host, one for its Postgres. Tested end to end on
2026-09-28 with v0.7.4: deploy, first admin, admin sign-in, publish, update,
rollback, saved data, download my data, machine restart, redeploy with
migrations, and `/internal/*` hidden. Then everything was deleted.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).


Everything at the smallest size worked: the Simple Host machine used about
75 MB of its 256 MB (server ~25 MB, Caddy ~50 MB).

5. Point both names at the app, then ask Fly for their certificates. A CNAME to
   `my-sh.fly.dev` for each name is enough (or the A and AAAA records that
   `fly certs add` prints). The certificates were issued within a minute.

## simple-hack-coolify.md

Simple Hack: **Hackathons, now for everyone.** Participants describe an idea to
their AI and it builds their team's site. No coding needed. The best idea wins,
not the best coder. Organisers choose the platform for college students, product
managers and leaders, and people who aren't programmers.
Its default `ghcr.io/vineetu/simple-hack:0.8.20` image can be overridden with
`SIMPLE_HACK_IMAGE`. Current release verification is in the standalone guide;
the dated cloud test below records the release actually exercised there.

````markdown
**Tested on Coolify 4.3.23 through its API on 2026-10-02.** This guide uses a
user-defined **Docker Compose Empty Service**. Coolify deployed the published
`ghcr.io/vineetu/simple-hack:0.8.1` image, ran all 44 migrations, and served an
event and a published team site over trusted HTTPS. The existing
[small-box Coolify guide](coolify.md) describes a different installation.
````

Guide links: [coolify.md](../platforms/coolify.md).

[`deploy/hack/standalone/coolify/service-template.yaml`](../../deploy/hack/standalone/coolify/service-template.yaml)
is the same file in Coolify's service-template form: Coolify generates the
database user and password, `ADMIN_API_KEY`, `PASSCODE_ENC_KEY` and
`ANALYTICS_SALT`, so only `SITE_DOMAIN` and sign-in (`RESEND_API_KEY` with
`MAIL_FROM`, or company sign-in) are left to set. Steps 1 and 2 below still
apply. Tested 2026-10-19 on Coolify 4.4.6 with hack-v0.8.10: the release step
applied 58 migrations, the apex and an event's own address got Let's Encrypt
certificates through the passthrough, the sign-in page loaded, and an event was
created through the API.


The tested API path sent the exact file as base64 `docker_compose_raw` to
`POST /api/v1/services`, with `is_container_label_escape_enabled=false`, then
set the seven required environment values and started the Service. Coolify
normalizes Source Compose and generates a separate Deployable Compose. The
running container retained the TLS passthrough, PROXY protocol, HTTP routing,
and network labels. A Git-based Docker Compose **Application** with **Raw
Compose Deployment** is another supported Coolify path, but this package has
not been verified through that path or by clicking through the UI. [Coolify
Compose resource types](https://coolify.io/docs/applications/builds/docker-compose).


In the live Coolify test, the apex redirected from HTTP to HTTPS, and the apex,
event, and nested team addresses returned 200 with trusted Let's Encrypt TLS
1.3 certificates. The event and published team site survived a Coolify stop/start
and a separate same-image restart; the database still had 44 migrations. The
app reported v0.8.1 at commit `98f0f52`. Synthetic accounts and a fake mail
key kept this test free of outgoing email. The temporary 2 vCPU / 4 GB
DigitalOcean server existed for about 12 minutes, implying $0.0071 in prorated
compute at the API hourly rate; actual billing was not confirmed. It was deleted; its test SSH key and tag were also deleted and fresh API
checks confirmed absence.


The full platform uses the same hand-drawn landing film, ink theme, local
Caveat/Kalam fonts and per-event crayon colours as the hosted service. At
`https://<your-domain>/get-started`, “Pick your AI” leads to the instance's own
`https://<your-domain>/mcp`, role prompts and the FAQ; skill folders and plugin
ZIPs are under “Other ways to install” and also name your instance. Use those
instance downloads rather than the hosted GitHub skills when self-hosting.
Organisers share a join link and a private judge link; participants form teams,
publish a team site and complete an entry. No account keys are handed out as
part of that onboarding.

TLS passes through Traefik to the app's Caddy. PROXY protocol preserves visitor
addresses for rate limits and analytics. Postgres has no host port. The named
`db` and `data` volumes hold the database, team site files, and certificates.
Back up both before changing `SIMPLE_HACK_IMAGE` and restarting the Service.
The release step applies migrations before the app starts; a failed migration
prevents the new app from starting. A real v0.8.0-to-v0.8.1 upgrade was tested
separately with the standalone package; a version change through Coolify has
not yet been tested.


## Building and testing the Simple Hack package

The `hack-v0.8.20` release builds a matching base image and platform image from
one checkout and provides a ZIP or tar.gz installation package. It leaves the
older Simple Host small-box release pins intact. For a local build after that
base image exists, use the same release checkout:

```sh
docker build --build-arg SIMPLE_HOST_IMAGE=ghcr.io/vineetu/simple-host:hack-0.8.20 -f deploy/hack/standalone/Dockerfile -t simple-hack:candidate .
```

For an unpublished build, replace `SIMPLE_HOST_IMAGE` with a locally built
image containing its static binary at `/usr/local/bin/simple-host`. The schema
is copied from this checkout. Release publication must build both artifacts
from the same commit and publish the standalone image for each supported CPU.

```sh
bash deploy/hack/standalone/tests/install_test.sh
```

```sh
TEST_IMAGE=simple-hack:candidate bash deploy/hack/standalone/tests/smoke.sh
```

The smoke test uses disposable containers, loopback ports 18474–18476, a local
CA and admin-created fixture accounts. It checks fresh installation, a real
event and published team site, restart and upgrade persistence. It does not
claim production ACME issuance, browser email delivery or a cloud deployment.
Set `TEST_COOLIFY_PROXY=1` to also exercise the raw Coolify routing through a
real local Traefik container (Python's PyYAML is required). That test retains
the same event and site while replacing the direct port mapping with TLS
passthrough, checks hostname certificate refusal, visitor IP handling and
API-key log filtering, then removes its containers, network and volumes.

To test a real previous-to-current release upgrade locally, set
`TEST_PREVIOUS_IMAGE` to the previous published image and `TEST_IMAGE` to the
current image when running `tests/smoke.sh`. Set `TEST_PREVIOUS_PACKAGE` to
the previous download’s `deploy/hack/standalone` directory to use its installer
for the initial installation. `TEST_HACK_PRESENTATION=1` also
checks the film, local fonts, shared navigation and instance-specific
connector, API and skills. The fixture verifies the entry, team credential,
event settings, deployed files and local TLS CA after upgrading.

Visitor-save notifications use the same signed webhook and MCP Events engine as hosted Hack.
The package already provisions `PASSCODE_ENC_KEY`; keep it across upgrades. Without it,
new subscriptions are unavailable. Receivers must use publicly reachable HTTPS addresses;
private company-network callbacks are not supported. Apply `zj-hack-notifications.sql`
through `simple-host migrate` before deploying this version.
