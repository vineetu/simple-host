#!/usr/bin/env bash
# Use the upgrade script from the newly downloaded, versioned package.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
INSTALL_DIR=${INSTALL_DIR:-/opt/simple-hack}
[ -f "$INSTALL_DIR/.env" ] || { echo "No installation in $INSTALL_DIR" >&2; exit 1; }
exec bash "$HERE/install.sh" --dir "$INSTALL_DIR" "$@"
