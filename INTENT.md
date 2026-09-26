# INTENT

## What this is, and why it exists

Simple Host is one Go binary that hosts static websites and gives every site a small JSON
backend (per-site state plus append-only collections) in the same upload. It exists because the
sliver of dynamic behaviour most ordinary websites need (save an RSVP, count a vote, keep a
guestbook) is absurdly expensive to stand up separately. Agents deploy sites through the bundled
Website Deploy skill; humans mostly never touch the API directly.

## Who uses it, and in what situation

- A person telling their coding agent "put this online". The agent registers, builds, deploys.
- The agent itself, reading `llms.txt`, the OpenAPI spec and the skills to wire up saves.
- Visitors of the hosted sites: reading pages, and signing in with Google when a page saves
  something on their behalf.
- The owner (vineetu) as operator and admin of the live instance at simple-host.app.

## What success looks like

- An agent with the skill installed can ship a working site, including a form that saves, on
  the first try, without the owner intervening.
- Every site lives on its owner's own address, `https://<handle>.simple-host.app/<site>/`
  (or on a domain of its own). There a visitor signs in with Google or an emailed code and
  saves are per-person. Agents save with an API key anywhere.
- The whole thing keeps running on a 1 CPU / 1 GB box: one binary, one Postgres, one folder.

## The hackathon product, and the page that sells it

An organiser stands up a private instance on their own cloud account for the length of an
event. Their AI agent does the whole setup; they issue a key per participant; participants
publish with whatever agent they already use. It lives at simple-hack.app, which serves the
same binary as simple-host.app.

**simple-hack.app is written for the organiser, and for nobody else.** A participant never
reads it — they are handed a key and a prompt by the person running their event. Everything on
the page about the participant experience is there to tell the organiser what they will be
handing out, not to instruct a participant.

**Its one job is to get an organiser to paste the organiser prompt into their agent.** That is
the page's only measure. A sentence that does not help someone do that, or believe it will
work, does not belong on it.

**It is also the link shared on LinkedIn**, so most readers arrive cold: no context, not yet an
organiser, possibly just curious what this is. The top of the page has to make a stranger
understand the thing in one breath, in words they already have, before it asks anything of
them. That is a constraint on the opening, not a second audience — the page still exists to
start an event.

What follows from that, and is not negotiable without changing the line above:

- No sentence about limits, capacity, disk, retention or file sizes. An organiser asks "will
  this hold my event"; the answer is yes. Anything past yes is the implementation talking.
- No section listing what the product does not do. Stated absences read as "not ready".
- Nothing condescending about the reader's skills. Not "for people who have never deployed".
- No internal telemetry as copy. Median site sizes and admin screens are operator concerns.
- Operational facts an organiser must know *to run the event* stay: what it costs, what they
  hand out, how judging gets a list, what happens to the work afterwards.

## Non-goals

- Private or password-locked pages. Pages are always public; there is no view-lock and docs
  must not advertise one. The one thing that can be private is a collection on a site's own
  address (decisions 2026-09-24 "Private collections" and 2026-09-25); every other read stays open.
- A general-purpose backend. No schema, no queries, no server-side code for site authors.
- Metered third-party AI keys. AI create runs on the local Grok sidecar only.
- Starter templates and drop-in widgets. Removed 2026-09-05; agents build pages themselves.
- Isolating one person's sites from each other. All of a person's sites share their address
  (`<handle>.simple-host.app`), so a sign-in there covers all of them; a site that needs an
  origin of its own takes a free `<name>.simple-host.app` address or a custom domain.

## Constraints

- Runs from `/usr/local/bin/simple-host` as `simple-host.service`; env in `/etc/simple-host.env`.
  Editing the repo changes nothing until rebuilt and restarted.
- Fetch and fast-forward before starting work, push when finishing. Two sessions have overwritten
  each other in production before.
- `check-docs-sync.sh` must pass: routes, OpenAPI, llms.txt and the skills drift independently.
- Hosted pages never hold an API key. Anything a page does must work with a site-scoped cookie.

## Decisions already made

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
  writes to any site (accepted as that account's write; the store records no actor, only the
  server log does), and a page can sign a visitor in by emailed code as well as Google, creating the account if it does not exist. Reason: "keep it simple";
  an agent saving on behalf of a person gets their key through the same email-code flow the
  dashboard already uses, and there is only one kind of identity to reason about.
- **2026-09-05. Email sign-in codes are bound to a purpose and, for hosted pages, to one site.**
  A visitor code cannot be redeemed at the dashboard for an API key or on another site. Reason:
  review found the opposite let a phished guestbook code become a full account credential.
- **2026-09-05. Visitor sign-in only on a custom domain.** On the shared host all sites are one
  origin, so a sign-in there can never be private to one site. Reason: "if you want safe writes
  you need a domain" is one sentence everyone can understand. Superseded 2026-09-25: a person's
  own address counts as the site's own origin (see "Per-person subdomains").
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
  author), no personal-data rules in the docs; one sentence that sites and their data are public.
- **2026-09-06. A site with a domain lives only there.** Its shared-host page URL 302s to the
  domain (same path), and its shared-host API takes no writes at all, key or not; agents use the
  apex or the domain. Reads stay public. Disconnecting the domain reverses both at once and
  strands links people saved to the domain; the owner accepts that. Reason: one address, one
  place to save, one place to sign in, and no back door around per-person writes.
- **2026-09-06. A domain binding is provisional until DNS proves it.** Unproven bindings can be
  taken over by another site and expire after 24 hours; only a verified binding is exclusive.
  Reason: a name could otherwise be squatted forever by binding it without owning it.
- **2026-09-06. The connect-domain skill must carry registrar-specific help** for at least Vercel,
  GoDaddy and Porkbun, including the API call an agent can make with the user's credentials.
  Reason: the DNS record is the one step a human has to do, and it is where people get stuck.

## Open, deliberately parked

- Whether a site that disconnects its domain should be migrated back to a "normal" shared-host site in some
  guided way, rather than just having the redirect stop. Parked 2026-09-06; revisit when it happens.

- **2026-09-11. simple-hack.app is an organiser's page with one call to action.** Audience is the
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
  pages, the in-app AI's instructions (`generate.go`) and the run-hackathon skill all point at
  it today and change with the rebuild.
- **2026-09-24. The main way anyone uses Simple Host is a skill in their own AI app** (ChatGPT,
  Claude, Grok, Claude Code, Codex, and the like). Pages, onboarding and support are designed
  around getting the skill into that app, not around the in-app builder. Open problem, to fix:
  in chat apps the skill has no lasting sign-in, so every new chat asks for an email and a code.
  A sign-in that persists (the plugin/connector route) is required for this path to feel
  seamless.
- **2026-09-24. In AI chat apps, you sign in once and stay signed in.** Simple Host becomes a
  connector: a remote MCP endpoint behind OAuth, reusing the normal sign-in page (Google or an
  emailed code). The person adds it once in their AI app, signs in once in a browser window,
  and every later chat is already signed in. The skill stays as the know-how; the connector
  carries identity and actions. Reason: asking for an email and a code in every new chat is the
  biggest friction on the main path. Coding agents (Claude Code, Codex) already persist the key
  in a config file and keep working as they do. Order: after the page overhaul, before the Get
  started rebuild, so Get started ends with "add the connector". Port the MCP adapter from the
  enterprise repo (`internal/mcp`); the OAuth server is new and gets a security review.
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
  now includes the owner's own address, so any site can have private lists without a domain.
  The owner can mark a collection
  private: only signed-in visitors on the site's own domain submit (the server stamps their
  verified email), and only the owner reads it (key, connector, CSV, dashboard, or signed in on
  the domain); everyone else gets 404, and the shared host refuses them. Reverses the non-goal
  "Sign-in gates writing, nothing gates reading" for private collections on the site's own
  domain. The Simple Host operator can also read them, for moderation. The owner (and the
  operator) can edit or delete items in a private list; public lists stay append-only. Owner
  request. Reason: orders, RSVPs and surveys need owner-only reads.
- **2026-09-24. Free <name>.simple-host.app addresses.** Rewritten 2026-09-25: still offered and
  unchanged, but no longer needed for sign-in or privacy (the owner's address gives both); it is
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
