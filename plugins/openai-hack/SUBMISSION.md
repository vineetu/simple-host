# Simple Hack ChatGPT submission kit

This file is the source of truth for the Simple Hack listing in the ChatGPT plugin directory
(package `simple-hack`, a new plugin made through the ZIP flow "With MCP"). Every field below is
final and can be pasted as it is. It is not a record of a deployment, a reviewer run or a portal
submission; nothing has been uploaded yet.

## What only a person can do

These need the OpenAI portal, the production server or a ChatGPT account. Everything else in
this kit is done.

1. **Installer:** on the production box, set the reviewer account, AgentCat and (once the portal
   shows it) the domain token in `/etc/simple-hack.env`, then restart (see "Before submitting").
2. **Installer:** create the three demo events on simple-hack.app and send the owner the open
   event's join code.
3. **Owner:** put that join code in place of `TODO-OWNER-JOIN-CODE` in P3, here and in
   `chatgpt-app-submission.json`, then run `bash scripts/build-openai-plugin.sh hack`.
4. **Owner:** run the eight cases in ChatGPT as the reviewer, record a demo video, and put its
   address in the portal's review form.
5. **Owner:** in the portal: create the plugin, upload the package and icons, paste the fields,
   scan tools, verify the domain, enter the reviewer email and password in the private fields,
   attest and submit.

Last updated from the tools: 2026-11-21. The json files next to it carry the same fields:
`plugin.json` (package manifest), `chatgpt-app-submission.json` (tool annotations and cases,
checked against `tools/list` by `TestHackSubmissionToolsMatchChatGPTListing` in
`products/hack/mcp`) and `mcp.json` (the server address). `scripts/build-openai-plugin.sh hack`
checks that the cases here and in the json match. The guidelines followed are OpenAI's plugin
submission pages as read on 2026-11-21 (developers.openai.com/apps-sdk/app-submission-guidelines
and /plugins/deploy/submission).

The older draft under `products/hack/webassets/hack-toolkit` (`SUBMISSION.md`, `metadata/`,
version 0.2.13) is the Claude and download-site toolkit. It is not used for this listing.

## Before submitting

Each line says who does it. The installer works on the production server; the owner works in
the portal and in ChatGPT.

- [ ] **Installer:** pick a reviewer email on a domain you control (for example
  `openai-review@simple-hack.app`; it never receives mail, sign-in is by password) and a password
  of at least 16 characters. Hand both to the owner privately.
- [ ] **Installer:** make the password hash, without storing the password:
  `read -rs PW && printf '%s\n' "$PW" | /usr/local/bin/simple-hack review-account hash; unset PW`
- [ ] **Installer:** add to `/etc/simple-hack.env`:
  `REVIEW_ACCOUNT_EMAIL=<the reviewer email>` and
  `REVIEW_ACCOUNT_PASSWORD_HASH=<the printed value>`.
- [ ] **Installer:** add `AGENTCAT_PROJECT_ID=<the value of AGENTCAT_PROJECT_ID in /etc/simple-host.env>`
  to `/etc/simple-hack.env` (the same AgentCat project as Simple Host; events carry
  `product: simple-hack`).
- [ ] **Installer:** `sudo systemctl restart simple-hack`, then check the startup log says
  "reviewer sign-in enabled": `journalctl -u simple-hack -n 200 | grep -i reviewer`.
- [ ] **Installer:** sign in once at `https://simple-hack.app` through the connector consent page
  with the reviewer email and password (Other ways to sign in). The account is created on that
  first sign-in, as an ordinary (non-admin) account.
- [ ] **Installer:** create the demo data with two other ordinary accounts (an organiser, Ada
  Example, and a participant, Pat Example) and the reviewer, all with fictional names:
  - `review-build-day`, title Review Build Day, organised **by the reviewer**, left in Draft
    (stage `draft`). Used by P2.
  - `review-open-day`, title Review Open Day, organised by Ada Example, in Sign-up open (stage
    `open`), the reviewer not a member. Its join code goes into P3. Used by P3 and P4.
    Open it at least 15 minutes before any test run: its team addresses need their certificates
    before P4 can publish (until then the refusal is `team_sites_not_ready`).
  - `review-judging-day`, title Review Judging Day, organised by Ada Example; Pat Example's team
    **Pixel Pushers** has a filled and submitted entry (title Lunch Line); the reviewer joined
    with the judge link; the event moved to Judging (stage `judging`) with the starter rubric.
    Used by P5.
  - Do not create `review-jam-2027` or a team called Review Rockets: P1 and P3 create them. An
    event name is used once, so a rerun of P1 needs a new name.
- [ ] **Installer:** send the owner the join code of `review-open-day` (the part after `/join/`
  in its join link). Keep it out of chat logs other than the owner's.
- [ ] **Owner:** replace `TODO-OWNER-JOIN-CODE` in P3 (here and in
  `chatgpt-app-submission.json`) and rebuild the package.
- [ ] **Owner:** after the portal shows the domain token: **Installer** puts it in
  `/etc/simple-hack.env` as `OPENAI_APPS_CHALLENGE=<token>`, restarts simple-hack, and checks
  that `curl -s https://simple-hack.app/.well-known/openai-apps-challenge` prints exactly the
  token. Then the owner chooses **Verify Domain**.
- [ ] **Owner:** run all eight cases in ChatGPT as the reviewer and record a demo video (about a
  minute: connect, sign in with the reviewer password, P2, P3, P5). Put its address in the
  review form.

## Portal steps

1. Go to https://platform.openai.com/plugins → **Create plugin** → **With MCP**. Package name
   `simple-hack` (must match `name` in `plugin.json`).
2. Run `bash scripts/build-openai-plugin.sh hack`. The complete package is
   `dist/simple-hack-openai-plugin.zip`. The Skills tab takes
   `dist/simple-hack-openai-skills.zip`, which holds only skill folders.
3. Copy the listing fields below. Upload `plugins/openai-hack/assets/logo.png` (512 by 512) and
   `plugins/openai-hack/assets/icon.png` (256 by 256), both from the Simple Hack icon.
4. Fill in the MCP tab as below, then **Scan Tools**. Expect 179 tools. Verify the domain as in
   "Before submitting".
5. Enter the reviewer email and password in the portal's private credentials fields. Never put
   the password in this kit, the ZIP, a prompt or a recording.
6. Fill in the review information: the five positive and three negative cases below, and the
   demo video address.
7. Attest, submit for review, and on approval choose when to publish.

## Listing fields

- **Package name:** simple-hack
- **Version:** 1.0.0 (nothing has been uploaded under `simple-hack`)
- **Display name:** Simple Hack
- **Developer:** Vineet Sriram
- **Category:** Productivity
- **Short description:** Run, join and judge hackathons (30 characters, the portal's limit)
- **Package description:** Run, join and judge hackathons on simple-hack.app through the Simple Hack connector: create events, form teams, publish team websites, submit and score entries, and publish results.
- **Website:** https://simple-hack.app
- **Support:** https://simple-hack.app/support (the support page; its email is support@simple-host.app, which covers both services)
- **Privacy policy:** https://simple-hack.app/privacy.html
- **Terms:** https://simple-hack.app/terms
- **Brand color:** #a33f31 (Simple Hack's accent red)
- **Dark brand color:** #ee8f92
- **Commerce:** none. Simple Hack sells nothing.

### Long description

Simple Hack runs hackathons on simple-hack.app. The connector acts as the signed-in person, in the role they have in each event: organiser, participant or judge.

Organisers create a draft event, set sign-up questions, tracks, entry rules and the judging rubric, and move the event through its stages. They post announcements, manage people, teams and judges, and publish results. Join and judge links come back in tool results for the organiser to share.

Participants join an event with a join code, or apply to one from the public directory. They form or join a team, publish the team's website, fill in and submit the entry, and vote when voting is open.

Judges join with a judge code, read the rubric and their queue, score entries with an optional comment, and declare conflicts.

Each team website has its own address and can save key-value data (KV), SQLite tables and files, with rules for who can read and change them. Teammates can change it until the team's deadline.

Sign in with Google or an emailed code. Sign out everywhere on your Simple Hack account page revokes the connection. Simple Hack is open source, made and run by Vineet Sriram.

### Starter prompts

The portal takes at most three, of at most 128 characters each. These three are in `plugin.json`:

- What should I do next for my hackathon?
- Help me set up a one-day hackathon for my school's coding club.
- I'm judging a hackathon. What's in my queue, and how do I score?

Alternates, for the website or a later version:

- Find me a team in the spring hackathon that still needs a designer.
- Publish our team's project page and submit our entry before the deadline.

### Capabilities

- Create and run a hackathon, from a draft to published results
- Set sign-up questions, tracks, entry rules and the judging rubric
- Post announcements, and email participants when you choose to
- Join an event with a code, or apply from the public directory
- Form, join and manage a team
- Publish your team's website, with saved data and access rules
- Fill in and submit your team's entry, and vote
- Score entries against the rubric and declare conflicts
- Export participants, teams, scores and results

## Skills

Five skills from `products/hack/webassets/hack-toolkit/skills` (Simple Hack's own set, separate
from Simple Host's by decision): `run-hackathon` (the onboarding skill), `join-hackathon`,
`judge-hackathon`, `website-deploy` and `website-deploy-builder`. The build packages them as they
are in that folder.

## MCP tab

- Server: `https://simple-hack.app/mcp`, Streamable HTTP, as in `mcp.json`. One server.
- Authentication: OAuth. Choose discovery from the server (dynamic client registration, DCR):
  authorization code flow with PKCE (S256), metadata at
  `https://simple-hack.app/.well-known/oauth-protected-resource/mcp` and
  `https://simple-hack.app/.well-known/oauth-authorization-server`. Scopes `sites events`. No
  API key, client secret or custom header.
- Tool inventory: 179 tools, each listed on its own (no `find_tools` or `run_tool`). The `hack_*`
  tools cover events, sign-up, teams, entries, voting, judging and results. The website and
  storage tools (`create_site`, `update_site`, `storage_*` and others) work on a team's website
  with `owner` set to the event's name. `list_notifications` and `remove_notification` come with
  MCP Events.
- Annotations: every tool sets readOnlyHint, destructiveHint and openWorldHint (and
  idempotentHint) as explicit booleans, by the rule in `products/hack/mcp/annotations_test.go`;
  the website and storage tools follow Simple Host's rule in `host/mcp/annotations_test.go`.
  openWorldHint is false throughout: every tool works inside the person's own events and team
  sites. The values are in `chatgpt-app-submission.json`, and a test fails if they drift from
  the server. Justifications are no longer required and are not included.
- Permissions: the consent screen says the app can "manage events you organise and join events
  as a participant or judge", "read and change your event entries, votes and scores" and
  "publish your current team's site after you select the team in the connector". In practice
  the connection acts as the signed-in person in each event, with that person's role:
  organisers manage their events, participants their own team and entry, judges their own
  scores and conflicts. It can read and change team website files and saved data the person's
  team may change, and return exports an organiser may download.
- Apps UI: the judge and organiser screens (`hack_show_judge`, `hack_show_organiser`) are not
  offered to ChatGPT; it gets tool text and structured results.

## Screenshots

None in the portal: it refuses screenshots when the MCP server returns no UI
(`screenshots_not_allowed`), and Simple Hack returns none to ChatGPT. `listing-screenshots/`
holds four 1280 by 800 images for the website and the demo video, made from a local fixture with
fictional data (its README says how): the public event page, the organiser's Home, a
participant's Overview and a judge's Overview.

## Privacy and data handling

The connection sends Simple Hack the tool arguments the app supplies (event details, sign-up
answers, entries, scores, website files) and returns the results the person's role allows. Team
rosters return emails only when an organiser asks for them (`include_emails`). Join, judge and
co-organiser links are returned only to organisers and should be kept private. We receive
neither the chat history nor unrelated conversations.

AgentCat (agentcat.com) processes connector usage analytics, in the same project as Simple Host.
For each tool call it receives the tool's name, the arguments and result, the reason the app
gives for the call when it gives one, the app's name, how long the call took, a scrambled
account reference, and tags for the product (`simple-hack`) and, for an event the person is
part of or one listed publicly, their role in it (organiser, participant, judge or none) and its
stage. A failed call sends only its short error code, not the error message. Names of events,
teams and tracks, stages, roles and settings are sent. Anything we have not listed as safe is
left out (`host/mcp/analytics_allow.go`), including what people type (sign-up answers,
messages, entries, judges' comments), join, judge, team and co-organiser codes and links,
passcodes, keys, file contents, email addresses, other people's names and account IDs, and
applicants' city, country, age range and gender. Email addresses, private links, codes and
keys are removed from the stated reason (`host/mcp/analytics_scrub.go`,
`host/mcp/analytics_sanitize.go`). AgentCat's redaction removes further identifiers.
It never receives the IP address a person connects from. The privacy page,
https://simple-hack.app/privacy.html, says this under "Who we share it with".

## Reviewer sign-in

Start the Simple Hack connection in ChatGPT. On the sign-in page, open **Other ways to sign
in**, enter the reviewer email and the password from the portal, choose **Sign in**, then
**Allow**. No emailed code, magic link or 2FA. The form shows only when `REVIEW_ACCOUNT_EMAIL`
and `REVIEW_ACCOUNT_PASSWORD_HASH` are set on the Simple Hack server; it is the same consent page
and sign-in route as Simple Host's, and `TestHackReviewerSignIn` in `products/hack/servertest`
checks it end to end on Simple Hack.

## Demo fixtures

The demo data is listed under "Before submitting". The local MCPJam fixture
(`scripts/mcpjam/seed.py hack`) builds the same shape for checks on this box.

## Five positive test cases

### P1

**Scenario:** Check an event name and create a draft event.

**Prompt:** Check whether review-jam-2027 is free. If it is, create a draft hackathon called Review Jam 2027, organised by Ada Example, contact ada@example.org, for 20 people, starting 2027-03-06 in Europe/London. Purpose: a one-day student build.

**Expected tools:** hack_check_event_name, hack_create_event

**Expected result:** Calls hack_check_event_name for review-jam-2027. If it is free, confirms the details, then calls hack_create_event once with them. Reports the draft's address and stage, and the join and judge links exactly as returned. Does not open sign-up, post announcements or publish a website.

### P2

**Scenario:** Open sign-up for a draft event and share its join link.

**Prompt:** Open sign-up for review-build-day and give me the join link.

**Expected tools:** hack_get_event, hack_set_event_stage

**Expected result:** Reads the event with hack_get_event, says what opening sign-up does and waits for a yes, then calls hack_set_event_stage with stage open once. Gives the join link exactly as returned. May offer an announcement, but posts or emails nothing unless asked.

### P3

**Scenario:** Join an event with a join code and create a team.

**Prompt:** Join the hackathon with join code TODO-OWNER-JOIN-CODE. I have read the code of conduct and I accept it. Then create a team called Review Rockets.

**Expected tools:** hack_preview_join, hack_join_event, hack_create_team

**Expected result:** Previews the join code and shows the event and its code of conduct, then calls hack_join_event with accept_coc true. Asks before creating the team, then calls hack_create_team once with the name Review Rockets. The reviewer becomes the team's admin. No other team is joined or changed.

### P4

**Scenario:** Publish the team's website under the event.

**Prompt:** Publish a one-page website for my team Review Rockets that says what we are building: a study timer.

**Expected tools:** hack_get_my_teams, create_site

**Expected result:** Finds the team and its event with hack_get_my_teams, asks before the first publication, then calls create_site once with the team's site name and owner set to the event's name. Gives the team site's address exactly as returned. No other site is changed or deleted.

### P5

**Scenario:** Read the judging queue and rubric, then score one entry.

**Prompt:** I am judging review-judging-day. Show my queue and the rubric, then give Pixel Pushers 4 out of 5 on every criterion with the comment: Clear demo.

**Expected tools:** hack_get_judge_queue, hack_get_rubric, hack_score_team

**Expected result:** Reads the queue and the rubric, confirms the scores and comment with the judge, then calls hack_score_team once for Pixel Pushers with one score per criterion. Does not change the rubric, other judges' scores or any conflict.

## Three negative test cases

### N1

**Scenario:** Brainstorming does not need the connector.

**Prompt:** Give me ten theme ideas for a student hackathon.

**Expected tools:** None

**Expected result:** Answers in chat with theme ideas. Does not call any Simple Hack tool or create anything.

### N2

**Scenario:** Running server code is outside what team websites do.

**Prompt:** Deploy our team's Flask API with a Postgres database for our hackathon project.

**Expected tools:** None

**Expected result:** Explains that a team website serves static files with built-in KV, SQLite and file storage, and does not run the team's own server code. Deploys nothing. May offer a static front end instead.

### N3

**Scenario:** Events on other platforms are outside the connector.

**Prompt:** Register me for a hackathon listed on devpost.com.

**Expected tools:** None

**Expected result:** Explains that Simple Hack works only with events on simple-hack.app and cannot register anyone elsewhere. Does not call any Simple Hack tool.
