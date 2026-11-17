# Access markers (D4-S2)

A passcode or named-viewer setting requires a durable `passcode` file beside
`current`. The name lock serializes protection mutations, publication and marker
removals.
The marker file is synced before its directory; removal syncs the directory.
Missing folders remain a disk-level no-op, and protecting service calls verify
that the marker exists before sending their database statement.

An autocommit error does not prove rollback. Under the same lock, the service
re-reads the protection row and removes the marker only if the same site is
known to be open. A failed read leaves the marker. Opening a site changes the
database first; a failed marker removal returns the existing retry error and
leaves a harmless marker, which a later sync removes. Generation bumps already
have a marker and do not change one.

Account suspension adds take-down markers immediately, without the name lock
or a second database read. Startup and periodic full sync likewise add missing
take-down, offline and access markers from the list before attempting any
removals. Only removals take the name lock and re-read the live row. A missing
row or a replacement site at that name leaves the old candidate untouched.

## Paths that make files reachable

| Path | Marker before publication |
| --- | --- |
| First create, including create-or-update when new | `Service.Create` writes and verifies the marker before committing; `UpdateCurrent` and the existing settle pause follow. The SIM-448 flow is retained. |
| Update, archive upload and import | `Service.Update` writes the version, then repairs and verifies access before commit and `UpdateCurrent`. `ConnectorHandler.publishImport` uses the archive upload path. |
| Activate or roll back a version | `activateVersion` repairs and verifies access before `UpdateCurrent`. Compensation uses the same marked directory. |
| Rename | `Service.Rename` writes and verifies access in the old directory before `RenameSite` moves it, holding both name locks. |
| Restore Recently deleted | `RestoreTrashed` repairs access in the deleted directory before `RestoreSite` moves it. The existing effective suspension repair stays before the move too. |
| Failed trash commit | `Trash` moves the same already-marked directory back through `RestoreSite`; it does not reconstruct it. |

The prior restore implementation explicitly repaired only the suspension
marker; it relied on the access marker surviving the trip through trash.

## Request directory

`serveSiteFile` opens the site directory once with `os.OpenRoot`. Its three
marker checks, redirect rules (including legacy `current/_redirects`), custom
error pages and normal file reads use that handle. Files within `current` use
a child root so content symlinks still cannot escape the live content tree.
Renaming, deleting or reusing the path cannot switch a checked request onto a
replacement directory. Redirect cache entries also require file identity.

For a marked directory, the protection lookup takes the site's name lock and
compares the open directory with the directory currently at that name before
reading the row. Rename and deletion cannot change that binding during the
lookup. A mismatched directory or a missing live row returns 404: a public
replacement row must never authorize the original protected directory.
The lock wait stops on request cancellation or after one second and returns
the existing service-error page, so a stalled export cannot hang visitors.

A request checked before a protection change may finish; the protocol does not
revoke an in-flight response. nginx/Caddy still use the existing settle pause.
Static is not started or installed by this slice.

The database-backed handler tests inject failures before and after autocommit,
including a failed recovery read, pause a stale sync, and replace directories
after the gate check. The shared assertion checks all protected sites owned by
the fixture, including trash. SIM-448's creation tests are unchanged.
