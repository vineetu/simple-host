# v3 Path-Model — Production Cutover Runbook (simple-host.app)

**Status:** ready, NOT executed. Requires explicit go-ahead. The whole v3 build is done and
verified on agent-deploy.dev (branch `v3-path-model`, 12 commits, DB never wiped). This runbook
cuts it over to the real `simple-host.app` box, which hosts **~30 live sites + jot donations** —
so every step is reversible and checked against a baseline.

## Preconditions
- Merge/push `v3-path-model` (review first).
- On the prod box, confirm the env the runports process holds: `SITE_DOMAIN=ideaflow.page`?
  **No — prod is `simple-host.app`.** Verify `SITE_DOMAIN`, `PUBLIC_BASE_URL`, `DATA_DIR`,
  `ADMIN_API_KEY`, `DB_DSN`, `RESEND_API_KEY`. Add for v3: `CONTENT_HOST=sites.simple-host.app`
  (else it derives to that anyway), `CNAME_TARGET=cname.simple-host.app`.
- ⚠️ Per the workspace CLAUDE.md, before ANY prod rebuild also set `SITE_DOMAIN` and
  `DEPLOY_SCRIPT` explicitly if this box uses cortex-share (the public source dropped the
  compiled ideaflow defaults). simple-host.app (UpCloud) does NOT use cortex-share, so
  `DEPLOY_SCRIPT` should stay empty — confirm.
- DNS (Vercel, simple-host.app): ensure `sites.simple-host.app` A/ALIAS → box (or covered by the
  existing `*.simple-host.app` wildcard — it is), and `cname.simple-host.app` → box.
- Stage rollback: keep the current prod binary as `simple-host.bak-prev` and a `pg_dump` before
  migrations.

## Baseline (capture BEFORE touching anything)
```
pg_dump ... > /root/v3-cutover-prebaseline.sql
psql -tAc "SELECT count(*) FROM users; SELECT count(*) FROM sites;"
ls /root/workspace/general/sites            # or DATA_DIR — record the site dirs
```

## Order (re-key first, drop-unique last, disk before drop — same as the tested sequence)
1. **Binary (behaviour-identical first).** Build with go124, `pg_dump`, deploy the v3 binary with
   the CURRENT schema still in place (0a/0b/0c/0d/0e code tolerates the old schema until each
   migration runs, EXCEPT the drop-unique — deploy binary first, it still works name-globally).
   Verify `/readyz`, load 3 real sites.
2. **0b migration** — `legacy_hostnames` table + backfill (`db/migrations/0b-legacy-hostnames.sql`,
   swap the domain to simple-host.app). Additive; verify a state call still 200s.
3. **0c-1** — apply `db/migrations/0c1-handles.sql` (handles), run
   `scripts/build-symlink-farm.sh` (handles/<handle> → by-id/<uid> → flat dirs).
4. **0c-2 disk move** — `db/migrations/0c2-diskmove.sh` (flat → by-id, back-compat symlinks,
   disk_path). **CHECKSUMMED** — it aborts on any mismatch. Verify every site still serves.
5. **1a migration** — custom-domain columns (`db/migrations/... 1a`).
6. **0e migration** — `db/migrations/0e-per-user-names.sql`: DROP `sites_name_key`, ADD
   `sites_user_name`, backfill `site_url` to path URLs (swap domain to simple-host.app). This is
   the flip — do it LAST, after disk is by-id-keyed.
7. **Edge (Caddy)** — this box currently fronts via the ideaflow/nginx proxy + simple-host on 8090
   (cortex-runports). Decide the :443 topology (design §8: Caddy sole edge). Add:
   - content host `sites.simple-host.app` block (path → handles farm; /v1,/internal → app),
   - `on_demand_tls { ask http://127.0.0.1:8090/internal/tls-ask }`,
   - custom-domain serving from `domains/{host}`.
   Mirror `deploy/Caddyfile.v3-content-host.example` (swap domain). Validate + reload.
   NOTE: simple-host.app's real edge differs from the test box (ideaflow proxy). Reconcile the
   proxy-vs-Caddy :443 ownership here — this is the single riskiest prod-specific step; spike it
   on the box first if unsure.
8. **Verify** the full regression (both origins, path serving, user-scoped API + namespacing,
   legacy subdomain 301/serve, a bound custom domain, donations page + jot-webhook unaffected).

## Rollback
- Binary: copy `simple-host.bak-prev` back, `cortex-runports repair --port 8090`.
- Schema: the migrations are additive except 0e's constraint swap — to roll back 0e:
  `ALTER TABLE sites DROP CONSTRAINT sites_user_name; ALTER TABLE sites ADD CONSTRAINT
  sites_name_key UNIQUE(name);` (only safe while no two sites share a name — true right after
  cutover). Disk: the flat `<name>` symlinks still point at by-id dirs; reverting the binary keeps
  serving. Full restore: `psql < /root/v3-cutover-prebaseline.sql` (last resort).

## Critical prod-specific risks (not present on the test box)
- **jot donations (jot-webhook :8081, poller, page) and simple-host :8090 are bread-and-butter** —
  do the cutover in a low-traffic window; keep an eye on `/root/data/logs/`.
- The prod edge is the ideaflow/nginx proxy, not a clean Caddy box. Step 7 must reconcile that.
- `bass.ideaflow.page` and other runports web SERVICES are unaffected (separate ports) — confirm.
```
