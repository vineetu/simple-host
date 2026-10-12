# Capabilities in full

Moved here from SKILL.md unchanged.

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. Choose a preset per resource (and per SQLite table): `records` for each signed-in visitor’s own records, `inbox` for forms. `signed-in` alone still means shared access. For orders, bookings, reservations, RSVPs with a status, support requests and assignments, use the [Each person's records](handoff-and-examples.md#each-persons-records) pattern in `references/handoff-and-examples.md` with domain-specific tables, status and linked change rows; quote requests, contact forms, waitlists and sign-ups are `inbox`, sent without sign-in. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

[Open Claude connectors](https://claude.ai/new?modal=add-custom-connector#customize/connectors/yours). Name it **Simple Host**, paste `https://simple-host.app/mcp`, and choose **Add**. In the sign-in window, sign in with Google or an email code, then choose **Allow**. The connector works in the Claude web, desktop, and phone apps.

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
  10,000,000 bytes for KV/SQLite plus 10 MB for files per website; check `storage_get_usage`. Choose own reads for private visitor records; each person reads only their own rows. Compress phone photos before file uploads.
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

Pages are public: anyone with the link can open them, unless the owner opens the whole site
only to named viewers (people named by email, each signing in on the site) or puts one passcode
on it. A passcode is shared, not a login; a site has one or the other, not both. Each new storage
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
| RSVP, waitlist, sign-up, contact form | “Each person's records”: SQLite with preset `records`, status and linked change rows; `inbox` for a form only the owner reads; `board` or `wall` for a shared sign-up sheet; owner review, or an `admin` page |
| Survey or quiz with answers collected | “Each person's records” for answers people can revisit; aggregate through owner tooling |
| Poll, votes, likes, counter | KV or SQLite resource with a policy suited to the audience; browser-only votes are not tamper-proof |
| Guestbook, wall of messages | SQLite resource with public reads and signed-in writes |
| Only these people should see it (emails given) | named viewers (`grant_site_viewer` with those emails, after asking) |
| Only family, a class or a team should see it | ask: named viewers by email (each signs in) or one shared passcode (`set_site_passcode`, with the code the person chose); not unlisted |
| Small shop | product list in the page, cart in `localStorage`, “Each person's records” SQLite pattern, plus owner review |
| Calculator, game, drawing tool, planner | static + `localStorage` |
| Dashboard from public data | static + `fetch()` to a public API |
| Searchable small structured data | a SQLite resource, with a schema chosen for the site and a whole-resource access policy |
| Simple settings or public key/value data | a KV resource with the appropriate whole-resource policy |
| Visitor photo or document uploads | a files resource; resize/compress phone photos before upload and budget within the separate 10 MB file allowance and 1 MB per-upload cap |
| Report from a spreadsheet or export | the data as a `.json` or `.csv` file in the site, rendered in the page |
| A shorter address (optional) | free `<name>.simple-host.app` (one `connect_domain` call), or their domain via the `connect-domain` skill |

## Say these up front

- A public list (guestbook, votes, public comments) gets a page that shows what was collected;
  plan it in, the person rarely asks for it. Owner-only or each-person's-own data has no viewer
  page: the owner's view is the connector (`storage_sql_query`) and the dashboard, since a key
  that could read it back must never sit in a page.
- **Anything personal** (orders, RSVPs, survey answers, sign-ups; names, emails, phone numbers,
  addresses): use "Each person's records" with own reads and add-only signed-in writes,
  a status column and linked change rows; the owner reviews and updates status through the
  connector or dashboard, never a page. This works on every site's
  own address; a free `<name>.simple-host.app` or their own domain is optional.
- Public lists stay public: guestbook, votes, public comments. Say so plainly.
- Pages are public unless the whole site has named viewers (only the people named by email) or
  a passcode (anyone given it can pass it on).
  A storage resource's `owner` policy is owner-only; `signed-in` means every signed-in visitor,
  with shared access unless read=own is selected. For own reads on a new site, choose read=own with add-only writes; people request changes by adding linked history rows; original records stay add-only. Existing sites may retain deprecated Personal or private Submissions.

## Hand off

Confirm the plan in two or three sentences (pages, the address, what is saved where and whether
it is private, and whether the owner reads it through the connector/dashboard or a public
results page), then build it with `website-deploy`, which asks once before a new site goes
online (name, address, public to anyone with the link). For an existing site, read its files
first and change only what the plan needs.
