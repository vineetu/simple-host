# Install Simple Hack on DigitalOcean

Run events, teams, team sites, judging and results on your own server.
People use password accounts without email.

## What you need

Use an Ubuntu 24.04 droplet with Docker, or build your own snapshot with Packer
and a DigitalOcean API token. The [standalone guide](simple-hack-standalone.md#what-you-need)
covers server size, DNS and certificate capacity.

The recipe in `deploy/hack/standalone/digitalocean/` builds an image with Docker,
Simple Hack and first-login setup. No database, admin key, mail key or certificate
is created in the snapshot. A private snapshot does not create a Marketplace listing;
that also requires vendor access, listing materials and image review
([Marketplace partner workflow](https://github.com/digitalocean/marketplace-partners)).

## Install

### Build the image

The image pinned by this package's Packer template is published. The build
fails if that image cannot be pulled. Packer and a DigitalOcean API token are
required for building. From the repository root:

```sh
make -C deploy/hack/standalone/digitalocean validate
```

```sh
make -C deploy/hack/standalone/digitalocean build
```

Supply `DIGITALOCEAN_API_TOKEN` through your own secret environment. To build
with another published application image, run Packer from that directory with
`-var simple_hack_image=your-image-reference`. Use a digest to fix the exact
artifact. The build uses a temporary 2 GB droplet in `nyc3` by default and saves
a snapshot. Both the build and retained snapshot incur provider charges.

### Install from your snapshot

Create a droplet from the snapshot and add your SSH key. Configure the domain
and certificate prerequisites from [the standalone guide](simple-hack-standalone.md).
On first root SSH login, setup asks for your domain, verifies the apex, event
and team DNS shapes against the droplet IPv4, and asks for the Resend key
without echoing it. This first-login setup uses IPv4: remove existing AAAA
records before proceeding. Configure and verify IPv6 separately after installation.

Setup generates credentials once. It starts the database and applies migrations
before starting the platform. It continues if SSH disconnects. The private log is
`/opt/simple-hack/install.log`. After failure, reconnect or run
`/opt/simple-hack-setup/digitalocean/first-login.sh` to retry. The first-login
hook is removed only after successful installation.

## First sign-in

Open your domain and choose **Create an account**. Use an email or username plus a password.
Open `https://<your-domain>/admin` with the key from:

```sh
sudo grep ADMIN_API_KEY /opt/simple-hack/.env
```

Set `PASSWORD_SELF_SIGNUP=off` for admin-created accounts only.
Create an event and publish a team site. Check their HTTPS addresses from another machine.

## Connect an AI app

In ChatGPT or Claude, add a custom connector called **Simple Hack** with
`https://<your-domain>/mcp`. Sign in with your account on this server and choose **Allow**.
Your server's `/get-started` page has the same steps. Use your own server's address.

## Upgrade

Follow the [standalone upgrade steps](simple-hack-standalone.md#upgrade).
The installer keeps your persistent Docker volumes and existing secrets.

## Back up

Follow the [standalone backup steps](simple-hack-standalone.md#back-up):
a logical Postgres dump plus the `data` volume, with a private copy of the configuration.

## Troubleshooting

First-login setup checks IPv4 DNS. Remove AAAA records until you configure and verify IPv6.
After a failure, read `/opt/simple-hack/install.log` and rerun
`/opt/simple-hack-setup/digitalocean/first-login.sh`.

Company sign-in and the TLS mode are environment settings, not part of the
first-login prompts. Add them to `/opt/simple-hack/.env` and rerun
`/opt/simple-hack-setup/digitalocean/first-login.sh --apply` afterwards (see
[the standalone guide](simple-hack-standalone.md#company-sign-in-and-private-networks)
for what each one does). A DigitalOcean droplet is public by nature, so
`TLS_MODE=internal` or `acme` here only makes sense on a droplet attached to
a VPC private network with no public inbound rule, reached over a VPN or
DigitalOcean's private networking rather than the public internet.

Release notes: [the changelog](https://github.com/vineetu/simple-host/blob/main/CHANGELOG.md).
