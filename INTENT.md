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
- On the shared host anyone can read and write a site's data from the page. On a site with its
  own domain, a visitor signs in with Google or an emailed code and saves are per-person.
  Agents save with an API key anywhere.
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

- Private or password-locked pages. Sign-in gates writing, nothing gates reading. There is no
  implementation of a view-lock; docs must not advertise one.
- A general-purpose backend. No schema, no queries, no server-side code for site authors.
- Metered third-party AI keys. AI create runs on the local Grok sidecar only.
- Starter templates and drop-in widgets. Removed 2026-09-05; agents build pages themselves.
- Isolating sites from each other on the shared host. Every site there is one origin. Anything
  that needs privacy between sites, including visitor sign-in, lives on a custom domain.

## Constraints

- Runs from `/usr/local/bin/simple-host` as `simple-host.service`; env in `/etc/simple-host.env`.
  Editing the repo changes nothing until rebuilt and restarted.
- Fetch and fast-forward before starting work, push when finishing. Two sessions have overwritten
  each other in production before.
- `check-docs-sync.sh` must pass: routes, OpenAPI, llms.txt and the skills drift independently.
- Hosted pages never hold an API key. Anything a page does must work with a site-scoped cookie.

## Decisions already made

- **2026-08-14. Every state/collection write requires a signed-in visitor.** Not per-site opt-in.
  Reason: writes should cost an identity without giving pages an API key. `SPEC.md` is the
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
  you need a domain" is one sentence everyone can understand.
- **2026-09-06. Shared host: anyone can read and write.** Page saves there need no sign-in and
  no key; it is a public scratchpad guarded by rate limits and size caps. Reason: keep the
  shared host simple and useful; sign-in remains a feature you get by connecting a domain.
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
