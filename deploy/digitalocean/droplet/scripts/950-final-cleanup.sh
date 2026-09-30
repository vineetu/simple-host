#!/bin/bash
# After DigitalOcean's 900-cleanup.sh, before the image check: what that
# script leaves behind or writes again after its own log pass.
#
# - Logs written during its apt run and zero-fill are emptied again.
# - /etc/machine-id is emptied so each droplet makes its own at first boot
#   (systemd treats an empty file as "first boot").
# - Docker's engine-id is removed so each droplet's daemon makes its own;
#   the daemon is stopped first so it does not write it back.
set -euo pipefail

systemctl stop docker.socket docker.service 2>/dev/null || true
rm -f /var/lib/docker/engine-id

find /var/log -type f -exec truncate -s 0 {} +
rm -rf /var/log/*.gz /var/log/*.[0-9] /var/log/*-????????
truncate -s 0 /etc/machine-id
rm -rf /tmp/* /var/tmp/*
