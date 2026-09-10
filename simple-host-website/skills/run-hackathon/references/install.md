# Installing

One command over SSH on the fresh box.

**`<user>` is not always `root`.** UpCloud, Hetzner, DigitalOcean and Vultr give
you `root`. **Oracle gives you `ubuntu` and refuses root outright**, which is why
the script is fetched to `/tmp` and run with `sudo`: `/root` is not writable by
the user you land as.

`StrictHostKeyChecking=accept-new` matters when nobody is watching: the first
connection to a brand-new machine otherwise stops to ask whether you trust its
fingerprint, and an unattended agent simply hangs there.

```bash
ssh -o StrictHostKeyChecking=accept-new -i ~/.ssh/hackathon_key <user>@<ip> \
  'curl -fsSL https://raw.githubusercontent.com/vineetu/simple-host/main/deploy/install/install.sh -o /tmp/install.sh && \
   sudo bash /tmp/install.sh --host <event>.<domain> --content sites.<event>.<domain> --image ghcr.io/vineetu/simple-host:0.1.1'
```

**Pin the image.** `latest` moves on every release, so an unattended re-run can
pull a build that does not match the compose file it fetched. Use the newest
published tag from the repository's releases page rather than `latest`, and use
the same tag if you re-run.

Optional flags:

- `--email you@example.com` — where Let's Encrypt sends expiry notices.
- `--ref <branch-or-tag>` — where the compose file and schema are fetched from.

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

**A `404` here means DNS, not the install.** The request reached something else,
usually the domain's wildcard, because the records have not propagated or point
elsewhere. Check what the name resolves to before touching the server.

## If something is wrong

```bash
ssh -i ~/.ssh/hackathon_key <user>@<ip> 'cd /opt/simple-host && docker compose ps && docker compose logs --tail 40'
```

Caddy repeatedly failing to get a certificate almost always means DNS is not
pointing at this machine yet, or port 80 is blocked.
