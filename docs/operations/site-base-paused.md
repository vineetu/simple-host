# Paused site base (2026-10-09)

The owner paused the alternate base on 2026-10-09. simple-host.app is the only
address served and handed out. Revisit about 2026-11-09; the old migration plan
is in [history](../history/site-base-domain-move.md).

## Live settings

Back up `/etc/simple-host.env` with a dated, root-only copy before changing it.
Never print its contents or source it with shell tracing enabled.

- Keep `SITE_BASE_DOMAIN=simple-host.site` solely to recognise the paused zone.
- Set `SITE_BASE_MOVE=off` and unset `SITE_BASE_CERT_DIR`.
- Keep `PERSON_HOSTS=canonical`, `SITE_HOSTS=canonical` and the existing
  `SITE_CERT_DIR` for simple-host.app.

`SiteBaseHosts` now sends every request on the paused base to the same hostname
labels on `SITE_DOMAIN` with a 301, retaining escaped path and query. This
includes person pages, site hosts, free names and `/v1/` requests. Only .app
serves content, accepts new free-name claims and requests new certificates.
Do not remove the base setting: it identifies the old links to redirect.

If stored free names were moved, use `simple-host move-site-base --from
simple-host.site --to simple-host.app` for counts first, then `--apply` to move
them back. It preserves old disk links; inspect conflicts before proceeding.
In the previous `serve` mode names were already handed out and stored as .app.

## TLS and issuers

Leave `simple-host-site-certs-site.path` and `.timer` stopped and disabled.
Keep all existing .site certificates on disk; do not revoke or renew them.
The shared `certbot.timer` still renews other domains. Move only `.site` renewal
configuration files from `/etc/letsencrypt/renewal/` into a dated, root-only
`/var/backups/simple-host-site-pause-<timestamp>/renewal/` directory so that
ordinary `certbot renew` does not renew the paused zone. Do not move the live,
archive or nginx certificate files. Preserve these configurations for a later
resume. Serialize this step with the issuer/renewal lock
`/run/simple-host-certbot.lock`; leave the shared lock implementation alone.
Keep the nginx TLS proxy blocks so shared HTTPS links reach the app while
their existing certificates are valid. Apply the generated HTTP redirect option
with `sudo HTTP_MODE=app bash deploy/prod/nginx-site-base-domain.sh --apply`
from the repo root: HTTP links go directly to matching .app hosts, while HTTPS
continues through the app redirect. The script backs up its generated file,
tests nginx and reloads, restoring on a failed test. Never edit live nginx files
by hand or open the content-host file. simple-host.app and simple-hack.app
certificate issuance and the shared Certbot lock remain unchanged.

## Verify and rollback

Restart both `simple-host` and `simple-hack` after installing the checked binary.
From a client, check a real person host and site host on .site for 301s to their
matching .app hosts, with path and query kept. Check health, the .app site and
person page, the legacy content link and neighbouring services as in CLAUDE.md.
A new deployment must return an .app `site_url`. Public pages, OpenAPI, llms.txt,
connector descriptions and skill downloads must contain only .app examples.

If deployment verification fails, restore the backed-up binary and env file and
restart both services. Restore the old HTTP behavior through the same script
with `HTTP_MODE=same --apply`. Keep the .site issuer units disabled during rollback.
The saved .site renewal configurations stay paused unless the owner resumes the move.
