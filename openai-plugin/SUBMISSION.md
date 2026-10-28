# Simple Host 1.0.2 resubmission kit

This kit prepares the resubmission after the 2026-10-28 rejection: "A plugin call returns personal identifiers that isn't required for the user's request." It is not a record of deployment, completed reviewer tests, or portal submission. Submit only after the companion server fixes are deployed and verified with the reviewer account.

## Changes since rejection

- Personal data is minimised in connector results. `who_am_i` includes the account email only on request. Visitors use per-site opaque references; `storage_visitor_emails` resolves only the requested visitors when the owner asks who sent an entry.
- Viewer changes return only what changed, and signed link tokens contain no readable internal IDs. Simple Hack applies equivalent minimisation to account, event, team, viewer, and storage results.
- ChatGPT and Codex connections list every operation as its own tool, with no discovery tool or generic executor. Tool annotations describe actual reads, writes, destructive effects, and external access.
- Instructions act within the person's request and no longer trigger unrelated tool calls. OpenAI skills call named tools directly, omit other-product installation steering, and contain no registrar credential workflow.
- The privacy policy describes current storage, connector disclosures, visitor references, and notifications. The consent screen describes file and private-data reads, downloads, visitor counts, and notification management.
- Listing copy describes hosting what the person or their AI made, with current KV, SQLite, and file storage. It contains no pricing promotion or promise of 30-day history for current storage.

## Portal steps

1. Open the existing Simple Host draft at https://platform.openai.com/plugins. Keep its registered package identity, `app-6ab570c7e41c81918c88f0026f2e1895`, and choose version `1.0.2`. Do not create a new package or rename it.
2. Run `bash scripts/build-openai-plugin.sh`. The complete package is `dist/simple-host-openai-plugin.zip`. The Skills tab takes `dist/simple-host-openai-skills.zip`, which contains only skill folders. Use the complete ZIP where the portal requests the corrected plugin package.
3. Copy the listing fields below. Upload `openai-plugin/assets/logo.png` (512 by 512) and `openai-plugin/assets/icon.png` (256 by 256). Both use the approved Rounded mark.
4. Configure the MCP tab as below. The submission JSON lists 55 tools, matching the ChatGPT tool inventory with MCP Events enabled. The Go test `TestSubmissionToolsMatchChatGPTListing` checks tool names and all three annotation flags against `tools/list`. Confirm that the deployed server matches before submitting.
5. Refresh or rescan the portal's tools before resubmitting. A cached tool inventory from the rejected version is not sufficient. Check the refreshed inventory for both ChatGPT and Codex connections.
6. Enter the reviewer email and the password supplied through the portal's private credentials fields. Never put the password in this kit, the ZIP, a prompt, or a recording. Verify the existing demo fixtures and the additional signed-in RSVP fixture below.
7. Run all five positive and three negative cases in ChatGPT using the reviewer account. Record actual outcomes, attach a demo video URL accessible to reviewers, and verify the policy links and sign-in flow. These live checks remain required; local package validation does not replace them.
8. Paste the response to the reviewer below and resubmit the corrected package.

## Listing fields

- **Package name:** app-6ab570c7e41c81918c88f0026f2e1895
- **Version:** 1.0.2
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

## Reviewer sign-in

Start the Simple Host connection in ChatGPT. On the Simple Host sign-in page, expand **Other ways to sign in**, enter the reviewer email **openai-review@simple-host.app** and the password supplied in the portal, choose **Sign in**, then **Allow**. No email code or 2FA.

The sign-in page is served at `https://simple-host.app/oauth/authorize` with the connection's OAuth parameters. Start it from ChatGPT rather than opening the address without those parameters. The exact disclosure label is in `internal/handler/static/connect.html`. Verify that reviewer sign-in is enabled before resubmitting.

## Integration prerequisites

The privacy page's daily analytics hash wording requires the companion analytics change before release. At package preparation time, `internal/analytics/ingest.go` still derives a lasting hash. Verify daily rotation, absence of stored IPs in analytics, and no hash disclosure before publishing the policy. This task changes package and page text, not Go results or analytics behavior.

The Claude skill sources remain outside this change. `simple-host-website/skills/connect-domain/SKILL.md` and `simple-host-website/skills/connect-domain/references/registrars.md` still contain the registrar credential path, as do their generated copies under `plugins/simple-host/skills/connect-domain/`. The OpenAI skills are separately maintained copies; `scripts/build-openai-plugin.sh` packages them directly, and `scripts/sync-claude-plugin.sh` does not generate them.

## Demo fixtures and execution status

The reviewer account uses `garden-party-rsvp`, `feedback-survey`, and `pickle-shop`. Their files and sample rows are in `openai-plugin/demo-sites`; `scripts/seed-reviewer-demo.py` documents seeding. The seed contains seven RSVPs, eight feedback responses, and four orders. Do not rerun the seeder on an already seeded account: it inserts duplicate rows.

The seeder's rows are owner-attributed and have no visitor identity. Before running P5, an operator must submit one additional RSVP through the garden party page while signed in as a controlled test visitor, using the name **Reviewer identity check**. Confirm that exactly one row has that name and a nonempty visitor reference. Keep that visitor's sign-in credentials outside chat and the package. Use the trusted browser sign-in flow; do not invent an email for an owner-attributed seed row. With one such extra entry, P4 counts eight RSVPs; otherwise it must report the actual count. For P1, attach index.html and style.css from openai-plugin/demo-sites/garden-party-rsvp as the supplied replacement files. They target the existing seeded site, so their sign-in configuration and storage remain valid.

The cases below are specifications, not executed results. Reviewer credentials, production feature flags, live fixtures, portal scans, and the demo video must be verified during the separately authorized resubmission. The JSON cases are identical to these fields.

## Five positive test cases

### P1

**Scenario:** Publish supplied replacement page files to the seeded garden party site.

**Prompt:** Publish these supplied page files to my existing garden-party-rsvp site. Keep its saved RSVPs.

**Expected tools:** get_site, update_site

**Expected result:** Publishes the supplied index.html and style.css to garden-party-rsvp and returns its address. Existing RSVP storage is unchanged. No unsolicited photo, notification, or identity calls.


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

**Scenario:** Identify only the signed-in test visitor who submitted one RSVP.

**Prompt:** Who submitted the RSVP named "Reviewer identity check" on garden-party-rsvp? Give me only that person's sign-in email.

**Expected tools:** storage_sql_query, storage_visitor_emails

**Expected result:** Queries only visitor_id for the single RSVP named Reviewer identity check, then passes that per-site opaque reference to storage_visitor_emails for garden-party-rsvp. Returns only that person's email, with no other visitor emails or internal account IDs. Requires the signed-in RSVP fixture described in this kit.


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

## Response to the reviewer

We addressed the 2026-10-28 finding by limiting connector results to data needed for the requested operation. Account email is returned only on request, and visitors normally appear as opaque references scoped to one site. When an owner asks who sent an entry, the connector resolves only the requested visitor's email. Viewer edits return only the change, and signed links no longer expose readable internal IDs; corresponding Simple Hack results follow the same approach. ChatGPT and Codex now list each operation individually, and annotations and instructions reflect its actual behavior without unsolicited calls. We removed registrar credential instructions from the OpenAI skills and updated the listing, privacy policy, and consent screen. The resubmission includes matching positive and negative test cases, including a count with no emails and an explicitly requested identity lookup.
