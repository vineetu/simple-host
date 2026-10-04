# Simple Hack story and ink theme — 2026-10-04

The owner-approved 12-scene story replaces the hosted root landing page. Its
wording, drawings and navigation are retained. Changes to the source are the
HTML envelope, real action routes, shared theme initialization and local fonts.

## Claim audit

| Story/home claim | Result | Product evidence |
| --- | --- | --- |
| Create an event page and share one join link | True | Hosted create flow and join codes |
| One page on your own event subdomain, with dates, rules and prizes | True | Event hostname dispatch and built-in page |
| Teams form on their own | True | Participants create a team or join with a team code; no automatic matchmaking is implied |
| Sign-up with email and the code of conduct | True | Email-code sign-in and required CoC acceptance |
| Judges have their own room and judging screen | True | Judge link, queue, rubric scoring and comments |
| Free | True | Hosted event creation has no payment flow |
| Run it with an agent sidekick; draft details, collect links, prepare scorecards | True | Organiser/member connector tools and toolkit |
| Organisers, builders and judges can participate | True | Existing member roles and permission boundaries |
| Connect from supported AI chat apps | True | Get started flow and connector instructions |
| Self-host on your own server/domain | True | Same downloadable server supports standalone event mode |
| Your events, directory, privacy, terms, report and how it works | True | Real app routes and the owner's existing first-party flow page |

No claim needed a wording correction.

## Verification

Playwright uses the prescribed Chromium binary. Public pages and signed-in
organiser, participant, judge, voter and administrator pages are checked at
320, 390 and 1280 pixels in light and dark. Expected HTTP error documents
(404, take-down and unavailable pages) are distinguished from script errors.
The fixture uses real app handlers, a disposable database and a local email
sink; only certificate readiness and email delivery are simulated. The team
project goes through the real publishing API, as an agent would publish it.

The browser sweeps passed 90 public, 126 signed-in and 42 admin layout checks,
plus six populated-directory checks. Shared theme override persistence and the
judge's real project link also passed. `make check` and docs sync passed against
a separate throwaway Postgres database (including fresh-install migrations).

The film rehearsal covers tap, swipe, keys, wheel, skip, return visit, replay,
finish, scene deep link and browser Back. Event rehearsal covers sign-in,
creation, all manage tabs, colour selection, joining/CoC, team creation,
project publication, judging, voting, lock/publish and results.

Simple Host's existing index and site.css have byte-level regression tests.
Its production home and signed-in dashboard have before/after screenshots
at each width and theme. Local browser evidence is in `/tmp/hack-ink-evidence`.

The built-in event page is restyled; organiser-authored custom websites keep
their authors' styles. The seven crayon accents preserve slug-derived variety,
with an optional organiser selection shared by the page, cards and icon.

The first client deployment gate found that HTML filename routes (such as
`/privacy.html`) did not carry no-store. It restored the prior binary. The
hosted-only file-serving branch now uses the same cache policy as clean routes,
with a regression test; Simple Host retains its original filename cache policy.

The corrected deployment passed both services' public health and readiness,
all changed apex routes, local font requests, nonce CSP and no-store headers,
unknown-route rendering, site/person hosts, legacy 302 and neighbour checks.
Both production databases track migration z8. Simple Host's home and signed-in
dashboard have 12 byte-identical before/after screenshot pairs. Both services
are active, and their restart logs have no boot errors.
