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

**Address families.** An account can also connect `*.<its domain>` so every one of its sites
answers at `<site>.<its domain>` (see [Domains and certificates](domains-and-certificates.md)).
A site then has one main address: its custom domain or free name, else its most specific
family address (unless that family's `canonical` switch is off), else its own address. The
site's own address redirects to the main one, and sign-in and saves happen there; every family
address of a site keeps working. `ADDRESS_FAMILIES` turns the feature off; it also needs
`ADDRESS_FAMILY_CERT_DIR` and the family issuer, so it is off in practice on a small box.

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
| `SITE_BASE_DOMAIN` | `<SITE_DOMAIN>` | text | The domain people's and sites' addresses live under, when not SITE_DOMAIN itself. The app stays on SITE_DOMAIN. |
| `SITE_BASE_MOVE` | `off` | `off` / `serve` / `canonical` / `redirect` / `permanent` | How far addresses have moved from SITE_DOMAIN to SITE_BASE_DOMAIN: both answer (serve), the new ones are handed out (canonical), old ones redirect (redirect: 302, permanent: 301). |
| `SETUP_PASSWORD` | none | secret | The password a box in setup mode asks for. install.sh generates it. **Security-sensitive.** |
| `SETUP_PUBLIC_API` | `https://simple-host.app` | text | Where a box in setup mode claims a free hostname from. |
| `OPENAI_APPS_CHALLENGE` | none | text | The OpenAI plugin portal's domain-verification token. Unset: not served. |
<!-- /settings -->

## Reserved names

Some names read as the service, its operator or a sensitive function, so nobody new may take
them. They are refused, with "this name is reserved; pick another", to a new or changed
handle, to a newly claimed free address `<name>.<domain>`, and to an account's automatic
handle at sign-up (it gets `<name>-2` instead). Accounts and sites that already hold one keep it,
and it keeps working. The list lives in one place in the code (`reservedNewNames` in
`internal/handler/platformsubdomain.go`):

admin, administrator, root, sys, system, support, help, helpdesk, info, contact, hello,
security, abuse, postmaster, hostmaster, webmaster, noreply, no-reply, mail, email, smtp, www,
api, app, apps, status, billing, payments, pay, login, signin, sign-in, signup, sign-up, auth,
oauth, sso, account, accounts, dashboard, console, docs, doc, blog, cdn, static, assets, media,
files, download, downloads, setup, enterprise, legal, privacy, terms, report, policy, team, staff,
official, verify, verification, update, secure, simplehost, simple-host, simplehack,
simple-hack, test, dev, staging, prod, internal, localhost.

A site name only ever appears under its owner's address (`<site>.<handle>.<domain>`), so for
new sites only the names that pass for the service or its operator are refused: admin,
administrator, root, sys, system, support, helpdesk, security, abuse, postmaster, hostmaster,
webmaster, noreply, no-reply, billing, payments, login, signin, sign-in, signup, sign-up, auth,
oauth, sso, account, accounts, verify, verification, secure, official, simplehost, simple-host,
simplehack, simple-hack, internal, localhost. Everyday names such as blog, docs or team stay
free for sites.

The names the server itself routes (v1, internal, sites, skills, the platform's own hosts and
the ones nginx answers itself) were reserved before and stay refused everywhere.

## Recipes

**A small box for an event.** Point `hack.example.com` and `sites.hack.example.com` at the
server, then run the installer with `--host hack.example.com --content sites.hack.example.com`.
Nothing else is needed.

**Behind your own reverse proxy.** Set `BIND_ADDR=127.0.0.1` and `PORT=8090`, and have the proxy
send both hostnames to that port with the original `Host` header.

## Your home page

Your address opens your showcase by default. Choose a site in the owner app's
**Your address → Home page** control, or use connector `set_home_page` with
`{"site":"portfolio"}`. Owner REST clients use `GET/PUT /v1/me/home`;
`{"site":null}` restores the showcase. Deploy-only keys cannot change it.
The site's normal address still works. Your home has the same file serving,
404, passcode, sign-in and storage; its API and visitor sign-ins cover that site
only. Include `https://simple-host.app/auth.js`; `SH_CONFIG.site` may name the selected site.
Rename follows the choice. Offline, taken-down or suspended homes fall back to
the showcase; deleting the home clears the choice, including Recently deleted.
Both base domains work in their serving modes; hosted events are excluded.

Custom home pages can list projects live from
`GET https://simple-host.app/v1/u/{handle}/showcase.json`. This public feed has
`bio` and `sites[]` (`name`, optional `title`/`description`, `url`, `updated_at`,
`pinned`, `order`), and exactly the showcase visibility rules. Fetch without
credentials; it allows any origin, caches for 30 seconds and uses public-read
rate limits. The Website Deploy skill includes a small example.


The default showcase is yours to curate. In **Your showcase**, save a plain-text
bio, pin projects and give each public site an order number. Pinned projects come
first, then smaller order numbers; ties retain creation order. Unlisted sites
stay hidden even when pinned. The feed uses this same order and bio.

Owner REST: `GET/PUT /v1/me/bio` with `{"bio":"I make things"}` (empty clears),
and `GET/PUT /v1/sites/{sitename}/showcase` with `{"pinned":true,"order":10}`;
you can send either field alone. Orders range from 0 to 1000000. Connector tools
are `set_bio` and `set_showcase_site`. Deploy-only keys cannot curate. The bio limit
is `SHOWCASE_BIO_MAX_LENGTH` (280 characters by default, configurable from 1 to 2000).

An account's own apex domain for its whole space is not available yet. The
current family scripts handle site labels and an already supplied wildcard
certificate; account-home apex routing and apex-plus-wildcard certificate
provisioning need further proxy/issuer work. Per-site custom domains and address
families remain available.

## Your home page

Your public person address opens your showcase, or an owned site you choose.
In the owner dashboard, use **Your address → Home page** and **Your showcase**
for a plain-text bio, pins and order. Connector tools are `set_home_page`,
`set_bio`, `set_showcase_site`; owner REST uses `GET/PUT /v1/me/home`,
`GET/PUT /v1/me/bio`, `GET/PUT /v1/sites/{name}/showcase`.
`{"site":null}` restores the showcase. Rename follows the site; deletion clears
it, and offline, suspended or taken-down homes fall back to the showcase.
Pinned sites come first, then smaller order numbers, then newest creation time.
The bio limit is `SHOWCASE_BIO_MAX_LENGTH` (280 characters; range 1–2000).

A custom home can fetch `GET /v1/u/{handle}/showcase.json` from the API origin
without credentials. Its `handle`, `bio` and `sites` include each visible site's
`name`, `url`, `title`, `description`, `created_at`, `updated_at`, `pinned` and
`order`. The feed uses the public showcase filter, CORS without credentials and
a 30-second cache. Unlisted, offline, passcode-protected and taken-down sites
stay hidden.

On hosted Simple Host, the selected site serves at the person-host root and
keeps its normal URL; sign-in and data on that origin cover the selected site
only. On a small-box install, including Droplet 1-Click and Coolify, the default
path model remains: `https://sites.<domain>/<handle>` opens the selected site's
normal URL. The owner dashboard remains `https://<domain>/<handle>`.
No wildcard DNS or extra certificate is required for path-model home selection.
Person-host installations keep the hosted behavior when configured. A domain
for a whole personal space has not shipped; existing per-site domains remain.
Simple Hack accounts own no personal sites, so this feature does not apply.
