# One-level addresses for new accounts (2026-11-22)

`SITE_BASE_MOVE=flat` gives every person without a certificate of their own
(`SITE_CERT_DIR/ready/<handle>`) one-level addresses under `SITE_BASE_DOMAIN`:
`<site>--<handle>.simple-host.site` and `<handle>.simple-host.site`, all on the
one `*.simple-host.site` wildcard. Everyone with a certificate keeps
`<site>.<handle>.simple-host.app`. The decision is `Addressing.FlatFor`
(`host/sites/addressing.go`); INTENT 2026-11-22 says why. It replaces the paused
two-label move of 2026-09-28 (history: [site base move](../history/site-base-domain-move.md)).

## Live settings

Back up `/etc/simple-host.env` with a dated, root-only copy before changing it.
Never print its contents or source it with shell tracing enabled.

- `SITE_BASE_DOMAIN=simple-host.site` (already set).
- `SITE_BASE_MOVE=flat` (was `off`). `SITE_BASE_CERT_DIR` is no longer read: remove it.
- Unchanged: `PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical`, `SITE_CERT_DIR`.

simple-hack.app is unchanged: leave `SITE_BASE_MOVE` and `TEAM_SITE_FLAT_HOSTS` unset
there (the Simple Hack program ignores `flat`, with a log line, unless
`TEAM_SITE_FLAT_HOSTS=true`).

`flat` runs only on an install whose existing names can all hold one-level
labels (`Hosts.RequireFlatClean`, at startup): no handle (current, alias or
retired on erasure), site name (live or in Recently deleted) or free name holding
`--`, no site named `xn`, no free name stored under the base, and no
`<site>--<handle>` over 63 characters. Otherwise the log says
`SITE_BASE_MOVE=flat refused, running as off:` with the counts, and the
program runs as `off`. Check the start log for it after the switch.

## Before the switch

1. The wildcard serves: `sudo openssl x509 -in /etc/letsencrypt/live/simple-host.site/fullchain.pem -noout -enddate -ext subjectAltName`
   names `*.simple-host.site` and an end date more than 30 days away. Its renewal
   configuration was moved aside by the 2026-10-09 pause: put back only
   `simple-host.site.conf` from `/var/backups/simple-host-site-pause-*/renewal/`
   (never the per-person `<handle>.simple-host.site.conf` files), then
   `sudo certbot renew --cert-name simple-host.site --dry-run`. Serialize with
   `/run/simple-host-certbot.lock`.
2. nginx answers one-label hosts under the base with that wildcard and proxies
   them to the app (the repo copy `deploy/prod/nginx-site-base-domain.conf`, the
   "person's host" block). Re-apply the generated file with plain HTTP sent to
   the same host instead of to .app:
   `sudo HTTP_MODE=same bash deploy/prod/nginx-site-base-domain.sh --apply`
   (backs up, tests and reloads; restores on a failed test). Never edit live
   nginx files by hand.
3. DNS: `*.simple-host.site` resolves to this box (the zone wildcard), so a new
   `<site>--<handle>` label needs no record.
4. Leave `simple-host-site-certs-site.path` and `.timer` stopped and disabled: nothing
   is requested under the base.
5. Stop and disable the per-person `.app` issuer units (`simple-host-site-certs.path`
   and `.timer`) BEFORE the switch, and wait for a running issue to finish
   (`systemctl is-active simple-host-site-certs.service` says inactive). In flat
   mode the app never writes a request, so the issuer has no new work; existing
   certificates keep renewing through plain `certbot renew`.

## Switch

Install the binary and the env change together, restart both programs, and
check the start log has no `flat refused` line. Then empty the requests
directory: `sudo find /var/lib/simple-host-site-certs/requests -type f -delete`.
(The app also withdraws any request it finds for a person without a
certificate, every 10 minutes.)

## Verify

From a client:

- a new account's first site: its `site_url` is `https://<site>--<handle>.simple-host.site/` and answers 200 at once;
  `https://<handle>.simple-host.site/` is its page;
- an existing site host (`https://madurai-idly.vineetu.simple-host.app/`) and person host answer 200 as before;
- `https://madurai-idly--vineetu.simple-host.site/` and `https://vineetu.simple-host.site/` 302 to the .app addresses;
- `http://<site>--<handle>.simple-host.site/` 301s to the same https host;
- the neighbours and health checks in CLAUDE.md.

## Rollback: fix forward

Setting `SITE_BASE_MOVE=off` again strands every published flat link (they
301 to a `.app` label that names no site), so a problem after the switch is
fixed forward, not rolled back. Only before the first flat site exists is
`off` a clean undo.

## Known, left as they are

- A browser that cached a paused-era 301 (`<handle>.simple-host.site` to
  `.app`) can loop on that one old link once that person is flat; clearing
  the browser's cache ends it.
- The removed positions `serve`, `canonical`, `redirect` and `permanent`
  run as `off` with a warning. Hosted runs `off` today.
- Analytics replays treat a label the same under either base, so a view on
  a flat host whose label once had a free-name binding on the other base can
  be counted to that binding.
