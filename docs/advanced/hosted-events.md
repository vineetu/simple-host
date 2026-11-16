# Hosted events

Run `simple-hack` (built from `cmd/simple-hack`) as a hosted hackathon platform, the way simple-hack.app runs:
anyone who signs in creates an event, the event's public page is `<event>.<SITE_DOMAIN>`, and
(from the team-sites milestone) each team's site is `<team>.<event>.<SITE_DOMAIN>`. Accounts on
such a server own no personal sites. Design: `docs/designs/simple-hack-platform.md`.

`simple-host` (built from `cmd/simple-host`) runs the hosting platform without the events product.
`EVENTS` is compatibility-only and no longer selects the product: `simple-hack` accepts
`EVENTS=hosted` or unset, while `simple-host` refuses `EVENTS=hosted` at startup.

There are no caps on how many participants, teams or judges an event has. The limits below only
keep one account from flooding the server, and every one is an environment setting.

<!-- settings:group=events -->
| Setting | Default | Allowed | What it does |
|---|---|---|---|
| `EVENT_NAME_PEER` | none | text | The loopback address (http://127.0.0.1:<port>) of the other server handing out names under the same zone; each asks the other before taking a name. Used with EVENTS=hosted, or with EVENT_DNS_TOKEN (self-host claims); ignored otherwise. |
| `TEAM_SITE_FLAT_HOSTS` | `false` | `true` / `false` | Hosted events: true serves a team's site at <team>--<event>.<SITE_DOMAIN> (one DNS label) instead of <team>.<event>.<SITE_DOMAIN> (two), so one wildcard certificate and one ingress rule cover every event and team instead of a certificate SAN and a Helm upgrade per event. The default is false. |
<!-- /settings -->

## Claim and application behavior

Re-claiming an event hostname extends its lifetime. The 60-day maximum prevents
a forgotten claim pointing at a recycled cloud address for months.
The pending-event application cap returns 409 `event_applications_full`.
