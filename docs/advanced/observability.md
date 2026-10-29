# Observability

- **Health:** `GET /healthz` answers 200 when the server is up.
- **Startup log:** the release and commit, every setting changed from its default, and a
  `WARNING` for a rate limit set far looser than its default or a `RATE_LIMIT_*` name that is not
  a setting (a typo). On a small box: `cd /opt/simple-host && sudo docker compose logs app`.
- **Admin page (`/admin`, admin key):** disk use, the running release, every account and site,
  API traffic by route and caller, and the saved-data watch.
- **Visit analytics** per site come from the web server's access log, not a tracking script.
  Visitor addresses are hashed with a salt; where a caller is comes from a database on the
  server, never a lookup elsewhere.

<!-- settings:group=observability -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `ANALYTICS_PAGES_PER_SITE_DAY` | `200` | 10–10000 pages | Distinct pages a site's Top pages keeps per day; views of further new pages that day are counted together as (other). |
| `ANALYTICS_REFERRERS_PER_SITE_DAY` | `100` | 10–10000 domains | Distinct referring domains a site keeps per day; further new domains that day are counted together as (other). |
| `NETWORK_MONTHLY_ALLOWANCE_GB` | `10240` | 0–10000000 GB | The box's free outbound data a month, in GB (1 TB = 1024 GB), which the admin page's network bar measures this month's outbound against. 10240 is Oracle Cloud Always Free's 10 TB. 0 hides the bar. |
| `NETWORK_ALERT_PCT` | `75` | 1–100 percent | The share of NETWORK_MONTHLY_ALLOWANCE_GB at which the admin page's network bar turns amber, and the box's daily watch script sends its one alert of the month. |
| `NETWORK_SAMPLE_MINUTES` | `60` | 1–1440 minutes | How often the box's network counters are added to the month's totals. The admin page adds the growth since the last sample itself, so this only bounds what a reboot can lose. |
| `API_METRICS_FLUSH_SECONDS` | `20` | 5–3600 seconds | How often counted API calls are written to the database for the admin page's API traffic and growth. Calls counted since the last write are lost if the server stops abruptly. |
| `AGENTCAT_PROJECT_ID` | none | text | AgentCat project for MCP tool-call events (arguments, results, and stated intent, with secrets and file contents left out); empty disables sending. |
| `ANALYTICS_LOG` | none | text | The web server's access log that visit analytics are read from. Empty: no analytics. |
| `ANALYTICS_SALT` | none | secret | Salt for the hashed visitor addresses in analytics. Empty: derived from ADMIN_API_KEY. **Security-sensitive.** |
| `NETWORK_INTERFACE` | `<default route>` | text | The network interface whose traffic the admin page reports as the box's own. Empty: the one the default route leaves by. Inside a container, where that is the container's own, network use is off unless this is set. |
| `GEOIP_DIR` | `<DATA_DIR>/../geoip` | text | Where the local location databases are. Missing files mean blank locations, never a lookup elsewhere. |
<!-- /settings -->

## AgentCat tool-call context

Hosted Simple Host sends the existing usage fields plus arguments and results (8 KB of JSON
for each) and stated intent (2,048 characters). Other installations are not enrolled. The
optional `context` input appears whenever analytics is enabled, for every caller including
ChatGPT, OpenAI, and Codex connections (owner, 2026-10-29). It is removed before validation
unless the tool defines its own context. Wrapped calls use the
inner operation's arguments and intent, with outer intent as a fallback. Arguments are
sanitized once. Unknown or retired targets, forbidden discovery targets (`run_tool` and
`find_tools`), and non-object inner arguments send `parameters: {"arguments": "[removed]"}`.
The resource name and error reporting stay unchanged. Non-string context is also removed
before validation, but only a string becomes intent.

The sender makes a separate copy before enqueueing. Its field inventory, checked against
the connector schemas and outputs on 2026-10-29, is:

- Secrets: field names containing `passcode`, `password`, `secret`, `token`, `apikey`,
  `authorization`, or `cookie` after lowercasing and removing underscores and hyphens;
  also `code`, `signincode`, `verificationcode`, `teamkey`, `signingkey`, and `privatekey`.
  This includes `passcode_protected` and `passcode_set_at` under the requested name rule.
  Plain `key` is a credential only in `hack_create_team_key` and `hack_get_team_key`;
  storage KV key names remain readable. `auth_header` is removed as a whole because its
  credential is in a child named `value`. Webhook signing secrets are carried by REST
  and `events/subscribe`, not current tool-call results; `secret` is covered structurally.
- Links: `upload_url`, `photos_url`, `import_url`, `download_url`, `signed_url`,
  `preview_url`, `pinned_url`, `join_url`, `judge_url`, and `invite_url`, plus `join_code`
  and `judge_code`. Plain `url` is removed for preview, export, storage download, Hack
  archive export, co-organiser invitation creation, and code regeneration tools.
  `create_upload_link.how` is removed because its curl command repeats the signed URL.
- Files: the path-to-content `files` and `files_base64` maps for site publishing, including
  `hack_publish_event_website`; `update_site.edits[].find` and `.replace`; every `*_base64`
  field, including `content_base64`, `image_base64`, and `archive_base64`; `base64` in
  screenshot tools; and `storage_put_file.data`. JSON-string forms of files and edits
  receive the same treatment. Malformed edit entries and unknown fields inside edit objects
  become file size/type placeholders; only `path`, `find`, and `replace` are recognized.
  `read_site_file.content` and `files[].content`, and the
  `csv` field in all five Hack CSV exports, become size/type objects. File-read text
  uses the resulting metadata. Data URIs beginning with `data:` and containing
  `;base64,` replace whole string values in arguments and structured/JSON results, including
  sponsor `logo_data`, and whole non-JSON text blocks. They are not searched for inside
  ordinary prose. Intent uses the same data-URI check (`[file: N bytes, <type>]`) and SDK
  large-base64 check before its length cap, with no other processing. WordPress and photo
  upload bytes travel over their upload HTTP routes, not tool calls; the tool returns
  only the link or report. Unlabelled base64 strings at least 10 KB long use the SDK's
  binary-data placeholder instead.
- Emails: `email` and `emails` in account, named-viewer, storage, and Hack tools;
  Hack `contact_email` (event create/update arguments and `organiser.contact_email` in
  event results); `visitor_email` and `visitor_emails`; and viewer `added`, `already_listed`, and `removed`
  fields. A visitor reference in the same object replaces the email, otherwise the
  value is `[removed]`. SQL `columns` and positional `rows` are treated as named fields
  too, visiting each cell once under its column's field policy and skipping a second
  generic walk of `rows`. The owner's email in `who_am_i` is removed. Viewer,
  `set_site_access` (including non-JSON errors), and visitor-lookup prose uses
  the sanitized structured copy; JSON text results receive the same field treatment.

Passcode, upload/import link, preview/export, unpublished update, Hack team-key, archive,
invitation, event detail, and code-regeneration tools can repeat secrets in prose, so their
telemetry text blocks are replaced with `[removed: contains a secret]`. The client's text,
structured result, and metadata are unchanged. UI metadata is not included in telemetry.

The SDK's image, audio, binary-resource, unknown-content, and large-base64 placeholders
run after these removals. The structural walk stops at 32 levels with `[Object]` before
further descent. Depth 10, breadth 100, and string truncation with `...` run next,
with progressively smaller limits until each JSON field fits in 8,192 bytes. The event's
`error` remains the short server code; the response includes the tool's error result.
Request headers are not captured. AgentCat never receives the IP address a person connects from.

The SDKs have no built-in personal-data scrubber. AgentCat's project redaction removes
personal identifiers before storage. No local email, URL, credential-content, or other
personal-data pattern matching is used. Arbitrary prose, SQL aliases, and user-defined
fields outside this inventory rely on that server-side redaction. The local tests verify
what is posted, not AgentCat's storage or project configuration.
