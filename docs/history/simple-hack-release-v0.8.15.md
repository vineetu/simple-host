# Simple Hack standalone v0.8.15

Status: published and verified on 2026-11-04.

Carries everything on main since hack-v0.8.14: password sign-in and self
sign-up for self-hosted installs (`PASSWORD_SIGNIN`, `PASSWORD_SELF_SIGNUP`,
`POST /v1/auth/password`, `POST /v1/auth/password/signup`, admin-created
accounts via `POST /v1/admin/password-users` with a temporary password the
person must change on first use, admin reset via
`POST /v1/admin/users/{id}/password`); a signed-in password account changing
its own password from the Account page (`POST /v1/me/password`, counted on
the sign-in limiters, never revoking other keys or sessions, with the same
Password card on the Host account page `showcase.html` as on `index.html`,
`GET /v1/me` reporting `has_password`, a connector OAuth token refused with
403 `connector_not_allowed`, and a wrong current password answered 403 so the
dashboard and Hack account screens don't read it as an expired key); flat
team-site hosts for hosted Kubernetes events (`TEAM_SITE_FLAT_HOSTS`, chart
value `teamSites.flatHosts`, off by default) serving a team at
`<team>--<event>.<host>` instead of `<team>.<event>.<host>` so one wildcard
certificate and Ingress rule cover every event and team, with simple-hack.app
itself left unchanged; and self-hosted Hack and dashboard pages hiding the
"run your own server" marketing that only makes sense on the hosted service.

Underneath, cleanup 3.1 through 5.4 continued the architecture work: an
immutable `edition.Edition` object is injected everywhere identity, storage
and notification policy used to be read from package globals; SQL moved out
of `internal/db` behind `app/*` services and matching `repository/*`
packages (accounts, sites, storage, and Hack's own events, teams, members
and entries); Simple Hack's app code now lives in its own `app/hack/*`
packages rather than mixed in with Simple Host's; CI runs the integration
suite against a real Postgres service container instead of a stub; the
custom regex-based static bundler was replaced with esbuild behind a
platform adapter; new migrations are named `NNNN-what.sql` after the frozen
legacy list, checked by a real upgrade test that builds a database at the
previous release and compares it against a fresh install; and the old
"classic" Simple Hack single-page app (`static/hack-app.html`,
`?ui=classic`) was deleted, with the modular app now serving every signed-in
route including `/signin`.

Annotated tag `hack-v0.8.15` points at `2593aa4d`, the commit built and
published. The workflow built both images from the tag and packaged the Helm
chart, publishing the
[release](https://github.com/vineetu/simple-host/releases/tag/hack-v0.8.15).

The ZIP, tar.gz and the `simple-hack-0.1.0.tgz` Helm chart all match
SHA256SUMS. The published `simple-hack:0.8.15` image's binary reports
`v0.8.15 (commit 2593aa4)`, with migration `0002-password-sign-in.sql` last
in its build.

As for v0.8.14, no DigitalOcean token was configured on the release host, so
the release was checked in local Docker containers: a fresh install of
`hack-0.8.14` with Postgres (79 migrations), an account created through the
admin API, then an upgrade with the 0.8.15 image's `migrate`
(`0001-hack-judging-event-fk.sql` and `0002-password-sign-in.sql`, 2
migrations, applied cleanly), restart on 0.8.15, and the account still
present. With `PASSWORD_SIGNIN=on`, a password account was created through
`POST /v1/admin/password-users`, which returned a temporary password; signing
in with it at `POST /v1/auth/password` first answered
`password_change_required` as designed, and succeeded once a `new_password`
was supplied, returning a session API key. The Helm chart's `values.yaml`
carries `signIn.password` (`enabled`, `selfSignup`) and `teamSites.flatHosts`.
A full event flow was not exercised in the container; production
simple-hack.app has run this code since the same day.
