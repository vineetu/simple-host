# INTENT

## What this is, and why it exists

Simple Host is one Go binary that hosts static websites and gives every site a small
backend with owner-defined KV, SQLite and file resources. Its older state, collection
and declared-data APIs remain available on Simple Host for existing sites only. It exists because the
sliver of dynamic behaviour most ordinary websites need (save an RSVP, count a vote, keep a
guestbook) is absurdly expensive to stand up separately. Agents deploy sites through the bundled
Website Deploy skill; humans mostly never touch the API directly.

## Who uses it, and in what situation

- A person telling their coding agent "put this online". The agent registers, builds, deploys.
- The agent itself, reading `llms.txt`, the OpenAPI spec and the skills to wire up saves.
- Visitors of the hosted sites: reading pages, and signing in (Google or an emailed code) on the
  site's own address when a page saves something on their behalf.
- The owner (vineetu) as operator and admin of the live instance at simple-host.app.

## What success looks like

- An agent with the skill installed can ship a working site, including a form that saves, on
  the first try, without the owner intervening.
- Every site lives at its own address, `https://<site>.<handle>.simple-host.app/` (or on a
  domain of its own), its own browser origin. There a visitor signs in with Google or an emailed
  code and saves are per-person. Agents save with an API key anywhere.
- The whole thing keeps running on a 1 CPU / 1 GB box: one binary, one Postgres, one folder.

## The hackathon product, and the page that sells it

simple-hack.app is a hosted hackathon platform (owner decision 2026-09-30, reversing the
organiser-prompt page of 2026-09-11): anyone who signs in creates an event there and runs it
end to end. It is a second copy of the same binary on the same box (`EVENTS=hosted`,
`simple-hack.service`), with its own accounts and database. The event's public page is
`<event>.simple-hack.app`; each team's site will be `<team>.<event>.simple-hack.app`.
Organisers share one join link and one judge link; participants sign in, accept the code of
conduct and form teams. Design: `docs/designs/simple-hack-platform.md`.

**2026-10-03 owner decision — withdraw unreviewed Simple Hack skill downloads.** The
current Hack toolkit and first-party skill distribution use a reviewed,
connector-only five-skill snapshot. Agents do not solicit or process sign-in
codes, API keys, passwords or passcodes in chat. Older toolkit ZIPs are removed
from the public site and prior first-party immutable ChatGPT ZIP routes return
410; historic bytes remain in Git history. The marketplace release is not
recommended until separately updated. The platform's own sign-in and REST APIs
remain available to people and locally configured clients.

Self-hosting stays in the platform and installer, but the run-hackathon skill covers only
events on simple-hack.app (owner decision 2026-10-03). On simple-hack.app the self-host
page link remains a quieter second option; it is outside the skill.

Simple Hack means **Hackathons, now for everyone.** AI builds a team's product from
a description, so people who aren't programmers can show their ideas and compete.
The best idea wins, not the best coder.

Its audience includes college students who know tech; product managers and leaders who
want their people to compete with ideas; and anyone who isn't a programmer. The organiser
picks the platform. The participant's moment — describing an idea and seeing their AI build
a live team site — is why they pick it. Event pages, teams, judging and results keep the
hackathon in one place, on phones and in the AI apps people already use.

**2026-10-04 owner decision — the approved story is the landing page.** The hand-drawn
12-scene film plays on the first visit; returning visitors see the actions and can replay it.
Keep its artwork and tap/swipe/key/wheel navigation, with the copy and scene changes
approved in the 2026-10-04 everyone decision below. Use one warm-paper, red/teal
ink and crayon theme across hosted Simple Hack, with local Caveat/Kalam fonts, readable
body text, OS light/dark plus the shared override. Each event has a stable slug colour or
an organiser-selected crayon. Simple Host's presentation and behavior stay unchanged.

**2026-10-04 owner decision — Get started teaches setup, not skill reading.**
`/get-started` follows Simple Host's install content flow for organisers, participants
and judges, while retaining Simple Hack's ink theme. Lead with adding the connector
to the AI people already use, then role prompts and plain product answers. Skill
files and ZIPs stay in collapsed Other ways to install. The owner’s 2026-10-04 follow-up
puts each AI setup and FAQ answer in a collapsed native accordion so people only open
what they use; AI rows are exclusive and support deep links. Simple Host's install page
does not change.

**The simple-hack.app page's one job is to get an organiser to click "Create event".** It is
written for the organiser. It is also the link shared on LinkedIn, so the top has to make a
stranger understand the thing in one breath before it asks anything. Self-hosting is a quieter
second link.

What follows from that, and is not negotiable without changing the line above:

- No sentence about limits, capacity, disk, retention or file sizes. An organiser asks "will
  this hold my event"; the answer is yes. Anything past yes is the implementation talking.
- No section listing what the product does not do. Stated absences read as "not ready".
- Only what works today is on the page. A feature that is not live is not described.
- Nothing condescending about the reader's skills. Not "for people who have never deployed".
- No internal telemetry as copy.
- Everything is free, and the page says so in one line.

## Non-goals

- Per-page locks and logins for viewing. The one view-lock is a single passcode on a whole site
  (decision 2026-09-29 "Site passcode"); there is no per-page lock, no per-person viewer list and
  no viewing sign-in. On Simple Host, legacy private Submissions and Personal records
  remain available for existing sites (decisions 2026-09-24 "Private collections",
  2026-09-25 and 2026-09-27 steps 3-4).
- A general-purpose backend was a non-goal for the original saved-data API. The
  2026-10-02 storage-primitives decision below supersedes the no-schema/no-query
  part for new per-site SQLite databases; arbitrary server-side code remains a
  non-goal.
- Metered third-party AI keys. AI create runs on the local Grok sidecar only.
- Starter templates and drop-in widgets. Removed 2026-09-05; agents build pages themselves.
- Isolation beyond the browser origin. Each site is its own origin (decision 2026-09-26), so a
  person's sites are kept apart by the browser and a sign-in covers one site; nothing isolates
  pages inside one site from each other.

## Constraints

- Runs from `/usr/local/bin/simple-host` as `simple-host.service`; env in `/etc/simple-host.env`.
  Editing the repo changes nothing until rebuilt and restarted.
- Fetch and fast-forward before starting work, push when finishing. Two sessions have overwritten
  each other in production before.
- `check-docs-sync.sh` must pass: routes, OpenAPI, llms.txt and the skills drift independently.
- Hosted pages never hold an API key. Anything a page does must work with a site-scoped cookie.

## Decisions already made

- **2026-10-05. Simple Host’s film landing and blue ink theme.** The approved
  12-scene Simple Host film becomes `/`: first visits play the story; returning
  visitors see Get started, Your sites, How it works and replay. Preserve its
  artwork, copy, tap/swipe/key/wheel navigation, scene links, Back and reduced
  motion. Keep the letter-by-letter intro, timing and two-colour logo; give
  transformed letters ink room and release completed step animations for Safari.
  All app pages use the film’s teal/blue inks and navy night paper, with local
  handwriting for headings/controls, readable body/form/table text, tabular
  numbers, 44 px controls and the existing shared OS theme override. Pick your
  AI and FAQ use native accordions; AI rows are exclusive and deep-linked.
  Published user pages and Simple Hack’s red/teal look keep their own styles.
  Small-box installs receive the shared pages on their next release. Enterprise’s
  separate application is excluded. This supersedes the earlier statements that
  Simple Host’s presentation and install page stay unchanged. Reason: owner’s
  approved preview and request to switch Simple Host similarly to Simple Hack.

- **2026-10-05. Home-page parity across installable editions.** Carry phases
  1–3 (home choice, live showcase feed, pins/order/bio) into small-box installs
  and Enterprise, and update every product/help/installer surface. Small-box
  path showcases open the existing site URL; Enterprise follows OIDC, existing
  access checks and site origins. Enterprise has no custom domains or short
  names (owner decision 2026-09-29). The whole-space custom-domain phase did
  not ship and stays out of this release. Simple Hack has no personal sites.


- **2026-10-05. Your home page.** Every account's own address is its showcase by
  default, or any site it chooses. People create whatever they want and share the
  links they like. A selected home follows rename and falls back to the showcase
  when unavailable; deletion clears it. Its normal site address remains available.
  Sign-in and data on the person origin cover the selected site only; the owner app
  remains on simple-host.app. A live showcase feed and pin/order/bio let people curate their projects. An own
  domain for the whole space is allowed only if existing issuer/proxy automation
  supports it; stop rather than improvise infrastructure. Hosted events are excluded.


- **2026-10-04. Hackathons, now for everyone.** Keep the word hackathon. The best idea
  wins, not the best coder: participants describe their idea and their AI builds the team
  site. Audience includes non-technical people, college students who know tech, product
  managers and leaders who want their people to compete with ideas. The organiser chooses
  the platform because anyone can take part. The 12-scene film shows this missing building
  moment in scene 8, judges in scene 9, and Free in the opening and closing subs.


- **2026-10-03. Simple Hack website storage is only KV, SQLite and files.**
  Remove the old state, collection and declared-data HTTP and MCP surfaces from
  hosted Simple Hack, even though existing clients break. Legacy Hack requests
  return 410 `legacy_storage_removed`; hide those tools from Hack discovery.
  Preserve stored legacy rows for operator recovery rather than deleting data.
  The 1,000,000-byte allowance pools only KV, SQLite and file resources,
  separate from deployed assets and retained versions. Resource policies cover
  the whole resource: `signed-in` does not isolate visitor rows. Event signup
  and management remain on the trusted Simple Hack apex. On Simple Host, the
  old APIs keep their existing behavior and privacy for existing sites, but
  are deprecated and should not be offered for new builds. This decision
  supersedes the 2026-10-02 Hack compatibility language below.


- **2026-10-02. Give each website 1,000,000 bytes of new storage by default.**
  This is one decimal-megabyte allowance pooled across its new KV, SQLite and
  raw-file resources, with an owner-only used/remaining/limit report and a
  per-type breakdown. Existing deployed assets and legacy saved data retain
  their separate limits and data. A self-hosted instance may configure the
  new allowance. Generated phone-photo upload pages should resize and
  compress images in the browser before sending them; the raw-file API still
  preserves the bytes it receives. This supersedes the briefly considered
  half-megabyte default.

- **2026-10-02. Apply site storage primitives to hosted Simple Hack too.**
  Team sites use their current team credential, team-only scope and deadline
  checks; organiser custom event websites use a current organiser route mapped
  to the event website site ID. Both retain their own 1,000,000-byte site pool
  and visitor-origin resource policies. The older declared-data API and
  Enterprise replica/S3 deferral remain unchanged. Reason: the owner wants
  feature parity across both hosted services.

- **2026-10-02. Offer three site storage primitives: key–value pairs, per-site
  SQLite, and raw files.** Agents choose keys, tables, and file layouts rather
  than a prescribed collection kind. Each owner-created resource has separate
  read and write access: anyone, signed-in visitors, or owner; the default is
  owner-only. Anyone really includes anonymous writes when the owner chooses
  it. A resource may inherit the site's existing passcode gate or explicitly
  opt out; an unlocked visitor follows the resource policy, while authorized
  owner tooling bypasses the passcode. The 2026-08-14 signed-in-write decision
  still applies to existing state and collection APIs and their existing
  privacy rules; it does not constrain these new opt-in resources. Existing
  data is never silently moved or made public. This replaces the earlier
  no-schema/no-query non-goal only for per-site SQLite, not with a general
  server-code runtime. Reason: the owner wants agents to choose structures and
  behavior freely while the site owner controls who may read and write.

- **2026-10-02. An event organiser can also judge.** Keep their organiser membership
  and let them use the same judging queue, assignments, conflicts and score locks as judges.
  Reason: the owner wants to organise and score from one account without separate roles or accounts.

- **2026-08-14. Every state/collection write requires a signed-in visitor.** Not per-site opt-in.
  Reason: writes should cost an identity without giving pages an API key. `docs/history/SPEC.md` is the
  historical design; later entries here override it.
- **2026-08-23. AI create uses Grok only, via the local CLIProxy sidecar.** No fallbacks. Reason:
  no metered third-party AI keys, no silent provider switches.
- **2026-09-05. Enforcement flipped globally (`WRITE_AUTH_MODE=on`).** Reason: the rule is only
  true if it holds everywhere; log mode let agents build saves that would silently break later.
- **2026-09-05. Google is the only sign-in provider for now.** GitHub stays wired in code but
  unconfigured. Reason: good enough; one button is simpler for visitors and for the skill.
- **2026-09-05. Pages may ask who is signed in.** `GET /v1/sites/{site}/me` added, overriding
  SPEC §1.5's earlier rejection of a session endpoint. Reason: a page must be able to show a
  sign-in button before the visitor types, not discover the need on the first failed save.
- **2026-09-05. A hosted helper script, `https://simple-host.app/auth.js`, is the documented
  way to save.** Reason: the skill can then state one rule (include the script, call
  `SH.requireSignIn()` before saving) instead of every agent re-implementing thirty lines.
- **2026-09-05. Skills teach sign-in as a precondition, not as a 401 branch.** Reason: an agent
  that only handles the error builds a form that looks fine in testing and fails for visitors.
- **2026-09-05. One account model; visitors are not a separate class.** Any account's API key
  writes to any site (accepted as that account's write; superseded 2026-09-26: only its own sites; the store records no actor, only the
  server log does), and a page can sign a visitor in by emailed code as well as Google, creating the account if it does not exist. Reason: "keep it simple";
  an agent saving on behalf of a person gets their key through the same email-code flow the
  dashboard already uses, and there is only one kind of identity to reason about.
- **2026-09-05. Email sign-in codes are bound to a purpose and, for hosted pages, to one site.**
  A visitor code cannot be redeemed at the dashboard for an API key or on another site. Reason:
  review found the opposite let a phished guestbook code become a full account credential.
- **2026-09-05. Visitor sign-in only on a custom domain.** On the shared host all sites are one
  origin, so a sign-in there can never be private to one site. Reason: "if you want safe writes
  you need a domain" is one sentence everyone can understand. Superseded 2026-09-25: a person's
  own address counts as the site's own origin (see "Per-person subdomains"); since 2026-09-26
  every site's own address is (see "Per-site subdomains").
- **2026-09-06. Shared host: anyone can read and write.** Page saves there need no sign-in and
  no key; it is a public scratchpad guarded by rate limits and size caps. Reason: keep the
  shared host simple and useful; sign-in remains a feature you get by connecting a domain.
  Reversed for simple-host.app on 2026-09-24 (built 2026-09-26); still holds on event and
  self-hosted instances, where the shared host is where pages live (`PERSON_HOSTS=off`).
- **2026-09-05. Widgets and starter templates removed.** Reason: unused, stale, and every extra
  surface is another place the story can drift.
- **2026-09-05. Say "Google sign-in", never "Google only".** More providers may come; GitHub stays
  wired but unconfigured and is not advertised.
- **2026-09-05. Keep the story simple.** No "attributable writes" claim (nothing records an
  author; superseded 2026-09-27: every write records its author, shown only to the owner), no personal-data rules in the docs; one sentence that sites and their data are public.
- **2026-09-06. A site with a domain lives only there.** Its shared-host page URL 302s to the
  domain (same path), and its shared-host API takes no writes at all, key or not; agents use the
  apex or the domain. Reads stay public. Disconnecting the domain reverses both at once and
  strands links people saved to the domain; the owner accepts that. Reason: one address, one
  place to save, one place to sign in, and no back door around per-person writes.
  Amended 2026-09-29 (address families): one home address, several addresses. A site's main
  address is its custom domain (or free name), else its most specific canonical family address,
  else its own address. Saves and sign-in happen on the main address and on its family
  addresses; the site's own address redirects to the main one; every family address keeps
  working (a site with a domain of its own redirects from them to it).
- **2026-09-06. A domain binding is provisional until DNS proves it.** Unproven bindings can be
  taken over by another site and expire after 24 hours; only a verified binding is exclusive.
  Refined 2026-09-27: the proof is a TXT ownership record, and a binding past it cannot be taken
  over either.
  Reason: a name could otherwise be squatted forever by binding it without owning it.
- **2026-09-06. The connect-domain skill must carry registrar-specific help** for at least Vercel,
  GoDaddy and Porkbun, including the API call an agent can make with the user's credentials.
  Reason: the DNS record is the one step a human has to do, and it is where people get stuck.

- **2026-09-11. simple-hack.app is an organiser's page with one call to action.** Reversed 2026-09-30 (see "simple-hack.app is a hosted hackathon platform"). Audience is the
  organiser alone; the action is pasting the organiser prompt. Reason: the page had been edited
  for a day without anyone able to say what it was for, so every note about it produced a repair
  to a sentence rather than a decision about whether the sentence belonged. A capacity
  calculator, an account ceiling and a paragraph of file-size statistics all reached the live
  site that way. See "The hackathon product, and the page that sells it" above.
- **2026-09-24. The paste-back flow is removed.** Asking ChatGPT, Claude or Gemini in a browser
  to return a JSON blob and pasting it back into the page is no longer a way to build a site.
  The primary way to build is the person's own AI app — ChatGPT, Claude, Grok, Claude Code,
  Codex — with the Simple Host skill installed; the in-app AI is secondary. Reason: a newcomer was being asked to choose between three routes before they knew
  what any of them meant, and the third one asked non-technical people to handle JSON. The
  pages and the in-app AI's instructions (`generate.go`) point at
  it today and change with the rebuild. Done 2026-09-24 (Get started rebuilt, flow removed).
- **2026-09-24. The main way anyone uses Simple Host is a skill in their own AI app** (ChatGPT,
  Claude, Grok, Claude Code, Codex, and the like). Pages, onboarding and support are designed
  around getting the skill into that app, not around the in-app builder. Open problem, to fix:
  in chat apps the skill has no lasting sign-in, so every new chat asks for an email and a code.
  A sign-in that persists (the plugin/connector route) is required for this path to feel
  seamless. Solved 2026-09-24 by the connector (next entry); coding agents keep their key.
- **2026-09-24. In AI chat apps, you sign in once and stay signed in.** Simple Host becomes a
  connector: a remote MCP endpoint behind OAuth, reusing the normal sign-in page (Google or an
  emailed code). The person adds it once in their AI app, signs in once in a browser window,
  and every later chat is already signed in. The skill stays as the know-how; the connector
  carries identity and actions. Reason: asking for an email and a code in every new chat is the
  biggest friction on the main path. Coding agents (Claude Code, Codex) already persist the key
  in a config file and keep working as they do. Order: after the page overhaul, before the Get
  started rebuild, so Get started ends with "add the connector". Port the MCP adapter from the
  enterprise repo (`internal/mcp`); the OAuth server is new and gets a security review.
  Built 2026-09-24 (`https://simple-host.app/mcp`).
- **2026-09-24. No per-person subdomains.** Reversed 2026-09-25 (see "Per-person subdomains").
  Sites stay at `sites.simple-host.app/<handle>/<site>`.
  A person who wants their own origin brings their own domain; a Simple Host subdomain can be
  given on request but is not offered or advertised. Consequence: on the shared host, what a
  page saves stays readable by anyone, so anything private (an RSVP list, survey answers,
  orders) needs the site on its own domain. Reason: owner's call — keep the shared host as is.
  Partly superseded 2026-09-24: sites may now claim a free `<name>.simple-host.app` address
  themselves (see "Free <name>.simple-host.app addresses" below).
- **2026-09-24. Shared address: anyone can view, only signed-in people can save.** Rewritten
  2026-09-25: sites now live on their owner's address, where every page save already needs a
  signed-in visitor; this entry now only governs the old shared address while it still serves.
  Reverses
  2026-09-06 ("anyone can read and write"). On `sites.simple-host.app`, reading a site and its
  data stays open; every save from a page (state and collections: comments, RSVPs, votes)
  requires a visitor signed in with Google or an emailed code; the owner's agent saves with its
  key or the connector as before. Accepted limit: all sites share one origin, so a hostile site
  there could save something in a signed-in visitor's name; it cannot read anything private
  because nothing there is private. Sites on their own domain keep full protection. Reason: stop
  anonymous spam and tie every write to a real account. **Built 2026-09-26.** Pages there now
  302 to the person address, so sign-in is not offered on the shared host at all: a write there
  needs the owner's key or the connector, and anything else gets 401 `visitor_auth_required`.
  Applies only with `PERSON_HOSTS=canonical`; event and self-hosted instances keep 2026-09-06.
- **2026-09-24. Private collections on a site's own domain.** Rewritten 2026-09-25: "own domain"
  now includes the owner's own address (since 2026-09-26, the site's own address), so any site
  can have private lists without a domain.
  The owner can mark a collection
  private: only signed-in visitors on the site's own domain submit (the server stamps their
  verified email), and only the owner reads it (key, connector, CSV, dashboard, or signed in on
  the domain); everyone else gets 404, and the shared host refuses them. Reverses the non-goal
  "Sign-in gates writing, nothing gates reading" for private collections on the site's own
  domain. The Simple Host operator can also read them, for moderation. The owner (and the
  operator) can edit or delete items in a private list; public lists stay append-only. Owner
  request. Reason: orders, RSVPs and surveys need owner-only reads. The append-only part is
  reversed for the owner on 2026-09-27 (see that decision below).
- **2026-09-24. Free <name>.simple-host.app addresses.** Rewritten 2026-09-25: still offered and
  unchanged, but no longer needed for sign-in or privacy (the site's own address gives both); it is
  a shorter address of the site's own, and it shares one namespace with handles. A site may self-serve a free
  `<name>.simple-host.app` address: first come, first served, verified at once, reserved names
  refused; it behaves exactly like a custom domain. Replaces "a Simple Host subdomain can be
  given on request but is not offered or advertised" for sites that need privacy; the skills
  offer it first when a site collects anything personal.
- **2026-09-25. Per-person subdomains.** Reverses "2026-09-24. No per-person subdomains". Every
  account's handle is its own address: `https://<handle>.simple-host.app/` lists the person's
  public sites, and each site lives at `https://<handle>.simple-host.app/<site>/` — the address
  every tool, page and skill hands out. All existing sites moved automatically. Old
  `sites.simple-host.app/<handle>/<site>/` links keep working (they redirect: the nginx step
  shipped 2026-09-25 16:26 UTC); claimed `<name>.simple-host.app` addresses and custom domains are
  unchanged, and a site with one of those lives there (its person-address URL redirects to it).
  The person address is that person's own origin, so visitors sign in there, every page save
  there needs a signed-in visitor, and private collections work there. Handles, claimed names,
  reserved names and retired per-name hostnames are one first-come namespace, checked both ways.
  The operator account's handle `admin` became `simple-host-team` (old links keep resolving
  through an alias); no other handle changed. Accepted cost: what a page kept in the browser
  (localStorage) starts empty at the new address; server-saved data moves with the site. Event
  and self-hosted instances keep the path model (`PERSON_HOSTS=off`). simple-host.app goes to the
  Public Suffix List so person addresses become separate sites to browsers too. Reason: owner's
  call — every person gets an address of their own, and sign-in and privacy stop needing a domain.
  Partly superseded 2026-09-26: sites no longer live at `<handle>.simple-host.app/<site>/` (that
  form now redirects) and a sign-in no longer covers all of a person's sites (see "Per-site
  subdomains"); the person page, the namespace and the rest stand.
- **2026-09-26. Per-site subdomains.** Every site lives at `https://<site>.<handle>.simple-host.app/`,
  its own browser origin, served at the host root. The person page stays at
  `https://<handle>.simple-host.app/`. Old `<handle>.simple-host.app/<site>/` and
  `sites.simple-host.app/<handle>/<site>/` links redirect there (302, path and query kept).
  Claimed `<name>.simple-host.app` addresses and custom domains are unchanged. Each person gets a
  `*.<handle>.simple-host.app` certificate issued automatically; until it exists their sites keep
  the person-path address, and every tool hands out whichever address is live. Sign-in is now per
  site: a visitor signs in on the site's host and that covers that site only. What a page kept in
  the browser starts empty at the new address; server-saved data moves with the site. Event and
  self-hosted instances keep their current model (`SITE_HOSTS=off`). Reason: an origin of its own
  for every site, and nicer addresses. Owner approved 2026-09-26. Built and live 2026-09-26
  (`SITE_HOSTS=canonical`). Certificates are capped at 40 new per week and 12 per day (Let's
  Encrypt counts every `*.simple-host.app` certificate against one limit); the Public Suffix
  List entry, still planned, would lift that cap as well as separating sites for browsers.
- **2026-09-26. API keys are stored only as hashes.** Each sign-in (email code or Google) issues
  a new key and shows it once; keys from earlier sign-ins keep working until the person rotates,
  which replaces them all and disconnects connected apps. A lost key cannot be shown again: sign
  in again for a new one. Reason: a leaked database must not hand out working keys.
- **2026-09-26. Visitor Google sign-in starts on the site's own address.** It sets a
  short-lived cookie there, and the final step signs in only the browser holding it. Reason: a
  sign-in finished in someone else's browser must not sign a visitor in as them (login CSRF).
  Email-code sign-in already had no cross-browser step.
- **2026-09-26. A key writes only its own account's sites.** State and collection writes with an
  API key, a connector token or the MCP server need the key's account to own the site (or be the
  platform admin); any other key gets the 404 of a missing site. Signed-in visitors on the
  site's own address are unchanged. Supersedes the "any account's key writes to any site" part
  of 2026-09-05. Reason: signup is free and the Origin gate stops only browsers, so any stranger
  with a script could overwrite or wipe every site's saved data (state-storage review).
- **2026-09-27. Keys are managed one at a time, and Sign out ends the key.** Every key has a
  name (where it came from, or typed), its last 4 characters and last-used; the owner lists,
  mints and revokes keys from the Keys panel, and Sign out deletes the key the browser held.
  Rotate stays as "Sign out everywhere". New keys start `shk_`. The organiser can replace a
  participant's keys with one new key. Refines 2026-09-26 ("keep working until the person
  rotates"). Reason: one leaked key should not force cutting off every agent and app, and a
  signed-out browser must not leave a live key behind. Owner approved 2026-09-27 (completeness plan).
- **2026-09-27. Deploy-only keys, and keys expire when unused.** A key can be minted "deploy
  only" (create, update, roll back and list sites, preview links; nothing else, one route table in
  `internal/auth/scope.go`) for CI secrets; a key unused for 180 days (`KEY_IDLE_EXPIRY_DAYS`)
  stops working, and a panel-minted key may carry a fixed expiry. `PUT ?create=1` deploys from CI in
  one call. Idle expiry rather than a fixed lifetime, because it never breaks a pipeline that
  actually runs. Reason: a key in a CI secret should not be able to delete sites or read private
  lists, and forgotten keys should not work for ever. Owner approved via the completeness plan.
- **2026-09-27. The site owner may delete or clear entries in any list, public included;
  visitors still only append.** The owner deletes one entry (owner app, API, MCP
  `delete_collection_item`) or empties a whole list after typing its name (owner app, API
  `DELETE .../collections/{c}` with `{"confirm": "<c>"}`, MCP `clear_collection`). Editing an
  entry stays private-lists only. Reverses "public lists stay append-only" (2026-09-24) for the
  owner only. Reason: spam could not be removed. Owner approved via the completeness plan.
  Refined the same day by "Saved data step 2": a visitor sees, changes and withdraws their own
  Submissions; Shared boards are edited item by item.
- **2026-09-27. The owner app is the one place to manage sites.** Rename, domains (with the DNS
  record, last problem and "Check again"), every list with its public/private switch, saved
  data, Download and Delete live on `/<handle>`; the apex dashboard lists sites and links there
  for anyone with a handle. Reason: `/dashboard` already sent those people to the owner app,
  where half the controls were missing. Owner approved via the completeness plan.

- **2026-09-27. Custom domains go live without the operator.** Once a connected domain's DNS
  points here its certificate is issued automatically (a root issuer, HTTP-01) and a binding
  whose DNS points here no longer expires while it waits. Connecting a new domain keeps the
  site's current address serving until the new one works, then the old one redirects. A
  working domain that fails every check for a day emails the owner; after three days it stops
  being the site's address and can be connected afresh by whoever holds it. A free
  `<name>.simple-host.app` a site lets go stays with that site (it redirects, or says the site
  was removed) and nobody else can claim it. Owner approved ("automate"), completeness plan.
  Reason: the self-serve flow could not finish without the operator, and a lapsed or switched
  address stranded visitors.
- **2026-09-27. Custom domains need a TXT ownership record.** A custom domain counts as verified
  (and gets a certificate) only while the DNS TXT record `_simple-host.<domain>` holds the site's
  own random token; "something on this server answered HTTPS" no longer proves anything. The
  issuer refuses any name another server on the box already answers (exact, wildcard or regex
  `server_name`) and reuses only certificates it issued. A lapsed domain is released completely
  (no live server or certificate left behind) and needs the TXT record to be connected again.
  Each account asks for at most 5 new domain certificates a day; taken-down sites get no checks
  or certificates. Domains verified before this decision keep working without the record while
  they stay verified. Refines 2026-09-06 ("provisional until DNS proves it") and the decision
  above. Reason: a security review found any account could bind operator and customer
  hostnames on this box (trip.chhotabreak.com, *.quotes.chhotabreak.com) and be marked verified
  for them. Orchestrator-mandated security fix, 2026-09-27.

- **2026-09-27. People can download all their data and delete their account and all data
  themselves; deletion is immediate and final.** "Download my data" and "Delete my account" in
  the owner app (and `/dashboard` for accounts without a handle), `GET /v1/me/export.zip` and
  `DELETE /v1/me`. Deletion bypasses Recently deleted, also removes the person's entries in other
  people's lists, and retires their handle and names so nobody inherits their links. Refused for
  suspended and admin accounts and while event hostnames are held. Reason: GDPR and trust.
  Owner approved 2026-09-27.
- **2026-09-27. Download my data is one .zip.** A folder per site (`sites/<name>/`: files/,
  state.json, collections.json), sites in Recently deleted too (`recently-deleted/<name>/`, with
  when each goes for good), and the account documents. `GET /v1/me/export.zip`; the older
  `/v1/me/export.tar.gz` serves the same zip so agents that used it keep working. The per-site
  download and `export_site` stay .tar.gz. Reason: a .zip opens with a double click everywhere,
  and a site that is only deleted is still the person's data. Owner decision 2026-09-27.
- **2026-09-27. Idle sites are cleaned up, with warning and an easy way back.** A site with no
  visits by people and no new version for 90 days: its owner is emailed (reply-to support) with
  one-click "Keep it" (resets the clock, no sign-in) and "Download it" links; 30 days later with
  nothing done it moves to Recently deleted (7 days) and a second email carries a one-click
  "Restore it" link. Never touched: sites with a custom domain or claimed name, sites the owner
  marks Keep (owner app and `keep_site`), sites the owner took offline (they acted on it on
  purpose), preview sites, admin and operator sites, taken-down sites. The owner's
  own visits cannot be told apart from anyone else's (visitor addresses are only kept hashed), so
  any person's visit counts. Off until `IDLE_CLEANUP=on`, with an admin dry run to check first,
  a per-run email cap, and nothing done while visit records are too short or stale to trust.
  Reason: abandoned free sites pile up; a removal the owner did not see coming would break trust.
  Owner approved 2026-09-27 (completeness plan). Tightened after review 2026-09-27: activity also
  counts saved-data and list writes and any restore; the emailed links open a confirmation page
  and act on its button (mail scanners follow links); no "Download it" link (an export holds
  private lists, so it needs a sign-in); the operator's accounts (`IDLE_CLEANUP_EXEMPT_HANDLES`),
  the plugin reviewer and event accounts are exempt; nothing runs if visit records are more than
  6 h stale. Reason: a site in real use must never be removed.
- **2026-09-27. www and the bare domain both work.** Connecting `brand.com` or `www.brand.com`
  also sets up the other as a redirect to the one chosen, on the same certificate, with one TXT
  proof on the chosen name, but only if the other points here and nothing else on the server
  answers it; otherwise it is reported as not set up, with why. Owner approved 2026-09-27
  (completeness plan).

- **2026-09-27. Saved-data redesign approved: kinds Page info, Submissions, Personal, Shared
  board; every number configurable.** Every piece of saved data gets a name and one kind, picked
  once by the owner's agent: **Page info** (`content`, the owner writes, anyone reads),
  **Submissions** (`entries`, visitors add; private to the owner unless made public; a visitor
  sees, changes and withdraws their own), **Personal** (`mine`, one private record per person)
  and **Shared board** (`board`, a list a group edits). All four will be built, in steps. The
  nine recommendations are accepted: a key writes only its own account's sites (already live);
  undo reaches back 30 days by time, always keeping one version per item per day; visitor
  whole-document replaces are logged for 7 days and then become owner-only; the four kinds, with
  a wrong choice failing safe; Submissions private to the owner by default; Enterprise encryption
  with a cluster secret first, a KMS later; live updates wait (polling covers today); `notify`
  daily by default on private Submissions, off on public ones; "Only these people" accepts whole
  domains. Every time, limit and size is an env knob whose default is the plan's value
  (`SAVED_DATA_*`). Step 1 (the safety floor) is built first and changes nothing a page may do:
  30-day history and undo for saved data and list items, recoverable deletes and clears, the
  author recorded on every write (shown only to the owner; this supersedes "the store records no
  actor" in 2026-09-05), idempotency keys, exact numbers, stable limit codes, a read rate limit and
  a per-site total, and the 7-day watch; the three tightenings (owner-only whole replace,
  object-only documents, bounded visitor `inc`) come in a later step after the watch. Every site
  existing before the kinds keeps today's behaviour (`legacy_data`). Reason: saved data could be
  wiped with no way back and without knowing who did it, and the open model does not scale to
  the forms people actually build. Owner approved 2026-09-27 (state-review page, plan).
- **2026-09-27. Saved data step 1, review fixes.** The per-site total counts live data only
  (page data and live list items; history keeps its own thinned cap, Recently deleted is not
  counted) and refuses only writes that grow it, so a flood can never lock an owner out of their
  own site and clearing a list makes room at once. The owner can delete for good what the undo
  holds (one Recently deleted item, a list's whole Recently deleted, a site's history), behind a
  typed confirmation, logged, for erase requests and floods. `Idempotency-Key` applies only to a
  writer with an identity (owner key, connector, signed-in visitor) and never stores a response
  body. The operator's moderation is shown to owners as "Simple Host (operator)". Reason: the
  review of step 1 found a flood could fill a site for 30 days with no way out, and replayed
  answers could fill the shared disk. Decided under the approved plan; the owner may overrule.

- **2026-09-27. Saved data step 2 (kinds), as built.** A name is declared with
  `PUT /v1/sites/{s}/data/{name}/kind` (connector `declare_data`); sites created from now on take
  no saves under an undeclared name (`declare_first`), sites that existed before keep today's
  behaviour for undeclared names (`legacy_data`) and page data (`/state`) is unchanged everywhere
  until the watch ends. Page info is one JSON object per name; Submissions keep using the
  private/public list underneath, so every list route, the owner app and the undo work on them.
  A visitor's own entries are theirs to list, change and withdraw (a 10-minute undo of their own
  withdrawal; the owner restores anything for 30 days); anything not theirs answers 404, never
  "not yours", so nothing about other people's entries is revealed. "Who may save" is one setting
  per site (not per name), covering every visitor write on the site; the owner always may, and a
  blocked person can still withdraw what they sent. The digest email goes to the site owner's
  sign-in address through the existing notice sender; its stop link is signed with a key derived
  from the admin key (it must outlive restarts) and acts only on POST from a confirmation page.
  Filter fields, per-person quotas and the deploy-time `data` map are left for a later step.
  Reason: the approved plan's step 2, smallest complete version. Decided under the approved
  plan; the owner may overrule. (Its "declare first" default is superseded by the next entry.)
- **2026-09-27. Undeclared saved data is Shared and public by default on simple-host.app;
  configurable with SAVED_DATA_DEFAULT_KIND.** Data a page saves without declaring a kind is the
  fourth kind, **Shared**: anyone can read it, and signed-in visitors can save to it (today's
  behaviour), so old skills, AI create, dashboard uploads and existing pages keep working on new
  sites too. `SAVED_DATA_DEFAULT_KIND=declare_first` makes an install strict (an undeclared name
  on a site made after the kinds refuses saves); sites from before the kinds are Shared either
  way. The per-site switch on create is gone: `legacy_data` only marks the sites from before.
  Page info, Submissions and (later) Personal stay opt-in upgrades the AI declares; the skills
  teach it that undeclared means Shared and public, that anything with personal details (RSVPs,
  orders, sign-ups) is private Submissions, owner-only content is Page info, and to prefer the
  stricter kind when unsure. The three tightenings for Shared after the 7-day watch remain
  deferred. With it (review of step 2): declared Submissions take entries only from a signed-in
  visitor; a block and one per person count an address with its `+tag` dropped (identities stay
  cheap: "per signed-in account"); an `@domain` allow trusts the address a sign-in verified,
  including a Google account made with a company address after its owner left (superseded
  2026-09-27: sites see the account's current sign-in email); a private name
  that holds entries becomes public only with `confirm_public` and never becomes Page info; the owner may always save and
  undo; Submissions names per site are capped (`SAVED_DATA_ENTRIES_NAMES_MAX`); submission emails
  are claimed before sending, so they go out once and a failed send is not retried. Reason: a
  strict default broke every site built by an older skill or by AI create on arrival. Owner
  decision 2026-09-27.
- **2026-09-27. Personal is as private as the site's pages (review of steps 3-4).** The site's
  own pages run in the visitor's browser on the site's origin, so a page the owner (or anyone who
  can publish there) writes can read the visitor's record and send it elsewhere; code cannot stop
  that while the owner controls the pages. So the promise is worded everywhere as: "Simple Host's
  owner tools never show a person's Personal record; the site's own pages run in the visitor's
  browser and can read that visitor's record, so only use Personal on sites you trust", and the
  skills tell the AI never to write a page that sends a record anywhere else. Also decided with
  the review: Personal names are stored `private = true` (an older binary fails closed; no
  rollback below v0.7.0 once one exists); kind changes to or from Personal are checked under the
  name's lock; at most 1,000 people per Personal name (`SAVED_DATA_PERSONAL_PEOPLE_MAX`); the
  owner sees a Personal name's count and size only from 3 people up; a person's delete after an
  owner clear sticks through the owner's Restore; board writes are also limited per signed-in
  person (`SAVED_DATA_BOARD_WRITES_PER_MIN`, 30); a board's Restore all names a window and every
  owner restore keeps the board's cap. Owner-approved review fixes, 2026-09-27.
- **2026-09-27. Saved data steps 3 and 4 (Personal, Shared board), as built.** **Personal**
  (`mine`) is one private record per signed-in visitor per name. Only that visitor writes it;
  Simple Host's owner tools (key, connector, owner app, CSV, history, Recently deleted, the
  site's download) and the operator never show one (the site's own pages can read it for their
  visitor; see the review decision above): the owner sees how many people have a record
  and their size, and can clear the name for everyone (restore brings back only what the clear
  took). This is stricter than the plan's "the owner can export it": the build brief said
  Personal content is never visible to the owner, and a record is its person's (it is in their
  own Download my data and goes with their account). A name must be empty to become Personal
  and a Personal name that holds records never becomes another kind. The visitor lists and
  restores their own record's 30-day history. **Shared board** (`board`): anyone reads it; any
  signed-in visitor allowed to save adds, changes and deletes any item, one at a time, with an
  optional version check (409 `version_conflict`); whoever deleted an item can undo it for a few
  minutes; only the owner clears it; no live feed (pages poll with `If-None-Match`). Both need
  the site's own address. Every number is a `SAVED_DATA_*` knob (64 KB per record, 16 KB per
  item, 2,000 items per board, 20 names of each per site). Reason: the approved plan's steps 3
  and 4, smallest complete version. Decided under the approved plan; the owner may overrule.

- **2026-09-27. People can change their handle, also after publishing.** The dashboard shows the
  address with a Change button; after sites exist the change keeps the old handle as an alias, so
  old links redirect, rate-limited through `handle_changed_at`. The new handle gets its own
  certificate; what pages kept in the browser starts empty. Replaces "the address is fixed once
  something is published". Reason: a handle taken from an email's local part is public and
  otherwise permanent. Owner approved 2026-09-27; built 2026-09-27 (branch cp/recover): once per
  30 days after publishing, changes before publishing stay free; the owner app's "Your address".
- **2026-09-27. Deleting a site is recoverable for 7 days.** A deleted site goes offline at once
  but keeps its files, versions, saved data, private lists, claimed names and its name for 7 days
  ("Recently deleted", with Restore on the owner app, the API and the connector), then it is
  removed for good. Deleting a whole account (admin) stays immediate. Reason: an owner (or their
  agent) deleting the wrong site lost every RSVP and order with one click. Owner approved
  2026-09-27 (completeness plan).
- **2026-09-27. A handle that ever published stays held after a change.** Even with no sites
  left (all purged), the old handle is kept as an alias of the account, so old links and
  hand-made vhosts keyed on the handle folder never follow a stranger who claims it; only a
  handle that never published anything is freed. Reason: security review L3. Decided under the
  owner's security-fix go, 2026-09-27.
- **2026-09-27. Every "get the intruder out" lever also ends sign-ins on sites.** Sign out
  everywhere, the admin's new key, email change and its undo, removing a Google/GitHub sign-in
  and suspension end the account's visitor sessions, its own sites included (where they carry
  owner powers over saved data). Reason: security review M1. Owner decision 2026-09-27.
- **2026-09-27. A deploy key is as powerful as the site it publishes.** No behaviour change:
  every place a deploy key is made says "a deploy key can publish code that runs when you open
  your own site; treat it like the site itself". Reason: security review M2. Owner decision
  2026-09-27.
- **2026-09-27. Sites see the account's current sign-in email.** Who may save, blocks, one per
  person and the `_submitted_by` stamp use the account's current address, not a Google address
  linked before it moved; existing stamps stay. This replaces the step-2 note that an `@domain`
  allow trusts a Google account made with a company address after its owner left. Reason:
  security review M3. Owner decision 2026-09-27.
- **2026-09-27. Reserved names for new claims.** Names that read as the service, its operator or
  a sensitive function (support, admin, billing, login, simple-host and the rest of the list in
  `docs/advanced/server-and-addresses.md`) are refused to new handles and new free addresses,
  and the platform-sounding subset to new site names. Existing holders keep theirs. Reason:
  impersonation. Owner decision 2026-09-27.
- **2026-09-27. The enterprise pages are light by default.** Replaced by the 2026-09-28 decision
  below: those pages follow the site-wide theme like every other page.
- **2026-09-28. One light/dark setting for the whole site; follows the visitor's system by
  default; a single override applies everywhere.** Every page the app serves looks the same way:
  with nothing picked it matches the visitor's system (light when there is no preference); the
  header's theme menu offers Match my system, Light and Dark, and that one choice is kept once
  and applies to every page before first paint. No page has a theme default or auto-dark of its
  own; the navy pages keep their identity with a navy dark palette. Reason: pages behaved
  differently (some followed the OS, some were light only). Owner decision 2026-09-28.
- **2026-09-27. /internal/ is closed to outside requests.** Every nginx vhost's
  `location ^~ /internal/` carries `internal;` (`deploy/prod/nginx-internal-lock.sh`, the
  content host included), so those pages are reached only through nginx's own rewrites and
  error pages. Reason: security review L2. Owner approved 2026-09-27.
- **2026-09-28. Enterprise on AWS: a quick path by Terraform.** /setup asks where Enterprise
  runs; AWS (plus "already have a cluster?") gives one line for AWS CloudShell that runs
  `deploy/terraform/aws` in the enterprise repo; "Something else" keeps the agnostic
  config.env path as it was. Azure is paused by the owner and Google Cloud untested, so
  neither is offered (a feature works fully or is not offered); each is one `CLOUDS` entry
  to switch on. Ingress is Traefik + cert-manager with
  the address delegated to a cloud DNS zone, so owner certificates stay automatic whatever DNS
  provider holds the parent. Reason: owner asked for an easier path by cloud, "keep it simple".
  Owner decision 2026-09-28.
- **2026-09-28. Skills check with the person before anything goes public.** Before a new site
  goes online the first time, the agent asks once (site name, address, public to anyone with
  the link) and waits for a yes. It always asks before deleting a site or data, making private
  data public, changing who can see or save, connecting a domain, rolling back or taking a site
  offline. Updates to a site the person asked for in the same conversation go ahead without
  asking again, since publishing is the point of the product. No skill tells an agent to act
  without confirming. Applies to every skill copy, `llms.txt` and the connector instructions
  (skills 0.27.0, OpenAI plugin 0.9.1). Reason: agents flagged auto-publish (an agent installing
  the skill with npx warned its user that the skill "tells assistants to publish on their own
  without checking with you"). Owner decision 2026-09-28.
- **2026-09-28. Terms refresh: commerce allowed, misinformation named.** Selling from free sites
  is fully allowed ("they can do whatever they want"): the terms say payment goes through a
  payment provider and the site owner is the seller, and add no rule restricting commercial use.
  The named regulated-goods list stays ("common sense"). A misinformation rule in the style of
  GitHub's and OpenAI's policies: false health or medical claims likely to endanger people,
  misleading voters about when, where or how to vote, and manipulated media meant to deceive
  about real events; satire, parody and opinion are fine. Timings: 48 hours for intimate-image
  removal, 30-day appeal window, 14 days' notice of adverse changes. The idle-sites line waits
  until idle cleanup is on. Reason: the 2026-09-28 gap review against OpenAI, GitHub, Netlify,
  Vercel and Cloudflare, and the TAKE IT DOWN Act. Owner decision 2026-09-28.
- **2026-09-28. Report a page on the platform.** `/report` is a small form that emails support;
  it stores nothing, takes only pages hosted here, and is linked from the footer, the home page,
  the terms and support. The take-down and offline pages stay bare (they load nothing).
  Reason: the intimate-image law wants the process on the platform, not only in the terms.
  Owner decision 2026-09-28.
- **2026-09-28. User sites move to simple-host.site; the app stays on simple-host.app.** Every
  person page, site and free name gets its address under `simple-host.site`
  (`<site>.<handle>.simple-host.site`, `<handle>.simple-host.site`, `<name>.simple-host.site`);
  the dashboard, sign-in, the API, `/mcp`, docs, emails, `sites.simple-host.app` and the CNAME
  target stay on `simple-host.app`. Every old `.app` address keeps working and redirects to the
  same page under `.site` (302 for a week, then 301); `/v1/` on old hosts is never redirected.
  Visitors are signed out once and browser-kept data starts fresh at the new address; saved data
  moves with the site. Self-hosted, hackathon and Enterprise installs keep one domain
  (`SITE_BASE_DOMAIN` defaults to `SITE_DOMAIN`, `SITE_BASE_MOVE` to off). Rolled out in steps
  that each undo with one setting; the new addresses are handed out no earlier than about
  2026-10-28, because company and school web filters block domains registered less than 30 days
  ago (simple-host.site was registered 2026-09-28). Reason: a page on a person's address should
  never share a registrable domain with the app, and a separate domain can go on the Public
  Suffix List. Owner decision 2026-09-28 ("Build it now and switch later");
  plan: `docs/designs/site-base-domain-move.md`.
- **2026-09-29. Site passcode: one passcode on a whole site.** Reverses the non-goal "Private or
  password-locked pages" (and the 2026-07-11 removal of the old view-lock). An owner may put one
  passcode on a whole site; every address of it then shows a plain "This site is protected" page
  (noindex, nothing of the site in it) until the visitor enters it, and the site is hidden from
  the person page. The passcode is readable by the owner (sealed with a server key, not hashed),
  any 6 or more characters with no format rules (the owner's agent picks the format); an unlock
  lasts until the passcode changes or the owner signs everyone out, never expires on its own, and
  there are no end dates (owners use offline or delete). Whole site only, one code per site. The
  owner's key and connector keep working; previews bypass it; the admin can remove it or preview
  a version for moderation (logged) but never reads it. Wrong tries are limited per address and
  per site. It is not a login and says so. Reason: the view-lock was removed because every site
  shared one origin, so a lock on one could not keep the others out; since 2026-09-26 every site
  has its own origin, so that reason is gone, and the owner asked for it for travel-trip sites
  shared with a group ("go work on it", 2026-09-29). Built and deployed dark
  (`SITE_PASSCODES=off` on simple-host.app), switched on after the "address families" feature.
  Enterprise: different on purpose (its access levels are tied to real identity).
- **2026-09-29. Address families: one domain for every site of an account.** An account connects
  `*.<its domain>` once (a wildcard DNS record plus the TXT ownership record) and every site X of
  the account answers at `X.<its domain>`, with an optional site-name prefix (`*.voucher.x.com`
  serves the sites `voucher-X`). All of the account's matching sites answer, with no per-site
  opt-in (each is already public at its own address); another account's sites never do. Nothing
  is served before the proof; once verified the family is the account's alone, and one that
  stops passing its checks is disconnected after 72 hours. A family address is the site's main
  address by default (below a custom domain, above its own address; see the 2026-09-06
  amendment), with a per-family switch. Release 1 serves families with a wildcard certificate the
  operator sets up (the admin names it; the family waits until then); one certificate per site
  name, self-serve on the customer's own DNS, is a later release. Hosted only: Enterprise has no
  custom domains, so it is different on purpose. Reason: chhotabreak runs four hand-made wildcard
  families (`*.trips`, `*.quotes`, `*.voucher`, `*.guide` under chhotabreak.com) where sign-in,
  saves and analytics did not work and every change needed the operator; one domain per account
  covering every site is what she needs. Owner decision 2026-09-29; design:
  `docs/designs/address-families.md`.

- **2026-09-30. simple-hack.app is a hosted hackathon platform.** Reverses "2026-09-11.
  simple-hack.app is an organiser's page with one call to action": the page's call to action is
  now Create event, with self-hosting second. Anyone who signs in can create an event, with no
  approval; creation records who is running it (organiser name, organisation or community,
  contact email, what the event is for, expected dates and participants), which the platform
  admin sees. No caps on participants, teams or judges; abuse safety is per-account event
  limits (`EVENT_CREATE_PER_DAY`, `EVENT_MAX_ACTIVE_PER_ORGANISER`), the per-site size cap
  (`MAX_ARCHIVE_MB=25`, `KEEP_VERSIONS=2` there), an instance byte budget
  (`HACK_INSTANCE_BUDGET_GB`) and the disk alert, all env settings. Everything is free. Team
  pages live under simple-hack.app (`<team>.<event>.simple-hack.app`). After an event closes,
  team sites stay up 30 days (`EVENT_SITES_KEEP_DAYS`) and are then removed, with warning emails
  to the organiser; the event page and its results stay. Results: the public sees winners only,
  each team privately sees its own scores and the judges' comments, and the organiser can switch
  to a full public ranking. Reason: organisers asked to run events without standing up a
  server; the platform keeps what they build under one address and one set of rules. Owner
  decisions 2026-09-30; design `docs/designs/simple-hack-platform.md`.

- **2026-10-01. Enterprise installs into Kubernetes the company already runs.** The setup
  helper produces tunable Helm values or Kubernetes YAML rendered from that same chart.
  Connect existing Postgres or install one persisted Postgres in the cluster; connect the
  existing bucket, identity provider, ingress and certificate issuer. It does not create
  EKS, AKS, a control plane or cloud account infrastructure. Provider-specific storage
  examples remain optional. Reason: the owner expects an application install into their
  existing cluster, not another cluster to operate.

## Open, deliberately parked

- Whether a site that disconnects its domain should be migrated back to a "normal" shared-host site in some
  guided way, rather than just having the redirect stop. Parked 2026-09-06; revisit when it happens.
- **2026-09-28. A new account chooses its address at sign-up; pages never use native dialogs.**
  When an emailed code, link or Google sign-in would create an account, the person (or their
  agent, via `choose_handle` then `handle` on `POST /v1/auth/verify`) chooses the handle before
  it is created, prefilled with the address it would have been given; a taken or reserved one
  is refused at once, naming the address. No page uses the browser's confirm/alert/prompt: AI
  browser agents cannot see them, so the page hangs. Reason: an owner's agent got `hello-2`
  and then could not change it. Owner decision 2026-09-28.
