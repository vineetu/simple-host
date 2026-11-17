# Target architecture

## 1. Purpose and status

The platform/product boundary described here is implemented through split slice D3-S8.
It follows the owner decisions of 2026-11-12 in [INTENT.md](../../INTENT.md), which
supersede the earlier single-program cleanup plan. [ARCHITECTURE.md](../../ARCHITECTURE.md)
is the current runtime map. [Products and add-ons](products.md) records the actual
extension points and the designs for light products and add-ons; those two designs
have no implementation yet. Day 4 work, including the separate Static page server,
is outside this boundary's completion claim.

## 2. Package map and import contract

One repository has three areas: the platform in `host/`, full products in
`products/`, and add-ons in `addons/` once the first one is built. There is no
`addons/` directory yet. There is one Go module except for the rules engine's
nested module at `host/storage/rules`.

```text
cmd/simple-host/             platform-only entry point
cmd/simple-hack/             platform + Hack entry point
host/
  server/                   CLI dispatch, composition, startup and shutdown
  extension/                product description in platform types
  handler/ auth/ mcp/ httpx/ shared HTTP, credentials, MCP and protocol helpers
  app/                      accounts, sites, storage, legacy, notifications, operations
  repository/               area SQL and sqlkit transaction primitives
  model/ rules/             shared values and pure decisions
  config/ edition/          platform settings and fixed instance policy
  db/                       platform schema and migration catalogue
  storage/                  local deployments, markers, SQLite and runtime files
    rules/                  rules engine (nested Go module)
  webassets/                platform pages, docs, skills and downloads
  workers/                  lifecycle and status contracts
products/hack/
  extension.go              one description: Extension()
  hackhttp/ mcp/             product HTTP, AI tools and screens-in-chat
  app/ repository/ rules/   product workflows, SQL and decisions
  model/ config/ edition/   product records, settings and policy
  db/                       product schema and migration catalogue
  webassets/ papermetrics/  product assets and research measurement
  servertest/ testdb/        product integration fixtures
```

Runtime imports point down: platform code imports only platform code; a product
imports the platform and itself. Products and add-ons never import each other.
The platform calls product implementations through interfaces or function fields
using platform types, declared by the consuming platform package and assembled by
`host/extension`. There is no plugin framework or init-time registration.

The enforced Go import contract is [scripts/layering/rules.txt](../../scripts/layering/rules.txt),
checked by `scripts/check-layering.sh`. Both passes must allow an edge:

- The fine rows (`[final]`, with `[test]` additions for tests) preserve the existing
  handler → app → repository/rules separation. Matching rows within a pass are
  unioned; there is no broad platform row granting every platform dependency.
- The independent `[areas]` pass prevents platform-to-product imports, including
  test imports. Only `host/server/**`, `products/**` and `cmd/simple-hack` may
  import `host/extension`. The current product rows cover `products/hack/**`.
  The build-time inventory and SQL ownership tools have explicit rows permitting
  inspection of both areas; they are not runtime dependencies.

`[transitional]` is empty and a new transitional edge fails the check. Platform
integration tests use `host/server/servertest` from `package server_test` when
needed; tests requiring Hack fixtures live under `products/hack/`. The nested
rules module is checked separately. The package list for `cmd/simple-host` must
contain no package under `products/`; the binary string scan is supporting
evidence, not the proof of that boundary.

The other boundary gates check platform names, SQL ownership and embedded public
text, including ZIP/gzip members. Existing name/text matches have narrow reasoned
allowlists in `scripts/allow/`; passing those scans does not mean every historical
product reference has been removed.

## 3. Programs, policy and install identity

`cmd/simple-host` passes no extension to `server.Run`; `cmd/simple-hack` passes
`hack.Extension()`. A full product is chosen by that build, with at most one full
product per install. Each install has its own accounts, Postgres database and file
root. `EVENTS` no longer selects a product: the Host program refuses `EVENTS=hosted`,
while the Hack program accepts it or an unset value.

`server.Build` uses `edition.Default(cfg.Limits)` for the platform, or the product's
`Extension.Edition` factory. Policy is injected before constructing services and
normal routes. Product settings use `ConfigSpec.Settings` and `ConfigSpec.Load`;
Hack's typed limits live under `products/hack/config`, beside platform limits.
Membership, stages, deadlines and site protection remain live facts, not policy
frozen in an edition.

`install_product` records the install's product. Startup and changing migration
commands check it before writes; adopting a populated database without a record
requires explicit `migrate -record-product`. A fresh install uses
`migrate -init -record-product`. See [install identity](persistence.md#install-product-identity).

## 4. Workflows and data ownership

HTTP and MCP reach shared application services; pure rules decide and repositories
execute SQL. Hack's transports, workflows, records and queries live under
`products/hack/`. Shared site, account and storage workflows ask the product at the
[documented hooks and callers](products.md#hooks-and-their-callers), preserving the
caller transaction and lock order. Holding-account status comes from the product
hook; `users.event_account` is an account-that-does-not-sign-in flag, not evidence
that an account holds an event.

`host/db` owns the platform schema and migrations; `products/hack/db` owns Hack's.
Fresh platform installs create no Hack tables. Existing databases keep unused
product tables and migration history; table names are unchanged. The composed
catalogue preserves one combined migration order. Platform runtime SQL does not
query product tables except for the narrowly allowlisted adoption-evidence probe
in `host/server/product.go:productEvidence`: it checks for `public.events` and,
only when present, whether it contains rows, so Host cannot adopt a Hack database.
Hack supplies its own evidence through `Extension.ValidateProduct`.

Application errors retain existing codes, response fields and precedence. A package
boundary is not permission to change a workflow's observable behavior. See
[Hack domain](hack-domain.md) and [persistence](persistence.md) for the detailed state
and data contracts.

## 5. Transactions, locks and workers

Application services own transactions. Repositories use `repository/sqlkit.Querier`
and receive the same `*sql.Tx` where locks or several statements belong to one
workflow. Product hooks keep their statement position inside that transaction;
result rows close before the caller issues another statement. Cross-service work
uses the existing explicit transaction path, not a nested transaction or a generic
unit-of-work framework.

Preserve the actual flow's ordering: event before membership/team locks in Hack;
account-file mutex before site mutex for deploys; sorted site mutexes for rename;
site before team when restoring a held site. New deploys commit metadata before
promoting `current`; active-version selection switches `current` before commit
and compensates on commit failure. Runtime files and SQLite have their own
consistency boundaries. Postgres and disk are not one atomic transaction. The
[persistence guide](persistence.md) records those differences.

Every background loop has an owner implementing `workers.Owner`. The composition
root registers enabled owners in `host/server/workers.go` and exposes their status
through the admin usage report. Product workers arrive through `Parts.Workers`;
the product site sweep runs around platform marker reconciliation in its existing
order. Shutdown drains HTTP, then stops and joins workers before closing their
dependencies, with a separate worker context. Tests that start workers cancel and
join them before database cleanup. These are in-process workers, not the future
worker add-on.

## 6. HTTP, MCP and inventories

Keep one `http.ServeMux`. `Extension.Wire` registers product routes and returns the
parts the platform installs before listening. `Extension.Gate` supplies the
product middleware between the platform legacy cutoff gate and scope gate.

The platform owns MCP transport, registry, discovery, dispatch and common tools.
Hack supplies one `mcp.Contribution` with its declarations, instructions, variants,
result hooks and per-server apps factory. MCP's REST replay retains the existing
authentication, scope, origin and other route checks; a listing filter is not
authorization for a direct call. [Generated route and tool inventories](../generated/)
record the two products; OpenAPI remains the request/response contract.

## 7. Assets and generated documentation

Platform embeds live in `host/webassets`; Hack embeds live in
`products/hack/webassets`, including its skill archives and compiled docs.
`extension.Assets.Replace` lists platform names replaced by the product; a missing
listed replacement never falls back to platform bytes. `Own` lists product-only
names, served only at routes mounted by the product through `AssetServices.ProductFiles`.
The shared asset pipeline still supplies chrome, headers and instance rewriting.

Hack's handwritten browser modules and MCP bundle generator live under the product.
Generated bundles, API contracts, features pages and plugin copies are regenerated
from their established sources. The Host OpenAPI source composes with Hack's
`openapi.part.yaml`; Hack's features part supplies explicit structural changes.
See [frontend](frontend.md) for source/output paths and commands.

## 8. Light products and add-ons

The [light-product design](products.md#light-products) is files loaded by the same
platform program; the [add-on design](products.md#add-ons) allows a separate process
or an in-program component. Both are designed, not built. Neither adds a loader,
new trusted roles, an add-on directory or a service as part of this refactor slice.

not built

## 9. Where to add a feature

- Platform capability: use `host/app`, its rules/repositories and shared HTTP/MCP
  adapters. Keep compatibility work in `host/app/legacy`.
- Hack capability: use `products/hack/hackhttp` or `products/hack/mcp`, with product
  app/rules/repository code and shared platform services. Add a hook only at a real
  platform caller that needs product knowledge.
- Migration: update the owning area's schema and add its next assigned numbered
  migration. Keep frozen filenames and the combined catalogue order; see
  [contributing](contributing.md) and [persistence](persistence.md).
- Worker: choose its owner, inject dependencies, register it through the composition
  root, and retain cancellation, joins and status reporting.
- Light product or add-on: follow the limits in [products.md](products.md); implement
  the design only when the first real use is agreed.

Follow `CLAUDE.md` for documentation and checks. Update the current map when
ownership changes, and regenerate derived assets instead of hand-editing copies.
