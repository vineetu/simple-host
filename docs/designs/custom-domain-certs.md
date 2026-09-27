> **Status: Built (2026-09-27), not yet installed on the box** — `internal/handler/domaincert.go`, `domaincheck.go`, issuer in `deploy/domain-certs/`. Live once the steps below are done and `DOMAIN_CERT_DIR` is set.

# Custom-domain certificates without the operator

A person connects `shop.brand.com`, adds the DNS record, and the domain goes live on its own.
Same hand-off as the per-person site-host certificates (`per-site-subdomains.md`): the app
never runs certbot; a root-owned issuer does.

## Flow

1. The domain check (every 2 minutes) sees the domain resolve here while HTTPS does not answer.
   The app writes an empty `DOMAIN_CERT_DIR/requests/<domain>` and reports
   `certificate_status: issuing`. A binding whose DNS points here does not expire.
2. The issuer (`simple-host-domain-certs`, path unit on `requests/` + 10-minute timer) takes
   requests oldest first. It honours one only while `/srv/simple-host/sites/domains/<domain>`
   (the app's link for the binding) exists. It checks the A record points here and that no AAAA
   record points elsewhere (15-minute retry), then runs certbot HTTP-01 with the webroot
   `/var/www/acme` that the port-80 default server already answers for any host.
3. On success it writes `sites-available/simple-host-domain-<domain>` from
   `vhost.conf.template`, links it into `sites-enabled`, runs `nginx -t`, reloads, and writes
   `ready/<domain>`. On failure it writes `failed/<domain>` with one line (retried after 6 h);
   the app shows it as `last_error` with `certificate_status: failed`.
4. The next check fetches `https://<domain>/`; a 2xx makes it `active`. If the site had an
   earlier own address (`previous_domain`), it is let go now: a claimed
   `<name>.simple-host.app` is kept in `legacy_hostnames` and redirects to the domain.
5. When the binding goes (disconnect, expiry, site delete) the app removes the link; the
   issuer's next run removes its server block, the ready marker and the certificate it issued.
   Hand-made `customdomain-<domain>` servers are never touched and count as ready.

Caps: 50 new certificates per rolling day, 10 per run. Renewals are certbot's own timer (the
lineage keeps the webroot and `systemctl reload nginx` as its deploy hook).

## Install (operator, once)

```
sudo install -m 0755 deploy/domain-certs/issue.sh /usr/local/sbin/simple-host-domain-certs && sudo install -D -m 0644 deploy/domain-certs/vhost.conf.template /usr/local/lib/simple-host-domain-certs/vhost.conf.template && sudo install -m 0644 deploy/domain-certs/simple-host-domain-certs.service deploy/domain-certs/simple-host-domain-certs.timer deploy/domain-certs/simple-host-domain-certs.path /etc/systemd/system/ && sudo install -D -m 0644 deploy/domain-certs/simple-host.service.d-domain-certs.conf /etc/systemd/system/simple-host.service.d/domain-certs.conf && sudo install -d -m 0755 -o simplehost -g simplehost /var/lib/simple-host-domain-certs/requests && sudo systemctl daemon-reload && sudo systemctl enable --now simple-host-domain-certs.timer simple-host-domain-certs.path
```

Then add `DOMAIN_CERT_DIR=/var/lib/simple-host-domain-certs` to `/etc/simple-host.env` (with
`sed`, after a backup), apply `db/migrations/cp-domains-custom-domain-certs.sql`, and restart
`simple-host`. Optional `/etc/simple-host-domain-certs.conf` overrides `IP` (this server's
IPv4; default the apex's A record), `IP6`, `DAILY`, `PER_RUN`, `RETRY_AFTER`.
