#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
here=$PWD
tmp=$(mktemp -d)
trap 'chmod -R u+w "$tmp"
 rm -rf "$tmp"' EXIT
export TMPDIR="$tmp"
export GOFLAGS=-p=2
script="$here/move.sh"
fresh() {
 rm -rf "$tmp/repo"
 mkdir "$tmp/repo"
 cp -R "$here/testdata/move/." "$tmp/repo/"
 cd "$tmp/repo"
 git init -q
 git add .
 git -c user.name=Fixture -c user.email=fixture@example.com commit -qm fixture
 cat > "$tmp/map" <<'EOF'
internal/storage host/storage
internal/* host/*
db host/db
EOF
}
run() {
 bash "$script" --map "$tmp/map" > "$tmp/log" 2>&1
}
commit() {
 git add .
 git -c user.name=Fixture -c user.email=fixture@example.com commit -qm moved
 }
snapshot() {
 python3 - <<'PY'
import hashlib
import os
h=hashlib.sha256()
for root,dirs,files in os.walk('.'):
 dirs[:]=sorted(d for d in dirs if d!='.git')
 for f in sorted(files):
  p=os.path.join(root,f)
  h.update(p.encode())
  h.update(open(p,'rb').read())
print(h.hexdigest())
PY
}

plain() {
 fresh
 nested=$(sha256sum host/storage/rules/*)
 go run ./scripts/layering
 run
 go run ./scripts/layering
 go build ./...
 go vet ./...
 go test ./...
 test "$nested" = "$(sha256sum host/storage/rules/*)"
 grep -q '=> ./host/storage/rules' go.mod
 test "$(cat inventory.txt)" = host/alpha/alpha.go
 grep -q '"host/alpha/alpha.go:Value"' scripts/inventory/dynamic.go
 grep -q 'gofmt -w host/alpha host/storage host/db/' Makefile
 grep -q 'example.com/fixture/host/buildinfo.Version' Dockerfile
 grep -q 'paths: \[host/alpha, host/db/schema.sql\]' .github/workflows/ci.yml
 grep -q '\$PWD/host/alpha/data.txt' scripts/check.sh
 grep -q '../../host/alpha/data.txt' scripts/tests/x.test.mjs
 test "$(cat host/storage/existing.txt)" = keep
 cat > "$tmp/expected" <<'EOF'
# Paths are relative to internal/; @/ prefixes module-root paths.
[final]
@/cmd/app -> alpha @/host/db
{alpha,storage} -> storage !buildinfo @/host/storage/rules
buildinfo -> -
[test]
alpha -> @/host/db
[transitional]
alpha -> storage test  # removal step fixture
EOF
 diff -u "$tmp/expected" scripts/layering/rules.txt
}
dynamic_failure() {
 fresh
 sed -i 's@internal/alpha/alpha.go:Value@internal/alpha/missing.go:Value@' scripts/inventory/dynamic.go
 commit
 before=$(snapshot)
 if run
 then
  return 1
 fi
 test "$before" = "$(snapshot)"
 test -z "$(git status --porcelain)"
 grep -q 'dynamic key missing' "$tmp/log"
}
late_reference() {
 fresh
 run
 commit
 mkdir -p cmd/third
 printf 'package third\nimport _ "example.com/fixture/internal/alpha"\n' > cmd/third/third.go
 git add cmd
 run
 test "$(git diff --name-only)" = cmd/third/third.go
 grep -q 'example.com/fixture/host/alpha' cmd/third/third.go
 go build ./...
}
late_file() {
 fresh
 run
 commit
 mkdir -p internal/alpha
 echo 'package alpha' > internal/alpha/late.go
 git add internal
 run
 test -f host/alpha/late.go
 test ! -d internal/alpha
}
collision() {
 fresh
 run
 commit
 mkdir -p internal/alpha
 for f in one two
 do
  echo old > "internal/alpha/$f.txt"
 echo new > "host/alpha/$f.txt"
 done
 git add .
 before=$(snapshot)
 status=$(git status --porcelain)
 if run
 then
  return 1
 fi
 test "$before" = "$(snapshot)"
 test "$status" = "$(git status --porcelain)"
 grep -q 'collision: internal/alpha/one.txt' "$tmp/log"
 grep -q 'collision: internal/alpha/two.txt' "$tmp/log"
 cp host/alpha/one.txt internal/alpha/one.txt
 cp host/alpha/two.txt internal/alpha/two.txt
 git add .
 run
 test ! -e internal/alpha/one.txt
}
nested_move() {
 fresh
 echo local > host/storage/rules/local.txt
 echo 'host/storage/rules modules/rules' > "$tmp/map"
 before=$(cat host/storage/rules/rules.go)
 run
 test -f host/storage/rules/local.txt
 test ! -e modules/rules/local.txt
 test "$before" = "$(cat modules/rules/rules.go)"
 grep -q '=> ./modules/rules' go.mod
 grep -q 'example.com/fixture/host/storage/rules' internal/storage/store.go
 go build ./...
}
longest() {
 fresh
 echo 'package host' > internal/storage.go
 git add internal/storage.go
 sed -i 's@internal/storage host/storage@internal/storage special/storage@' "$tmp/map"
 run
 test -f host/storage.go
 test ! -f special/storage.go
 test -f special/storage/store.go
 test -f host/alpha/alpha.go
 go build ./...
}
idempotent() {
 fresh
 run
 before=$(snapshot)
 status=$(git status --porcelain)
 run
 test "$before" = "$(snapshot)"
 test "$status" = "$(git status --porcelain)"
 grep -qi 'nothing changed' "$tmp/log"
}
dry_run() {
 fresh
 before=$(snapshot)
 bash "$script" --map "$tmp/map" --dry-run > "$tmp/log"
 test "$before" = "$(snapshot)"
 test -z "$(git status --porcelain)"
 grep -q 'MOVE internal/alpha/alpha.go -> host/alpha/alpha.go' "$tmp/log"
}
file_wildcard() {
 fresh
 server_dir=internal/server
 command_dir=cmd/server
 cat > "$tmp/map" <<EOF
$command_dir/hack_extension.go products/hack/extension.go
$server_dir/hack_*_test.go products/hack/servertest/
$server_dir/transport_fixture_test.go products/hack/servertest/transport_fixture_test.go
EOF
 sed -i "s@internal/alpha/alpha.go:Value@$server_dir/hack_one_test.go:TestOne@" scripts/inventory/dynamic.go
 run
 grep -q 'products/hack/servertest/hack_one_test.go:TestOne' scripts/inventory/dynamic.go
 test -f products/hack/extension.go
 test -f products/hack/servertest/hack_one_test.go
 go build ./...
 go test ./...
 assert_rerun
}

layering_and_text() {
 fresh
 cat >> scripts/layering/rules.txt <<'EOF'
[final]
{alpha,storage,buildinfo} -> {alpha,storage,buildinfo}/** !alpha
/** -> storage
EOF
 echo 'internal/alpha products/hack/alpha' > "$tmp/map"
 printf '%s\n' 'internal/{alpha,storage}' 'internal/{alpha}' 'docs/internal/alpha' 'host/db/schema.sql' > refs.txt
 git add refs.txt
 bash "$script" --map "$tmp/map" --text refs.txt > "$tmp/log" 2>&1
 grep -qFx '{storage,buildinfo} -> {storage,buildinfo}/** @/products/hack/alpha/** !@/products/hack/alpha' scripts/layering/rules.txt
 grep -qFx '@/products/hack/alpha -> {storage,buildinfo}/** @/products/hack/alpha/** !@/products/hack/alpha' scripts/layering/rules.txt
 grep -qFx '/** -> storage' scripts/layering/rules.txt
 grep -qFx 'internal/{alpha,storage}' refs.txt
 grep -qFx 'products/hack/{alpha}' refs.txt
 grep -qFx 'docs/internal/alpha' refs.txt
 grep -q 'REVIEW brace token refs.txt' "$tmp/log"
 go build ./...
}
plan_errors() {
 fresh
 echo 'internal/alpha/data.txt elsewhere/data.txt' > "$tmp/map"
 before=$(snapshot)
 if run
 then
  return 1
 fi
 test "$before" = "$(snapshot)"
 grep -q 'embed leaves package' "$tmp/log"
 printf '%s\n' 'internal/alpha target' 'internal/alpha other' > "$tmp/map"
 if run
 then
  return 1
 fi
 test "$before" = "$(snapshot)"
 grep -q 'malformed or duplicate' "$tmp/log"
 printf '%s\n' 'internal/alpha/alpha.go same.go' 'internal/storage/store.go same.go' > "$tmp/map"
 if run
 then
  return 1
 fi
 test "$before" = "$(snapshot)"
 grep -q 'duplicate destination' "$tmp/log"
}
untracked_and_discovery() {
 fresh
 printf '%s\n' 'internal/alpha/local.txt' > .gitignore
 mkdir -p scripts/split
 cp "$script" scripts/split/move.sh
 cp "$here/move_test.sh" scripts/split/move_test.sh
 git add scripts/split
 echo ignored > internal/alpha/local.txt
 echo untracked > internal/alpha/untracked.txt
 run
 cmp "$script" scripts/split/move.sh
 cmp "$here/move_test.sh" scripts/split/move_test.sh
 test -f internal/alpha/local.txt
 test -f internal/alpha/untracked.txt
 grep -q 'LEAVE untracked/ignored internal/alpha/local.txt' "$tmp/log"
 fresh
 echo 'scripts/check.sh tools/deep/check.sh' > "$tmp/map"
 run
 grep -qF 'cd "$(dirname "$0")/../.."' tools/deep/check.sh
 bash tools/deep/check.sh > "$tmp/check-output"
}

assert_rerun() {
 local before status
 before=$(snapshot)
 status=$(git status --porcelain)
 run
 test "$before" = "$(snapshot)"
 test "$status" = "$(git status --porcelain)"
 grep -qi 'nothing changed' "$tmp/log"
}

assert_plan_error() {
 local before status
 before=$(snapshot)
 status=$(git status --porcelain)
 if run; then
  echo 'expected plan error'
  return 1
 fi
 test "$before" = "$(snapshot)"
 test "$status" = "$(git status --porcelain)"
 grep -q "$1" "$tmp/log"
}

root_tokens() {
 fresh
 cat > scripts/refs.txt <<'EOF'
cp db/schema.sql "dist/$package/db/"
$root/db/schema.sql ${REPO_DIR}/db/schema.sql $(pwd)/db/schema.sql $(git rev-parse --show-toplevel)/db/schema.sql
$other/db/schema.sql dist/$ROOT/db/schema.sql x$ROOT/db/schema.sql
$(PWD)/db/schema.sql $(GIT rev-parse --show-toplevel)/db/schema.sql
EOF
 git add scripts/refs.txt
 run
 grep -qFx 'cp host/db/schema.sql "dist/$package/db/"' scripts/refs.txt
 grep -qFx '$root/host/db/schema.sql ${REPO_DIR}/host/db/schema.sql $(pwd)/host/db/schema.sql $(git rev-parse --show-toplevel)/host/db/schema.sql' scripts/refs.txt
 grep -qFx '$other/db/schema.sql dist/$ROOT/db/schema.sql x$ROOT/db/schema.sql' scripts/refs.txt
 grep -qFx '$(PWD)/db/schema.sql $(GIT rev-parse --show-toplevel)/db/schema.sql' scripts/refs.txt
 grep -q 'scripts/refs.txt:3: ' "$tmp/log"
}

destination_tree_errors() {
 fresh
 echo blocked > host/alpha
 assert_plan_error 'destination ancestor is a file'
 fresh
 mkdir -p host/alpha/data.txt
 assert_plan_error 'destination is a directory'
 fresh
 echo blocked > host/alpha
 git add host/alpha
 assert_plan_error 'destination ancestor is a file'
 fresh
 printf '%s\n' 'internal/alpha/data.txt target' 'db/schema.sql target/child' > "$tmp/map"
 assert_plan_error 'planned file is destination ancestor'
 fresh
 echo blocked > host/alpha
 echo 'internal/alpha host/alpha' > "$tmp/map"
 assert_plan_error 'destination ancestor is a file'
}

nested_destination_errors() {
 fresh
 mkdir -p internal/storage/rules
 echo 'package rules' > internal/storage/rules/late.go
 git add internal/storage/rules
 assert_plan_error 'destination inside nested module'
 fresh
 mkdir -p modules/rules
 echo unrelated > modules/rules/local
 echo 'host/storage/rules modules/rules' > "$tmp/map"
 assert_plan_error 'unsupported whole-module'
 fresh
 printf '%s\n' 'host/storage/rules modules/rules' 'internal/storage/store.go modules/rules/incoming.go' > "$tmp/map"
 assert_plan_error 'destination inside nested module'
}

untracked_destinations() {
 for kind in untracked ignored; do
  fresh
  mkdir -p host/alpha
  cp internal/alpha/data.txt host/alpha/data.txt
  if test "$kind" = ignored; then
   echo host/alpha/data.txt > .gitignore
  fi
  assert_plan_error 'untracked file at destination'
 done
}

twin_relative_and_cleanup() {
 fresh
 mkdir -p products/hack/alpha
 cp internal/alpha/alpha_test.go products/hack/alpha/alpha_test.go
 git add products
 echo 'internal/alpha products/hack/alpha' > "$tmp/map"
 run
 grep -q '../../../db/schema.sql' products/hack/alpha/alpha_test.go
 go test ./products/hack/alpha
 test ! -d internal/alpha
 fresh
 mkdir -p twins/old twins/new
 echo same > twins/old/file.txt
 cp twins/old/file.txt twins/new/file.txt
 git add twins
 echo 'twins/old twins/new' > "$tmp/map"
 run
 test ! -d twins/old
}

lexer_and_embed() {
 fresh
 cat > internal/alpha/lexer.go <<'EOF'
package alpha
var Quote = '"'
// an odd " must not open a literal
/* another " */
var Raw = `unrelated " text`
var Relative = "../../db/schema.sql"
// prose //go:embed ../../db/schema.sql
EOF
 # Quoted patterns and spacing stay byte-identical when resolution is unchanged.
 sed -i 's@//go:embed data.txt@  //go:embed "data.txt"   `./data.txt`@' internal/alpha/alpha.go
 git add internal/alpha
 echo 'internal/alpha products/hack/alpha' > "$tmp/map"
 run
 grep -q '"../../../db/schema.sql"' products/hack/alpha/lexer.go
 grep -qFx '// prose //go:embed ../../db/schema.sql' products/hack/alpha/lexer.go
 grep -qF '//go:embed "data.txt"   `data.txt`' products/hack/alpha/alpha.go
 go test ./products/hack/alpha
 fresh
 sed -i 's@//go:embed data.txt@//go:embed "data.txt"   `./data.txt`@' internal/alpha/alpha.go
 cp internal/alpha/alpha.go "$tmp/embed-original"
 echo 'db host/db' > "$tmp/map"
 run
 cmp "$tmp/embed-original" internal/alpha/alpha.go
 fresh
 echo 'internal/alpha/data.txt internal/alpha/renamed.txt' > "$tmp/map"
 run
 grep -qFx '//go:embed renamed.txt' internal/alpha/alpha.go
}

dynamic_disk_validation() {
 fresh
 run
 rm host/alpha/alpha.go
 assert_plan_error 'dynamic key missing file'
 fresh
 sed -i 's@internal/alpha/alpha.go:Value@internal/alpha/directory.go:Value@' scripts/inventory/dynamic.go
 mkdir internal/alpha/directory.go
 assert_plan_error 'dynamic key missing file'
}

module_rerun_and_replace() {
 fresh
 echo 'host/storage/rules modules/rules' > "$tmp/map"
 run
 assert_rerun
 sed -i 's@=> ./modules/rules@=> ./host/storage/rules@' go.mod
 run
 grep -q '=> ./modules/rules' go.mod
 assert_rerun
}

whole_root_and_text_scope() {
 fresh
 printf '%s\n' 'fmt-roots:' '	gofmt -l internal/ cmd/ db/' >> Makefile
 mkdir -p scripts/split docs/ux-atlas deploy/hack/standalone
 cp "$script" scripts/split/move.sh
 cp "$here/move_test.sh" scripts/split/move_test.sh
 echo 'internal/alpha/data.txt' > scripts/split/other.txt
 echo 'path = "internal/alpha/data.txt"' > docs/ux-atlas/build.py
 echo 'COPY db/schema.sql /schema.sql' > deploy/hack/standalone/Dockerfile
 git add .
 bash "$script" --map "$tmp/map" --text deploy/hack/standalone/Dockerfile > "$tmp/log" 2>&1
 cmp "$script" scripts/split/move.sh
 cmp "$here/move_test.sh" scripts/split/move_test.sh
 grep -qF 'gofmt -l host/ cmd/ host/db/' Makefile
 grep -qFx 'host/alpha/data.txt' scripts/split/other.txt
 grep -q 'host/alpha/data.txt' docs/ux-atlas/build.py
 grep -qFx 'COPY host/db/schema.sql /schema.sql' deploy/hack/standalone/Dockerfile
 go run ./scripts/layering
}

empty_wildcard() {
 fresh
 echo 'package server' > internal/server/hack__test.go
 git add internal/server
 echo 'internal/server/hack_*_test.go products/hack/servertest/' > "$tmp/map"
 run
 test -f products/hack/servertest/hack__test.go
 assert_rerun
}

leftover_paths() {
 fresh
 printf '%s\n' 'db/schema.sql' 'testdb/schema.sql' 'host/db/schema.sql' 'docs/internal/alpha' 'internal/alpha/data.txt' > refs.txt
 git add refs.txt
 run
 grep -q 'refs.txt:1: db/schema.sql' "$tmp/log"
 grep -q 'refs.txt:5: internal/alpha/data.txt' "$tmp/log"
 if grep -qE 'refs.txt:[234]:' "$tmp/log"; then
  return 1
 fi
}

batched_git() {
 fresh
 mkdir -p twins/old twins/new
 echo same > twins/old/one.txt
 echo same > twins/old/two.txt
 cp twins/old/one.txt twins/new/one.txt
 cp twins/old/two.txt twins/new/two.txt
 git add twins
 echo "twins/old twins/new" >> "$tmp/map"
 mkdir -p "$tmp/bin"
 real_git=$(command -v git)
 cat > "$tmp/bin/git" <<EOF
#!/usr/bin/env bash
if test "\${1-}" = mv || test "\${1-}" = rm; then
 printf '%s\\n' "\$*" >> "$tmp/git-calls"
fi
exec "$real_git" "\$@"
EOF
 chmod +x "$tmp/bin/git"
 PATH="$tmp/bin:$PATH" run
 grep -q 'mv -- internal/alpha/alpha.go internal/alpha/alpha_test.go internal/alpha/data.txt host/alpha/' "$tmp/git-calls"
 grep -q 'rm -f -- twins/old/one.txt twins/old/two.txt' "$tmp/git-calls"
}

runtime_failure() {
 fresh
 mkdir -p "$tmp/fail-bin"
 printf '%s\n' '#!/usr/bin/env bash' 'exit 17' > "$tmp/fail-bin/gofmt"
 chmod +x "$tmp/fail-bin/gofmt"
 if PATH="$tmp/fail-bin:$PATH" run; then
  return 1
 fi
 grep -q 'working tree is changed' "$tmp/log"
 test -f host/alpha/alpha.go
}


bare_root_scope() {
 fresh
 head -1 scripts/check-audience.py > "$tmp/audiences"
 run
 head -1 scripts/check-audience.py > "$tmp/audiences-after"
 cmp "$tmp/audiences" "$tmp/audiences-after"
 grep -qFx "scan = ('internal', 'cmd', 'products')" scripts/check-audience.py
 grep -qF '"cmd", "host", "products"' scripts/inventory/main.go
}

late_relative() {
 for shape in D1 D3; do
  fresh
  if test "$shape" = D1; then
   echo 'internal/alpha products/hack/alpha' > "$tmp/map"
   old=internal/alpha/data.txt
   new=products/hack/alpha/data.txt
  else
   old=db/schema.sql
   new=host/db/schema.sql
  fi
  run
  commit
  mkdir -p cmd/probe
  printf 'package probe\nvar Path = "../../%s"\n' "$old" > cmd/probe/probe.go
  printf 'cat ../%s\n' "$old" > scripts/stale.sh
  git add cmd/probe scripts/stale.sh
  run
  grep -qF "\"../../$new\"" cmd/probe/probe.go
  grep -qFx "cat ../$new" scripts/stale.sh
  assert_rerun
 done
 # The destination may be a directory supplied only by the planned future tree.
 fresh
 mkdir -p cmd/probe
 printf 'package probe\nvar Path = "../../old/alpha/"\n' > cmd/probe/probe.go
 printf '%s\n' 'old/* products/hack/*' 'internal/alpha products/hack/alpha' > "$tmp/map"
 git add cmd/probe
 run
 grep -qF '"../../products/hack/alpha/"' cmd/probe/probe.go
}

escaped_literals() {
 fresh
 cat > internal/alpha/escaped.go <<'GO'
package alpha
var UTF8 = "../../db/\xc3\xa9"
var Byte = "../../db/\xff"
var Unicode = "../../db/\u00e9"
var Octal = "../../db/\303\251"
var Safe = "../../db/back\\slash\"quote"
GO
 # Safe escapes still rewrite; unsupported escapes retain their original bytes.
 python3 - <<'PY'
import os
for name in (b'back\\slash"quote',):
    open(b'db/' + name, 'wb').close()
PY
 gofmt -w internal/alpha/escaped.go
 grep -E '^var (UTF8|Byte|Unicode|Octal)' internal/alpha/escaped.go > "$tmp/escaped-original"
 git add internal/alpha db
 run
 grep -E '^var (UTF8|Byte|Unicode|Octal)' host/alpha/escaped.go > "$tmp/escaped-after"
 cmp "$tmp/escaped-original" "$tmp/escaped-after"
 for name in UTF8 Byte Unicode Octal; do
  grep -qF "REVIEW escaped literal internal/alpha/escaped.go: " "$tmp/log"
  grep -q "var $name" host/alpha/escaped.go
 done
 test "$(grep -c 'REVIEW escaped literal internal/alpha/escaped.go:' "$tmp/log")" -eq 4
 grep -qF '"../db/back\\slash\"quote"' host/alpha/escaped.go
 # The --text path must also preserve escaped literals selected as root paths.
 fresh
 printf '%s\n' 'package main' 'var Path = "db/\xff"' > scripts/escaped.go
 cp scripts/escaped.go "$tmp/text-escaped"
 git add scripts/escaped.go
 run
 cmp "$tmp/text-escaped" scripts/escaped.go
 grep -qF 'REVIEW escaped literal scripts/escaped.go: "db/\xff"' "$tmp/log"
}

embed_comment_spans() {
 fresh
 cat > internal/alpha/examples.go <<'GO'
package alpha
var Example = `
//go:embed ../../db/schema.sql
`
/*
//go:embed ../../db/schema.sql
*/
GO
 gofmt -w internal/alpha/examples.go
 cp internal/alpha/examples.go "$tmp/embed-examples"
 git add internal/alpha/examples.go
 run
 cmp "$tmp/embed-examples" host/alpha/examples.go
 # Default Go text files also retain example directives as written.
 fresh
 cat > scripts/examples.go <<'GO'
package main

var Example = `
//go:embed db/schema.sql
`

/*
//go:embed db/schema.sql
*/
GO
 cp scripts/examples.go "$tmp/embed-examples"
 git add scripts/examples.go
 run
 cmp "$tmp/embed-examples" scripts/examples.go
}

leftover_prefixed() {
 fresh
 before=$(snapshot)
 bash "$script" --map "$tmp/map" --dry-run > "$tmp/log" 2>&1
 test "$before" = "$(snapshot)"
 grep -qF 'Remaining old paths need a human look (before the move):' "$tmp/log"
 grep -qF 'scripts/mcpjam/fixture.sh:2:' "$tmp/log"
 grep -qF 'scripts/mcpjam/fixture.sh:3:' "$tmp/log"
 run
 grep -qF 'scripts/mcpjam/fixture.sh:2:' "$tmp/log"
 grep -qF 'scripts/mcpjam/fixture.sh:3:' "$tmp/log"
}

# The subshell is a standalone command, never the condition of an if: errexit
# remains active inside every case and every helper called by the case.
failures=0
for case in bare_root_scope late_relative escaped_literals embed_comment_spans leftover_prefixed plain dynamic_failure late_reference late_file collision nested_move longest idempotent dry_run file_wildcard layering_and_text plan_errors untracked_and_discovery root_tokens destination_tree_errors nested_destination_errors untracked_destinations twin_relative_and_cleanup lexer_and_embed dynamic_disk_validation module_rerun_and_replace whole_root_and_text_scope empty_wildcard leftover_paths batched_git runtime_failure; do
 set +e
 (
  set -euo pipefail
  "$case"
 ) > "$tmp/case-log" 2>&1
 result=$?
 set -e
 if test "$result" -eq 0; then
  echo "PASS $case"
 else
  echo "FAIL $case"
  cat "$tmp/case-log"
  cat "$tmp/log"
  failures=$((failures + 1))
 fi
 done
 test "$failures" -eq 0
