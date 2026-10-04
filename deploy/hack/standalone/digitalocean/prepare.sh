#!/usr/bin/env bash
set -euo pipefail
: "${SIMPLE_HACK_IMAGE:?}"
cd /opt/simple-hack-setup
chmod 755 install.sh upgrade.sh digitalocean/*.sh
install -D -m 755 digitalocean/onboot.sh /var/lib/cloud/scripts/per-instance/001_simple_hack
mkdir -p /var/lib/digitalocean
printf 'application_name="Simple Hack"\napplication_version="0.8.5"\n' > /var/lib/digitalocean/application.info
# Pull only. No service, volume, password or certificate is created at build.
docker pull "$SIMPLE_HACK_IMAGE"
docker pull postgres:16-alpine
# Use the image validated by this build, including an optional digest pin.
python3 - <<'PY'
import os
from pathlib import Path
p=Path('compose.yaml')
p.write_text(p.read_text().replace('ghcr.io/vineetu/simple-hack:0.8.5', os.environ['SIMPLE_HACK_IMAGE']))
p=Path('install.sh')
p.write_text(p.read_text().replace('ghcr.io/vineetu/simple-hack:0.8.5', os.environ['SIMPLE_HACK_IMAGE']))
PY
test ! -e /opt/simple-hack/.env
