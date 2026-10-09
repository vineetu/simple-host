# Run the small box on Fly.io

![Simple Host on Fly.io: your DNS points both names at the app; Fly's edge ends HTTPS and forwards to one machine running Caddy and the Simple Host server with a 1 GB volume; a separate Fly Postgres app on Fly's private network](https://simple-host.app/diagrams/fly.svg)

The small box (the installer's Postgres + server + Caddy) runs on Fly.io as two
apps: one for Simple Host, one for its Postgres. Tested end to end on
2026-09-28 with v0.7.4: deploy, first admin, admin sign-in, publish, update,
rollback, saved data, download my data, machine restart, redeploy with
migrations, and `/internal/*` hidden. Then everything was deleted.

The files are in [`deploy/platforms/fly/`](../../deploy/platforms/fly/):
`fly.toml`, a `Dockerfile` that copies the released server into the Caddy
image, a `Caddyfile` (the small box's, without its TLS parts), and `start.sh`.
Nothing is compiled; no change to the server is needed.

## What it looks like

| Piece | On a VPS (installer) | On Fly |
|---|---|---|
| HTTPS | Caddy, on-demand certificates | Fly's edge: `fly certs add` for both names |
| Sites served from disk, `/internal/*` hidden, access log | Caddy container | Caddy in the same machine as the server (port 8080) |
| Server | `app` container | same machine, as a normal user, on 127.0.0.1:8090 |
| Database | `db` container | a separate Fly Postgres app, attached as `DB_DSN` |
| Sites on disk | Docker volume | 1 GB Fly volume at `/data` |
| Schema and migrations | the installer runs `simple-host migrate` | `release_command` loads `schema.sql` into an empty database, then runs `simple-host migrate`, before every deploy |

## Minimum size and cost

Everything at the smallest size worked: the Simple Host machine used about
75 MB of its 256 MB (server ~25 MB, Caddy ~50 MB).

| Item | Size | Per month |
|---|---|---|
| Simple Host machine | shared-cpu-1x, 256 MB | about $2 |
| Postgres machine | shared-cpu-1x, 256 MB | about $2 |
| Two volumes | 1 GB each, $0.15/GB | $0.30 |
| Two certificates | $0.10 each | $0.20 |
| Shared IPv4 and IPv6 | | free |
| **Total** | | **about $4.50**, plus egress ($0.02/GB in North America and Europe) |

Why this Postgres: Fly's Managed Postgres starts at $38/month (Basic plan),
eight times the rest of the box put together. The unmanaged `fly postgres`
cluster is a normal Fly app on a volume, the same thing the VPS small box runs
in a container. Fly does not support it (no managed failover or help), but it
does take daily volume snapshots, and a small box did not have more than that
on a VPS either. Any other Postgres works too: set `DB_DSN` to its URL.

**A Fly account needs a card.** On a trial account without one, Fly stops
every machine after 5 minutes ("Trial machine stopping"). The box then goes
dark, and the next deploy fails with `database not reachable: EOF` because
Postgres is stopped. Add a card before you start.

## Commands

You need `flyctl` (`fly auth login`), a domain, and the release you want
(this guide: 0.7.11, set in `fly.toml` as `SIMPLE_HOST_VERSION`). Below, the app
is `my-sh`, the region `sjc`, and the address `hack.example.com`, with
participant sites on `sites.hack.example.com`.

1. Edit `deploy/platforms/fly/fly.toml`: set `app`, `primary_region`, and the
   three hostnames (`SITE_DOMAIN`, `CONTENT_HOST`, `PUBLIC_BASE_URL`).

2. Create Postgres, the app, and the volume, then attach the database.
   `attach` stores the connection string as the secret `DB_DSN`, which the
   server reads. It also prints the string, so do not paste that output
   anywhere.

   ```sh
   fly postgres create --name my-sh-db --org personal --region sjc --vm-size shared-cpu-1x --volume-size 1 --initial-cluster-size 1
   fly apps create my-sh --org personal
   fly postgres attach my-sh-db -a my-sh --variable-name DB_DSN
   fly volumes create simple_host_data --size 1 -r sjc -a my-sh -y
   ```

3. Make the admin key and store it. Save the file somewhere safe: it is the
   first admin's sign-in.

   ```sh
   umask 077; echo "sh_admin_$(head -c 24 /dev/urandom | od -An -tx1 | tr -d ' \n')" > admin.key; fly secrets set ADMIN_API_KEY="$(cat admin.key)" -a my-sh --stage
   ```

4. Deploy from the directory with the files. The first deploy loads the schema
   and applies every migration (`applied 28 migration(s)`) before the machine
   starts.

   ```sh
   cd deploy/platforms/fly && fly deploy --ha=false
   ```

   `--ha=false` keeps it at one machine: the volume holds the sites, so the
   app cannot run two.

5. Point both names at the app, then ask Fly for their certificates. A CNAME to
   `my-sh.fly.dev` for each name is enough (or the A and AAAA records that
   `fly certs add` prints). The certificates were issued within a minute.

   ```
   hack.example.com        CNAME  my-sh.fly.dev
   sites.hack.example.com  CNAME  my-sh.fly.dev
   ```

   ```sh
   fly certs add hack.example.com -a my-sh && fly certs add sites.hack.example.com -a my-sh
   ```

6. Sign in: open `https://hack.example.com/admin` and paste the admin key.
   From there, issue participant accounts ("Issue participant accounts") and
   hand out their keys, as on any small box.

## Everyday operations

- **Upgrade:** set the new `SIMPLE_HOST_VERSION` in `fly.toml` and
  `fly deploy --ha=false`. The release command runs the new release's
  migrations first; if they fail, the deploy stops and the old version keeps
  running.
- **Restart:** `fly machine restart <id> -a my-sh` (`fly machines list -a my-sh`
  shows the id). Sites, saved data and accounts all survive.
- **Settings:** anything in [configuration.md](../configuration.md) goes in
  `[env]` in `fly.toml` (limits, times) or `fly secrets set` (keys). Email
  sign-in codes need `RESEND_API_KEY` and `MAIL_FROM` as secrets; without them
  participants use the keys the admin issues, which is how a small box runs by
  default.
- **Admin key lost:** `fly ssh console -a my-sh -C "printenv ADMIN_API_KEY"`.
- **Backups:** Fly snapshots each volume daily and keeps 5 days
  (`fly volumes snapshots list <vol-id>`); snapshots over 10 GB cost
  $0.08/GB. "Download my data" and the admin export work as on any small box.
- **Delete everything:** `fly apps destroy my-sh -y && fly apps destroy my-sh-db -y`,
  then remove the two DNS records. Destroying an app takes its machines,
  volume, IP addresses and certificates with it.

## Gotchas

- **Trial accounts stop machines after 5 minutes.** See above.
- **The release image cannot run alone on Fly.** It is the server only. Sites
  are served from disk by Caddy, and Caddy also hides `/internal/*` and writes
  the access log that visitor analytics reads. So the Dockerfile puts both in
  one machine; pointing Fly straight at the server leaves every site a 404
  and exposes `/internal/*`.
- **A new database is empty.** `simple-host migrate` only adds to an existing
  schema. `start.sh release` loads `db/schema.sql` from the same release tag
  first, only when the `users` table is missing.
- **New Fly volumes belong to root.** `start.sh` hands `/data/sites` to the
  server's user on first boot.
- **Visitor addresses.** Every request reaches Caddy from Fly's proxy. The
  Caddyfile trusts the private range and reads `Fly-Client-IP`, then sends the
  server that address alone in `X-Forwarded-For`. Without this, rate limits
  would put every visitor in one bucket, and analytics would count the proxy.
- **Health checks and the log.** Fly checks `/healthz` about twice a second.
  Logged, that is roughly 250 MB a day on a 1 GB volume, so the Caddyfile does
  not log `/healthz`.
- **`sites.<domain>/healthz` is a 404.** Only the main address answers it,
  as on a VPS.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [Your home page](../your-home-page.md).
