#!/usr/bin/env bash
# Phase 0c-1 symlink-farm reconciler (idempotent).
# Builds handles/<handle> -> by-id/<user_id> -> <flat name dir>, over the existing
# flat site dirs, WITHOUT moving any data. Path serving resolves:
#   handles/<handle>/<site>/current/<rest>  ==  sites/<site>/current/<rest>
set -euo pipefail
ROOT=/srv/simple-host/sites
DB="sudo -u postgres psql -tA simplehost"

mkdir -p "$ROOT/by-id" "$ROOT/handles"

# by-id/<user_id>/<name> -> ../../<name>   (one link per site)
$DB -c "SELECT s.user_id, s.name FROM sites s;" | while IFS='|' read -r uid name; do
  [ -z "$uid" ] && continue
  mkdir -p "$ROOT/by-id/$uid"
  ln -sfn "../../$name" "$ROOT/by-id/$uid/$name"
done

# handles/<handle> -> ../by-id/<user_id>   (one link per user with a handle)
$DB -c "SELECT handle, id FROM users WHERE handle IS NOT NULL;" | while IFS='|' read -r handle uid; do
  [ -z "$handle" ] && continue
  ln -sfn "../by-id/$uid" "$ROOT/handles/$handle"
done

echo "=== farm ==="
ls -la "$ROOT/handles" "$ROOT/by-id"
for d in "$ROOT"/handles/*; do
  [ -e "$d" ] || continue
  echo "-- $d --"; ls -la "$d/"
done
