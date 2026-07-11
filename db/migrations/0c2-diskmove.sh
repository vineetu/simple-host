#!/usr/bin/env bash
# Phase 0c-2 one-time disk move: flat sites/<name> -> by-id/<uid>/<name>, with a
# back-compat symlink sites/<name> -> by-id/<uid>/<name>. Idempotent + checksummed.
set -euo pipefail
ROOT=/srv/simple-host/sites
DB="sudo -u postgres psql -tA simplehost"

sum_tree() { # checksum of all file contents+relpaths under $1 (follows symlinks)
  ( cd "$1" && find -L . -type f | sort | while read -r f; do printf '%s  ' "$f"; sha256sum "$f" | awk '{print $1}'; done | sha256sum | awk '{print $1}' )
}

$DB -c "SELECT s.user_id, s.name FROM sites s;" | while IFS='|' read -r uid name; do
  [ -z "$uid" ] && continue
  flat="$ROOT/$name"
  dest="$ROOT/by-id/$uid/$name"
  echo "### $name (uid=$uid)"
  before=$(sum_tree "$flat"); echo "  before=$before"

  if [ -L "$flat" ]; then
    echo "  already migrated (sites/$name is a symlink); skipping move"
  else
    # 0c-1 left by-id/<uid>/<name> as a symlink pointing back to flat; remove it first.
    [ -L "$dest" ] && rm -f "$dest"
    mkdir -p "$ROOT/by-id/$uid"
    mv "$flat" "$dest"
    ln -sfn "by-id/$uid/$name" "$flat"
  fi

  after_flat=$(sum_tree "$flat"); after_byid=$(sum_tree "$dest")
  echo "  after(sites/$name)=$after_flat  after(by-id)=$after_byid"
  if [ "$before" != "$after_flat" ] || [ "$after_flat" != "$after_byid" ]; then
    echo "  !!! CHECKSUM MISMATCH — ABORT"; exit 1
  fi
  echo "  OK content identical via both paths"

  $DB -c "UPDATE versions v SET disk_path = 'by-id/$uid/$name/v'||v.version_number||'/' FROM sites s WHERE v.site_id=s.id AND s.name='$name';" >/dev/null
done

echo "=== resulting layout ==="
ls -la "$ROOT" | grep -vE '^total'
echo "=== versions.disk_path ==="
$DB -c "SELECT s.name, v.version_number, v.disk_path FROM versions v JOIN sites s ON s.id=v.site_id ORDER BY s.name, v.version_number;"
