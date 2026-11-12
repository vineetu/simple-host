# Install Simple Hack on your own server

Run a hackathon platform on your own server: events, teams, team sites, judging and results.
People use password accounts without email.

## What you need

- A Linux server with Docker Engine and the Compose plugin, Python 3, OpenSSL and `flock`.
- About 1 GB RAM and 10 GB disk is enough. While running, it uses about 200 MB RAM and 0.7 GB disk.
- Your own domain. Point `hack.<domain>` and `*.hack.<domain>` at the server. Check that event and nested team addresses resolve there. Remove AAAA records unless you also configure and verify IPv6.
- Ports 80 and 443 free and reachable from the internet.

Caddy gets one certificate per platform, event or team address, stored in the `data` volume for reuse.
Plan capacity before a large event because Let's Encrypt allows 50 new certificates per registered domain per seven days, shared with other services on that domain ([CA limits](https://letsencrypt.org/docs/rate-limits/)).
One wildcard certificate for `*.hack.<domain>` does not cover team addresses such as `team.event.hack.<domain>`.

## Install

1. Use a Linux server with Docker and its Compose plugin. Point `hack.example.com` and `*.hack.example.com` at it.
2. Download and unpack the release:

   ```sh
   VERSION=0.8.20
   curl -fLO https://github.com/vineetu/simple-host/releases/download/hack-v$VERSION/simple-hack-$VERSION.tar.gz
   tar xzf simple-hack-$VERSION.tar.gz
   cd simple-hack-$VERSION
   ```

3. Create the configuration file:

   ```sh
   umask 077; cp deploy/hack/standalone/.env.example /tmp/sh.env
   cat >> /tmp/sh.env <<'EOF'
   SITE_DOMAIN=hack.example.com
   EOF
   ```

4. Run the installer:

   ```sh
   sudo bash deploy/hack/standalone/install.sh --config /tmp/sh.env && rm /tmp/sh.env
   ```

5. Open `https://hack.example.com/signin` and choose **Create an account**.

The [setup helper](https://simple-hack.app/setup) fills in your domain.

## First sign-in

Choose **Create an account** and use an email or username plus a password.
Every account can create events. Open `https://hack.example.com/admin` with the key from:

```sh
sudo grep ADMIN_API_KEY /opt/simple-hack/.env
```

Set `PASSWORD_SELF_SIGNUP=off` for admin-created accounts only, then create them from `/admin`.

## Connect an AI app

In ChatGPT or Claude, add a custom connector called **Simple Hack** with
`https://<your-domain>/mcp`. Sign in with your account on this server and choose **Allow**.
Your server's `/get-started` page has the same steps. Always use your own server's address.

## Settings

Add these settings in step 3, or change them later: write only the lines you change
to a new file and re-run the installer with it. They are kept across upgrades.
There is no event limit by default. For a setting not listed in `.env.example`, add it to
the environment map in `/opt/simple-hack/compose.yaml`; upgrades replace that file, so add it again after each upgrade.

```sh
umask 077; echo 'SUPPORT_EMAIL=help@example.org' > /tmp/sh.env
sudo bash /opt/simple-hack/install.sh --config /tmp/sh.env && rm /tmp/sh.env
```

| Setting | What it does |
|---|---|
| `SUPPORT_EMAIL` | Your support contact shown on pages. |
| `PRIVACY_URL` | Your own privacy policy, linked from the footer. |
| `RESEND_API_KEY`, `MAIL_FROM` | Optional email-code sign-in and email notices. Use a real key and a verified sender. |
| `ASSISTANT_BASE_URL`, `ASSISTANT_MODEL`, `ASSISTANT_API_KEY` | Configure the organiser Ask assistant. It stays off until the base URL and model point at an OpenAI-compatible endpoint. Set the API key if the endpoint needs one. |
| `HTTP_BIND`, `HTTPS_BIND` | Change which address and port the server listens on. Defaults are `0.0.0.0:80` and `0.0.0.0:443`. |

## Upgrade

Download and unpack the new release as in step 2, setting `VERSION` to the new version.
From that release's directory, run:

```sh
sudo bash deploy/hack/standalone/upgrade.sh --image ghcr.io/vineetu/simple-hack:$VERSION
```

The upgrade keeps your domain, credentials, database and files, and saves the previous
settings as `/opt/simple-hack/.env.previous`. It stops the app while applying migrations.
If a migration fails, fix the cause and rerun the upgrade. Do not downgrade a migrated database;
restore a matching backup instead.

If your previous release did not include `zi-site-search-summary.sql`, run this once after upgrading:

```sh
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env run --rm --no-deps -T app site-summary-backfill
```

## Back up

The `db` volume holds Postgres. The `data` volume holds team site files, certificates
and access logs. Back up both: use a logical Postgres dump plus a copy of the data volume.
Stop the app while taking the pair so its files and database stay consistent:

```sh
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env stop app
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env exec -T db pg_dump -U simplehack -d simplehack > simple-hack-db.sql
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env run --rm --no-deps -T --entrypoint tar app czf - -C /data . > simple-hack-data.tar.gz
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env up -d --no-deps app
```

Keep a private copy of `/opt/simple-hack/.env` and `/opt/simple-hack/tls` with the backups.
Deleting the volumes deletes your data.

## Troubleshooting

- **DNS or certificates:** check that the platform, event and nested team addresses point at this server. Remove conflicting records and check certificate capacity.
- **Ports busy:** stop the service using ports 80 or 443, or use the [Coolify guide](/docs/self-hosting/platforms/simple-hack-coolify.md) with its existing proxy.
- **Migration failure:** the installer leaves the app stopped and both volumes intact. Fix the reported cause and rerun the installer or upgrade.
- **Logs:** run the command below to read app logs.

```sh
sudo docker compose --project-directory /opt/simple-hack --env-file /opt/simple-hack/.env logs app
```

From another machine, check that `https://<your-domain>/healthz` and `/readyz` return 200.
`https://<your-domain>/internal/tls-ask` must return 404 from outside.

## Company sign-in and private networks

Use your own OpenID Connect provider for work accounts. Private networks need internal
DNS and a TLS mode that your devices trust. Set these options in the configuration file
and re-run the installer with `--config`.

### Settings

| Setting | What it does |
|---|---|
| `OIDC_ISSUER` | The identity provider's issuer URL. Setting this together with `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET` turns on company sign-in. |
| `OIDC_CLIENT_ID` | The OAuth client ID registered at the identity provider. |
| `OIDC_CLIENT_SECRET` | The client secret. Never print or log it. |
| `ALLOWED_EMAIL_DOMAINS` | A comma list, such as `example.com,example.co.uk`. Sign-in from any other email domain is refused. Required when the issuer is Google, since any Google account can otherwise authenticate, not only this company's. |
| `OIDC_LABEL` | The name on the sign-in button. Default text reads "Sign in with your company account". Setting `OIDC_LABEL=Acme Okta` changes it to "Sign in with Acme Okta". |
| `OIDC_INSECURE_ALLOWED` | Set to `true` only to allow a plain HTTP issuer for local testing. Leave unset in any real installation. |
| `MCP_APP_MY_SITES` / `MCP_APP_DASHBOARD` | Optional connector screens, both `off` by default. Set `on` or `accounts:<handles>` in `.env`. Dashboard also needs My sites.  |
| `EMAIL_SIGNIN` | `on` by default. Set to `off` to turn off email-code sign-in everywhere: the account, the connector and every team site's visitors. With `EMAIL_SIGNIN=off`, keep password sign-in or company sign-in enabled so people can still sign in. |

With company sign-in configured, `RESEND_API_KEY` and `MAIL_FROM` become
optional. Set them to send sign-in codes and email notices.

### Registering the redirect URI

Every identity provider needs the same redirect URI, built from your own
domain:

```
https://<SITE_DOMAIN>/v1/auth/oauth/oidc/callback
```

- **Okta.** Admin console, Applications, Create App Integration, choose OIDC
  and Web Application. Set the sign-in redirect URI above and the grant type
  to Authorization Code. `OIDC_ISSUER` is the org URL, such as
  `https://your-org.okta.com`.
- **Google Workspace.** Google Cloud Console, APIs & Services, Credentials,
  Create OAuth client ID, Web application. Set the authorized redirect URI
  above. `OIDC_ISSUER` is `https://accounts.google.com`. Set
  `ALLOWED_EMAIL_DOMAINS` to the company's domain so sign-in stays inside the
  organisation.
- **Microsoft Entra ID.** Entra admin center, App registrations, New
  registration, platform Web, with the redirect URI above. `OIDC_ISSUER` is
  `https://login.microsoftonline.com/<tenant-id>/v2.0`. Create the client
  secret under Certificates & secrets.
- **Keycloak.** In the realm, Clients, Create client, OpenID Connect, with
  client authentication on and the valid redirect URI above. `OIDC_ISSUER` is
  `https://<keycloak-host>/realms/<realm>`.

### Choosing a TLS mode

`TLS_MODE` (`--tls` at install time) picks how Caddy gets its certificates.
The `public`, `internal` and `acme` modes obtain certificates on demand.
The `certs` mode uses the files you supply.

| Mode | Source | Use it when |
|---|---|---|
| `public` (default) | Let's Encrypt, the same as today | The server is reachable from the public internet on ports 80 and 443. |
| `internal` | Caddy's own local certificate authority | The server is only reachable inside the company network, with no public DNS and no outbound ACME traffic, and IT can add one more trusted root to company devices. |
| `acme` | The company's own ACME server (`--acme-ca URL`), such as step-ca, Smallstep, Active Directory Certificate Services with its ACME add-on, or Vault PKI's ACME endpoint | The company already runs internal PKI and wants Simple Hack's certificates issued and renewed through it like everything else internal. |
| `certs` | Certificate files you supply (`--cert-dir DIR`), no on-demand issuance | There is a known, fixed list of addresses in advance and no CA (public, internal or ACME) is available or wanted. |

`--ca-cert FILE` supplies the company's root certificate (PEM). It is trusted
by the application for calls to an internal identity provider, and, in `acme`
mode, by Caddy for calls to an internal ACME server. It is copied to
`tls/ca.pem` in the install directory and is unrelated to which `TLS_MODE` is
in effect.

For `certs` mode, each `.pem` file in `--cert-dir` holds a certificate chain
and its private key together. Across all the files, the coverage must include
the apex domain, its wildcard, and the wildcard of every event:
`<domain>`, `*.<domain>` and `*.<event>.<domain>` for each event you run.
A wildcard certificate covering `<domain>` and `*.<domain>` is not
enough on its own, because a team site is a third DNS label down
(`<team>.<event>.<domain>`). Add one certificate per event, or reissue the
event wildcard whenever a new event is created.

### DNS inside the company

A private installation needs the same two DNS shapes as a public one, just
answered by the company's internal DNS instead of a public registrar: an
apex record for the domain itself and a wildcard record for everything under
it (`hack.company.internal` and `*.hack.company.internal`). That one wildcard
resolves both an event address (`spring.hack.company.internal`) and a team
address under it (`robot.spring.hack.company.internal`). Point both records
at the server's address on the company network, and keep them off any public
DNS zone.

### Trusting the certificate on company devices

In `internal` mode, no public certificate authority is involved, so devices
need to trust Caddy's own root certificate once. After the first HTTPS
request to the installation (an issuance only happens on demand), the
installer prints a one-line command that saves that root certificate to a
file. Hand that file to IT to add to the trusted roots on company devices
through MDM or group policy. The
`acme` mode's trust question is handled the same way as the rest of the
company's internal PKI, since the certificates come from there.

Release notes: [the changelog](https://github.com/vineetu/simple-host/blob/main/CHANGELOG.md).
