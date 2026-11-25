# REST API without the connector

API key: use Simple Host without a connector. Use this only when no Simple Host connector tools
are available (a terminal agent, CI).
Everything else in this skill still applies: ask before the same steps, give the person the
returned URLs, never put a key in a page.

- Base URL: `https://simple-host.app/v1`
- Every call: `X-API-Key: <key>`. The key acts as the person who made it.
- The person makes a named key on the website at https://simple-host.app/dashboard/account#keys
  (shown once) and puts it in the environment (`SIMPLE_HOST_KEY`). Never ask them to paste it into
  chat, and never write it to a page, a log or a commit. Tools cannot make keys. Signing in by
  emailed code from an agent (`POST /v1/auth`) is retired: do not try it.
- A site of a team: add `?owner=<team handle>`.

## Publish

```
curl -sS -X PUT "https://simple-host.app/v1/sites/my-site?keep_other_files=true" \
  -H "X-API-Key: $SIMPLE_HOST_KEY" -H 'Content-Type: application/json' \
  -d '{"files":{"index.html":"<!doctype html>..."},"files_base64":{"logo.png":"iVBOR..."}}'
```

The answer has the live `url`: give it exactly as returned. Leave out `keep_other_files` to
replace the whole site. A new site name is made by the first publish.

## Other calls

| Job | Call |
|---|---|
| Me, teams, keys, use | `GET /me` |
| My sites; search; the bin | `GET /sites?q=&bin=true` |
| One site (address, versions, access, domain, data) | `GET /sites/{site}` |
| Read a file | `GET /sites/{site}/files/{path}` |
| Offline, listed, live version, restore | `PATCH /sites/{site}` `{"offline":true}` `{"listed":true}` `{"live_version":3}` `{"restore":true}` |
| Access | `PATCH /sites/{site}` `{"access":"open"}`, `{"access":"viewers","viewers":["a@x.com"]}`, `{"access":"passcode","passcode":"..."}` (the person types it, not you) |
| Domain, short name | `PATCH /sites/{site}` `{"domain":"shop.example.com"}` `{"short_name":"bakery"}` |
| Notifications | `PATCH /sites/{site}` `{"notifications":[...]}` |
| Move to the bin | `DELETE /sites/{site}` |
| Big archive, or photos | `POST /sites/{site}/upload-links` `{"kind":"archive"}` or `{"kind":"files","namespace":"photos","public":true}` (`public` makes a new namespace readable by anyone who can open the site; only the owner writes), then `PUT /uploads/{token}` |
| Visit numbers | `GET /sites/{site}/stats?days=30` |
| Owner SQL | `POST /sites/{site}/storage/sqlite/{ns}/query`, `/execute`, `/schema` with `{"sql":"...","params":[]}` |
| Rules | `PUT /sites/{site}/storage/{ns}/rules` `{"rules":{...},"spam_check":false}` |
| Rows | `GET`/`POST /sites/{site}/storage/sqlite/{ns}/tables/{table}/rows`, `GET`/`PATCH`/`DELETE .../rows/{id}` |
| Values | `GET`/`PUT`/`DELETE /sites/{site}/storage/kv/{ns}/keys/{key}` |
| Files | `GET`/`PUT`/`DELETE /sites/{site}/storage/files/{ns}/objects/{path}` |
| Recipes | `GET /recipes/{topic}?site=<site>` |
| Teams | `POST /teams`, `POST /teams/{team}/invites`, `POST /invites/{id}/accept`, `DELETE /teams/{team}/members/{person}`, `DELETE /teams/{team}` |
| Handle, name, home site | `PATCH /owners/me` (or a team handle) |
| Download everything | `POST /me/export` (the link comes by email) |

Every refusal is JSON `{"code": "...", "fix": "..."}`: read `fix`. Full reference:
https://simple-host.app/docs.
