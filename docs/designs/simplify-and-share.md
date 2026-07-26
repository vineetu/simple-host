# simple-host.app: simplification pass + the sharing gap

Status: §1, §2, §6 **implemented** on this branch · §3 partially · §4 **not started**
Date: 2026-07-25
Scope: port the simplification work already shipped on the sibling internal
instance, and close the collaboration gap.

**Jump to [§7 Release plan](#7-release-plan) for what actually shipped and what
is still outstanding.**

---

## 0. Ground truth (verified 2026-07-25, not from memory)

| Fact | Evidence |
|---|---|
| Live host `https://simple-host.app` serves plugin **0.8.4** | `GET /skills/version` → `{"version":"0.8.4"}` |
| Live is byte-identical to `origin/main` @ `4037578` | downloaded `/skills.zip`, diffed all three `SKILL.md` against the working tree — identical |
| The repo has **no CI and no deploy script** | no `.github/`, `scripts/` holds only `build-symlink-farm.sh` and `check-docs-sync.sh` |
| The box is nginx-fronted at `209.151.146.185` | `curl -I` → `Server: nginx`; no SSH credentials available to this workstation |
| `go build`, `go vet`, `go test ./...` are green on `main` | run locally before any edit |
| Three skills ship: `website-deploy` (21,888 B), `website-deploy-builder` (14,684 B), `connect-domain` (6,975 B) | `wc -c simple-host-website/skills/*/SKILL.md` |
| `npx skills add vineetu/simple-host` works and discovers all three skills | ran `npx skills@latest add vineetu/simple-host -l` |
| There is **no collaboration/sharing** anywhere in the backend | `grep -ri "editor\|collaborat" internal/` → zero authorization hits |

The sibling internal instance (same lineage, diverged) shipped a simplification
pass over the last ~40 commits. This document is what of that pass applies here,
plus what this codebase needs that the internal one already has.

---

## 1. The core problem: the skill is a 22 KB wall

`simple-host-website/skills/website-deploy/SKILL.md` is **21,888 bytes** in one
file. Every agent that touches this platform loads all of it — the framework
build table, the pre-flight checklist, the widget CSS-variable palette, the
GitHub-Pages-as-a-backend recipe — before it can answer "deploy this folder."

The internal instance solved this with a **router + on-demand references**:

```
simple-host/
  SKILL.md                              9,432 B   <- routing + rules that always apply
  references/account-recovery.md        3,070 B
  references/collaboration.md           7,220 B
  references/frameworks.md              4,683 B
  references/packaging-and-validation.md 6,745 B
  references/state-and-ai.md            3,102 B
  references/updating.md                3,992 B
```

`SKILL.md` carries only what is true for *every* operation, plus a table that
says which single reference to read for the operation at hand:

| Operation | Required reference |
|---|---|
| Install or update skills | `references/updating.md` |
| Register or recover a key | `references/account-recovery.md` |
| Edit, download, roll back, delete, or share an existing site | `references/collaboration.md` |
| Detect and build a framework for subpath hosting | `references/frameworks.md` |
| Validate, package, upload, and verify a site | `references/packaging-and-validation.md` |
| Add state, search, or AI/browser capabilities | `references/state-and-ai.md` |

**Change 1.1 — split `website-deploy/SKILL.md` into a router plus references.**

Proposed split (byte estimates from the current line ranges):

| New file | From current SKILL.md | ~Size |
|---|---|---|
| `SKILL.md` (router) | §Service, §Workflow Overview, §Two ways to deploy (short form), §Key Knowledge, the reference table | ~6 KB |
| `references/register.md` | §Register (lines 49–79) | ~2.5 KB |
| `references/frameworks.md` | §Framework Detection and Build (80–103) | ~3 KB |
| `references/packaging-and-validation.md` | §Pre-Flight Checks, §Upload, §Post-Deploy (104–162) | ~5 KB |
| `references/backend.md` | §Backends & extras, state, collections, widgets, templates (192–305) | ~8 KB |
| `references/domains-and-privacy.md` | §Custom domains, §Private pages — merge with the standalone `connect-domain` skill | ~4 KB |
| `references/operations.md` | §Other Operations, §Analytics (163–191) | ~2 KB |

`buildSkillsZip()` and `buildSingleSkillZip()` both walk recursively (`fs.WalkDir`
over the skill root), so nested `references/` directories are packaged into
`/skills.zip` and `/skills/<name>.zip` with **no Go change**. Verified by reading
`internal/handler/ui.go`.

Net effect: the always-loaded surface drops from ~22 KB to ~6 KB, and an agent
answering "deploy this folder" reads ~11 KB instead of 22 KB.

### 1.3 — The split breaks the URL-install shapes unless we handle them

Three documented install paths fetch **a single `SKILL.md` by URL** and never see
a sibling directory:

| Path | Where |
|---|---|
| `copilot skill add https://simple-host.app/skills/website-deploy/SKILL.md` | `install.html:278-279` |
| `hermes skills install …/SKILL.md --name website-deploy` | `install.html:295` |
| `/.well-known/skills/index.json`, which advertises `"files": ["SKILL.md"]` | `internal/handler/skillshub.go:141` |

A user on any of these ends up with a router that points at `references/*.md`
files they do not have. That is strictly worse than the 22 KB monolith.

Required alongside the split:

1. Each reference pointer in `SKILL.md` carries **both** the relative path and an
   absolute URL, so an agent that lacks the file can fetch it:
   `references/backend.md` (or `https://simple-host.app/v1/skills/website-deploy/references/backend.md`).
2. Serve those URLs — extend `skillshub.go` with
   `GET /v1/skills/{name}/references/{file}` and the `.well-known` equivalent.
3. `wellKnownEntry.Files` must be **enumerated from the skill directory**, not
   the hardcoded `[]string{"SKILL.md"}` it is today
   (`skillshub.go:141`), so discovery hubs pull the whole skill.

**Change 1.2 — do the same to `website-deploy-builder` (14,684 B).** Its
"Capability tree" (8 numbered subsections) is reference material, not routing.

---

## 2. The install page tells people the wrong thing about ChatGPT

`internal/handler/static/install.html` predates OpenAI merging the Codex app into
the ChatGPT app (~2026-07-09). Today the page offers three paths, and the
ChatGPT path is the *paste-JSON-back* path:

> `install.html:313-323` — **"In ChatGPT, Gemini or Copilot / Copy a prompt,
> paste back the result."**

That was correct when ChatGPT was a web chat with no filesystem. It is now wrong
for anyone on the ChatGPT **desktop** app, which has a filesystem-capable local
mode and reads the same skills directory Codex CLI used. Those users are told to
copy-paste JSON when they could install the skill and just say "deploy this."

**Change 2.1 — give ChatGPT desktop a real skill-install path.** Keep the
paste path, but demote it to "ChatGPT on the web, Gemini, or a phone." Add
ChatGPT desktop to the skill path alongside Claude Code and Cursor.

**Change 2.2 — fix the stale agent labels.** `install.html:255` labels
`~/.agents/skills/` as **"Codex CLI"**. That directory is now what ChatGPT
desktop and Cursor read. Relabel it by what the folder *is*, not by one product
that used to own it.

**Change 2.3 — fix the Windows instruction in the pasteable prompt.**
`install.html:272` says:

> `(On Windows, ~ means %USERPROFILE%.)`

`%USERPROFILE%` expands in `cmd.exe` only. In PowerShell it is a literal string,
so an agent following this creates a folder *named* `%USERPROFILE%`. This is the
exact bug fixed on the internal instance. Correct form:

> Work out my home folder using the shell you are actually in: `$HOME` on macOS
> or in PowerShell, `%USERPROFILE%` only in `cmd`.

**Change 2.4 — `connect-domain` is missing from the hand-registered install
routes and from every list on the page.**

Precisely: the *dynamic* skills hub already handles it —
`GET /v1/skills` reports `count: 3` including `connect-domain`,
`GET /v1/skills/connect-domain` returns 200, and
`/.well-known/skills/index.json` lists it (all verified against live). The gap is
the **hand-registered legacy routes**, which name only two skills:

- `/skills/connect-domain.zip` → **404** (verified). `serveSkillZip` /
  `serveSkillMarkdown` are registered for `website-deploy` and
  `website-deploy-builder` only (`internal/handler/ui.go:74-77`).
- Those are exactly the routes `install.html` links to.

Fix by registering the routes for every bundled skill (loop over
`listBundledSkills()` rather than adding a third hardcoded pair).
- The Claude.ai / Claude Desktop row (`install.html:260`) therefore offers two
  ZIPs out of three.
- The GitHub Copilot lines (`install.html:245-246`, `278-279`) install two skills
  out of three.
- The pasteable agent prompt (`install.html:272`) tells the agent to confirm
  "website-deploy and website-deploy-builder now appear" — a user who follows it
  will believe a partial install succeeded.

Fix: register the two missing routes and add `connect-domain` to every list.
This is a genuine defect, independent of any simplification.

**Change 2.5 — register the user during install.** The internal instance folded
account creation into the install prompt: the agent asks for the email, echoes it
back for confirmation, POSTs it, and saves the key to a config file. Here,
registration is a separate trip to the website (sign in, 6-digit code) *before*
the skill is useful, and the skill's own §Register section (a 30-line procedure)
duplicates it. Folding registration into install removes one context switch.

Caveat specific to this codebase: registration is a **two-step** email
verification (`POST /v1/auth` → code → `POST /v1/auth/verify`), not the internal
instance's single call. The install prompt must wait for the user to read their
inbox, which makes it longer than the internal one. Recommend keeping it as an
*optional step 4* in the prompt rather than a blocking one.

---

## 3. The homepage is a sign-in wall

`internal/handler/static/index.html` (101,772 B) is not a marketing page — it is
the application. A logged-out visitor gets a headline, three feature cards, and
an email box (`index.html:1015-1027`). Everything else — the AI builder, the site
list, the "Deploy with your agent" callout (`index.html:1139-1150`) — is behind
authentication.

The internal instance replaced its equivalent with a **four-step journey with
progressive reveal**: only step 1 is visible; each step reveals the next as you
complete it, and progress survives a reload via `localStorage` with a bounded
TTL. The point is that a first-time visitor sees exactly one instruction.

**Change 3.1 — put the journey in front of the sign-in wall**, not behind it.
A logged-out visitor should be able to (1) copy an install prompt, (2) see what
to say to their agent, without creating an account first. Registration then
happens *inside* the agent (Change 2.5), which is where the user already is.

**Change 3.2 — one visible step at a time.** Ship steps 2..N `hidden`; reveal on
the previous step's action. Two implementation notes learned the hard way on the
internal instance, both worth carrying over:

- Fire the reveal **before** the clipboard write, not in its `.then()`. A user
  whose clipboard is blocked otherwise gets stranded with no next step.
- `display: grid` on an `<li>` overrides the UA's `[hidden] { display: none }`.
  An explicit `.journey__steps > li[hidden] { display: none; }` rule is required.

**Change 3.3 — bound the persisted progress at both ends.** The internal
implementation stores `savedAt` and discards the entry when
`age < 0 || age > TTL` — a clock skew that future-dates the entry otherwise
passes a naive `age > TTL` check and restores stale progress forever.

**Change 3.4 — add a FAQ.** The public app has none. The internal instance added
one after a support thread revealed the docs never stated who can see a site.
The questions that apply here (the answers differ — see §5):

- Are my sites public by default?
- Can someone else change or overwrite my site?
- Is the shared data on my site private?
- What is this good for?

The third one matters more here than internally: `sites.simple-host.app` is a
**shared origin across all sites**, and state/collections are Origin-gated
rather than authenticated. The current wording for this lives only in
`CLAUDE.md:33` and in a blockquote inside the skill
(`website-deploy/SKILL.md:203-208`). A user reading the website is never told.

---

## 4. The sharing gap

The user's framing: *"it doesn't have the sharing skill too."* The skill is the
visible part; the backend is the actual gap.

**Today every site has exactly one owner.** `sites.user_id` is a single FK
(`db/schema.sql:17`), and the ownership check is literally "fetch the row scoped
to this user" — `db.GetSiteByUser(...)`, which returns `sql.ErrNoRows` → 404 for
anyone else. There is no second person who can deploy, roll back, or even list a
site they didn't create.

`docs/designs/multi-user-editing.md` (dated 2026-06-25, status **Proposal**)
already specifies the fix: a `site_collaborators(site_id, user_id, role)` table
with `owner`/`editor`/`viewer`, invites reusing the existing magic-link flow,
`versions.created_by` for attribution, and optimistic `If-Match` concurrency.
The design is sound and matches what the internal instance actually shipped.

**Two corrections to that design before anyone implements it:**

1. **The effort estimate is stale.** The design says the ownership predicate
   appears at *five* call sites. It is now **ten**, because `domains.go` and
   `analytics.go` were added after the design was written:

   ```
   internal/handler/site.go:283, 331, 785, 1020, 1077, 1136, 1206
   internal/handler/domains.go:152, 210, 258
   internal/handler/analytics.go:23
   ```

   Each needs a `need` role decided: analytics and domain-read are plausibly
   `viewer`; domain bind/unbind should be `owner`, not `editor`, because a domain
   is a global namespace claim.

2. **The schema lives in `db/schema.sql` now**, not in `README.md` as the design
   states. There is still no migration framework, so this is a hand-applied
   `psql` step against the live database.

**Change 4.1 — implement the collaborator model** per that design, with the two
corrections above.

**Change 4.2 — add the sharing surface to the skill.** On the internal instance
this is `references/collaboration.md`, and one rule from it is load-bearing
enough to belong in the router `SKILL.md` rather than the reference:

> Resolve access *before* answering about an existing site. Never infer
> editability from the owner-scoped list endpoint — for a normal user it lists
> owned sites only and silently omits sites shared with them.

A shared site stays one owner resource, one URL, one version history, one stored
copy. The failure mode to design against is an agent "helpfully" creating a copy
under the editor's own account when it gets a 404.

**Sequencing.** 4.1 requires a DDL change on the production database. It cannot
ship in the same push as the simplification work, and it cannot ship at all from
a workstation with no database access. Treat §1–§3 and §4 as two separate
releases.

---

## 5. What must NOT be ported

The internal instance carries Sony-specific content that would be wrong or
nonsensical here:

- **The information-classification rule** (only *Internal Use* and *Public* may
  be hosted; never *Confidential*/*Secret*/*Top Secret*; no PII or customer
  data). This is SIE's five-tier scheme. simple-host.app is a public product with
  no such taxonomy. The *underlying* warning — "the store is public, never put
  secrets in it" — is already in the skill and should stay; the tier language
  should not come across.
- **The Slack support channel** `#nurture-ai-playground-support`. There is no
  equivalent; the public app's support route is the GitHub repo.
- **"Anyone on the Sony VPN who has the link"** — the public app's sites are
  reachable by anyone on the internet, which is a *stronger* warning, not the
  same one.
- **The internal hostname** and the `/sites/{user}/{site}/` path shape. This app
  uses a separate content host `sites.simple-host.app/<handle>/<sitename>/`.
- **`fix-paths-for-subpath-hosting`** as a separate skill — its subject matter is
  already inside `website-deploy`'s framework table here.

---

## 6. Defects found while surveying (fix regardless of scope)

| # | Defect | Evidence |
|---|---|---|
| D1 | `/skills/connect-domain.zip` and `/skills/connect-domain/SKILL.md` are 404 | routes absent at `internal/handler/ui.go:74-77`; confirmed 404 against live |
| D2 | Claude.ai, Copilot, and the pasteable-prompt install paths all silently install 2 of 3 skills | `install.html:245-246, 260, 272, 278-279` |
| D3 | `%USERPROFILE%` in a PowerShell context creates a literally-named folder | `install.html:272` |
| D4 | `~/.agents/skills/` is labelled "Codex CLI" | `install.html:255` |
| D5 | The shared-origin caveat for state/collections is documented for agents but never for humans | present at `website-deploy/SKILL.md:203-208`, absent from every HTML page |
| D6 | `scripts/check-docs-sync.sh` — the documented pre-deploy check — aborts on macOS | it uses `declare -A` (bash 4+); macOS ships bash 3.2, so with `set -u` it dies at line 45 with `state: unbound variable`, skips the whole capability-coverage section, and never prints its success line. Fixed in this branch by replacing the associative array with tab-separated lines, and widened to search a skill's whole directory so a split skill still passes. |
| D7 | `/.well-known/skills/index.json` hardcodes `"files": ["SKILL.md"]` | `internal/handler/skillshub.go:141` — wrong the moment any skill gains a second file |

---

## 7. Release plan

### Release A — simplification. DONE on this branch.

| Change | Status |
|---|---|
| §1.1 `website-deploy` split: 21,888 B monolith → 5,996 B router + 5 references (22,333 B total, **no content dropped** — verified fact-by-fact against the old file) | done |
| §1.3 references reachable by URL: new `GET /v1/skills/{name}/references/{file}` + `.well-known` mirror; `SKILL.md` cites every reference by relative path *and* absolute URL | done |
| §2.1 ChatGPT desktop treated as a skill-install target, not a paste-only chat | done |
| §2.2 `~/.agents/skills/` relabelled; Cursor listed explicitly | done |
| §2.3 `%USERPROFILE%` removed from the PowerShell instruction; the Windows column is now labelled PowerShell and uses `$env:USERPROFILE` | done |
| §2.4 / D1 per-skill download routes generated from the bundle, so `/skills/connect-domain.zip` and `.../SKILL.md` exist | done |
| D2 `connect-domain` added to the Claude.ai ZIP row, both Copilot blocks, and the pasteable prompt | done |
| §3.4 / D5 a "Before you publish" FAQ on `/install.html`, and a visibility line on the signed-out homepage | done |
| D6 `check-docs-sync.sh` fixed for bash 3.2 and widened to search a whole skill directory | done |
| D7 `/.well-known/skills/index.json` enumerates real files | done |
| `setup.sh` copies whole skill directories and installs **all three** skills (it previously copied one file of one skill) | done |
| Staleness notice only fires when the server is genuinely newer — an `npx` user ahead of the server is no longer told to update in a circle | done |
| Plugin `0.8.4 → 0.9.0` | done |
| Tests: `skillshub_test.go`, `notice_middleware_test.go` (the repo previously had no coverage here) | done |

Verified: `go build`, `go vet`, `go test ./... -count=1`,
`bash scripts/check-docs-sync.sh`, plus a local server smoke of every skill,
static, and API route, HTML well-formedness on the changed pages, and the
staleness matrix end to end.

### §3 — homepage: partially done, deliberately

The visibility/FAQ content shipped. The **four-step progressive-reveal journey
did not**, and that is a judgement call worth stating plainly rather than
quietly dropping:

`index.html` here is not a marketing page — it is the application (sign-in, AI
builder, site list, admin). There is no test coverage over it. Restructuring the
shell of a live product unattended, overnight, risks silently breaking sign-in
for everyone. The equivalent surface on this product is `/install.html`, which
*is* a dedicated onboarding page and which this branch simplified.

If the journey is still wanted on the apex page, it should be built with someone
watching it.

### Release B — sharing. Not started.

§4 needs `site_collaborators` + `versions.created_by` DDL applied by hand to the
live Postgres (no migration framework), then the binary, then the skill
reference. Both DDL statements are additive and inert to the running binary, so
the schema can go first safely. This cannot be done from a workstation with no
database access.

### Deploying

The repo has no CI and no deploy script; the live box is nginx-fronted and no
credentials for it exist on this workstation. Before these changes `origin/main`
and live were byte-identical, which is consistent with the server tracking
`main` — but that is inference, not proof.

Pushing `main` is therefore the deploy action available here. Confirm it landed
by watching `GET /skills/version` change from `0.8.4` to `0.9.0`. If it does not
change, the server needs a manual pull-and-restart on the box.
