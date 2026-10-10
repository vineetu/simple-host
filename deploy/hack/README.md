# simple-hack.app instance

Repo files for a second Simple Host binary on this box, listening on
`127.0.0.1:8091` with `EVENTS=hosted`. Nothing here is applied by itself:
the integrator reviews and runs the scripts.

| File | What it is |
|---|---|
| `simple-hack.service` | systemd unit (user `simplehack`, env `/etc/simple-hack.env`) |
| `simple-hack.env.example` | Documented environment; first `--apply` writes `/etc/simple-hack.env` |
| `setup-instance.sh` | One-time setup (user, dirs, Postgres, env, units). Dry run by default |
| `setup-instance_test.sh` | Sandbox test for the setup script |
| `logrotate-simple-hack.conf` | Rotate `/var/log/simple-hack/analytics.log` (30 days) |

Related, not in this directory:

- `deploy/prod/nginx-site-base-domain.sh` with `APEX_MODE=app` and
  `NGINX_NAME=simple-hack` — nginx for `simple-hack.app`
- `deploy/site-certs/simple-host-site-certs-hack.*` — per-event certificates

The weekly demo reset is in `deploy/prod/hack-demo-reset.{sh,service,timer}`; [installation and stage details](../../docs/operations/simple-hack.md#weekly-demo-reset). Check its schedule and last run with `systemctl list-timers hack-demo-reset.timer` and `journalctl -u hack-demo-reset.service -n 50 --no-pager`.

## Order

1. `sudo bash deploy/hack/setup-instance.sh` — read the dry run
2. `sudo bash deploy/hack/setup-instance.sh --apply` — user, dirs, database
   `simplehack`, `/etc/simple-hack.env`, the unit, logrotate, site-certs conf
   and units. Enables the site-certs `.path` and `.timer`. Does **not** start
   `simple-hack.service` and does **not** touch nginx or DNS.
3. Install the site-certs issuer from this repo (it gained `REQUESTS_OWNER`), after a backup:
   `sudo cp -p /usr/local/sbin/simple-host-site-certs /usr/local/sbin/simple-host-site-certs.bak-$(date +%Y%m%d-%H%M%S) && sudo install -m 755 deploy/site-certs/issue.sh /usr/local/sbin/simple-host-site-certs`
4. Issue the platform wildcard `simple-hack.app` + `*.simple-hack.app` (certbot DNS-01, the
   Vercel hooks, lineage `simple-hack.app`).
5. Deploy this build as `/usr/local/bin/simple-host` (the same binary as simple-host.app: back up,
   install, restart simple-host.service and verify it first; the old binary has no hosted mode),
   then record and apply the migrations against the hack database:
   `sudo bash -c 'DB_DSN=$(grep "^DB_DSN=" /etc/simple-hack.env | cut -d= -f2-) /usr/local/bin/simple-host migrate'`
   (it must list `hack1-events.sql`; the server refuses to start in hosted mode without its index).
6. Start the app: `sudo systemctl enable --now simple-hack.service`, and check
   `curl -s -H 'Host: simple-hack.app' http://127.0.0.1:8091/healthz`.
7. nginx, in one reload: back up and unlink the old hand-written
   `/etc/nginx/sites-enabled/simple-hack.app` (it proxied `/` to simple-host.app's
   `/hackathons`), then install the new file:
   `sudo cp -p /etc/nginx/sites-available/simple-hack.app /var/backups/simple-hack.app.nginx-$(date +%Y%m%d-%H%M%S) && sudo rm /etc/nginx/sites-enabled/simple-hack.app && sudo env NGINX_NAME=simple-hack CERT_VAR=sh_hack_cert_person SITE_BASE_DOMAIN=simple-hack.app APP_DOMAIN=simple-hack.app APP_UPSTREAM=127.0.0.1:8091 SITE_BASE_CERTS=/etc/nginx/simple-host-site-certs-hack APEX_MODE=app ANALYTICS_LOG=/var/log/simple-hack/analytics.log CLIENT_MAX_BODY=64m bash deploy/prod/nginx-site-base-domain.sh --apply`
   (if that fails, `sudo ln -s /etc/nginx/sites-available/simple-hack.app /etc/nginx/sites-enabled/ && sudo nginx -t && sudo systemctl reload nginx` puts the old page back).
8. DNS: change the zone's `*` record from the Vercel ALIAS to an A record for this box
   (explicit records for self-hosted event claims stay and win over it).
9. simple-host.app: add `EVENT_NAME_PEER=http://127.0.0.1:8091` to `/etc/simple-host.env` and
   restart it, so its self-host claims and hosted events share one name list.
   simple-host.app's `EVENT_DOMAINS` must include `simple-hack.app`: it answers the peer only for
   its own event zones, and would otherwise call every name free.
