#!/usr/bin/env bash
# Architecture fitness function: the one structural rule this codebase has.
#
#   internal/handler and internal/hackhttp are the two package fan-outs.
#   internal/auth may import internal/db. internal/hackhttp is transitional
#   (step 3.2): it may import the leaf packages Hack HTTP already uses
#   (httpx, rules/*, model, edition, auth, db, email, config, buildinfo,
#   papermetrics, tarball), but never internal/handler, except in its own
#   _test.go files, which build a real *handler.SiteHandler for their
#   fixtures (and symmetrically, internal/handler's _test.go files may
#   import internal/hackhttp to build a real *hackhttp.HackHandler) —
#   docs/architecture/target.md §2. Everything else is a leaf. Test files
#   (*_test.go) may also import internal/testdb.
#
# A document saying so would rot. This fails the build instead.
#
#   bash scripts/check-layering.sh
set -u
cd "$(dirname "$0")/.."

fail=0

# Packages allowed to depend on other internal packages, and on what.
#   handler  -> anything
#   auth     -> db
#   hackhttp -> its transitional leaf set (non-test); + handler (test only)
allowed_for() {
  case "$1" in
    handler) echo "ANY" ;;
    auth)    echo "db" ;;
    edition) echo "config" ;;
    hackhttp) echo "httpx rules model edition auth db email config buildinfo papermetrics tarball" ;;
    *)       echo "" ;;
  esac
}

# Packages allowed ONLY from _test.go files, on top of allowed_for.
#   hackhttp's tests build a real *handler.SiteHandler (teamSiteApp-style
#   fixture); handler's tests symmetrically build a real *hackhttp.HackHandler.
test_extra_for() {
  case "$1" in
    hackhttp) echo "handler mcp storage" ;;
    handler)  echo "hackhttp" ;;
    *)        echo "" ;;
  esac
}

echo "== internal package layering =="
for dir in internal/*/; do
  pkg=$(basename "$dir")
  allowed=$(allowed_for "$pkg")
  [ "$allowed" = "ANY" ] && continue
  test_extra=$(test_extra_for "$pkg")

  # Internal packages this one imports (non-test), excluding itself.
  deps_nontest=$(grep -rhoE --exclude='*_test.go' '"github\.com/vsriram/simple-host/internal/[a-z]+' "$dir" 2>/dev/null \
    | sed -E 's|.*internal/||' | sort -u | grep -v "^$pkg$" || true)
  # Internal packages this one imports from _test.go files, excluding
  # itself and internal/testdb (the shared test-database helper, every
  # package's test files may use it).
  deps_test=$(grep -rhoE --include='*_test.go' '"github\.com/vsriram/simple-host/internal/[a-z]+' "$dir" 2>/dev/null \
    | sed -E 's|.*internal/||' | sort -u | grep -v "^$pkg$" | grep -v '^testdb$' || true)

  for d in $deps_nontest; do
    if ! grep -qxF "$d" <<<"$(tr ' ' '\n' <<<"$allowed")"; then
      echo "  FAIL: internal/$pkg imports internal/$d — leaf packages must not depend on siblings"
      fail=1
    fi
  done
  for d in $deps_test; do
    if ! grep -qxF "$d" <<<"$(tr ' ' '\n' <<<"$allowed $test_extra")"; then
      echo "  FAIL: internal/$pkg's tests import internal/$d — leaf packages must not depend on siblings"
      fail=1
    fi
  done
done
[ "$fail" -eq 0 ] && echo "  ok — handler and hackhttp are the only fan-outs; every other package is a leaf"

echo
if [ "$fail" -ne 0 ]; then
  echo "LAYERING VIOLATION — see the Invariants section of ARCHITECTURE.md."
  echo "Either move the shared code into the caller, or make the case for a new boundary."
  exit 1
fi
echo "layering ok ✓"
