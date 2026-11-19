# Limits and keeping files small

## Numbers at a glance

- Each upload may be at most 100 MB.
- On simple-host.app, Simple Host keeps 4 versions of a site; a site may total 200 MB including retained versions; 1 GB per account.
- Saved data: KV and SQLite share 10,000,000 bytes per site; files have a separate 10 MB allowance per site; one file upload from a page is under 1,000,000 bytes. `storage_get_usage` shows what is used.
- The Add photos page makes small WebP or JPEG copies on the device.

## Keep website files small

Before deploying, check the size of every file and the whole output folder. `who_am_i` shows
account file usage and `list_sites` shows site sizes. The live copy and retained versions both
count; the server checks after pruning old versions.

For personal photos, give the person `create_upload_link`'s `photos_url`; the page shrinks them.
Keep zip files, installers and videos elsewhere and link to them. Remove files you no longer use.

If a deploy returns `site_total_too_large`, `account_storage_full` or `site_too_large`, follow its
tips and reduce the files before retrying. Ask which old sites the person no longer needs before
deleting any.
