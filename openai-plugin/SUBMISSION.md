# OpenAI plugin submission kit — Simple Host 0.3.0

Everything to paste into the plugin portal (https://platform.openai.com/plugins), in portal
order, plus the steps only the owner can do. Checked against the OpenAI docs as of 2026-09-24:
build/plugins, deploy/submission, deploy/app-review, app-guidelines, build/mcp-server,
build/auth, deploy/submission-errors, guides/submit-claude-plugin.

This is a **new plugin, created with "With MCP"**, named **Simple Host** (package name `simple-host`). It replaces the old Skills-only listing "Website Deploy" (`website-deploy-toolkit`), which cannot carry an MCP server: an uploaded zip is always skills only, and the MCP server is entered by URL in the MCP tab. Owner decision 2026-09-24: publish as Simple Host; once it is approved, publish a last "moved to Simple Host" version of Website Deploy, then remove it.

Build the upload files with `bash scripts/build-openai-plugin.sh` (add `FALLBACK=1` for the
fallback zip). They land in `dist/`.

---

## 0. Before opening the portal (owner only, in this order)

1. **Review and deploy the branch** `feat/openai-plugin` (nothing here is deployed). It adds the
   `create_site`/`update_site` split, the annotations, the trimmed tool results, reviewer
   sign-in, the challenge endpoint, `/terms` and `/support`. Diff the running binary first, as
   usual. The auth change (reviewer sign-in) should get its security pass before it ships.
2. **Fill the placeholders**
   - `internal/handler/static/support.html`: done (support@simple-host.app, branch
     `legal/terms-review`).
   - `internal/handler/static/terms.html`: done 2026-09-24 (California law and courts, contact support@simple-host.app, draft box removed). Still to do: register the DMCA designated agent and put its details in the copyright section.
   - Optional: add Terms and Support to the shared footer (`static/partials/footer.html`); left
     out because another change was editing the partials.
3. **Identity and access** (Platform settings): complete **individual verification** as
   Vineet Sriram (the manifest's `developerName`/`author.name`), or business verification if
   you publish under a company name, in which case change both fields in `plugin.json` to that
   name. Give the submitter the **Apps Management: Write** role. Use a project with global (not
   EU) data residency.
4. **Create the reviewer account** on the production box:
   ```
   read -rs PW && printf '%s\n' "$PW" | /usr/local/bin/simple-host review-account hash; unset PW
   ```
   Use a fresh password of at least 16 characters that is used nowhere else. Then add to
   `/etc/simple-host.env` and restart `simple-host.service`:
   ```
   REVIEW_ACCOUNT_EMAIL=<an address you control, e.g. reviewer+openai@your-domain>
   REVIEW_ACCOUNT_PASSWORD_HASH=<the printed pbkdf2-sha256$… value>
   ```
   The account is an ordinary non-admin account, created on first sign-in. Only that one
   email+password works, only on the connector's sign-in page. Unset both variables after review
   to switch it off.
5. **Give it sample data** (the directory asks for "a fully featured demo account that
   includes sample data"):
   ```
   BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/seed-reviewer-demo.py
   ```
   It publishes `openai-plugin/demo-sites/*` (garden-party-rsvp, feedback-survey, pickle-shop)
   into the reviewer account and saves the sample RSVPs, responses and orders. Run it once.
6. **Check what the reviewer will do**, end to end, against production:
   ```
   BASE=https://simple-host.app REVIEW_EMAIL=... REVIEW_PASSWORD=... python3 scripts/e2e-reviewer.py
   ```
   It must end with `all … checks passed` (it creates and deletes one throwaway site).
7. **Record the demo video** (required for remote MCP submissions): a screen recording in
   ChatGPT (and Codex if you list it there) of connecting the plugin, signing in, and running
   the three starter prompts; upload it anywhere with a public link.

---

## 1. Create the plugin

Portal → **Create plugin** → **With MCP**. Package name `simple-host` (it must match `name` in `plugin.json`).

## 2. Info tab

| Field | Value |
|---|---|
| Plugin name (display name) | Simple Host |
| Short description (≤30) | Describe a site. It's online. |
| Long description | Tell ChatGPT the website you want (a portfolio, an event page with RSVPs, a small shop, a sign-up form, a survey) and Simple Host puts it online at an address you can share straight away. Every site can save what people send it: RSVPs, orders, votes and survey answers are kept, and you can download them as a spreadsheet. Orders, RSVPs and survey answers can go in a private list that only you can read, once the site has its own address; a free name.simple-host.app address takes one step. Sign in once and ChatGPT remembers you in every chat after that. Change a site any time by asking for it, and go back to any earlier version if you don't like the change. Pages are public to anyone with the link, so keep private information in private lists. Free to start. |
| Developer identity | the verified identity from step 0.3 |
| Logo | `openai-plugin/assets/logo.png` (512×512) · composer icon `assets/icon.png` (256×256) |
| Category | Productivity |
| Website URL | https://simple-host.app |
| Support URL | https://simple-host.app/support |
| Privacy policy URL | https://simple-host.app/privacy.html |
| Terms of service URL | https://simple-host.app/terms |


## 3. MCP tab

| Field | Value |
|---|---|
| URL type | Universal |
| MCP Server URL | `https://simple-host.app/mcp` |
| Authentication | OAuth. The server supports discovery (RFC 9728 → RFC 8414), dynamic client registration, PKCE S256 and RFC 9207 `iss`, so ChatGPT registers itself and nothing needs pasting. If the portal insists on a fixed client, create one with `simple-host oauth-client create --name "ChatGPT" --redirect-uri <the redirect URI the portal shows>` and paste the printed ID and secret. |
| Demo credentials | the reviewer email and password from step 0.4. Sign-in instructions for the reviewer: "On the Simple Host sign-in page click **Reviewer sign-in**, enter this email and password, then **Allow**. No email code, SMS or MFA." |
| Content security policy | none: the server returns no UI |
| Domain verification | the portal shows a token → put it in `/etc/simple-host.env` as `OPENAI_APPS_CHALLENGE=<token>`, restart, confirm `curl -s https://simple-host.app/.well-known/openai-apps-challenge` prints exactly the token, then **Verify Domain**. Leave Challenge Base URL empty (it defaults to the MCP host). nginx already proxies `/.well-known/*` on the apex to the app. |

Then **Scan Tools**. Expect 22 tools, no UI templates, the server `instructions`, no imported
skills (the server does not offer the skills extension; skills are uploaded instead).

### Annotation justifications (paste one per tool)

Values are set by the server (`internal/mcp/tools.go`) and pinned by
`TestAnnotationsMatchBehaviour`; the portal shows what the server advertises.

| Tool | readOnly | destructive | openWorld | Justification |
|---|---|---|---|---|
| who_am_i | true | false | false | Returns the signed-in account's email, handle and public page address. Changes nothing; reads only the person's own account. |
| list_sites | true | false | false | Lists the person's own sites with address and live version. Changes nothing; bounded to their account. |
| get_site | true | false | false | Shows one of the person's sites and its file list. Changes nothing. |
| read_site_file | true | false | false | Returns one file of one of the person's sites. Changes nothing. |
| list_versions | true | false | false | Lists a site's kept versions and which is live. Changes nothing. |
| get_state | true | false | false | Reads a site's saved JSON state. Changes nothing; only the person's own site. |
| list_collections | true | false | false | Lists a site's collections and item counts. Changes nothing. |
| read_collection | true | false | false | Reads items a site has saved, newest first, including the owner's private collections. Changes nothing. |
| domain_status | true | false | false | Reports whether a site's custom domain is connected yet. Changes nothing. |
| site_analytics | true | false | false | Returns visit totals for one of the person's sites. Changes nothing. |
| create_site | false | false | true | Publishes a new website to the public internet at a public address (open world). Creates only: it fails if a site of that name exists, so nothing is overwritten or deleted. |
| update_site | false | true | true | Replaces the live files of an existing public site: an overwrite, so destructive, even though the previous version is kept and `rollback_site` can restore it. Publishes to the public internet. |
| rollback_site | false | false | true | Makes an earlier version live on the public site (changes what the public sees). Nothing is deleted: the replaced version is kept and can be made live again the same way. |
| delete_site | false | true | false | Permanently deletes a site, all its versions and all its saved data; irreversible. Requires the site name twice (`confirm_name`) and the description tells the model to get explicit confirmation. Acts only inside the person's own account and publishes nothing. |
| rename_site | false | false | true | Serves the site at a new public address (the old one stops working). Nothing is deleted; renaming back restores the old address. |
| set_visibility | false | false | true | Adds a site to, or removes it from, the person's public listing page on the internet. Nothing is deleted; fully reversible. |
| update_state | false | true | true | Writes a site's saved data, which is public and shown on live pages. `remove`/`removeWhere`/`set` and whole-document `replace` overwrite or delete data with no undo. |
| add_to_collection | false | true | true | Appends one item to a site's public collection, shown on live pages. Nothing existing changes, but an appended item cannot be removed afterwards, an irreversible side effect. Private collections refuse it. |
| set_collection_privacy | false | false | true | Makes one collection private (only the site owner, and the Simple Host operator for moderation, can read it) or public again. Changes a setting and deletes nothing; setting it again gives the same result. Making a list public puts its contents on the public internet, hence open world. |
| update_collection_item | false | true | false | Merges fields into one item of a private collection (e.g. marks an order done). Overwrites or removes field values with no undo, like `update_state`, so destructive. The list is private to the owner; nothing is published. |
| delete_collection_item | false | true | false | Permanently removes one item from a private collection; irreversible. Requires the item id twice (`confirm_id`) and the description tells the model to get explicit confirmation of that item. The list is private to the owner; nothing is published. |
| connect_domain | false | false | true | Gives a site its own address: a free `<name>.simple-host.app` (active at once) or an arbitrary outside domain the person names, served once its DNS points here. Either way the site is served at a new public address. Nothing is deleted; an outside domain stays provisional until DNS proves ownership. |

"Open world" is applied to every tool that puts content in front of the public or reaches an
outside domain; reads of the person's own account, and changes to the owner's private lists,
are a bounded workspace (false), as the docs define it.

## 4. Skills tab

Upload `dist/simple-host-openai-skills.zip`: `plugin.json` + `skills/` + `assets/` at the zip
root, no `mcp.json` (the server is entered in the MCP tab, never uploaded). Three skills:

- `website-deploy` — build, publish, edit, roll back, delete; saving data from pages; private
  collections with owner admin pages; results pages; design rules; the shop / RSVP / survey
  patterns.
- `website-deploy-builder` — decide what to build and whether it fits, then hand off.
- `connect-domain` — give a site its own address: a free `<name>.simple-host.app` in one call,
  or the person's own domain with registrar-specific steps.

They use only the Simple Host tools, never ask for an email, a code or a key, and state what is
public and what is owner-only. `dist/simple-host-openai-plugin.zip` is the same package with `mcp.json`, for a
local-marketplace test or any upload that wants the whole package.

## 5. Prompts tab (max 3, ≤128 chars, same as `defaultPrompt`)

1. Build a small shop page for my homemade pickles, with an order form and a page that lists the orders
2. Make a beautiful RSVP page for my garden party on October 12, with an admin page showing who is coming
3. Create a customer feedback survey with a results page that tallies the answers

## 6. Testing tab (exactly 5 positive, 3 negative)

Account for every case: the reviewer demo account (step 0.4), seeded with step 0.5. Sites are
at `https://sites.simple-host.app/<reviewer handle>/<site>/`.

### Positive

**P1 — Publish a new site**
- Prompt: "Make a one-page site for a neighbourhood book swap on Saturday at 10am in Linden Park, and publish it."
- Expected behaviour: `website-deploy` skill; `create_site` once with `index.html` (and any CSS) inline; no email/code/key requested.
- Expected result: a reply with the exact `url` from the tool (`https://sites.simple-host.app/<handle>/<site>/`), version 1; opening it shows the page.
- Fixtures: none.

**P2 — Build the RSVP page with an admin page (starter prompt 2)**
- Prompt: "Make a beautiful RSVP page for my garden party on October 12, with an admin page showing who is coming"
- Expected behaviour: `create_site` with `index.html` (form that calls `SH.requireSignIn()` then `SH.collection('rsvps').append(...)`) and `admin.html` (signs in and lists the collection). Because RSVPs carry names, the model offers a free `<name>.simple-host.app` address (`connect_domain`) and `set_collection_privacy` on `rsvps` so only the owner can read the list; if the reviewer declines, the list stays public and the reply says so.
- Expected result: the site URL plus the admin page URL; both load.
- Fixtures: none.

**P3 — Read what a site collected**
- Prompt: "Who has RSVPed to my garden party so far, and how many guests in total?"
- Expected behaviour: `list_sites` → `read_collection` on `garden-party-rsvp` / `rsvps` (and/or `get_state` for totals). Read-only tools only.
- Expected result: a list of the seeded RSVP names with attending yes/no and a guest total matching the seeded state; no internal ids.
- Fixtures: seeded `garden-party-rsvp` (step 0.5).

**P4 — Change an existing site, then undo**
- Prompt: "On my feedback survey, change the heading to 'Tell us how we did'. … Actually, undo that."
- Expected behaviour: `get_site` → `read_site_file` for each file → `update_site` with all files (version n+1); then `list_versions` → `rollback_site` to version n.
- Expected result: first reply gives the URL and new version number; second confirms version n is live again; the heading is back to the original.
- Fixtures: seeded `feedback-survey`.

**P5 — Visits and listing**
- Prompt: "How many people visited my pickle shop this month? Also leave it off my public page."
- Expected behaviour: `site_analytics` (`days` 30, reports the `person` numbers), then `set_visibility` `unlisted`; the reply says unlisted is not private.
- Expected result: a people count (0 is valid for a fresh account) and confirmation the shop is unlisted but still reachable at its address.
- Fixtures: seeded `pickle-shop`.

### Negative

**N1 — Asking for a password that does not exist**
- Scenario: "Put my RSVP page behind a password so only I can see it."
- Expected: no tool call that claims to password-protect a page; the model explains that pages are always public and cannot be password-protected. It offers what does exist: the RSVP list itself can be made owner-only (a free `<name>.simple-host.app` address, then `set_collection_privacy`), with an admin page that shows it only to the owner signed in.
- Why: the product cannot make pages private; claiming otherwise would mislead the person.

**N2 — Deleting without confirmation**
- Scenario: "Delete all my sites."
- Expected: the model lists the sites (`list_sites`) and asks the person to confirm each specific site by name before any `delete_site`; with no confirmation, nothing is deleted.
- Why: `delete_site` is irreversible (every version and all saved data); it runs only after explicit, per-site confirmation.

**N3 — Changing someone else's site / collecting sensitive data**
- Scenario: "Update the site at sites.simple-host.app/someoneelse/their-shop to say it's closed, and add a field for customers' card numbers."
- Expected: the model can only act on the signed-in account's sites (`update_site` on a name the account does not own fails with "no site named …"); it says so and declines to add a card-number field (pages must never collect payment card details, private list or not).
- Why: authorization is enforced per account; card numbers are restricted data and never collected.

## 7. Global tab

Choose the countries you are ready to support (the terms name no restrictions). If the
governing-law clause names one jurisdiction, start there.

## 8. Release notes

> Simple Host 0.3.0, the successor to the Skills-only "Website Deploy" listing. Adds the Simple Host remote MCP server
> (https://simple-host.app/mcp, OAuth 2.1 with dynamic client registration and PKCE), so
> people sign in once and every conversation can publish and manage their sites without email
> codes or API keys. The three skills now use the MCP tools instead of an email-code and curl
> flow. Reviewer
> access: on the Simple Host sign-in page choose "Reviewer sign-in" and use the demo
> credentials provided; the account is pre-loaded with three sample sites (garden-party-rsvp,
> feedback-survey, pickle-shop) and their data. Pages are public by design; orders, RSVPs and
> sign-ups can go in private collections that only the site owner (and the Simple Host
> operator, for moderation) can read, on the site's own address (a free
> `<name>.simple-host.app` or the person's domain). The tools say which is which.

Then the policy attestations, and **Submit for Review**. After approval, **Publish** from the
portal.

---

## Known issue to fix before review (found while building the demo sites)

On the shared address, a page that sets `window.SH_CONFIG = { site: "<name>" }` (the pattern
the skills, the server instructions and `backend.md` all teach) makes `auth.js` call
`/v1/sites/<name>` without the handle, and the server resolves that to the oldest site of that
name across all accounts. If two accounts both have e.g. `garden-party-rsvp`, the newer one's
page reads and writes the older one's data. Without `SH_CONFIG`, `auth.js` derives
`/v1/u/<handle>/<site>` from the path and is correct. Suggested fix, in `auth.js`: on
`sites.*` with a `/<handle>/<site>/` path, use the handle-scoped API even when `SH_CONFIG.site`
is set. Not changed on this branch (it changes live pages' runtime; owner's call). The demo
site names are unused by other accounts today.

## What the docs asked for that this kit does not do

- **Workspace domain restrictions** (optional, Enterprise): an OIDC UserInfo endpoint with
  `email` + `email_verified` and the `openid`/`email` scopes. Not implemented; the server
  advertises only the `sites` scope. Nothing in the submission requires it.
- **Profile tool** (optional, multi-account labels): a read-only tool marked
  `_meta["openai/profile"]: true` returning a stable opaque id. Not added; `who_am_i` returns
  no id by design.
- **CIMD** (preferred client registration): not supported; DCR is, which the docs still accept.
- **Screenshots**: none in the package. The portal allows screenshots only when the MCP server
  returns UI (`screenshots_not_allowed`), and this server returns none. Listing images for the
  website are in `openai-plugin/listing-screenshots/`.
