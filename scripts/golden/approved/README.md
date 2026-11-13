# Approved golden differences

See [Comparing two recordings](../README.md#comparing-two-recordings) for commands and diagnostics.

Add one `<slice id>.json` file in this directory in the slice's pull request.
Its only top-level fields are `slice` (the filename without `.json`) and `entries`.
Only files selected by `--slice` are read. For stacked branches use `--slice A,B`.
A named slice without a file contributes no entries, with a notice on stderr.

The reference is always built from the commit now deployed. It already includes
earlier deployed slices, whose approval files are never needed again and stay
here as the review record. There is no checked-in baseline. Nobody re-records
a baseline to make a comparison pass: record reference and candidate separately.

Each entry needs product (`host` or `hack`), request, and a reviewed, non-empty
one-sentence reason. Reasons starting with `TODO` are rejected. These examples
show all forms (put them inside the file's `entries` array):

```json
{"product":"host","request":"GET /hackathons","reason":"The directory moves to Hack.","at":"status","old":200,"new":302}
```

```json
{"product":"host","request":"GET /v1/skills","reason":"The Hack skill leaves Host.","at":"body.count","old":4,"new":3}
```

```json
{"product":"host","request":"GET /openapi.json","reason":"The description now describes Host alone.","at":"text","removed":["  description: Hosted hackathons …"],"added":["  description: …"]}
```

```json
{"product":"host","request":"tools/list personal","reason":"Host no longer needs the product qualification.","at":"body.result.tools[*].description","replace":{"from":" (not Simple Hack)","to":""}}
```

```json
{"product":"hack","request":"GET /v1/admin/usage","reason":"The old usage endpoint is retired.","change":"removed"}
```

```json
{"product":"host","request":"rules-on/tools/call get_page_recipe booking","reason":"The new scenario gains the booking recipe request.","change":"added"}
```

```json
{"product":"host","scenario":"rules-on","reason":"The reference cannot run the new rules setting.","change":"added"}
```

```json
{"product":"hack","scenario":"rules-on","reason":"The temporary rules scenario is retired.","change":"removed"}
```

Pinned `old` and `new` values must exactly match both recordings. Use the string
`"__absent__"` for a missing key or array item on either side. A pinned object or
array covers every changed leaf beneath that path. Paths use dot identifiers
and numeric array indexes: `body.result.tools[2].inputSchema`. Other object keys
use JSON bracket quotes: `body["a.b"]`, `headers["content-type"]`. Header values
(including a set-cookie array) are compared whole. No wildcard is allowed in a
pinned-value entry. At the top level of `old` or `new`, `"__absent__"` means only actual absence; it never matches a present literal string. Approve that literal through a hand-written pinned enclosing body object or array; nested strings retain their ordinary meaning. A literal `"__absent__"` at the body root or in a whole header value is unapprovable because no supported enclosing path exists.

Only `replace` permits `[*]`, selecting array indexes, including multiple nested
arrays. All traversed arrays must keep their lengths, all selected values must
be strings, and each candidate value must equal the reference after replacing
every occurrence. At least one selected value must change.

A text entry pins exactly the removed and added lines, in order, from a shortest
edit script (ties prefer deletion), after trimming the common prefix and suffix.
These lists pin lines and their order, not positions. This is sound only because the entry is checked against this exact pair of recordings and reviewed with the printed diff.
The search has a hard edit-distance cap of 4000. If the remaining edit distance
exceeds that cap, the deterministic fallback removes every remaining reference
line and adds every remaining candidate line. If either trimmed side is empty,
the other side is returned directly without searching. An approval for a fallback
must list all those removed and added lines; `--propose` prints exactly that list.
Small edits in large files still use the shortest script. It works for text bodies and the readable
rendering of JSON bodies. When both bodies are JSON, it owns a changed leaf only if one side is absent, or neither value is `rendersLossy` and their `renderJSON` outputs differ. Embedded JSON strings (including scalar documents) are normalized lossily, so every changed string of that form needs a structural pin even when its rendered lines also change. Negative zero and non-finite numbers are also lossy; containers containing lossy values need pins when compared whole. These pins may sit beside a text entry without overlap. The text entry must still list the whole rendered line difference, including lines from lossy values; it may verify those lines while the structural pins own all changed leaves. No separate hash/length entry is needed. Every JSON text file must equal `renderJSON(body)` exactly. Every text file must be valid UTF-8, and its hash and length are verified against its actual bytes
before comparison, even for wholly approved requests or scenarios. Text file
paths, hashes and lengths are derived and never independent differences. For
JSON, structural body comparison is authoritative; a rendering-only change
cannot consume an entry. A trailing newline is significant (the split includes
the final empty line). Empty added or removed arrays are valid.

Every difference must have exactly one matching entry; every entry must cover
at least one difference, except a matching text entry may verify lines from lossy JSON leaves owned by structural pins. Wrong old/new values, extra changes in the same
request, unused entries, duplicate entries and overlapping parent/child entries
fail. A text entry overlaps any body entry that covers the same body change;
such combinations fail unless the body entry owns only leaves excluded from text ownership. Request additions/removals account for their sequence
shift; reordering surviving requests always fails.

Named scenarios use `<scenario>/<request name>` for ordinary request entries.
If the prefix before the first `/` is a non-base scenario recorded for that product in either folder, it identifies that scenario; otherwise it identifies a base request. A base request whose name starts with a recorded scenario prefix is an integrity error (`base request name collides with scenario prefix`). Whole
scenario entries apply only when that run exists on one side, once per product;
`base` can never be approved whole. Attach that scenario's output to the pull
request and read it line by line. The comparator prints its JSON/text locations
and request count. After the next deploy both programs run it and ordinary
request comparison applies.

Record the candidate with `--scenario all`. Record the reference with
`--scenario all --skip-scenario <each scenario added by the slices named in --slice>`;
use one comma-separated `--skip-scenario` list for multiple scenarios. For example, use
`node scripts/golden/run.mjs --host /tmp/reference.bin --out /tmp/golden-reference --scenario all --skip-scenario rules-on`
and `node scripts/golden/run.mjs --host /tmp/candidate-host.bin --out /tmp/golden-candidate --scenario all`.
An `added` whole-scenario entry requires that scenario to be absent from the
reference folder; a `removed` entry requires it to be absent from the candidate
folder. If both folders contain it, compare request by request. For an addition the reference may omit a scenario its program cannot run. For a removal the candidate must stop recording that scenario (drop it from the scenario table).

## How a slice uses this, step by step

1. Build both programs: the deployed commit for the reference and the slice for the candidate.
2. Record both into separate folders, using the scenario flags above.
3. Run `diff.mjs` with `--slice` naming the slice (or stacked slices) and `--propose`.
4. The comparator cannot know which named scenarios should exist: a scenario missing from both folders is not detected. Record the candidate with `--scenario all` and the reference with every scenario its program can run; check the successful comparison’s `N requests in M runs` line against the scenario table.
5. Review each proposed entry against the slice text and both recordings, then write its true reason.
6. Commit the approval file in the slice's pull request, attaching whole-scenario output for review.
7. Re-run with the same `--slice`, without `--propose`, until exit 0.

## What a reviewer checks

- The `N requests in M runs` line agrees with the scenario table; no expected named scenario is missing from both folders.
- Every entry has a true reason.
- Pinned values are what the slice text predicts.
- No entry is broader than needed: prefer a leaf path over a parent object,
  and use a text entry only where lines are what changed.
- Whole-scenario output was attached and read line by line.

Body-kind changes are strict: approve `body_kind` separately and explicitly
approve the body changes. A missing text file counts as zero lines: approve the removed or added lines with an `at:"text"` entry. For a kind transition, the text entry covers no structural body differences; pin `body` separately when a JSON or binary body appears, disappears, or changes, using `"__absent__"` on the absent side. Empty bodies have no body.

`--propose` prints a ready-to-paste draft document on stdout and diagnostics on
stderr. It never writes a file or changes the failing exit status. Review every
entry, fill each reason, and paste into the slice file. One-sided text transitions produce complete proposals unless a structural pin would require the reserved literal string. `--propose` never promotes a reserved literal to a parent: it reports `cannot propose: the value is the reserved string __absent__; pin an enclosing object by hand` on stderr and emits no entry for that difference. Other differences are still proposed and the comparison stays failed (exit 1). Reorders cannot be proposed as approvable entries. Use `schema.json`
(draft 2020-12) in editors; the command also validates files without dependencies.

Every recorded run, base or named, must contain at least one request. By default `--require host,hack` requires base runs for both products in both folders. A Host-only fixture can use `--require host`. Recording artifacts must be regular files and directories must be real directories: symlinks, FIFOs, sockets and devices are rejected before reading. Recording folders require real-path containment; text metadata has exactly `file`, `sha256`, and `length`.
