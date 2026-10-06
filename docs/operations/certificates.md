# Certificates on the hosted box

The root issuers under `deploy/site-certs/` and `deploy/domain-certs/` try
Let's Encrypt first with their existing arguments. A failed Certbot request
containing `too many certificates`, `rateLimited` or
`urn:ietf:params:acme:error:rateLimited` retries once at
`https://acme.zerossl.com/v2/DV90`. Other failures keep the existing retry path.

Platform sites on simple-host.app, simple-host.site and simple-hack.app keep
DNS-01, the Vercel hooks/credentials and their ECDSA wildcard name. Custom
domains keep HTTP-01, the webroot, ECDSA and all requested names (including an
eligible www/bare partner). Both keep the same Certbot lineage and deploy hook;
no nginx change or alternate certificate path is needed. Existing queue caps
still apply to successful issuance, including ZeroSSL. Address families do
not issue via ACME: their script checks an operator-provided wildcard lineage
and leaves its certificates and renewal setup alone.

## Configuration and credentials

`/etc/simple-host-cert-issuers.conf` is root-owned shell configuration, sourced
by both issuers before their existing per-issuer configuration. See
`deploy/cert-issuers/config.example`. `CERT_FALLBACK_CA=zerossl` is the script
and hosted-box default; `CERT_FALLBACK_CA=none` disables new fallbacks. A
per-issuer `.conf` can override it, or an exported environment value works
when the shared config leaves it unset. The app does not read this knob, so
it is not in the app's settings catalog.

The helper reads `/etc/simple-host-secrets/zerossl-eab.json` (root:root 0600),
containing `success`, `eab_kid` and `eab_hmac_key`. Registration always uses
support@simple-host.app. The API key is not read by issuers. EAB goes into a
0600 temporary Certbot config inside a 0700 directory, never into command
arguments. ZeroSSL diagnostics are private and temporary too, since account
registration logs can contain the EAB binding. Both are removed on exit;
fallback failures emit a short reason without raw diagnostics.

## Renewal and alerts

Certbot stores the successful lineage's ZeroSSL server and registered account,
plus its original DNS hooks or webroot and deploy hook. EAB binds the account
when registering; the saved account key authenticates subsequent renewals,
so a renewal does not need the temporary EAB file. Keep `/etc/letsencrypt/accounts`
with the renewal files when backing up or restoring. The helper restores the
usual log directory in the newly issued lineage's renewal configuration.
The existing twice-daily `certbot.timer` runs `certbot renew` across both CAs;
there is no global server override. Setting the fallback knob to `none` does
not change existing ZeroSSL renewals.

Every fallback attempt sends a short outcome (issued, failed, or credentials
unavailable) through the same Hermes Signal JSON-RPC as `sh-network-watch`,
at `http://127.0.0.1:8086/api/v1/rpc`. Account and recipient come from the existing
root-only `/etc/sh-network-watch.env`. A shared lock and last-attempt timestamp
under `/var/lib/simple-host-cert-alerts` limit notes to one per hour per domain
(platform zone for wildcard requests, connected domain for HTTP-01). Alert
failure is logged and does not undo a successful certificate.

## Install and verify

Run `sudo bash deploy/cert-issuers/install.sh` from a checkout. It installs the
shared helper, site/domain scripts, DNS helper, deploy hook, unchanged domain
template and existing service/path/timer units. Replaced files are backed up
under `/var/backups/simple-host-cert-issuers/<UTC timestamp>/`. Existing issuer
configs are preserved; only a missing shared config is created. It reloads
systemd; existing enabled paths/timers continue to run. Family scripts and
nginx configuration are not changed.

`bash deploy/cert-issuers/fallback_test.sh` tests primary success, the three
rate-limit forms, identical DNS/HTTP arguments and partner SANs, disabled
fallback, unrelated errors, failed fallback, credential handling, temporary
cleanup, renewal settings and hourly alerts. `make check` includes it alongside
the issuer pipeline fixtures. For a real smoke test, scope the queue to one
throwaway account and inject the primary failure with a Certbot stub that
only delegates the fallback to real Certbot. Never spend Let's Encrypt
production quota on a test; use staging for any real LE request. Verify the
certificate and page from an HTTPS client, delete the account through the
normal API, then revoke and delete only the test lineage and its copied key
and DNS records.

ZeroSSL is a bridge around Let's Encrypt's shared registered-domain limit.
Adding the platform domains to the Public Suffix List remains the long-term
fix: certificates beneath each handle then count independently.

Reference: [Certbot CA and config guide](https://eff-certbot.readthedocs.io/en/stable/using.html),
[ZeroSSL ACME](https://zerossl.com/documentation/acme),
[ZeroSSL revocation](https://help.zerossl.com/hc/en-us/articles/900005244486-Revoking-Certificates-Issued-via-ACME).

## Hosted smoke test, 2026-10-06

One throwaway account and its `probe` site were created through the normal API.
The app's wildcard request was copied into a scoped issuer queue; the issuer
used the normal DNS helper and deploy hook. A test-only Certbot wrapper returned
`rateLimited` for the primary without contacting Let's Encrypt and delegated
the fallback to real Certbot. Exactly one ZeroSSL wildcard was issued for
`*.zs-probe-8e353726-01.simple-host.app`. An HTTPS client validated the certificate,
its ZeroSSL ECC issuer and wildcard SAN, and received 200 with the expected page.
The renewal config retained ZeroSSL and its registered account, ECDSA and the
normal DNS/deploy hooks; an ordinary targeted `certbot renew` loaded it and
correctly skipped the fresh certificate without another issuance. A future
renewal was not forced (it would issue a second real certificate).

The account was deleted through the normal API (204); ZeroSSL revocation and
lineage deletion succeeded. Copied keys, ready/request markers and both test A
records were removed; paused issuer triggers were restored. The first ZeroSSL
attempt failed before registering an account or issuing a certificate, and the
hourly Signal throttle retained its failure note when the subsequent attempt
succeeded. No EAB values were found in retained plain Certbot logs. No Let's
Encrypt production request was made.
