# Per-person addresses: the nginx step left for the owner

Status 2026-09-25: person addresses (`https://<handle>.simple-host.app/<site>/`) are live and
canonical (`PERSON_HOSTS=canonical`). The old `https://sites.simple-host.app/<handle>/<site>/`
links are still served exactly as before, because the content-host nginx block
(`/etc/nginx/sites-enabled/sites-content-host`) holds a plaintext secret and was deliberately not
opened. This is the one change to make there, by hand.

The Go side is already deployed: `GET /internal/site-redirect/{handle}[/{site}[/{rest...}]]`
(`internal/handler/personhost.go`, `contentHostRedirect`). In canonical mode it answers **302**
to the person address (path and query kept; an old handle kept as an alias — `admin` — maps to
its new one; a site with its own domain goes to the domain). A site pinned to the content host
(`vineetu/eb2-wait`, see below) or any request while `PERSON_HOSTS` is not `canonical` is
**served from disk by Go**, exactly as nginx serves it today. So the nginx change is safe to
apply and safe to leave in place if the flag is ever turned back.

## 1. The change (content host server block)

Back up first: `sudo cp -p /etc/nginx/sites-available/sites-content-host /etc/nginx/sites-content-host.bak-$(date +%Y%m%d-%H%M%S)`

In the `location ~ "^/(?<h>[a-z0-9-]{1,39})/(?<s>[a-z0-9-]{1,63})(?<rest>/.*)?$"` block, add the
three lines marked `+`, after the existing `domain-redirect` `if`:

```nginx
    location ~ "^/(?<h>[a-z0-9-]{1,39})/(?<s>[a-z0-9-]{1,63})(?<rest>/.*)?$" {
        # A site that has its own domain lives there: the app answers a 302.
        if (-f /srv/simple-host/sites/handles/$h/$s/domain-redirect) {
            rewrite ^ /internal/domain-redirect/$h/$s$rest last;
        }
+       # Sites moved to <handle>.simple-host.app (2026-09-25). eb2-wait stays: it needs /eb2-api/ here.
+       set $sh_site "$h/$s";
+       if ($sh_site != "vineetu/eb2-wait") { rewrite ^ /internal/site-redirect/$h/$s$rest last; }
        alias /srv/simple-host/sites/handles/$h/$s/current$rest;
        index index.html;
        error_page 404 = @notfound;
    }
```

`rewrite ... last` re-enters location matching with `/internal/...`, which the existing
`location ^~ /internal/` proxies to the app. (A directory without its trailing slash, served by
Go when the flag is not canonical, redirects to the public `/<handle>/<site>/...` path Go
rebuilds itself; no extra header is needed.)

Optional, the person's page: in `location ~ "^/(?<sh>[a-z0-9-]{1,39})/?$"` change
`proxy_pass http://127.0.0.1:8090/internal/showcase/$sh;` to
`proxy_pass http://127.0.0.1:8090/internal/site-redirect/$sh;` (302 to
`https://<handle>.simple-host.app/`; it renders the page itself when not canonical).

Then: `sudo nginx -t && sudo systemctl reload nginx`.

Leave `location ^~ /v1/`, `/internal/`, the `/eb2-api/*` locations and the bare `/<h>/<s>` 308
alone: `/v1/` keeps answering on the content host forever (old pages call it).

## 2. Verify from a client

```
curl -sI https://sites.simple-host.app/vineetu/madurai-idly/ | grep -iE '^(HTTP|location)'     # 302 -> https://vineetu.simple-host.app/madurai-idly/
curl -sI 'https://sites.simple-host.app/vineetu/madurai-idly/x?y=1' | grep -i '^location'      # path + query kept
curl -sI https://sites.simple-host.app/admin/form-coach-design/ | grep -i '^location'          # -> simple-host-team.simple-host.app
curl -sI https://sites.simple-host.app/vineetu/ielts/ | grep -i '^location'                    # -> ielts.vineetsriram.com (own domain)
curl -s -o /dev/null -w '%{http_code}\n' https://sites.simple-host.app/vineetu/eb2-wait/       # 200, still served here
curl -s -o /dev/null -w '%{http_code}\n' https://sites.simple-host.app/v1/u/vineetu/sites/madurai-idly/state -H 'Origin: https://sites.simple-host.app'   # 200
```

Rollback: restore the `.bak` and reload.

## 3. After ~7 quiet days: 301

Change `http.StatusFound` to `http.StatusMovedPermanently` in `contentHostRedirect` (the two
person-address redirects only; keep the own-domain one a 302 so disconnecting a domain still
takes effect at once), rebuild, deploy. A 301 is cached by browsers indefinitely, which is why
it waits for a soak.

## 4. Moving eb2-wait (so it can leave the content host)

`vineetu/eb2-wait` calls root-absolute `/eb2-api/chat` and `/eb2-api/stt-ws`, which only the
content-host block serves (with `if ($http_origin != "https://sites.simple-host.app") { return 403; }`
and the sidecar token). To move it:

1. Add the same three `location = /eb2-api/...` blocks to the wildcard `*.simple-host.app` server
   in `/etc/nginx/sites-enabled/simple-host`, guarded to the one host, e.g. wrap with
   `if ($host != "vineetu.simple-host.app") { return 404; }` and change the Origin check to
   `https://vineetu.simple-host.app`. The token line must be copied from the content-host file by
   hand (it is the secret).
2. Remove `"vineetu/eb2-wait": true` from `contentHostOnlySites` in
   `internal/handler/personhost.go`, rebuild, deploy.
3. Drop the `$sh_site` exception from the content-host block (it then redirects like the rest).
