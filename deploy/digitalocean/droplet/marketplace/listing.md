# Simple Host: DigitalOcean Marketplace listing

Vendor material for the 1-Click submission, which has not been sent yet.

## App name

Simple Host

## Short description

Host static websites with a small built-in backend for forms, sign-ups and votes, on one droplet with HTTPS.

## Long description

Simple Host hosts static websites and gives each one a small JSON backend in
the same upload, so a page can save an RSVP, count a vote or keep a guestbook
without a separate server. Sites are published by an AI coding agent (Claude
Code, Codex and others, with the Simple Host skill) or by a plain HTTP API.
Sites are served over HTTPS at `https://<sites address>/<handle>/<site>/`.

It is one Go server, Postgres and Caddy, run with Docker Compose. The whole
thing is sized for the smallest droplet with 1 GB of memory.

On first login the droplet asks for your domain and shows which DNS records to
add. Once they resolve, it installs Simple Host from a pinned release. It comes
with an admin dashboard and account keys for the people who publish. Every
site keeps versions you can roll back to, and visitor analytics stay on the
droplet. Visitors can sign in to save data with Google or an emailed code once
you add a Google client or a Resend key to `/opt/simple-host/.env`
(`GOOGLE_OAUTH_CLIENT_ID` and `GOOGLE_OAUTH_CLIENT_SECRET`, or `RESEND_API_KEY`).

## Software included

| Software | Version | License |
|---|---|---|
| Simple Host | 0.7.12 | MIT |
| Docker Engine (docker-ce) and the Docker Compose plugin | latest stable at build time | Apache 2.0 |
| Caddy (`caddy:2-alpine` image) | 2.x | Apache 2.0 |
| PostgreSQL (`postgres:16-alpine` image) | 16 | PostgreSQL License |
| Ubuntu | 24.04 LTS | open source (GPL and others) |

## Getting started (shown on the droplet page)

1. Log in: `ssh root@your_droplet_public_ipv4`. Setup starts by itself.
2. Enter your domain (like `hosting.example.com`) and the address sites are served from (`sites.hosting.example.com` by default).
3. At your DNS provider, add A records for your domain and `*.your-domain` pointing at the droplet's IPv4 address (and one for the sites address if it is outside your domain). Setup checks them and waits if they are not there yet.
4. When the install finishes, open `https://your-domain/admin` and sign in with the admin key it printed. To see the key again: `grep ^ADMIN_API_KEY= /opt/simple-host/.env`.

Full guide: https://github.com/vineetu/simple-host/blob/main/docs/platforms/digitalocean.md

## Support

- Email: support@simple-host.app
- Guide: https://github.com/vineetu/simple-host/blob/main/docs/platforms/digitalocean.md
- Website: https://simple-host.app

## Recommended size

Basic droplet, Regular CPU, `s-1vcpu-1gb` (1 vCPU, 1 GB memory, 25 GB disk),
about $6 a month. The $4 droplet (512 MB) is too small. Pick a larger disk if
the sites will hold large files.

## Category and tags

Category is Website Hosting (or Developer Tools). Tags are static sites, web hosting, docker, ai agents.

## Ports

22 (SSH), 80 (HTTP) and 443 (HTTPS). UFW is enabled and blocks everything else. A port published from a Docker container later bypasses UFW.

Your public person page can open an owned home site. Curate the showcase with a
short bio, pinned projects and manual order; custom homes use its live JSON feed.
The default path addresses require no wildcard DNS.
