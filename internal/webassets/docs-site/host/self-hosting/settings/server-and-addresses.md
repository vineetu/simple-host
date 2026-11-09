# Server and addresses

A Simple Host server needs one domain it owns, like `example.com`. The dashboard, the API
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

**Hosted address decision (2026-10-09).** simple-host.app is the only address.
The alternate site base is paused; `SITE_BASE_MOVE=off` and a retained distinct
`SITE_BASE_DOMAIN` 301 old person/site/free-name links to matching app hosts with
path and query kept. The base certificate directory is unset and the .site issuer
stays disabled. See [the operations runbook](/docs/self-hosting/operations/site-base-paused.md).
Revisit about 2026-11-09.

**Address families.** An account can also connect `*.<its domain>` so every one of its sites
answers at `<site>.<its domain>` (see [Domains and certificates](/docs/self-hosting/settings/domains-and-certificates.md)).
A site then has one main address: its custom domain or free name, else its most specific
family address (unless that family's `canonical` switch is off), else its own address. The
site's own address redirects to the main one, and sign-in and saves happen there; every family
address of a site keeps working. `ADDRESS_FAMILIES` turns the feature off; it also needs
`ADDRESS_FAMILY_CERT_DIR` and the family issuer, so it is off in practice on a small box.

<!-- settings:group=server -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `PRIVACY_URL` | none | URL | This install's own privacy policy, linked from the footer instead of ours. Unset: the hosted service keeps its own; a self-hosted install hides the link. |
| `SITE_DOMAIN` | none | text | The domain this server lives at, like example.com. Unset, the server starts in setup mode and asks for one. |
| `CONTENT_HOST` | `sites.<SITE_DOMAIN>` | text | The separate hostname sites are served from, so pages never share an origin with the dashboard. |
| `PUBLIC_BASE_URL` | `https://simple-host.app` | text | The server's own address, used in emails and sign-in redirects. install.sh sets it to https://&lt;SITE_DOMAIN>. |
| `PORT` | `8090` | 1–65535 | The port the server listens on. |
| `BIND_ADDR` | none | text | The interface the server listens on, like 127.0.0.1 behind a proxy. Empty listens on all. |
| `TRUSTED_PROXY_CIDRS` | none | text | The networks the proxies in front of this server connect from, comma-separated (10.0.0.0/8,fd00::/8). Set, X-Forwarded-For is believed only from a peer in one of them and read right to left past every trusted hop, so a client cannot choose its own address for rate limits. Empty believes the last entry from any peer, which is right behind one local nginx. **Security-sensitive.** |
| `DEPLOY_SCRIPT` | none | text | A script run after each site goes live. Empty runs nothing. |
| `PERSON_HOSTS` | `off` | `off` / `serve` / `canonical` | Each account at its own address, &lt;handle>.&lt;SITE_DOMAIN>. canonical makes it the address handed out. |
| `SITE_HOSTS` | `off` | `off` / `serve` / `canonical` | Each site at its own address, &lt;site>.&lt;handle>.&lt;SITE_DOMAIN>. Needs PERSON_HOSTS. |
| `SITE_BASE_DOMAIN` | `<SITE_DOMAIN>` | text | Optional alternate base for person/site/free-name addresses. The app stays on SITE_DOMAIN. With SITE_BASE_MOVE=off, retain this only to redirect old base links back to SITE_DOMAIN. |
| `SITE_BASE_MOVE` | `off` | `off` / `serve` / `canonical` / `redirect` / `permanent` | off: only SITE_DOMAIN serves; a distinct configured base 301s back with path/query kept. serve: both answer; canonical: base addresses handed out; redirect/permanent: old SITE_DOMAIN addresses redirect to the base (302/301). |
| `SETUP_PASSWORD` | none | secret | The password a box in setup mode asks for. install.sh generates it. **Security-sensitive.** |
| `SETUP_PUBLIC_API` | `https://simple-host.app` | text | Where a box in setup mode claims a free hostname from. |
| `MCP_APP_PASSCODE` | `off` | text | Passcode screen in compatible MCP Apps clients. off, on, or accounts:alice,bob. Personal Simple Host callers only. |
| `MCP_PASSCODE_SCREEN_TTL_SECONDS` | `600` | at least 1 seconds | Lifetime of a single-use sealed passcode screen session. Positive integer; invalid values use the default. |
| `MCP_APP_PHOTO_PICKER` | `off` | text | Photo picker in compatible MCP Apps clients. off, on, or accounts:alice,bob. Simple Host callers only. |
| `MCP_APP_MY_SITES` | `off` | text | My sites in compatible MCP Apps clients. off, on, or accounts:alice,bob. Simple Host callers only. |
| `MCP_APP_DASHBOARD` | `off` | text | Weekly overview inside My sites. Requires MCP_APP_MY_SITES too. off, on, or accounts:alice,bob. Simple Host callers only. |
| `MCP_APP_SITE_PREVIEW` | `off` | text | Optional site preview card. off, on, or accounts:alice,bob. Requires a client and site headers that permit framing; leave off otherwise. Simple Host callers only. |
| `BRAIN_DIR` | none | text | Folder containing bundle.json for the owner product-knowledge server; empty disables it, and hosted events ignore it. |
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
official, verify, verification, update, secure, simplehost, simple-host, test, dev, staging, prod, internal, localhost, photos, photo-codecs.

A site name only ever appears under its owner's address (`<site>.<handle>.<domain>`), so for
new sites only the names that pass for the service or its operator are refused: admin,
administrator, root, sys, system, support, helpdesk, security, abuse, postmaster, hostmaster,
webmaster, noreply, no-reply, billing, payments, login, signin, sign-in, signup, sign-up, auth,
oauth, sso, account, accounts, verify, verification, secure, official, simplehost, simple-host,
internal, localhost. Everyday names such as blog, docs or team stay
free for sites.

The names the server itself routes (v1, internal, sites, skills, the platform's own hosts and
the ones nginx answers itself) were reserved before and stay refused everywhere.

## Recipes

**A small box for an event.** Point `example.com` and `sites.example.com` at the
server, then run the installer with `--host example.com --content sites.example.com`.
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
a 30-second cache. Unlisted, offline, passcode-protected, named-viewers-only and taken-down sites
stay hidden.

On hosted Simple Host, the selected site serves at the person-host root and keeps its normal URL; sign-in and data on that origin cover the selected site only. On a small-box install, including Droplet 1-Click and Coolify, the default path model remains: `https://sites.<domain>/<handle>` opens the selected site's normal URL. The owner dashboard remains `https://<domain>/<handle>`. No wildcard DNS or extra certificate is required for path-model home selection. Person-host installations keep the hosted behavior when configured. A domain for a whole personal space has not shipped; existing per-site domains remain.

## MCP Apps in installed copies

The original four `MCP_APP_*` flags are off by default in every package. Set `on` or `accounts:alice,bob` in the installation's `.env` or Coolify environment settings, then recreate the app container. Dashboard also needs My sites. Leave site preview off unless both the client and the site permit framing. Configured instance hosts determine its frame origins; this setting does not change site framing headers.

The Passcode screen is a separate opt-in: pass `MCP_APP_PASSCODE` to the server
process as `on` or `accounts:alice,bob`. It defaults off and is personal Simple Host
only. `MCP_PASSCODE_SCREEN_TTL_SECONDS` sets its single-use session lifetime (600
seconds by default). Installer forwarding for these two settings is not included.

`MCP_APP_MY_SITES` and `MCP_APP_DASHBOARD` are read from the server process environment on each request. Both default to `off`; `on` enables all eligible connections, and `accounts:alice,bob` limits them to authenticated handles. Dashboard requires My sites.
