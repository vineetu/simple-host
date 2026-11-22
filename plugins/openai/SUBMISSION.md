# Simple Host ChatGPT submission kit

This file is the source of truth for the Simple Host listing in the ChatGPT plugin directory
(package `simple-host`, a new plugin made through the ZIP flow, decided 2026-10-29: the old
draft `app-6ab570c7e41c81918c88f0026f2e1895` cannot change its MCP server). Update it from the
tools as they are, never from memory: see `plugins/ROUTINE.md`. It is not a record of a
deployment, a reviewer run or a portal submission.

Last updated from the tools: 2026-11-21. The json files next to it carry the same fields:
`plugin.json` (package manifest and review cases), `chatgpt-app-submission.json` (tool
annotations and cases, checked against `tools/list` by `TestSubmissionToolsMatchChatGPTListing`)
and `mcp.json` (the server address). `scripts/build-openai-plugin.sh host` checks that the cases
match in all three.

## Portal steps

1. Go to https://platform.openai.com/plugins → **Create plugin** → **With MCP**. Package name
   `simple-host` (must match `name` in `plugin.json`).
2. Run `bash scripts/build-openai-plugin.sh host`. The complete package is
   `dist/simple-host-openai-plugin.zip`. The Skills tab takes
   `dist/simple-host-openai-skills.zip`, which holds only skill folders.
3. Copy the listing fields below. Upload `plugins/openai/assets/logo.png` (512 by 512) and
   `plugins/openai/assets/icon.png` (256 by 256).
4. Fill in the MCP tab as below, then **Scan Tools**. Expect 68 tools. Domain
   verification: the portal shows a token; put it in `/etc/simple-host.env` as
   `OPENAI_APPS_CHALLENGE=<token>`, restart, check that
   `curl -s https://simple-host.app/.well-known/openai-apps-challenge` prints exactly the token,
   then **Verify Domain**.
5. Enter the reviewer email and password in the portal's private credentials fields. Never put
   the password in this kit, the ZIP, a prompt or a recording.
6. Run the five positive and three negative cases in ChatGPT with the reviewer account. Record
   what happened, attach a demo video URL reviewers can open, and check the policy links and
   sign-in.
7. Fill in the review information (cases, demo video), submit for review, and on approval choose
   when to publish.

TODO-OWNER: confirm the version. `plugin.json` says 1.0.1. Keep it if nothing has been
uploaded under `simple-host` yet; otherwise raise it before uploading.
TODO-OWNER: approve the final listing copy below before submitting.

## Listing fields

- **Package name:** simple-host
- **Version:** 1.0.1
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

Each site can save key-value data (KV), SQLite tables, and files. Access rules control who can read, add, edit, and delete data. New resources are private by default. Use an inbox for submissions only the owner reads, records for each signed-in visitor's entries with owner access, or public data for content everyone may read. Visitors sign in with Google or an emailed code; no password or sign-up form.

Update site files, preview a saved version, or restore an earlier version. The four latest site versions are kept by default, and deleted sites can be restored for 7 days. Current storage stays until you delete it or its site; it does not have automatic 30-day change history.

Connect your own domain with HTTPS or choose a short name.simple-host.app address. Manage who can open a site, read visitor counts and top pages, and download a copy of your site.

Share sites with a team: every member publishes and changes the team's sites with their own sign-in. Or let one person edit a single site.

Connect your Simple Host account through the app's sign-in flow. You can revoke the connection from the Simple Host dashboard. Simple Host is open source, made and run by Vineet Sriram.

### Starter prompts

- Put the page we made online on Simple Host.
- Update the opening hours on my existing site.
- Show the saved entries from my garden party RSVP site.

### Capabilities

- Publish websites, each at its own public address
- Edit, rename, preview, roll back, and delete your sites
- Save bookings, quote requests, waitlists, and orders
- Choose who can read and add saved data, with simple rules
- Let visitors sign in so each person sees only their own records
- Lock a site with a passcode or open it only to named people
- Connect your own domain or a short name.simple-host.app address
- Share sites with a team, or let one person edit one site
- See visitors, top pages, and where they came from
- Download a copy of any site

## Skills

Three skills from `host/webassets/simple-host-website/skills` (skills version 0.27.119, from
`host/webassets/simple-host-website/.claude-plugin/plugin.json`): `website-deploy` (the
onboarding skill), `website-deploy-builder` and `connect-domain`. The build packages them as
they are in that folder; there is no separate OpenAI copy.

## MCP tab

- Server: `https://simple-host.app/mcp`, Streamable HTTP, as in `mcp.json`.
- Authentication: OAuth with dynamic client registration (DCR), authorization code flow and
  PKCE (S256). Use the server's discovery metadata; do not paste an API key or a client secret.
- Tool inventory: 68 tools, each listed on its own; neither `find_tools` nor
  `run_tool` is listed to ChatGPT. Production has MCP Events on (`list_notifications`,
  `remove_notification`) and Teams on (`create_team`, `list_teams`, `list_team_members`,
  `add_team_member`, `cancel_team_invitation`, `answer_team_invitation`, `remove_team_member`,
  `delete_team`, `list_site_editors`, `add_site_editor`, `remove_site_editor`). Site and storage
  tools take an optional `owner` to act on a team's sites or a site the person edits.
- Annotations: every tool sets readOnlyHint, destructiveHint and openWorldHint (and
  idempotentHint) by the rule in `host/mcp/annotations_test.go`. The values are in
  `chatgpt-app-submission.json`. OpenAI no longer requires justifications; the file keeps them.
- Permissions: the consent screen covers publishing and managing sites, reading site files and
  saved data including private entries, downloading copies, reading visitor counts, changing
  access and addresses, and managing notifications.
- Apps UI: MCP Apps screens are off by default (the `MCP_APP_*` settings) and are not shown to
  reviewers. The server has optional screens for sites, analytics, photos and previews; the
  submitted configuration uses tool text and structured results. Do not claim the server has no
  UI implementation.

## Privacy and data handling

AgentCat (agentcat.com) processes connector usage analytics. For each tool call it receives the
arguments and result, with passcodes, keys and file contents left out by us and email addresses
and other identifiers removed by AgentCat's redaction. It never receives the IP address a person
connects from. Every connection may supply a short stated reason for the call. It is free text
that by instruction carries no names, emails or secrets. We remove email addresses, private
links, codes and keys from it, and it goes through the same redaction. A failed call sends only its short
error code, not the error message. The
privacy page is https://simple-host.app/privacy.html.

## Reviewer sign-in

Start the Simple Host connection in ChatGPT. On the Simple Host sign-in page, open **Other ways
to sign in**, enter the reviewer email **openai-review@simple-host.app** and the password from
the portal, choose **Sign in**, then **Allow**. No email code or 2FA. The form shows only when
`REVIEW_ACCOUNT_EMAIL` is set on the server (`host/webassets/static/connect.html`).

TODO-OWNER: check that reviewer sign-in is on in production and the password in the portal works.

## Integration prerequisites

The privacy page's daily analytics hash wording needs the companion analytics change before
release: `host/sites/analytics/ingest.go` still uses one lasting salt. Check daily rotation, no
stored IPs in analytics and no hash disclosure before publishing the policy.

## Demo fixtures and execution status

The reviewer account uses `garden-party-rsvp`, `feedback-survey` and `pickle-shop`. Their files
and sample rows are in `plugins/openai/demo-sites`; `scripts/seed-reviewer-demo.py` seeds them
(seven RSVPs, eight feedback responses, four orders). Do not rerun the seeder on a seeded
account: it inserts duplicate rows. P1 creates a new site and P3 a new team in the reviewer
account; a team name is never given out again once used, so a rerun of P3 needs a new name.

- 2026-10-28, production build b4c65325: `scripts/e2e/e2e-reviewer.py` signed in as the reviewer
  and ran the then P1 to P5 at the tool level; all 45 checks passed. That run predates Teams.
- 2026-11-21: the kit was updated to 68 tools (Teams added) and P3 now covers
  Teams. The MCPJam checks pass locally (`plugins/ROUTINE.md`, step 1). Not yet run in ChatGPT.

TODO-OWNER: run all eight cases in ChatGPT against production and record the results here.
TODO-OWNER: record a new demo video (the current one predates Teams) and put its URL in
`plugin.json` (`review.demo_recording_url`).
TODO-OWNER: the listing screenshots in `listing-screenshots/` are for the website and video only
(the portal refuses screenshots when the server returns no UI); retake them if you want Teams shown.

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

**Scenario:** Create a team and invite one person to it.

**Prompt:** Create a team called rise-bakery-crew so my staff can work on our sites, and invite sam@example.com to it.

**Expected tools:** create_team, add_team_member

**Expected result:** Says the team name becomes its address and cannot be changed later, waits for a yes, then calls create_team once and add_team_member with sam@example.com. Says that nothing is emailed to Sam, so the person should tell Sam to sign in with that email and accept. No site is created, changed or deleted.

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
