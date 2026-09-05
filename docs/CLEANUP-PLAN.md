# simple-host cleanup plan (v2)

**Status:** proposal, 2026-09-01. v1 was reviewed by Codex; verdict was "ship with
named changes". This is v2 with those changes applied.

**Goal:** a stranger can read this repo and contribute to it, and adding a feature
stops requiring archaeology. Success is concrete: a response-shape change cannot
silently break a page nobody remembered.

**Not in scope:** new product features.

**Deliberately kept:** AI create talks to a Grok CLI sidecar and only that. One
provider, no fallback chain, no metered API keys. Intentional cost decision.

---

## What is wrong (measured)

| Problem | Evidence |
|---|---|
| Fresh install could not run the product | `db/schema.sql` was missing `collection_items`, `sites.state`, `sites.state_version`, `sites.view_password_hash`, and `api_request_daily` / `api_ip_daily` / `ip_geo`. Collections, per-site state, private pages and admin metrics were all dead on a clean install. Production worked only because those columns were added by hand-applied migrations. **Fixed and gated — see Phase 0** |
| Two 90 KB+ pages each carry their own copy of the same logic | `index.html` 2,478 lines, `showcase.html` 2,242. **8 functions defined independently in both**: `api`, `esc`, `toast`, `renderAnalytics`, `confirmDelete`, `rollbackTo`, `toggleVersions`, `updatePreview` |
| That duplication already shipped a user-visible bug | Analytics response shape changed; `index.html` was updated, `showcase.html` was not, and every site rendered **0 views · 0 visitors**. No error anywhere — the fields were simply `undefined` |
| Defensive noise hides real errors | 80 `catch` blocks across the two pages, mostly silent. The zeros bug produced no console error because every value passes through a guard that turns `undefined` into `0` |
| Handler grab-bags | `internal/handler/site.go` 1,505 lines, `generate.go` 1,037, `db/queries.go` 945 |
| Docs drift silently | `openapi.json` fell a whole contract behind `openapi.yaml` while `check-docs-sync.sh` printed "docs in sync ✓" — it never opened the json. Fixed |
| The edge config is not in the repo | `log_format shanalytics` exists only in `/etc/nginx/conf.d/` on the box. A rebuild from this repo produces a server whose analytics silently collect nothing |

Root cause: no shared client layer, and no test that the repo alone can stand up
a working install.

---

## Phase 0 — the day-one blockers (partly done)

1. **Fresh-install schema.** *Done.* `db/schema.sql` now creates everything the
   code queries; verified by booting the real server against a clean database and
   exercising deploy → atomic state ops → collections end to end.
2. **`scripts/check-fresh-install.sh`.** *Done.* Applies the schema to a throwaway
   database and runs one query of every shape the server issues. Fails loudly.
3. **Ship the edge config.** Move the nginx `log_format` and site templates into
   `deploy/` so the repo is sufficient to stand the system up.
4. **Fix the known public defects** (below) rather than "folding them in".
5. **One contributor command.** A `make check` that runs build, tests,
   `check-docs-sync.sh` and `check-fresh-install.sh`. Document its dependencies —
   the docs check needs Python + PyYAML and currently only *warns* when they are
   missing, so the guarantee can silently not run.

### Known public defects

| Defect | Effect |
|---|---|
| `/notfound.html` serves an unsubstituted template | Public page returns **HTTP 200** with literal `__SH_MESSAGE__` |
| `/admin.html` bypasses `adminUICSP` | Same bytes as `/admin`, without the CSP header |
| `/.well-known/` directory listing | Go file-server index exposed |
| `privacy.html` omits analytics | Stable salt + 400-day retention makes `ip_hash` a persistent pseudonymous identifier, undisclosed. **Owner decision, not a code fix** |
| `last_24h` served but unread | Dead payload; either use it or drop it |

---

## Phase 1 — one strict analytics parser

Per Codex: extract **the parser, not the renderers**. Page-specific rendering and
DOM behaviour stay page-specific — they genuinely differ, and merging them risks
breaking actions for no gain.

- One module that turns an analytics payload into a typed result, used by both pages.
- It is **strict**: an unknown or missing shape raises, it does not coerce to `0`.
- Both pages surface that failure visibly ("couldn't load analytics"), rather than
  rendering a confident zero.
- Shared `api()` is deliberately *excluded* for now: the two are not equivalent
  (`index.html` sends `X-Skill-Version` and handles `FormData`/`Blob`;
  `showcase.html` does neither). Unifying them is a behaviour change, not a move.

**Fixture tests, not live-diffing.** Live data never exercises the cases that
matter. Tests feed the parser: the current shape, the obsolete flat shape (must
fail visibly), an empty-traffic site, a site with `unknown` history, and a
malformed payload. Codex's point stands — diffing against live output cannot
prove any of these.

## Phase 2 — stop swallowing errors

Replace blanket `try {} catch {}` with a real handler or nothing. State optional
values once at the boundary instead of guarding every read.

## Phase 3 — narrow, justified moves only

Split `site.go` along its existing seams (deploys/versions, domains, visibility,
data). **Not** a per-entity split of `queries.go` — Codex is right that
line-count is not a reason, and the churn would bury the initial public history.

## Phase 4 — comments, opportunistically

Delete narration and banners while touching the surrounding code. No project-wide
pass, no percentage target. Keep comments that record a decision or a trap: why
the visitor salt is stable, why the `*_daily` tables must never be dropped, why
logrotate needs `create` + `delaycompress`.

## Phase 5 — make drift impossible

- `openapi.json` regenerated by an **explicit command**, not as a side effect of
  `go build`. CI fails when the committed file is stale.
- `check-docs-sync.sh` and `check-fresh-install.sh` run in CI.
- The architecture page maps features to packages and UI entry points. Kept
  coarse on purpose — Codex's warning that an exhaustive per-file map becomes its
  own drifting artefact is correct.

## Phase 6 — open-source hygiene

CONTRIBUTING, no box-specific paths or secrets in the tree, and fix the README
build line: it says `GOARCH=amd64` while the production box is `aarch64`, so the
documented command yields a binary that will not run there.

---

## Features that do not work as documented

Found while inventorying. These are honesty problems, not cleanup, and the
owner's standing rule is that a feature works fully or is not offered.

1. **Private pages are worse than absent.** The password can be set, but nothing
   at the edge enforces it — so pages are not actually locked. It *does*
   immediately 403 the site's own state and collections, silently breaking that
   site's forms, comments and counters, and the unlock screen is unreachable.
2. **The AI chat cannot publish.** It builds, previews and iterates, but has no
   publish control — while the model's own prompt tells users the site goes live
   in about a minute.
3. **Attached PDFs are never read.** Accepted and size-checked; contents never
   reach the model. Only images do.
4. **Custom domain status never becomes `active`** — the field has no writer — and
   HTTPS on a connected domain is a manual operator step, not automatic.
5. **Visitor sign-in is not enforced** (`write auth mode: log`); unsigned writes
   still succeed, while every agent-facing doc says a session is required.
6. **GitHub sign-in is built but switched off.**

Each needs a decision: fix, or stop advertising. That decision is the owner's.

---

## Open questions, answered

Codex's answers, recorded so they are not relitigated:

- **Shared module vs a build step:** shared module. A build step contradicts the
  project's "one binary, no build farm" pitch.
- **Phase 3 churn before open-sourcing:** do the narrow handler split, skip the
  broad one. Review risk in a dirty tree outweighs tidiness.
- **What was cosmetic:** the exhaustive blast-radius map, the comment-density
  target, build-time OpenAPI generation, splitting by line count, and live-output
  diffing as the verification method. All cut from v2.
