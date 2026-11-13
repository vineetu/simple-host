# Split rehearsal seed

This invented fixture is for rehearsing data moves, permission changes and their
rollback on a fresh schema from either checked-in dump. Later slices D2-S5b,
D2-S6, D2-S8, D3-S5c, D3-S5b and D4-S10b rehearse on it. It contains no production
data. Changes to this fixture are reviewed like code.

From the repo root, with `TEST_PG_URL` set to a disposable server's bare URL
(for example `postgres://postgres:pw@127.0.0.1:5432`, no path or query), build
one rehearsal database and load it:

```bash
rehearsal_db="sh_rehearsal_$(openssl rand -hex 8)"
psql "${TEST_PG_URL}/postgres?sslmode=disable" -X -v ON_ERROR_STOP=1 -c "CREATE DATABASE \"$rehearsal_db\";"
sed '/^\\restrict/d; /^\\unrestrict/d' scripts/split/evidence/simple-host.schema.sql | psql "${TEST_PG_URL}/${rehearsal_db}?sslmode=disable" -X -v ON_ERROR_STOP=1
psql "${TEST_PG_URL}/${rehearsal_db}?sslmode=disable" -X -v ON_ERROR_STOP=1 -f scripts/split/seed/seed.sql
```

Use `simple-hack.schema.sql` in the third command to rehearse against that dump.
The `sed` removes only the dump's restrict/unrestrict commands for older psql
clients; psql 16.10+ can instead load the dump directly with `-f`. The seed uses
one transaction and explicitly named columns. Load it once into an empty schema;
a second load fails on a duplicate user and rolls back.

After the rehearsal, remove only the database created above:

```bash
psql "${TEST_PG_URL}/postgres?sslmode=disable" -X -v ON_ERROR_STOP=1 -c "DROP DATABASE \"$rehearsal_db\";"
```

To check both dumps, run `bash scripts/split/seed/load-check.sh`. It uses
`TEST_PG_URL` if set; otherwise it starts an isolated `postgres:16-alpine`
container on a random localhost port. It checks the first load and refused
second load, prints counts, and removes only databases/container/volume it
created. It requires psql and openssl, plus Docker if no server URL is supplied.
Expected counts per dump: 4 users, 5 sites, 1 event, 2 teams, 2 grants.

All UUIDs below have prefix `00000000-0000-4000-8000-`; the final column gives
the full twelve-digit suffix. There are two ordinary participant/site owners,
a holding account and a separate organiser identity. As in `CreateEvent`, the
holding handle equals the event slug, its username is
`event+00000000-0000-4000-8000-000000000010@events.invalid`, its display name
is NULL, and claiming the handle sets `handle_changed_at`. The
`event_used_names` reservations already use that same event slug.

| Case | Handle, slug or other query key | UUID suffix |
|---|---|---|
| Ordinary owner / approved Live participant | `seed-one` | `000000000001` |
| Ordinary owner / approved Trash participant (approved application) | `seed-two` | `000000000002` |
| Holding account (`event_account=true`) | `seed-rehearsal` | `000000000003` |
| Organiser | `seed-organiser` | `000000000004` |
| Event (`stage=building`, held by account 3) | `seed-rehearsal` | `000000000010` |
| Live team | `seed-live` | `000000000011` |
| Team with Recently deleted site | `seed-trash` | `000000000012` |
| Deleted former team (no team row) | `seed-former` | `000000000013` |
| Live holding-account site | `seed-live` | `000000000021` |
| Recently deleted holding-account site | `seed-trash` | `000000000022` |
| Owner 1 passcode site | `seed-passcode` | `000000000023` |
| Owner 2 named-viewer site | `seed-viewers` | `000000000024` |
| Owner 1 active custom domain | `seed-domain`, `seed.example.org` | `000000000025` |
| Grant selecting Live team | client `seed-connected-app` | `000000000031` |
| Grant selecting deleted former team | client `seed-connected-app` | `000000000032` |
| Announcement | `Rehearsal announcement` | `000000000040` |
| Unseen team-admin notice | user 1, team 11 | `000000000041` |
| Seen team-removal notice | user 2, deleted team 13 | `000000000042` |
| Unseen team-admin notice | user 2, team 12 | `000000000043` |

Team sites are linked by `sites.user_id = events.account_id` and
`sites.name = event_teams.slug`, not a `sites.team_id`. Site 22 has `deleted_at`
one day ago, `purge_at` in 29 days, no idle token, no home-site reference and no
web-push subscription, matching `repository/sites.MarkSiteDeleted`.

Site 24 has `access='specific'` and viewers `viewer-one@example.com` and
`viewer-two@example.org`. Site 23 has nonempty, deliberately fake
`passcode_enc` bytes, a set timestamp and generation 1; it cannot be decrypted
or unlocked as a working passcode. The old `view_password_hash` is unused.
Site 25 has `domain_status='active'` and `domain_cert_status='live'`; no DNS or
certificate is provisioned.

## Deleted team selection

Grant 32 retains `selected_team_id=00000000-0000-4000-8000-000000000013`.
Neither dump nor `db/schema.sql` defines a foreign key on this column, so it
is **not nulled** on team deletion. `repository/accounts.SelectOAuthTeam`
stores selection only on the grant; `app/hack/teams.DeleteTeam` deletes the
team and its API keys without deleting or clearing OAuth grants/tokens.
The fake refresh-token row for grant 32 survives. Notice 42 and the
`event_used_names` reservation for `seed-former` survive too: neither has a
team FK. The seed represents user 2 having subsequently joined team 12;
the removed team itself and its entry/API keys are absent. A membership's
`team_id` does have `ON DELETE SET NULL`, with a trigger clearing admin/join
metadata and another revoking Hack subscriptions when membership changes.
The seed does not retain a subscription for the removed team.

## Saved data and notifications

KV is in PostgreSQL at this commit: `site_storage_kv` stores JSONB values and
writer IDs (`repository/storage/kv.go`). Site 21 has resource `settings` with
keys `theme`, `counter`, `draft`; site 25 has `settings/enabled`.
`site_storage_resources` describes those KV resources plus site 21's SQLite
resource `records` and files resource `uploads`. `site_storage_tables` has
policy overrides for `records.notes` and `records.tasks`; these are metadata,
not SQLite rows. Legacy data also lives in PostgreSQL: all five `sites.state`
values, `collection_settings` for `21/notes` (board) and `24/responses`
(private entries), and collection item IDs 1–3. Dump triggers maintain state
and collection byte counters. Integer item/event sequences are advanced past
the explicit seed IDs.

Site subscriptions are `seed-webhook` (site 25) and `seed-mcp` (site 21,
grant 31), with two `site_push_rules`, three `record.changed` events (sequence
1–3), and three delivery rows (two pending, one delivered). Hack has three
notices, two unseen, with pending email work. Team 11 has a submitted `event_entries` row; announcement
40 has no mail-delivery rows. Applications use `event_members.joined_via` and
`approval_status`, not a separate applications table.

There are no production records, real people, real credentials, deployed site
content, version trees, disk files, SQLite database bytes, file objects,
usable passcodes or working notification endpoints. Fake tokens, secrets and
domain proofs are visibly labelled constants. This is a database rehearsal
fixture, not a runnable copy of a hosted site; do not run delivery workers
against it. No `/etc` environment files or production databases are needed.
