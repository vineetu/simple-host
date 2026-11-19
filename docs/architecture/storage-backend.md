# Deploy-content backend inventory (D4-S6)

`SiteFiles` owns deploy bytes. `LocalServing` owns the served tree, links,
markers and local operational paths. `DiskStorage` implements both; there is
no production second backend. `app/sites` re-exports these contracts so
transports keep the existing handler → app import boundary; no layering row
is widened. `FileVersion` selects a retained version, the
live copy, or a recently deleted tree without a filesystem path.

## Package inventory

Inventoried before the interface change, after the rules-track storage work.
Each row includes every receiver method in that source file, including private
implementation helpers. The method counts in the old brief predate this tree.

| Source | Content (`SiteFiles`) | Local serving / implementation |
|---|---|---|
| `disk.go` | `WriteFiles`, `RenameSite`, `DeleteSite`, `DeleteUser`, `DeleteVersion`, `RemoveFileEverywhere` | `Changes`, `MarkChanged`, `changed`, `DataDir`, `SiteDir`, `VersionDir`, `BindDomain`, `UnbindDomain`, `UpdateCurrent`, `EnsureHandleLink`, `pointDomain`, `removeCompatLink`, `SetDomainRedirect`, `ClearDomainRedirect`, `HasHandleLink`, `RemoveHandleLink`, `RemoveHandleLinkOf`, `SetSuspended`, `IsSuspended`, `SetOffline`, `IsOffline`, `SetPasscodeMarker`, `HasPasscodeMarker`, `removeLegacyUnlistedMarkers`, `RedirectsFile`, `writeRedirectsMarker`, `ReadRedirects` |
| `trash.go` | `TrashSite`, `RestoreSite`, `PurgeTrashedSite` | `TrashDir`, `SetTrashedSuspended`, `PurgeTrashedSiteLinks`, `UnbindDomainOf` |
| `families.go` |  | `FamilyDir`, `BindFamily`, `UnbindFamily`, `FamilyLinkTarget`, `SetLivesElsewhere` |
| `footprint.go` | `SiteFootprint`, `AccountFileBytes` |  |

Other source files in the package:

| Files | Classification |
|---|---|
| `certreq.go` | Local certificate issuer hand-off; unchanged free functions, outside both interfaces. |
| `import_staging.go` | Local private import scratch files; unchanged free functions, outside both interfaces. |
| `maintenance_lock.go` | Rules-track local maintenance lock and install identity; unchanged, outside both interfaces. |
| `sqlite_gate.go`, `export_sqlite.go`, `runtime_*.go`, `rules/` | Per-site runtime resources (SQLite databases, and key-value namespaces, each one SQLite file and the only key-value store), SQLite admission/mechanics and the nested rules module; unchanged and outside the deploy backend. |
| `backend_disk.go`, `backend_open_*.go` (new) | Content reads, walks and version listing; platform search-only directory flags; local trash redirect-marker read and auxiliary-byte measurement. |
| `access_marker.go` | Durable local access markers (`setAccessMarker`, `SetTrashedAccessMarker`); trash repair remains part of `LocalServing` before restore. |
| `*_test.go` | Existing storage coverage plus the shared disk/in-memory contract suite. |

Disk lifecycle calls already move a whole site directory (runtime files and
markers included), and rename/delete already coordinate domain/handle links.
This interface-only slice preserves their signatures, order and rollback. A
future backend must coordinate those local effects with `LocalServing`; this
slice does not split or relocate runtime resources.

## Caller inventory

Content callers now use `ReadFile`, `OpenFile`, `StatFile`, `StatVersion`, `WalkFiles` or footprints.
Walk readers open bytes only when read, so patches can skip removed or replaced
files before encountering read errors. Retained-file listings use `StatFile`
for metadata, including safe internal symlinks, without requiring readable bytes.
The retained-file API keeps its stricter filename validation at the endpoint;
backend reads accept deployed names, including literal backslashes, within the version.
Live page opens preserve `os.Root.Open` component resolution, including missing
parents and internal directory symlinks followed by `..`. Retained reads resolve
contained symlinks, then open from a held directory using search-only parent
handles and refusing symlink replacements after validation. Relative data roots
are made absolute before computing the resolved path relative to that handle.
`StatVersion` checks a tree without enumerating its parent or opening its bytes,
preserving retained reads through searchable parents and exports of readable
redirect markers beside trees that cannot be listed.

| Caller | Content operation |
|---|---|
| `app/sites/service.go` | Patch merges walk the active version. |
| `app/sites/export.go`; `handler/export.go`, `account_data.go`, `heldsites_export.go` | Live, trash and held-project exports select contents logically and walk files; runtime export still uses its separate local root. |
| `handler/versionfiles.go` | Retained-file read/list through the backend, keeping metadata retention checks, invalid-path errors and safe internal-symlink behavior. |
| `handler/visitor_tools.go`, `showcase.go`, `seo.go` | Page text, metadata and crawler preferences read deploy contents through the backend. |
| `app/operations/imagescan.go` | Deploy-image reads/removals use the backend; evidence and runtime buckets remain local. |
| `server/sitesummary.go` | Summary backfill reads the selected version through the backend. |
| `handler/heldsites_export.go` | Account deploy footprint plus local auxiliary bytes preserves the existing all-regular-file total, including runtime resources and markers. |

Physical installation usage and research footprint scans (`handler/usage.go`,
`adminsites.go`, `namepeer.go`, `capacity`, `papermetrics`) measure local disk
metadata, including runtime files, markers and other installation files. They
do not read deploy contents and retain their existing accounting.

## Remaining `DataDir()` / `VersionDir()` uses

Captured with `git grep -n -E 'DataDir\(\)|VersionDir\(' -- '*.go'`
after the cut. Storage definitions/internal uses are backend implementation;
all test-only uses are fixture setup/assertions (content fixtures or local
serving/link/marker/runtime assertions), never a production content reader.

| Production caller outside `storage` | Classification / reason |
|---|---|
| `app/sites/service.go`: `DataDir()` | Local operational `.deploy-queue` hand-off. |
| `app/operations/imagescan.go`: `DataDir()` | Local evidence default and refusal of evidence under served paths. |
| `handler/adminsites.go`, `handler/usage.go`: `DataDir()` | Local physical installation usage, not deploy-content reads. |
| `handler/preview.go`: `VersionDir()` | Local web-serving root for a retained-version preview. |
| `papermetrics/product.go`: `DataDir()`; `products/hack/papermetrics/hack_counter.go`: `DataDir()` | Local physical research measurements; not deploy-content reads. |

Other local serving roots (`SiteDir` / `TrashDir`) remain in page serving,
preview/path-existence routing, redirects and runtime-resource export/access.
Deploy export roots no longer come from those methods. No production caller
outside `storage`, `server` or `static` names `*storage.DiskStorage`; product
fixtures use the interfaces as well.
