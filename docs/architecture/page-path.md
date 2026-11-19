# Page routing and database reads

D4-B1 preserves the single-process page path. The production server uses
`postgresFacts`, which delegates to the existing services with the same query
order. `copyFacts` is also used by the optional `static` subcommand. Neither normal program startup nor the deployment recipes start Static.

## Page address resolution

`host/pagepath.Resolve` takes one published version and a decoded path inside it. It answers with the file to serve, a redirect that adds or removes the trailing slash, or a miss. A file page is served at `/faq` and a folder index at `/faq/`. The other spelling redirects there. When both exist, each address serves its own page. An exact filename takes priority over an HTML sibling. The root serves `index.html` and the root `_redirects` file is never a page. A redirect always names an address that serves a file.

Every program path that serves or reads a page asks this one function:

- `serveRootFile` serves site hosts, person paths, selected homes, claimed names, family hosts, custom domains, previews and Static. It resolves and opens on the same confined version root. A redirect is the escaped public path with its trailing slash added or removed, so the mount, the visitor's spelling and the query string are kept. It sets revalidation headers unless a gate already set a private policy.
- The selected-home check (`homePathExists`) uses it to decide whether the home has a page before a sibling site is tried.
- `visitor_read_site` reads the file it names. It re-confirms access and opens the file under the site's lock, so a passcode change, rename, take-down or publish cannot land between the check and the read.

Access gates (take-down, offline, passcode, named viewers) answer before any of this.

`pagepath.Taken` answers a second question from the same lookup: is something already at this path, either a page at either spelling or a folder. A site's own redirect rule that is not forced stands aside there. Serving (`applyRootRedirect`), the old-address check on site hosts (`siteHasPath`) and the publish-time rule check (`validateRedirects`, over the files being published) all use it.

Rules are decided first, for the path as requested. The slash redirect happens only afterwards, where the file lookup would otherwise serve or miss. Rule matching ignores trailing slashes, so the address the slash redirect names gets the same rule decision as the request did, and it holds a page, so it serves. So a slash redirect is always the last hop and cannot be part of a loop. The publish-time loop check does not model it.

## Which front door answers

| Address | Normal file path | Program path measured |
|---|---|---|
| Person host with selected home or site path | Program | `PersonHosts`, `serveHomePage`, `servePersonHost` |
| Site host or flat group-site host | Program | `SiteHosts`; flat labels first rule out an exact account handle |
| Claimed platform name, current or previous | Program | `BoundSubdomains`; current binding wins over previous |
| Ordinary custom domain, current or previous | Web server | Direct Go request is also recorded, but is not the normal file path; nginx/Caddy serve the domain link and hand off gates/API |
| Retired platform name | Program | `LegacyHostRedirect`; current address or removed notice |
| Renamed site's old name or handle alias | Program | Current name first, then alias redirect |
| Address-family host | Web server for ordinary files; program for API/gates/misses | `FamilyHosts` direct response recorded; exact custom domain wins over the family |
| Content host `/<handle>/<site>/` | Web server | Raw Go response recorded separately from `/internal/site-redirect/<handle>/<site>/` and `/internal/notfound` with `X-Original-URI` |
| Unknown host | Program fallback | Fixture app's 404 |
| `/internal/tls-ask` | Program | Host allowlist, product host lookup, then custom-domain lookup; failures deny |

The content path has no `/u/` prefix. A retained previous custom domain is
still served while its replacement is pending; an old claimed name that was
released redirects through `legacy_hostnames`. The route tests in `host/handler` cover those
cases and the custom-domain, family and content-host gate hand-offs. They do not exercise a running nginx, Caddy or certificate issuer.

## Read inventory and classification

The statement numbers below come from the numbered SQL list of the D4-S1a recording (`pagepath-matrix.txt`), which X4-S1 removed together with its test once the page-address tests covered each address. A query
returning mixed columns is classified by how its consumer uses each field.
Unused columns from the original service queries do not enter the snapshot.

| Statements / reader | Class | What is retained or checked |
|---|---|---|
| 1, 2, 19: custom-domain queries | Routing | Site/owner/name, current and previous address, verification and status; current before previous, exact host before alternate base |
| 3, 22: owner by handle and handle alias | Routing + response | Owner ID and handle only, including validated content-host miss lookup and branded 404 owner wording; no account credential or admin authority |
| 4: home-site selection | Routing | Selected site ID |
| 5, 18: site by ID or owner/name | Routing + response | Folder identity and active version; response-only noindex bit and home-page fallback bit |
| 20: site owner | Routing | Owner handle, owner ID and current site name; deleted rows excluded |
| 21: renamed-site lookup | Routing | Old name to current live site; owner must still match |
| 23, 24, 25: legacy names | Routing | Retired name's site, names retained by deleted sites, oldest live owner's handle |
| 26: `ListAllFamilies`, loaded by `refreshFamilies` | Routing | Prefix, suffix, ranking, canonical flag, verification and certificate readiness inputs; the issuer's ready file is checked on disk |
| `GroupHosts.BySlug` and `HasSite`, from TLS lookup | Routing | Group ID/slug/holding account and group/site names; the optional `GroupFactsLoader` runs inside the full load's transaction |
| 6: `GetSitePasscodeByName` | Protection | Fresh sealed passcode, generation, named-viewer mode and offline state; never copied |
| 7: visitor session, including live SSO parent/key | Protection | Fresh session for viewer gate and owner frame guard; never copied |
| 8–11: suspended viewer, account/email, signup mode, viewer membership | Protection | Fresh named-viewer checks; never copied |
| Owner comparison after 7 | Protection | Host compares the fresh visitor ID with folder owner; product owner checks stay behind `BindingProvider.OwnerOnSite` and are never cached |
| 12: session/SSO touch | Protection side effect | Existing gate activity update, not a routing read; unchanged |
| 13: access-request notification subscriber lookup | Protection side effect | Existing asynchronous notification path, joined by the fixture; unchanged |
| 14–16: public-site list, profile bio and showcase order | Response, API hand-over | Person-page fallback; showcase rendering is outside Static's page-copy contract (rule 9), and stays in the API |
| 17: clear a deleted selected home | Routing repair | Existing `HomeSite` side effect in the PostgreSQL implementation; the read-only copy does not repair the database |

There is **no request-time site-expiry read** today. The expired fixture still
serves until the expiry sweep removes it. Unlisted is a profile-listing choice:
`markRestrictedSite` does not set a special cache or indexing header for it.
An offline or taken-down selected home falls back to the person page. These are
recorded behavior, not changes made by this slice.

`PageNoIndex` projects `markRestrictedSite`'s output. `HomeUnavailable` projects
the selected-home fallback. They cannot admit a visitor: after any folder is
selected, its disk take-down/offline/access markers and fresh access checks still
run. The copy has no sealed passcode, passcode generation, viewer list, session,
owner-membership verdict or separate cached protection flags. A stale row for a
missing folder yields the existing disk 404.

## The dormant routing copy

`LoadSiteTable` uses one `REPEATABLE READ, READ ONLY` transaction, records its
transaction start timestamp, and closes each result set before starting the
next query. The optional product loader reads only product address facts inside
that same transaction. It does not alter the existing `GroupHosts` contract.
A single reloader attempts a full load every ten seconds by default, waits after
a slow attempt, publishes only changed successful loads, and logs one failure
per streak. Every successful full load renews the saved timestamp, even when
the table is unchanged; failed saves are retried on the next successful load.
This keeps full-load freshness separate from routing-table publication and
persistence failures. The connection pool owns reconnects. There is no LISTEN, NOTIFY,
trigger or migration.

A missing address can use `Reloader.Lookup`: one context-bounded 300 ms leaf
lookup, returning owner/site routing and response facts and, for a previously
unknown family hostname, its family routing facts. Current custom domains win
over previous custom domains, then family addresses. The handler retains family
label validation and the issuer's on-disk readiness check. The HTTP boundary
in D4-S4a must call it once on a miss and re-resolve using the resulting copy.
It must map no-copy to 503 with Retry-After, and an unreachable missing address
to 404. Existing-address lookups do not consult PostgreSQL for routing. Targeted
lookups do not extend the full snapshot's age. The API-change fixture exercises
publish, rename, delete, claim and release through the API, immediate targeted
lookups, and the result after one full refresh. A newly live family also resolves
before the next full load. Product TLS misses use one product-owned targeted
read, including a new group with no website; group and team address facts are
merged into the copy alongside the platform address facts.

`routing.json` under the supplied state directory contains format version 1,
product, install identity, transaction start time and SHA-256 of its JSON body.
Writes use a temporary file, file sync, rename and directory sync. Loading rejects
malformed files, bad checksums, unknown versions, product/identity mismatches and
files older than seven days before exposing a table. Identity is read only from
`<DATA_DIR>/.install-id`, owned by the earlier key-value move. Missing or unreadable
identity means no saved copy is read or written; a successful database load can
still provide the first in-memory copy. No API startup code writes identity here.

## Optional Static process

`simple-host static` (or `simple-hack static` for the product's routing facts)
starts a separate listener. `STATIC_BIND` defaults to `127.0.0.1`, `STATIC_PORT`
to `8091`, `STATIC_API_URL` to `http://127.0.0.1:8090`, and
`STATIC_REFRESH_SECONDS` to ten seconds. `STATIC_STATE_DIR` defaults to
`static-state` beside `DATA_DIR`. Use the API's address settings, database and
existing data directory. A valid saved copy lets public pages start with no
database; without any copy, page requests return 503 with Retry-After. A missing
address gets one 300 ms lookup and never a guessed folder.

Static opens the existing data directory without creating it or cleaning old
markers. It takes the existing `.maintenance.lock` shared and read-only: the API
or installer must have created that file first. It never creates `.install-id`.
Only its routing snapshot is written; no workers, metrics collector, migrations,
product Wire function or user-data writes run. A product contributes only its
read-only routing loader through `Extension.LoadRoutingFacts`.

The existing page handlers serve public and unlisted pages and the front's
`/internal/*` page hand-offs. Static checks the opened site directory for take-down, offline and
access markers in that order before serving a file from it. Any access marker,
regardless of its contents, sends the request to the API with the original host,
path, cookies and method. The API supplies the status, body, framing and cache
headers for both passcode and named-viewer sites. During an API outage these
sites return the service-error page (503), even to visitors with a valid cookie.
Static waits up to five seconds for the API’s response headers on a protected page.
Static serves unmarked public and unlisted pages from the routing copy. If Simple
Host cannot look up a visitor session, or Simple Hack needs the product’s owner
check, the API supplies the page and its framing headers. If the API cannot be
reached, times out or returns a server error, Static checks the same directory’s markers again and
serves the unmarked page with `X-Frame-Options: SAMEORIGIN` and
`Content-Security-Policy: frame-ancestors 'self'`. Marker lookup errors return
the service-error page. Take-down and offline markers take precedence even
when the request carries a visitor cookie.
Serving passcode sites in Static is deferred to later work tracked in Linear.

API routes, sign-in, previews,
showcases and person roots without a home also go to the API. An unavailable API
answers 503. Requests and responses stream, including uploads. Original Host is
preserved, X-Forwarded-Host is removed, and forwarding uses the existing trusted
client-address rule. Forwarded protocol is accepted only from a configured
trusted front; otherwise Static uses its incoming transport scheme. The API
must trust Static's address in `TRUSTED_PROXY_CIDRS`.

SIGTERM stops acceptance, drains HTTP for at most ten seconds, cancels and joins
the reloader, and exits successfully. Static remains off in all normal startup
and deployment paths; switching the production front door requires its later
approval.
