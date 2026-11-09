# Showcase

Public feed of a person’s showcased sites.

## GET /v1/u/{handle}/showcase.json

<div data-operation="GET /v1/u/{handle}/showcase.json"></div>

Exactly the public showcase projection. Unlisted, passcode-protected, offline, taken-down and deleted sites are omitted, as are sites of suspended accounts. Independent of the chosen home. Public without a key, CORS wildcard without credentials, cached 30 seconds and rate limited like public reads. Hosted events excluded.

No authentication required.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| handle | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Public showcase |
| 404 | Handle not found or hosted event mode |
| 429 | Rate limited (code rate_limited). Saved-data reads allow 30 a second per address with a burst of 60 (SAVED_DATA_READ_PER_SEC, SAVED_DATA_READ_BURST). |
