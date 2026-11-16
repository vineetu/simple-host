# D3-S5b: selected-team write retirement

`hack_grant_teams` is authoritative. Selection checks the grant owner, scope,
participant role and approved membership in one `INSERT … SELECT … ON CONFLICT`
statement. The legacy `oauth_grants.selected_team_id` column remains in the
schema but is neither read nor written by the running program and is no longer
required by the platform schema check. There is no migration in this slice.

## Deploy handoff (orchestrator only)

D2-S6 is deployed as `e4cea061ebb30de0a11ad61b9f688203cc39bed6`.
Immediately before deploying D3-S5b, run this read-only comparison on **both**
production databases and require zero:

```sql
SELECT count(*)
FROM oauth_grants g FULL JOIN hack_grant_teams h ON h.grant_id = g.id
WHERE g.selected_team_id IS DISTINCT FROM h.team_id;
```

Raise the coordination file's `oldest safe commit:` to the later, in main's
history, of its current value and `e4cea061ebb30de0a11ad61b9f688203cc39bed6`.
Retain the matching backup program names. Never lower another slice's floor.
The builder does not perform these production checks, change that record,
or deploy. Security review and independent approval remain required.

The D2-S6 follow-up's startup difference check applies **while both stores are
written**. This candidate retires that write, so differences after selection
are expected: it must not refuse startup or report them as corruption. The
zero-difference check above is a pre-deploy check of the double-writing program.

After a rollback to a double-writing program and redeploy, this count may
legitimately be non-zero: untouched legacy column values can be stale. Do not
"fix" that difference with the reconcile script; the table remains authoritative.

**Never re-apply `0020-hack-grant-teams.sql` by hand after D3-S5b.** Its historical
copy makes the old column win and would overwrite newer selections. The applied
migration and its ledger entry stay unchanged; the normal runner skips it.

## Way back

All repair scripts require both services stopped, with no remaining writers.
The shared rollback floor must permit the intended program independently.

| Situation | Action |
|---|---|
| Return to a D2-S6-or-newer, double-writing program | No repair: it reads the table and resumes writing both stores for new selections. Untouched legacy column values can still be stale. |
| Return below D2-S6 | First run `selected-team-restore.sql` with `-v table_is_newer=yes`. It copies table choices and clears columns with no table choice. This removes only this slice's reader requirement; other active requirements may still forbid the older program. |
| Move forward after a column-only program ran | Run `selected-team-reconcile.sql` with `-v column_is_newer=yes`. It copies the authoritative column, including absent and deleted-team choices. |
| No allowed program below the shared floor | Restore a complete backup predating every active requirement: database, files, configuration and program together. |

Both scripts are idempotent and lock both tables inside their transaction.
Never reconcile from a stale column after retirement. The seeded rehearsals in
`products/hack/repository/hackteams/connector_test.go` cover both recorded
production schemas, missing selections, deleted teams/grants, old-writer changes,
script guards and repeated repairs. These fixtures contain only invented data.
