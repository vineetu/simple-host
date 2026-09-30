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

## Order

1. `sudo bash deploy/hack/setup-instance.sh` — read the dry run
2. `sudo bash deploy/hack/setup-instance.sh --apply` — user, dirs, database
   `simplehack`, `/etc/simple-hack.env`, the unit, logrotate, site-certs conf
   and units. Enables the site-certs `.path` and `.timer`. Does **not** start
   `simple-hack.service` and does **not** touch nginx or DNS.
3. Integrator: issue the platform wildcard for `simple-hack.app`, then
   `NGINX_NAME=simple-hack SITE_BASE_DOMAIN=simple-hack.app APP_DOMAIN=simple-hack.app APP_UPSTREAM=127.0.0.1:8091 SITE_BASE_CERTS=/etc/nginx/simple-host-site-certs-hack APEX_MODE=app ANALYTICS_LOG=/var/log/simple-hack/analytics.log CLIENT_MAX_BODY=64m sudo -E bash deploy/prod/nginx-site-base-domain.sh --apply`
4. Integrator: DNS for `simple-hack.app` and `*.simple-hack.app`
5. Integrator: `sudo systemctl enable --now simple-hack.service`
