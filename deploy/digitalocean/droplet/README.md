# Simple Host 1-Click for DigitalOcean

Verified 2026-10-06 with the published v0.7.10 image in disposable local containers: fresh installation and an actual v0.7.9 upgrade through Caddy preserved site files, KV, SQLite, file objects and full-mode policies; add/own order schemas, 150 public photos, NFC upload names, the separate file pool and restart persistence passed. This check used native arm64 and localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

Verified 2026-10-05 with the published v0.7.9 image in disposable local containers: a fresh schema and an actual v0.7.4 upgrade through Caddy preserved site content and passed home selection/clearing, showcase fallback, bio, pins/order, live feed and restart persistence. This check used localhost HTTP; earlier cloud and TLS evidence below remains dated to the versions tested.

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
- `/opt/simple-host-setup/first-login.sh`, `/opt/simple-host-setup/upgrade.sh`,
  the motd, and `001_onboot`.

Made on the droplet, never in the image: the admin key, the database password,
`/opt/simple-host/.env`, the database and its volume, and the TLS certificates.
The installer makes them when the first-login setup runs it.

## First boot and first login

1. `018-force-ssh-logout.sh` holds SSH logins until the droplet has booted;
   `files/var/lib/cloud/scripts/per-instance/001_onboot` (once per droplet)
   adds the setup to root's `.bashrc` and lets SSH in.
2. At root's first interactive login, `first-login.sh` takes a lock
   (`/run/simple-host-setup.lock`; a second session is told setup is running
   and where its log is), shows the droplet's IPv4 address (the reserved IP
   when one is assigned, from the metadata service, else the first public
   IPv4 of `hostname -I`) and asks for the dashboard address and the sites
   address (default `sites.<address>`). It says which A records to add and
   checks them against public DNS (`r` checks again for up to
   `DNS_WAIT_SECONDS`, `c` continues, `q` quits). A name that does not
   resolve is never "ok"; when the droplet's IP is unknown the check is
   skipped with a line saying so. An AAAA record that is not the droplet's
   IPv6 gets a warning (Let's Encrypt tries IPv6 first).
3. It fetches `deploy/install/install.sh` by the pinned commit, checks its
   sha256, and runs it with `--host` and `--content` in its own session
   (`nohup setsid`, HUP ignored), so a dropped SSH connection does not stop
   it. The login session only follows the log. Each run gets a fresh log,
   `/root/simple-host-install-<date>-<time>.log`, mode 600.
4. When the installer succeeds, the detached run removes the admin key line
   from that log (the key stays only in `/opt/simple-host/.env`), writes
   `/opt/simple-host-setup/.done` and removes the setup's lines from
   `.bashrc`. The `.done` marker, not `SITE_DOMAIN` in `.env` (which the
   installer writes before it pulls, migrates and checks health), is what
   "already set up" and the motd look at. Stopped or failed, setup runs again
   at the next login.
5. `upgrade.sh` reads `SITE_DOMAIN` and `CONTENT_HOST` from `.env` and runs
   the installer https://simple-host.app/setup pins (or `--commit C
   --sha256 S`) with them, the same way. Without `--host` and `--content` the
   installer would rewrite both empty.

Setup does not ask for an email. At the pinned release `install.sh --email`
only sets `ACME_EMAIL` for the Caddy container, whose Caddyfile has no `email`
directive.

Every time and limit in `first-login.sh` is a variable at its top
(`DNS_WAIT_SECONDS`, `DNS_POLL_SECONDS`, `DNS_RESOLVER`, timeouts and retries).
The build's apt lock wait is the Packer variable `apt_lock_timeout` (600 s).

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
| `scripts/999-img_check.sh` | [marketplace-partners](https://github.com/digitalocean/marketplace-partners) `scripts/99-img-check.sh` at `735b351eb3aa244b610287ef17d9e70b002f83a1`, v1.8.1 (Apache 2.0). Saved as `999-img_check.sh`, the name droplet-1-clicks' `make update-scripts` gives it in `common/scripts/` |

The build ends as DigitalOcean's templates do, with `900-cleanup.sh`, then
this image's `950-final-cleanup.sh`, then `999-img_check.sh`. The extra step
empties the logs written during the cleanup's apt run and zero-fill,
with rsyslog stopped first so a UFW block logged before the image check
does not leave `kern.log` and `ufw.log` non-empty. It also
empties `/etc/machine-id` and removes Docker's `engine-id` (with the daemon
stopped), so each droplet makes its own. The image check exits non-zero on a
failed check, which fails the build. `cloud-init status --wait` may exit 2
(finished with recoverable errors) without failing the build.

## Token

`make build` reads `DIGITALOCEAN_API_TOKEN` from the environment; it is never
in a file. A Full Access token with a short expiry is simplest. A custom-scoped
token needs what Packer's DigitalOcean builder calls: create, read and delete
droplets and run actions on them (power off, snapshot), create and delete the
temporary SSH key, read images, actions, regions and sizes.

## Submitting

`marketplace/` holds the vendor material: `listing.md`, `getting-started.md`
and the list of screenshots to take. Nothing has been submitted.

Your home page (2026-10-05): home selection, public showcase feed, bio, pins and
manual order also work on small-box path installs. The public person page opens
the selected site’s usual URL; the owner dashboard keeps its own origin. No
whole-space domain is included. See [home-page guide](../../../docs/your-home-page.md).
