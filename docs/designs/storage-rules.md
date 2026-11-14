# Storage row rules

Status: built for activation with R4-S2. SQLite and key-value data use row rules;
file buckets keep access presets. Existing SQLite and key-value presets work
until the announced switch. This document is the language reference.

## Resource and policy sets

The owner saves `rules` through `storage_set_resource` or
`PUT /v1/sites/{site}/storage/resources/{name}`. Follow the tool description
returned by this server: if it does not accept `rules`, the server is older and
its description says what to send. A key-value namespace must already live in
SQLite before it accepts rules. Rules cannot be saved on a file bucket.

```json
{"kind":"sqlite","rules":{"version":1,"policies":[
  {"command":"read","using":"visitor_id == auth.id || auth.role == \"owner\""},
  {"command":"add","check":"auth.signed_in"},
  {"command":"edit","using":"auth.role == \"owner\""},
  {"command":"delete","using":"auth.role == \"owner\""}
]}}
```

This is a bookings database: people add bookings and see their own; the owner
reads and manages all. Owner tools bypass row rules. An owner signed in on the
site uses `auth.role == "owner"` under the same rules as other page callers.
That grants storage data access only, on the site's own address; framed pages
and old-version previews never gain owner rights.

A set has `version: 1` and `policies`. A policy has optional `name`, `command`,
`restrictive` (false by default; true makes it restrictive), and clauses:

| Command | `using`: stored row | `check`: written row |
|---|---|---|
| `read` | required | refused |
| `add` | refused | required |
| `edit` | required | optional; inherits `using` when absent |
| `delete` | required | refused |
| `all` | required for read, edit, delete | optional; inherits `using` for add and edit |

For each command, permissive policies combine with OR, restrictive policies
with AND, and the two groups combine with AND. Without an applicable permissive
policy access is denied. An empty policy list denies all page callers, including
owner-on-site. `all` is a command, not an automatic grant.

SQLite `tables: {"bookings": {"rules": {...}}}` replaces the default set for
that table; it does not add to it. Omit `tables` to keep existing table sets;
send `{}` to remove them. When converting a resource with table presets, send
rules for each table or `tables: {}`. Do not mix rules and preset fields in the
same resource save or table entry. A rules-only table entry needs no preset.
Saving only `site_passcode` on a rules resource keeps its rules.

## Language

Expressions use `true`, `false`, `&&`, `||`, `!`, parentheses, comparisons
`==` and `!=`, `in`, `contains`, `is null`, `is not null`, and `lower()`.
Ordering operators, SQL, functions other than `lower`, and comments are refused.

```ebnf
expression = orExpr ;
orExpr     = andExpr { "||" andExpr } ;
andExpr    = notExpr { "&&" notExpr } ;
notExpr    = "!" notExpr | primary ;
primary    = "(" expression ")" | comparison | binding | "true" | "false" ;
comparison = operand ( "==" | "!=" ) operand
           | operand "in" list
           | operand "contains" operand
           | operand "is" [ "not" ] "null" ;
operand    = string | number | "true" | "false" | column | binding
           | "lower" "(" operand ")" ;
list       = "[" literal { "," literal } "]" | binding ;
literal    = string | number ;
column     = identifier | "row" "." identifier ;
binding    = identifier "." identifier ;
```

Identifiers are ASCII `[A-Za-z_][A-Za-z0-9_]{0,63}`. Keywords are lower case
and case-sensitive: `true false null in contains is not lower row`. Use
`row.in` for a column named `in`. Bare columns match the actual schema
case-insensitively. `row.name` always names a column; other dotted names must
be declared trusted bindings. An unknown column, binding or namespace is an error.

Strings use single or double quotes and JSON-style escapes, including escaped
quotes and Unicode surrogate pairs; they must be valid UTF-8, contain no NUL
and have no raw newline. Numbers follow JSON syntax: plain integers fitting
int64 stay integers; fractions, exponents and larger integers become finite
float64 values. `-0` is integer zero. Whitespace is space, tab, CR or LF.

Boolean positions accept comparisons, boolean bindings and boolean literals,
not bare columns, strings or numbers. Equality compares a column with a literal
or binding, or bindings with compatible scalar types. Two columns, two
literals, chained comparisons and comparison operands containing a parenthesised
expression are refused. Use `is null`, never `== null`.

`in` accepts a column or text/number binding on the left and a nonempty list
of up to 64 strings or numbers, all one type, or a text-list binding on the
right. `contains` accepts text operands. `lower()` accepts a column, text
binding or text literal, lowers ASCII only, and cannot be nested. Null tests
accept columns or nullable bindings, not literals or non-nullable bindings.

Comparisons use stored SQLite types after column affinity. Text is compared
as bytes, case-sensitive even on a NOCASE column; numbers compare only with
integer/real storage, not numeric text. Booleans against columns are 1 and 0.
Blobs never equal anything. Integer/real comparison follows SQLite numeric
comparison, preserving integer precision. `contains` tests a byte substring;
an empty substring matches any text. Binding-only conditions are evaluated
using the request's trusted values.

Negation is pushed into comparisons before evaluation. A comparison with a
NULL or wrongly typed operand is false **in both polarities**: for NULL,
`status == "open"` and `!(status == "open")` are both false. Only explicit
null tests handle NULL. An empty text-list binding makes `in` false and its
negation true only for text. AND and OR are ordinary two-valued operations.

## Trusted facts and writes

The platform supplies `auth.id` (nullable visitor identity), `auth.signed_in`
and `auth.role` (`anon`, `visitor` or `owner`), `auth.ai` (visitor-AI caller)
and `auth.teams` (empty text list on the platform). `auth.checked` exists only on
installs with the spam check configured, and only in an add policy's `check`.
Products may declare additional trusted bindings; site data never supplies them.

Every alternative in the `using` of a permissive `edit`, `delete` or `all`
policy must include a positive top-level AND term: `auth.signed_in`,
`auth.signed_in == true`, `visitor_id == auth.id`, or `auth.role == "owner"`
(equality may be reversed and columns may use `row.`). A guard only in
`check`, under negation, or in just one OR branch is insufficient.

Edit and delete require read visibility of the stored row as well as their
own `using`. Add checks the stored inserted row; edit checks the stored edited
row before commit. Failed checks roll back. An edit may make its row unreadable;
the response contains only a change count. Hidden and missing rows return the
same 404, including a failed edit check. Lists filter before paging. Foreign
keys must reference a row readable under the parent table's effective set.
Visitor-AI edits and deletes stay limited to that visitor's own rows.

Identity-dependent permissive policies require an active account: a suspended
visitor is refused with `account_suspended`. A permissive read of `true` still
allows that visitor to read what anyone reads, even if a restrictive policy
mentions identity. Bindings are read again inside the site slot before building
the statement; a provider failure denies the request.

Visitors never send SQL. Pages use `table(t).list/get/add/edit/delete` and
the corresponding fixed REST routes. The server stamps author and timestamps;
visitors cannot supply primary keys or change server-owned columns. Owner tools
retain their separate SQL console. On Host, `for` is accepted only when every
permissive read alternative requires the row's author or owner, with at least
one author alternative; it marks the row `written_by: "owner"` for an already
signed-in person.

Idempotent write replay checks current rules and bindings before returning the
stored response. If those make the command impossible it is refused. Row-dependent
conditions are not reevaluated on a replay; the stored response contains no row
data and no write repeats.

## Examples

Owner-maintained public data uses a read policy `using: "true"`, and an `all`
policy `using: "auth.role == \"owner\""`. An owner-only inbox uses that
owner policy plus `add` with `check: "true"`. For personal editable notes, use
`all` with `using: "visitor_id == auth.id || auth.role == \"owner\""` and
`check: "auth.signed_in"`.

For moderated comments, combine permissive public read, checked add and owner
edit/delete policies with a restrictive read policy:

```json
{"command":"read","restrictive":true,"using":"auth.role == \"owner\" || approved_at is not null"}
```

The **approval stamp** applies only when the effective set references
`approved_at` and that column exists as TEXT. Rules saves add the column when
needed. Visitor adds store NULL; owner-on-site and owner-tool adds stamp the
current time (owner tools may supply a value). Only owner tools and owner-on-site
may edit it. The rule above hides held posts even from their author; the owner
approves through `.edit(id, {approved_at: new Date().toISOString()})`. A leftover
column that no rule names is an ordinary editable column and stamps nothing.

KV rules can name `key`, `value`, `visitor_id`, `created_at`, `updated_at`;
`value` is normalized JSON text, not a queryable JSON object. For example,
`key contains "public/"` can limit a read. A set on a missing key is an add;
on an existing key it is an edit. Edits keep the original author.

## Disclosures, status and transition

A global UNIQUE conflict can reveal that a hidden row exists. A key-value
write by someone allowed to attempt an add can reveal that a key exists
through `key_exists`, even when the key is unreadable. Use opaque random keys
instead of sensitive values. These are existing disclosures, not private-key
namespaces. Reads and deletes keep hidden equal to missing.

The owner's Web Push rule grants its chosen column to the row's author,
regardless of read rules at write or delivery time. Other visitors never receive
that push; queued delivery can survive row deletion or a rules change.
Owner-created webhooks and connected-app events retain their full fields and
carry `preset: ""` for rules resources.

`rules_status` is `preset`, `rules` or `invalid`. Rules responses include
`rules`, `rules_errors` and table sets, and omit preset/access fields. A schema
or binding change can invalidate rules: page requests fail with `rules_invalid`
while owner tools can still repair the resource. A bad save is `invalid_rules`
with a `rule_error` position and clause; rules off or a KV namespace still on
Postgres gives `rules_unavailable`. Saved rules are enforced even when the
accept-new-rules flag is later turned off.

Limits: 256 KiB per save, 50 table sets, 32 policies per set, 64-byte policy
names, 2,048-byte expressions, 256 expression nodes, depth 32, and 64 list
items. A composed predicate is at most 8,192 bytes and 256 arguments;
whole statements are at most 32,768 bytes and 32,766 parameters.

Key-value data uses one SQLite file **per namespace** under the site. PostgreSQL
remains the JSON normalizer. Cutover deletes the moved namespace's Postgres rows
when its store marker changes. Exports, attribution, notifications and quotas
continue across the move. The operator's maintenance commands require the server
stopped and the data-directory exclusive lock. `migrate-rules` is operator-only,
including on self-hosted installs; site owners are not offered a conversion tool.

Older programs see deny-visitor fill values on a rules resource, including for
owner-on-site. Saving through that older program overwrites the fill and can
serve the sent preset; a rules-aware program then sees invalid state. The fill
is a safety net, not a rollback guarantee: the oldest-safe-program floor still
applies. Before the hard switch unconverted resources retain their presets;
file buckets retain them before and after it. Recipe topic names stay the same.
