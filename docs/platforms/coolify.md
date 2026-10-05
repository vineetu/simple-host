# Run the small box on Coolify

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

## Before you start

- A server with Coolify installed, its proxy on ports 80 and 443.
- A domain, say `hack.example.com`, with two `A` records to the server's address:
  `hack.example.com` and `sites.hack.example.com`.

## Steps

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

- **Upgrade:** set `SIMPLE_HOST_VERSION` (default 0.7.9) to the new release and
  redeploy. `release` applies that release's migrations first. If they fail, the
  app does not start. Coolify has already stopped the old containers by then, so
  the site is down until you fix it and redeploy. Your data stays in its volumes.
- **Settings:** the compose file passes the common ones (`MAX_ARCHIVE_MB`,
  `KEEP_VERSIONS`, `MAX_SITES_PER_ACCOUNT`, email and Google sign-in). Any
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
