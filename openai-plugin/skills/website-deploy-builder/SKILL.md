---
name: website-deploy-builder
description: Decide what to build on Simple Host before building it. Use when the person has an idea for a website or web tool but has not settled what it should do, asks whether Simple Host can handle accounts, payments, a database, private data or server code, or needs a static-site plan. Map it to KV, SQLite or files with resource-wide policies, deprecated private Submissions or Personal records on existing Simple Host sites, browser storage and public APIs; explain who can read and write before handing off to website-deploy.
---

On Simple Host, the older state, collection and declared-data APIs are deprecated. Use them only to maintain an existing site that depends on their behavior. New sites should use owner-defined KV, SQLite and file resources. These resources have whole-resource access policies, so do not treat `signed-in` as per-person row privacy. Simple Hack websites expose only KV, SQLite and files; event signup stays on the trusted Simple Hack apex.


<!-- Derived from simple-host-website/skills/website-deploy-builder/SKILL.md. Keep in step. -->

# Plan a website on Simple Host

Use this to turn an idea into a concrete plan, then build it with the `website-deploy` skill.
The person's own explicit instructions take priority over this guidance. Keep planning short:
if the idea is clear, go straight to building.

## What a Simple Host site can be

- **Static files**: HTML, CSS, JS, images, fonts, served at the site's own address
  `https://<site>.<handle>.simple-host.app/`, or optionally at a free `<name>.simple-host.app`
  or the person's own domain. Any number of pages.
- **Shared state**: one small JSON document per site (about 1 MB) with atomic ops. Counters,
  vote tallies, settings, a short list.
- **New storage resources**: owner-configured KV entries, a small SQLite database or raw files.
  Each resource has independent `anyone`, `signed-in` or `owner` read and write policies,
  plus a choice to inherit or bypass the site's passcode. All three kinds share
  1,000,000 bytes per website; check `storage_get_usage`. Policies cover the entire resource,
  not individual rows or visitors. Compress phone photos before file uploads.
- **Collections**: lists, one item per submission, newest first. RSVPs, sign-ups,
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
the whole site (`set_site_passcode`). It is a shared passcode, not a login. Each new storage
resource may inherit or bypass it. Existing Submissions are private to the owner by default,
and existing Personal records remain private per visitor; new storage policies do not create
per-person privacy. Visitors sign in when the chosen policy requires it.

## Not a fit

Say so plainly, then offer the part that does fit:

- Server code, scheduled jobs, sending email or texts, webhooks.
- Per-user roles, row-level privacy or confidential medical, financial and ID data in a
  shared storage resource. For each visitor's own simple private record, use the existing
  Personal kind; for owner-private submissions, use existing Submissions.
- Taking card payments on the page. (A shop can take orders and the owner confirms and bills
  separately, or link out to a payment page the owner already has.)
- Calling APIs that need a secret key. A key in a page is public.

For those, suggest keeping Simple Host as the front end and a separate service for the part
that needs a server.

## Map the idea

| The person wants | Build |
|---|---|
| Landing page, portfolio, CV, menu, event info | static pages |
| RSVP, waitlist, sign-up, contact form | private collection + owner admin page; a plain count in state if wanted |
| Survey or quiz with answers collected | collection `responses` + `results.html` aggregating them (private and owner-only if answers are personal) |
| Poll, votes, likes, counter | state with `inc` (remember "already voted" in `localStorage`) |
| Guestbook, wall of messages | public collection, listed newest first on the page |
| Only family, a class or a team should see it | a site passcode (`set_site_passcode`), which the person shares themselves |
| Small shop | product list in the page, cart in `localStorage`, private `orders` collection + owner `orders.html` |
| Calculator, game, drawing tool, planner | static + `localStorage` |
| Dashboard from public data | static + `fetch()` to a public API |
| Searchable small structured data | a SQLite resource, with a schema chosen for the site and a whole-resource access policy |
| Simple settings or public key/value data | a KV resource with the appropriate whole-resource policy |
| Visitor photo or document uploads | a files resource; resize/compress phone photos before upload and budget within 1,000,000 bytes |
| Report from a spreadsheet or export | the data as a `.json` or `.csv` file in the site, rendered in the page |
| A shorter address (optional) | free `<name>.simple-host.app` (one `connect_domain` call), or their domain via the `connect-domain` skill |

## Say these up front

- Anything that collects data gets a page that shows what was collected. Plan it in; the person
  rarely asks for it.
- **Anything personal** (orders, RSVPs, survey answers, sign-ups; names, emails, phone numbers,
  addresses): make the collection private first, before the form goes live, and add an
  owner admin page that shows the list only to the owner signed in. This works on every site's
  own address; a free `<name>.simple-host.app` or their own domain is optional.
- Public lists stay public: guestbook, votes, public comments. Say so plainly.
- Pages are public unless the whole site has a passcode, and anyone given it can pass it on.
  A storage resource's `owner` policy is owner-only; `signed-in` means every signed-in visitor,
  with no row-level separation. Use Personal or private Submissions for per-person privacy.

## Hand off

Confirm the plan in two or three sentences (pages, the address, what is saved where and whether
it is private, the results or admin page),
then build it with `website-deploy`, which asks once before a new site goes online (name,
address, public to anyone with the link). For an existing site, read its files first and change only
what the plan needs.
