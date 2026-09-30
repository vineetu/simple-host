# Simple Host 1-Click for DigitalOcean

The Packer build for the Simple Host droplet image (Ubuntu 24.04), laid out as
in DigitalOcean's [droplet-1-clicks](https://github.com/digitalocean/droplet-1-clicks)
repository. The guide for people using it is
[docs/platforms/digitalocean.md](../../../docs/platforms/digitalocean.md).

## Commands

Run from this directory. Only `build` needs a token.

```sh
make validate
```

```sh
make test
```

```sh
read -rsp 'DigitalOcean API token: ' DIGITALOCEAN_API_TOKEN && echo && export DIGITALOCEAN_API_TOKEN && make build; unset DIGITALOCEAN_API_TOKEN
```

- `validate`: `packer init plugins.pkr.hcl` (the DigitalOcean builder plugin),
  then `packer validate template.json`.
- `lint`: `bash -n` and `shellcheck` on this image's own scripts.
- `pins`: the installer release, commit and sha256 in `template.json` and
  `first-login.sh` match the ones https://simple-host.app/setup uses
  (`internal/handler/static/setup/setup.js`).
- `test`: `pins`, `lint`, then `test/first-login_test.sh`, which runs the
  first-login setup, `001_onboot` and the motd in an `ubuntu:24.04` container
  with the installer and DNS stubbed (needs Docker).
- `build`: provisions a temporary droplet and saves it as the snapshot
  `simple-host-24-04-snapshot-<time>`. Packer deletes the droplet afterwards.

## What is in the image, and what is not

Baked in at build time, the same on every droplet:

- Docker Engine and the compose plugin, from Docker's apt repository, as
  `deploy/install/install.sh` installs them (`scripts/010-docker.sh`).
- The release's container images (`caddy`, `postgres`, `ghcr.io/vineetu/simple-host`),
  pulled so the install at first login is quicker, and the installer pin in
  `/opt/simple-host-setup/installer.env` (`scripts/011-simple-host.sh`, which
  also fetches the installer by commit and fails the build if its sha256 is
  wrong).
- `dig` (`bind9-dnsutils`) for the DNS check, UFW allowing only 22, 80 and 443.
- `/opt/simple-host-setup/first-login.sh`, the motd, and `001_onboot`.

Made on the droplet, never in the image: the admin key, the database password,
`/opt/simple-host/.env`, the database and its volume, and the TLS certificates.
The installer makes them when the first-login setup runs it.

## First boot and first login

1. `018-force-ssh-logout.sh` holds SSH logins until the droplet has booted;
   `files/var/lib/cloud/scripts/per-instance/001_onboot` (once per droplet)
   adds the setup to root's `.bashrc` and lets SSH in.
2. At root's first interactive login, `first-login.sh` shows the droplet's
   IPv4 address, asks for the dashboard address, the sites address (default
   `sites.<address>`) and an email, says which A records to add, and checks
   them against public DNS (`r` checks again for up to `DNS_WAIT_SECONDS`,
   `c` continues, `q` quits).
3. It fetches `deploy/install/install.sh` by the pinned commit, checks its
   sha256, and runs it with `--host`, `--content` and `--email`. The output
   goes to the screen and to `/root/simple-host-install.log` (mode 600: it
   holds the admin key).
4. On success it prints the dashboard URL and how to see the admin key again,
   and removes its lines from `.bashrc`. Stopped or failed, it stays and runs
   again at the next login.

Every time and limit in `first-login.sh` is a variable at its top
(`DNS_WAIT_SECONDS`, `DNS_POLL_SECONDS`, `DNS_RESOLVER`, timeouts and retries).

## A new release

After a release, set `installer_release`, `installer_commit`,
`installer_sha256` and `application_version` in `template.json`, and the same
three `INSTALLER_*` defaults at the top of `first-login.sh`, to the values in
`setup.js`. `make pins` fails until they agree. Then build a new snapshot and
submit it as a new version of the listing.

## Files from DigitalOcean

Copied unchanged, with their headers:

| File | From |
|---|---|
| `scripts/014-ufw-http.sh`, `018-force-ssh-logout.sh`, `020-application-tag.sh`, `900-cleanup.sh`, `files/var/lib/digitalocean/application.info` | `common/` in [droplet-1-clicks](https://github.com/digitalocean/droplet-1-clicks) at `2bd00db9195c84949f948dafca499b9444f998b4` (MIT) |
| `scripts/999-img_check.sh` | `scripts/99-img-check.sh` in [marketplace-partners](https://github.com/digitalocean/marketplace-partners) at `735b351eb3aa244b610287ef17d9e70b002f83a1`, v1.8.1 (Apache 2.0), which droplet-1-clicks' `make update-scripts` copies into `common/scripts/` |

`900-cleanup.sh` and `999-img_check.sh` run last in the build, as in
DigitalOcean's templates. The image check exits non-zero on a failed check,
which fails the build.

## Token

`make build` reads `DIGITALOCEAN_API_TOKEN` from the environment; it is never
in a file. A Full Access token with a short expiry is simplest. A custom-scoped
token needs what Packer's DigitalOcean builder calls: create, read and delete
droplets and run actions on them (power off, snapshot), create and delete the
temporary SSH key, read images, actions, regions and sizes.

## Submitting

`marketplace/` holds the vendor material: `listing.md`, `getting-started.md`
and the list of screenshots to take. Nothing has been submitted.
