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
| `EVENT_CREATE_PER_DAY` | `3` | 1–1000 events | Hosted events (EVENTS=hosted): events one account may create in a rolling day. |
| `EVENT_MAX_ACTIVE_PER_ORGANISER` | `2` | 1–1000 events | Hosted events: events one account may run at once (every stage but archived). |
| `EVENT_TEAM_SIZE_DEFAULT` | `4` | 1–50 people | Hosted events: the team size cap a new event starts with; the organiser changes it. |
| `EVENT_SITES_KEEP_DAYS` | `30` | 1–3650 days | Hosted events: how long team sites stay up after an event closes, before they are removed (the organiser is warned by email first). The event page and results stay. |
| `EVENT_REMOVAL_WARN_DAYS` | `14` | 1–3650 days | Hosted events: how long before team sites are removed the organiser is warned by email, once. The event page and results stay. |
| `HACK_INSTANCE_BUDGET_GB` | `10` | 0–100000 GB | Hosted events: disk the whole instance may use for team sites; new events are refused above 80% of it. 0: no budget. |
| `RATE_LIMIT_EVENT_CODES_IP` | `120,1s` | any (warns past 10× looser) | Hosted events: join, judge and team-code lookups and joins, per network address (roomy: a whole venue can share one address). |
| `RATE_LIMIT_EVENT_CODES_USER` | `20,3s` | any (warns past 10× looser) | Hosted events: join, judge and team-code joins and lookups, per account. |
| `RATE_LIMIT_EVENT_NAMES_USER` | `60,1s` | any (warns past 10× looser) | Hosted events: event address checks while someone types one, per account. |
| `EVENTS` | `off` | `off` / `hosted` | hosted runs this server as a hosted hackathon platform (simple-hack.app): anyone signed in creates an event with its own page at <event>.<SITE_DOMAIN>, and accounts own no personal sites. |
| `EVENT_NAME_PEER` | none | text | The loopback address (http://127.0.0.1:<port>) of the other server handing out names under the same zone; each asks the other before taking a name. Used with EVENTS=hosted, or with EVENT_DNS_TOKEN (self-host claims); ignored otherwise. |
<!-- /settings -->
