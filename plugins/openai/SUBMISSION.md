# Simple Host ChatGPT submission kit

This file is the source of truth for the Simple Host listing in the ChatGPT plugin directory
(package `simple-host`, a new plugin made through the ZIP flow, decided 2026-10-29: the old
draft `app-6ab570c7e41c81918c88f0026f2e1895` cannot change its MCP server). Update it from the
tools as they are, never from memory: see `plugins/ROUTINE.md`. It is not a record of a
deployment, a reviewer run or a portal submission.

Last updated from the tools: 2026-11-26 (the rewritten service, `next/mcp/tools.go`). The json files next to it carry the same fields:
`plugin.json` (package manifest and review cases), `chatgpt-app-submission.json` (tool
annotations and cases, checked against `tools/list` by `TestSubmissionToolsMatchListing` in `next/mcp`)
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
4. Fill in the MCP tab as below, then **Scan Tools**. Expect 51 tools (47 for the model and four `passcode_screen_*` tools, which only the in-chat passcode screen calls). Domain
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

TODO-OWNER: confirm the version. `plugin.json` says 1.1.0 (the rewritten connector). If 1.1.0
is already uploaded under `simple-host`, raise it before uploading.
TODO-OWNER: approve the final listing copy below before submitting.

## Listing fields

- **Package name:** simple-host
- **Version:** 1.1.0
- **Display name:** Simple Host
- **Developer:** Vineet Sriram
- **Category:** Productivity
- **Short description:** Host and manage your websites
- **Package description:** Host websites you or your AI have made, at their own addresses. Publish and update files through the Simple Host connector, manage saved data and access, connect domains, and read visitor counts.
- **Website:** https://simple-host.app
- **Support:** https://simple-host.app/support
- **Privacy policy:** https://simple-host.app/privacy
- **Terms:** https://simple-host.app/terms
- **Brand color:** #157e8a
- **Dark brand color:** #3fb0bb

### Long description

Simple Host hosts websites you or your AI have made. Your AI puts the files online through the connector, at an address you can share, and Simple Host keeps them there. Publish a page you have made, update an existing site, or ask to see its saved entries.

Each site can save data: tables, values and files. Rules control who can read, add, change and delete it, and new data is private to you by default. Use an inbox for submissions only you read, bookings each signed-in visitor keeps their own, or public data for content everyone may read. Visitors sign in with Google or an emailed code; no password.

Choose who may open a site: anyone with the link, one passcode, or only people you name by email. Make an older version live again, take a site offline, and restore a deleted site for 7 days. Four recent versions are kept.

Connect your own domain with HTTPS or choose a short name.simple-host.app address. Read visit numbers and top pages, and download a copy of everything.

Share sites with a team: every member publishes and changes the team's sites with their own sign-in.

Connect your Simple Host account through the app's sign-in flow. You can revoke the connection from the Simple Host dashboard. Simple Host is open source, made and run by Vineet Sriram.

### Starter prompts

- Put the page we made online on Simple Host.
- Update the opening hours on my existing site.
- Show the saved entries from my garden party RSVP site.

### Capabilities

- Publish websites, each at its own public address
- Update, roll back, take offline and delete your sites
- Save bookings, quote requests, waitlists, and orders
- Choose who can read and add saved data, with simple rules
- Let visitors sign in so each person sees only their own records
- Lock a site with a passcode or open it only to named people
- Connect your own domain or a short name.simple-host.app address
- Share sites with a team
- See visitors, top pages, and where they came from
- Download a copy of everything

## Skills

Three skills from `host/webassets/simple-host-website/skills` (skills version 0.28.1, from
`host/webassets/simple-host-website/.claude-plugin/plugin.json`): `website-deploy` (the
onboarding skill), `website-deploy-builder` and `connect-domain`. The build packages them as
they are in that folder; there is no separate OpenAI copy.

## MCP tab

- Server: `https://simple-host.app/mcp`, Streamable HTTP, as in `mcp.json`.
- Authentication: OAuth with dynamic client registration (DCR), authorization code flow and
  PKCE (S256). Use the server's discovery metadata; do not paste an API key or a client secret.
- Tool inventory: 51 tools, each one operation with its own schema, with no `find_tools`,
  `run_tool` or generic executor. Account: `who_am_i`, `update_account`, `download_my_data`,
  `delete_account`. Teams: `create_team`, `invite_to_team`, `accept_invite`, `remove_from_team`,
  `delete_team`. Sites: `list_sites`, `get_site`, `publish_site`, `create_upload_link` (the photo picker),
  `create_site_upload_link` (a whole site as one archive), `read_site_file`, `take_site_offline`, `put_site_online`, `set_site_listed`, `rollback_site`,
  `restore_site`, `delete_site`, `set_site_address`, `site_stats`, `set_notifications`. Who may
  open a site: `open_site_to_anyone`, `require_passcode` (opens the passcode screen; the passcode
  never enters the chat), `allow_only_named_people`, `add_site_viewers`, `remove_site_viewers`,
  `sign_out_site_visitors`. Passcode screen only (hidden from the model):
  `passcode_screen_open`, `passcode_screen_show`, `passcode_screen_set`,
  `passcode_screen_generate`. Saved data with the caller's rights: `list_rows`, `read_row`,
  `add_row`, `change_row`, `delete_row`, `list_values`, `read_value`, `set_value`, `delete_value`,
  `list_files`, `save_file`, `delete_file`. The owner's SQL: `list_tables`, `query_sql` (read
  only), `run_sql` (changes). Also `set_data_rules`, `get_recipe`. Every tool declares an
  `outputSchema` its answers match (`TestEveryToolsOutputAndHints`). Site
  tools take an optional `owner` to act on a team's sites. Notifications are in the same server.
- Annotations: every tool sets readOnlyHint, destructiveHint, openWorldHint and idempotentHint
  and a title, by one rule written in `next/mcp/tools.go`: readOnly when it only reads;
  destructive when it can delete, overwrite or replace, or end or narrow access (undo does not
  count); openWorld when it emails an address the person names, connects an outside domain, or
  can make content readable by anyone on the internet; idempotent when a repeat changes nothing
  more. `TestEveryToolsOutputAndHints` derives each hint from the routes the tool really calls,
  and `TestSubmissionToolsMatchListing` checks `chatgpt-app-submission.json` against the server.
- Permissions: the consent screen covers publishing and managing sites, reading site files and
  saved data including private entries, downloading copies, reading visit numbers, changing
  access and addresses, and managing notifications.
- Apps UI: the server returns four in-chat screens (my sites, visit numbers, photo picker and
  passcode) as MCP Apps and Apps SDK widgets, each with `ui.domain` (and `openai/widgetDomain`)
  `https://simple-host.app` and a CSP that allows nothing but the photo picker's scripts from
  that origin. Reviewers see them in ChatGPT. The passcode screen
  seals the passcode so it never enters the chat.

## Privacy and data handling

AgentCat (agentcat.com) processes connector usage analytics. For each tool call the new service
(`next/mcp/mcp.go`, `agentcat`) sends the tool name, the arguments and the result, with only
`passcode` fields and `_meta` taken out by us; file contents in `publish_site`, `save_file` and
`read_site_file` are sent as they are. It also sends the account id as the actor id and a daily
session hash. Email addresses and other identifiers are left to AgentCat's own redaction. It
never receives the IP address a person connects from. A failed call sends only its short error
code. The privacy page (https://simple-host.app/privacy) says tool calls go to AgentCat and that
sign-in codes and passcodes are never sent, which matches.

## Reviewer sign-in

Start the Simple Host connection in ChatGPT. On the Simple Host sign-in page, open **Other ways
to sign in**, enter the reviewer email **openai-review@simple-host.app** and the password from
the portal, choose **Sign in**, then **Connect**. No email code or 2FA. The form shows only on the
way to approving an app, and only when `SH_REVIEW_EMAIL` and `SH_REVIEW_PASSWORD_HASH` are set on
the server (`next/access/signin/review.go`); it opens only that one non-operator account (it sees
51 tools, 47 of them for the model, and the three demo sites) and is limited like other sign-in.

TODO-OWNER: copy `REVIEW_ACCOUNT_EMAIL` and `REVIEW_ACCOUNT_PASSWORD_HASH` from the old service's
settings into `SH_REVIEW_EMAIL` and `SH_REVIEW_PASSWORD_HASH` (same values), restart, and check
that the password in the portal's Review details form signs in.

## Integration prerequisites

The server changes the pre-submission review of 2026-11-25 asked for (reviewer sign-in,
`who_am_i` without emails or ids by default, output schemas, the screens' own domain, the
annotations, and `data` and `passcode_screen` split into single-purpose tools), and those of the
second review (`set_site_access` and `change_site` split into single-purpose tools; no internal
ids of people, teams or invitations in any result; `get_site` gives named people's emails only
with `include_emails`) are in `next/`.
They must be live before **Scan Tools**: the portal scans the live server.

## Demo fixtures and execution status

The reviewer account uses `garden-party-rsvp`, `feedback-survey` and `pickle-shop`. Their files
and sample rows are in `plugins/openai/demo-sites`; `scripts/seed-reviewer-demo.py` seeds them
(seven RSVPs, eight feedback responses, four orders). Do not rerun the seeder on a seeded
account: it inserts duplicate rows. P1 creates a new site and P3 a new team in the reviewer
account; a team name is never given out again once used, so a rerun of P3 needs a new name.

- 2026-10-28, production build b4c65325: `scripts/e2e/e2e-reviewer.py` signed in as the reviewer
  and ran the then P1 to P5 at the tool level; all 45 checks passed. That run predates Teams.
- 2026-11-21: the kit was updated to the old service's 68 tools (Teams added).
- 2026-11-25: the kit was rewritten for the new service's 25 tools (the two operator tools are left out; tool names, cases P1 to P5,
  listing text, skills 0.28.0). The reviewer demo data (`scripts/seed-reviewer-demo.py`,
  `scripts/e2e/e2e-reviewer.py`) was written for the old API and has not been checked against the
  new service.
- 2026-11-26: the tools were split to 41 (37 for the model), with output schemas, titles and the
  four hints; P4 uses `query_sql`; skills 0.28.1.
- 2026-11-26, local run of `next` at 992900e0e (not production): the reviewer signed in through the
  real sign-in page in a headless browser (Other ways to sign in, password, Connect), the code
  was exchanged for a token, and tools/list matched an ordinary account's 41 tools. P1 to P5 ran
  at the tool level with the expected results. Not yet run in ChatGPT or MCPJam.
- 2026-11-26, after the second review: `set_site_access` and `change_site` were split into 11
  single-purpose tools (50 in all, 46 for the model); values, stored files and rows say `mine`
  in place of who saved them, and no result carries an internal id
  (`TestEveryToolsOutputAndHints` checks every answer). P2 uses `rollback_site`. Not yet run in
  ChatGPT.
- 2026-11-26, third review, local run of `next` at 149c50894 (not production): the reviewer's
  tools/list equals an ordinary account's (50 tools, 46 for the model), and P1 to P5 ran at the
  tool level with the expected results on locally seeded demo sites. Open: `query_sql` still
  shows visitor ids in a `SELECT *` join, an alias or an expression of `visitor_id`
  (OPENAI-REVIEW-3 A1). Not yet run in ChatGPT.
- 2026-11-26, after the third review: every person's id in a saved-data answer (any column,
  alias, expression or text) shows as `you` or `visitor-<8 hex>`, one label per person on the
  site, and a label in `where`, `params` or the SQL means that person (A1). For the site's
  managers a UUID that is not a person's (an order number, a key) stays as stored; visitors see
  every id-shaped value as a label. Checked in a local run at 66c817acc. `create_upload_link` is the
  photo picker only; `create_site_upload_link` gives the whole-site archive link, destructive
  (A2). 51 tools, 47 for the model. Not yet run in ChatGPT.

TODO-OWNER: run all eight cases in ChatGPT against production and record the results here.
TODO-OWNER: record a new demo video (the current one shows the old tools) and put its URL in
`plugin.json` (`review.demo_recording_url`).
TODO-OWNER: the listing screenshots in `listing-screenshots/` are for the website and video only;
retake them if you want the new screens shown.

## Five positive test cases

### P1

**Scenario:** Put a one-page site online as a new site.

**Prompt:** Put a one-page website online for my bakery, Rise and Shine, as a new site named rise-and-shine. It should list our opening hours: Monday to Friday 7 to 3, Saturday 8 to 1.

**Expected tools:** publish_site

**Expected result:** Says the site name and that anyone with the link can open it, waits for a yes, then calls publish_site once and returns the live address exactly as the tool gave it (https://rise-and-shine--<handle>.simple-host.site/ or https://rise-and-shine.<handle>.simple-host.app/). The page shows the bakery name and hours. No photo, notification, or team calls, and no emails in the result.

### P2

**Scenario:** Update the seeded feedback survey and undo the requested change.

**Prompt:** On feedback-survey, change the heading to "Tell us how we did". Then, in the same chat: Undo that change.

**Expected tools:** get_site, read_site_file, publish_site, rollback_site

**Expected result:** The survey shows the requested heading, published with keep_other_files so the other files stay. After confirmation, rollback_site makes the previous version live and the original heading returns. No account or visitor emails are returned.

### P3

**Scenario:** Create a team and invite one person to it.

**Prompt:** Create a team called rise-bakery-crew so my staff can work on our sites, and invite sam@example.com to it.

**Expected tools:** create_team, invite_to_team

**Expected result:** Calls create_team once with the handle rise-bakery-crew, then invite_to_team with sam@example.com, which emails Sam an invitation. Says the handle is part of the team's site addresses and that nobody is a member until Sam signs in with that email and accepts. No site is created, changed or deleted.

### P4

**Scenario:** Count RSVPs without identifying visitors.

**Prompt:** How many RSVPs do I have on garden-party-rsvp?

**Expected tools:** query_sql

**Expected result:** Runs a COUNT(*) on the rsvps table of the site's rsvps namespace and returns the number matching the current fixture count. Returns no emails, names, or visitor references. Does not call who_am_i.

### P5

**Scenario:** Visitor numbers without per-visitor detail.

**Prompt:** How many people visited feedback-survey this week, and which pages did they read?

**Expected tools:** site_stats

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
