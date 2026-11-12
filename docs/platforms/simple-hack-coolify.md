# Install Simple Hack on Coolify

Run events, teams, team sites, judging and results on your own server.
People use password accounts without email.
Its default `ghcr.io/vineetu/simple-hack:0.8.20` image can be overridden with `SIMPLE_HACK_IMAGE`.

## What you need

A server with Coolify, a domain and wildcard DNS. Use a **Docker Compose Empty
Service** with the proxy settings below. The [Simple Host guide](coolify.md)
describes a separate installation.

The [service template](../../deploy/hack/standalone/coolify/service-template.yaml)
can generate the database credentials, `ADMIN_API_KEY`, `PASSCODE_ENC_KEY` and
`ANALYTICS_SALT` for you. Set `SITE_DOMAIN`. Email settings are optional.
Steps 1 and 2 below still apply.

## Install

1. Prepare the domain and certificate capacity in the
   [standalone guide](simple-hack-standalone.md). Point the apex and wildcard DNS
   records at the Coolify server. Use Coolify's **Traefik v3** proxy with `http`
   and `https` entrypoints and the external `coolify` Docker network. In the
   proxy's Compose configuration, add
   `--entrypoints.https.allowACMEByPass=true` under Traefik's `command:` and
   restart the proxy. This lets the application's Caddy answer TLS-ALPN
   challenges through Traefik's TLS passthrough. See [Traefik entrypoints](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/).
2. In your Coolify project and environment, create a **Docker Compose Empty**
   Service and paste [`compose.yaml`](../../deploy/hack/standalone/coolify/compose.yaml)
   into **Source Compose**. Under **Edit Compose File**, turn off **Escape
   special characters in labels** so the domain regex and Compose variables
   reach Traefik correctly. Leave the component **Domains** fields empty: the
   supplied labels route the apex, event, and nested team names. Review the
   generated **Deployable Compose** before deploying. [Coolify Service Compose](https://coolify.io/docs/services/configuration/docker-compose).
3. Set `SITE_DOMAIN`, `DB_PASSWORD`,
   `ADMIN_API_KEY`, `PASSCODE_ENC_KEY`, and `ANALYTICS_SALT` under the Service's
   environment variables. `PASSCODE_ENC_KEY` is 32 random bytes in standard
   base64. The other secrets can be random hex. The standalone installer's
   `--prepare-only` option generates private values. Set `SIMPLE_HACK_IMAGE`
   to use another release. Keep these values across restarts and upgrades.
   Password accounts don't need email. Set `RESEND_API_KEY` and `MAIL_FROM` only
   if you want email-code sign-in or email notices.
   Company sign-in (`OIDC_ISSUER`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`,
   `ALLOWED_EMAIL_DOMAINS`, `OIDC_LABEL`, `OIDC_INSECURE_ALLOWED`,
   `EMAIL_SIGNIN`) and the TLS mode (`TLS_MODE`, `ACME_CA`) go in under the
   same Service environment variables. See
   [the standalone guide](simple-hack-standalone.md#company-sign-in-and-private-networks)
   for what each one does. For `internal`, `acme` or `certs`, place `ca.pem`
   and `certs/` in a `tls/` directory next to this Service's compose file on
   the Coolify host, the same layout the standalone installer creates.
4. Deploy. Check that `db` is healthy, `release` exits with code 0, and `app`
   becomes healthy. The one-shot `release` container can make Coolify's
   aggregate Service status show **exited** even while `app` and `db` are
   healthy. Check `https://<your-domain>/`, an event address, and a published
   team address in a browser. Each should have a trusted certificate.

## First sign-in

Open your domain and choose **Create an account**. Use an email or username plus a password.
Open `https://<your-domain>/admin` with the Service's `ADMIN_API_KEY`.
Set `PASSWORD_SELF_SIGNUP=off` for admin-created accounts only.

## Connect an AI app

In ChatGPT or Claude, add a custom connector called **Simple Hack** with
`https://<your-domain>/mcp`. Sign in with your account on this server and choose **Allow**.
Your server's `/get-started` page has the same steps. Use your own server's address.

## Upgrade

Back up first, then change `SIMPLE_HACK_IMAGE` and restart the Service.
The `release` container runs migrations before the app starts. A failed migration
prevents the new app from starting. Fix the cause and deploy again.
Do not downgrade a migrated database. Restore a matching backup instead.

## Back up

The `db` and `data` volumes hold Postgres, team site files and certificates.
Keep both. Use a logical Postgres dump plus a copy of the data volume, as described
in the [standalone guide](simple-hack-standalone.md#back-up), using your Service's
Compose project and environment file paths.

## Troubleshooting

- Check the platform, event and nested team DNS records and certificate capacity.
- Leave the component **Domains** fields empty and check the Traefik passthrough settings above.
- Check `db`, `release` and `app` separately if Coolify shows **exited** for the Service.

TLS passes through Traefik to Caddy. PROXY protocol preserves visitor addresses
for rate limits and analytics. Postgres has no host port.

A public one-click catalog listing is separate: see [Coolify's template eligibility](https://coolify.io/docs/contribute/service).

Release notes: [the changelog](../../CHANGELOG.md).
