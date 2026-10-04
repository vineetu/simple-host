# Full Simple Hack on Coolify

Its default `ghcr.io/vineetu/simple-hack:0.8.1` image can be overridden with
`SIMPLE_HACK_IMAGE`. Current release verification is in the standalone guide;
the dated cloud test below records the release actually exercised there.

The full platform uses the same hand-drawn landing film, ink theme, local
Caveat/Kalam fonts and per-event crayon colours as the hosted service. At
`https://<your-domain>/get-started`, “Pick your AI” leads to the instance's own
`https://<your-domain>/mcp`, role prompts and the FAQ; skill folders and plugin
ZIPs are under “Other ways to install” and also name your instance. Use those
instance downloads rather than the hosted GitHub skills when self-hosting.
Organisers share a join link and a private judge link; participants form teams,
publish a team site and complete an entry. No account keys are handed out as
part of that onboarding.

![Simple Hack Get started](assets/simple-hack-get-started.png)


**Tested on Coolify 4.3.23 through its API on 2026-10-02.** This guide uses a
user-defined **Docker Compose Empty Service**. Coolify deployed the published
`ghcr.io/vineetu/simple-hack:0.8.1` image, ran all 44 migrations, and served an
event and a published team site over trusted HTTPS. The existing
[small-box Coolify guide](coolify.md) describes a different installation.

1. Prepare the domain, verified email sender, and certificate capacity in the
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
3. Set `SITE_DOMAIN`, `RESEND_API_KEY`, `MAIL_FROM`, `DB_PASSWORD`,
   `ADMIN_API_KEY`, `PASSCODE_ENC_KEY`, and `ANALYTICS_SALT` under the Service's
   environment variables. `PASSCODE_ENC_KEY` is 32 random bytes in standard
   base64; the other secrets can be random hex. The standalone installer's
   `--prepare-only` option generates private values. Set `SIMPLE_HACK_IMAGE`
   to use another release. Keep these values across restarts and upgrades.
4. Deploy. Check that `db` is healthy, `release` exits with code 0, and `app`
   becomes healthy. The one-shot `release` container can make Coolify's
   aggregate Service status show **exited** even while `app` and `db` are
   healthy. Check `https://<your-domain>/`, an event address, and a published
   team address in a browser. Each should have a trusted certificate.

The tested API path sent the exact file as base64 `docker_compose_raw` to
`POST /api/v1/services`, with `is_container_label_escape_enabled=false`, then
set the seven required environment values and started the Service. Coolify
normalizes Source Compose and generates a separate Deployable Compose. The
running container retained the TLS passthrough, PROXY protocol, HTTP routing,
and network labels. A Git-based Docker Compose **Application** with **Raw
Compose Deployment** is another supported Coolify path, but this package has
not been verified through that path or by clicking through the UI. [Coolify
Compose resource types](https://coolify.io/docs/applications/builds/docker-compose).

TLS passes through Traefik to the app's Caddy. PROXY protocol preserves visitor
addresses for rate limits and analytics. Postgres has no host port. The named
`db` and `data` volumes hold the database, team site files, and certificates.
Back up both before changing `SIMPLE_HACK_IMAGE` and restarting the Service.
The release step applies migrations before the app starts; a failed migration
prevents the new app from starting. A real v0.8.0-to-v0.8.1 upgrade was tested
separately with the standalone package; a version change through Coolify has
not yet been tested.

In the live Coolify test, the apex redirected from HTTP to HTTPS, and the apex,
event, and nested team addresses returned 200 with trusted Let's Encrypt TLS
1.3 certificates. The event and published team site survived a Coolify stop/start
and a separate same-image restart; the database still had 44 migrations. The
app reported v0.8.1 at commit `98f0f52`. Synthetic accounts and a fake mail
key kept this test free of outgoing email. The temporary 2 vCPU / 4 GB
DigitalOcean server existed for about 12 minutes, implying $0.0071 in prorated
compute at the API hourly rate; actual billing was not confirmed. It was deleted; its test SSH key and tag were also deleted and fresh API
checks confirmed absence.

A public one-click catalog listing is separate: Coolify currently requires
1,000 GitHub stars for a new service template. [Template eligibility](https://coolify.io/docs/contribute/service).
