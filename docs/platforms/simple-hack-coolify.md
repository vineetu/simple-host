# Full Simple Hack on Coolify

**Published package; not yet verified on a live Coolify server.** The existing
[small-box Coolify guide](coolify.md) describes a different installation. This
package runs the complete event platform and uses your own apex, event and
team addresses. Its default `ghcr.io/vineetu/simple-hack:0.8.1` image is published.

1. Prepare the domain, verified email sender and certificate capacity described
   in [the standalone guide](simple-hack-standalone.md). Point apex and wildcard
   DNS at the Coolify server.
2. Use Coolify with its **Traefik v3** proxy, entrypoints named `http` and
   `https`, and external Docker network `coolify`. The bundled configuration
   is not a Caddy-proxy template. If the proxy has a TLS challenge resolver,
   enable `allowACMEByPass` on its HTTPS entrypoint so the application's Caddy
   can answer TLS-ALPN certificate challenges. Confirm this setting with the
   installed Traefik version; HTTP challenge routing may also be intercepted
   by a proxy's own ACME resolver. [Traefik entrypoints](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/).
3. Create a Git-based Docker Compose application from this repository. Use
   `deploy/hack/standalone/coolify/compose.yaml` and enable **Raw Compose
   Deployment**. Leave generated Domains fields empty: the file supplies
   routing for all three hostname shapes. [Coolify Raw Compose](https://coolify.io/docs/applications/builds/docker-compose).
4. Supply `SITE_DOMAIN`, `RESEND_API_KEY`, `MAIL_FROM`, `DB_PASSWORD`,
   `ADMIN_API_KEY`, `PASSCODE_ENC_KEY`, and `ANALYTICS_SALT` as persistent
   environment values. `PASSCODE_ENC_KEY` is 32 random bytes in standard
   base64; the other secrets can be random hex. Generate them with the
   standalone installer's `--prepare-only` option and copy the private values
   into Coolify. Raw Compose does not promise to generate them for you.
   Set `SIMPLE_HACK_IMAGE` when using a different release.
5. Deploy. The release container initializes Postgres and applies migrations
   before the app starts. Sign in, create an event and publish a team project;
   check both addresses over trusted HTTPS.

TLS passes through Traefik to the app's Caddy. PROXY protocol preserves visitor
addresses for rate limits and analytics. Postgres has no host port. Application
ports stay on Docker networks. The two named volumes persist database, project
files and certificates. Back them up before changing `SIMPLE_HACK_IMAGE` and
redeploying. A failed migration keeps the new app from starting.

The local routing test is useful evidence, but does not substitute for deploying
through Coolify's actual UI/API and testing public certificates. A public
one-click catalog listing is separate: Coolify currently requires 1,000 GitHub
stars for a new service template. [Template eligibility](https://coolify.io/docs/contribute/service).

Local verification on 2026-10-01 used Traefik 3.6.7 with the package's real
labels, TLS passthrough and PROXY protocol. An existing event and published
project continued serving through that proxy using a local CA. This is not a
claim that Coolify itself or public ACME issuance has been tested for this package.
