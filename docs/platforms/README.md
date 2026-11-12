# Where it runs

- Simple Hack on one server: [install on your own server](simple-hack-standalone.md).
- Simple Host on one server: [UpCloud or any Ubuntu VPS](upcloud.md), or use the [setup helper](https://simple-host.app/setup).
- Kubernetes / Enterprise: use [Kubernetes](../kubernetes.md) and the [Enterprise guide](https://github.com/vineetu/simple-host-enterprise/blob/main/docs/cloud/digitalocean.md).

Simple Host needs a process that stays on, persistent disk and Postgres.
Choose a provider guide below.

| Provider | Guide |
|---|---|
| UpCloud or any Ubuntu VPS | [Install Simple Host](upcloud.md) |
| DigitalOcean | [Install Simple Host](digitalocean.md) |
| Coolify | [Install Simple Host](coolify.md) |
| Fly.io | [Install Simple Host](fly.md) |
| Railway | [Install Simple Host](railway.md) |
| Render | [Install Simple Host](render.md) |
| Coolify | [Install Simple Hack](simple-hack-coolify.md) |
| DigitalOcean | [Install Simple Hack](simple-hack-digitalocean.md) |

Vercel, Netlify and shared PHP hosting do not provide the persistent disk and
always-on process this installation needs.
