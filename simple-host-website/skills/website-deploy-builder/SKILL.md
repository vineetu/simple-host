---
name: website-deploy-builder
description: Plan a website on Simple Host before building it. Use when the person has an idea for a site or web tool but has not settled what it should do, asks whether Simple Host can handle bookings, forms, accounts, payments, a database, private pages or server code, or wants to know what to build with Simple Host KV, SQLite or files. Explains who can read and write what, then hands implementation to website-deploy.
---

# Website Deploy Builder

Use this skill to decide what to build. When the person has chosen, hand the build to the
`website-deploy` skill, which publishes it. Use the signed-in Simple Host connector; if it is
missing, ask the person to connect Simple Host (https://simple-host.app/install.html). Never ask
for sign-in codes, API keys or passwords in chat.

## What Simple Host is

A static-file host: every site lives at `https://<sitename>.<handle>.simple-host.app/` and serves
HTML, CSS, JS, images and fonts. Nothing runs on the server. Each site can still save data in
three kinds of resource the owner creates: KV (named JSON values), SQLite (tables and queries) and
files (photos, PDFs). Each resource, and each SQLite table, has a preset that says who may read,
add, edit and delete. Visitors sign in on the site itself with an emailed code or Google, never
with an account or a password.

## Map the request

| They want | Plan |
|---|---|
| a landing page, portfolio, CV, multi-page site | static only, relative links |
| bookings, appointments, table reservations | SQLite preset `records`, recipe `booking`: the customer signs in when they book and sees their status; the owner confirms from chat |
| a quote request, contact form, waitlist, sign-up, application, feedback | SQLite preset `inbox`, recipe `inbox`: anyone sends without signing in; only the owner reads |
| a menu, prices, opening hours the owner updates | KV preset `public`, recipe `public`: the owner changes it from chat, no republish |
| orders from a small shop | SQLite preset `records` with a status and a linked change table |
| a client portal | one site per client, opened only to that client's email (named viewers), recipe `portal` |
| reviews, comments, a guestbook | preset `wall`: everyone reads, people sign in to post |
| photos visitors share | files preset `wall`, recipe `gallery` |
| a shared sign-up sheet or task list | preset `board` |
| things each visitor keeps for themselves | preset `personal` |
| only some people may open the whole site | named viewers by email, or one shared passcode; ask which if they did not say |
| their own domain, or a shorter free address | the `connect-domain` skill, or a free `<name>.simple-host.app` |
| a tool that runs only in the browser | static plus `localStorage` or `IndexedDB` |
| data from a public API | static plus `fetch()` to a CORS-enabled API, no secret keys in the page |

## How to use this skill

1. Ask what they are trying to build, in plain language. Let them describe the idea.
2. Decide whether it can run as a static site. Name any part that cannot (server code, payments
   processing, custom account systems, long-running jobs) and suggest a substitute or another
   service for that part.
3. If visitors save anything, state each resource, its preset, and whether sign-in is needed,
   before designing the page. Personal details go in `inbox`, `records` or `personal`, never in a
   resource everyone signed in can read.
4. Always pair a form with a way for the owner to read it back: from chat with
   `storage_sql_query`, or an owner page on the site (recipe `admin`).
5. Give a short plan: how to structure it, the storage shape, and the gotchas.
6. Hand off to `website-deploy` to build and publish. For a site that already exists, write a
   short prompt the person can paste into a fresh agent chat instead, and say in it to confirm
   any step that deletes data, makes private data public, or changes who can see or save.

## Advanced: open only when the plan needs it

If a file is not on disk next to this one, fetch its URL.

| Need | Reference |
|---|---|
| Every capability with its REST and connector routes, the capability tree (browser storage, external APIs, exports, routing, addresses), the full request-to-capability table, existing declared-data sites | `references/capabilities.md` · https://simple-host.app/v1/skills/website-deploy-builder/references/capabilities.md |
| Example prompts for another agent, the deploy handoff, home page, file sizes, each person's records worked example | `references/handoff-and-examples.md` · https://simple-host.app/v1/skills/website-deploy-builder/references/handoff-and-examples.md |
| Presets, custom access matrices and the page helper in full | the `website-deploy` skill's `references/storage.md` · https://simple-host.app/v1/skills/website-deploy/references/storage.md |
| Simple Hack team or event sites | the separate Simple Hack `website-deploy-builder` skill |
