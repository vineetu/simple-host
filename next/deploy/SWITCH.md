# Switch: live Simple Host (old, :8090) to simple-host-next (:8095)

Draft. Nothing here is installed. Run as root on the box, in order. Each step says what to
check and how to go back from that point. `OLD` = `simple-host.service`, `NEW` = `simple-host-next.service`.

## Before (all must hold; RUNBOOK "Switch")
- `cd next && go build ./... && go vet ./... && go test -count=1 ./...` green; the route-audit fixes are in.
- Two `sh-copy copy` + `sh-copy check` runs in a row print nothing.
- Every address answers through the new vhost on a test port (`curl --resolve <host>:443:127.0.0.1`).
- One Let's Encrypt staging certificate issued by the service; your own Claude and ChatGPT work on copied tokens.
- Top 20 sites and every recipe pass in a real browser; the `sh-copy report` emails are approved.
- Google sign-in: nothing to change at Google; the new service uses the same redirect URI (`/v1/auth/oauth/google/callback`, commit 80117f160).
- Decide per hand-made vhost (step 5 table). Move the Hermes WhatsApp pair page off
  `sites.simple-host.app/vineetu/wa-pair/` (path-style links stop at the switch) and update `hermes-wa-doctor`.

## 0. Prepare (no user-visible change) — about 30 minutes, any time before
```sh
systemctl stop sh-next-rehearsal; systemctl reset-failed sh-next-rehearsal || true   # frees :8095
mv /srv/simple-host-next /srv/simple-host-next.rehearsal-$(date +%F)                   # rehearsal data used a throwaway passcode key
useradd --system --home-dir /srv/simple-host-next --shell /usr/sbin/nologin simplehost-next
usermod -aG simplehost simplehost-next      # read-only reach into hard-linked version files (owner simplehost)
install -d -o simplehost-next -g www-data -m 0710 /srv/simple-host-next
install -d -o simplehost-next -g www-data -m 2750 /srv/simple-host-next/certs   # setgid: nginx (www-data) reads certs
install -d -o simplehost-next -g simplehost-next -m 0750 /srv/simple-host-next/geoip
install -o simplehost-next -m 0640 /srv/simple-host/geoip/dbip-city-lite.mmdb /srv/simple-host-next/geoip/
sudo -u postgres createuser simplehost_next -P && sudo -u postgres createdb -O simplehost_next simplehost_next
sudo -u postgres psql -c "CREATE ROLE sh_copy_ro LOGIN PASSWORD '…' IN ROLE pg_read_all_data"   # old DB, read only
install -m 640 -o root -g simplehost-next env.example /etc/simple-host-next.env   # then fill values (see env.example)
cd /home/ubuntu/workspace/wt-next/next && go build -o /tmp/shn ./cmd/simple-host-next && go build -o /tmp/sh-copy ./cmd/sh-copy
install -m 755 /tmp/shn /usr/local/bin/simple-host-next && install -m 755 /tmp/sh-copy /usr/local/bin/sh-copy
install -m 644 simple-host-next.service /etc/systemd/system/ && systemctl daemon-reload
install -m 644 nginx-simple-host-next.conf /etc/nginx/sites-available/simple-host-next     # NOT enabled yet
pg_dump -Fc simplehost > /var/backups/simplehost-$(date +%F).dump                           # old DB name: check DB_DSN
```
Check: `id simplehost-next`; `systemd-analyze verify /etc/systemd/system/simple-host-next.service`; dump file size > 0.
Back: `userdel simplehost-next; dropdb simplehost_next`; remove the files. Nobody noticed anything.

## 1. First copy and start NEW on :8095 (no user-visible change)
```sh
set -a; . /etc/simple-host-next.env; set +a
export OLD_DATABASE_URL=postgres://sh_copy_ro:…@127.0.0.1/simplehost OLD_DATA_DIR=/srv/simple-host \
       OLD_CERT_READY_DIR=/var/lib/simple-host-site-certs/ready OLD_LETSENCRYPT_DIR=/etc/letsencrypt/live
sh-copy copy && sh-copy check          # root: it reads /etc/letsencrypt/live
chown -R simplehost-next /srv/simple-host-next; chgrp -R www-data /srv/simple-host-next/certs
systemctl enable --now simple-host-next
```
Check: `curl -s -H 'Host: simple-host.app' http://127.0.0.1:8095/healthz` → `{"ok":true}`;
`journalctl -u simple-host-next -n 50` has no errors; `sudo -u simplehost-next cat <one copied version file> >/dev/null`
(proves the hard-linked files are readable); `sudo -u www-data ls /srv/simple-host-next/certs/` lists the own domains.
Back: `systemctl disable --now simple-host-next`. Nothing lost (no one has used NEW).

## 2. Test the new nginx file on a spare port (no user-visible change)
Make a copy with `listen 443 ssl` → `listen 127.0.0.1:8443 ssl` (drop `default_server` on 80 and 443 and the port-80
server), enable it, `nginx -t && systemctl reload nginx`, then for each address class:
```sh
for h in simple-host.app vineetu.simple-host.app <site>.<handle>.simple-host.app <site>--<handle>.simple-host.site \
         <own domain> <label>.quotes.chhotabreak.com <short name>.simple-host.app; do
  curl -s -o /dev/null -w "%{http_code} $h\n" --resolve $h:8443:127.0.0.1 https://$h:8443/; done
```
Check: each answers with the right certificate (`openssl s_client -connect 127.0.0.1:8443 -servername $h </dev/null | openssl x509 -noout -subject -enddate`).
Back: remove the test file, reload. Nothing lost.

## 3. Freeze OLD and take the final copy — downtime starts (writes stop; nginx still serves OLD's static files)
```sh
systemctl stop simple-host-domain-certs.timer simple-host-family-certs.timer simple-host-site-certs.timer simple-host-site-certs-site.timer
systemctl stop simple-host-domain-certs.path simple-host-family-certs.path simple-host-site-certs.path simple-host-site-certs-site.path
systemctl stop simple-host
systemctl stop simple-host-next
sh-copy copy && sh-copy check          # must print nothing
chown -R simplehost-next /srv/simple-host-next; chgrp -R www-data /srv/simple-host-next/certs
systemctl start simple-host-next
```
Check: `sh-copy check` empty, exit 0; `/healthz` ok on :8095.
Back: `systemctl start simple-host` and the timers/paths. Lost: nothing (NEW not public yet). Downtime so far: minutes.

## 4. Switch nginx — NEW goes live
```sh
mkdir -p /etc/nginx/sites-old
cd /etc/nginx/sites-enabled && mv simple-host simple-host-site sites-content-host simple-host-domain-* simple-host-family-* /etc/nginx/sites-old/
ln -s /etc/nginx/sites-available/simple-host-next /etc/nginx/sites-enabled/simple-host-next
nginx -t && systemctl reload nginx
```
Check (from outside, e.g. a curl with no --resolve): `https://simple-host.app/healthz` (what sh-uptime-watch probes), `/.well-known/oauth-protected-resource/mcp`,
`/auth.js`, `/llms.txt`; an open site on each address class; a passcode site; a named-viewer site; an own domain;
a Chhota Break `<label>.quotes.chhotabreak.com`; `POST /mcp` initialise from your Claude; sign in with Google and with a code;
publish a test site with a key sent as `X-API-Key`; `tail -f /var/log/nginx/error.log` shows no certificate load errors;
unrelated sites still answer: `for h in gods-eye.simple-host.app sf-fog.today jot-transcribe.com orijournal.app sf-fog.lab.simple-host.app simple-hack.app; do curl -sI https://$h | head -1; done`.
If `nginx -t` fails: nothing has changed; fix, or move the files back.
Back (fast): `rm /etc/nginx/sites-enabled/simple-host-next; mv /etc/nginx/sites-old/* /etc/nginx/sites-enabled/; nginx -t && systemctl reload nginx; systemctl stop simple-host-next; systemctl start simple-host` and the cert timers/paths.

## 5. Hand-made vhosts that still proxy to :8090
These are outside the generated files; each is decided before the switch and done right after step 4.
| File | Today | At the switch |
|---|---|---|
| `customdomain-agent-deploy.dev`, `customdomain-call-assist.agent-deploy.dev`, `vineetsriram.com` | own domain, old farm + /v1 to 8090 | If `psql simplehost_next -c "select host,status from hostnames where host in (…)"` lists it and `certs/<host>/` exists: move the file to `sites-old/` (the default server takes it). |
| `ielts.vineetsriram.com` | old farm + /v1 to 8090, plus `/ielts-api/*` to :8104 | Keep the file; replace its `root`/`/v1/`/`/internal/` parts with one `location / { proxy_pass http://simple_host_next; … }` (same headers as the new file); keep `/ielts-api/*`. |
| `paragliding-beginners-map.simple-host.app` | one site from the old farm | If it is a short name in `names`: move to `sites-old/`. Else keep (serves the frozen old files; its passcode/offline pages break). |
| `sub-lab.simple-host.app` (`*.lab.simple-host.app`) | old farm by folder name | Not an address shape of NEW. Keep as is (static, frozen) and tell the owner, or retire it. |
`nginx -t && systemctl reload nginx` after each. Back: put the file back from `sites-old/`.

## 6. Certificates after the switch
```sh
systemctl disable simple-host-domain-certs.timer simple-host-family-certs.timer simple-host-site-certs.timer simple-host-site-certs-site.timer \
  simple-host-domain-certs.path simple-host-family-certs.path simple-host-site-certs.path simple-host-site-certs-site.path
grep -l -R 'deploy_hook\|renew_hook' /etc/letsencrypt/renewal/ ; ls /etc/letsencrypt/renewal-hooks/*/
certbot renew --dry-run
```
Check: `certbot.timer` still enabled; renewal hooks only copy certificates (the site-certs deploy hook into
`/etc/nginx/simple-host-site-certs/<handle>/` must STAY: the per-handle block reads those copies) and none writes a
vhost into sites-enabled; the dry run passes for `simple-host.app`, `simple-host.site` and a per-handle lineage.
The old own-domain lineages in /etc/letsencrypt now renew via webroot `/var/www/acme` (still served first on port 80);
NEW renews its own copies in `certs/`.
Back: re-enable the units only together with step 4's way back.

## 7. Watch (first 48 hours), then keep OLD for 30 days
`journalctl -u simple-host-next -f`; nginx error.log; `/v1/admin/overview`; connected apps refreshing tokens.
Keep `simple-host.service` (disabled), its database, `/srv/simple-host` and `sites-old/` untouched for 30 days.

## What going back loses once people have used NEW
Going back = step 4's way back. OLD restarts from the moment of step 3; nothing written on NEW is carried back:
- new accounts and sign-ins (people sign in again; new accounts do not exist on OLD), handle and display-name changes;
- keys made or revoked on NEW (a revoked key works again on OLD);
- AI app connections made on NEW, and refresh tokens rotated on NEW (if NEW rotates refresh tokens, apps that
  refreshed on NEW must reconnect on OLD);
- sites published, rolled back, deleted, restored, taken offline; passcode, named-viewer, domain and short-name changes;
- saved data: rows, values and uploaded files written to namespace SQLite files and file stores
  (copy back by hand from `/srv/simple-host-next/sites/<id>/data/*.sqlite` if needed);
- own and shared domains connected on NEW and the certificates it issued;
- visit numbers counted on NEW, push devices registered, exports queued (emailed links stop working), Ask-for-access requests.
Keep the window short; decide go or back within the first hours.
