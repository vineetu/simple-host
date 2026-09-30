#!/bin/bash
# Docker Engine and the compose plugin, from Docker's apt repository: the same
# packages and source deploy/install/install.sh installs when Docker is
# missing, so the installer finds them and skips that step on first login.
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
chmod a+r /etc/apt/keyrings/docker.asc
# shellcheck source=/dev/null
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" \
  > /etc/apt/sources.list.d/docker.list
APT_LOCK_TIMEOUT_SECONDS=${APT_LOCK_TIMEOUT_SECONDS:-600}
apt-get -qq -o DPkg::Lock::Timeout="$APT_LOCK_TIMEOUT_SECONDS" update
apt-get -qqy -o DPkg::Lock::Timeout="$APT_LOCK_TIMEOUT_SECONDS" install docker-ce docker-ce-cli containerd.io docker-compose-plugin
systemctl enable --now docker
docker --version
docker compose version
