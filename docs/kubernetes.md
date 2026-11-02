# Simple Hack on Kubernetes

One container image, one pod, your Postgres, your load balancer. This is the
runbook for an internal hackathon run inside a company: the chart is
`deploy/kubernetes/simple-hack/`, the same chart rendered to plain YAML is
`deploy/kubernetes/manifests/simple-hack.yaml`, and the image is
`ghcr.io/vineetu/simple-host:hack-<release>` (the Simple Hack release build;
0.8.14 is the first that can build its own database, which this needs).

The pod runs `simple-host` in Hack mode (`EVENTS=hosted`): organisers sign in
at `https://<host>/` and create events, each event's page is
`https://<event>.<host>/`, and each team's site is its own origin,
`https://<team>.<event>.<host>/` (so no team's page can read another's
visitor sessions). TLS ends at your load balancer; the pod speaks plain HTTP
on 8090.

## Prerequisites

Have these before `helm install`; the chart cannot make them.

1. **A Postgres 16 database and a role that owns it, not a superuser.**
   No extensions.

   ```sql
   CREATE ROLE hack LOGIN PASSWORD '...';
   CREATE DATABASE hack OWNER hack;
   ```

   The pod builds the schema in an empty database on its first start and
   applies migrations on every start (`MIGRATE_ON_START=true`, under a
   Postgres advisory lock, so a second pod starting at the same moment
   waits). Keep the connection string handy:
   `postgres://hack:PASSWORD@<pg-host>:5432/hack?sslmode=require`.
   The server opens at most 25 connections (`DB_MAX_OPEN_CONNS`).

2. **DNS: the apex and one wildcard**, both to the address of the load
   balancer in front of your ingress controller: `hack.example.com` and
   `*.hack.example.com`. A DNS wildcard matches more than one label, so that
   one record also resolves `team.event.hack.example.com`. Inside a company
   these are records in the internal zone. Check `hack.example.com`,
   `anything.hack.example.com` and `a.b.hack.example.com` all resolve before
   installing.

3. **The load balancer and TLS.** A certificate for `hack.example.com`,
   `*.hack.example.com` (event pages) and, for each event you will run,
   `*.<event>.hack.example.com` (its team sites): a wildcard certificate
   covers one label only, so the event's wildcard has to be a SAN of its
   own. Decide the event's slug (what the organiser types when creating it;
   `spring-hack` gives `spring-hack.hack.example.com`) and get the
   certificate issued with that SAN. Inside a company that is one request to
   the internal CA; a public CA issues it too (Let's Encrypt with a DNS
   challenge). Terminate TLS at the load balancer or the ingress controller.
   Whatever terminates it must forward `X-Forwarded-Proto: https` and append
   the client address to `X-Forwarded-For`; every common load balancer and
   ingress controller does. Without `X-Forwarded-Proto` the sign-in cookies
   lose their `Secure` flag; without a sane `X-Forwarded-For` every
   participant shares one rate limit. Uploads are up to 64 MB per request
   (`MAX_ARCHIVE_MB`, default 25 in Hack, plus overhead): raise the proxy's
   body limit to match (the chart's ingress-nginx annotations do).

   The Ingress needs the same list: an Ingress host wildcard also matches one
   label, so the chart adds a `*.<event>.hack.example.com` rule for every
   slug under `ingress.events`. An event created later is one line and a
   `helm upgrade` away (or `ingress.catchAll: true` when the controller is
   this app's alone).

4. **An OIDC client at your identity provider** (Entra ID, Okta, Google
   Workspace, Keycloak): a confidential web client, authorization-code
   grant, with exactly this redirect URI:

   ```
   https://hack.example.com/v1/auth/oauth/oidc/callback
   ```

   Note the issuer URL, client ID and client secret. Provider-by-provider
   steps are in [simple-hack-standalone.md](platforms/simple-hack-standalone.md#registering-the-redirect-uri).
   Without an identity provider, sign-in is by emailed code through Resend
   (`signIn.email`), which needs the pod to reach api.resend.com.

5. **A storage class** that can give a ReadWriteOnce volume (every default
   one can). The volume holds every site; 20 GB is plenty for a hackathon.

6. `kubectl` and `helm` 3 pointed at the cluster, and a namespace.

## Install

Copy [`deploy/kubernetes/examples/values-example.yaml`](../deploy/kubernetes/examples/values-example.yaml)
to `my-values.yaml` and fill in the "set this" lines (`host`,
`database.dsn`, `signIn.oidc.issuer`, `clientId`, `clientSecret`) and the
event slug under `ingress.events`. Every other value is documented in
[`values.yaml`](../deploy/kubernetes/simple-hack/values.yaml). Then:

```sh
helm install simple-hack deploy/kubernetes/simple-hack \
  --namespace simple-hack --create-namespace \
  -f my-values.yaml
kubectl -n simple-hack rollout status deploy/simple-hack
kubectl -n simple-hack logs deploy/simple-hack
```

The log shows `MIGRATE_ON_START: building the schema if the database is
empty`, `built the schema from db/schema.sql`, the migrations being
recorded, then `hosted events: on (the hackathon platform at
https://hack.example.com)` and the listen line. The pod is Ready when
`/readyz` can ping Postgres.

Check from outside:

```sh
curl -sS https://hack.example.com/healthz          # {"status":"ok"}
curl -sS -o /dev/null -w '%{http_code}\n' https://hack.example.com/   # 200
```

Without Helm: create the Secret from
[`manifests/secret.example.yaml`](../deploy/kubernetes/manifests/secret.example.yaml),
edit the hostnames in
[`manifests/simple-hack.yaml`](../deploy/kubernetes/manifests/simple-hack.yaml),
and `kubectl apply -f` both. Re-render your own copy with
`deploy/kubernetes/render.sh` after changing the example values.

### Pod security

The pod meets the Kubernetes *restricted* Pod Security Standard: non-root
(uid 65532), read-only root filesystem, no capabilities, seccomp
`RuntimeDefault`, no service-account token. The namespace can carry
`pod-security.kubernetes.io/enforce=restricted`. It writes only to `/data`
(the volume) and `/tmp` (an emptyDir).

## First sign-in and the first admin

There is no admin step. The first person to open `https://hack.example.com/`
signs in with the company account, lands on their home page and creates the
event; they are its organiser. Anyone else who signs in and has the event's
join link, judge link or applies from the event page takes part. Nothing
about the first account is special.

The *platform admin* is not a person: it is the `ADMIN_API_KEY`, used as an
`X-API-Key` header against `/v1/admin/*` (list every event, take a site
down, create key-only accounts for a bot or a test). The chart generated it
on the first install and keeps it across upgrades:

```sh
kubectl -n simple-hack get secret simple-hack-secrets \
  -o jsonpath='{.data.ADMIN_API_KEY}' | base64 -d
```

Keep it out of chat and tickets. With `signIn.email.enabled: "off"` (the
example values), only accounts from the identity provider can sign in.
`signIn.oidc.allowedEmailDomains` refuses anyone outside the listed domains
even when the provider would have let them through.

## Upgrades

A release is a new image tag. Migrations run on start, so an upgrade is:

```sh
helm upgrade simple-hack deploy/kubernetes/simple-hack \
  --namespace simple-hack -f my-values.yaml \
  --set image.tag=hack-0.8.15
kubectl -n simple-hack rollout status deploy/simple-hack
```

The Deployment's strategy is `Recreate`: the old pod stops, the new one
starts, applies the release's pending migrations, then listens. Expect about
thirty seconds of downtime per upgrade; do it between sessions, not during
judging. A migration that fails leaves the pod in `CrashLoopBackOff` with
the reason in its log and the database as it was (each file runs in its own
transaction); `helm rollback simple-hack` brings the previous image back,
and a database the newer release already migrated still runs the older
binary (migrations are additive).

Changing a value restarts the pod (the settings are read at start). The
generated secrets (`ADMIN_API_KEY`, `PASSCODE_ENC_KEY`, `ANALYTICS_SALT`)
are read back from the live Secret on every upgrade and never rotate on
their own; the Secret and the volume claim survive `helm uninstall`.

To prepare a database by hand instead (`migrateOnStart: false`), run the
same image once:

```sh
kubectl -n simple-hack run migrate --rm -it --restart=Never \
  --image ghcr.io/vineetu/simple-host:hack-0.8.14 \
  --env DB_DSN="$(kubectl -n simple-hack get secret simple-hack-secrets -o jsonpath='{.data.DB_DSN}' | base64 -d)" \
  -- migrate -init
```

## Backups

Two things hold state, and a restore needs both from about the same time:

- **Postgres**: accounts, events, teams, scores, which site version is
  live. Your database's own backups cover it; by hand,
  `pg_dump -Fc "$DB_DSN" > hack.dump` and `pg_restore -d "$DB_DSN" hack.dump`.
- **The volume** (`/data`): every site's files and versions, and each
  site's SQLite, KV and uploaded files. Take a VolumeSnapshot of the
  claim `simple-hack-data` (your CSI driver's class), or copy it out:

  ```sh
  kubectl -n simple-hack exec deploy/simple-hack -- tar -C /data -cf - . > data.tar
  ```

  (`tar` is not in the image; use `kubectl cp` or a debug container with
  the volume mounted, or the snapshot.)

Take the two together while the pod is scaled to zero
(`kubectl -n simple-hack scale deploy/simple-hack --replicas=0`) for a
consistent pair; during an event, a live `pg_dump` plus a snapshot a minute
apart is fine (a site saved in between is missing from one and the server
tolerates it). To restore: scale to zero, `pg_restore`, restore the snapshot
into a new claim, set `persistence.existingClaim`, scale back up.

A copy of the Secret belongs with the backups: `PASSCODE_ENC_KEY` seals every
site passcode and notification secret, and the data is unreadable without it.

## What doesn't work without what

| Without | What happens |
|---|---|
| The wildcard DNS record | `https://hack.example.com/` works; every event page and team site is unreachable. The platform's home page still hands out those addresses. |
| A certificate that covers `*.hack.example.com` | Event pages warn or fail in the browser. |
| The event's `*.<event>.hack.example.com` SAN in that certificate | Team sites warn or fail in the browser; the rest of the event (sign-up, teams, judging) works. |
| The event's slug under `ingress.events` (or `ingress.catchAll`) | The ingress controller answers `https://<team>.<event>.hack.example.com/` with its default backend (404). Add the slug and `helm upgrade`. |
| Team sites at `<event>.hack.example.com/<team>/` | Not served, by design: each team is its own origin so no team's page can read another's visitor sessions or storage. |
| `X-Forwarded-Proto: https` from the proxy | Sign-in works; cookies are set without `Secure`. |
| A trustworthy `X-Forwarded-For` (or `trustedProxyCIDRs` covering the proxy) | Everyone shares one rate limit, and `SIGNUP_BLOCKED_COUNTRIES` sees the proxy's address. |
| The identity provider reachable from the pod | Company sign-in fails at the redirect; the pod's log says why. With `signIn.email` on, emailed codes still work. |
| Resend reachable (when `signIn.email` is on) | No sign-in codes and no notices. Company sign-in is unaffected. |
| A role that owns the database | The first start fails to build the schema (`permission denied`); the log says so. |
| An empty database owned by another application | The start refuses: the users table exists but is not ours. Give Simple Hack its own database. |
| The volume | Nothing starts (`persistence.enabled: false` is refused). |
| A second replica | Refused by design: one pod, `Recreate`. Uploads, imports and rate limits are in-process, and the volume is ReadWriteOnce. |
| Egress to the internet | Fine for an internal event with company sign-in, as long as the identity provider is reachable. Team sites that call outside services do that from the browser, not the pod. Site-storage webhooks (team notifications) need egress to wherever they point. |
| The connector screens (`MCP_APP_*`) | Off here; they are the hosted service's. Everything else over MCP works at `https://hack.example.com/mcp`. |

## Troubleshooting

- `kubectl -n simple-hack logs deploy/simple-hack` has every startup reason:
  a missing setting, a database it cannot reach (it waits two minutes, then
  exits), a schema it refuses.
- `kubectl -n simple-hack exec deploy/simple-hack -- /usr/local/bin/simple-host migrate -status`
  lists which migrations the database has.
- A pod that is Running but not Ready cannot ping Postgres: `/readyz`
  returns 503 with `{"status":"unready"}`.
- `kubectl -n simple-hack port-forward deploy/simple-hack 8090:8090` and
  `curl -H 'Host: hack.example.com' http://127.0.0.1:8090/` reaches the pod
  without the load balancer, to tell its problems from the proxy's.
- `deploy/kubernetes/tests/kind-check.sh` runs the whole thing against a
  throwaway kind cluster with its own Postgres, as a release check.
