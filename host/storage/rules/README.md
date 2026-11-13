# Row rules engine

This is the standard-library-only nested module for row rules. It performs no
I/O and reads no clock or global state. Import it as `ruleengine` to distinguish
it from `internal/rules/storage`. Run `make test-rules` from the repository root,
or `go vet ./... && go test ./...` here. No `go.work` is needed.

R1-S1 supplies typed values, schemas, strict JSON policy decoding, the bounded
parser and table-independent validation. No request path enforces these rules
yet. `Compile` returns `not built yet`; `Plan` is a declaration for R1-S2a.
`Convert` converts nothing and reports `not built yet` until R2-S6.

Expressions support `&&`, `||`, `!`, equality, `in`, `contains`, null tests and
ASCII `lower`. Bare names and `row.name` name columns; other qualified names
name declared bindings. Validation checks binding types and placement without
guessing the columns of a table that does not yet exist. Permissive edit,
delete and all policies require an identity guard in every using alternative.

Default bounds are 2,048 expression bytes, 256 tree nodes, depth 32, 64 list
items, 32 policies and 64 bytes per policy name. Nodes include operands,
operators, lists and explicit parentheses; depth includes parentheses. The
parser checks nesting before recursing. The future compiler also has bounds
of 8,192 predicate bytes and 256 predicate arguments; these are not parser
limits and are tested with the compiler in R1-S2a.

Policy JSON decodes to version 1 and keeps expression text exactly as supplied.
Encoding a decoded set and decoding it again preserves that set. JSON shape
errors have position -1; expression errors use zero-based byte offsets.

The root layering gate checks all engine imports, including tests and files
for other build targets, and checks that CI and both releases explicitly run
the nested module's tests. Root `go test ./...` alone does not run them.
