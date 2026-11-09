# Paused site base (2026-10-09)

The owner paused the alternate base on 2026-10-09. simple-host.app is the only
address served and handed out. Revisit about 2026-11-09; the old migration plan
is in [history](https://github.com/vineetu/simple-host/blob/main/docs/history/site-base-domain-move.md).

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


## Verify and rollback


If deployment verification fails, restore the backed-up binary and env file and
restart both services. Restore the old HTTP behavior through the same script
with `HTTP_MODE=same --apply`. Keep the .site issuer units disabled during rollback.
The saved .site renewal configurations stay paused unless the owner resumes the move.

## Resume

Set `SITE_BASE_MOVE` and `SITE_BASE_CERT_DIR` again, restore the saved renewal
configurations and enable `simple-host-site-certs-site.path` and `.timer`. The
app then writes .site requests for new people, and the certificate watch alerts
on them, without other changes.
