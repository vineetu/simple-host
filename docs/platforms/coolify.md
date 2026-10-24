# Run the small box on Coolify

Verified 2026-10-15 on two real DigitalOcean droplets (sfo2, `s-1vcpu-2gb`, `<ip>.nip.io` names, Let's Encrypt certificates) with the published v0.7.19 image: a fresh install; a v0.7.17 box with a site, a public KV value and a records SQLite resource upgraded straight to v0.7.19 by re-running the installer with no flags; and a v0.7.17 box taken through v0.7.18 to v0.7.19 the same way. Every box kept its addresses, the site, the KV value and the presets, applied `ze-spam-checked-posts.sql` and `zf-signup-detail.sql` once, refused the comments preset with `spam_check_unavailable` until Turnstile keys were added to `.env` (then accepted it, and a re-run kept the keys), and named itself, never simple-host.app, in robots.txt, Get started, llms.txt and the connector's instructions. Both droplets were deleted.

Verified 2026-10-10 with the published v0.7.17 image in disposable native arm64 containers: fresh install and an actual v0.7.16 upgrade through the installer's compose file (the image's `migrate`, as install.sh runs it) preserve the site list, KV values and resource policies across the swap; the storage-presets migration applies once and nothing is left pending; existing resources show their preset (an open write-anyone resource as `legacy`), and a preset with per-table rules, the row edit route and an R1 refusal answer as designed.

Verified 2026-10-10 with the published v0.7.16 image in disposable native arm64 containers: fresh install and an actual v0.7.15 upgrade through the installer's compose file (the image's `migrate`, as install.sh runs it) preserve the site list, KV values and resource policies across the swap; the new migration applies once and nothing is left pending, and the named-viewers and visitor-lookup routes answer. All four binary checksums pass, and both CPU images are published (arm64 run). This check used localhost HTTP against the app, without Caddy; earlier evidence below remains dated to its tested versions.

Verified 2026-10-10 with the published v0.7.15 image in disposable native arm64 containers: fresh install and an actual v0.7.14 upgrade through the installer's compose file preserve the site list, KV values and resource policies across the swap, and the click-to-play landing and its film files (206 ranges, video/mp4, video/webm, image/jpeg) serve through Caddy. Both Linux binary checksums pass, and both CPU images are published (arm64 run, amd64 binary inspected). This check used localhost HTTP against the API; earlier cloud and TLS evidence below remains dated to its tested versions.

Verified 2026-10-09 with the published v0.7.12 image in disposable native arm64 containers: fresh install and an actual v0.7.11 upgrade through Caddy preserve site files, KV, SQLite, file objects and existing full policies across restart. Add/own schemas, a 150-photo gallery, NFC uploads and the separate file pool pass. Generate and status return 404, and the AI API tag and model settings are absent. All four published binary checksums and anonymous pulls of both CPU images pass. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to its tested versions.


Verified 2026-10-09 with the published v0.7.11 image in disposable native arm64 containers: fresh install and an actual v0.7.10 upgrade through Caddy preserve site files, KV, SQLite, file objects and existing full policies across restart. Add/own schemas, a 150-photo gallery, NFC uploads and the separate file pool pass. Removed endpoints return 404; setup generators and settings remain available. All four binary checksums and anonymous pulls of both CPU images pass. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to its tested versions.


Verified 2026-10-06 with the published v0.7.10 image in disposable local containers: fresh installation and an actual v0.7.9 upgrade through Caddy preserved site files, KV, SQLite, file objects and full-mode policies; add/own order schemas, 150 public photos, NFC upload names, the separate file pool and restart persistence passed. This check used native arm64 and localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

Verified 2026-10-05 with the published v0.7.9 image in disposable local containers: a fresh schema and an actual v0.7.4 upgrade through Caddy preserved site content and passed home selection/clearing, showcase fallback, bio, pins/order, live feed and restart persistence. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

![Simple Host on Coolify. Your DNS points both names at your server. Coolify's proxy ends HTTPS and forwards to one Docker Compose resource with three containers, the app (Caddy and the Simple Host server, with a data volume), Postgres (with a volume) and a release step that loads the schema and migrations before the app starts. Your Git repository holds the files.](https://simple-host.app/diagrams/coolify.svg)

The small box (the installer's Postgres + server + Caddy) runs on [Coolify](https://coolify.io)
as one Docker Compose resource. Coolify's proxy ends HTTPS for the two names;
inside the resource the app container runs Caddy and the server, Postgres is its
own container, and a one-shot `release` container loads the schema and applies
every migration before the app starts.

Tested 2026-10-01 on a DigitalOcean droplet (2 vCPU, 4 GB) with Coolify 4.3.23.
The resource was created through Coolify's API with the same settings as the steps
below, and the screens were not clicked through, so the button names follow
Coolify's source (v4.4.0). Coolify built the images, ran `release` (schema and 28
migrations), started the app healthy, and got Let's Encrypt certificates for both
names. A site published and loaded on `sites.<domain>`, `/internal/*` returned 404,
the visitor's address in the access log was the real one, no API key was in the
log, and a redeploy kept the site and had nothing to migrate.

The files are in [`deploy/platforms/coolify/`](../../deploy/platforms/coolify/):
`compose.yaml`, a `Dockerfile` that copies the released server into the Caddy
image, a `Caddyfile`, and `start.sh`. Nothing is compiled.

## Without a Git build: the service template

[`deploy/platforms/coolify/service-template.yaml`](../../deploy/platforms/coolify/service-template.yaml)
uses only published images (the server, `caddy` and `postgres`), so nothing is
built on the server. In Coolify, create a **Docker Compose Empty** service and
paste it. Coolify generates both addresses (`web` for the dashboard and API,
`sites` for published sites), the database password and the admin key
(`SERVICE_PASSWORD_64_ADMIN`), plus the persistent sealing key
(`SERVICE_REALBASE64_32_PASSCODE`). Keep that key when editing or upgrading the
resource; changing it makes stored passcodes and notification secrets unreadable. A page saves data only on a site's own
address: connect one in the dashboard (the site's **Domain**) and add that name
to `web`'s domains.

Tested 2026-10-19 on Coolify 4.4.6 (a DigitalOcean `s-2vcpu-4gb` droplet, sslip.io
names): the schema and 57 migrations applied, both addresses got Let's Encrypt
certificates, the admin sign-in page loaded, an account was issued, a site was
published and served on the sites address, a visitor's form entry was saved on
the site's own address, and a restart had nothing to migrate.

## Before you start

- A server with Coolify installed, its proxy on ports 80 and 443.
- A domain, say `hack.example.com`, with two `A` records to the server's address:
  `hack.example.com` and `sites.hack.example.com`.

## Older Git-build recipe

The service template above supersedes this recipe for new installations.
Existing Git-build resources remain supported; keep their volumes and settings.
This recipe retains its `KEEP_VERSIONS=3` default, while the service template
defaults to one.


1. In Coolify, go to **Projects > your project > New Resource > Public Repository**
   (or Private). Repository `https://github.com/vineetu/simple-host`, branch
   `main`. Build pack **Docker Compose**, Base Directory
   `/deploy/platforms/coolify`, Docker Compose Location `/compose.yaml`.

2. Under **Environment Variables**, add `SITE_DOMAIN` = `hack.example.com`.
   `ADMIN_API_KEY` and the database password are generated for you.

3. Under the app service's **Domains**, enter both names:

   ```
   https://hack.example.com,https://sites.hack.example.com
   ```

4. **Deploy.** The first build copies the release into the image and takes
   about a minute. The `release` step runs first and exits. Then the app starts.

5. Open `https://hack.example.com/admin` and paste the value of
   `SERVICE_PASSWORD_ADMIN` (Environment Variables). From there, issue
   participant accounts and hand out their keys, as on any small box.

## Everyday operations

- **Upgrade:** set `SIMPLE_HOST_VERSION` (default 0.7.19) to the new release and
  redeploy. `release` applies that release's migrations first. If they fail, the
  app does not start. Coolify has already stopped the old containers by then, so
  the site is down until you fix it and redeploy. Your data stays in its volumes.
- **Settings:** the compose file passes the common ones (`MAX_ARCHIVE_MB`,
  `KEEP_VERSIONS`, `MAX_SITES_PER_ACCOUNT`, email, Google and OIDC sign-in,
  notification limits, photo limits, and all four `MCP_APP_*` flags). Any
  setting from [configuration.md](../configuration.md) goes under the app's
  `environment:` in your own fork's copy of `compose.yaml`, as a line like the others. Point the resource at your fork instead of `vineetu/simple-host`.
- **Backups:** two volumes hold everything, `db` (Postgres) and `data` (sites and
  the access log). Back up both, with Coolify's server backup or your own.
  "Download my data" and the admin export work as on any small box.
- **Delete everything:** delete the resource and tick the volumes, then remove
  the two DNS records.

## Gotchas

- **Both names go on the app service.** The content routes only answer on
  `sites.<domain>`, and Coolify sends a request only for the names listed.
- **Behind Cloudflare,** set `CLIENT_IP_HEADER` to `CF-Connecting-IP`. Without it
  the visitor's address in the access log and the rate limits is Cloudflare's.
  The default, `X-Forwarded-For`, is what Coolify's proxy sets when visitors
  reach it directly. Let only Cloudflare reach ports 80 and 443 (the server's firewall),
  or anyone can send that header themselves.
- **One instance.** The sites live on one volume, so do not scale the app past
  one container.
- **The build reaches GitHub** for the release's `schema.sql` (the image is built
  on your server, not pulled).

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).


The schema snapshot in the service template includes `zh-site-notifications.sql`
and `zi-site-search-summary.sql`; its immutable commit and checksum move together.
Release image pins remain unchanged until release cutting. After upgrading to a
release containing summaries, run `simple-host site-summary-backfill` in the app
terminal after migration. It only updates sites without summaries.
The Apps flags default off; dashboard also needs My sites, and preview still
requires a client and site headers that permit framing.
