# Design principles

These are the owner's rules for how this code is built. They come first: when a change, a
review finding or an instruction conflicts with one of them, say which principle and stop
to ask. `INTENT.md` says what the product is for; this file says how the code must be shaped.

Each principle has: the rule, what it forbids, how it is checked, and where it came from.
"Owner" lines are his words. "Proposed by Claude" marks a rule the owner did not state
himself; it stands only while he accepts it.
The last section lists the places where two principles pull against each other and which one
wins, so the same argument is not had twice.

## The principles

### 1. One owner per entity

A person lives in Access. A site lives in Sites. Saved data lives in Data. In Simple Hack an
event, a member, a team and an entry each have one owning package. Anything that needs to
know about an entity asks its owner through a small set of plain functions.

- Forbids: reading or writing another module's table; joining across modules for display.
- Checked by: `sqlcheck -owners` (in `make ready`), with `scripts/split/table-owners.txt`.
- Owner, 2026-11-21: "User is the number one entity of our platform. Anything which tries to
  get user information should be in one location."

### 2. One copy of each fact

A fact is stored once. Everything else keeps the id and looks it up. A person's name and
email live on the account and nowhere else, so deleting the account is the whole of removing
their identity.

- Forbids: copying a name, email, handle or address into another table.
- Checked by: review. Known copies still to remove are listed in the last section.
- Owner, 2026-11-21: "You can just delete everything, let the account ID stay."

### 3. One owner per rule

Each decision is made by one function that every route, tool and product calls: who may act,
who may see, is it live, is the name valid, what is the limit, which status an error maps to.

- Forbids: a second copy of a check, even a "quick" one in a route or a tool.
- Checked by: review; the visitor gate, site state and error mapping each have one function.
- Owner, 2026-11-18: "One owner per rule. No duplicated logic."

### 4. Thin edges

A web route reads the request, calls one function, writes the answer. An AI tool declares
itself and makes one call to the same route the dashboard uses. Real web work stays at the
edge: cookies, redirects, streaming, choosing which page to show.

- Forbids: decisions, loops over business state, or database code in a route or a tool.
- Checked by: the thin-edge check in `scripts/check-layering.sh`.
- Owner, 2026-11-20: "Basically reading the request, call one function, answer."

### 5. One-way structure

Access knows nothing of Sites or Data. Sites may use Access. Data may use both. A job that
spans modules, such as deleting a person, lives in the one layer above them (`host/ops`).
Simple Hack sits on top and uses the platform only through module functions. A routes
package never imports another routes package.

- Forbids: circular imports; hooks installed at start-up so a lower module can call a higher one.
- Checked by: `scripts/check-layering.sh`.
- Proposed by Claude (the module order); the owner approved the module split on 2026-11-19
  and asked for "separation of concerns as modules inside one program".

### 6. No machinery

Plain functions only. Leave out interfaces that exist just to break an import cycle, bags of
callbacks, wrappers around wrappers, option structs for one caller, and frameworks.
The few real extension points are where Simple Hack plugs into the platform.

- Forbids: adding a layer to solve a problem that moving a function would solve.
- Checked by: the func-field check (`scripts/layering/funcfields.txt`).
- Owner, 2026-11-19: "This is how we create 500,000 lines of code."

### 7. Find the simple model first

Before building a fix, ask where the fact or rule lives. If it lives in one place, the fix
is usually to change it there, and the problem disappears. A plan with more than three steps
for a simple point is a sign of over-complication.

- Forbids: answering a simple design point with an audit, a proof, or a new subsystem.
- Worded by Claude from the owner's correction, 2026-11-21: "What is there to prove in that?
  Isn't that simple? ... there should be one place for accounts, not a hundred."

### 8. Rare things may fail

A rare race or edge case may fail cleanly and be retried. Only four things justify extra
code: someone could see or change what they should not, data could be lost, an operator hold
could be bypassed, or an everyday action would break.

- Forbids: caps, guards, retries and lock choreography for things that almost never happen.
- Owner, 2026-11-19: "If it fails, it fails. Retry is always fine."

### 9. Simple and short

Both, always. Short functions with one job, readable top to bottom, and as little code as
does the job. Clever is welcome when it makes the code smaller and what it does is still
plain on the page: one table for twenty branches, one general function for five special
ones. The clever to avoid is the hidden kind, where the code only works because of something
a reader cannot see from where they stand. Only when simple and short truly conflict does
simple win, and then the test is which version is easier to verify.

- Forbids: functions over complexity 25; cutting one operation into many tiny pieces that
  only make sense together; carrier structs that only pass state between fragments; code
  made longer in the name of clarity.
- Checked by: `gocyclo -over 25`; line counts reported before and after a change.
- Owner, 2026-11-20 and 2026-11-22: "I want it to be simple and short. I never want it to be
  long." When one has to give: "simpler but longer is better." Clever is good.
- Why it matters for AI readers (Claude's note): what a model can hold in view is the scarce
  resource, so short helps more than it does for people, and a surprise is what makes a small
  model wrong.

### 10. A change retests only its own part

Each module's tests run without building the other modules. The full run is for installs.

- Checked by: `scripts/test-integration.sh` (cached by default, `--full` to force everything)
  and the rerun table in the notes.
- Owner, 2026-11-20: "When we add features, we should not need to test everything."

### 11. A small model can make the change

One obvious pattern per kind of change, so a new route, tool or field is made by copying its
neighbour. The instructions in `CLAUDE.md` and each package's `doc.go` are true. The wrong
thing fails loudly with a message that says what to do. `make ready` runs every pre-commit step.

- Owner, 2026-11-20: "It should be manageable for small AI to go in and add changes."

### 12. Tests prove features and guard the three things

Tests exist to show a feature works and to guard access, data and operator holds, each once,
at the layer that owns the rule. No tests of wording, internals, or rare races.

- Owner, 2026-11-20: "Having so many tests, I still don't get it."

### 13. Safe by default

Something new is private until someone says otherwise. A new text field stays out of
analytics until it is listed as safe, a platform tool stays out of Simple Hack until it is
added there, and saved data is owner-only until rules open it.

- Proposed by Claude after a small-model trial leaked a new private field to analytics.
  Accepted by the owner, 2026-11-22: "fine if that's the right way to go."

### 14. A refactor is judged by numbers

Before a clean-up, name the numbers that must move: outside reads per table, tangled
functions, retest time, handlers reaching into each other. Report them before and after.
If they did not move, say "nothing real changed". Moving files is not refactoring.

- Worded by Claude from the owner's complaint, 2026-11-20: "You just broke down the files,
  keeping the core as is."

### 15. Delete what is not needed

Remove compatibility code for things nobody uses, dead code, and any second way of doing the same thing.
Documentation, skills and generated files are brought up to date in one pass, not kept in
step change by change.

- Owner, 2026-11-19: "Delete everything which is not necessary."

## Where two principles pull against each other

These are decided. Do not reopen them without the owner.

| Tension | Decision |
|---|---|
| One owner per table (1) against one trip to the database on a busy path | The busy read stays one statement. It goes in one named file of the owner that needs it (`host/sites/repo/with_owner.go` reads a site with its owner), listed as an exception. Nowhere else. |
| One owner per table (1) against a safety check that must be atomic | A safety check and the action it guards stay in one statement, or one locked transaction. Never read, release, then act. |
| One-way structure (5) against a job that needs several modules | The job moves up to `host/ops`, in place of a hook or a reversed import. |
| No machinery (6) against a real plug-in point | Simple Hack's extension points stay: schema, assets, edition, storage provider, group hosts and gate, the erasure guard. Nothing else. |
| Thin edges (4) against not changing what users see | A route keeps a check at the edge when moving it would change which error a person gets first. Web work (cookies, redirects, page choice) is not business logic. |
| Simple against short (both in 9) | Want both. Remove dead and duplicate code always. When one must give, keep the version that is easier to verify. |
| Fewer tests (12) against safety | Every rule about access, data and operator holds keeps one test of the refusal and one of the allowed case. Everything else may go. |
| Rare things may fail (8) against operator holds and privacy | A race that could serve a held site or show private data is fixed. A race that only fails cleanly is left. |
| Operator reports against one owner per table (1) | Reports and metrics may read across tables. Each is a named exception. They never write. |

## Known gaps

Kept here so nobody mistakes them for the design.

- Stored copies of identity still present: pending email changes, named-viewer emails, team
  invitation addresses, sign-in codes, linked-provider emails, version author text, and in
  Simple Hack the organiser's public contact name and email. Some are the fact itself (an
  invitation is addressed to an email). The rest should become lookups.
- A site's address is stored beside its name.
- Many Access and Sites routes still call more than one function.
- The domain-check workers live in a routes package. They belong in `host/ops`.

## How to use this file

- Before a change: check it against the principles. If it conflicts with one, say which and ask.
- When the owner rules on something new: add it here with the date and his words.
- When two principles conflict in a new way: add a row to the table, do not improvise.
- A review of the code is a review against this file.
