#!/usr/bin/env bash
# Architecture fitness function: the one structural rule this codebase has.
#
#   internal/handler is the only package that may import other internal
#   packages. internal/auth may import internal/db. Everything else is a leaf.
#
# A document saying so would rot. This fails the build instead.
#
#   bash scripts/check-layering.sh
set -u
cd "$(dirname "$0")/.."

fail=0

# Packages allowed to depend on other internal packages, and on what.
#   handler -> anything
#   auth    -> db
allowed_for() {
  case "$1" in
    handler) echo "ANY" ;;
    auth)    echo "db" ;;
    *)       echo "" ;;
  esac
}

echo "== internal package layering =="
for dir in internal/*/; do
  pkg=$(basename "$dir")
  allowed=$(allowed_for "$pkg")
  [ "$allowed" = "ANY" ] && continue

  # Internal packages this one imports, excluding itself.
  deps=$(grep -rhoE '"github\.com/vsriram/simple-host/internal/[a-z]+' "$dir" 2>/dev/null \
    | sed -E 's|.*internal/||' | sort -u | grep -v "^$pkg$" || true)

  for d in $deps; do
    if ! grep -qxF "$d" <<<"$(tr ' ' '\n' <<<"$allowed")"; then
      echo "  FAIL: internal/$pkg imports internal/$d — leaf packages must not depend on siblings"
      fail=1
    fi
  done
done
[ "$fail" -eq 0 ] && echo "  ok — handler is the only fan-out; every other package is a leaf"

echo
if [ "$fail" -ne 0 ]; then
  echo "LAYERING VIOLATION — see the Boundaries section of ARCHITECTURE.md."
  echo "Either move the shared code into the caller, or make the case for a new boundary."
  exit 1
fi
echo "layering ok ✓"
