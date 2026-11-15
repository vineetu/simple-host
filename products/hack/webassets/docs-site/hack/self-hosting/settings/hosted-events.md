# Hosted events

`EVENTS=hosted` runs a server as a hosted hackathon platform, the way simple-hack.app runs:
anyone who signs in creates an event, the event's public page is `<event>.<SITE_DOMAIN>`, and
(from the team-sites milestone) each team's site is `<team>.<event>.<SITE_DOMAIN>`. Accounts on
such a server own no personal sites. Design: `docs/designs/simple-hack-platform.md`.

There are no caps on how many participants, teams or judges an event has. The limits below only
keep one account from flooding the server, and every one is an environment setting.

<!-- settings:group=events -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `EVENT_LIMIT_EXEMPT` | none | emails and domains | Comma-separated sign-in emails and whole domains (@domain.org), case-insensitive with spaces trimmed; no subdomain matching. Empty by default. Admins and EVENT_LIMIT_EXEMPT accounts are never limited by EVENT_CREATE_PER_DAY (default 20) or EVENT_MAX_ACTIVE_PER_ORGANISER (no limit by default; 0 = no limit for either). |
| `EVENT_CREATE_PER_DAY` | `20` | 0–1000 events | Hosted events (EVENTS=hosted): events one account may create in a rolling day (default 20; 0 = no limit). Admins and EVENT_LIMIT_EXEMPT accounts are never limited. |
| `EVENT_MAX_ACTIVE_PER_ORGANISER` | `0` | 0–1000 events | Hosted events: events one account may run at once (every stage but archived); no limit by default (0 = no limit). Admins and EVENT_LIMIT_EXEMPT accounts are never limited. |
| `EVENT_APPLY_PENDING_PER_EVENT` | `500` | 1–100000 applications | Hosted events: applications one event may have waiting for a decision at once, apart from the per-account pending cap. |
| `EVENT_TEAM_SIZE_DEFAULT` | `20` | 1–50 people | Hosted events: the team size cap a new event starts with; the organiser changes it. |
| `EVENT_SITES_KEEP_DAYS` | `30` | 1–3650 days | Hosted events: how long team sites stay up after an event closes, before they are removed (the organiser is warned by email first). The event page and results stay. |
| `EVENT_REMOVAL_WARN_DAYS` | `14` | 1–3650 days | Hosted events: how long before team sites are removed the organiser is warned by email, once. The event page and results stay. |
| `HACK_INSTANCE_BUDGET_GB` | `10` | 0–100000 GB | Hosted events: disk the whole instance may use for team sites; new events are refused above 80% of it. 0: no budget. |
| `HACK_EVENT_ICON_MAX_BYTES` | `131072` | 1–4194304 bytes | Hosted events: largest PNG, JPEG or WebP event icon upload in bytes. |
| `RATE_LIMIT_EVENT_CODES_IP` | `120,1s` | any (warns past 10× looser) | Hosted events: join, judge and team-code lookups and joins, per network address (roomy: a whole venue can share one address). |
| `RATE_LIMIT_EVENT_CODES_USER` | `20,3s` | any (warns past 10× looser) | Hosted events: join, judge and team-code joins and lookups, per account. |
| `RATE_LIMIT_EVENT_NAMES_USER` | `60,1s` | any (warns past 10× looser) | Hosted events: event address checks while someone types one, per account. |
| `RATE_LIMIT_EVENT_APPLY_IP` | `30,2m` | any (warns past 10× looser) | Hosted events: applications to an event without a join link, per network address. |
| `RATE_LIMIT_EVENT_APPLY_USER` | `5,12m` | any (warns past 10× looser) | Hosted events: applications to an event without a join link, per account. |
| `EVENTS` | `off` | `off` / `hosted` | hosted runs this server as a hosted hackathon platform (simple-hack.app): anyone signed in creates an event with its own page at &lt;event>.&lt;SITE_DOMAIN>, and accounts own no personal sites. |
| `HACK_DEMO_EVENT_SLUG` | `demo` | text | Base event slug for the standing demo on the hosted hackathon platform; empty uses demo. |
| `EVENT_NAME_PEER` | none | text | The loopback address (http://127.0.0.1:&lt;port>) of the other server handing out names under the same zone; each asks the other before taking a name. Used with EVENTS=hosted, or with EVENT_DNS_TOKEN (self-host claims); ignored otherwise. |
| `TEAM_SITE_FLAT_HOSTS` | `false` | `true` / `false` | Hosted events: true serves a team's site at &lt;team>--&lt;event>.&lt;SITE_DOMAIN> (one DNS label) instead of &lt;team>.&lt;event>.&lt;SITE_DOMAIN> (two), so one wildcard certificate and one ingress rule cover every event and team instead of a certificate SAN and a Helm upgrade per event. Hosted simple-hack.app keeps this off: behaviour is byte-for-byte unchanged. |
| `ASSISTANT_BASE_URL` | none | text | The OpenAI-compatible API root (e.g. an Ollama Cloud endpoint) the organiser assistant chat sends chat-completions requests to. Unset (with ASSISTANT_MODEL): the assistant is off and the UI shows nothing. |
| `ASSISTANT_MODEL` | none | text | The model name sent to ASSISTANT_BASE_URL for the organiser assistant chat. Unset: the assistant is off. |
| `ASSISTANT_API_KEY` | none | secret | Bearer key for ASSISTANT_BASE_URL, when the endpoint needs one. **Security-sensitive.** |
| `ASSISTANT_REASONING_EFFORT` | none | text | Sent as reasoning_effort when set; `none` turns thinking off on DeepSeek via Ollama Cloud. Leave empty for providers that reject it. |
| `ASSISTANT_MONTHLY_BUDGET_USD` | `5` | text | Instance-wide monthly spend ceiling for the organiser assistant, in US dollars. Once reached, every event's assistant refuses new questions until the 1st of the next month. |
| `ASSISTANT_DAILY_QUESTIONS` | `30` | text | Questions one organiser may ask the assistant per UTC day, across every event they organise. |
| `ASSISTANT_PRICE_INPUT_PER_MTOK_USD` | `0.5` | text | The configured model's input price, in US dollars per million tokens, used to account spend against ASSISTANT_MONTHLY_BUDGET_USD. |
| `ASSISTANT_PRICE_OUTPUT_PER_MTOK_USD` | `2.0` | text | The configured model's output price, in US dollars per million tokens, used to account spend against ASSISTANT_MONTHLY_BUDGET_USD. |
<!-- /settings -->

## Claim and application behavior

Re-claiming an event hostname extends its lifetime. The 60-day maximum prevents
a forgotten claim pointing at a recycled cloud address for months.
The pending-event application cap returns 409 `event_applications_full`.
