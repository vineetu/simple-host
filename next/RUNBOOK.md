# Simple Host (next): runbook

One Go binary, `cmd/simple-host-next`, behind nginx (hosted) or Caddy (self-hosted Compose).
It serves the platform (`SH_APP_DOMAIN`), every site address and the public pages itself.

## Settings (environment)

| Name | What it is | Default |
|---|---|---|
| `SH_ADDR` | Where to listen | `127.0.0.1:8095` |
| `DATABASE_URL` | Postgres; tables are made on an empty database at start | (required) |
| `SH_DATA_DIR` | Site versions, namespace SQLite files, file stores, exports, `certs/` | `/srv/simple-host-next` |
| `SH_APP_DOMAIN` | The service's own host; older accounts' `site.handle.<this>` and short names | `simple-host.app` |
| `SH_SITE_DOMAIN` | New accounts' `site--handle.<this>` | `simple-host.site` |
| `PASSCODE_ENC_KEY` | base64 of 32 bytes; the SAME key as the old service, so passcodes carry | (required) |
| `RESEND_API_KEY` | Resend key for sign-in codes, export links, idle-site emails | (none: no mail) |
| `MAIL_FROM` | Sender | `Simple Host <hello@simple-host.app>` |
| `SH_MAIL_URL` | Resend's address (a fake in tests) | `https://api.resend.com/emails` |
| `TURNSTILE_SITE_KEY`, `TURNSTILE_SECRET` | The platform's one spam-check key pair | (none: spam check off) |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | Google sign-in; the button is hidden when unset | (off) |
| `COMPANY_CLIENT_ID`, `COMPANY_CLIENT_SECRET`, `COMPANY_AUTH_URL`, `COMPANY_TOKEN_URL`, `COMPANY_USER_URL` | A company's own sign-in (self-hosted) | (off) |
| `ACME_EAB_GOOGLE`, `ACME_EAB_ZEROSSL` | `<key id>:<base64url HMAC>` for the 2nd and 3rd certificate issuer | (that issuer skipped) |
| `SH_VAPID_KEY` | base64url of a P-256 private key: browser push | (push off) |
| `AGENTCAT_PROJECT_ID`, `SH_AGENTCAT_URL` | AgentCat receives every tool call | (off) / AgentCat's public events URL |
| `SH_SELF_HOSTED` | `1`: first-run `/setup`, operator makes accounts | (hosted) |
| `SH_COUNTRY_FILE` | IP-to-country file, MaxMind `.mmdb` (the box's DB-IP `dbip-city-lite.mmdb` works), for the admin world map | (no countries) |

Fixed, not settings: the limits table (`core/limits`), Let's Encrypt → Google → ZeroSSL
directories, Turnstile and dns.google addresses, the Meta Muse client.
The operator is `people.operator` (carried by the copy tool; on a self-hosted install, `/setup`).

## Run it

```sh
cd next && go build -o simple-host-next ./cmd/simple-host-next
DATABASE_URL=postgres://… PASSCODE_ENC_KEY=… RESEND_API_KEY=… SH_COUNTRY_FILE=/srv/simple-host/geoip/dbip-city-lite.mmdb ./simple-host-next
curl -s -H 'Host: simple-host.app' http://127.0.0.1:8095/health     # {"ok":true}
```
Tests: `go test ./...` (one throwaway Postgres in docker on 127.0.0.1:54329, or `SH_TEST_POSTGRES`).
Self-hosted: `web/selfhost/compose.yaml` and `.env.example`; `docker compose up -d`, then open `https://DOMAIN/setup`.

## What the web server must do

- Terminate HTTPS and forward every host to `SH_ADDR`, keeping `Host`.
- Send `X-Forwarded-Proto: https` and `X-Real-IP: $remote_addr` (trusted only from loopback).
  The client address feeds rate limits, visitor counting and the country lookup; no country header is used.
- Port 80: `/.well-known/acme-challenge/` to the service, everything else redirected to HTTPS.
- `/mcp`: `proxy_buffering off` and a long read timeout (the event stream).
- Uploads: `client_max_body_size` at least 150m (sites are capped at 100 MB, sent as base64 JSON).

## Copy tool (`cmd/sh-copy`)

```sh
go build -o sh-copy ./cmd/sh-copy
export OLD_DATABASE_URL=postgres://readonly@…/simplehost   # a read-only role
export OLD_DATA_DIR=/srv/simple-host                         # old sites and storage
export OLD_CERT_READY_DIR=…   # one file per handle with its own *.handle.simple-host.app certificate
export OLD_LETSENCRYPT_DIR=/etc/letsencrypt/live
export DATABASE_URL=… SH_DATA_DIR=/srv/simple-host-next SH_APP_DOMAIN=simple-host.app SH_SITE_DOMAIN=simple-host.site
./sh-copy copy     # copy or copy again: keeps ids, upserts, removes what is gone; versions hard-linked
./sh-copy check    # every difference old vs new, one per line; exit 1 if any
./sh-copy report   # owners affected by dropped features (emails for the owner to approve)
```
Same disk for old and new data (hard links). Run with the new service stopped, or accept a re-copy.

## Certificates

- Read per request from `SH_DATA_DIR/certs/<host>/{fullchain,privkey}.pem` (own and shared domains,
  issued by the service; renewed by its domains worker every 5 minutes as needed).
- Carried wildcards stay in `/etc/letsencrypt/live` (`*.simple-host.app`, `*.simple-host.site`,
  per-handle `*.<handle>.simple-host.app`, the 4 shared domains) and are renewed by `certbot.timer`.
- nginx chooses per request: a `map $ssl_server_name $sh_cert` file points those names at their
  letsencrypt folder; anything else falls to `SH_DATA_DIR/certs/$ssl_server_name`. nginx must be
  ≥ 1.15.9 and able to read `certs/`. Issuing a new certificate needs no reload.

## Switch (PLAN section 5, as built)

Before: all 41 tests and the copy fixture test green; two `sh-copy copy` + `check` runs in a row with
zero differences; every address answers through `curl --resolve <host>:443:127.0.0.1` against the new
vhost on a test port; one Let's Encrypt staging certificate issued; your own Claude and ChatGPT work on
copied tokens; top 20 sites and every recipe pass in a real browser; the `report` emails approved;
`pg_dump` of the old database taken.

1. Install `simple-host-next.service` (`SH_ADDR=127.0.0.1:8095`, its own database, `/srv/simple-host-next`,
   `SH_COUNTRY_FILE`, the old `PASSCODE_ENC_KEY`) and start it.
2. Generate `/etc/nginx/simple-host-next-certs.map` from `/etc/letsencrypt/live` (names above).
3. Stop the old service, run the final `sh-copy copy`, then `sh-copy check` (must print nothing).
4. Move the old vhost symlinks to `sites-old/`; enable `simple-host-next.conf`: port 80 as above; port 443
   one catch-all server, `ssl_certificate $sh_cert; ssl_certificate_key $sh_key;`,
   `proxy_pass http://127.0.0.1:8095`, the headers above, buffering off for `/mcp`.
5. `nginx -t`, then reload.
6. Disable the old `domain-certs`, `family-certs` and `site-certs` units (they rewrite vhosts). Keep
   `certbot.timer`; check no renewal hook writes old vhost files.

Back: restore the symlinks, reload nginx, start the old service. Keep the old service and data
untouched for 30 days. Writes on the new service are not carried back (copy namespace SQLite files by
hand if needed), so keep the window short.
