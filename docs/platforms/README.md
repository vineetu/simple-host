# Where it runs

The small box needs three things: a process that is always on, a disk that
keeps its files, and Postgres. Anywhere that gives all three can run it.

| Where | How | Status |
|---|---|---|
| UpCloud (recommended) | [this guide](upcloud.md): the installer on the smallest server | tested 2026-09-28, about $4/month; [$25 in credits](https://signup.upcloud.com/?promo=JF2WCV) (referral link) |
| Any other Ubuntu VPS | the installer, [simple-host.app/setup](https://simple-host.app/setup) | the same installer |
| Hostinger VPS | the installer | an Ubuntu VPS like any other |
| DigitalOcean | [this guide](digitalocean.md): the 1-Click droplet, or the installer on the $6 droplet | not yet tested on DigitalOcean |
| Fly.io | [this guide](fly.md) | tested 2026-09-28, about $4.50/month |
| Render | [this guide](render.md) | tested 2026-09-28, about $13.55/month (needs a card) |
| Railway | | not yet tested |
| Vercel, Netlify, shared PHP hosting | | not as-is: a 4.5 MB request limit (smaller than a site upload), no disk that keeps files, no always-on process |

One diagram per layout, at the top of each guide:
[UpCloud, DigitalOcean and any Ubuntu server](https://simple-host.app/diagrams/upcloud.svg) ·
[Fly.io](https://simple-host.app/diagrams/fly.svg) ·
[Render](https://simple-host.app/diagrams/render.svg).
Enterprise on AWS has [its own](https://simple-host.app/diagrams/enterprise-aws.svg).
Enterprise on DigitalOcean Kubernetes is a Helm chart and a 1-Click:
[its guide](https://github.com/vineetu/simple-host-enterprise/blob/main/docs/cloud/digitalocean.md).
