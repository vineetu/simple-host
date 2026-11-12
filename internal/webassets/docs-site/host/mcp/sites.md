# Sites and versions

## list_sites

<div data-tool="list_sites"></div>

List this account's own sites (admins: pass all_accounts for every account's), newest edited first. Call this before changing a site to get its exact name. Optionally filter with query, dates, order, and limit/offset for paging.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| all_accounts | boolean |  | Admins only: every account's sites instead of just this one. |
| created_after | string |  | Created on or after RFC3339 or YYYY-MM-DD (UTC). |
| created_before | string |  | Created on or before RFC3339 or YYYY-MM-DD (whole UTC day). |
| limit | integer |  | Maximum results; default 200 with filters, capped at 500. |
| offset | integer |  | Results to skip; default 0. |
| order | string, one of `updated`, `created`, `name` |  | Sort key; default updated (last edited first). |
| query | string |  | Words to find in the site's name, domain, title, description, headings or text; every word must match. |
| updated_after | string |  | Updated on or after RFC3339 or YYYY-MM-DD (UTC). |
| updated_before | string |  | Updated on or before RFC3339 or YYYY-MM-DD (whole UTC day). |

## get_site

<div data-tool="get_site"></div>

Show one site: its live address, active version, custom domain, and the list of files in the live version with their sizes. Call this before editing an existing site, then read_site_file for each file you will change. It names only the live version; list_versions shows every kept version.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## read_site_file

<div data-tool="read_site_file"></div>

Return the contents of one file of a site (path), or of up to 10 at once (paths), from the live version unless `version` is given. Read only the files you will change; for a small change you can skip reading and use update_site's edits. Text files come back as text; binary files (images, fonts) are only described. Use update_site with keep_other_files: true to keep files you are not changing.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| path | string |  | Path of the file from the site root, e.g. `index.html` or `css/style.css`. |
| paths | array of string |  | Several paths read in one call, instead of path. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| version | integer |  | Version number to read from. Defaults to the live version. |

## create_site

<div data-tool="create_site"></div>

Publish a NEW website from files given inline, at a public address. Fails if this account already has a site of that name, so it never overwrites anything; to change an existing site use update_site. When the person says "my site", "our site", or "my salon site", call list_sites first: if they already have that site, add to it with update_site instead of making a new one. `index.html` is required. Use relative links only (`css/style.css`, never `/css/style.css`), because a site can be served under a path as well as at a root. The site is public to anyone with the returned URL as soon as this returns. A root _redirects file holds redirect rules, is validated on publish and is never served. A form visitors send (contact, quote, booking, sign-up, order) must save to storage: call get_page_recipe first; never use a mailto: link or an outside form service. Check who_am_i for account file usage and list_sites for site sizes before deploying. Hosted Simple Host keeps 4 versions; new sites have a 200 MB whole-file allowance and accounts 1 GB (operator exceptions apply). The separate live copy and every kept version count; saved data and storage resources do not. For the person's own photos, give them create_upload_link's photos_url (it shrinks them); never ask them to resize or attach photos. Keep zip files, installers and videos elsewhere and link to them. Remove unused files; ask the person which old sites they no longer need.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| files | object | yes | Map of file path → text content, e.g. {"index.html": "&lt;!DOCTYPE html&gt;…", "css/style.css": "body{…}"}. Paths are relative, no leading slash, no `..`. |
| files_base64 | object |  | Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. Pick a short, descriptive name. |

## update_site

<div data-tool="update_site"></div>

Replace the live files of an EXISTING site with a new version, at its public address. By default the files you send are the complete new version. To change one or two pages of a big site, send just those files with keep_other_files: true. remove_files lists paths to drop, only when keeping other files. For a small change (hours, a price, a line of text), send edits instead: each {path, find, replace} replaces text that occurs exactly once in the live file; if a find is missing or repeated, nothing is published. `index.html` is required unless keeping other files; use relative links only. The previous version is kept and can be made live again with rollback_site. Fails if there is no site of that name (use create_site). A root _redirects file holds redirect rules, is validated on publish and is never served. A form visitors send (contact, quote, booking, sign-up, order) must save to storage: call get_page_recipe first; never use a mailto: link or an outside form service. Check who_am_i for account file usage and list_sites for site sizes before deploying. Hosted Simple Host keeps 4 versions; new sites have a 200 MB whole-file allowance and accounts 1 GB (operator exceptions apply). The separate live copy and every kept version count; saved data and storage resources do not. For the person's own photos, give them create_upload_link's photos_url (it shrinks them); never ask them to resize or attach photos. Keep zip files, installers and videos elsewhere and link to them. Remove unused files; ask the person which old sites they no longer need. With `publish: false` the new version is only stored, not made live: visitors keep seeing the current one, and the answer carries a preview link (anyone with it can open it, for one hour) to look at it first; make it live with rollback_site when the person is happy. `author` names who made the version in list_versions.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| author | string |  | Who made this version, shown in list_versions; free text, for example a person's name. |
| edits | array of object |  | Small changes to live text files, instead of sending them whole; every other file is kept. |
| files | object |  | Map of file path → text content, e.g. {"index.html": "&lt;!DOCTYPE html&gt;…", "css/style.css": "body{…}"}. Paths are relative, no leading slash, no `..`. |
| files_base64 | object |  | Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps. |
| keep_other_files | boolean |  | Keep unchanged files from the live version; default false. |
| publish | boolean |  | Default true: the new version goes live at once. false stores it without making it live, and returns a preview link. |
| remove_files | array of string |  | Paths to remove; only with keep_other_files. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## set_keep_versions

<div data-tool="set_keep_versions"></div>

Set a site's version count; older versions are removed for good. Only accounts enabled by the operator may change it. Other accounts get: Simple Host keeps your 4 latest versions. 0 uses the account default. The live version always stays.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| keep_versions | integer | yes | How many versions to keep (the live one included); 0 goes back to the account default. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## list_versions

<div data-tool="list_versions"></div>

List the versions kept for a site, newest first, with which one is live. Call this before rollback_site. Each version in the text may name its author (whoever the deployer stated); a version marked not_yet_live can be made live with rollback_site, or removed with discard_version where that tool is offered. `pending: true` lists only those not-yet-live versions.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| pending | boolean |  | true lists only versions stored with publish: false that have never been live. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## rollback_site

<div data-tool="rollback_site"></div>

Make one of a site's kept versions live (visitors see it immediately): an earlier one, or one stored with update_site `publish: false` after the person has looked at its preview. Nothing is deleted; the current version stays available and can be restored the same way. Confirm the version with the person first.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| version | integer | yes | The version number to make live, from list_versions. |

## preview_version

<div data-tool="preview_version"></div>

Make a preview link for one of a site's kept versions (from list_versions): the person opens it in their browser to see that version exactly as visitors would, before making it live with rollback_site. The link works for anyone who has it, for one hour and only for that version; pages opened from it cannot save anything, and search engines do not index it. Give it to the person to click; do not post it anywhere public.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| version | integer | yes | The version number to preview, from list_versions. |

## discard_version

<div data-tool="discard_version"></div>

DESTRUCTIVE: removes, for good, a version stored with update_site `publish: false` that has never been live. The live version and versions that have been live cannot be discarded (retention prunes those). Confirm with the person first, naming the version number from list_versions.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| version | integer | yes | The never-live version to discard, from list_versions (marked not_yet_live). |

## delete_site

<div data-tool="delete_site"></div>

DESTRUCTIVE: takes the site offline at once, with every version and all of its saved data (storage resources included). It stays in Recently deleted for 7 days, where restore_site brings it back exactly as it was; after that it is gone for good. Its name stays taken until then. Only call this after the person has explicitly confirmed, in this conversation, that they want this specific site deleted. Pass the site name twice: as `site` and as `confirm_name`.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| confirm_name | string | yes | The same site name again, typed out, as confirmation. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## list_deleted_sites

<div data-tool="list_deleted_sites"></div>

List the account's sites in Recently deleted: each was deleted within the last 7 days and can be brought back with restore_site until its purge_at time, when it is removed for good.

## restore_site

<div data-tool="restore_site"></div>

Bring back a site from Recently deleted (see list_deleted_sites): same name, same address, every version, saved data and connected address, live again at once.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## rename_site

<div data-tool="rename_site"></div>

Give a site a new name, which changes its address. Links to the old address keep working: they redirect to the new one until a new site is created with the old name. A connected custom domain stays attached. Not for "a shorter link" or "an address without my username": that is connect_domain with a free &lt;name&gt;.simple-host.app.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| new_name | string | yes | The new site name: lowercase letters, numbers and hyphens. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## set_home_page

<div data-tool="set_home_page"></div>

Make one of the person's sites their home page, served at their personal address https://&lt;handle&gt;.simple-host.app/ instead of the showcase (the list of their sites); site null brings the showcase back. The site's own address keeps working too. If that site goes offline or is deleted, the showcase shows again.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string or null | yes | Your site's name, or null for the showcase. |

## set_visibility

<div data-tool="set_visibility"></div>

List on my profile: on shows the site on the owner's profile page (https://&lt;handle&gt;.simple-host.app/); off leaves it off. New sites start off. Every open site can be found by search engines. To keep a site out, use a passcode or named viewers, take it offline, or ship your own robots.txt / noindex.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| visibility | string, one of `public`, `unlisted` | yes | List on my profile: `public` = on; `unlisted` = off. Does not affect search engines. |

## set_site_offline

<div data-tool="set_site_offline"></div>

Take a site offline (`offline: true`): every address of it shows a plain "This site is offline" page and visitors can no longer save anything (RSVPs, votes, sign-ups, private lists), read its saved data or lists, or sign in, for example when an event is over or a form must stop taking entries. Nothing is deleted: files, versions, saved data and lists are kept, and you can still update, read and export it. `offline: false` puts it back online at every address. Confirm with the person before taking a site offline.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| offline | boolean | yes | true takes the site offline; false puts it back online. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## keep_site

<div data-tool="keep_site"></div>

Mark a site Keep (or clear the mark). Simple Host emails the owner about a site nobody has visited or updated for 90 days and moves it to Recently deleted 30 days later unless it is kept; a site marked Keep is never flagged. Sites with their own domain or claimed name are never flagged either.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| keep | boolean |  | true (default) keeps the site up for good; false lets it be flagged again when idle. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## site_analytics

<div data-tool="site_analytics"></div>

Visits to a site over the last N days, split into people (not counting the owner's own visits), bots and monitoring, with the most viewed pages and the domains visitors came from (people only). When asked how many people visited, report the `person` numbers only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| days | integer |  | Window in days (1–90, default 30). |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## export_site

<div data-tool="export_site"></div>

Make a download link for a copy of one of the person's sites: a .tar.gz holding its live files and its saved data (storage resources with their KV values, SQLite databases, and files, plus any older state and collections). The link works for 10 minutes and only for that site; give it to the person to click, and make a new one if it has expired. Do not post it anywhere public: until it expires, anyone with it can download the copy.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
