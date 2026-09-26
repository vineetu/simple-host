> **Status: Done (2026-09-26)** — every site at `https://<site>.<handle>.simple-host.app/` (`internal/handler/sitehost.go`, `SITE_HOSTS=canonical`); per-person certificates from the issuer in `deploy/site-certs/`. Supersedes the address half of `per-person-subdomains-plan.md`.

# Per-site subdomains (2026-09-26)

Every hosted site gets its own origin, `https://<site>.<handle>.simple-host.app/`, with its files at
the host root. The person host `https://<handle>.simple-host.app/` stays the person's page of
public sites. Claimed `<name>.simple-host.app` names and custom domains are unchanged. INTENT
decision "2026-09-26. Per-site subdomains".

## Addresses and redirects

- `<site>.<handle>.simple-host.app/...` — the site. `/v1/` there answers for that one site only.
- `<handle>.simple-host.app/<site>/...` — 302 to the site host, path and query kept (once the
  person's certificate is ready; before that it serves the site, see Fallback).
- `sites.simple-host.app/<handle>/<site>/...` — 302 to the site's live address (nginx rewrite to
  `/internal/site-redirect/...`). `vineetu/eb2-wait` stays on the content host.
- A site with a claimed name or custom domain 302s there from every other address.
- A root-absolute link written for an older address (`/<handle>/<site>/x` or `/<site>/x`) that
  names no file in the site is sent to `/x` at the site root.
- Old handle aliases 302 to the current `<site>.<handle>` host.

## Certificates

The platform wildcard `*.simple-host.app` covers one label only, so each person needs
`*.<handle>.simple-host.app`.

1. The app drops an empty `SITE_CERT_DIR/requests/<handle>` (on first site, and a sweep every
   10 minutes). `SITE_CERT_DIR` is e.g. `/var/lib/simple-host-site-certs`.
2. A root-owned issuer (`deploy/site-certs/`, systemd timer every 10 minutes) runs certbot DNS-01
   with the Vercel hooks in `/usr/local/lib/certbot-vercel/`, within a budget of 40 new
   certificates per rolling week and 12 per rolling day (Let's Encrypt allows 50 per registered
   domain per week; the daily cap keeps a burst of sign-ups from spending the week), 6 per run.
   Requests past the budget wait in the queue (FIFO); renewals are plain `certbot renew`.
   A path unit also starts a run when a request lands.
   Before issuing it makes sure `<handle>` and `*.<handle>` have explicit A records at Vercel.
   Why: the DNS-01 TXT record `_acme-challenge.<handle>` makes `<handle>.simple-host.app` an
   empty non-terminal, and per RFC 4592 the zone wildcard `*.simple-host.app` then no longer
   answers for `<handle>` or anything under it (verified: `lab.simple-host.app`, which only has
   `*.lab` beneath it, answers NODATA). Without the explicit records, the person's address would
   stop resolving during every issuance and renewal (and for good if a TXT record were left
   behind).
3. It copies the certificate where nginx reads it and writes `SITE_CERT_DIR/ready/<handle>`.
4. nginx has one server for `~^(?<site>[a-z0-9-]+)\.(?<person>[a-z0-9-]+)\.simple-host\.app$`
   that loads the per-person certificate by variable and proxies to the app.

Usually live within ~10 minutes of a person's first site; renewal is the issuer's job.

## Fallback

The app never hands out or redirects to a site host before `ready/<handle>` exists: a TLS name
mismatch cannot be fixed after the handshake. Until then that person's sites keep the
person-path form `<handle>.simple-host.app/<site>/` (served by Go, `/v1/` bound to that person's
sites), and `site_url`, the connector `url` and every page show whichever address is live. Docs
tell agents to give the person the returned URL, never a composed one, and to keep links relative
(the same site can be served under a path or at a root).

## Flag

`SITE_HOSTS=off|serve|canonical`, default `off`; needs `PERSON_HOSTS` on.
- `off` — no site hosts. Event and self-hosted instances keep their current model.
- `serve` — site hosts answer; addresses handed out stay person-path.
- `canonical` — production: the site host is the address, and old forms redirect to it.
Without `SITE_CERT_DIR` every person counts as ready (for instances with their own certificates).

## Security bindings

- Visitor sessions (`__Host-sh_vsess`, host-only) are issued on the site host and cover that one
  site; a sign-in no longer covers all of a person's sites.
- Emailed visitor codes are bound to one site; the OAuth `return_to` on a site host resolves to
  that site (`SiteReturnSite`) and the one-time establish hop sets the cookie only there.
- `/v1/` on a site host resolves every lookup to that one site; no fallback to a global name.
- Private collections accept submissions only on the site's own address.
- Hosted pages still never hold an API key.
- Open, pre-existing (not made worse): the OAuth `state` is not tied to the browser that
  started sign-in, and a `/v1/visitor/establish?once=` link can be handed to a victim within its
  60 s life (login CSRF: the victim ends up signed in as the attacker on that one site). On a
  site host the blast radius is one site instead of all of a person's sites. Fix: a site-host
  `/v1/visitor/oauth/{provider}` hop that sets a `__Host-` binding cookie and passes its hash
  through the OAuth state to the establish token (needs a column on `oauth_states` and
  `visitor_establish_tokens`, and an auth.js change). Not done here.
- All `<site>.<handle>` hosts are same-site with each other and the apex until simple-host.app is
  on the Public Suffix List (`public-suffix-list-submission.md`); cookies are host-only and writes
  require `X-SH-CSRF`, so that gap does not open cross-site writes.

## Accepted costs

- What a page kept in the browser (localStorage, IndexedDB) starts empty at the new address;
  server-saved data moves with the site. Owner accepted, as on 2026-09-25.
- Analytics keep the same `site_id`; the ingester attributes site hosts.
