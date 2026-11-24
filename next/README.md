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
caller resolved, and the request counted against `limits.Requests`. Data routes that pages call
on their origin and the AI calls on the platform are mounted on both `Site` and `Platform`.

## Who is calling, and which site

```go
c := web.CallerOf(r)    // c.ID == "" : nobody. c.Visitor(): signed in on this one site only.
                        // c.Via: "key", "app", "session" (dashboard) or "site". c.Operator.
site, err := a.Sites.Of(r)        // site address: its site. Platform: {site} path value,
                                  // owned by ?owner=<team handle> or else the caller.
err = a.Sites.Manage(r, site)     // refuses unless owner, member of the owning team, or operator
p := sites.PlaceOf(r)             // p.Site, or p.Owner on a public page
```

Suspended people are refused before any route runs. A key and an app token are the person.

```go
access.Cookie(r, "sh_session"); access.SetCookie(w, r, name, value, life) // __Host- over HTTPS; life 0 removes
a.People.EndSignIns(ctx, id)      // every browser, site session and app grant (keys stay)
access.NewTeam(ctx, tx, name); access.Remove(ctx, q, id) // a team row; delete a person or team, handle retired
access.SealHop(h, life); access.OpenHop(s) // visitor sign-in hop/ticket between a site and the platform
a.Sites.Count(ctx, ownerID)       // live sites of an owner
```
## The one access decision for a visit

```go
d, err := a.Sites.Visit(r, site)  // Open, TakenDown, Offline, NeedCode, NeedSignIn, AskAccess
if err != nil { return err }      // database down: an error, never the page
if err := d.Err(); err != nil { return err } // data/API routes: the decision as a refusal
```

A page that is not Open: `sites.GatePage(w, r, d, 0, "")` (passcode form, sign in, Ask for access,
offline, taken down); `sites.Next(r)` is where a gate form goes back to.
Passcode: `a.Sites.SetUnlocked(w, r, site)`, `Unlocked(r, site)`, `SealPasscode`, `OpenPasscode`.
After changing anything an address or visit depends on (sites, names, hostnames,
owner_addresses, a suspension) call `a.Sites.Forget()`: lookups are cached in memory.

## Events (cross-feature messages)

```go
db.Event(ctx, tx, "access", site.OwnerID, site.ID, map[string]string{"email": e}) // in the change's tx
a.Data.Changed(ctx, site, ns, table, "add")  // data change: an event only if watched. ops/notify delivers
```

## Limits

```go
if err := limits.Allow(r, limits.SignInCodes, email); err != nil { return err } // rate; "" = caller or IP
if err := limits.Fits(r, limits.SiteBytes, total); err != nil { return err }    // size, total after change
limits.KeptVersions.N, limits.QueryTime.Per                                     // plain numbers
```
The operator is never limited. Add a row to the table in `core/limits`; never a setting.

## Saved data

```go
a.Data.Create(ctx, site, "bookings")          // namespace + its file (_values, _files tables)
ns, err := a.Data.Open(r, site, "bookings")   // sandboxed, size cap and query time guards
defer ns.Close()
rows, err := ns.Query(ctx, `SELECT * FROM b WHERE id = ?`, id) // rows.Columns, rows.Rows
res, err := ns.Exec(ctx, `INSERT INTO b (name, author) VALUES (?, ?)`, n, c.ID)
plan, err := ns.Plan(ctx, "b")                // nil plan: no rules, owner only
pred, err := plan.Using(rules.OpRead, data.Bindings(r, isOwner, checked)) // SQL predicate
err = a.Data.Turnstile(r, token)       // the platform's one key pair; ns.SpamCheck: switched on
```
A row's author is the person's id (`auth.id` in rules), in the column `visitor_id` (also in
`_values` and `_files`). Space used: `a.Data.Sizes(site.ID)`.

```go
q, err := a.Data.Begin(r, a.Sites, write)     // a data route's {ns}: visit decision, X-SH-CSRF, spam check
cond, args, err := q.Rule(ctx, "b", rules.OpRead, false) // "1" for the owner; refuses those left out
err = ns.Tx(ctx, func() error { ... })        // all or nothing
err = a.Data.PutFile(r, ns, "photos/a.jpg", "image/jpeg", c.ID, body) // the ONE way into a file store
```

## Refusals
Return `web.Refuse(status, "code", "One plain sentence saying how to fix it.")` from a
`web.Func` handler: written as `{"code", "fix"}`. `web.NotFound` for anything missing or hidden;
any other error is logged and answered `server_error`. `web.Read(r, &v)`, `web.JSON(w, 200, v)`.

## Versions
```go
dir, err := a.Sites.Stage(site)   // write the files into dir, then:
n, err := a.Sites.Commit(ctx, site, dir, c.ID) // live on every address at once; old ones pruned
a.Sites.VersionDir(site.ID, site.LiveVersion); a.Sites.URL(site); sites.ParseRedirects(text)
```

## Tests

```go
k := testkit.Start(t, keys.Mount, publish.Mount) // throwaway database + every fake
bob := k.Person(t, "bob")                         // bob.ID, bob.Email, bob.Key
k.Publish(t, bob, "blog", map[string]string{"index.html": "hi"})
b := k.Browser()                                  // own cookie jar; any host reaches the service
r := testkit.Call(t, b, "POST", "https://simple-host.app/v1/keys", body, "Authorization", "Bearer "+bob.Key)
k.Exec(t, `UPDATE people SET operator = true WHERE id = $1`, bob.ID) // setup no feature offers
```
Fakes: `k.Mail.To(email)`, `k.Push.URL`/`Sent()`, `k.DNS.Set(name, "TXT", v)`, `k.Google.Email`
(then visit `a.Config.GoogleAuthURL`), `k.Turnstile` (the good token), Pebble at
`a.Config.ACMEDirectories[0]`. Outside calls use `a.HTTP`; browser-given addresses `a.Guarded`.
Postgres: `SH_TEST_POSTGRES` (admin URL), else docker container `sh-next-test` on
127.0.0.1:54329 (in memory, left running). Tests are `package x_test`; only SPEC.md section 7 tests.
