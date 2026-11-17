#!/usr/bin/env bash
# Use the backup/restore protocol from the candidate package, before install
# rewrites either the saved environment or Compose file.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-hack}
SHARED="$HERE/shared-upgrade.sh"
[ -f "$SHARED" ] || SHARED="$HERE/../../shared/upgrade.sh"
IMAGE="ghcr.io/vineetu/simple-hack:0.8.20"
TO="0.8.20"
args=()
while [ "$#" -gt 0 ]; do
  case "$1" in
    --dir) INSTALL_DIR=${2:?}; shift 2 ;;
    --image) IMAGE=${2:?}; shift 2 ;;
    --to) TO=${2:?}; shift 2 ;;
    *) args+=("$1"); shift ;;
  esac
done
if [ "$SHARED" != "$INSTALL_DIR/shared-upgrade.sh" ]; then
  install -m 700 "$SHARED" "$INSTALL_DIR/shared-upgrade.sh"
fi
SHARED="$INSTALL_DIR/shared-upgrade.sh"
exec bash "$SHARED" --dir "$INSTALL_DIR" --product simple-hack \
  --to "$TO" --image "$IMAGE" --compose "$HERE/compose.yaml" "${args[@]}"
