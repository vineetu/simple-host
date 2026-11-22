# REST API without the connector

Use this only when no Simple Host connector tools are available (a terminal agent, CI, a script).
Everything else in these skills still applies: ask before the same steps, give the person the
returned URLs, never put a key in a page.

- Base URL: `https://simple-host.app/v1`
- Every authenticated call: `X-API-Key: <key>`
- Every call: `X-Skill-Version: 0.27.119` (without it the server keeps asking you to update)
- Keep the key out of chat, logs, pages and commits. Store it in `~/.website-deploy/config.json`
  (resolve `~` yourself) or the environment's secret store.

## Get an API key

The person signs in with their email; the code goes to their inbox (15-minute code).

```
curl -sS https://simple-host.app/v1/auth -H 'Content-Type: application/json' -d '{"email":"me@example.com"}'
curl -sS https://simple-host.app/v1/auth/verify -H 'Content-Type: application/json' -d '{"email":"me@example.com","code":"123456"}'
```

The second call returns `api_key` (and `handle`). Have the person run it themselves, or create a
key in the dashboard's Keys panel; never ask them to paste the code or key into chat. A new
email creates the account.

## Publish

Inline JSON, for sites you wrote (`index.html` required; binary files base64 in `files_base64`):

```
curl -sS -X POST https://simple-host.app/v1/sites/my-site/files -H "X-API-Key: $KEY" -H 'X-Skill-Version: 0.27.119' -H 'Content-Type: application/json' -d '{"files":{"index.html":"<!doctype html>..."}}'
```

| Job | Call |
|---|---|
| New site, inline JSON | `POST /sites/{site}/files` `{"files":{...},"files_base64":{...}}`; add `"access":"specific","viewers":[...]` for a private site |
| Replace an existing site | `PUT /sites/{site}/files` (same body) |
| Change some files, keep the rest | `PUT /sites/{site}/files?keep=1` (`remove_files` drops named paths) |
| Create or update in one call (CI) | `PUT /sites/{site}/files?create=1` |
| Store a version without making it live | add `?publish=false`; the answer has `preview_url` |
| Archive (`.tar.gz` or `.zip`, files at the root) | `POST /sites/{site}` new, `PUT /sites/{site}` existing, body `--data-binary @site.tar.gz` |

The API rejects archives over 100 MB. Every answer has `site_url`: give that to the person.

## Sites and versions

| Job | Call |
|---|---|
| Who am I, handle, usage | `GET /me` |
| List sites | `GET /sites` |
| Delete a site (restorable for 7 days) | `DELETE /sites/{site}`; undo with `POST /sites/{site}/restore` |
| List versions | `GET /sites/{site}/versions` |
| Roll back or make a stored version live | `PUT /sites/{site}/active-version` `{"version_number": 3}` |
| Files of a version | `GET /sites/{site}/versions/{n}/files`, `.../files/{path}` |
| List on my profile | `PUT /sites/{site}/visibility` `{"visibility":"public"}` or `"unlisted"` |
| Named viewers | `POST /sites/{site}/viewers` `{"emails":[...]}`, `DELETE /sites/{site}/viewers/{email}` |
| Own domain or short address | `POST /sites/{site}/domain` `{"domain":"rsvp.example.com"}`, `GET` to check, `DELETE` to remove |
| Teams | `POST /teams` `{"name"}`, `GET /teams`, `GET`/`POST /teams/{team}/members` (`{"email"}` invites), `DELETE /teams/{team}/members/{handle}`, `POST /me/team-invitations/{id}` `{"answer":"accept"}`, `DELETE /teams/{team}` |
| Site editors | `GET`/`POST /sites/{site}/editors` `{"person":"<handle>"}`, `DELETE /sites/{site}/editors/{handle}` |

Add `?owner=<team or account>` to any `/sites` call to act on a team's sites, or on the one site you edit.

## Storage

Paths start `/sites/{site}/storage`. Rules and the full table: `references/storage.md`.

| Job | Call |
|---|---|
| Create a resource with rules | `PUT /resources/{name}` `{"kind":"sqlite","rules":{...}}` (`kv`, `files` the same) |
| List resources, usage | `GET /resources`, `GET /usage` |
| KV put / get / delete | `PUT /kv/{name}/keys/{key}` `{"value": <JSON>}`, `GET`, `DELETE` the same path |
| SQL schema / query / execute | `POST /sqlite/{name}/schema`, `/query`, `/execute` with `{"sql":"...","params":[...]}` |
| Files put / get / list / delete | `PUT /files/{name}/objects/{path}` raw bytes with `Content-Type`; `GET`, `DELETE` the same path; `GET /files/{name}/objects` |

```
curl -sS -X PUT https://simple-host.app/v1/sites/my-site/storage/resources/menu -H "X-API-Key: $KEY" -H 'X-Skill-Version: 0.27.119' -H 'Content-Type: application/json' -d '{"kind":"kv","rules":{"version":1,"policies":[{"command":"read","using":"true"},{"command":"all","using":"auth.role == \"owner\""}]}}'
```

A request that names a page (`Origin`) must come from the site's own address; scripts send none.
On `429` or `503`, wait for `Retry-After`. Create calls accept an `Idempotency-Key` header for
safe retries.
