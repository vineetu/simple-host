# Full Simple Hack on DigitalOcean

**The full platform installer was tested on DigitalOcean on 2026-10-01.** The
Packer snapshot recipe is validated but has not been built or tested; a
Marketplace listing has not been submitted or approved. This package is
separate from the existing Simple Host small-box snapshot, and v0.8.0 remains
an unpublished release candidate.

The live test used one Ubuntu 24.04 `s-1vcpu-2gb` droplet in `nyc3`, with an
amd64 candidate built from source commit `7a1180b`. The platform, event and
team addresses used the temporary IP's `sslip.io` names and received trusted
Let's Encrypt certificates. A desktop organiser and mobile participant signed
in through the browser, joined an event, created a team and published its
project. Email-code messages were captured by an isolated mail transport sink
inside the test network; external email delivery was not tested.

Fresh installation applied all 38 migrations. Restart and a second-image
redeploy preserved the event, team, project, credentials, settings and existing
certificate. The second image contained the same candidate binary; this was
an upgrade/persistence check, not a migration between released versions.
Unknown/deeper hostnames were refused certificates, and the public internal
certificate-check route returned 404. The disposable droplet, SSH key and tag
were deleted after verification; no snapshot or backup was retained.

The recipe in `deploy/hack/standalone/digitalocean/` builds an Ubuntu 24.04
image with Docker, the full Simple Hack application image and first-login
setup. It reuses the existing small-box Docker installation, firewall, cleanup
and DigitalOcean image validation scripts. No database, admin key, mail key or
certificate is created in the snapshot.

## Build the image

Publish a standalone application image from the release commit first; the
default v0.8.0 is a candidate and is not assumed available. The Packer build
fails if the image cannot be pulled. Packer and a DigitalOcean API token are
required for building. From the repository root:

```sh
make -C deploy/hack/standalone/digitalocean validate
```

```sh
make -C deploy/hack/standalone/digitalocean build
```

Supply `DIGITALOCEAN_API_TOKEN` through your own secret environment. To build
with another published application image, run Packer from that directory with
`-var simple_hack_image=your-image-reference`; use a digest to fix the exact
artifact. The build uses a temporary 2 GB droplet in `nyc3` by default and saves
a snapshot; both the build and retained snapshot incur provider charges.

## Install from your snapshot

Create a droplet from the snapshot and add your SSH key. Configure the domain
and email prerequisites from [the standalone guide](simple-hack-standalone.md).
On first root SSH login, setup asks for your domain, verifies the apex, event
and team DNS shapes against the droplet IPv4, and asks for the Resend key
without echoing it. This first-login setup uses IPv4: remove existing AAAA
records before proceeding. Configure and verify IPv6 separately after installation.

Setup generates credentials once, starts the database, applies migrations and
starts the full platform. It continues if SSH disconnects. The private log is
`/opt/simple-hack/install.log`; after failure, reconnect or run
`/opt/simple-hack-setup/digitalocean/first-login.sh` to retry. The first-login
hook is removed only after successful installation.

After startup, sign in at your domain, create an event and publish a team
project. Verify trusted certificates and the event and team hostnames from a
separate client. A built snapshot must also pass a real first-login installation
and upgrade test before being described as a verified DigitalOcean one-click
image. The existing live test verified the installer on a plain droplet.

For upgrades and backups, follow [the standalone guide](simple-hack-standalone.md).
The installer uses persistent Docker volumes and preserves existing secrets;
it does not contact simple-host.app to register an event name.

Publishing a public Marketplace entry additionally requires DigitalOcean
vendor access, listing materials and image review. Building a private snapshot
does not complete that process. [Marketplace partner workflow](https://github.com/digitalocean/marketplace-partners).
