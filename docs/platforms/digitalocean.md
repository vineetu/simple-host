# Run the small box on DigitalOcean

![Simple Host on a DigitalOcean droplet, as on any Ubuntu server: two DNS A records point at the server; one Ubuntu server runs the installer's docker compose, with Caddy on ports 80 and 443 fetching certificates on demand from Let's Encrypt, the Simple Host server, Postgres, and Docker volumes for the sites, the log, the certificates and the database](https://simple-host.app/diagrams/upcloud.svg)

A droplet is an Ubuntu server like any other, so the small box runs on it with
the standard installer: the same layout as on UpCloud. Start from the Simple Host 1-Click
droplet, which asks for your domain when you first log in and runs the
installer for you, or from a plain Ubuntu droplet where you run the installer
yourself.

Not yet tested on a real DigitalOcean account. The 1-Click image is built from
[`deploy/digitalocean/droplet/`](../../deploy/digitalocean/droplet/) (checked
with `packer validate`, and its first-login setup tested in a container), and
is not in the DigitalOcean Marketplace yet; until it is, build it into your
own account as below.

## Cost

| Item | Size | Per month |
|---|---|---|
| Droplet `s-1vcpu-1gb` | 1 CPU, 1 GB, 25 GB disk, one IPv4 address | $6 |
| Snapshot of the 1-Click image (only if you build it yourself) | a few GB at $0.06/GB | under $0.30 |

The database, the web server and the sites all run on that one droplet. The
$4 droplet has 512 MB, which is below what the small box needs. Weekly backups
are optional and add 20%.

## With the 1-Click droplet

1. In the DigitalOcean control panel, create a droplet from **Simple Host**
   (Marketplace), or from your own snapshot (**Snapshots** tab): size
   `s-1vcpu-1gb` or larger in any region, with your SSH key.
2. At your DNS provider, add two A records pointing at the droplet's public
   IPv4 address: your domain, and `*.<your domain>`.
3. Log in as root: `ssh root@<droplet IP>`. The setup starts by itself. It asks
   for the address (your domain), the address sites are served from
   (`sites.<your domain>` unless you change it) and an email for certificate
   notices, checks that DNS points at the droplet, then runs the installer from
   its pinned release after checking its sha256. It runs once; if you stop it,
   it runs again at your next login, or now with
   `/opt/simple-host-setup/first-login.sh`.
4. Open `https://<your domain>/admin` and paste the admin key the installer
   printed (also kept as `ADMIN_API_KEY` in `/opt/simple-host/.env`).

The droplet's firewall (UFW) allows only SSH, HTTP and HTTPS.

## Build the 1-Click image yourself

You need [Packer](https://developer.hashicorp.com/packer/install) and a
DigitalOcean API token with an expiry (API → Tokens). From the repository
root:

```sh
make -C deploy/digitalocean/droplet validate
```

```sh
read -rsp 'DigitalOcean API token: ' DIGITALOCEAN_API_TOKEN && echo && export DIGITALOCEAN_API_TOKEN && make -C deploy/digitalocean/droplet build; unset DIGITALOCEAN_API_TOKEN
```

The build provisions a temporary `s-1vcpu-1gb` droplet in `nyc3` with Docker
and the release's images, runs DigitalOcean's cleanup and image checks on it,
and saves it as a snapshot named `simple-host-24-04-snapshot-<time>`. Packer
deletes the droplet afterwards. No admin key, password, certificate or database is in the image; the
installer makes them on the droplet at first login.

## By hand

1. In the DigitalOcean control panel, create a droplet: image **Ubuntu 24.04
   (LTS) x64**, size `s-1vcpu-1gb`, any region, and your SSH key.
2. At your DNS provider, add two A records pointing at the droplet's public
   IPv4 address: your domain, and `*.<your domain>`.
3. Answer the questions at https://simple-host.app/setup?product=small-box
   with **a server I already have**, then run the install command it gives on
   the droplet over SSH. It fetches the installer from its pinned release and
   checks its sha256 before running it.
4. Open `https://<your domain>/admin` and paste the admin key the installer
   printed (also kept as `ADMIN_API_KEY` in `/opt/simple-host/.env`).

If anything fails, paste the error at
https://simple-host.app/setup?product=small-box#help.

## Everyday operations

- **Admin key lost:** `grep ^ADMIN_API_KEY= /opt/simple-host/.env` on the droplet.
- **Upgrade:** run the install command from https://simple-host.app/setup?product=small-box
  again on the droplet. The installer is also the upgrade: it pulls the release
  it pins, applies that release's database changes, and keeps your data, key
  and settings.

## Removing it

Destroy the droplet in the DigitalOcean control panel (Destroy → Destroy
Droplet), with its backups and snapshots if you made any, then remove the two
DNS records. Nothing else was created.
