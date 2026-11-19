# Page routing and database reads

Pages are served by the one program. Its address lookups go through
`postgresFacts`, which delegates to the existing services with the same query
order.

## Page address resolution

`host/pagepath.Resolve` takes one published version and a decoded path inside it. It answers with the file to serve, a redirect that adds or removes the trailing slash, or a miss. A file page is served at `/faq` and a folder index at `/faq/`. The other spelling redirects there. When both exist, each address serves its own page. An exact filename takes priority over an HTML sibling. The root serves `index.html` and the root `_redirects` file is never a page. A redirect always names an address that serves a file.

Every program path that serves or reads a page asks this one function:

- `serveRootFile` serves site hosts, person paths, selected homes, claimed names, family hosts, custom domains and previews. It resolves and opens on the same confined version root. A redirect is the escaped public path with its trailing slash added or removed, so the mount, the visitor's spelling and the query string are kept. It sets revalidation headers unless a gate already set a private policy.
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

## Recorded behavior

There is **no request-time site-expiry read** today. The expired fixture still
serves until the expiry sweep removes it. Unlisted is a profile-listing choice:
`markRestrictedSite` does not set a special cache or indexing header for it.
An offline or taken-down selected home falls back to the person page.
