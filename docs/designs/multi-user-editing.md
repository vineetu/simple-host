# Design: Multi-User Editing (Sharing & Collaboration)

Status: Proposal
Author: Platform
Date: 2026-06-25

## Summary

Today every site in simple-host has exactly one owner (`sites.user_id`, a single
FK to one `users` row). There is no way for a second person to upload a new
version, roll back, or even see a site they didn't create. This design adds
**per-site collaborator grants** with three roles (`owner`, `editor`, `viewer`),
email-based invites that reuse the existing magic-link flow, **version
attribution** (who uploaded each version), and **optimistic concurrency** on
upload (an `If-Match: <active_version>` guard) so two editors can't silently
clobber each other.

It is deliberately **not** a teams/org product and **not** a real-time
co-editor. simple-host hosts immutable static tarball versions; the unit of
concurrency is "an upload", not "a keystroke". The recommendation is the smallest
schema and code change that makes "let my collaborator also deploy this site"
work safely, with a clean migration that leaves every existing API key and every
existing single-owner site working untouched.

**Recommendation in one line:** add one table `site_collaborators(site_id,
user_id, role)`, replace the scattered `site.user_id == caller.id` checks with a
single `authorize(ctx, db, user, siteName, need) (Site, error)` helper, add a
`created_by` column to `versions`, and guard uploads with `If-Match`.

## Goals

- Multiple users can be granted access to one site, with distinct capability
  levels (view / deploy / administer).
- Invites are email-based and reuse the existing magic-link / `auth_tokens`
  machinery — no new credential type.
- Every version records **who** uploaded it; surface that in the API and admin UI.
- Concurrent uploads are safe: a stale editor gets a clear "someone deployed
  ahead of you" error instead of silently overwriting.
- Backward compatible: existing API keys, existing single-owner sites, and the
  `ADMIN_API_KEY` short-circuit all keep working with zero migration friction.

## Non-goals

- **Teams / orgs / shared billing.** Netlify and Vercel both center on a "team"
  object that owns projects and carries billing. simple-host has no billing and
  ~30 hand-managed sites; a teams layer is overkill. (See Alternatives.)
- **Real-time collaborative editing (CRDT/OT).** This is a static-file host, not
  Google Docs. The artifact is a whole-site tarball that is extracted atomically.
  Co-editing is explicitly out of scope; the right concurrency primitive here is
  optimistic version locking, not operational transforms.
- **Per-file or per-path permissions.** Access is per-site, all-or-nothing within
  a role. Static sites are small; sub-site ACLs add complexity nobody needs.
- **SSO / SCIM / directory sync.** Out of scope; these are enterprise concerns
  that the magic-link model doesn't reach for.

## Current state (what exists today)

Ownership is single-FK and the check is duplicated across every mutating handler.

- Schema: `sites.user_id UUID REFERENCES users(id) ON DELETE CASCADE`, one owner
  per site (`README.md` lines 64-72). `versions` has **no** uploader column
  (`README.md` lines 73-81; `internal/db/models.go` lines 26-33).
- The ownership check is literally "fetch the row scoped to this user_id":
  `db.GetSiteByUser(ctx, db, user.ID, name)` returns `sql.ErrNoRows` (→ 404) if
  the caller doesn't own it (`internal/db/queries.go` lines 116-134). It is
  called in:
  - `updateSite` — `internal/handler/site.go` line 306
  - `listVersions` — line 384
  - `setActiveVersion` — line 441
  - `deleteSite` — line 500
- `createSite` writes `CreateSite(ctx, tx, user.ID, ...)` — owner is the caller
  (`internal/handler/site.go` line 230; `queries.go` lines 70-93).
- `listSites` branches on `user.IsAdmin`: admins see `ListAllSites`, everyone else
  sees `ListSitesByUser(user.ID)` (`internal/handler/site.go` lines 551-555;
  `queries.go` lines 156-206). A non-admin can only ever list sites they own.
- Admin short-circuit: `auth.Middleware` mints a synthetic `&db.User{ID:"admin",
  IsAdmin:true}` with **no DB row** when `X-API-Key == ADMIN_API_KEY`
  (`internal/auth/middleware.go` lines 42-51). Any new join/FK on `user_id` must
  never assume `"admin"` exists in `users`.
- Auth is a single `X-API-Key` header resolved to a `*db.User` in request context
  via `auth.GetUser(ctx)` (`middleware.go` lines 53-85).
- State routes (`/v1/sites/{site}/state`) are **origin-checked, not user-checked**
  — they trust the subdomain, not the API key (`internal/handler/site.go` lines
  86-202). They are unaffected by this design and stay as-is.
- Storage is whole-site atomic: `WriteFiles` stages to `.vN.tmp` then renames;
  `UpdateCurrent` copies `vN` → `current` (`internal/storage/disk.go` lines
  29-90). Versions are append-only; `version_number = MAX + 1`
  (`internal/handler/site.go` lines 324-330).

The takeaway: there is exactly **one** authorization predicate in the codebase
(`user_id == caller`), expressed five times via `GetSiteByUser` and once via the
`IsAdmin` branch. Generalizing it is a focused change.

## Proposed design

### 1. Sharing/collaboration model

**Per-site collaborator grants, not teams.** A site has one canonical `owner`
(kept on `sites.user_id` for compatibility) plus zero or more rows in a new
`site_collaborators` table. This mirrors GitHub's *personal-repository*
collaborator model (a repo owner adds collaborators directly, no org required),
which is the closest analog to simple-host's "one person, a handful of sites"
reality. See [GitHub: permission levels for a personal account
repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/repository-access-and-collaboration/permission-levels-for-a-personal-account-repository).

**Roles** (a deliberately small subset of the Netlify/Vercel/GitHub ladder):

| Role     | Can do | Maps to |
|----------|--------|---------|
| `viewer` | List the site, list versions, read state via API. **No** upload, rollback, delete, or membership change. | GitHub *Read*, Vercel *Viewer*, Netlify *Reviewer* |
| `editor` | Everything `viewer` can, plus upload new versions (`POST`/`PUT`), roll back (`active-version`). **Cannot** delete the site or manage collaborators. | GitHub *Write*, Netlify *Developer*, Vercel *Project Developer* |
| `owner`  | Everything `editor` can, plus delete the site and add/remove/change collaborators. There is always exactly one primary owner (`sites.user_id`); additional owners are `site_collaborators` rows with `role='owner'`. | GitHub *Admin*, Vercel *Owner* |

This is intentionally a 3-rung ladder. Netlify collapsed "Collaborator" into a
"Developer" role and adds Reviewer/Internal-Builder for its own product needs
([Netlify roles](https://docs.netlify.com/manage/accounts-and-billing/team-management/roles-and-permissions/));
Vercel splits team-level vs project-level roles
([Vercel access roles](https://vercel.com/docs/rbac/access-roles)). We don't need
that granularity — owner/editor/viewer covers "let them deploy", "let them look",
and "let them run the site".

**Invites reuse magic-link.** No new credential mechanism. Flow:

1. An `owner` calls `POST /v1/sites/{name}/collaborators` with
   `{"email": "x@y.com", "role": "editor"}`.
2. If a `users` row exists for that email, insert the grant immediately (the
   invitee already has an API key from a prior sign-in).
3. If not, insert a `users` row up-front (exactly as `requestSignIn` already does
   at `internal/handler/user.go` lines 98-115) and create the grant against that
   user id. The invitee gets access the moment they complete their first
   magic-link sign-in for that email — their API key is minted lazily on verify.
   Optionally fire an invite email via the existing `email.Sender`.

Because grants key on `user_id`, and a `users` row is keyed on `username` (=
email), an invite to an email that hasn't signed in yet "pre-binds" cleanly. No
separate `invitations` table is required for the MVP — the `users`-row-up-front
pattern already in the codebase covers it. (A future `invited_at`/`accepted_at`
audit trail could live on `site_collaborators`; see Open questions.)

#### Schema DDL

Consistent with the existing style in `README.md` (UUID PKs, `ON DELETE CASCADE`,
`TIMESTAMPTZ DEFAULT now()`, no migration framework — this goes in the README
schema block and is applied with `psql`):

```sql
CREATE TABLE site_collaborators (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  site_id     UUID NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  role        TEXT NOT NULL CHECK (role IN ('owner', 'editor', 'viewer')),
  invited_by  UUID REFERENCES users(id) ON DELETE SET NULL,
  created_at  TIMESTAMPTZ DEFAULT now(),
  UNIQUE (site_id, user_id)            -- one role per user per site
);

CREATE INDEX site_collaborators_user_idx ON site_collaborators (user_id);
CREATE INDEX site_collaborators_site_idx ON site_collaborators (site_id);
```

Notes:
- `UNIQUE (site_id, user_id)` makes a user's role on a site single-valued; a
  re-invite is an `UPSERT ... ON CONFLICT (site_id, user_id) DO UPDATE SET role`.
- `sites.user_id` stays as the **primary owner** and is *not* duplicated into
  `site_collaborators`. The effective permission of the primary owner is `owner`
  regardless of any row. (This keeps the backfill empty — see Migration.)
- `invited_by` references `users(id)`. The synthetic admin has no `users` row, so
  invites performed by the `ADMIN_API_KEY` user must store `NULL` here (the FK
  would otherwise fail). Handler must special-case `user.ID == "admin"` → `NULL`.

### 2. Authorization changes

Today the predicate is "the row exists for this `user_id`". Replace the five
call sites with **one** capability check. Define a role ordering and a single
`authorize` helper that resolves a site by name and verifies the caller has at
least the needed role — folding in the admin short-circuit and the primary-owner
case.

New query (`internal/db/queries.go`):

```go
// GetEffectiveRole returns the caller's role on a site by NAME, considering both
// primary ownership (sites.user_id) and collaborator grants. Returns "" (and
// sql.ErrNoRows) if the site doesn't exist; returns ("", nil) if the site exists
// but the user has no grant. The site row is returned so callers avoid a 2nd read.
func GetEffectiveRole(ctx context.Context, db *sql.DB, userID, siteName string) (Site, string, error) {
	const q = `
		SELECT s.id, s.user_id, s.name, s.active_version, s.site_url,
		       s.created_at, s.updated_at,
		       CASE
		         WHEN s.user_id = $1 THEN 'owner'
		         ELSE c.role
		       END AS effective_role
		FROM sites s
		LEFT JOIN site_collaborators c
		       ON c.site_id = s.id AND c.user_id = $1
		WHERE s.name = $2
	`
	var site Site
	var role sql.NullString
	err := db.QueryRowContext(ctx, q, userID, siteName).Scan(
		&site.ID, &site.UserID, &site.Name, &site.ActiveVersion,
		&site.SiteURL, &site.CreatedAt, &site.UpdatedAt, &role,
	)
	if err != nil {
		return Site{}, "", err // sql.ErrNoRows => site doesn't exist
	}
	return site, role.String, nil // role.String == "" means "no access"
}
```

Authorization helper (new `internal/auth/authz.go`, or a method on `SiteHandler`):

```go
// roleRank orders capabilities. Higher = more power.
var roleRank = map[string]int{"viewer": 1, "editor": 2, "owner": 3}

func atLeast(have, need string) bool {
	return roleRank[have] >= roleRank[need]
}

// authorizeSite resolves a site by name and checks the caller has >= need.
// Admin short-circuits to owner on every site. Returns the site row so handlers
// don't re-query. On no-access it returns 404 (not 403) to avoid leaking the
// existence of sites the caller can't see — matching today's GetSiteByUser
// behavior, which 404s on a foreign site.
func (h *SiteHandler) authorizeSite(ctx context.Context, user *db.User, name, need string) (db.Site, error) {
	if user.IsAdmin { // synthetic admin (ID=="admin", no users row) or a real is_admin user
		site, err := db.GetSite(ctx, h.database, name)
		return site, err // admin sees everything; err is sql.ErrNoRows if absent
	}
	site, role, err := db.GetEffectiveRole(ctx, h.database, user.ID, name)
	if err != nil {
		return db.Site{}, err // ErrNoRows -> handler renders 404
	}
	if role == "" || !atLeast(role, need) {
		return db.Site{}, sql.ErrNoRows // treat "exists but no access" as 404
	}
	return site, nil
}
```

Handler call sites then collapse. For example `updateSite`
(`internal/handler/site.go` line 306) changes from:

```go
site, err := db.GetSiteByUser(r.Context(), h.database, user.ID, siteName)
```

to:

```go
site, err := h.authorizeSite(r.Context(), user, siteName, "editor")
```

Mapping of need-level per route:

| Handler | Route | Required role |
|---------|-------|---------------|
| `createSite` | `POST /v1/sites/{name}` | n/a — creating; caller becomes primary owner (`sites.user_id = caller`). Reject if site name already taken (existing 409 path). |
| `updateSite` | `PUT /v1/sites/{name}` | `editor` |
| `setActiveVersion` | `PUT .../active-version` | `editor` |
| `listVersions` | `GET .../versions` | `viewer` |
| `listSites` | `GET /v1/sites` | self — see below |
| `deleteSite` | `DELETE /v1/sites/{name}` | `owner` |
| collaborator CRUD | `.../collaborators` | `owner` |

**`listSites` change:** today non-admins see only `ListSitesByUser(user.ID)`.
Generalize the query to UNION owned sites with sites where the user has a
collaborator grant:

```sql
SELECT s.id, s.user_id, s.name, s.active_version, s.site_url,
       s.created_at, s.updated_at,
       CASE WHEN s.user_id = $1 THEN 'owner' ELSE c.role END AS my_role
FROM sites s
LEFT JOIN site_collaborators c ON c.site_id = s.id AND c.user_id = $1
WHERE s.user_id = $1 OR c.user_id = $1
ORDER BY s.created_at ASC, s.name ASC;
```

Add `my_role` to `siteResponse` (`internal/handler/site.go` lines 33-43) so the
UI can render a badge and hide controls the caller can't use. The `IsAdmin`
branch (`ListAllSites`) is untouched.

### 3. Concurrent edit safety

The artifact is a whole-site tarball extracted atomically (`disk.WriteFiles`
stages then renames — `internal/storage/disk.go` lines 29-74). There is no
sub-document merge to do; the only hazard is **lost update**: editor A and editor
B both `GET` the site at v5, both build locally, both `PUT`. Today the second PUT
just becomes v6 and silently wins, and A never learns their work was based on a
now-stale view.

**Recommendation: optimistic concurrency via `If-Match` on the active version.**
This is the standard HTTP pattern — client sends the version it believes is
current; server rejects with `412 Precondition Failed` if it has moved on. See
[Optimistic Concurrency Control in HTTP
Services](https://medium.com/bestmile/optimistic-concurrency-control-in-http-services-c1bd911b89ad)
and [HTTP 412](https://http.dev/412).

Concretely:
- `GET /v1/sites/{name}` and `.../versions` responses already expose
  `active_version`. Also set an `ETag` header = `"v<active_version>"` so HTTP
  tooling can round-trip it.
- On `PUT /v1/sites/{name}` (upload) and `PUT .../active-version`, **optionally**
  accept `If-Match: "v5"`. If present and it doesn't equal the site's current
  `active_version`, return `412` with a body telling the caller to re-pull:
  ```json
  { "error": "site has been updated by another editor",
    "active_version": 7, "your_version": 5 }
  ```
- If `If-Match` is **absent**, preserve today's last-write-wins behavior (so
  existing clients and the MCP plugin keep working unchanged). The guard is
  opt-in; the Website Deploy plugin can start sending `If-Match` once the server
  supports it.

**Make the version bump atomic.** Today `GetMaxVersionNumber` then
`CreateVersion(max+1)` runs inside a tx (`internal/handler/site.go` lines
317-359), but two concurrent uploads can both read `max=5` and race on
`INSERT ... version_number=6`. The `UNIQUE(site_id, version_number)` constraint
(`README.md` line 81) already prevents a double-6 — the loser gets a unique
violation. Today that surfaces as a 500. With this design, **catch the unique
violation on `CreateVersion` and return `409 Conflict`** ("concurrent upload, retry"),
and check `If-Match` *inside* the transaction with `SELECT ... FOR UPDATE` on the
`sites` row so the precondition and the bump are consistent:

```go
// inside BeginTx:
//   SELECT active_version FROM sites WHERE id=$1 FOR UPDATE   -- row lock
//   if ifMatch != "" && ifMatch != active_version -> 412, rollback
//   max := GetMaxVersionNumber(tx, siteID)
//   CreateVersion(tx, siteID, max+1, ...)   -- unique-violation -> 409
```

This is a few lines in `updateSite`; no new infrastructure.

**Real-time co-editing: explicitly out of scope.** CRDTs/OT solve concurrent edits
to a *shared mutable document*. simple-host has no shared mutable document — it has
immutable versioned snapshots. The pragmatic, correct primitive is optimistic
locking, which we get for nearly free from the existing version counter. We will
not build OT/CRDT.

### 4. Audit & attribution

Add an uploader column to `versions` so every version records who created it:

```sql
ALTER TABLE versions
  ADD COLUMN created_by UUID REFERENCES users(id) ON DELETE SET NULL;
```

- `created_by` is `NULL`able and `ON DELETE SET NULL` so deleting a user doesn't
  cascade-delete history, and so the synthetic admin (no `users` row) can write
  `NULL` (handler special-cases `user.ID == "admin"` → `NULL`, same as
  `invited_by`).
- Wire it through `CreateVersion` (`internal/db/queries.go` lines 351-368): add a
  `createdBy *string` param. `updateSite`/`createSite` pass `user.ID` (or `nil`
  for admin).
- Surface it: extend `versionResponse` (`internal/handler/site.go` lines 45-50)
  with `CreatedByUsername string` (LEFT JOIN `users` in `ListVersionsBySite`,
  `queries.go` lines 370-393). The admin UI version list (and the per-site
  version history) then shows "v7 — uploaded by alice@example.com — 2h ago".
- Collaborator changes are auditable via `site_collaborators.invited_by` +
  `created_at`. (A fuller `audit_log` table is a future option; see Open
  questions — not needed for MVP.)

### API changes (summary)

New endpoints (registered in `SiteHandler.Register`, `internal/handler/site.go`
lines 61-75; remember each `mux.Handle` line is the routing per `CLAUDE.md`):

| Endpoint | Method | Role | Body / Notes |
|----------|--------|------|--------------|
| `/v1/sites/{name}/collaborators` | GET | `viewer` | List grants `[{username, role, invited_by, created_at}]`. Owner row synthesized from `sites.user_id`. |
| `/v1/sites/{name}/collaborators` | POST | `owner` | `{email, role}` → upsert grant; create `users` row if email unknown. |
| `/v1/sites/{name}/collaborators/{email}` | PUT | `owner` | `{role}` → change role. |
| `/v1/sites/{name}/collaborators/{email}` | DELETE | `owner` | Revoke grant. Cannot remove the primary owner; reassign first. |

Changed responses:
- `siteResponse` gains `my_role` (the caller's effective role).
- `versionResponse` gains `created_by_username`.
- `PUT /v1/sites/{name}` and `.../active-version` honor optional `If-Match`,
  return `412` on mismatch, `409` on the version-bump race.
- All write handlers respond `403` if the caller is authenticated but
  under-privileged for that specific action (e.g. an `editor` calling DELETE),
  while keeping `404` for "site not visible to you at all" to avoid existence
  leakage. (Editor-tries-to-delete is a known-visible site, so 403 is correct and
  not a leak.)

### UI notes

The admin UI is embedded HTML (`internal/handler/static/`, rebuild required per
`CLAUDE.md`). Minimal additions:
- Site list: a role badge (`owner`/`editor`/`viewer`) from `my_role`; hide
  Delete/Share for non-owners, hide Upload/Rollback for viewers.
- Per-site "Collaborators" panel: list grants, add-by-email with a role select,
  remove, change-role — all gated to owners.
- Version history: show `created_by_username` next to each version.

## Migration & rollout plan

Designed to be **zero-break**. No existing key, site, or request changes behavior
until someone opts in by adding a collaborator or sending `If-Match`.

1. **Schema (additive only).** Apply against the live Postgres (no migration
   framework — `psql`, mirroring `README.md`'s setup block):
   ```sql
   CREATE TABLE site_collaborators (...);          -- new, empty
   ALTER TABLE versions ADD COLUMN created_by UUID  -- nullable, backfills to NULL
     REFERENCES users(id) ON DELETE SET NULL;
   ```
   Both are additive; existing rows are valid as-is. **No backfill of
   `site_collaborators` is needed** — the primary owner is still `sites.user_id`,
   which `GetEffectiveRole`/`authorizeSite` resolve to role `owner` without any
   row. Existing `versions` rows simply have `created_by = NULL` ("uploader
   unknown / pre-attribution"), which the UI renders as "—".

2. **Code, behind the same behavior.** Deploy a binary where:
   - `authorizeSite` replaces `GetSiteByUser` at the 5 call sites. For a
     single-owner site with no collaborators, it returns exactly what
     `GetSiteByUser` returned (owner → 404 for everyone else). Behavior is
     identical until a grant exists.
   - `If-Match` is honored only when present; absent = today's behavior.
   - New collaborator endpoints exist but do nothing until called.
   Because the source module path is `github.com/vsriram/simple-host` and the
   binary is built with `go124` and swapped in via `cortex-runports repair --port
   8090` (see root `CLAUDE.md`), this is an ordinary rebuild+redeploy. The
   `DB_DSN`/`ADMIN_API_KEY` env on the runports process is preserved by a binary
   swap + `repair`.

3. **Rollout order:** apply schema first (safe while the old binary runs — it
   ignores the new table/column), then swap the binary, then `repair`. Verify
   with `/healthz`, `/readyz`, and an end-to-end "share a test site to a second
   email, sign in as that email, upload" check (there are no unit tests in this
   repo — verify against the running server, per `CLAUDE.md`).

4. **Plugin update (optional, later):** teach the Website Deploy MCP server to
   send `If-Match` on deploy and to surface a `412` as "someone deployed ahead of
   you; pull latest". This is a plugin version bump (`plugin.json`) and rides the
   existing `_notice`/skill-staleness channel — no permission re-ask.

5. **Rollback:** the binary swap is reversible via the timestamped
   `simple-host.bak-*` backups (root `CLAUDE.md`). The schema additions are inert
   to the old binary, so a binary rollback needs no DB rollback.

## Security considerations

- **404-vs-403 leakage.** Keep returning `404` for sites the caller has no grant
  on (preserves today's `GetSiteByUser` semantics — a foreigner can't even probe
  existence). Use `403` only for "you can see it but can't do *this* action"
  (e.g. editor → DELETE), which is not a leak because visibility is already
  established.
- **Privilege escalation via invite.** Only `owner` may add/modify/remove grants,
  and an `owner` cannot grant a role they don't have (n/a here since owner is the
  ceiling). An `editor` cannot invite anyone. Enforce server-side; never trust the
  UI to hide it.
- **Primary-owner integrity.** `sites.user_id` must always reference a live user.
  Don't allow deleting the primary owner's grant via the collaborator API; require
  an explicit owner-transfer (out of scope for MVP — Open question). `ON DELETE
  CASCADE` on `sites.user_id` (existing) means deleting the owning user still
  deletes the site, which is the current behavior — collaborators would lose the
  site. Flag this in docs.
- **Admin synthetic user.** `invited_by` and `created_by` must be `NULL` for the
  `ADMIN_API_KEY` user (`ID="admin"` has no `users` row); a non-null FK write
  would fail. Both columns are nullable and handlers special-case it.
- **Invite to an unknown email pre-creates a `users` row** (with an API key never
  disclosed until magic-link verify — same property as `requestSignIn`). This is
  safe: the key is only revealed after the email round-trips. Rate-limit the
  collaborator-invite endpoint to avoid using it as a user-enumeration / spam
  vector (it can email arbitrary addresses).
- **State endpoints unchanged.** `/state` remains origin-checked, not
  role-checked — collaboration doesn't touch it. Any user who can serve the site
  can already read/write its state from the browser; that's by design and
  orthogonal.
- **No new credential type.** Access is still a single `X-API-Key`; collaboration
  changes *what a key can reach*, not *how keys are minted*. Smaller attack
  surface than adding tokens/sessions.

## Alternatives considered

### A. Teams / orgs (Netlify & Vercel model) — rejected for MVP
Both Netlify and Vercel center on a **team** object that owns projects and carries
billing, with team-level roles (Owner/Member/Developer/Viewer) and optional
project-scoped overrides
([Netlify roles](https://docs.netlify.com/manage/accounts-and-billing/team-management/roles-and-permissions/),
[Vercel access roles](https://vercel.com/docs/rbac/access-roles),
[Vercel project-level roles](https://vercel.com/docs/rbac/access-roles/project-level-roles)).
This is the right model at scale (shared billing, dozens of members, SSO/SCIM),
but it adds two tables (`teams`, `memberships`), a team-selection concept, and a
much bigger blast radius — for a host with no billing and ~30 owner-managed sites.
We can grow into it: `site_collaborators` is forward-compatible with later adding
a `team_id` to `sites` and a team→site default-role layer. Start per-site.

### B. Cloudflare-style policy/scope engine — rejected
Cloudflare models access as policies of (actor, role, scope) with account-,
domain-, and resource-scoped roles
([Cloudflare account roles](https://developers.cloudflare.com/fundamentals/manage-members/roles/),
[role scopes](https://developers.cloudflare.com/fundamentals/manage-members/scope/)).
Extremely flexible, extremely heavy. A general policy engine for a 3-role,
one-resource-type system is over-engineering.

### C. GitHub personal-repo collaborators — chosen as the model
GitHub personal repos let an owner add collaborators directly (pull/push), without
an org; orgs add the Read/Triage/Write/Maintain/Admin ladder
([GitHub personal-repo
permissions](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/repository-access-and-collaboration/permission-levels-for-a-personal-account-repository),
[GitHub org repository roles](https://docs.github.com/en/organizations/managing-user-access-to-your-organizations-repositories/managing-repository-roles/repository-roles-for-an-organization)).
Our `site_collaborators(site_id, user_id, role)` with viewer/editor/owner is the
direct analog of personal-repo collaborators plus a trimmed role ladder. Best
fit for the codebase as it stands.

### D. Concurrency: last-write-wins (status quo) vs. pessimistic locks vs. optimistic — optimistic chosen
- *Last-write-wins* (today): simplest, but silently loses an editor's work. Fine
  for one owner, dangerous for many.
- *Pessimistic lock* ("check out the site to edit"): no lost updates, but holds
  state across requests, needs lock expiry, and fights the stateless API. Overkill
  for infrequent deploys.
- *Optimistic `If-Match`*: standard HTTP, stateless, opt-in, and we already have
  the version counter to compare against
  ([OCC in HTTP](https://medium.com/bestmile/optimistic-concurrency-control-in-http-services-c1bd911b89ad)).
  Chosen.

### E. CRDT / OT real-time co-editing — rejected (wrong problem)
The artifact is an immutable extracted tarball, not a live shared document. There
is nothing to merge. Out of scope.

## Open questions

1. **Owner transfer.** MVP keeps the primary owner on `sites.user_id` and forbids
   removing it via the collaborator API. Do we need an explicit
   `PUT /v1/sites/{name}/owner` to hand a site off (e.g. before offboarding)?
   Recommended as a fast-follow.
2. **Deleting the owning user.** `sites.user_id` is `ON DELETE CASCADE` today, so
   deleting an owner deletes their sites out from under any collaborators. Do we
   want to instead block deletion while collaborators exist, or auto-promote a
   collaborator-owner? Needs a product call.
3. **Invite emails.** Do we send a "you've been added to {site}" email via the
   existing `email.Sender`, or rely on the invitee discovering it on next sign-in?
   Email is nicer UX but adds a Resend dependency to the invite path.
4. **Full audit log.** `created_by` + `invited_by` cover the two highest-value
   events. Is a general `audit_log(actor, action, site, at)` table worth it, or is
   per-row attribution enough? Defer until asked.
5. **Per-site state and roles.** Should `viewer` be blocked from writing `/state`?
   Today state is origin-checked, not role-checked, so anyone loading the site can
   write it. Left as-is; revisit if state becomes sensitive.

## Sources

- [Netlify — Roles and permissions](https://docs.netlify.com/manage/accounts-and-billing/team-management/roles-and-permissions/)
- [Netlify — Manage project access](https://docs.netlify.com/manage/accounts-and-billing/team-management/manage-project-access/)
- [Vercel — Access Roles (RBAC)](https://vercel.com/docs/rbac/access-roles)
- [Vercel — Project Level Roles](https://vercel.com/docs/rbac/access-roles/project-level-roles)
- [Cloudflare — Account roles](https://developers.cloudflare.com/fundamentals/manage-members/roles/)
- [Cloudflare — Role scopes](https://developers.cloudflare.com/fundamentals/manage-members/scope/)
- [GitHub — Permission levels for a personal account repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/repository-access-and-collaboration/permission-levels-for-a-personal-account-repository)
- [GitHub — Repository roles for an organization](https://docs.github.com/en/organizations/managing-user-access-to-your-organizations-repositories/managing-repository-roles/repository-roles-for-an-organization)
- [Optimistic Concurrency Control in HTTP Services](https://medium.com/bestmile/optimistic-concurrency-control-in-http-services-c1bd911b89ad)
- [HTTP 412 Precondition Failed](https://http.dev/412)
- [If-Match — HTTP header guide](https://http.dev/if-match)
