# Limits and keeping files small

## Numbers at a glance

- A site may be up to 300 MB on simple-host.app. **Archive limit** is 100 MB.
- Simple Host keeps 4 versions of a site; new websites (from the 2026-10-05 deployment) may total 200 MB including retained versions; 1 GB per account.
- Saved data: KV and SQLite share 10,000,000 bytes per site; files have a separate 10 MB allowance per site; one file upload from a page is under 1,000,000 bytes. `storage_get_usage` shows what is used.
- The Add photos page makes small WebP or JPEG copies on the device.

## Keep website files small

Before deploying, check the size of every file and the whole output folder. Read `who_am_i` without `include_email` (or `GET /v1/me`) for account file usage and `list_sites` (or `GET /v1/sites`) for site sizes. Simple Host normally keeps 4 versions, allows 200 MB including live files and retained versions for new websites from the 2026-10-05 deployment, and 1 GB per account. Earlier websites have no total cap; operator exceptions apply. The live copy is separate and counts too. The server checks after pruning, so replacing a site uses the resulting total. Saved data and storage resources have their own limits.

For personal photos, call `create_upload_link` (a photo picker in the chat, or `photos_url` to give the person); the page shrinks them. Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use. Delete old sites you don't need.

If a deploy returns `site_total_too_large`, `account_storage_full` or `site_too_large`, follow its tips and reduce the files before retrying. Ask which old sites the person no longer needs before deleting any. Only accounts enabled by the operator may change the version count; other accounts get “Simple Host keeps your 4 latest versions”.

## Redirects and plain addresses

Publish a root `_redirects` text file with `FROM [key=value ...] TO [STATUS]`, one rule per line. Paths, `:name` segments and final `*` splats are supported; use `:splat` in targets. Targets are local paths or absolute HTTP(S) URLs with a literal host. Status is 301 by default, or 302, 404 or 410; append `!` to override existing content. 404/410 serve a local page. First match wins; matching ignores trailing slashes. Rules run after site access gates. Query strings carry over only without query conditions or a target query.

The file is validated on publish and never served. Limits: 128 KiB, 2048 bytes per line, 1000 rules by default (`REDIRECTS_MAX_RULES` on self-hosted installs). Fix publish errors at their reported line numbers. `/faq` serves `faq.html` directly with 200; directory-only `faq/index.html` keeps the 301 to `/faq/` for relative links.
