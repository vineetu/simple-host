# Simple Host 1.0.0 new-plugin submission kit

This kit creates a **new** ChatGPT plugin, package `simple-host`, rather than resubmitting to
the old draft `app-6ab570c7e41c81918c88f0026f2e1895`. It is not a record of deployment,
completed reviewer tests, or portal submission.

## Why a new plugin

The old draft was created through the portal's legacy form before the ZIP/MCP workflow
existed. Uploading the corrected 1.0.2 package to it fails: "Adding, removing, or replacing an
MCP server isn't supported for existing plugins. Restore the original MCP servers or create a
new plugin." A reupload-to-rebind workaround some developers report (OpenAI community threads
1410075 and 1406571) is a portal bug affecting only some legacy-form apps and is not guaranteed;
both threads land on the same resolution: create a new plugin through the current ZIP flow.
Decision 2026-10-29: do that. The old draft is left as is; it does not block a new plugin, and
nothing further will be uploaded to it.

Everything else — the connector, the three skills, the reviewer account, the demo fixtures and
the test cases below — carries over unchanged from 1.0.2. Only the package identity is new:
`simple-host` instead of the old draft's bound `app-...` id, starting at version `1.0.0` as a
first submission.

## Portal steps

1. Go to https://platform.openai.com/plugins → **Create plugin** → **With MCP**. Package name
   `simple-host` (must match `name` in `plugin.json`).
2. Run `bash scripts/build-openai-plugin.sh`. The complete package is
   `dist/simple-host-openai-plugin.zip`. The Skills tab takes
   `dist/simple-host-openai-skills.zip`, which contains only skill folders.
3. Copy the listing fields below. Upload `openai-plugin/assets/logo.png` (512 by 512) and
   `openai-plugin/assets/icon.png` (256 by 256). Both use the approved Rounded mark.
4. Configure the MCP tab as below, then **Scan Tools**. Expect 55 tools, matching the ChatGPT
   tool inventory with MCP Events enabled. The Go test `TestSubmissionToolsMatchChatGPTListing`
   checks tool names and all three annotation flags against `tools/list`. Confirm the deployed
   server matches before submitting. Domain verification: the portal shows a token for this new
   app; put it in `/etc/simple-host.env` as `OPENAI_APPS_CHALLENGE=<token>`, restart, confirm
   `curl -s https://simple-host.app/.well-known/openai-apps-challenge` prints exactly the token,
   then **Verify Domain**.
5. Enter the reviewer email and the password supplied through the portal's private credentials
   fields. Never put the password in this kit, the ZIP, a prompt, or a recording. Verify the
   existing demo fixtures below.
6. Run all five positive and three negative cases in ChatGPT using the reviewer account. Record
   actual outcomes, attach a demo video URL accessible to reviewers, and verify the policy links
   and sign-in flow.
7. Complete the review information (test cases, demo video), submit for review, and on approval
   choose when to publish.

## Listing fields

- **Package name:** simple-host
- **Version:** 1.0.0
- **Display name:** Simple Host
- **Developer:** Vineet Sriram
- **Category:** Productivity
- **Short description:** Host and manage your websites
- **Package description:** Host websites you or your AI have made, at their own addresses. Publish and update files through the Simple Host connector, manage saved data and access, connect domains, and read visitor counts.
- **Website:** https://simple-host.app
- **Support:** https://simple-host.app/support
- **Privacy policy:** https://simple-host.app/privacy.html
- **Terms:** https://simple-host.app/terms
- **Brand color:** #157e8a
- **Dark brand color:** #3fb0bb

### Long description

Simple Host hosts websites you or your AI have made. Your AI puts the files online through the connector, at an address you can share, and Simple Host keeps them there. Publish a page you have made, update an existing site, or ask to see its saved entries.

Each site can save key-value data (KV), SQLite tables, and files. Access presets control who can read, add, edit, and delete data. New resources are private by default. Use an inbox for submissions only the owner reads, records for each signed-in visitor's entries with owner access, or public data for content everyone may read. Visitors sign in with Google or an emailed code; no password or sign-up form.

Update site files, preview a saved version, or restore an earlier version. The four latest site versions are kept by default, and deleted sites can be restored for 7 days. Current storage stays until you delete it or its site; it does not have automatic 30-day change history.

Connect your own domain with HTTPS or choose a short name.simple-host.app address. Manage who can open a site, read visitor counts and top pages, and download a copy of your site.

Connect your Simple Host account through the app's sign-in flow. You can revoke the connection from the Simple Host dashboard. Simple Host is open source, made and run by Vineet Sriram.

### Starter prompts

- Put the page we made online on Simple Host.
- Update the opening hours on my existing site.
- Show the saved entries from my garden party RSVP site.

### Capabilities

- Publish websites, each at its own public address
- Edit, rename, preview, roll back, and delete your sites
- Save bookings, quote requests, waitlists, and orders
- Choose who can read and add saved data, with simple presets
- Let visitors sign in so each person sees only their own records
- Lock a site with a passcode or open it only to named people
- Connect your own domain or a short name.simple-host.app address
- See visitors, top pages, and where they came from
- Download a copy of any site

## MCP tab

- Tool inventory: 55 individually listed operations, including `list_notifications` and `remove_notification`. Neither `find_tools` nor `run_tool` is listed. The annotations and their justifications are in `chatgpt-app-submission.json`.
- Server: `https://simple-host.app/mcp`, Streamable HTTP, as in `mcp.json`.
- Authentication: OAuth with dynamic client registration (DCR), authorization code flow, and PKCE (S256). Use the server's discovery metadata; do not paste an API key or a client secret into the package.
- Permissions: the consent screen covers publishing and managing sites, reading site files and saved data including private entries, downloading copies, reading visitor counts, changing access and addresses, and managing notifications.
- Apps UI: MCP Apps screens are off by default and are not shown to reviewers. The server has optional screens for sites, analytics, photos, and previews, but the submitted configuration uses tool text and structured results. Do not claim that the server has no UI implementation.

## Privacy and data handling

AgentCat (agentcat.com) processes connector usage analytics. For each tool call it receives the arguments and result, with passcodes, keys, and file contents left out by us and email addresses and other identifiers removed by AgentCat's redaction. It never receives the IP address a person connects from. Every connection, ChatGPT included, may optionally supply a short stated reason for the call; it is free text, carries no names, emails, or secrets by instruction, and goes through the same redaction before storage.

## Reviewer sign-in

Start the Simple Host connection in ChatGPT. On the Simple Host sign-in page, expand **Other ways to sign in**, enter the reviewer email **openai-review@simple-host.app** and the password supplied in the portal, choose **Sign in**, then **Allow**. No email code or 2FA.

The sign-in page is served at `https://simple-host.app/oauth/authorize` with the connection's OAuth parameters. Start it from ChatGPT rather than opening the address without those parameters. The exact disclosure label is in `internal/handler/static/connect.html`. Verify that reviewer sign-in is enabled before submitting.

## Integration prerequisites

The privacy page's daily analytics hash wording requires the companion analytics change before release. At package preparation time, `internal/analytics/ingest.go` still derives a lasting hash. Verify daily rotation, absence of stored IPs in analytics, and no hash disclosure before publishing the policy. This task changes package and page text, not Go results or analytics behavior.

The Claude skill sources remain outside this change. `simple-host-website/skills/connect-domain/SKILL.md` and `simple-host-website/skills/connect-domain/references/registrars.md` still contain the registrar credential path, as do their generated copies under `plugins/simple-host/skills/connect-domain/`. The OpenAI skills are separately maintained copies; `scripts/build-openai-plugin.sh` packages them directly, and `scripts/sync-claude-plugin.sh` does not generate them.

## Demo fixtures and execution status

The reviewer account uses `garden-party-rsvp`, `feedback-survey`, and `pickle-shop`. Their files and sample rows are in `openai-plugin/demo-sites`; `scripts/seed-reviewer-demo.py` documents seeding. The seed contains seven RSVPs, eight feedback responses, and four orders. Do not rerun the seeder on an already seeded account: it inserts duplicate rows.

P4 must report the actual RSVP count (seven in the seed). P1 creates a new site in the reviewer account; the reviewer can ask to delete it afterwards. An owner who asks who sent a particular entry gets that one visitor's email through storage_visitor_emails; the seeded rows were saved by the owner, so they carry no visitor reference and that path is covered by unit tests instead of a portal case.

2026-10-28, against production build b4c65325: `scripts/e2e-reviewer.py` signed in as the reviewer (registered as ChatGPT), saw 55 tools listed individually with no generic executor, and ran P1 to P5 at the tool level on the seeded sites; all 45 checks passed, including no account email, display name, or account ID in any default result. The negative cases and the demo video still need a run in ChatGPT itself after the tool Refresh.

## Five positive test cases

### P1

**Scenario:** Put a one-page site online as a new site.

**Prompt:** Put a one-page website online for my bakery, Rise and Shine, as a new site named rise-and-shine. It should list our opening hours: Monday to Friday 7 to 3, Saturday 8 to 1.

**Expected tools:** create_site

**Expected result:** Says the site name and address and that anyone with the link can open it, waits for a yes, then calls create_site once and returns the live https://rise-and-shine.<handle>.simple-host.app/ address. The page shows the bakery name and hours. No photo, notification, or identity calls, and no emails in the result.

### P2

**Scenario:** Update the seeded feedback survey and undo the requested change.

**Prompt:** On feedback-survey, change the heading to "Tell us how we did". Then, in the same chat: Undo that change.

**Expected tools:** get_site, read_site_file, update_site, list_versions, rollback_site

**Expected result:** The survey shows the requested heading. After confirmation of the rollback, the previous version restores the original heading. No account or visitor emails are returned.


### P3

**Scenario:** Read only the requested order details from the seeded pickle shop.

**Prompt:** Show the orders saved by pickle-shop, including items, totals, and status.

**Expected tools:** storage_sql_query

**Expected result:** Queries only items, total_cents, and status in the orders resource and returns the four seeded orders. No customer names, contacts, emails, or unrelated records are returned, and nothing is changed.


### P4

**Scenario:** Count RSVPs without identifying visitors.

**Prompt:** How many RSVPs do I have on garden-party-rsvp?

**Expected tools:** storage_sql_query

**Expected result:** Returns COUNT(*) from the rsvps table in the rsvps resource, matching the current fixture count. Returns no emails, names, or visitor references. Does not call who_am_i or storage_visitor_emails.


### P5

**Scenario:** Visitor numbers without per-visitor detail.

**Prompt:** How many people visited feedback-survey this week, and which pages did they read?

**Expected tools:** site_analytics

**Expected result:** Returns visit totals, daily counts, and top pages for feedback-survey. The result has no IP addresses, locations of individuals, emails, or visitor identifiers. Nothing is changed.


## Three negative test cases

### N1

**Scenario:** Brainstorming does not authorize hosting or account access.

**Prompt:** Help me brainstorm names for my new hair salon.

**Expected tools:** None

**Expected result:** Answers in chat with name ideas. Does not call any Simple Host tool or publish anything.


### N2

**Scenario:** An uploaded server application is outside the hosting service.

**Prompt:** Deploy my Node.js API with a Postgres database to production.

**Expected tools:** None

**Expected result:** Explains that Simple Host serves static files and its built-in storage; it does not run your own server code. No deployment occurs. Offers to discuss a static frontend if useful.


### N3

**Scenario:** Summarizing an unrelated external website is outside the connector.

**Prompt:** Summarize what is on https://example.com for me.

**Expected tools:** None

**Expected result:** Explains that Simple Host does not retrieve arbitrary external pages. Makes no Simple Host call and does not direct another connector to run.

