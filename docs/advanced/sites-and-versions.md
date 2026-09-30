# Sites and versions

Every publish is a new version. The owner can publish without going live, open a preview link to
any kept version, make it live, roll back, rename a site (old links keep working), take it
offline, put a passcode on it, and download a copy with its saved data. A deleted site waits in
Recently deleted (see [Cleanup and retention](cleanup-and-retention.md)).

**Size.** `MAX_ARCHIVE_MB` bounds one upload and one site. It is a guard against one upload
filling a disk, not a budget: the median site on simple-host.app is about 25 KB. A proxy in front
needs its own body limit raised to match. `MAX_ARCHIVE_MB_OVERRIDES` gives named accounts their
own size; it holds new deploys only, so a larger site already live stays up. The admin page shows
each site's live copy (what the limit is about) and its total on disk (with kept versions).

**Versions kept.** Every version is a full copy of the site, so on a small disk the history,
not the sites, is what fills it. The installer keeps one version (no rollback); simple-host.app
keeps all.

**Site passcodes.** An owner may put one passcode on a whole site. Every address of the site
then shows a plain "This site is protected" page until the visitor enters it; a right passcode
lets that browser in on that one address until the passcode changes or the owner signs everyone
out. `SITE_PASSCODES` switches the feature; it also needs `PASSCODE_ENC_KEY` and a per-site
address (`SITE_HOSTS` and `PERSON_HOSTS` on). A server where every site shares one address (a
small box, an event box) refuses passcodes, because an unlock there would cover every site.
Generate the key once with `openssl rand -base64 32` and keep it secret like `ADMIN_API_KEY`: it
seals every stored passcode so owners can read theirs back. Changing or losing it makes every
stored passcode unreadable and signs every visitor out; owners then set new ones.
`PASSCODE_MIN_LENGTH` is the shortest code allowed. Wrong tries are limited per address on a site
(`RATE_LIMIT_PASSCODE_IP`, then `PASSCODE_LOCKOUT_MINUTES`) and per site from everyone together
(`RATE_LIMIT_PASSCODE_SITE`, then `PASSCODE_SITE_LOCKOUT_MINUTES`); visitors already let in are
not affected. A site with a passcode has a `passcode` file next to `current` in its folder, so
addresses nginx or Caddy serve straight from disk hand the request to the app instead. The
shipped vhosts and `deploy/prod/nginx-suspended-marker.sh` already do this; a hand-made nginx
vhost that serves a site folder needs this line after its `offline` check:

```
if (-f <site folder>/passcode) { rewrite ^ /internal/passcode$uri last; }
```

and, when the block has no `/internal/` proxy to the app, a `location ^~ /internal/passcode/`
one. `nginx-suspended-marker.sh` adds both to every block that serves a site folder.

<!-- settings:group=sites -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `MAX_SITES_PER_ACCOUNT` | `100` | 1–100000 sites | Sites one account may hold (sites in Recently deleted count). |
| `MAX_SITES_OVERRIDES` | none | handle:sites | Accounts that may hold a different number of sites than MAX_SITES_PER_ACCOUNT: comma-separated <handle>:<sites>, e.g. chhotabreak:2000 (1 to 100000 each). It follows the handle: an account that changes its handle keeps its override under the old one. |
| `MAX_ARCHIVE_MB_OVERRIDES` | none | 0–0 handle:mb | Accounts whose sites may be a different size than MAX_ARCHIVE_MB: comma-separated <handle>:<MB>, e.g. jot-transcribe:300 (1 to 500 each). It applies to new deploys only (a larger site already live stays up) and follows the handle like MAX_SITES_OVERRIDES. A proxy in front must accept bodies this large (4/3 of it for JSON and connector deploys), and the connector takes messages that large from every account, so keep values modest. |
| `MAX_FILES_PER_SITE` | `50000` | 100–50000 files | Files in one upload. It can only be lowered. |
| `PREVIEW_LINK_TTL_MINUTES` | `60` | 5–10080 minutes | How long a preview link to a stored version works. **Security-sensitive.** |
| `EXPORT_LINK_TTL_MINUTES` | `10` | 1–60 minutes | How long a site download link works (it holds private lists too). **Security-sensitive.** |
| `SITE_PASSCODES` | `on` | `on` / `off` | on lets owners put a passcode on a site (it needs PASSCODE_ENC_KEY and a per-site address). off refuses new ones; sites that already have one keep asking for it. |
| `PASSCODE_MIN_LENGTH` | `6` | 4–64 characters | Shortest passcode an owner may set, in characters. Any characters count; digits only is fine. |
| `PASSCODE_LOCKOUT_MINUTES` | `15` | 1–1440 minutes | How long one address is refused on a site once it has used up RATE_LIMIT_PASSCODE_IP. |
| `PASSCODE_SITE_LOCKOUT_MINUTES` | `15` | 1–1440 minutes | How long a site refuses every passcode try once RATE_LIMIT_PASSCODE_SITE is used up (visitors already let in are not affected). |
| `RATE_LIMIT_UPLOAD` | `30,10s` | any (warns past 10× looser) | Uploads and deploys per client. |
| `RATE_LIMIT_SITE_OPS` | `30,2s` | any (warns past 10× looser) | Deleting, changing and restoring sites, and admin sign-in tries, per address. |
| `RATE_LIMIT_EXPORT` | `10,10s` | any (warns past 10× looser) | Site and account downloads per address. |
| `RATE_LIMIT_ANALYTICS` | `30,2s` | any (warns past 10× looser) | Top pages and referring domains reads, per address. |
| `RATE_LIMIT_PASSCODE_IP` | `5,3m` | stricter freely; loosest `20,45s` | Wrong passcode tries on one site per address. **Security-sensitive.** |
| `RATE_LIMIT_PASSCODE_SITE` | `60,1m` | stricter freely; loosest `240,15s` | Wrong passcode tries on one site from everyone together. **Security-sensitive.** |
| `MAX_ARCHIVE_MB` | `100` | at least 1 MB | Largest upload, and the size one site may have. |
| `KEEP_VERSIONS` | `0` | at least 0 versions | Deploys kept per site; 0 keeps all. install.sh sets 1, which means no rollback. |
| `PASSCODE_ENC_KEY` | none | secret | Key that seals site passcodes (32 random bytes, base64: openssl rand -base64 32). Unset: no site can get a passcode. Changing it makes every stored passcode unreadable and signs every visitor out; owners then set new ones. **Security-sensitive.** |
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
