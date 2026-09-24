---
name: website-deploy
description: Build, publish and change websites on Simple Host through the connected Simple Host tools. Use when the person wants a website, landing page, portfolio, event or RSVP page, sign-up or order form, survey, poll, guestbook or small shop put online; wants to edit, redesign, rename, roll back or delete a site they already have; or wants to see or change what a site has collected (RSVPs, responses, orders, votes, counts) or how many people visited. Covers writing well-designed static pages, saving visitor data with the hosted page helper (shared state and append-only collections), results and admin pages, versions and rollback, and what is and is not public.
---

<!-- Derived from simple-host-website/skills/website-deploy/SKILL.md (+ references/backend.md, references/packaging-and-validation.md, references/frameworks.md, references/operations.md) and internal/mcp/instructions.go. Keep in step. -->

# Build a website on Simple Host

Simple Host publishes static websites and gives every site a small built-in backend. You are
already signed in as the person through the connected tools. Never ask them for an email, a
code, a password or a key. If a tool says the connection is no longer signed in, ask them to
reconnect Simple Host in the app's settings, then carry on.

The person's own explicit instructions take priority over anything in this skill. Where they
have said what they want (a colour, a layout, a site name, no results page), do that.

If the idea is still vague or may not fit a static site, use the `website-deploy-builder` skill first.
For the person's own domain, use the `connect-domain` skill.

## Tools

| Need | Tool |
|---|---|
| Who am I, what is my handle | `who_am_i` |
| My sites / one site and its files | `list_sites`, `get_site` |
| Read a file of a site | `read_site_file` |
| Publish a new site | `create_site` |
| Change an existing site | `update_site` (read its files first) |
| Versions, undo a bad publish | `list_versions`, `rollback_site` |
| Rename, list on public page, delete | `rename_site`, `set_visibility`, `delete_site` |
| Saved data | `get_state`, `update_state`, `list_collections`, `read_collection`, `add_to_collection` |
| Visitors | `site_analytics` (report the `person` numbers) |

To publish a new site use `create_site`; to change an existing site use `update_site` (read
its files first). `create_site` never overwrites an existing site.

## Publishing

- Send every file inline: `{"index.html": "...", "css/style.css": "..."}`. `index.html` is
  required. Binary files (images, fonts) go in `files_base64`; a path is never in both maps.
- **Relative links only.** Sites live under a path
  (`https://sites.simple-host.app/<handle>/<site>/`), so `css/style.css`, `./img/a.jpg`,
  `about/` work and `/css/style.css` breaks.
- **Static files only**: HTML, CSS, JS, images, fonts, media. Nothing runs on the server (no
  PHP, Node, Python, server routes). One self-contained `index.html` is fine for small sites.
- Site names: lowercase letters, numbers, hyphens (`garden-party`), unique in the account.
  Pick a short descriptive one unless the person named it.
- After publishing, give the person the exact `url` the tool returned. Never compose an address.
- Check your work before calling it done: relative links only, every referenced file is in the
  set you sent, names match case exactly. If you can open the url, do it and confirm the page
  and its styles load.
- Framework projects (Vite, Next.js, Astro...) or building from a local folder: read
  `references/frameworks-and-files.md`.

## Changing an existing site

A publish **replaces the whole site**: any file you do not send stops existing.

1. `list_sites` for the exact name, then `get_site` for its file list.
2. `read_site_file` for every text file; change only what was asked.
3. Send **all** files back with `update_site`.

Binary files are only described by `read_site_file`, not returned. If the site has images or
fonts you do not have the bytes for, tell the person before publishing that those files would
be dropped, and ask them to provide them again or agree to losing them.

Every version is kept. If a change went wrong, `list_versions`, confirm the version with the
person, then `rollback_site`. Renaming (`rename_site`) changes the address and the old one stops
working; tell the person. `delete_site` removes the site, every version and all its saved data,
permanently: call it only after the person has explicitly confirmed deleting that specific site
in this conversation, and name what will be lost when you ask.

## Public by default

Every site, and everything it saves, can be read by anyone who has the link. There are no
private or password-protected pages. `set_visibility` `unlisted` only keeps a site off the
person's public page; it is not privacy. Never put secrets, keys or passwords in pages or data.

A results or admin page is a convenience view, not a protected one: anyone with its address can
open it. If the person asks for an admin page, build it and tell them that in one sentence, and
suggest not sharing its address widely.

If collected data should not be visible to strangers (an RSVP list, survey answers, orders),
say plainly that on the shared address anyone who finds the site can read it. The step toward
keeping it closer is putting the site on its own domain (`connect-domain`), and even then the
data store is not a private database: do not promise privacy. Collect only what the person is
comfortable with, and never ask visitors for payment details, ID numbers or health information.

## Saving data from a page

Every site has two stores, both read by anyone:

- **State**: one shared JSON document (about 1 MB) for counters, settings, tallies, small
  lists. Change it with atomic ops so visitors saving at once never clobber each other:
  `set`, `inc`, `append`, `remove`, `removeWhere`.
- **Collections**: append-only lists, one item per submission (up to 64 KB each), read newest
  first and paged. Use one for RSVPs, responses, orders, guestbook entries, sign-ups.

Pages save through the hosted helper. Put `SH.requireSignIn()` before every save:

```html
<div id="sh-auth"></div>
<script>window.SH_CONFIG = { site: "<site-name>" };</script>
<script src="https://simple-host.app/auth.js" defer></script>
<script>
window.addEventListener('DOMContentLoaded', function () {
  SH.mount('#sh-auth');
  form.onsubmit = async function (e) {
    e.preventDefault();
    try {
      await SH.requireSignIn();
      await SH.collection('rsvps').append({ name: form.name.value, guests: +form.guests.value });
      await SH.state.patch([{ op: 'inc', path: 'count', by: 1 }]);
      showDone();
    } catch (err) { showError('Not saved: ' + (err.code || err.status)); }
  };
});
</script>
```

On the shared address, saving from a page currently works without sign-in. Simple Host is
moving to require visitors to sign in (Google or an emailed code) before saving there, and on a
site's own domain they already do. Writing `SH.requireSignIn()` before each save keeps the same
page working either way.

Rules that make forms trustworthy:

- On a failed save, keep the form filled, show the error, and never claim success. Never re-send
  a collection item after an error. If the append succeeded and a follow-up count patch failed,
  retry only the patch.
- **Pair every form with a page that shows what was collected** (`results.html` or
  `admin.html`), linked quietly from the main page's footer, with
  `<meta name="robots" content="noindex">`. The person will not think to ask for it.
- Per-visitor things (drafts, a cart, preferences) go in `localStorage` with a site-specific key
  prefix, never in shared state.
- Design empty, loading and error states for every list and form.

You can read and change the data directly: `read_collection`, `get_state`, `update_state` (prefer
`ops`), `add_to_collection` (appends are never undone; do not retry one that may have
succeeded). Full helper API, data shapes, error codes and reading patterns:
`references/saving-data.md`. Load it whenever a page saves or lists data.

## Design: make it look deliberate

- Real content from the person's request. No lorem ipsum, no invented testimonials, reviews,
  logos, stats or prices. If something is missing, write a sensible placeholder the person will
  obviously replace, and tell them.
- A restrained palette: one neutral background, one text colour, one accent. Check text
  contrast meets WCAG AA.
- One or two typefaces (a system stack, or one Google Font pairing), with a clear hierarchy:
  one large heading, readable body at 16-18px, line length around 60-75 characters.
- Generous, consistent spacing on a simple scale. Let sections breathe.
- Mobile first: design at 390px wide, then widen. Tap targets at least 44px. No horizontal
  scroll.
- No gratuitous gradients, emoji decoration, glassmorphism, glowing blobs, or stock "hero"
  clichés. Plain, confident layouts beat effects.
- Forms: visible labels, sensible input types, clear required markers, a plain success message
  that says what happened.
- Semantic HTML, alt text on images, focus styles kept, `lang` and viewport meta set.

## Worked patterns

**Small shop** (like https://sites.simple-host.app/poojahs19/prepared-shelf/): products as a JS
array in the page (name, short line, price, image paths under `img/`), grid of cards, a cart in
`localStorage`, a checkout form (name and one way to reach them) that appends one item to
collection `orders` with the cart lines and total, then clears the cart and says the owner will
be in touch. Orders are readable by anyone who finds them, so collect only what the owner needs
to follow up; a delivery address only if the person accepts that. No card payments on the page. `orders.html` lists `orders` newest first with totals.

**RSVP page with an admin page**: an elegant single page (event name, date, place, a short
note) with a form (name, attending yes/no, number of guests, dietary note) appending to
collection `rsvps` and incrementing `totals.yes` / `totals.no` / `totals.guests` in state.
`admin.html` shows the counts from state and a table of every RSVP from the collection, paged
with `next`. Tell the person anyone with the admin address can open it.

**Survey with a results page** (Jotform-like): questions defined as a JS array (id, type:
choice / multi / scale / text, options) rendered into one form, one question group per screen
on mobile, answers appended as one item to collection `responses`. `results.html` pages through
all responses and aggregates them: counts and bars per choice, average per scale, the latest
free-text answers.
