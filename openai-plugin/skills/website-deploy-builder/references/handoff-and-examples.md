# Handoff, home page, file sizes and worked examples

Moved here from SKILL.md unchanged.

## Hand off

Confirm the plan in two or three sentences (pages, the address, what is saved where and whether
it is private, and whether the owner reads it through the connector/dashboard or a public
results page), then build it with `website-deploy`, which asks once before a new site goes
online (name, address, public to anyone with the link). For an existing site, read its files
first and change only what the plan needs.

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

Example uses: shop orders, RSVPs and event sign-ups, bookings and appointments, applications (jobs, clubs, hackathons), support requests, homework or assignment submissions, survey answers people can revisit, and a waitlist with “my place in line” (a plain waitlist is `inbox`).

Use this pattern when someone asks for any of these. Choose resource and table
names that fit the domain (`bookings`, `applications`, and so on), keeping
the `records` preset, a status column and a
linked change table in the same database. Declare its foreign key so a person's
change rows can reference only their own record. This pattern is for hosted
Simple Host and small-box installs.

### Worked example: shop orders

Create an `orders` SQLite resource with `storage_set_resource(site,"orders",body)`
(or owner PUT):

```json
{"kind":"sqlite","preset":"records","site_passcode":"inherit"}
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

Both tables use the resource's `records` preset (add signed-in, read own, edit and delete owner). The server
adds indexed `visitor_id TEXT`, stamps `created_at` on visitor inserts, and
ignores client-sent identity and timestamps. A declared foreign key checks that
the referenced order exists and belongs to the same customer, in the insert
transaction (404 `invalid_reference` for a missing order and for another customer's order alike).

Customers never rewrite or delete a placed order. They customise it by adding
a change, a note, a cancellation request or a requested new quantity to
`order_changes`; the original order and change history remain. The page shows
“My orders”, each current status and its history, using two reads and matching
`order_changes.order_id` to `orders.id` in the page. There is no `include=` option.

```html
<script>window.SH_CONFIG = {site: 'pantry'};</script>
<script src="https://simple-host.app/auth.js"></script>
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

The owner sees all orders and history through `storage_sql_query`. Each row's
`visitor_id` is the customer's sign-in; to answer "who placed order 12?" read
`SELECT visitor_id FROM orders WHERE id = 12` and pass it to
`storage_visitor_emails`, which gives the email they signed in with. Tell the
person; never write the email into a page or into saved data. The owner's
view applies or acknowledges requests and updates the order's status or stage
with `storage_sql_execute`, for example `UPDATE orders SET status=? WHERE id=?`
with params `["packed",17]`. Keep the history when handling a request. Use the
trusted owner dashboard/connector, or an admin page (`get_page_recipe` topic
`admin`) where the owner, signed in on the site with their account email, lists
every order and sets a status with `table('orders').edit(id, {status})`; never
put an owner key in a page.

Create a `photos` file bucket. Choose preset `wall` for photos that visitors may
view (authors can remove their own), or `records` for photos only the sender and
the shop owner see:

```json
{"kind":"files","preset":"wall","site_passcode":"inherit"}
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

Build sites in the person’s own AI app, then publish them with Simple Host. Get started: https://simple-host.app/install.html
