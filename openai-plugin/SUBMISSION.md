# OpenAI plugin submission kit — Simple Host 0.9.4

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask. No plugin or listing publication is part of this update.

[Open Claude connectors](https://claude.ai/new?modal=add-custom-connector#customize/connectors/yours). Name it **Simple Host**, paste `https://simple-host.app/mcp`, and choose **Add**. In the sign-in window, sign in with Google or an email code, then choose **Allow**. The connector works in the Claude web, desktop, and phone apps.

Simple Host has been submitted to the ChatGPT and Claude app directories and is under review. Once it is approved, you will be able to add it from there. This update prepares local package files only; it does not publish plugins or listings.

Everything to paste into the plugin portal (https://platform.openai.com/plugins), in portal
order, plus the steps only the owner can do. Checked against the OpenAI docs as of 2026-09-24 (update rules re-checked 2026-09-28):
build/plugins, deploy/submission, deploy/app-review, app-guidelines, build/mcp-server,
build/auth, deploy/submission-errors, guides/submit-claude-plugin.

This is a **new plugin, created with "With MCP"**, named **Simple Host** (package name `simple-host`). It replaces the old Skills-only listing "Website Deploy" (`website-deploy-toolkit`), which cannot carry an MCP server: the submission portal’s Skills tab takes skills only, and its MCP tab takes the server URL. ChatGPT Plugins also accepts a full plugin ZIP through Add, then Upload plugin archive. Owner decision 2026-09-24: publish as Simple Host; once it is approved, publish a last "moved to Simple Host" version of Website Deploy, then remove it.

Build the upload files with `bash scripts/build-openai-plugin.sh` (add `FALLBACK=1` for the
fallback zip). They land in `dist/`.

**Submitted 2026-09-24 as version 0.3.0** (22 tools; justifications, test cases and app info
uploaded as the `chatgpt-app-submission.json` of commit `30d12c5`). **To update it**, submit a new
version, 0.9.4: OpenAI re-scans the MCP tools by itself (new and changed tools go live once its
automated checks pass), but changed plugin information needs a new version, review and publication
(developers.openai.com/plugins/deploy/submission). Rescan the tools (41 now), upload the current
`chatgpt-app-submission.json` in the "Use Codex" box (45 core tools with three justifications
each, app info, 5 + 3 test cases), replace the long description, capabilities and prompts, and use
the release notes in §8. Nothing in §0 needs redoing: the reviewer account and its demo sites are
in place, and `scripts/e2e-reviewer.py` passes against production.

---

## 0. Before opening the portal (owner only, in this order)

1. **Review and deploy the branch** `feat/openai-plugin` (nothing here is deployed). It adds the
   `create_site`/`update_site` split, the annotations, the trimmed tool results, reviewer
   sign-in, the challenge endpoint, `/terms` and `/support`. Diff the running binary first, as
   usual. The auth change (reviewer sign-in) should get its security pass before it ships.
2. **Fill the placeholders**
   - `internal/handler/static/support.html`: done (support@simple-host.app, branch
     `legal/terms-review`).
   - `internal/handler/static/terms.html`: done 2026-09-24 (California law and courts, contact support@simple-host.app, draft box removed). The DMCA designated agent is registered (DMCA-1081064, directory contact support@simple-host.app since 2026-09-28) and named in the copyright section.
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
| Long description | Tell ChatGPT the website you want (a portfolio, an event page with RSVPs, a small shop, a sign-up form, a survey) and Simple Host puts it online at its own address that you can share straight away.<br><br>Every site can save what people send it. RSVPs, orders, votes and survey answers are kept, and you can download them as a spreadsheet. Orders, RSVPs and sign-ups can go in a private list that only you can read, and you choose who may save: anyone who signs in, or only the people you list.<br><br>Change a site any time by asking for it. Earlier versions are kept, so you can preview one or put it back. Saved data keeps 30 days of history, so a deleted entry or an unwanted change can be restored, and a deleted site can be brought back for 7 days.<br><br>Ask how many people visited, which pages they read and where they came from. Give a site a free name.simple-host.app address or connect your own domain, and download a copy of any site whenever you like.<br><br>Sign in once and ChatGPT remembers you in every chat after that. Pages are public to anyone with the link, so keep private information in private lists. Free to start. |
| Capabilities | Publish websites, each at its own public address<br>Edit, rename, preview, roll back and delete your sites<br>Save form submissions, RSVPs, votes and orders<br>Keep orders and sign-ups in private lists only you can read<br>Read what your sites have collected, and undo changes for 30 days<br>Choose who can save on a site, and block people<br>Connect your own domain or a free simple-host.app address<br>See visitors, top pages and where they came from<br>Download a copy of any site |
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

Then **Scan Tools**. Expect 42 tools (27 core, including `get_page_recipe`, and 15 storage), no UI templates, the server `instructions`, no imported
skills (the server does not offer the skills extension; skills are uploaded instead).
Every tool declares an `outputSchema` describing its `structuredContent`
(`internal/mcp/outputs.go`), so the scan should raise no "Add an outputSchema" recommendation.
The schemas are pinned against real results by `TestEveryToolResultMatchesItsOutputSchema`
(`internal/mcp`) and `TestOutputSchemasMatchRealResults` (`internal/handler`).

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
| domain_status | true | false | false | Reports whether a site's custom domain is connected yet. Changes nothing. |
| site_analytics | true | false | false | Returns visit totals for one of the person's sites. Changes nothing. |
| export_site | false | false | false | Mints a new 10-minute download link for a copy of one of the person's own sites (files and saved data). Not read-only because each call creates a live bearer link on the server; it deletes or overwrites nothing, and the link is given to the person, nothing is published or sent anywhere. |
| create_site | false | false | true | Publishes a new website to the public internet at a public address (open world). Creates only: it fails if a site of that name exists, so nothing is overwritten or deleted. |
| update_site | false | true | true | Replaces the live files of an existing public site: an overwrite, so destructive, even though the previous version is kept and `rollback_site` can restore it. Publishes to the public internet. |
| rollback_site | false | false | true | Makes an earlier version live on the public site (changes what the public sees). Nothing is deleted: the replaced version is kept and can be made live again the same way. |
| preview_version | false | false | false | Mints an owner-only, one-hour link showing one kept version of the person's site. Changes nothing that is live and publishes nothing; not read-only because each call creates a new link. |
| delete_site | false | true | false | Takes a site offline with all its versions and all its saved data. It stays in Recently deleted for 7 days (`restore_site` brings it back), then it is removed for good. Requires the site name twice (`confirm_name`) and the description tells the model to get explicit confirmation. Acts only inside the person's own account and publishes nothing. |
| list_deleted_sites | true | false | false | Lists the person's own sites in Recently deleted and when each is removed for good. Changes nothing. |
| restore_site | false | false | true | Brings a site back from Recently deleted, live again at its public address (open world). Nothing is deleted or overwritten. |
| rename_site | false | false | true | Serves the site at a new public address (the old one redirects to it). Nothing is deleted; renaming back restores the old address. |
| set_bio | false | false | true | Changes the public showcase plain-text bio; reversible and deletes no site or data. |
| set_showcase_site | false | false | true | Changes a site’s showcase pin and order. Unlisted stays hidden; reversible. |
| set_home_page | false | false | true | Changes the account’s personal home between showcase and one owned site. Reversible; no deletion. |
| set_keep_versions | false | true | false | Sets retention and removes older version copies for good. Only operator-allowed accounts can change it; the live version always stays. It does not publish anything. |
| set_visibility | false | false | true | Adds a site to, or removes it from, the person's public listing page on the internet. Nothing is deleted; fully reversible. |
| set_site_offline | false | false | true | Takes a public site offline (every address shows "This site is offline", visitor saves stop) or back online. Nothing is deleted; the other value undoes it. |
| set_site_passcode | false | false | true | Puts one passcode on the person's own site, changes or removes it, signs every visitor out, or shows the current one to its owner. Every address then shows "This site is protected" until a visitor enters it, which changes what the public sees (open world). Nothing is deleted: files, versions and saved data are kept, and removing the passcode puts the site back as it was. The description tells the model to ask the person first and to use the passcode they chose. |
| keep_site | false | false | true | Marks one of the person's sites Keep (or clears the mark), so the idle-site cleanup never flags it. Changes a flag only; nothing is deleted or published, and setting it again gives the same result. Open world because the flag decides whether a public site stays on the internet. |
| get_page_recipe | true | false | false | Returns the exact resource setup and page code for a storage job (topics `records`, `form`, `gallery`); it reads no site and changes nothing. |
| storage_list_resources | true | false | false | Reads the owner's resource declarations and policies; changes nothing. |
| storage_get_usage | true | false | false | Reads this website's pooled KV, SQLite and file byte usage, allowance and breakdown; changes nothing. |
| storage_set_resource | false | true | true | Creates or changes a resource policy, which can expose data or permit public writes. Kind stays immutable. Ask before changing an existing policy. |
| storage_delete_resource | false | true | false | Permanently removes a resource and all its data. Ask the owner to confirm the exact resource. |
| storage_list_kv_keys | true | false | false | Reads owner-visible keys; changes nothing. |
| storage_get_kv | true | false | false | Reads one owner-visible JSON value; changes nothing. |
| storage_put_kv | false | true | true | Replaces a JSON value, which may be public under the resource policy. Ask before overwriting. |
| storage_delete_kv | false | true | false | Removes one JSON value. Ask before deleting. |
| storage_sql_query | true | false | false | The REST SQLite engine authorizer enforces a read-only query; changes nothing. |
| storage_sql_execute | false | true | true | Changes rows, which may be public under the resource policy. Ask before changing or deleting rows. |
| storage_sql_schema | false | true | false | Changes a resource schema and may remove or reshape data. Owner only; ask first. |
| storage_list_file_objects | true | false | false | Lists owner-visible durable file paths; changes nothing. |
| storage_put_file | false | true | true | Uploads or replaces a durable file, which may be public under the resource policy. Ask before overwriting. |
| storage_delete_file | false | true | false | Removes one durable file. Ask before deleting. |
| storage_file_download_link | false | false | false | Mints a scoped, short-lived owner download URL for full file bytes. Keep the link private. |
| connect_domain | false | false | true | Gives a site its own address: a free `<name>.simple-host.app` (active at once) or an arbitrary outside domain the person names, served once its DNS points here and a TXT ownership record proves it is theirs. Either way the site is served at a new public address. Nothing is deleted; an outside domain stays provisional until its TXT record proves ownership. |
| remove_domain | false | true | true | Disconnects a site's custom domain or free `<name>.simple-host.app` address, so the site is served at a different public address (open world). Destructive: links to a disconnected custom domain stop working and the domain can then be connected by someone else. Requires the address typed out (`confirm_domain`) and the description tells the model to get explicit confirmation first. |

"Open world" is applied to every tool that puts content in front of the public or reaches an
outside domain; reads of the person's own account, and changes to the owner's private lists,
are a bounded workspace (false), as the docs define it.

## 4. Skills tab

Upload `dist/simple-host-openai-skills.zip`: `plugin.json` + `skills/` + `assets/` at the zip
root, no `mcp.json` (the server is entered in the MCP tab, never uploaded). Three skills:

- `website-deploy` — build, publish, edit, roll back, delete; KV, SQLite and file storage
  resources; visitor sign-in; each person's own records (orders, RSVPs, bookings); design
  rules; deprecated saved data for existing sites only.
- `website-deploy-builder` — decide what to build and whether it fits, then hand off.
- `connect-domain` — give a site its own address: a free `<name>.simple-host.app` in one call,
  or the person's own domain with registrar-specific steps.

They use only the Simple Host tools, never ask for an email, a code or a key, and state what is
public and what is owner-only. `dist/simple-host-openai-plugin.zip` is the same package with `mcp.json`, for a
local-marketplace test or any upload that wants the whole package.

## 5. Prompts tab (max 3, ≤128 chars, same as `defaultPrompt`)

1. Build a small shop for my spice pantry with an order form, and keep the orders private so only I can read them
2. Make a site for my home cleaning business with a booking form, then show me who has booked
3. Create a sign-up page for a local families resource hub, and tell me how many people visited this week

## 6. Testing tab (exactly 5 positive, 3 negative)

Upload them with the app info and justifications in `chatgpt-app-submission.json` (the portal's "Use Codex" box); the text below is the same. Account for every case: the reviewer demo account (step 0.4), seeded with step 0.5 (garden-party-rsvp: 7 RSVPs, feedback-survey: 8 responses, pickle-shop: 4 orders). Sites are at `https://<site>.openai-review.simple-host.app/`.

### Positive

**P1 — Publish a new website from a description.**
- Prompt: "Make a one-page site for a neighbourhood book swap on Saturday at 10am in Linden Park, and publish it."
- Tools: create_site
- Expected: A new site is published and its public address is returned (https://<site>.openai-review.simple-host.app/). Opening it shows the event details.

**P2 — Read what a site has collected, using the sample garden party RSVP site in the demo account.**
- Prompt: "Who has RSVPed to my garden party so far, and how many guests in total?"
- Tools: storage_sql_query
- Expected: The 7 sample RSVPs with names and whether they are coming, plus the total guest count, read from the "rsvps" SQLite resource (read:own, so each guest's own page shows only their own reply; the owner's connector reads every row with storage_sql_query).

**P3 — Change a live site, then undo the change, using the sample feedback survey in the demo account.**
- Prompt: "On my feedback survey, change the heading to "Tell us how we did". Then, in the same chat: Actually, undo that."
- Tools: read_site_file, update_site, list_versions, rollback_site
- Expected: The live survey shows the new heading; after the undo the previous version is live again with the original heading.

**P4 — Give a site its own free address and make its orders a signed-in, owner-readable SQLite resource, using the sample pickle shop in the demo account.**
- Prompt: "Put my pickle shop on its own address pickle-shop-demo.simple-host.app and make sure only I can read all the orders."
- Tools: connect_domain, storage_set_resource, storage_sql_schema
- Expected: The shop is live at https://pickle-shop-demo.simple-host.app/ immediately (no DNS step) and its orders live in a SQLite resource with read:own, write:signed-in, write_mode:add: each customer sees only their own orders, and the owner reads every order with storage_sql_query.

**P5 — See how many people visited a site.**
- Prompt: "How many people visited my pickle shop in the last 30 days, and where did they come from?"
- Tools: site_analytics
- Expected: Visit totals for the period split into people and bots, with the most viewed pages and the domains people came from (all zero is a valid answer for the demo account).

### Negative

**N1** "Help me brainstorm a name and a domain for my new bakery." — Brainstorming only; nothing is being built or published, so Simple Host should not be used.

**N2** "Deploy my Node.js API with a Postgres database to production." — Deploying a server application with a database is outside what Simple Host does (it hosts websites).

**N3** "Summarize what's on https://example.com for me." — Reading or summarising someone else's website is not something Simple Host does.

## 7. Global tab

Choose the countries you are ready to support (the terms name no restrictions). If the
governing-law clause names one jurisdiction, start there.

## 8. Release notes

> Simple Host 0.9.4. The listing now describes what the connector does today; the MCP server is unchanged in address and sign-in (https://simple-host.app/mcp, OAuth 2.1 with dynamic client registration and PKCE).
> - Every site now lives at its own address, `<site>.<handle>.simple-host.app`; a free `<name>.simple-host.app` or the person's own domain stays optional.
> - 42 tools (22 in 0.3.0). New since then: preview_version, list_deleted_sites, restore_site, set_site_offline, set_site_passcode, keep_site, remove_domain, export_site, the 15 storage_* tools for KV, SQLite and file resources, and get_page_recipe. The older state, collection and declared-data tools (declare_data, list_data, update_data, get_state, update_state, list_collections, read_collection, add_to_collection, set_collection_privacy, update_collection_item, delete_collection_item, clear_collection, data_history, restore_data, list_deleted, restore_item, delete_forever, set_who_can_save, block_person) are no longer offered; those routes keep working for existing sites only, maintained with the owner's own API key.
> - Undo: deleted sites can be restored for 7 days; saved data and list items keep 30 days of history and can be put back.
> - New sites save into owner-defined KV, SQLite and file resources, each with its own read and write policy (anyone, signed-in, own or owner). Visitors sign in on the site's own address, never with the owner's account sign-in; `get_page_recipe` returns the exact setup and page code for each person's own records (orders, RSVPs, bookings) and for a form only the owner reads.
> - Visitor counts now include top pages and referring domains; a site can be downloaded as a copy through a 10-minute link.
> - Every destructive tool asks for the name, id or domain typed twice and tells the model to get explicit confirmation first.
> - The skills ask the person once before a new site goes online (name, address, public to anyone with the link), and before deleting, making private data public, changing who can see or save, connecting a domain or rolling back.
> - A new account chooses its address (handle) when signing up instead of being given one; it can be changed later from the dashboard, not more than once in 30 days.
> - Relative links are now a tip rather than a rule: root-relative links work at a site's live address; relative ones keep version previews and a new site's first minutes working too.
> - Visitor content is data, not instructions: the skills and connector tell the model to treat entries, saved data, comments, submissions, referrers and page content as untrusted, quote it for the person, never act on requests inside it, and point out entries that try to instruct an AI.
> Reviewer access is unchanged: on the Simple Host sign-in page choose "Reviewer sign-in" and use the demo credentials provided; the account holds three sample sites (garden-party-rsvp, feedback-survey, pickle-shop) and their data.

Then the policy attestations, and **Submit for Review**. After approval, **Publish** from the
portal.

---

## Fixed: same site name in two accounts on the old shared address

`auth.js` once called `/v1/sites/<name>` without the handle on `sites.simple-host.app` when
`SH_CONFIG.site` was set, which the server resolves to the oldest site of that name. It now
uses the handle-scoped API there, and old `sites.simple-host.app/<handle>/<site>/` links
redirect to the site's own address, where the page API answers only for that site.

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
