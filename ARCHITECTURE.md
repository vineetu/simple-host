# Architecture

Orientation for people changing this code. It answers two questions: *where is
the thing that does X*, and *what does the thing I'm looking at do*.

It is deliberately coarse. It names files and packages but does not link them,
and it does not list every function — those go stale, and a stale map is worse
than none. Anything that must stay true is enforced by a check in `make check`,
not by this document. Read `FEATURES` (served at `/features`) for what the
product does; this file is only about where it lives.

## Bird's eye view

One Go binary serves every site's static files and the REST API. Postgres holds
users, sites, versions and aggregates. A folder on disk holds versioned site
files. nginx terminates TLS and maps hostnames to the binary.

A request is either **content** (someone visiting a hosted site) or **API**
(an owner or their agent calling `/v1/...`). Content is served from disk by
path; API calls are authenticated, hit Postgres, and return JSON.

## Codemap

### `cmd/server`

`main.go` is the wiring: config, database handle, middleware chain, background
loops.

Routes are registered across `main.go` and about nine files in
`internal/handler`, each next to the code it serves. To find what handles a URL,
grep for `mux.Handle`, or look it up in `openapi.yaml`, which is the contract and
is checked against the registered routes.

### `internal/handler`

Every HTTP handler, plus the entire web UI. The largest package by far, and the
one most likely to be where your change belongs.

Some landmarks, not an inventory. `site.go` is deploy, versions and rollback.
`ui.go` decides which static page a bare URL gets. `stateops.go` and
`collections.go` are the per-site datastore.
`visitorsession.go` serves Origin-gated `GET .../me` for site sessions; hosted
`static/auth.js` exposes `window.SH` for visitor sign-in, status, state and collections. `generate.go` is Create-with-AI.
`analytics.go` and `apimetrics.go` are two unrelated things both called
"analytics" — the first is per-site visitor traffic, the second is per-endpoint
API call counts.

`static/` holds the web UI as hand-written HTML with inline CSS and JS, embedded
into the binary. `index.html` is the owner dashboard; `showcase.html` is the
public profile *and* the owner's Analytics tab. **Both render analytics, and
both are served on two different origins.** That pair has already caused one
production bug (see Traps).

### `internal/db`

The SQL behind the request path, one file per area, plus `models.go` for the row
types. No ORM — queries are written out, and the schema lives in `db/schema.sql`.
The analytics ingester is the one exception: it writes its aggregate tables
directly, because it runs off the request path entirely.

### `internal/analytics`

Reads the nginx access log and turns it into per-hour, per-class aggregates.
`ingest.go` tails the log by inode and byte offset; `classify.go` decides whether
a request was a person, a bot, or our own monitoring. Nothing here is on the
request path — it is a background loop.

### `internal/storage`

`disk.go` — versioned site files on disk. The only package that writes site
content.

### The leaves

`internal/config` (environment), `internal/auth` (API key middleware),
`internal/tarball` (extract, validate, sanitize uploads), `internal/email`
(magic links), `internal/oauth` (Google, GitHub).

### Outside the Go tree

`db/schema.sql` is the canonical schema; `db/migrations/` is history, applied by
hand. `deploy/prod/` is the nginx configuration. `simple-host-website/` is the
agent-facing plugin: the skills a coding agent installs to deploy to this
service. `scripts/` holds the checks.

## Boundaries

**`internal/handler` is the only package that imports other internal packages.**
Everything else is a leaf, except `internal/auth`, which reaches `internal/db`.
Enforced by `scripts/check-layering.sh`.

This is the property worth protecting. It means any leaf can be read and changed
without understanding the rest of the system, and it is the reason a package
here is cheap to replace.

**The API contract lives in `openapi.yaml`,** not in the handlers. `openapi.json`
is generated from it and must never be hand-edited.

## Invariants

Mostly stated as absences, because those are the things you cannot infer by
reading code:

- **No build step for the frontend.** No bundler, no npm, no framework. The HTML
  is written by hand and embedded. This is why the pages are large, and it is a
  deliberate trade for "one binary, no build farm".
- **No fallback model provider.** Create-with-AI talks to one Grok sidecar. If it
  is down the feature fails honestly rather than silently substituting.
- **No client-side analytics.** Nothing is injected into hosted pages — no
  beacon, no cookie, no script. Traffic is derived entirely from the server's own
  access log. Adding a script tag would break the promise the product makes.
- **No object store, no CDN, no queue.** Files are on local disk.
- **Raw visitor IPs are never stored.** Only a salted hash. The salt is stable,
  not per-day, because a rotating salt makes counting unique visitors over a
  range impossible.
- **`site_view_daily` and `site_visitor_daily` are never written to, and must
  never be dropped.** They are the only surviving record of traffic from before
  classification existed, and are served as the `unknown` class.

## Traps

Things that have already gone wrong, or are one edit away from going wrong.

**One payload, two pages.** `index.html` and `showcase.html` both render
analytics. The response shape changed, one page was updated, the other silently
rendered `0` for every site — no error, because a missing field read as
`undefined` and got coerced to zero. Any change to an API response shape must be
checked against *both* pages. This is why the analytics parser is shared and
strict.

**Two origins.** `showcase.html` is served from the apex *and* from the content
host. A `<script src="/...">` resolves on one and 404s on the other, so shared
frontend code is inlined rather than linked.

**The log tail is stateful.** The ingester tracks position by inode and byte
offset, so logrotate must use `create` (not `copytruncate`), and must
`delaycompress` — the ingester reads the previous file as plain text and cannot
read gzip.

**The compiled binary is not the repo.** Editing source changes nothing until
rebuild and restart. The production host is `aarch64`.

## Checks

`make check` runs all of them, and they are the actual guarantee this document
is not:

- `scripts/check-layering.sh` — the boundary above
- `scripts/check-docs-sync.sh` — routes match `openapi.yaml`, `openapi.json`
  matches `openapi.yaml`, and both pages carry the analytics parser verbatim
- `scripts/check-fresh-install.sh` — `db/schema.sql` alone can run the product

`make check` runs all of them. If you add a check, add it there — this list is
prose and will rot; the Makefile is what executes.

Revisit this file when a check changes or a package appears. Not otherwise.

2026-09-05: shared email-code helpers serve dashboard and site-scoped visitor sign-in; visitors use the same accounts, and any valid account API key may write state/collections as that account.
