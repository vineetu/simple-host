# Installing

One command over SSH, as root, on the fresh box.

```bash
ssh -i ~/.ssh/hackathon_key root@<ip> \
  'curl -fsSL https://raw.githubusercontent.com/vineetu/simple-host/main/deploy/install/install.sh -o /root/install.sh && \
   bash /root/install.sh --host <event>.<domain> --content sites.<event>.<domain>'
```

Optional flags:

- `--email you@example.com` — where Let's Encrypt sends expiry notices.
- `--image ghcr.io/vineetu/simple-host:0.1.0` — pin a version instead of `latest`.

## What it does

Installs Docker, writes `/opt/simple-host/.env`, pulls the published image and
starts three containers: Caddy, the server and Postgres. Nothing is compiled on
the box.

**It is idempotent.** If it fails partway, run the same command again. The admin
key and database password are generated once and preserved across re-runs, so a
retry will not lock the organiser out or break the database.

## The output

```json
{"host":"https://builds.example.com","content_host":"https://sites.builds.example.com","admin_api_key":"sh_admin_...","dir":"/opt/simple-host"}
```

**Give the admin key to the organiser immediately.** It is displayed once.
Nothing else can show it, and without it they are not the administrator of their
own instance.

## Certificates

Caddy requests them on the first HTTPS request to each hostname. Allow a minute.
Port 80 must be reachable from the internet, because that is how the challenge
is answered.

Confirm before handing over:

```bash
curl -sS -o /dev/null -w '%{http_code}\n' https://<event>.<domain>/healthz
```

`200` means the instance is live with a valid certificate.

## If something is wrong

```bash
ssh -i ~/.ssh/hackathon_key root@<ip> 'cd /opt/simple-host && docker compose ps && docker compose logs --tail 40'
```

Caddy repeatedly failing to get a certificate almost always means DNS is not
pointing at this machine yet, or port 80 is blocked.
