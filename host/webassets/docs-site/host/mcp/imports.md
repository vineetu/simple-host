# Imports and uploads

## create_import_link

<div data-tool="create_import_link"></div>

Move a WordPress site here from the export the person downloads from WordPress themselves (WordPress.com: Tools &gt; Export). Each call makes a new link. Returns a one-time page link (30 minutes, this site only) for them to open in their browser and drop the files on. The import creates the site, or publishes a new version of it, at its public address, so it can replace what is live (the previous version is kept for rollback_site). When they say it finished, call get_import_report. Never paste the link into a web page.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
| theme_url | string |  | Optional old site's address, prefilled on the upload page to copy its look. |

## get_import_report

<div data-tool="get_import_report"></div>

Read your import's progress, report and exact setup calls for its dynamic features. After it finishes, run those calls and check the pages. Write root _redirects rules from the old address list for any changed paths (including wildcards and ?p=123 query links); fix publish errors at their line numbers.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| import_id | string | yes | The import_id returned by create_import_link. |

## create_upload_link

<div data-tool="create_upload_link"></div>

Opens a photo picker in the chat (apps that show MCP Apps); elsewhere returns photos_url to give the person. Call it as soon as a page needs the person's own photos (menu dishes, a listing, portfolio, team) and the site exists (publish the page first), even before they mention photos, instead of building an upload page or asking them to attach, convert, resize, or host photos. They pick any format on phone or computer; each is shrunk into images/, keeping other files. When they say done, get_site and place them. Photos visitors share are the gallery recipe instead. archive: true gives shell agents a whole-site replacement upload. Each call makes a new link.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| archive | boolean |  | Shell archive upload that replaces the whole site. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
