---
name: website-deploy-builder
description: Decide what to build on Simple Host before building it. Use when the person has an idea for a website or web tool but has not settled what it should do, asks whether Simple Host can handle accounts, payments, a database, private data or server code, or needs a static-site plan. Map it to KV, SQLite or files with resource-wide policies, deprecated private Submissions or Personal records on existing Simple Host sites, browser storage and public APIs; explain who can read and write before handing off to website-deploy.
---

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. Choose `read:own` for each signed-in visitor’s own records and `write_mode:add` for new-only writes. `signed-in` alone still means shared access. For orders, RSVPs, sign-ups, bookings, applications, support requests, assignments, revisitable surveys or waitlists, use the [Each person's records](#each-persons-records) pattern below with domain-specific tables, status and linked change rows. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.


<!-- Derived from simple-host-website/skills/website-deploy-builder/SKILL.md. Keep in step. -->

# Plan a website on Simple Host

Use this to turn an idea into a concrete plan, then build it with the `website-deploy` skill.
The person's own explicit instructions take priority over this guidance. Keep planning short:
if the idea is clear, go straight to building.

## What a Simple Host site can be

- **Static files**: HTML, CSS, JS, images, fonts, served at the site's own address
  `https://<site>.<handle>.simple-host.app/`, or optionally at a free `<name>.simple-host.app`
  or the person's own domain. Any number of pages.
- **Deprecated shared state on existing sites**: one small JSON document per site (about 1 MB) with atomic ops. Counters,
  vote tallies, settings, a short list.
- **New storage resources**: owner-configured KV entries, a small SQLite database or raw files.
  Each resource has independent `anyone`, `signed-in` or `owner` read and write policies,
  plus a choice to inherit or bypass the site's passcode. All three kinds share
  1,000,000 bytes for KV/SQLite plus 10 MB for files per website; check `storage_get_usage`. Choose own reads for private visitor records; each person reads only their own rows. Compress phone photos before file uploads.
- **Deprecated collections on existing sites**: lists, one item per submission, newest first. RSVPs, sign-ups,
  survey responses, orders, guestbook entries. On any site, a collection
  can be made **private**: signed-in visitors add to it, and only the site owner — and the
  Simple Host operator, for moderation — can read it. The owner can mark items done or delete
  them; in a public list the owner can delete (spam) but not edit. In declared Submissions each
  visitor also changes and withdraws their own.
- **Per-visitor storage** in the browser (`localStorage`, IndexedDB): drafts, carts, settings,
  game saves, a private journal on one device.
- **Public APIs** called from the page with `fetch()` (weather, maps, open data), when the API
  allows browser requests and needs no secret key.

Pages are public: anyone with the link can open them, unless the owner puts one passcode on
the whole site through the trusted Simple Host dashboard. It is a shared passcode, not a login. Each new storage
resource may inherit or bypass it. Existing Submissions are private to the owner by default,
and existing Personal records remain private per visitor; the “Each person's records” pattern gives new records own reads and add-only writes. Visitors sign in when the chosen policy requires it.

## Not a fit

Say so plainly, then offer the part that does fit:

- Server code, scheduled jobs, sending email or texts, webhooks.
- Custom per-user roles, per-field access or confidential medical, financial and ID data in a
  shared storage resource. For per-person reads use read=own and add-only writes; people request changes by adding linked history rows; original records stay add-only. Existing Simple Host sites may keep Personal and private Submissions when they already depend on those semantics.
- Taking card payments on the page. (A shop can take orders and the owner confirms and bills
  separately, or link out to a payment page the owner already has.)
- Calling APIs that need a secret key. A key in a page is public.

For those, suggest keeping Simple Host as the front end and a separate service for the part
that needs a server.

## Map the idea

| The person wants | Build |
|---|---|
| Landing page, portfolio, CV, menu, event info | static pages |
| RSVP, waitlist, sign-up, contact form | “Each person's records”: own-readable add-only SQLite, status and linked change rows; owner review |
| Survey or quiz with answers collected | “Each person's records” for answers people can revisit; aggregate through owner tooling |
| Poll, votes, likes, counter | KV or SQLite resource with a policy suited to the audience; browser-only votes are not tamper-proof |
| Guestbook, wall of messages | SQLite resource with public reads and signed-in writes |
| Only family, a class or a team should see it | a site passcode configured by the person in the trusted Simple Host dashboard |
| Small shop | product list in the page, cart in `localStorage`, “Each person's records” SQLite pattern, plus owner review |
| Calculator, game, drawing tool, planner | static + `localStorage` |
| Dashboard from public data | static + `fetch()` to a public API |
| Searchable small structured data | a SQLite resource, with a schema chosen for the site and a whole-resource access policy |
| Simple settings or public key/value data | a KV resource with the appropriate whole-resource policy |
| Visitor photo or document uploads | a files resource; resize/compress phone photos before upload and budget within the separate 10 MB file allowance and 1 MB per-upload cap |
| Report from a spreadsheet or export | the data as a `.json` or `.csv` file in the site, rendered in the page |
| A shorter address (optional) | free `<name>.simple-host.app` (one `connect_domain` call), or their domain via the `connect-domain` skill |

## Say these up front

- Anything that collects data gets a page that shows what was collected. Plan it in; the person
  rarely asks for it.
- **Anything personal** (orders, RSVPs, survey answers, sign-ups; names, emails, phone numbers,
  addresses): use “Each person's records” with own reads and add-only signed-in writes,
  a status column and linked change rows; add owner review through the connector
  or an owner page before the form goes live. This works on every site's
  own address; a free `<name>.simple-host.app` or their own domain is optional.
- Public lists stay public: guestbook, votes, public comments. Say so plainly.
- Pages are public unless the whole site has a passcode, and anyone given it can pass it on.
  A storage resource's `owner` policy is owner-only; `signed-in` means every signed-in visitor,
  with shared access unless read=own is selected. For own reads on a new site, choose read=own with add-only writes; people request changes by adding linked history rows; original records stay add-only. Existing sites may retain deprecated Personal or private Submissions.

## Hand off

Confirm the plan in two or three sentences (pages, the address, what is saved where and whether
it is private, the results or admin page),
then build it with `website-deploy`, which asks once before a new site goes online (name,
address, public to anyone with the link). For an existing site, read its files first and change only
what the plan needs.

## Your home page

For "make this my home page", use the signed-in connector's `set_home_page`
with `{"site":"portfolio"}`, or a preconfigured owner credential with
`PUT /v1/me/home` and that body. `{"site":null}` restores the showcase.
`who_am_i` reports `home_site`. The person address serves the chosen site's files,
sign-in and storage; its normal site address keeps working. Use `https://simple-host.app/auth.js`, with
`window.SH_CONFIG={site:'portfolio'}` if the page sets an explicit site.
If the home is offline or taken down the showcase appears; rename follows the
site and deleting it clears the choice. The dashboard's Your address panel also
sets it. Available on Simple Host with person addresses, outside hosted events.
"Show my projects on my home page" can mean keeping the default public showcase
or building a site and making it home; unlisted sites stay off the showcase.

For "build me a home page that shows my projects", fetch the public live feed,
render with `textContent`, then choose that site as home. For example:

```html
<ul id="projects"></ul>
<script>
fetch('https://simple-host.app/v1/u/YOUR_HANDLE/showcase.json', {credentials:'omit'})
  .then(r => r.json()).then(feed => feed.sites.forEach(site => {
    const li = document.createElement('li'), a = document.createElement('a');
    a.textContent = site.title || site.name; a.href = site.url;
    li.append(a); document.querySelector('#projects').append(li);
  }));
</script>
```

Substitute the signed-in person's handle. The feed includes only the public
showcase's entries (never unlisted, offline, passcode or taken-down sites), plus
`bio`, `updated_at`, `pinned` and `order`. It allows cross-origin reads without
credentials, caches for 30 seconds and uses the public read rate limits.

To curate the default showcase, use connector `set_bio` with `{"bio":"I make things"}`
(empty clears), and `set_showcase_site` with `{"site":"project","pinned":true,"order":10}`.
Owner REST clients use `GET/PUT /v1/me/bio` and `GET/PUT /v1/sites/{name}/showcase`.
Both refuse deploy-only keys. Pin/order may be changed separately; order is an
integer from 0 to 1000000. Pinned projects come first, then smaller order numbers,
then creation order for ties. Unlisted sites stay hidden even when pinned.
The plain-text bio's limit is returned as `max_length`; it defaults to 280 characters
and is configured by `SHOWCASE_BIO_MAX_LENGTH`. Render it with `textContent`.
The dashboard's Your showcase has the same controls. Hosted events are excluded.

Your home page and showcase also work on small-box path installs: the public
person page opens the selected site’s normal URL, and the owner dashboard stays
on its own origin. Use the instance’s returned URLs and API origin.


## Keep website files small

Before deploying, compress images and check the size of every file and the whole output folder. Read `who_am_i` (or `GET /v1/me`) for account file usage and `list_sites` (or `GET /v1/sites`) for site sizes. Simple Host normally keeps 4 versions, allows 200 MB including live files and retained versions for new websites from the 2026-10-05 deployment, and 1 GB per account. Earlier websites have no total cap; operator exceptions apply. The live copy is separate and counts too. The server checks after pruning, so replacing a site uses the resulting total. Saved data and storage resources have their own limits.

Shrink photos to about 1600 px wide, using WebP or JPEG at about 80% quality; phone photos are often 4–12 MB. Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need.

Compress photos before the first deploy, not only after a refusal. If a deploy returns `site_total_too_large`, `account_storage_full` or `site_too_large`, follow its tips and reduce the files before retrying. Ask which old sites the person no longer needs before deleting any. Only accounts enabled by the operator may change the version count; other accounts get “Simple Host keeps your 4 latest versions”.

<a id="shop-with-orders"></a>

## Each person's records

People add records; each signed-in person sees only their own, with status and
history. The owner sees and updates all records. People request changes by
appending linked change rows, preserving the original record and its history.

Example uses: shop orders, RSVPs and event sign-ups, bookings and appointments, applications (jobs, clubs, hackathons), support requests, homework or assignment submissions, survey answers people can revisit, and a waitlist with “my place in line”.

Use this pattern when someone asks for any of these. Choose resource and table
names that fit the domain (`bookings`, `applications`, and so on), keeping
`read:"own"`, `write:"signed-in"`, `write_mode:"add"`, a status column and a
linked change table in the same database. Declare its foreign key so a person's
change rows can reference only their own record. This pattern is for hosted
Simple Host and small-box installs.

### Worked example: shop orders

Create an `orders` SQLite resource with `storage_set_resource(site,"orders",body)`
(or owner PUT):

```json
{"kind":"sqlite","read":"own","write":"signed-in","write_mode":"add","site_passcode":"inherit"}
```

Through owner `storage_sql_schema`, create these tables in two calls in that
same database:

```sql
CREATE TABLE orders (id INTEGER PRIMARY KEY, item TEXT NOT NULL,
                     quantity INTEGER NOT NULL, status TEXT DEFAULT 'placed',
                     created_at TEXT)
```

```sql
CREATE TABLE order_changes (
  id INTEGER PRIMARY KEY,
  order_id INTEGER NOT NULL REFERENCES orders(id),
  kind TEXT NOT NULL CHECK (kind IN ('change','note','cancel_request')),
  details TEXT NOT NULL,
  created_at TEXT
)
```

Both tables inherit add-only writes and own reads from the resource. The server
adds indexed `visitor_id TEXT`, stamps `created_at` on visitor inserts, and
ignores client-sent identity and timestamps. A declared foreign key checks that
the referenced order exists and belongs to the same customer, in the insert
transaction (404 for a missing order, 403 for another customer's order).

Customers never rewrite or delete a placed order. They customise it by adding
a change, a note, a cancellation request or a requested new quantity to
`order_changes`; the original order and change history remain. The page shows
“My orders”, each current status and its history, using two reads and matching
`order_changes.order_id` to `orders.id` in the page. There is no `include=` option.

```html
<script>window.SH_CONFIG = {site: 'pantry'};</script>
<script src="https://simple-host.app/auth.js" defer></script>
```

```js
await SH.requireSignIn();
const db = SH.storage.sqlite('orders');
const orders = db.table('orders');
const changes = db.table('order_changes');
const receipt = await orders.add({item: 'Chai spice', quantity: 2});
await changes.add({order_id: receipt.last_insert_id, kind: 'change',
                   details: 'Please change the quantity to 3'});
const mine = await orders.list({order: 'id', desc: 1, limit: 50});
const history = await changes.list({order: 'id', limit: 100});
// Both results contain only this customer's rows. Match history by order_id.
// Follow each next_after cursor with the same order and direction.
```

The owner sees all orders and history through `storage_sql_query`. The owner's
view applies or acknowledges requests and updates the order's status or stage
with `storage_sql_execute`, for example `UPDATE orders SET status=? WHERE id=?`
with params `["packed",17]`. Keep the history when handling a request. Use the
trusted owner dashboard/connector; never put an owner key in a page.

Create a `photos` file bucket with signed-in add-only writes. Choose
`read:"anyone"` for photos that visitors may view, or `read:"owner"` for photos
only the shop owner reads:

```json
{"kind":"files","read":"anyone","write":"signed-in","write_mode":"add","site_passcode":"inherit"}
```

```js
await SH.requireSignIn();
// preparedPhoto is a resized WebP Blob, below the single-upload cap.
await SH.storage.files('photos').put(crypto.randomUUID() + '.webp', preparedPhoto);
```

A page may instead call relative REST routes with `credentials:'same-origin'`
and `X-SH-CSRF: 1` for POST/PUT. The server takes identity from the site session.
Preserve the form on a refusal; never widen access to make a save work.

### The same pattern for bookings

Name the SQLite resource `bookings`, keeping the same policies. Through the
owner schema route, create these tables in two calls:

```sql
CREATE TABLE bookings (id INTEGER PRIMARY KEY, appointment TEXT NOT NULL,
                       status TEXT DEFAULT 'requested', created_at TEXT)
```

```sql
CREATE TABLE booking_changes (
  id INTEGER PRIMARY KEY,
  booking_id INTEGER NOT NULL REFERENCES bookings(id),
  kind TEXT NOT NULL CHECK (kind IN ('change','note','cancel_request')),
  details TEXT NOT NULL,
  created_at TEXT
)
```

The server adds indexed `visitor_id TEXT` to both tables as in the orders
example. People add bookings and linked change rows; “My bookings” reads both
tables and matches `booking_changes.booking_id` to `bookings.id`. Each person
sees only their own status and history. The owner reads all bookings and
changes, and updates status (for example, `confirmed`) through owner SQL.
