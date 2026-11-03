# Golden API snapshot harness

Deterministic proof that a rebuilt `simple-host`/`simple-hack` binary answers
the same way the last known-good one did. Boots the binary twice (a Host
instance, then a Hack instance, `EVENTS=hosted`), seeds each through its own
API, replays a fixed list of REST and MCP requests, and writes every
`{name, method, path, status, headers, body}` to a JSON file with run-specific
noise normalized to stable placeholders. Node 20+ built-ins only (global
`fetch`, `node:child_process`, ...) — the only external programs it shells
out to are `psql` (two throwaway databases) and the binary itself.

Files:

- `lib.mjs` — the `Namer` and the normalizers (`normalizeText`, `normalizeValue`,
  `normalizeSetCookie`), used by `run.mjs` while it captures each request.
- `run.mjs` — the harness: `node run.mjs BINARY OUT.json`.
- `diff.mjs` — the comparator (self-contained, no shared imports):
  `node diff.mjs A.json B.json`.
- `baseline.json` — recorded from commit `34f68ed2` (the last commit before
  step 3.1), the last point before the Host/Hack edition split began.

## Recording a baseline

Build the binary from a clean checkout at the commit you want (never from a
dirty working tree — the build command below needs a real git commit so
`buildinfo.Commit` is meaningful, though it gets normalized away anyway):

```
sudo systemd-run --quiet --wait --pipe --collect -p MemoryMax=2G -p WorkingDirectory=<tree> --uid=$(id -u) --gid=$(id -g) -E HOME=$HOME -E PATH=$PATH -E GOCACHE=$(go env GOCACHE) -E GOMODCACHE=$(go env GOMODCACHE) bash -c 'CGO_ENABLED=0 go build -o /tmp/golden-<sha>.bin ./cmd/server'
```

Then:

```
node scripts/golden/run.mjs /tmp/golden-<sha>.bin scripts/golden/baseline.json
```

`<tree>` can be any worktree at that commit; `db/schema.sql` is read from
*this* checkout by default (override with `GOLDEN_SCHEMA_SQL=<tree>/db/schema.sql`
if you are recording a commit whose schema differs from HEAD's — check first
with `git diff <sha> -- db/schema.sql`; if it's empty, there's no need).

## Replaying and diffing

Build the binary you want to check the same way, then:

```
node scripts/golden/run.mjs /tmp/golden-candidate.bin /tmp/candidate.json
node scripts/golden/diff.mjs scripts/golden/baseline.json /tmp/candidate.json
```

`diff.mjs` exits 0 and prints `ok: N requests, identical` when nothing
differs; otherwise it prints every differing request (not just the first)
with the exact path inside the body that changed, and exits 1. It checks,
in order: duplicate request names within one file, the replay *sequence*
itself (same names in the same order — the first point where it diverges is
reported; a request dropped, added, or moved is a real change even when
every individual request's own content still matches something on the other
side), then every request present on either side, diffed field by field all
the way down (there is no shortcut comparison that skips nested content).
Any difference here blocks the deploy it was checking — do not re-run
against a tweaked baseline to make it pass; either the candidate has a real
bug, or the baseline needs a *documented* re-record (see "What is
normalized" below for what is allowed to differ and why).

Requires a reachable disposable Postgres server: `127.0.0.1:55412`,
user/password `postgres`/`postgres` (the `cl12-pg` container), overridable
with `GOLDEN_PG_URL=postgres://user:pass@host:port` (no path or query string
— `run.mjs` owns the database name). It is never the box's shared Postgres
on `:5432`, and nothing it creates survives the run: each instance gets a
`CREATE DATABASE`-cloned-from-nothing throwaway, dropped in a `finally` that
also kills the server process and removes its temp `DATA_DIR`, so a normal
exit (success or a thrown error) always cleans up. A `SIGINT`/`SIGTERM`
mid-run (Ctrl-C, a timeout, `systemctl stop`) bypasses that `finally` —
Node does not run a suspended async function's pending `finally` blocks when
the process is killed by a signal, it just exits — so `run.mjs` separately
registers each resource's teardown the moment it exists (`addCleanup`) and
its own signal handlers run every one of them, most-recently-created first,
before exiting 1; a boot failure before a resource like `DATA_DIR` even
exists is handled by its own ordinary `catch`, not this path.

Runtime: ~6-7 seconds for both instances together (112 requests total, well
under the ~2 minute target) — most of it is the two `go` binaries' own
startup, not the HTTP calls.

## What is normalized, and why

Two runs of the *same* binary must produce byte-identical files; a real
behaviour change between two *different* binaries must not be hidden. Every
value below is either run-specific (never part of the behaviour being
checked) or genuinely random (an id, a key, a token, a short join/judge code,
a per-response security nonce) rather than a decision the server made. As a
rule, a normalization rule is scoped as narrowly as the thing it is actually
fixing — by exact value, by request name, or by a field name with no other
meaning anywhere in the codebase — never a blanket "any field called X"
unless that name genuinely has only one meaning everywhere (checked by hand,
and said so in a comment where it matters):

- **ids, keys, tokens, short codes**: pattern-matched (UUIDs, `shk_`-prefixed
  API keys, bare 32-64 hex tokens, a hack-mode account's `u-<10 hex>` handle,
  a version-preview link's signed token right after `/__preview/<n>/`) or,
  for the hack join/judge/organiser-invite/team codes (which don't match any
  of those shapes, and uniquely also appear in a request *path*, not just a
  body field — `registerSecret` in `run.mjs`), registered by exact value as
  soon as they come back from the server. Each distinct raw value maps to
  `<category:N>` in order of first appearance (`<id:1>`, `<key:2>`,
  `<code:3>`, ...) via `lib.mjs`'s `Namer`, and the same raw value always
  gets the same placeholder everywhere it shows up again.
- **a per-response security nonce** (the 16-byte base64 CSP nonce `uiCSP`
  stamps onto every admin/landing page's inline `<script>` tags, fresh on
  every response on purpose): `nonce="..."` → `nonce="<nonce>"`, a fixed
  placeholder rather than an indexed one — it is never meant to repeat, even
  within one run, so indexing it would add noise, not information.
- **timestamps**: any ISO-8601 datetime → `<ts>`, *except* one that exactly
  matches a value the seed itself supplied in a request body (the hack
  event's `starts_at`/`ends_at`, given as absolute dates in `run.mjs` and
  echoed back verbatim by `rfc3339UTC` in `internal/handler/hack.go`) — that
  is seeded input, not server-generated noise, so a real regression in date
  handling should still show up in a diff instead of being normalized away
  with everything the server actually generated (`created_at`, `deleted_at`,
  a measurement timestamp, ...). `knownSeedTimestamps()` in `run.mjs` is the
  one place that list lives; add to it there if a future request seeds
  another absolute date/time.
- **the chosen port, its bare `host:port` form, and the temp `DATA_DIR`**:
  each instance's own `PUBLIC_BASE_URL` (which embeds its ephemeral port),
  that same `127.0.0.1:<port>` without the scheme (the hack landing page's
  canvas illustration writes it into an inline script as example text,
  `yourhack.127.0.0.1:<port>`, with no `http://` in front to catch it), and
  `DATA_DIR` are registered as exact-replacement placeholders (`<base:N>`,
  `<hostport:N>`, `<tmpdir:N>`) the moment the instance boots, before any
  request — same mechanism as the codes above, just seeded by `run.mjs`
  itself instead of discovered from a response.
- **`last4` and `passcode`, wherever they appear**: the last 4 characters of
  a freshly minted API/deploy key, and a visitor-lock passcode just
  generated — the two deliberately-narrow exceptions to "never a blanket
  field-name rule" above, because both names are used nowhere in this
  codebase for anything else (checked by hand; see
  `ALWAYS_VOLATILE_FIELD_NAMES` in `lib.mjs` for the full reasoning and the
  one near-miss it's not: `hack_entries.go`'s unrelated string *value*
  `"passcode"` under the key `not_frameable_why`).
- **`GET /v1/admin/usage`'s `"commit"`, `"network"`, the `disk_*` fields,
  `"message"` and `"status"`**: the build commit is expected to genuinely
  differ between the baseline and candidate binaries (different commits) on
  purpose; the disk/network figures describe the shared box's real,
  currently-changing state, not anything the harness seeded; "message" and
  "status" are prose and a threshold derived from those same live numbers.
  All five are irreducible for that reason, but "message"/"status"/"commit"/
  "network" are also ordinary field names used elsewhere in the API for
  things that are *not* volatile — so the override is scoped to that one
  request by name (`EXTRA_VOLATILE_FIELDS` in `run.mjs`), never applied
  globally by key.
- **row order in `GET .../judging/dashboard`'s `teams`/`judges` arrays**:
  the server's own query orders each by its row's random id
  (`internal/db/hack_judging.go`), a fine, stable order for one run but not
  the same from one run to the next. `SORT_ARRAYS_BY` in `run.mjs`
  re-sorts both arrays by each row's seeded name before anything else looks
  at them — a harness-side fix for a harness-side comparison problem; the
  server's own response and its actual order are untouched.
- **`GET /v1/me/export.zip`'s body**: a zip whose entries carry their own
  timestamps and whose content includes a freshly minted API key in full —
  there is no reasonable way to normalize zip bytes short of unzipping, so
  `OPAQUE_BINARY_BODIES` in `run.mjs` replaces the body with a short note
  instead of a hash that would never match; status and content-type are
  still captured and compared.
- JSON object keys are sorted recursively, so insertion-order differences
  (map iteration order, struct field order) never show up as a diff.
- An HTML/text body (anything not `application/json`) is kept verbatim, after
  the same normalization, when it is at or under 2000 bytes; above that, only
  its sha256 and length travel, to keep `OUT.json` and its diffs readable.
- **headers**: `content-type`, `cache-control`, and a normalized `location`
  are kept as before; an `ETag`'s *presence* is recorded as `<etag>` (its
  actual value is a content hash expected to differ whenever the real
  content behind it does, e.g. a static asset — only whether the server
  sends one at all is meaningful here); `Set-Cookie` is recorded as a sorted
  list, one entry per cookie, each cookie's own value replaced with
  `<value>` but its name and attributes (`Path`, `HttpOnly`, `SameSite`, an
  `Expires` date, ...) kept (and still normalized, so e.g. an `Expires`
  timestamp still becomes `<ts>`).

Nothing else is touched: a real status code change, a field that moved, a
header that disappeared or changed, a request dropped from or reordered in
the sequence, a duplicate request name, or a body byte that differs for any
other reason shows up in the diff.

## Adding a request

Add one `await at('name', METHOD, path, { apiKey, body })` line inside
`seedAndCallHost` or `seedAndCallHack` in `run.mjs`, in the right place in
the sequence (most routes need the event/site/account state built by the
calls before them). `name` must be unique within its instance (it becomes
`host:name` or `hack:name` in the output; `at()` throws at record time if
you reuse one by accident). `diff.mjs` matches requests by that full name
for the per-request content comparison, but it *also* checks the replay
sequence itself (same names in the same order) — so moving an existing call
earlier or later in the list is a real, visible change to what the harness
checks, same as adding or removing one; it is not free to reorder calls just
to tidy up the file. If the response hands back a value later calls need
(an id, a slug, a code), read it off `.json` the same way the existing calls
do; if that value is itself random and will appear in a later request's
*path* (not just its body), call `registerSecret(exact, namer, 'code', value)`
right after reading it, the same way the hack join/judge/invite/team codes do
— otherwise the determinism check (replaying a pure GET is safe without
this; anything that mints a new random path segment is not) will fail on it.
