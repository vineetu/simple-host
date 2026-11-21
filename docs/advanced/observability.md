# Observability

- **Health:** `GET /healthz` answers 200 when the server is up.
- **Startup log:** the release and commit, every setting changed from its default, and a
  `WARNING` for a rate limit set far looser than its default or a `RATE_LIMIT_*` name that is not
  a setting (a typo). On a small box: `cd /opt/simple-host && sudo docker compose logs app`.
- **Admin page (`/admin`, admin key):** disk use, the running release, every account and site,
  and API traffic by route and caller.
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
| `AGENTCAT_PROJECT_ID` | none | text | AgentCat project for MCP tool-call events (arguments, results, stated intent, and product, role and stage tags, with secrets, file contents and people's details left out); empty disables sending. |
| `PAPER_METRICS` | `on` | `on` / `off` | Collects local paper metrics; off, false or 0 disables collection. |
| `ANALYTICS_LOG` | none | text | The web server's access log that visit analytics are read from. Empty: no analytics. |
| `ANALYTICS_SALT` | none | secret | Salt for the hashed visitor addresses in analytics. Empty: derived from ADMIN_API_KEY. **Security-sensitive.** |
| `NETWORK_INTERFACE` | `<default route>` | text | The network interface whose traffic the admin page reports as the box's own. Empty: the one the default route leaves by. Inside a container, where that is the container's own, network use is off unless this is set. |
| `GEOIP_DIR` | `<DATA_DIR>/../geoip` | text | Where the local location databases are. Missing files mean blank locations, never a lookup elsewhere. |
<!-- /settings -->

## AgentCat tool-call context

Any install with `AGENTCAT_PROJECT_ID` set sends the existing usage fields plus arguments and
results (8 KB of JSON for each) and stated intent (2,048 characters). Hosted Simple Host and
hosted Simple Hack use the same AgentCat project and are told apart by tags. The
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
  large-base64 check, then the pattern scrubber below, before its length cap. Photo upload bytes travel over
  the upload HTTP route. The tool returns the link. Unlabelled base64 strings at least
  10 KB long use the SDK's binary-data placeholder instead.
- Emails: `email` and `emails` in account, named-viewer, storage, and Hack tools;
  Hack `contact_email` (event create/update arguments and `organiser.contact_email` in
  event results); `visitor_email` and `visitor_emails`; and viewer `added`, `already_listed`, and `removed`
  fields. A visitor reference in the same object replaces the email, otherwise the
  value is `[removed]`. SQL `columns` and positional `rows` are treated as named fields
  too, visiting each cell once under its column's field policy and skipping a second
  generic walk of `rows`. The owner's email in `who_am_i` is removed. Viewer,
  `set_site_access` (including non-JSON errors), and visitor-lookup prose uses
  the sanitized structured copy; JSON text results receive the same field treatment.

Passcode, upload link, preview/export, unpublished update, Hack team-key, archive,
invitation, event detail, and code-regeneration tools can repeat secrets in prose, so their
telemetry text blocks are replaced with `[removed: contains a secret]`. The client's text,
structured result, and metadata are unchanged. UI metadata is not included in telemetry.

The SDK's image, audio, binary-resource, unknown-content, and large-base64 placeholders
run after these removals. The structural walk stops at 32 levels with `[Object]` before
further descent. Depth 10, breadth 100, and string truncation with `...` run next,
with progressively smaller limits until each JSON field fits in 8,192 bytes. The event's
`error` remains the short server code. A failed or refused call's `response` is only
`Refused: <code>.` (the same code), never the tool's error text, which can echo what the
caller sent. Request headers are not captured. AgentCat never receives the IP address a
person connects from.

The SDKs have no built-in personal-data scrubber, so the server pattern-matches free text
(`scrubAnalyticsText` in `host/mcp/analytics_scrub.go`) on both products: the stated intent,
every argument name, and on Simple Hack every string kept in the arguments and result. A Simple
Hack tool's copy keeps a string only under a key allowed in `host/mcp/analytics_allow.go`
(slugs, stages, roles, statuses and other fixed-set values, names and titles of events, teams,
tracks and sites, IDs and times); every other string, and any text block that is not JSON
(replaced by the structured result), becomes `[removed]`, so a new free-text field is left
out until its key is allowed. Numbers and booleans are kept. The scrubber replaces
email addresses; links whose path holds `/join/`, `/judge/`, `/team/`, `/organiser/`,
`/invite`, an upload, download, preview or OAuth path, a token-like segment, a query or a
fragment; UUIDs; key-like strings (`shk_`, `shint_` and similar prefixes, or 32 or more
letters and digits); and code-shaped words (8 or more characters of the join-code alphabet
with a digit and a letter). AgentCat's project redaction removes further personal
identifiers before storage. Arbitrary prose on Simple Host, SQL aliases, and user-defined
fields outside this inventory on Simple Host rely on that server-side redaction. The local tests verify
what is posted, not AgentCat's storage or project configuration.

### Tags

Every event carries `tags`, which AgentCat indexes for filtering:

- `env`: `prod` on the product's hosted address (simple-host.app, simple-hack.app), `dev`
  elsewhere.
- `product`: `simple-host` or `simple-hack`, from the edition's product name.
- `protocol`: the MCP revision the request declared, when it declared one.
- Simple Hack only, read by the analytics worker after the reply so the call never waits:
  `role` is the caller's role in the event the call names (`organiser`, `participant`,
  `judge`, or `none` when not a member) and `stage` is that event's stage. A call names its
  event by `slug`, else by the `owner` a website call names (the event); a team-website
  connection uses the event that holds the team's site. Both tags are sent only when the
  caller is a member, is that team site's connection, or the event is listed publicly (Find
  a hackathon); otherwise, as for a call about no event or an unknown event name, neither is
  sent, so a refused call never reveals an unlisted event's stage.

Tag keys and values follow AgentCat's limits (32-character keys, 200-character one-line
values); anything else is dropped. A refusal's `error.message` is its code whenever the code
came from this server or its REST API (lowercase letters, digits and underscores), including
Simple Hack's own codes; anything else is `tool_error`.

On product (Simple Hack) tools the copy also removes, by class of field rather than by a
list of today's names (`analyticsProductPersonField` in `host/mcp/analytics_scrub.go`):

- people's names: `display_name`, `username`, and every `*_name` except team, track, event,
  file, site and similar non-person names (so `sender_name`, `judge_name`, `account_name`,
  `organiser_name` go), and `name` or `id` in a person's object (one with `user_id`,
  `email`, `display_name` and the like, or inside `members`, `judges`, `people`,
  `successors` and the other people lists);
- account IDs: any `*_id`/`*_ids` naming a user, account, judge, member, owner, organiser,
  sender, creator, successor or applicant, every `*_by`, `me_id`, and lists of names or IDs
  under a people key (`organiser_judges`, `organisers_staying`, `members`, `others`);
- contact: any string under a key containing `email` or `phone`;
- codes: any string under a key ending in `code` (join, judge, team, invite);
- applicants' free text: `answers`, `city`, `country`, `age_range`, `gender`, on every Hack
  tool (including `hack_join_event`).

Team and event names are sent. Every string then goes through the pattern scrubber above,
which also removes raw account UUIDs wherever they appear. The event's keyed actor hash
replaces the caller's ID. `products/hack/mcp/hack_analytics_privacy_test.go` fills every
Hack tool's input and output schema, recorded REST result shapes, and every JSON key the
Hack REST layer writes with a planted value and fails if one of these classes survives.

## Network and page counts

The admin Overview shows the box's bytes in and out this month from the main
interface's kernel counters, sampled into the database, and website bytes sent
from the analytics log's ninth field (`$bytes_sent`). Traffic rows follow
`ANALYTICS_RETENTION_DAYS`. Setting `NETWORK_MONTHLY_ALLOWANCE_GB=0` also disables
the alert. The daily alert script is `deploy/prod/sh-network-watch.sh`.

Top-page paths are normalized before counting: percent escapes decoded once,
`//` collapsed, query and fragment dropped. The referrer-domain cap bounds
referrer spam. Local location files are DB-IP Lite databases.
