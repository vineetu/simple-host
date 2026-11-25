# simple-host-next

The new Simple Host: module `simplehost`, binary `cmd/simple-host-next`, spec RW2/SPEC.md.

## Imports
```go
"simplehost/app"                   // app.App: the running service a feature mounts on
"simplehost/core/web"              // routes, caller, refusals, JSON
"simplehost/core/db"               // db.Event, db.Q
"simplehost/core/limits"           // the one limits table
access "simplehost/access/core"    // people, names, credentials
sites "simplehost/sites/core"      // sites, addresses, the visit decision, versions
data "simplehost/data/core"        // namespaces (SQLite), rules, Turnstile
"simplehost/data/core/rules"       // the rules engine (copied), for plan.Using / plan.Check
```

A feature never imports a sibling feature. Shared things live in a `core`. All 30 tables
exist, in `{access,sites,data}/core/schema.sql` and `core/db/schema.sql` (ops). Add columns there.

## Mounting routes (the one pattern)

Each feature package has `func Mount(a *app.App)`. `cmd/simple-host-next/main.go` calls it
(one line in `mount`); a worker is one line in `work`: `go notify.Work(ctx, a)`.

```go
func Mount(a *app.App) {
	h := &handler{a}
	a.Routes.Platform.Handle("POST /v1/keys", web.Func(h.makeKey))       // simple-host.app
	a.Routes.Site.Handle("POST /_sh/passcode", web.Func(h.unlock))       // every site address
	a.Routes.Page.Handle("GET /{$}", web.Func(h.publicPage))             // <handle>.simple-host.app|site
}
func (h *handler) makeKey(w http.ResponseWriter, r *http.Request) error { ... } // see Refusals
```

Before a route runs, the host's site or public page is found (unknown host: "not found"), the
caller resolved, and the request counted against `limits.Requests`.

Caching (one rule, `cacheRule` in `app`, applied to every answer on a site address or public page):
a page or file of an open site, or a public page, gets `Cache-Control: public, max-age=60`, an hour at
Cloudflare and `Cache-Tag: site:<id>` / `owner:<id>`; everything else gets `private, no-store`,
except the page helper `/_sh/page.js` (public, 5 minutes in browsers, an hour at the edge, no tag).

Forged requests (one rule, `web.FromOwnPage`, applied in `access.Caller`): a browser cookie
(dashboard or site visitor) counts on anything but GET/HEAD only with `X-SH-CSRF` or a same-origin
`Origin`, and never with an `Origin`/`Sec-Fetch-Site` naming another address; `/mcp` never reads a cookie. Data routes that pages call
on their origin and the AI calls on the platform are mounted on both `Site` and `Platform`.

## Core functions, by package

A feature calls only these (and its own code). Each is listed once.

### app
```go
a.Config; a.DB; a.People; a.Sites; a.Data; a.Mail; a.Routes  // the running service
a.HTTP                            // outside calls; a.Guarded: addresses a browser gave (public IPs only)
a.Countries.Of(ip)                // ISO country code, "" when unknown (SH_COUNTRY_FILE): admin map and site visitors
a.ServeSite(w, r, site)           // answer with a site's routes, as on its own address
app.FromEnv(); app.New(ctx, cfg)  // config from the environment; the service on it
```

### core/web
```go
c := web.CallerOf(r)    // c.ID == "" : nobody. c.Visitor(): signed in on this one site only.
                        // c.Via: "key", "app", "session" (dashboard) or "site". c.Operator.
web.WithCaller(r, c); web.Func(fn); web.NewRoutes()
return web.Refuse(status, "code", "One plain sentence saying how to fix it.") // written as {"code", "fix"}
return web.NotFound     // anything missing or hidden; any other error: logged, "server_error"
web.Fail(w, r, err); web.Read(r, &v); web.JSON(w, 200, v)
web.Host(r); web.HTTPS(r); web.ClientIP(r)
```

### core/db
```go
db.Open(ctx, url); db.Apply(ctx, conn, schemas...)   // empty database: every table
db.Event(ctx, tx, "access", site.OwnerID, site.ID, map[string]string{"email": e}) // in the change's tx
```
`db.Q` is a `*sql.DB` or a `*sql.Tx`.

### core/limits
```go
if err := limits.Allow(r, limits.SignInCodes, email); err != nil { return err } // rate; "" = caller or IP
if err := limits.Fits(r, limits.SiteBytes, total); err != nil { return err }    // size, total after change
limits.KeptVersions.N, limits.QueryTime.Per                                     // plain numbers
```
Rows: Requests, SignInCodes, CodeMails, PasscodeTries, Reports, AccessAsks, SiteBytes, DataBytes,
FilesBytes, SandboxMemory, DatabaseMemory, Publishes, PushDevices, KeptVersions, QueryTime. `limits.Address(r)`: the IP
as limits count it (an IPv6 address by its /64). A site's page and file views are not counted. The operator is never limited. Add a row there; never a setting.

### core/mail, core/guard
```go
a.Mail.Send(ctx, to, subject, text)
guard.Client(timeout, loopback); guard.PublicIP(s); guard.ErrRefused
```

### access/core (people, names, credentials)
```go
a.People.Caller(r, siteID)        // who is calling (run before every route)
a.People.Get(ctx, id); a.People.ByHandle(ctx, h); a.People.Ensure(ctx, email)
a.People.ActsFor(ctx, c, ownerID) // the owner, a member of the owning team, or the operator
a.People.StartSession(w, r, id); a.People.StartSiteSession(w, r, id, siteID)
a.People.SiteConfirmed(ctx, siteID, id); a.People.EndSignIns(ctx, id) // every browser, site session, app grant (keys stay)
a.People.NewKey(ctx, id, name); a.People.Rename(ctx, id, displayName)
access.ValidName(n); access.ClaimName(ctx, q, n, kind, personID, siteID); access.RetireName(ctx, q, n)
                                  // ClaimName refuses the short reserved list (www, api, mcp, ...)
access.NewTeam(ctx, tx, name); access.Remove(ctx, q, id) // a team row; delete a person or team, handle retired
access.NewSecret(prefix); access.Hash(secret)
access.Cookie(r, "sh_session"); access.SetCookie(w, r, name, value, life) // __Host- over HTTPS; life 0 removes
access.SealHop(h, life); access.OpenHop(s) // visitor sign-in hop/ticket between a site and the platform
```
Suspended people are refused before any route runs. A key and an app token are the person.

### sites/core (sites, addresses, the visit decision, versions)
```go
site, err := a.Sites.Of(r)        // site address: its site. Platform: {site} path value,
                                  // owned by ?owner=<team handle> or else the caller.
err = a.Sites.Manage(r, site)     // refuses unless owner, member of the owning team, or operator
p := sites.PlaceOf(r)             // p.Site, or p.Owner on a public page; sites.WithPlace(r, p)
a.Sites.OwnerOf(r)                // ?owner= (a team) or else the caller: whose sites a platform request means
a.Sites.Get(ctx, ownerID, name); a.Sites.ByID(ctx, id); a.Sites.Count(ctx, ownerID); a.Sites.Create(ctx, ownerID, name)
a.Sites.Lookup(ctx, host); a.Sites.URL(site); sites.Partner(host) // www <-> bare
a.Sites.Forget(site.OwnerID, site.ID) // after committing a change to sites, names, hostnames, owner_addresses,
                                  // a suspension; behind Cloudflare it purges them (no ids: all the owner's; "": none)
d, err := a.Sites.Visit(r, site)  // Open, TakenDown, Offline, NeedCode, NeedSignIn, AskAccess
if err != nil { return err }      // database down: an error, never the page
if err := d.Err(); err != nil { return err } // data/API routes: the decision as a refusal (taken down: 410)
sites.GatePage(w, r, d, 0, "")    // a page that is not Open; sites.Next(r): where a gate form goes back to
a.Sites.SetUnlocked(w, r, site); a.Sites.Unlocked(r, site); a.Sites.SealPasscode(id, code); a.Sites.OpenPasscode(site)
dir, err := a.Sites.Stage(site)   // write the files into dir, then:
n, err := a.Sites.Commit(ctx, site, dir, c.ID) // live on every address at once; old ones pruned
a.Sites.VersionDir(site.ID, site.LiveVersion); sites.ParseRedirects(text)
a.Sites.Versions(ctx, site.ID)    // kept versions, newest first, with their author
a.Sites.Rehome(ctx, tx, ownerID)  // after a handle change: the flat address shape
```

### data/core (namespaces in SQLite, rules, the spam check)
```go
a.Data.Create(ctx, site, "bookings")          // namespace + its file (_values, _files tables)
ns, err := a.Data.Open(r, site, "bookings")   // its own sandbox (fixed memory), size cap, query time
defer ns.Close()
rows, err := ns.Query(ctx, `SELECT * FROM b WHERE id = ?`, id) // rows.Columns, rows.Rows
res, err := ns.Exec(ctx, `INSERT INTO b (name, visitor_id) VALUES (?, ?)`, n, c.ID)
err = ns.Tx(ctx, func() error { ... })        // all or nothing
plan, err := ns.Plan(ctx, "b")                // nil plan: no rules, owner only
pred, err := plan.Using(rules.OpRead, data.Bindings(r, isOwner, checked)) // SQL predicate; data.RuleSchema(spam)
q, err := a.Data.Begin(r, a.Sites, write)     // a data route's {ns}: visit decision, spam check
cond, args, err := q.Rule(ctx, "b", rules.OpRead, false) // "1" for the owner; refuses those left out
q.Visible(ctx, "b", op); q.Refused(ctx, "b", op); q.Author(v)
data.IsOwner(r, a.Sites, site); data.Limit(r)
err = a.Data.PutFile(r, ns, "photos/a.jpg", "image/jpeg", c.ID, body) // the ONE way into a file store
a.Data.FilePath(siteID, ns, name) // the name checked where used: inside the namespace or refused
data.ValidFileName(name); a.Data.Sizes(site.ID); a.Data.Namespaces(ctx, site.ID)
err = a.Data.Turnstile(r, token)              // the platform's one key pair; ns.SpamCheck: switched on
a.Data.Changed(ctx, site, ns, table, "add")   // data change: an event only if watched. ops/notify delivers
```
A row's author is the person's id (`auth.id` in rules), in the column `visitor_id` (also in
`_values` and `_files`). `"simplehost/data/core/rules"` is the rules engine (copied).

## Phase 3: ops, api, mcp

- `ops/notify`: routes 14-15 and the AI app's event stream `GET /mcp`; `notify.Set/Rules` (what the
  owner is told about), `Devices`, `PublicKey`, `Feed`, `Deliver` (browser push, RFC 8291 + VAPID).
- `ops/export` (9-10, `export.Next`), `ops/erase` (8), `ops/holds` (64, 66, `holds.TakeDown`),
  `ops/admin` (65, self-hosted `POST /v1/admin/people`, `admin.Counted` wraps the service for API and
  network counts), `ops/idle` (`idle.Sweep`).
- `api`: routes 6, 7, 23, 24; each field calls the owning package.
- `mcp`: `POST /mcp`, the 26-row tool table in `mcp/tools.go` (each row names the route it replays
  as the caller), the in-chat screens in `mcp/screens/`, AgentCat.
- `journeys/`: tests 38-41 with every feature mounted.

## Tests

```go
k := testkit.Start(t, keys.Mount, publish.Mount) // throwaway database + every fake
bob := k.Person(t, "bob")                         // bob.ID, bob.Email, bob.Key
k.Publish(t, bob, "blog", map[string]string{"index.html": "hi"})
b := k.Browser()                                  // own cookie jar; any host reaches the service
r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/keys", body, "X-API-Key", bob.Key)
k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, bob.ID) // setup no feature offers
k.SignIn(t, b, bob, siteURL)                      // a visitor session, for tests of features that need one
```
Fakes: `k.Mail.To(email)`, `k.Push.URL`/`Sent()`, `k.DNS.Set(name, "TXT", v)`, `k.Google.Email`
(then visit `a.Config.GoogleAuthURL`), `k.Turnstile` (the good token), `k.Cloudflare.Purges()` after `k.BehindCloudflare()`, Pebble at
`a.Config.ACMEDirectories[0]`. Outside calls use `a.HTTP`; browser-given addresses `a.Guarded`.
Postgres: `SH_TEST_POSTGRES` (admin URL), else docker container `sh-next-test` on
127.0.0.1:54329 (in memory, left running). Tests are `package x_test`; only SPEC.md section 7 tests.
