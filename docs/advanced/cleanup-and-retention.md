# Cleanup and retention

**Recently deleted.** Deleting a site takes it offline and keeps it whole (files, saved data,
versions, its name) for `DELETED_RETENTION_DAYS`; the owner can restore it until then.

**Idle-site cleanup** is off by default. With `IDLE_CLEANUP=on`, a site with no visits, deploys
or saves for `IDLE_AFTER_DAYS` gets its owner a warning email with a Keep link; if nothing
happens for `IDLE_GRACE_DAYS` more, it moves to Recently deleted and the owner gets a Restore
email. Sites on their own domain or a claimed name, sites marked Keep, and the admin's are never
touched.

**Analytics and API metrics** are kept for their retention windows and then removed.

The date a person was promised is stored when it is promised: shortening a window never removes
anything earlier than its email said, and lengthening one does not extend what was already
promised.

<!-- settings:group=cleanup -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `DELETED_RETENTION_DAYS` | `7` | 1–365 days | How long a deleted site stays restorable in Recently deleted. |
| `IDLE_AFTER_DAYS` | `90` | 7–3650 days | Idle cleanup: a site unused this long gets its owner a warning email. |
| `IDLE_GRACE_DAYS` | `30` | 1–365 days | Idle cleanup: how long after the warning an unused site moves to Recently deleted. |
| `ANALYTICS_RETENTION_DAYS` | `400` | 1–3650 days | How long visit analytics are kept. |
| `API_METRICS_RETENTION_DAYS` | `30` | 1–3650 days | How long the admin page's API-call counts and shortened caller addresses are kept. |
| `IDLE_CLEANUP` | `off` | `on` / `off` | on warns owners of long-unused sites, then moves them to Recently deleted. |
| `IDLE_CLEANUP_MAX_EMAILS` | `50` | at least 1 emails | Idle-cleanup emails one run sends. |
| `IDLE_CLEANUP_EXEMPT_HANDLES` | none | text | Accounts (comma-separated) whose sites the idle cleanup never touches. |
<!-- /settings -->

## Recipes

**Shorter retention.**

```
DELETED_RETENTION_DAYS=3
ANALYTICS_RETENTION_DAYS=90
API_METRICS_RETENTION_DAYS=7
```

**Clean up abandoned sites on a long-running box.** (Needs [email](email.md).)

```
IDLE_CLEANUP=on
IDLE_AFTER_DAYS=60
IDLE_GRACE_DAYS=14
```
