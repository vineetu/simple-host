# Getting started with the Simple Host 1-Click

You need a domain whose DNS you can change.

## 1. Log in

```sh
ssh root@your_droplet_public_ipv4
```

Setup starts by itself on the first login. It shows the droplet's public IPv4
address and asks for two addresses:

- **Address for the dashboard**, like `hosting.example.com`.
- **Address the sites are served from**. Press Enter for `sites.hosting.example.com`.

## 2. Add the DNS records

At your DNS provider, add two A records pointing at the droplet's IPv4
address: your domain, and `*.your-domain` (which covers the sites address).
If you chose a sites address outside your domain, add a third A record for it.
If the provider proxies traffic (Cloudflare's orange cloud), set these to DNS
only.

Setup checks the records. If they are not there yet, press `r` to keep
checking for up to 5 minutes, `c` to continue anyway (HTTPS starts working once
DNS points at the droplet), or `q` to stop and come back later.

## 3. Install

Setup fetches the Simple Host installer from its pinned release and runs it
once its sha256 matches. If the SSH connection drops, the install goes on; log
in again to see the result. It prints the admin key at the end.

## 4. Sign in

Open `https://your-domain/admin` and paste the admin key. From there, issue
accounts for the people who will publish sites.

To see the admin key again:

```sh
grep ^ADMIN_API_KEY= /opt/simple-host/.env
```

## If setup stopped

Setup runs until an install succeeds. It runs again at your next login, or now:

```sh
/opt/simple-host-setup/first-login.sh
```

The installer's output is in `/root/simple-host-install-<date>-<time>.log`. For help, paste
the error at https://simple-host.app/setup?product=small-box#help or write to
support@simple-host.app.

## Where things are

| What | Where |
|---|---|
| Settings and the admin key | `/opt/simple-host/.env` |
| Status | `cd /opt/simple-host && docker compose ps` |
| Logs | `cd /opt/simple-host && docker compose logs --tail 50` |
| Upgrade | `/opt/simple-host-setup/upgrade.sh` (keeps your addresses, data and key) |
