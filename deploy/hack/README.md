# simple-hack.app instance

Repo files for the `simple-hack` program on this box, listening on
`127.0.0.1:8091`. It accepts `EVENTS=hosted` or unset. Nothing here is applied by itself:
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

1. Build both candidates from one commit using the line in [CLAUDE.md](../../CLAUDE.md).
   `sudo bash deploy/hack/setup-instance.sh` — read the dry run
2. `sudo env SIMPLE_HOST=/tmp/simple-hack.new bash deploy/hack/setup-instance.sh --apply` — user, dirs, database
   `simplehack`, `/etc/simple-hack.env`, the unit, logrotate, site-certs conf
   and units. The legacy `SIMPLE_HOST` script variable selects the Hack candidate;
   setup runs its `migrate -init -record-product`. Enables the site-certs `.path` and `.timer`. Does **not** start
   `simple-hack.service` and does **not** touch nginx or DNS.
3. Install the site-certs issuer from this repo (it gained `REQUESTS_OWNER`), after a backup:
   `sudo cp -p /usr/local/sbin/simple-host-site-certs /usr/local/sbin/simple-host-site-certs.bak-$(date +%Y%m%d-%H%M%S) && sudo install -m 755 deploy/site-certs/issue.sh /usr/local/sbin/simple-host-site-certs`
4. Issue the platform wildcard `simple-hack.app` + `*.simple-hack.app` (certbot DNS-01, the
   Vercel hooks, lineage `simple-hack.app`).
5. For an upgrade, run the Hack candidate's status and migrations as `simplehack`
   with its environment before installing:
   `sudo systemd-run --wait --pipe --collect -p User=simplehack -p EnvironmentFile=/etc/simple-hack.env /tmp/simple-hack.new migrate -status`
   then `sudo systemd-run --wait --pipe --collect -p User=simplehack -p EnvironmentFile=/etc/simple-hack.env /tmp/simple-hack.new migrate`.
   Run `/tmp/simple-host.new` against Host's database with Host's unit user and
   environment. Back up both installed programs with one timestamp, install
   `/tmp/simple-host.new` as `/usr/local/bin/simple-host` and `/tmp/simple-hack.new`
   as `/usr/local/bin/simple-hack`, then restart both services as in CLAUDE.md.
   Verify both `version` commands print the same release and commit. Host cannot
   migrate Hack's database.
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

## Rollback

Restore both programs from the same `.bak-<timestamp>` pair and restart both
services, staying at or above the coordination file's oldest-safe commit.
To undo the initial split, restore the previous Hack unit (whose ExecStart
used `/usr/local/bin/simple-host`) and the saved combined program, reload
systemd, and restart both services. This does not undo SQL or data moves;
restore the matching database, site directory, configuration/keys and program
together when data recovery is required. See the
[deployment profiles](../../docs/operations/deployment-profiles.md).
