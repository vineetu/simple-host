# Sites and versions

## hack_get_event_website

<div data-tool="hack_get_event_website"></div>

**Read event website mode**

Read whether the built-in or custom event website is live and list published versions and upload limits. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| slug | string | yes | Event name. |

## hack_publish_event_website

<div data-tool="hack_publish_event_website"></div>

**Publish event website files**

Publish a complete event website from inline files (include index.html, with optional files_base64 for assets) or one archive_base64 tar.gz. This replaces the custom site's files, keeps version history, and switches the event host to custom. Ask the organiser before publishing. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| archive_base64 | string |  | Optional base64-encoded tar.gz archive; use instead of files and files_base64. |
| files | object |  | Map of site-relative path to UTF-8 file contents. |
| files_base64 | object |  | Optional map of file path → base64 content, for binary files such as images. A path must not appear in both maps. |
| slug | string | yes | Event name. |

## hack_set_event_website_mode

<div data-tool="hack_set_event_website_mode"></div>

**Choose event website**

Switch the event host between builtin and custom. The custom website must have been published first; switching back preserves its files. Ask the organiser first. Organiser only.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| mode | string | yes | builtin: the event page Simple Hack renders itself; custom: the website the organiser published with the event website tools. |
| slug | string | yes | Event name. |
