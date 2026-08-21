# Scaling the state document: 1 MB → 100 MB

| | |
|---|---|
| Status | Draft for discussion |
| Date | 2026-08-15 |
| Scope | How a site's JSON state is **stored**. Not the API shape, not access control, not realtime. |
| Goal | Keep one document per site, addressable by path, surgically updatable — and let a few sites hold 10–100 MB without the platform falling over. |

## 1. Where we are

State is one `jsonb` column plus an integer version:

```
sites.state          jsonb
sites.state_version  integer
```

`PATCH` applies ops (`set`, `inc`, `append`, `remove`), each carrying a `path`. Optimistic
concurrency already works: responses carry an `ETag` derived from `state_version`, and
`If-Match` drives a compare-and-swap (`UpdateSiteStateCAS`).

**The API is already path-shaped. Only the storage isn't.** That is the whole problem
this document addresses.

Production today, for calibration:

```
sites with state          10 of 55
largest state document    704 bytes
total state, platform-wide ~1.5 KB
cap                       1 MB
```

Nothing is remotely near the cap. This design is therefore not urgent — it is about
whether the *shape* can grow when something eventually needs it, and about not painting
ourselves into a corner in the meantime.

## 2. Why one jsonb column cannot become 100 MB

Not a policy limit — a mechanical one. Four things break, all of them quietly.

**Every write rewrites the whole document.** Postgres has no partial update of a `jsonb`
value. `jsonb_set` reads the datum, builds a new one, and writes it back. A single `inc`
on a counter inside a 10 MB document is a 10 MB read-modify-write.

**TOAST amplifies it.** Any value over ~2 KB is compressed and stored out-of-line in a
TOAST chain. Updating it rewrites the entire chain, not a delta. So the cost is
proportional to document size on *every* operation, however small.

**MVCC turns writes into garbage.** Each update writes a new row version and leaves the
old one for vacuum. A 10 MB document updated 100×/day generates roughly a gigabyte of
dead tuples per day, on a box with 11 GB of RAM and one Postgres.

**WAL amplification.** Those rewrites are all logged. Write throughput collapses long
before the 1 GB hard `jsonb` ceiling — practical trouble starts in the low tens of MB
with any write frequency at all.

Reads are no better: to read one counter, Postgres detoasts and decompresses the whole
value.

**Conclusion:** a single column can hold a large document *or* accept frequent surgical
writes. Not both.

## 3. What the others do, and the lesson

| System | Unit | Max | Partial update? |
|---|---|---|---|
| **Firestore** | document | **1 MiB** | Field-level, but the document stays small |
| **Firebase RTDB** | JSON tree | 10 MB per string; **256 MB per read location** | Yes — path-addressed |
| **Cloudflare Workers KV** | value | 25 MiB | **No** — whole-value writes, eventually consistent |
| **Durable Objects (SQLite)** | object | 10 GB | Yes — many small keys inside |

Two things stand out.

**Firestore's document limit is 1 MiB — identical to ours.** Google did not raise it. Their
documented guidance is to split into subcollections instead. That is a considered
position, not an oversight: a document is a unit of read, write, and contention, and
past about a megabyte it stops being a good unit for any of the three.

**Nobody offers "large document plus frequent surgical writes" as one primitive.** KV
gives you 25 MB but no partial update. Durable Objects give you 10 GB but only as many
small keys. RTDB gives you a big tree but caps what a single read can pull. Every one of
them shards underneath; they differ only in whether the sharding is visible.

**So the lesson is not "raise the limit."** It is: keep the *external* model one
path-addressed document, and shard *internally* so no single operation is proportional to
total size. RTDB is the closest model — one tree to the user, chunked underneath.

## 4. Design: one document, three storage tiers

The document a site sees never changes. `GET /state`, `GET /state/a/b`, `PATCH` with path
ops — same API, same mental model, arbitrarily extensible. What changes is the
representation underneath, chosen automatically by size. **No user ever picks a tier.**

### Tier A — inline `jsonb` (0 – 256 KB)

Exactly what exists today. One column, fully transactional, single-round-trip reads.
Every current site stays here forever; the largest is 704 bytes. No migration, no new code
path for the common case.

This tier is the reason the design is cheap: it changes nothing for ~100% of live traffic.

### Tier B — segmented, content-addressed files (256 KB – 100 MB)

Above the inline threshold the document is stored as **segments**: subtrees serialised to
individual files, with a small manifest kept in Postgres.

**Segmenting rule.** Split at depth 1 (top-level keys). If a segment exceeds the segment
cap (256 KB), split it again at its next depth, recursively. A segment is identified by
its path prefix, so `state/orders/2026-08` is a segment covering everything under that
path.

**Files are content-addressed and write-once:**

```
<DATA_DIR>/<site-id>/state/blobs/<sha256>.json
```

A segment file is never modified or overwritten. Writing a new version writes a new blob.

**The manifest is the only mutable thing**, and it lives in Postgres next to the version
we already keep:

```
path prefix        →  { blob hash, bytes, version }
"orders/2026-08"   →  { "a3f9…", 184320, 12 }
"votes"            →  { "7c1e…",    412,  3 }
```

The manifest for a 100 MB document is a few kilobytes — small enough to stay inline,
transactional, and cheap to read on every request.

**Why content-addressed matters.** It makes the two-store problem go away. The failure
mode of "files on disk plus metadata in a database" is normally that a crash between the
two leaves them disagreeing. Here, blobs are immutable and the manifest commit is the
single atomic act:

1. Serialise the changed segment, write to a temp file, `fsync`, rename to its hash name.
   Renaming to a content-addressed name can never destroy anything — worst case it
   rewrites an identical file.
2. Commit the manifest change in Postgres.

A crash before step 2 leaves an unreferenced blob, which a sweep collects later. A crash
after leaves nothing inconsistent. There is no window where a reader sees a torn
document. It also gives deduplication and cheap snapshots for free — a manifest *is* a
point-in-time snapshot, and retaining old manifests costs only the blobs they reference.

**Surgical write, concretely.** `PATCH` with op path `orders/2026-08/1731`:

1. Read the manifest, find the covering segment (`orders/2026-08`, 184 KB)
2. Read that one blob, apply the op
3. Write the new blob, commit the manifest with the new hash and bumped versions

Cost is proportional to **the segment**, not the document. Updating one order in a 100 MB
document touches 184 KB. That is the whole point of the tier.

**Concurrency.** Each segment carries its own version, so writes to different segments
never conflict. The document version (what `ETag`/`If-Match` already expose) becomes the
max of segment versions, preserving today's semantics exactly. Two people appending to
different collections in the same document stop contending — which they do today, because
they share one `state_version`.

### Tier C — cold segments (optional, later)

Segments untouched for a long time could be gzipped at rest, or moved off the hot path.
Listed for completeness; not proposed now. Nothing in Tier B forecloses it.

## 5. Reads must be bounded

The dangerous operation is not the write, it is `GET /state` on a large document. Follow
RTDB's instinct and cap what one read can pull.

- Below 1 MB: `GET /state` returns the whole document, as today.
- Above 1 MB: `GET /state` returns a **manifest view** — top-level keys with sizes and
  versions — not the body. To get data you name a path.
- `GET /state/<path>` always works and reads only the covering segments.

This is not a restriction so much as making the existing reality legible: nobody
usefully fetches 100 MB of JSON into a browser page. It also delivers the key-listing
capability Cloudflare KV has and we lack, without designing a separate feature.

## 6. What actually changes

| Surface | Change |
|---|---|
| `PATCH /state` with path ops | None. Same request, same response. |
| `GET /state` | None below 1 MB; returns a manifest above it. |
| `GET /state/<path>` | **New.** Reads one subtree. Additive; nothing breaks without it. |
| `ETag` / `If-Match` | None. Document version keeps its current meaning. |
| Ops (`set`/`inc`/`append`/`remove`) | None. |
| Quota | New per-site `state_quota_bytes`, default 1 MB. Raised per site by an operator — this is how "a few sites get 100 MB" happens without changing anyone else. |

Promotion between tiers is automatic and invisible: a write that pushes a document past
256 KB segments it. Demotion on shrink is possible but not worth building until something
shrinks.

## 7. Costs, honestly

**Backups get a second half.** Today `pg_dump` captures all state. With Tier B, large
documents live on disk and a database dump is no longer sufficient. The data directory
must be backed up too. Content-addressed blobs make that unusually easy — files are
immutable, so incremental backup is "copy the new hashes" — but the runbook changes, and
a backup that silently stops capturing state is exactly the sort of failure that is only
discovered when it matters.

**A garbage collector is now required.** Unreferenced blobs accumulate from crashes,
overwrites, and deletions. It is a simple sweep — walk the manifests, delete blobs nobody
references, with a grace period so an in-flight write is never collected — but it is a
background job that did not exist, and a buggy one deletes live data.

**Two storage systems instead of one.** Content-addressing makes them safe to combine,
but "one binary, one Postgres, one folder on disk" becomes meaningfully more complex.
That simplicity is a real asset of this project and this is the first thing to spend it on.

**Segmentation heuristics can be wrong.** Splitting at depth 1 works when top-level keys
carry comparable weight. A document with one giant top-level key gets one giant segment
and no benefit until the recursive split kicks in. The rule is simple and predictable,
which matters more than optimal — but it will occasionally be a poor fit.

**More code on the hot path.** Every state read gains a manifest lookup and a tier check.
Tier A keeps that to a branch on a column that is already being fetched, but it is not free.

## 8. What this deliberately does not do

- **No separate table exposed to users.** A site has one document. Segments, blobs and
  manifests are invisible; nobody names a key or picks a shard.
- **No query engine.** No indexes, no `where`, no sorting. This is a document store that
  addresses by path, and nothing more.
- **No new consistency model.** Postgres remains the source of truth and the arbiter of
  every version. No eventual consistency, no cache invalidation.
- **No change to collections.** Append-heavy lists already have the right home; this is
  only about the document.

## 9. Recommendation

Build **Tier A + Tier B**, in that order, and only when something actually needs it.

The strongest argument for the design is that Tier A is the status quo, so the whole
scheme costs nothing until a document crosses 256 KB — and today the largest is 704 bytes.
It can be built once there is a real user of it, and the API commitments made now (paths
already carry ops; `ETag` already exists) keep that door open at no cost.

The strongest argument against building it soon is Firestore: the company with the most
resources in this space looked at the same problem and kept documents at 1 MiB. If a site
here ever wants 100 MB of structured state, the honest first question is whether it wants
a *document* at all, or whether it wants a collection — which already scales, already
paginates, and already stores one row per item.

## 10. The tree model, and what it costs to add

RTDB's real idea is not "big JSON" — it is that **every node has an address**. You never
fetch the tree; you fetch `/a/b/c`. Our ops are already path-carrying (`splitPath` in
`internal/handler/stateops.go:273` splits on `.`), so half the model exists. What is
missing is that a path is not yet a *URL*.

### The surprise: the tree is cheap **today**, with no segmentation at all

Postgres has native `jsonb` path operators, and `splitPath` already returns exactly the
`text[]` they take:

| Operation | SQL | Notes |
|---|---|---|
| Read a subtree | `state #> '{a,b}'` | Extracts without parsing in Go |
| Replace a subtree | `jsonb_set(state, '{a,b}', $val, true)` | Creates the final key |
| Delete a subtree | `state #- '{a,b}'` | |

So `GET /state/a/b`, `PUT /state/a/b`, `DELETE /state/a/b` can ship against the **current
single-column storage**, with no manifest, no blobs, no tiers. The tree and the scaling
work are independent, and the tree is by far the cheaper half.

It is also the half that carries most of the user-visible benefit: partial reads, key
listing, and a mental model that scales conceptually even while storage does not.

### What actually has to be decided

Small in code, but each of these is a decision that is painful to change later.

1. **Separator.** Ops use `.`; URLs use `/`. Canonical form should be URL segments, with
   the dotted form kept for ops. Consequence: a key containing `/` becomes unaddressable
   by URL (keys containing `.` already are). Either percent-encode or document the
   restriction — but decide, because sites will start relying on whichever we ship.
2. **Missing paths.** `GET /state/nope` → `null` with 200, not 404. RTDB returns null, and
   generated pages already do `.catch(() => ({}))` on state reads; a 404 would turn "the
   counter isn't set yet" into an error path every page has to handle.
3. **Arrays.** Is `/state/items/0` an index? Reading, yes — `#>` handles integer segments.
   Writing is where it bites: `jsonb_set` will not conjure an array. Simplest defensible
   rule is that integer segments index existing arrays on read, and writes never create
   arrays implicitly. RTDB dodged this by not really having arrays.
4. **Intermediate creation — the one genuinely fiddly bit.** `jsonb_set(..., create_missing
   := true)` creates only the *final* key. `PUT /state/a/b/c` where `a` does not exist
   fails. Either build the intermediate objects in Go before the call, or nest
   `jsonb_set` per level. Not hard, but it is the part that will have the bugs.
5. **`ETag` at a path.** Keep it document-level for now: a subtree read returns the
   document's `ETag`, and `If-Match` guards the whole document. Conservative, preserves
   exactly today's semantics, and costs nothing. Per-subtree versions only become
   meaningful once segments exist (§4, Tier B) — at which point a segment's version *is*
   the subtree's version, for free.
6. **Depth and size caps.** Bound path depth (32 is generous) and segment count. A cheap
   guard against a pathological path turning into a pathological query.

### Effort

- **Reads** (`GET /state/<path>`) — small. One SQL operator, the existing splitter, a null
  case, and OpenAPI. This is the piece I would ship first and alone.
- **Writes** (`PUT`/`DELETE /state/<path>`) — about a day, nearly all of it in intermediate
  creation and array semantics rather than plumbing.
- **Docs** — `openapi.yaml` is the source of truth and `scripts/check-docs-sync.sh` will
  hard-fail on an undocumented route; `llms.txt` and both skills need the new addressing
  taught, or agents will keep sending whole documents.

### Why this ordering matters

**Segments are subtrees.** A segmented store and a tree API share one addressing model, so
building the tree first is not throwaway work — it is precisely the interface Tier B needs.
Build the tree now because it is cheap and independently useful; build segmentation only if
a document ever gets large enough to require it. The reverse order would mean designing
storage for an addressing scheme that does not exist yet.

## 11. Open questions

- What is the real segment cap? 256 KB is a guess. It should be measured against
  `TOAST_TUPLE_THRESHOLD` behaviour and typical write patterns, not chosen by feel.
- Should old manifests be retained as snapshots? Nearly free given immutable blobs, and it
  would give state the version history that deploys already have. Attractive, out of scope.
- Does the segment boundary need to be stable across writes? If a split point moves,
  `ETag` semantics need care so a client's `If-Match` still means what it meant.
- Should Tier B apply to `collection_items` rows too? A single 64 KB item is already
  capped, so probably not — but a photo-heavy collection has the same TOAST behaviour.
