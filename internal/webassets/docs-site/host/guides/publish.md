# Sites and deploy

## Publish a site
[What this is](/features.html#deploy-inline-files)


1. Prepare `index.html` and use relative links for the other files. Keep servers and build tools out of the upload; upload their static output.
2. Publish it.

      **In chat**
“Publish these files as my bakery site.” Confirm the name and address before the first publish.   **API**
Send `POST /v1/sites/{sitename}/files` with `{"files":{"index.html":"..."}}`, or send an archive to `POST /v1/sites/{sitename}` with `Content-Type: application/zip`. Send your key in `X-API-Key`.   
3. Open the returned address and test the links and form saves.


## Update files
[What this is](/features.html#deploy-update)


1. Read the current file list before replacing files.
2. Send the change.

      **In chat**
“On my bakery site, change the opening time to 8 am. Keep the other pages.”   **API**
Send `PUT /v1/sites/{sitename}/files?keep=1` with the changed `files`. To replace the whole file tree, omit `keep=1` and send every file.   
3. Open the live address and check the changed page.


## Preview and publish a version
[What this is](/features.html#site-preview)


1. Save the update with `publish=false`. Read `GET /v1/sites/{sitename}/versions` and note its version number.
2. Create a preview with `POST /v1/sites/{sitename}/versions/2/preview-link`. Open the returned link; it stops working after an hour.
3. Publish it with `PUT /v1/sites/{sitename}/active-version` and `{"version_number":2}`. To roll back, read the version list and send the earlier number instead.


## Rename or take a site offline
[What this is](/features.html#site-rename)


1. Choose the site and check its current name with `GET /v1/sites`.
2. Send `PATCH /v1/sites/{sitename}` with `{"name":"new-name"}` to rename it, or `{"offline":true}` to take it offline.
3. To reopen it, send `{"offline":false}` to the current name.


## Delete or restore a site
[What this is](/features.html#site-delete)


1. Download a copy with `GET /v1/sites/{sitename}/export.tar.gz` if you need it.
2. Confirm the site name, then send `DELETE /v1/sites/{sitename}`.
3. To undo, list `GET /v1/me/deleted-sites` and send `POST /v1/sites/{sitename}/restore` before the returned deadline. A deleted site is restorable for 7 days.


## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `site_exists` (409) | Name already used | Update that site or choose another name |
| `not_found` (404) | Update files: no matching site | Read the site list |
| `site_not_found` (404) | Rename or roll back: no matching site | Read the site list |
| HTTP 404 (no code) | Delete: no matching site | Read the site list |
| `item_too_large` (413) | Upload exceeds a cap | Check Limits and errors |

## Next
- [Storage and saved data](/docs/guides/storage.md)
- [Sign-in and access](/docs/guides/access.md)
