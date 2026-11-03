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

- `lib.mjs` — the `Namer` and the normalizers (`normalizeText`, `normalizeValue`),
  shared by `run.mjs` and `diff.mjs`.
- `run.mjs` — the harness: `node run.mjs BINARY OUT.json`.
- `diff.mjs` — the comparator: `node diff.mjs A.json B.json`.
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
with the exact path inside the body that changed, and exits 1. Any
difference here blocks the deploy it was checking — do not re-run against a
tweaked baseline to make it pass; either the candidate has a real bug, or
the baseline needs a *documented* re-record (see "What is normalized"
below for what is allowed to differ and why).

Requires a reachable disposable Postgres server: `127.0.0.1:55412`,
user/password `postgres`/`postgres` (the `cl12-pg` container), overridable
with `GOLDEN_PG_URL=postgres://user:pass@host:port` (no path or query string
— `run.mjs` owns the database name). It is never the box's shared Postgres
on `:5432`, and nothing it creates survives the run: each instance gets a
`CREATE DATABASE`-cloned-from-nothing throwaway, dropped in a `finally` that
also kills the server process and removes its temp `DATA_DIR`, so a crash
mid-run still cleans up.

Runtime: ~6-7 seconds for both instances together (112 requests total, well
under the ~2 minute target) — most of it is the two `go` binaries' own
startup, not the HTTP calls.

## What is normalized, and why

Two runs of the *same* binary must produce byte-identical files; a real
behaviour change between two *different* binaries must not be hidden. Every
value below is either run-specific (never part of the behaviour being
checked) or genuinely random (an id, a key, a token, a short join/judge code)
rather than a decision the server made:

- **ids, keys, tokens, short codes**: pattern-matched (UUIDs, `shk_`-prefixed
  API keys, bare 32-64 hex tokens) or, for the hack join/judge/organiser-invite
  codes (which don't match any of those shapes, and uniquely also appear in a
  request *path*, not just a body field — `registerSecret` in `run.mjs`),
  registered by exact value as soon as they come back from the server. Each
  distinct raw value maps to `<category:N>` in order of first appearance
  (`<id:1>`, `<key:2>`, `<code:3>`, ...) via `lib.mjs`'s `Namer`, and the same
  raw value always gets the same placeholder everywhere it shows up again.
- **timestamps**: any ISO-8601 datetime → `<ts>`.
- **the chosen port and the temp `DATA_DIR`**: each instance's own
  `PUBLIC_BASE_URL` (which embeds its ephemeral port) and `DATA_DIR` path are
  registered as exact-replacement placeholders (`<base:N>`, `<tmpdir:N>`) the
  moment the instance boots, before any request — same mechanism as the codes
  above, just seeded by `run.mjs` itself instead of discovered from a response.
- **the build commit** (`GET /v1/admin/usage`'s `"commit"` field): expected to
  genuinely differ between the baseline and candidate binaries (different
  commits) and is irreducible for that reason — key-based override to
  `<volatile>` in `lib.mjs`, regardless of its value's shape.
- **box-wide disk and network counters** (`disk_bytes`, `disk_free_bytes`,
  `disk_used_bytes`, `disk_used_pct`, `network`, all under `GET
  /v1/admin/usage`): these describe the shared box's real, currently-changing
  state, not anything the harness seeded — also irreducible, same key-based
  override.
- **`GET /v1/admin/usage`'s `"message"` and `"status"`**: prose and a
  threshold derived from those same live disk numbers ("This server is 80%
  full, with N GB left"), so they inherit the same irreducibility — but
  "message" and "status" are ordinary field names used elsewhere for things
  that are *not* volatile, so this override is scoped to that one request by
  name (`EXTRA_VOLATILE_FIELDS` in `run.mjs`), not applied globally by key.
- JSON object keys are sorted recursively, so insertion-order differences
  (map iteration order, struct field order) never show up as a diff.
- An HTML/text body (anything not `application/json`) is kept verbatim, after
  the same normalization, when it is at or under 2000 bytes; above that, only
  its sha256 and length travel, to keep `OUT.json` and its diffs readable.

Nothing else is touched: a real status code change, a field that moved, a
header that disappeared, or a body byte that differs for any other reason
shows up in the diff.

## Adding a request

Add one `await at('name', METHOD, path, { apiKey, body })` line inside
`seedAndCallHost` or `seedAndCallHack` in `run.mjs`, in the right place in
the sequence (most routes need the event/site/account state built by the
calls before them). `name` must be unique within its instance (it becomes
`host:name` or `hack:name` in the output, and `diff.mjs` matches requests by
that full name, not by position, so reordering existing calls never produces
a spurious diff). If the response hands back a value later calls need
(an id, a slug, a code), read it off `.json` the same way the existing calls
do; if that value is itself random and will appear in a later request's
*path* (not just its body), call `registerSecret(exact, namer, 'code', value)`
right after reading it, the same way the hack join/judge/invite codes do —
otherwise the determinism check (replaying a pure GET is safe without this;
anything that mints a new random path segment is not) will fail on it.
