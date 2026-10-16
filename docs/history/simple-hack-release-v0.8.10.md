# Simple Hack standalone v0.8.10

Status: published, verified, and pinned on 2026-10-16.

Simple Hack for companies (INTENT 2026-10-16). Annotated tag hack-v0.8.10 points
at 3549101a, the commit deployed to simple-hack.app and verified live. It adds
company sign-in through the company's OpenID Connect provider (`OIDC_ISSUER`,
`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `ALLOWED_EMAIL_DOMAINS`, `OIDC_LABEL`),
`EMAIL_SIGNIN=off`, and the installer's `--tls internal|acme|certs`, `--acme-ca`,
`--cert-dir` and `--ca-cert` options, plus the `/companies` page. The workflow
built both images from the tag and published the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.10).

The ZIP and tar.gz match SHA256SUMS (45 entries). `simple-hack:0.8.10` is
published for linux/arm64 and linux/amd64; both report v0.8.10 at commit 3549101
(arm64 on the owner's box, amd64 on the droplets).

Checks on two throwaway DigitalOcean droplets in sfo2, deleted afterwards with
their SSH key:

- Fresh install, `s-1vcpu-2gb`: the published 0.8.10 package with
  `--tls certs` (a certificate from a throwaway "company" CA covering the apex,
  `*.<domain>` and `*.spring.<domain>`), `--ca-cert`, a Dex identity provider
  served over HTTPS with that CA, `ALLOWED_EMAIL_DOMAINS=allowed.example.com`,
  `EMAIL_SIGNIN=off` and no Resend key. 58 migrations recorded. The apex, an event
  host and a team host present the company certificate and verify against the
  company CA. Playwright from the owner's box: the providers list shows `oidc`
  with its label, `POST /v1/auth` answers 403 `email_signin_off`, account sign-in
  through Dex works, an account outside the allowed domain gets the refusal page,
  and the connector flow works end to end (dynamic registration, sign-in through
  Dex, Allow, token exchange, `/mcp` tools/list and who_am_i).
- Upgrade, `s-2vcpu-4gb`: the published 0.8.9 package on a real Let's Encrypt
  certificate, with an event, member, team key and published team site created by
  the package fixture (`tests/smoke.py`). `upgrade.sh --image
  ghcr.io/vineetu/simple-hack:0.8.10` applied the one new migration
  (`zg-oidc-signin.sql`, 58 total) and kept every setting and credential (the
  installer adds `TLS_MODE=public` and `EMAIL_SIGNIN=on`); the fixture verified
  its data and Let's Encrypt certificates. The same box was then re-run with
  company sign-in, `--tls internal` and `--ca-cert`: the data verified again, a
  new event's address got a certificate from Caddy's local CA, and the same
  Playwright checks passed.

Before tagging, the candidate also passed the local package smoke test from the
published 0.8.9 package and image (`TEST_PREVIOUS_PACKAGE`, `TEST_PREVIOUS_IMAGE`,
`TEST_HACK_PRESENTATION=1`) and a local Dex run. Installer pins changed only after
these checks passed. Evidence: `jobs/hackent/` on the owner's box.
