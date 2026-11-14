# R2-S5b: key-value move rehearsal on copies of production

Run 2026-11-14 (UTC) with `scripts/kv-move-rehearsal.sh` at commit `873d38a8`
(`hack-v0.8.20-59-g873d38a8`, 99 migrations), once per install, on copies only.
Production was read, never written: no service was stopped. Counts, sizes, timings
and hashes only; no site, account, namespace or key names are recorded here.
The script's own step records are beside this file (`simple-hack.steps.jsonl`,
`simple-host.steps.jsonl`).

**Result: zero differences on both installs**, at every comparison: dry run, apply
copy proof, after cutover, after rollback with later writes, after the last-resort
paired restore.

## Inputs

| | Simple Hack | Simple Host |
|---|---|---|
| Database dump (`pg_dump -Fc`, one snapshot) | 17:48:18 to 17:48:19 | 17:49:42 to 17:49:44 |
| Dump size | 1,326,808 bytes | 9,232,761 bytes |
| Dump SHA-256 | `e5b52ce5…58678c7` | `3fc09092…f1871b5f` |
| Data-directory copy (whole directory, nothing skipped) | 17:48:19, under 1 s | 17:49:44 to 17:52:46 (182 s) |
| Data-directory size | 213,755 bytes, 181 files | 6,991,935,291 bytes, 35,969 files |

Difference from the documented input: the dump is "from the stopped install" in the
docs; here the installs kept serving, so the dump is one consistent snapshot and the
directory copy was taken live straight after it. Every key-value entry is in Postgres
today, so the dump alone fixes the key-value state; the directory only has to hold
each site's folder, and all of them resolved (no namespace was skipped).

## What was moved

| | Simple Hack | Simple Host |
|---|---|---|
| Production namespaces | 0 | 33 (all on Postgres) |
| Production entries | 0 | 123 |
| Production bytes (key + value) | 0 | 42,687 (keys 1,534; values 41,153) |
| Sites holding them | 0 | 30, of which 8 in Recently deleted |
| Script's invented site (added on the copy) | 2 namespaces, 6 entries, 186 bytes | 2 namespaces, 6 entries, 186 bytes |
| Moved in the rehearsal | 2 namespaces, 6 entries, 186 bytes | 35 namespaces, 129 entries, 42,873 bytes |

## Comparisons (all zero differences)

| Comparison | Simple Hack | Simple Host |
|---|---|---|
| Dry run: namespaces compared, SHA-256 pairs equal | 2 of 2 | 35 of 35 |
| Dry run: Postgres order equals byte order | 2 of 2 | 35 of 35 |
| Apply: copy proofs, source hash equals file hash | 2 of 2 | 35 of 35 |
| Apply: logical bytes, source and file | 186 and 186 | 42,873 and 42,873 |
| After apply: namespaces on SQLite, rows left in Postgres | 2, 0 | 35, 0 |
| Verify after apply | 2 ok | 35 ok |
| Cutover: every record plus owner reads through HTTP | 2 namespaces, 6 reads, 0 differences | 35 namespaces, 121 reads, 0 differences |
| Usage figure, usage tool, exported `kv.json` | equal | equal |
| Verify after later writes | 3 ok | 36 ok |
| Rollback: proofs, hash pairs equal | 3 of 3 | 36 of 36 |
| After rollback: namespaces on Postgres, rows | 3, 7 | 36, 130 |
| Rollback keeps later writes | 3 namespaces, 7 reads, 0 differences | 36 namespaces, 122 reads, 0 differences |
| Last-resort paired restore equals backup time | 2 namespaces, 6 reads, 0 differences | 35 namespaces, 121 reads, 0 differences |

Owner reads through HTTP cover live sites only (121 on Host); the 8 entries in
Recently deleted sites are compared record by record from the stores.

## Wall time of each step (seconds)

| Step | Simple Hack | Simple Host |
|---|---|---|
| Restore the input copies (dump restore, directory copy, local credentials) | 53.9 | 215.4 |
| Start before cutover, invented writes, first capture | 3.0 | 8.9 |
| 2. Dry run while serving | 0.06 | 0.73 |
| 3. Free-disk check | 0.55 | 4.68 |
| 4. Stop | 0.03 | 0.03 |
| 5. Paired backup (dump + tar.gz of the whole directory, hashed) | 0.51 | 865.6 |
| 6. Apply | 0.20 | 1.46 |
| 7. Verify | 0.04 | 0.16 |
| 8. Start on SQLite, smoke, compare | 3.7 | 8.0 |
| Later writes, verify | 0.31 | 4.0 |
| Stop, rollback, start, compare | 0.73 | 7.4 |
| Last-resort paired restore, start, compare | 3.5 | 142.9 |
| Whole rehearsal | 67 | 1,261.5 |

Stopped time a real cutover should expect (steps 4 to 8): **Simple Hack under 5 s;
Simple Host about 14.6 minutes, of which 14.4 minutes is the paired backup of the
6.99 GB data directory** and the move with its verification takes under 2 s. The backup
ran in Python's single-threaded gzip while the box's load average was 14 to 24 from
other test runs. It is the only step that scales with the install's size. Rollback
keeps the install stopped for under 8 s. The last-resort restore takes about 2.4
minutes on Host.

## Paired backups taken in the rehearsal

| | Simple Hack | Simple Host |
|---|---|---|
| `paired.dump` size | 1,120,398 bytes | 8,978,790 bytes |
| `paired.dump` SHA-256 | `b69e295c92d4204ac267879d1635911db5754f5e37c74750d9a56d97b88de764` | `31bb70efcdacaee8219bfb53c031e9cbac30d12ff305737d8485285ea5e4e9dc` |
| `paired.tar.gz` size | 47,142 bytes | 5,862,335,077 bytes |
| `paired.tar.gz` SHA-256 | `c694604d73f2c7250c9c3b3ff95bdd13353c6bc7c81fc7c3a3208f9f83372ef0` | `563eafa0356b47cea5fe6e573a1538c9134d02c5e477848a6dddd44a50f46e24` |
| Free-disk check: needed, free | 1.6 MB, 45.7 GB | 8.40 GB, 29.8 GB |

Full input-dump hashes: Hack
`e5b52ce5a2ca1c95601dce1dfafd9e85d0469f3bdad9cc33424116b9658678c7`, Host
`3fc09092293edd178c26a42a9a4bcebf5318c6b96bca3217426c110bf1871b5f`.

## Throughput

Move (Host, 35 namespaces, 129 entries, 42,873 bytes): dry run 0.73 s, about 175
entries/s. Apply took 1.46 s including its postcondition queries, about 88 entries/s and
0.03 MB/s, that is about 42 ms per namespace. The cost is per namespace (file
create, sync, rename, one transaction), not per byte. Paired backup: 6.99 GB in
865.6 s, 8.1 MB/s. Paired restore: 142.9 s including start and comparison.

Key-value reads on one site, 100 reads, `SITE_STORAGE_SITE_INFLIGHT=1` (NOTES item
(f): reads on SQLite run one at a time per site):

| Store, clients | Simple Hack reads/s | Simple Host reads/s | Load average (Hack, Host) |
|---|---|---|---|
| Postgres, 1 | 81.3 | 42.1 | 13.3, 14.1 |
| Postgres, 4 | 181.6 | 80.1 | 13.3, 13.8 |
| SQLite, 1 | 77.1 | 50.4 | 14.4, 7.9 |
| SQLite, 4 | 108.7 | 49.6 | 14.4, 8.4 |

With one client the two stores are level. With four clients Postgres scales (about
2x) and SQLite on Host does not (49.6 against 80.1 reads/s), as expected with one
slot per site. The box was heavily loaded, so treat the absolute figures as a floor.

## Peak disk use

Simple Host: 14.6 GB above the input copy while the paired archive existed beside
the scratch directory (scratch copy 7.0 GB, archive 5.9 GB, databases, plus other
jobs on the box, since it was measured across the filesystem). It was 21.6 GB including
the 7.0 GB input copy. Simple Hack: under 0.1 GB. A real cutover needs only the
paired backup: about 5.9 GB for Host.

## Observations on the data (Simple Host)

- No orphans: no key-value row without its namespace record, none without its site.
- 8 namespaces (8 entries) belong to sites in Recently deleted; all were moved
  inside the trashed directories, came back on rollback and compared equal.
- 5 of 33 namespaces are empty and moved as empty files.
- Entries per namespace: median 1, largest 40. Largest namespace 12,286 bytes.
- Largest value 12,281 bytes; 99th percentile 4,127 bytes, none above 64 KB.
- Longest key 71 bytes. No key has non-ASCII bytes, a slash, a backslash, white
  space or a control character; 3 contain `%` or `_`. No two production keys differ
  only by case (the 2 case-variant keys in the logs are the script's own).
- Values: 116 objects, 4 strings, 3 arrays.
- 31 entries carry an author that is not an account id (empty or a visitor id);
  authors are copied and compared as they are.
- Simple Hack has no key-value namespace at all today, so its rehearsal exercised
  the procedure on the script's invented site only.

## Clean-up

The dumps, the directory copies, the scratch databases and the throwaway Postgres
container were deleted after the runs. Only this folder was kept.
