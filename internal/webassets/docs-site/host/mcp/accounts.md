# Account

## who_am_i

<div data-tool="who_am_i"></div>

Return the Simple Host account this connection acts as: its handle (the &lt;handle&gt; in its page https://&lt;handle&gt;.simple-host.app/ and its site addresses https://&lt;site&gt;.&lt;handle&gt;.simple-host.app/) and its public page listing its sites. Email is returned only when include_email is true.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| include_email | boolean |  | Set true only when the person asks which email they are signed in with. |

## set_bio

<div data-tool="set_bio"></div>

Set the short plain-text bio shown on the person's public page (https://&lt;handle&gt;.simple-host.app/, the showcase of their sites). An empty string clears it; the limit is about 280 characters. It replaces the current bio, which is not kept anywhere, so ask the person before replacing or clearing a bio they wrote (their public page shows the current one).

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| bio | string | yes | Plain text, or empty to clear the bio. |

## set_showcase_site

<div data-tool="set_showcase_site"></div>

Pin a site to the top of the person's public page (the showcase of their sites), or set its position. Pinned sites come first, then smaller order numbers. Unlisted, offline, and passcode-protected sites never appear there, pinned or not. Supply pinned, order, or both.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| order | integer |  | Manual position among the pinned or unpinned sites: smaller numbers come first. |
| pinned | boolean |  | true pins the site to the top of the showcase; false unpins it. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
