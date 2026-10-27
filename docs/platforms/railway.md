# Run the small box on Railway

Built 2026-10-27, verified locally (see "What was tested" below): no Railway account was
available to this agent, so the exact container Railway would run — the same image, the same
environment variables, a volume at the same path — was built and run against a local Postgres
instead of a live Railway deploy. [SIM-212](https://linear.app/simple-hack/issue/SIM-212) ("Grok
bot: create the Railway account, publish the Simple Host template, and test one deploy") covers
the real end-to-end pass once an account exists; update this page's status line when that lands.

![Simple Host on Railway: your DNS points both names at the service, Railway's own edge ends HTTPS and forwards to one service running Caddy and the Simple Host server with a volume, Railway Postgres in the same project, your GitHub repository holds the Dockerfile the template builds from](https://simple-host.app/diagrams/railway.svg)

The small box (the installer's Postgres + server + Caddy) runs on
[Railway](https://railway.com) as one service with a volume, plus a Railway Postgres
database, both in one project. Railway's edge ends HTTPS for both names itself (no
separate CDN in front, unlike Render's Cloudflare); inside the service Caddy and the
server share one container, same as the Fly, Render and older Coolify recipes.

The files are in [`deploy/platforms/railway/`](../../deploy/platforms/railway/):
a `Dockerfile` that copies the released server into the Caddy image, a `Caddyfile`
(the small box's, without its TLS parts), and `start.sh`. Nothing is compiled; no
change to the server is needed.

## Unlike Render or Coolify: there is no file to import

Render reads `render.yaml`; Coolify's service template is a `compose.yaml` you paste in.
Railway has no equivalent committed file — a Railway template is a project configured
once in the dashboard (or `railway templates create`), then **published**, which is what
produces the `railway.com/deploy/<slug>` link a "Deploy on Railway" button points at.
Until that publishing step has happened once (tracked in the Linear ticket above), there
is no slug and no working one-click link, so none is shown here: a button that looks
clickable but does not deploy anything is worse than no button. Build the project by hand
with the commands below; `deploy/platforms/railway/README.md` is the reproducible list of
every setting used, so the result is exactly what the button will deploy once published,
and the project can be rebuilt or audited without Railway's UI being the only record of it.

## What it looks like

| Piece | On a VPS (installer) | On Railway |
|---|---|---|
| HTTPS | Caddy, on-demand certificates | Railway's edge: a certificate for each custom domain |
| Sites served from disk, `/internal/*` hidden, access log | Caddy container | Caddy in the same service as the server, on the service's `PORT` |
| Server | `app` container | same service, as a normal user, on 127.0.0.1:8090 |
| Database | `db` container | Railway Postgres, referenced as `${{Postgres.DATABASE_URL}}` |
| Sites on disk | Docker volume | a Railway volume mounted at `/data` |
| Schema and migrations | the installer runs `simple-host migrate` | the Pre-Deploy Command loads `schema.sql` into an empty database, then runs `simple-host migrate`, before every deploy |
| Generated secrets | you run `openssl rand -hex 32` | `${{secret(64)}}` in the template, except `PASSCODE_ENC_KEY` (see Gotchas) |

## Minimum size and cost

Rates from railway.com/pricing on 2026-10-27, billed per second (stopped services cost
nothing). This is an estimate, not a measured bill: no Railway account was available to run
it for a full month. The server and Caddy used about 75 MB on Fly's equivalent test, so a
small always-on service like this one stays well under Hobby's 48 vCPU/48 GB per-service
ceiling; the question is whether it fits inside the plan's included credit.

| Item | Plan | Included | Per month if you stay on it |
|---|---|---|---|
| Workspace | Hobby | $5/month, includes $5 of usage | $5 |
| Compute overage | — | — | ≈$10/GB-month memory, ≈$20/vCPU-month, both metered to the second past the included $5 |
| Volume (sites + access log) | Hobby | up to 5 GB | ≈$0.15/GB-month past the included $5 |
| Postgres | same project | shares the $5 credit | counted the same way, as its own service |
| Custom domains | Hobby: 2 per service | free | free |

A card is required even for the $5 trial credit (Railway has required one since 2023).
Running one small instance (app + Postgres) continuously for 30 days typically draws more
than $5 of the metered compute and storage above, so budget roughly **$5 (plan) plus a few
dollars of overage** — in the same range as Fly.io (about $4.50/month) or the DigitalOcean
droplet (about $6/month), and cheaper than Render (about $13.55/month, which has separate
paid minimums for its disk and Postgres tiers). Pro ($20/month, included credit $20) removes
the 5 GB volume cap (up to 1 TB) and raises the custom-domain limit to 20 per service.

## Commands

You need a Railway account with a card, a GitHub repository with these files (a fork of
Simple Host, or your own repository with `deploy/platforms/railway/` copied in), and a
domain. Below, the address is `hack.example.com`, with participant sites on
`sites.hack.example.com`. `deploy/platforms/railway/README.md` has the same steps as a
reference table; this is the walkthrough.

1. **New Project** in the Railway dashboard, then **Deploy from GitHub repo**, your fork.
   In the new service's Settings, set **Root Directory** to `deploy/platforms/railway`.
   Railway finds the `Dockerfile` there on its own; no build command is needed.

2. **+ New → Database → PostgreSQL** in the same project.

3. In the app service's **Variables** tab, add:

   ```
   DB_DSN=${{Postgres.DATABASE_URL}}?sslmode=disable
   ADMIN_API_KEY=${{secret(64)}}
   SITE_DOMAIN=hack.example.com
   ```

   Leave `CONTENT_HOST` unset; `start.sh` defaults it to `sites.$SITE_DOMAIN`.

4. In **Settings → Deploy**: Healthcheck Path `/healthz`, Pre-Deploy Command
   `/usr/local/bin/start.sh release`.

5. In **Settings → Volumes**, attach a volume at mount path `/data`.

6. **Deploy.** The first build copies the release into the image. The Pre-Deploy Command
   runs first, in its own container (`empty database: loading schema.sql`, then
   `simple-host migrate`); only once it succeeds does the new deployment go live.

7. In **Settings → Networking**, add both custom domains to the app service:

   ```
   hack.example.com
   sites.hack.example.com
   ```

   Railway shows a CNAME and a TXT record for each. Add both at your DNS provider:

   ```
   hack.example.com        CNAME  <value Railway shows>
   sites.hack.example.com  CNAME  <value Railway shows>
   ```

   Railway verifies each name and issues its certificate once the CNAME resolves; in
   earlier tests of this same image on other platforms that took under a minute.

8. Sign in: the admin key is the value you generated for `ADMIN_API_KEY` (Variables tab).
   Open `https://hack.example.com/admin` and paste it. From there, issue participant
   accounts and hand out their keys, as on any small box.

## Everyday operations

- **Upgrade:** change `SIMPLE_HOST_VERSION` (the Dockerfile's `ARG`, default 0.7.21) in
  your fork and push. The Pre-Deploy Command runs the new release's migrations first; if
  they fail, the deploy stops and the old version keeps running. A redeploy with nothing
  new logs `database is up to date; nothing to apply`.
- **Restart:** the service's **Restart** action in the dashboard. Sites, saved data and
  accounts all survive.
- **Settings:** anything in [configuration.md](../configuration.md) goes in the app
  service's Variables tab, exactly as named there.
- **Backups:** Railway's volume backups are native and incremental (Settings → Backups):
  daily (kept 6 days), weekly (kept 27 days), monthly (kept 89 days). Postgres gets the
  same volume backups, plus its own point-in-time tools. "Download my data" and the admin
  export work as on any small box.
- **Delete everything:** delete the project (this removes the service, the volume and the
  database together), then remove the two DNS records.

## Gotchas

- **Check `DB_DSN`'s resolved value before deploying.** The Variables tab previews what
  `${{Postgres.DATABASE_URL}}?sslmode=disable` resolves to. If Railway's `DATABASE_URL`
  ever already carries its own query string, appending another `?` produces an invalid
  DSN (the second `?` is just more of the first query string's value, so `sslmode` is
  never actually set) rather than a silently insecure connection: lib/pq then falls back
  to its own default (`require`), which fails outright against a Postgres with no TLS
  configured, rather than connecting unencrypted. If the preview shows a `DATABASE_URL`
  that already has a `?`, use `&sslmode=disable` instead.
- **The visitor-IP trust reasoning is unverified against a live deploy.** The Caddyfile
  trusts `X-Forwarded-For`'s first entry from Railway's private-range hop, on the
  understanding (from Railway's own docs and community reports, not a tested guarantee)
  that Railway's edge sets that header to the real client address rather than appending to
  whatever a client sent. If Railway's edge instead appends to a client-supplied value the
  way some other platforms do, a visitor could spoof the address `trusted_proxies_strict`
  picks, affecting rate limits and analytics but not authentication or saved data. Confirm
  this with a real request (`curl -H "X-Forwarded-For: 1.2.3.4" https://your-domain/...`
  and check the access log) once a Railway deploy exists, before relying on it.
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

## What was tested

Built and run locally on 2026-10-27 with the exact `deploy/platforms/railway` Dockerfile,
`PORT` set the way Railway injects it, and a volume at `/data`, against a local Postgres
container (no Railway account was available to this agent): `start.sh release` (the
Pre-Deploy Command) loaded `schema.sql` into an empty database and applied all 61
migrations cleanly; the admin key signed in at `/v1/me`; `POST /v1/admin/users` issued a
participant account and key; that key published a site (`POST /v1/sites/{name}` with the
archive as the raw body, `Content-Type: application/zip`), which answered 200 on the
`sites.`-prefixed host and correctly 404 on the dashboard host (content only ever serves
on `sites.*`, matching production); a KV resource and key written through
`/v1/sites/{name}/storage/kv/...` read back correctly; and a container restart (`docker
restart`) kept the site, the KV value and the admin account, with `/healthz` answering 200
before and after. This exercises the same image, environment variables and volume layout
Railway would run; it does not exercise Railway's own edge, domain verification, Pre-Deploy
Command isolation, or billing. The Linear ticket above tracks the real deploy. One bug this
test caught before it could ship: `start.sh` was first committed without its executable
bit, which would have made the container fail to start entirely (`permission denied`) the
moment it shipped on Railway's `CMD`-based image, exactly as it does locally without `chmod
755`.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).
