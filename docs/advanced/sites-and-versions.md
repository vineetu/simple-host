# Sites and versions

Every publish is a new version. The owner can publish without going live, open a preview link to
any kept version, make it live, roll back, rename a site (old links keep working), take it
offline, and download a copy with its saved data. A deleted site waits in Recently deleted (see
[Cleanup and retention](cleanup-and-retention.md)).

**Size.** `MAX_ARCHIVE_MB` bounds one upload and one site. It is a guard against one upload
filling a disk, not a budget: the median site on simple-host.app is about 25 KB. A proxy in front
needs its own body limit raised to match.

**Versions kept.** Every version is a full copy of the site, so on a small disk the history,
not the sites, is what fills it. The installer keeps one version (no rollback); simple-host.app
keeps all.

<!-- settings:group=sites -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `MAX_SITES_PER_ACCOUNT` | `100` | 1–100000 sites | Sites one account may hold (sites in Recently deleted count). |
| `MAX_FILES_PER_SITE` | `50000` | 100–50000 files | Files in one upload. It can only be lowered. |
| `PREVIEW_LINK_TTL_MINUTES` | `60` | 5–10080 minutes | How long a preview link to a stored version works. **Security-sensitive.** |
| `EXPORT_LINK_TTL_MINUTES` | `10` | 1–60 minutes | How long a site download link works (it holds private lists too). **Security-sensitive.** |
| `RATE_LIMIT_UPLOAD` | `30,10s` | any (warns past 10× looser) | Uploads and deploys per client. |
| `RATE_LIMIT_SITE_OPS` | `30,2s` | any (warns past 10× looser) | Deleting, changing and restoring sites, per address. |
| `RATE_LIMIT_EXPORT` | `10,10s` | any (warns past 10× looser) | Site and account downloads per address. |
| `RATE_LIMIT_ANALYTICS` | `30,2s` | any (warns past 10× looser) | Top pages and referring domains reads, per address. |
| `MAX_ARCHIVE_MB` | `100` | at least 1 MB | Largest upload, and the size one site may have. |
| `KEEP_VERSIONS` | `0` | at least 0 versions | Deploys kept per site; 0 keeps all. install.sh sets 1, which means no rollback. |
| `PREVIEW_ACCOUNTS` | none | text | Accounts (comma-separated) whose sites expire on their own. |
| `PREVIEW_TTL_HOURS` | `48` | at least 1 hours | How long those accounts' sites last. |
<!-- /settings -->

## Recipes

**A small hackathon box** (1 CPU, 1 GB, 25 GB disk). The installer's defaults already fit:
`--max-site-mb 100 --keep-versions 1`. To cap each participant too:

```
MAX_SITES_PER_ACCOUNT=20
```

**Keep rollback on a bigger disk.** Install with `--keep-versions 5`.
