# Where it runs

- Simple Host on one server: [UpCloud or any Ubuntu VPS](/docs/self-hosting/platforms/upcloud.md), or use the [setup helper](https://simple-host.app/setup).
- Kubernetes / Enterprise: use Kubernetes and the [Enterprise guide](https://github.com/vineetu/simple-host-enterprise/blob/main/docs/cloud/digitalocean.md).

Simple Host needs a process that stays on, persistent disk and Postgres.
Choose a provider guide below.

| Provider | Guide |
|---|---|
| UpCloud or any Ubuntu VPS | [Install Simple Host](/docs/self-hosting/platforms/upcloud.md) |
| DigitalOcean | [Install Simple Host](/docs/self-hosting/platforms/digitalocean.md) |
| Coolify | [Install Simple Host](/docs/self-hosting/platforms/coolify.md) |
| Fly.io | [Install Simple Host](/docs/self-hosting/platforms/fly.md) |
| Railway | [Install Simple Host](/docs/self-hosting/platforms/railway.md) |
| Render | [Install Simple Host](/docs/self-hosting/platforms/render.md) |

Vercel, Netlify and shared PHP hosting do not provide the persistent disk and
always-on process this installation needs.
