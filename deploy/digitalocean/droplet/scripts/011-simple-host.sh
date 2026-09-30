#!/bin/bash
# Prepares the image for the Simple Host installer, without running it.
#
# - Writes the installer pin (release, commit, sha256) that first-login.sh
#   reads, from the Packer variables.
# - Fetches the installer by that commit and checks the sha256 now, so a wrong
#   pin fails the image build instead of a customer's first login.
# - Pulls the container images that release runs, so the install on first
#   login does not wait for them. Public images only: no volume, no .env, no
#   key and no certificate is created here. Those belong to one droplet and
#   are made by the installer when the setup runs.
set -euo pipefail

: "${installer_release:?}" "${installer_commit:?}" "${installer_sha256:?}"
SETUP_DIR=/opt/simple-host-setup
FETCH_TIMEOUT_SECONDS=${FETCH_TIMEOUT_SECONDS:-60}
FETCH_RETRIES=${FETCH_RETRIES:-3}
RAW="https://raw.githubusercontent.com/vineetu/simple-host/$installer_commit"

install -d -m 0755 "$SETUP_DIR"
cat > "$SETUP_DIR/installer.env" <<EOM
INSTALLER_RELEASE=$installer_release
INSTALLER_COMMIT=$installer_commit
INSTALLER_SHA256=$installer_sha256
EOM
chmod 0644 "$SETUP_DIR/installer.env"
chmod 0755 "$SETUP_DIR/first-login.sh" /var/lib/cloud/scripts/per-instance/001_onboot /etc/update-motd.d/99-one-click

work=$(mktemp -d)
curl -fsSL --retry "$FETCH_RETRIES" --max-time "$FETCH_TIMEOUT_SECONDS" "$RAW/deploy/install/install.sh" -o "$work/install.sh"
printf '%s  %s\n' "$installer_sha256" "$work/install.sh" | sha256sum -c -
grep -qx "VERSION=\"$installer_release\"" "$work/install.sh" || { echo "install.sh at $installer_commit is not $installer_release" >&2; exit 1; }

# The images named in that release's compose file, with the release's own
# server image in place of the ${IMAGE} variable the installer fills in.
curl -fsSL --retry "$FETCH_RETRIES" --max-time "$FETCH_TIMEOUT_SECONDS" "$RAW/compose.yaml" -o "$work/compose.yaml"
app_image="ghcr.io/vineetu/simple-host:${installer_release#v}"
images=$(sed -n 's/^ *image: *//p' "$work/compose.yaml" | sed "s|\${IMAGE[^}]*}|$app_image|" | sort -u)
for image in $images; do
  docker pull -q "$image"
done
rm -rf "$work"
