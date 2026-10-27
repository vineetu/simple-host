# Simple Host small box on Railway

`docs/platforms/railway.md` walks through this end to end. This directory holds the
files Railway builds, not an importable template: Railway has no `render.yaml`- or
Coolify-`compose.yaml`-style file you commit and point a dashboard at. A Railway
template is a project you configure once (in the dashboard or with the CLI), then
publish; publishing is what produces the `railway.com/deploy/<slug>` link behind the
"Deploy on Railway" button. These three files are what that project's `app` service
builds from, and the settings below are the exact, reproducible list used when
building and publishing it.

Files: `Dockerfile` (copies the released server into the Caddy image, same shape as
`deploy/platforms/render`), `Caddyfile` (the small box's, without its TLS parts), and
`start.sh`. Nothing is compiled; no change to the server is needed.

## Services in the template

**Postgres** — Railway's own Postgres plugin (`+ New` → Database → PostgreSQL). No
settings to change.

**app** — source: your GitHub repo (a fork of `vineetu/simple-host`, or your own
repo with this directory copied in), Root Directory `deploy/platforms/railway`.
Railway detects the `Dockerfile` there on its own.

- Settings → Deploy → Healthcheck Path: `/healthz`
- Settings → Deploy → Pre-Deploy Command: `/usr/local/bin/start.sh release`
- Settings → Volumes → attach a volume, mount path `/data`
- Settings → Networking → Public Networking: generate a domain for a first look
  before DNS is ready, then add your two real custom domains once DNS is set
  (see `docs/platforms/railway.md`); Hobby allows 2 custom domains per service,
  which is exactly the two names a small box needs.
- Variables:

  | Variable | Value | Notes |
  |---|---|---|
  | `DB_DSN` | `${{Postgres.DATABASE_URL}}?sslmode=disable` | reference variable, not a secret; check the Variables tab's resolved preview — if `DATABASE_URL` already has a `?`, use `&sslmode=disable` instead |
  | `ADMIN_API_KEY` | `${{secret(64)}}` | generated once; this is the first admin's sign-in key |
  | `SITE_DOMAIN` | `hack.example.com` | the dashboard's own name; fill in after you own a domain |
  | `CONTENT_HOST` | *(leave unset)* | `start.sh` defaults it to `sites.$SITE_DOMAIN` |
  | `MAX_ARCHIVE_MB` | `100` | same default as the installer |
  | `KEEP_VERSIONS` | `3` | same deliberate deviation as the Render and Fly recipes; the installer itself defaults to 1 (no rollback) |

  Any other setting in [`docs/configuration.md`](../../../docs/configuration.md) goes
  in as one more Variable, exactly as named there. `PASSCODE_ENC_KEY` is deliberately
  left unset here: it must be 32 random bytes in standard base64
  (`openssl rand -base64 32`), and Railway's `secret()` function cannot be made to
  produce that format reliably, so this template does not auto-generate it. Site
  passcodes stay off until you set it by hand; that is a real limit, not an oversight
  (see the gotchas in `docs/platforms/railway.md`).

## Publishing the template

Once the project works end to end (sign-in, publish, saved data, restart, both
domains), use the dashboard's **Generate Template from Project** (project Settings)
or `railway templates create`, review the composer's captured Settings/Variables
tabs against the table above, then **Publish**. Railway assigns the slug that goes
in the button:

```markdown
[![Deploy on Railway](https://railway.com/button.svg)](https://railway.com/new/template/<SLUG>?utm_medium=integration&utm_source=button&utm_campaign=generic)
```

Fill in `<SLUG>` in `docs/platforms/railway.md`, the root `README.md`, and
`docs/platforms/README.md` once published. Until then, the button points at the
Railway dashboard's new-project flow instead of a one-click slug; see those files'
current state.
