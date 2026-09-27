# Server and addresses

A Simple Host server needs one domain it owns, like `hack.example.com`. The dashboard, the API
and the connector live there; sites are served from a second hostname, `sites.<domain>`, so a
page someone publishes can never script the dashboard. On a small box, Caddy fetches a
certificate for each name the first time it is asked for, once the server confirms the name is
its own.

**Setup mode.** A box installed without `--host` starts in setup mode: it serves one page that
asks for the setup password (printed once by the installer) and then where the box lives.

**Per-person and per-site addresses.** simple-host.app gives each account its own address,
`<handle>.<domain>`, and each site its own origin, `<site>.<handle>.<domain>`. That needs a
wildcard certificate per person, which a small box does not have, so both are `off` there and
sites live at `sites.<domain>/<handle>/<site>/`. `serve` answers on the new addresses without
handing them out; `canonical` hands them out and redirects the old ones.

<!-- settings:group=server -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `SITE_DOMAIN` | none | text | The domain this server lives at, like hack.example.com. Unset, the server starts in setup mode and asks for one. |
| `CONTENT_HOST` | `sites.<SITE_DOMAIN>` | text | The separate hostname sites are served from, so pages never share an origin with the dashboard. |
| `PUBLIC_BASE_URL` | `https://simple-host.app` | text | The server's own address, used in emails and sign-in redirects. install.sh sets it to https://<SITE_DOMAIN>. |
| `PORT` | `8090` | 1–65535 | The port the server listens on. |
| `BIND_ADDR` | none | text | The interface the server listens on, like 127.0.0.1 behind a proxy. Empty listens on all. |
| `DEPLOY_SCRIPT` | none | text | A script run after each site goes live. Empty runs nothing. |
| `PERSON_HOSTS` | `off` | `off` / `serve` / `canonical` | Each account at its own address, <handle>.<SITE_DOMAIN>. canonical makes it the address handed out. |
| `SITE_HOSTS` | `off` | `off` / `serve` / `canonical` | Each site at its own address, <site>.<handle>.<SITE_DOMAIN>. Needs PERSON_HOSTS. |
| `SETUP_PASSWORD` | none | secret | The password a box in setup mode asks for. install.sh generates it. **Security-sensitive.** |
| `SETUP_PUBLIC_API` | `https://simple-host.app` | text | Where a box in setup mode claims a free hostname from. |
| `OPENAI_APPS_CHALLENGE` | none | text | The OpenAI plugin portal's domain-verification token. Unset: not served. |
<!-- /settings -->

## Recipes

**A small box for an event.** Point `hack.example.com` and `sites.hack.example.com` at the
server, then run the installer with `--host hack.example.com --content sites.hack.example.com`.
Nothing else is needed.

**Behind your own reverse proxy.** Set `BIND_ADDR=127.0.0.1` and `PORT=8090`, and have the proxy
send both hostnames to that port with the original `Host` header.
