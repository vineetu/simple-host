# Simple Hack operations

Status: database restoration and current production file readback verified,
2026-10-01. Populated project-file recovery remains open. Recipient-free mail
send reporting is prepared locally but is not installed; provider bounce
reporting needs a separate Resend full-access monitoring key.

Simple Hack has its own `simplehack` database, `/srv/simple-hack` files and
`simple-hack.service`. The production backup mechanism is the existing
`agentbox-backup.timer`, not a second Simple Hack scheduler. These are operations
for this hosted box; an independent installation needs its own backup destination
and mail-provider setup.

## Existing nightly backup

The timer runs at 03:20 UTC with up to 20 minutes of random delay and catches up
after downtime. `/usr/local/bin/agentbox-backup.sh` dumps each database separately
in PostgreSQL custom format, compresses it with zstd and uploads it in the dated
Tier A snapshot. It also captures the box configuration in Tier A. Tier B mirrors
bulk files, preserves symlinks with rclone's `--links`, and parks overwritten or
deleted files. The script's default retention is 14 days; configuration can
override it. The remote is configured for encryption.

The October 1 run started at 03:29 UTC and finished at 05:14 UTC with exit status
zero. A Host file changed during its first transfer attempt; rclone's next
attempt succeeded. The local Hack dump is
`/var/backups/agentbox/postgres/simplehack.dump.zst`.

`/srv/simple-hack` is listed in `/etc/agentbox-backup/tier-b.list`, but that list
changed at 04:42 UTC and this run's journal has no Hack sync entry. Being on the
list is not proof that the files reached the remote. Do not mark file recovery
complete until a transfer and readback have been checked.

Inspect timer state without opening configuration secrets:

```sh
systemctl show agentbox-backup.service --property=ExecMainStatus,ExecMainStartTimestamp,ExecMainExitTimestamp
```

### Remaining file-backup verification

The backup script accepts `--tier b` and an alternative `LIST_B`, but still
regenerates its manifest, dumps every database and prunes the remote deleted-file
parking area. Even its `--dry-run` does the local dumps. Neither is a narrowly
scoped, read-only Hack check.

Coordinate a direct rclone transfer of `/srv/simple-hack` to its existing
`tierB/srv_simple-hack` destination, using the existing encrypted configuration,
`--links` and the existing overwritten-file parking convention. Use `copy` for
this verification so it does not delete remote-only files. Check the transfer
with `rclone check --download --one-way --links`, then copy a representative
project/version back to a private temporary directory. Verify file hashes and
the restored relative `current` and handle symlinks. Do not print file contents,
credentials or private paths from application rows. Do not run this concurrently
with the scheduled backup.

The shared Tier B exclusion list skips names including `node_modules` and
`*.tmp`. Those names can be valid uploaded project files. The minimal correction
is to omit that generic exclusion argument for the `/srv/simple-hack` source in
the existing loop, retaining the other sources' behavior. A broad change to the
shared list is unnecessary. This correction was applied on October 1 after a dated script backup; Bash
syntax validation passed. Other sources retain their existing exclusions.

On October 1, outside the scheduled backup, a scoped encrypted remote copy,
byte comparison and private readback passed. The two current files totalled
136,850,953 bytes; there were zero project-version directories and zero symlinks.
No remote deletion or whole-box backup ran. This proves the current empty project
storage reached the remote, not populated project-file recovery. Private recovered
files were removed afterward.

The database dump and file mirror occur at different times. A successful copy
does not prove an atomic database/files snapshot. Verify that any representative
version referenced by the restored database is present in the recovered files;
retain the existing deleted-file parking when checking an older dump.

## Repeatable database restore check

From the repository root, with the PostgreSQL 16 image already available locally:

```sh
sudo bash scripts/check-hack-restore.sh /var/backups/agentbox/postgres/simplehack.dump.zst
```

The script accepts a trusted custom-format `.dump` or `.dump.zst`. It creates a
PostgreSQL 16 container with no network, no published port and no host mount.
Database files live in a bounded tmpfs. It streams the dump through stdin and
restores with `--no-owner --no-acl --exit-on-error`; it never alters production
roles or the production database. No application or mail sender starts.

Output contains only pass/fail, schema counts and table-row counts. PostgreSQL
errors may contain row data, so raw diagnostics stay in a private temporary
directory and are deleted on exit. The container and database are removed on
exit, including failed checks. A cleanup failure is reported as failure and names
only the disposable container to remove. The database tmpfs is 512 MiB; increase
that explicit script limit when the backup grows beyond it.

The check verifies that the dump restores, that its constraints are validated,
that core/judging/archive schema exists, and that member, entry, score and rubric
references belong to the same event. It reports score and published-snapshot
counts without exposing their contents. It does not apply newer migrations to conceal an old
snapshot. It does not prove recovery of project files, old role grants, TLS
certificates or a working public deployment.

### Evidence from October 1

| Source | Restore | Current schema | Data consistency |
|---|---|---|---|
| Nightly dump from 03:29 UTC | Passed | Failed: all five judging tables absent | Restored constraints validated; no cross-event member/team or entry/team mismatches |
| Fresh read-only `pg_dump` of `simplehack` during this audit | Passed | Passed: 8 core tables, 5 judging tables, 4 archive columns | Restored constraints validated; no cross-event member/team or entry/team mismatches |
| Populated local `sh_hack_rehearsal` database | Passed | Passed | No member/team, entry/team, score/team or score/criterion event mismatches; all constraints validated |

Both production snapshots had zero events, teams, members, entries, sites and versions.
The populated rehearsal restored 2 events, 7 teams, 25 members, 6 entries,
69 criterion scores, 1 nonempty published result snapshot, 7 sites and 12 version
records. The score count and existence of the published snapshot were asserted
after restoration. This proves populated database recovery; project-file recovery
remains separate. The rehearsal source database was not modified.

The temporary fresh and rehearsal dumps were
deleted after testing and did not replace the canonical nightly backup. The
older nightly dump cannot recover judging data created after it was taken.

## Email volume and bounces

After this binary is deployed, `internal/email/resend.go` writes one structured
`hack_mail_send` journal record per Simple Hack send request: `outcome=accepted`
with the Resend ID, or `outcome=failed` with a fixed reason. Neither recipient,
subject, message, sign-in code nor API key is logged. Simple Host sends do not
produce this record. Accepted means Resend accepted the HTTP request, not that
the recipient received the message. The report starts counting only after the
new binary is deployed; it cannot reconstruct older sends.

`scripts/hack-mail-report.py --hours 24` reads only the `simple-hack.service`
journal and prints counts as JSON. The prepared `sh-hack-mail-watch.timer` runs
it daily at 08:00 UTC and writes the JSON to its own journal. It also checks
Resend's read-only sent-email list when the private monitoring key exists;
without that key the local counts continue and bounce status is unavailable.
The timer sends no alert. To install after release review, from this repository's
root:

```sh
sudo install -m 755 scripts/hack-mail-report.py /usr/local/bin/sh-hack-mail-report && sudo install -m 644 deploy/hack/sh-hack-mail-watch.service deploy/hack/sh-hack-mail-watch.timer /etc/systemd/system/ && sudo systemctl daemon-reload && sudo systemctl enable --now sh-hack-mail-watch.timer
```

Read its latest local report with `sudo journalctl -u sh-hack-mail-watch.service -g '"product": "simple_hack"' -n 1 -o cat --no-pager`.
The `provider` object says `unavailable` and its `bounced` value is `null`
until a monitoring key is configured. This is not a zero-bounce result.

To enable the daily provider check, create a **separate Full access** Resend
key in the Resend dashboard and store it privately at
`/etc/simple-hack-resend-monitor.key` (root-owned, mode 0600). The next timer
run checks provider status automatically. To check immediately, run:

```sh
sudo /usr/local/bin/sh-hack-mail-report --hours 24 --monitor-key-file /etc/simple-hack-resend-monitor.key --from-env-file /etc/simple-hack.env
```

The report reads only `MAIL_FROM` from the Hack environment file, then scans
the read-only [sent-email list](https://resend.com/docs/api-reference/emails/list-emails)
in pages of 100, up to 50 pages. It matches **both** exact IDs from Hack's local
accepted-send records and the configured sender, so Host sends on the same
Resend account cannot enter Hack totals. `last_event` is a provider snapshot;
`bounced` counts messages whose *current last event* is bounced, not every
historical bounce event. If a page limit or missing ID leaves incomplete
coverage, status is `partial` and the number of unmatched sends is shown.
A 403 is reported as `permission_denied`, with bounce count `null`; the existing
send key returned 403 on this read path and cannot establish bounce visibility.
Resend distinguishes [Sending access from Full access](https://resend.com/changelog/new-api-key-permissions).
No provider key, message, recipient or code is printed. No webhook, provider
write, email or Signal message is created by this report.

The existing `sh-support-mail-watch.timer` checks the Host support inbox;
it remains separate from this Hack-only report.
