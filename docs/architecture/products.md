# Products and add-ons

The owner decisions of 2026-11-12 in [INTENT.md](../../INTENT.md) define three
areas: the platform (`host/`), full products (`products/`) and future add-ons
(`addons/`, created with the first add-on). The implemented full product is Simple
Hack. Light products and add-ons below are designs only.

## Full products and the implemented boundary

`cmd/simple-host` builds the platform alone; `cmd/simple-hack` builds it with
`products/hack.Extension()`. Both call `host/server.Run`. Product selection is
fixed by the entry point, not by `EVENTS`. At most one full product belongs to an
install, alongside sites and, when implemented, any number of light products.
Each install has separate accounts, database and files. Its `install_product`
record prevents the wrong program from starting against it.

Platform code imports only platform code; a product imports the platform and
itself. Products and add-ons never import each other. The fine package layering
rules still apply within the platform. `scripts/check-layering.sh` checks both
fine layers and areas; `scripts/check-binary.sh` checks that the Host dependency
list contains no product package. Only `host/server/**`, `products/**` and
`cmd/simple-hack` may import `host/extension`. The build-time inventory and SQL
ownership tools have explicit access to both areas for inspection.

A full product owns its HTTP pages, data, AI tools, settings, docs and jobs. It
contributes them through one `extension.Extension`, rather than registration at
init time or a plugin framework. The [extension definition](../../host/extension/extension.go)
and [Hack implementation](../../products/hack/extension.go) are the source of the
following inventory. Interfaces belong to the platform consumers, use platform
types and receive the caller's transaction where the workflow requires one.

### Composition and contribution points

| Point | Platform caller and purpose |
|---|---|
| `Name`, `ValidateProduct` | `host/server/product.go` identifies the program and validates product evidence before explicit adoption of an existing database. Hack checks that its events table exists. |
| `Migrations`, `MigrationOrder` | `host/server/migrate.go:migrationSet` composes platform and product catalogues, preserving the frozen combined order. |
| `CheckSchema` | `host/server/migrate.go:checkProductSchema`, called by migration verification and `server.Build` after the platform schema check. |
| `Config.Settings`, `Config.Load` | `host/server/build.go:Build` loads product settings before database setup and merges their metadata for admin configuration; `host/server/settings.go` also lists them for the CLI. |
| `Edition` | `server.Build` constructs fixed product policy after schema verification; nil uses `edition.Default`. |
| `Early` | `server.Build` calls it after edition construction and installs `EarlyHooks.Accounts` with `accounts.SetGroupHooks` before the first accounts service. Services read those hooks at call time. |
| `Assets` | `server.Build` installs files and presentation hooks on the site handler before registering shared pages, then invokes `Assets.Routes` on the mux. See the asset hooks below. |
| `Gate` | `server.Build` constructs product middleware before the connector; requests pass through it inside `LegacyStorageCutoffGate` and outside `ScopeGate`. Hack rejects retired legacy storage routes here. |
| `Wire` | `server.Build` calls it after connector registration. It registers product routes and returns `Parts`; an error aborts construction. |
| `Parts.Tools` | `server.Build` calls `ConnectorHandler.SetTools`; `host/handler/connector.go` passes the contribution to each `host/mcp` server. |
| `Parts.Workers` | `host/server/workers.go:registerWorkers` retains product `workers.Owner` values alongside platform owners for startup, status and shutdown. |
| `Parts.SiteSweep` | `SiteHandler.SiteWorkers` starts the named `handler.GroupSweep`: `groupSweepPass` calls `Sweep`, platform suspension-marker sync every tenth pass, then `After`. Hack's after-pass work requests certificates and settles entries. |
| `Parts.ProductMetrics` | `server.Build` calls it when constructing the paper collector; Hack installs its snapshots, footprint tables and run limits. |
| `papermetrics.ProductCollector` (installed by `Parts.ProductMetrics`) | `host/app/operations/paper_worker.go` calls snapshot, refresh and flush lifecycle methods; `host/papermetrics` calls its request wrapper, histories, daily metrics, run definition and export callbacks. |
| `Parts.PaperRecorder` | `server.Build` wraps the served handler when paper metrics are enabled; Hack adds its request/site recording. |
| `StorageNoticeGroup` | `host/server/migraterules.go` passes it to `app/storage.ListNoticeRecipients` for the operator's recipient report. It labels holding accounts; it sends nothing by itself. |

### Hooks and their callers

`EarlyHooks.Accounts` has three hooks; `Parts.Hooks` has five. `server.Build` installs
the latter on the existing site handler before serving.

| Hook | Caller and behavior | Hack implementation |
|---|---|---|
| `accounts.GroupKeys` (`Accounts.Keys`) | `host/app/accounts/service.go`, `access.go` and `connector.go`: credential binding, live identity, write refusal, scoped file-link access and messages. `host/auth.ScopeGate` asks the accounts service per request. | `products/hack/repository/hackevents.GroupKeys` |
| `accounts.ConnectorTarget` (`Accounts.Target`) | `host/app/accounts/connector.go` and `group_hooks.go`: read/change a connected app's selected group site and label its connections. Selection lives in product data, not the old grant column. | `products/hack/repository/hackteams.ConnectorTarget` |
| `accounts.Erasure` (`Accounts.Erasure`) | `host/app/accounts/account_lifecycle.go` and `ops_account_delete.go`: holding-account checks and wording; deletion `Block`, `Before`, `Rows`, `After` in the same transaction, then `Sweep` after commit. | `products/hack/app/members.AccountErasure` |
| `sites.GroupGate` (`Hooks.GroupGate`) | `host/app/sites` deploy/create/update and lifecycle paths call `Check`; `deleted.go` calls `Suspension` under the restore transaction. These preserve membership/deadline write checks and product take-downs on restore. | `products/hack/app/teams.SiteGate` |
| `notifications.GroupSites` (`Hooks.Notifications`) | `host/app/notifications` resolves held sites, checks membership, locks the member before the site when creating a subscription, and enriches notification labels on the same database/transaction handle. `host/handler` also uses it for group notification addresses. | `products/hack.NotificationGroups` |
| `sites.SitemapFilter` (`Hooks.Sitemap`) | `host/app/sites` sitemap reads in `sitemap.go` filter candidates before emitting the platform sitemap. | `products/hack/repository/hackevents.FilterSitemapSites` |
| `handler.GroupHosts` (`Hooks.GroupHosts`) | `host/handler/grouphost.go` and host dispatch: group lookup by account/slug, site existence for TLS, custom website or product page, pinned-version retention and group wording. Shared routing supports both nested and flat team hosts. | `products/hack/hackhttp.NewGroupHosts` |
| `storage.BindingProvider` (`Hooks.Storage`) | `host/handler` resolves owner-on-site through the provider; `host/app/storage/bindings.go` validates the binding schema and `rules_decide.go` obtains trusted request-time values. An owner lookup error does not grant ownership. | `products/hack/app/storagebind.Provider` |

Nil hooks retain the platform behavior; they do not query product data. A missing
keys hook cannot authenticate stored group keys; no erasure hook means no product
holding accounts; no storage hook leaves the platform binding provider alone.
For editions requiring team keys or event owners, `Build` rejects a missing site
gate or storage provider. Hook calls preserve statement positions, transactions,
lock order and errors; a callback is not permission to weaken a platform check.

There is one narrow platform product-table probe, in
`host/server/product.go:productEvidence`, used only by install validation. It asks
whether `public.events` exists and, if it does, whether it contains any rows.
This read-only exception prevents Host from adopting an existing Hack database;
`scripts/split/sqlcheck/scan.go:productProbeAllowlist` records the precise exception. Every other
product query belongs to the product.

### Asset and AI-tool callbacks

| Contribution | Caller |
|---|---|
| `Assets.Replace`, `Own`, `Files`, `Skills` | `host/handler`'s asset pipeline. `Replace` is authoritative with no fallback for a missing replacement. `Own` files answer over HTTP only where the product mounts `AssetServices.ProductFiles()` through `Assets.Routes`. |
| `Assets.Rewrite` | `host/handler/basetext.go` calls the product rewrite in the shared page/text pipeline. |
| `ProductAssetHooks.GroupTexts` | `host/handler/grouphost.go` reads group error-page wording when group services are absent, including Static. |
| `ProductAssetHooks.InstanceText` | `host/handler` chrome, docs, discovery, auth text, skills and instance-text paths call `productInstanceText`. |
| `ProductAssetHooks.SkillAvailable`, `CatalogPlugin` | `host/handler/skillshub.go` filters skills and supplies the catalogue's plugin name. |
| `ProductAssetHooks.ExpandConnectSteps`, `PersonalPresentationHTML` | `host/handler/chrome.go` expands connect steps and adapts personal presentation. |
| `ProductAssetHooks.RewritePluginZip` | Declared in the contract, but never read by the platform; product archive routes call the product's ZIP rewrite directly. |
| `mcp.Contribution.Families`, `Notifications`, `SiteVariants`, `Instructions` | `host/mcp` composes the registry, notification tools, site variants and instructions. Shared discovery/listing filters remain platform-owned. |
| `mcp.Contribution.Rewrite`, `StorageError`, `NoTarget`, `MutationNames` | `host/mcp` calls product wording/result hooks and retired-mutation refusals. `NoTarget` receives the platform-computed `listable` boolean, not caller data. |
| `mcp.Contribution.Apps` | `host/mcp` constructs a contribution per server with `AppContext`; its app callbacks supply screens, resources, gating, resource reads, the app-only family and the sites-page script/name. |

See [frontend](frontend.md) for API/feature composition, archives and generated
assets, [persistence](persistence.md) for schema ownership and install identity,
and [the import contract](target.md#2-package-map-and-import-contract) for the
checks and their limits.

## Light products

A light product is a set of files the same platform program would load: templates,
row rules, a skill and text (decision 4). Templates provide its pages; row rules
express access using existing platform facts; the skill teaches an agent how to
use it; text supplies its wording. It has no Go build or separate program.
The loader, file format and packaging are left to the first real light product;
this design does not introduce a manifest or registration framework.

It can use only the identities and facts the platform already offers: visitors,
the site owner and rows belonging to the signed-in person (decision 5). It cannot
invent trusted roles, membership or background work. Files supplied by a site or
product cannot assert new trusted authority. Hack's server-supplied team,
organiser, stage and deadline bindings are full-product code, not a capability a
light product can create with a template or rule.

One trainer whose clients each see their own records fits this boundary. An
organisation with many trainers, assignments and shared membership does not. That
use would need an added platform ability or a small code product, decided when
it is real. Travel and trainers are future products, not installed examples.

not built

## Add-ons

An add-on is an optional capability separate from products (decision 16). It can
run in its own process behind the same front door, or inside the program when it
needs no isolation. The secrets service will run in its own process. A future
worker add-on is distinct from the in-process lifecycle owners already registered
in `host/server/workers.go`.

The `addons/` area is created with the first add-on. Add-ons and products do not
import each other; shared capabilities belong in the platform. No add-on process,
loader, API or directory is introduced now. A separate process does not imply a
new account system or a plugin framework.

The separate Enterprise repository remains frozen. Its planned return is an
organisation pack of add-ons: object storage for site files first, then
company-only sign-in, audit log, access levels and teams, and admin tooling. That
pack is future work. The refactor's storage-interface work prepares for an object
backend; it does not implement that backend or the Enterprise pack.

not built
