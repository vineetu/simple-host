# compat: the old ways, for 30 days (throwaway)

Callers of Simple Host from before the rebuild keep working until the sunset date:

- **Old addresses** (`routes.go`): the routes scripts, installed skills and handed-out links
  used, each answered through the current routes with the old request and answer shapes
  (`site_url`, `active_version`, the old lists). Current addresses are answered the old way
  only when the caller is old: an old query (`?keep=1`, `?create=1`, ...) or the old skill's
  `X-Skill-Version` header.
- **Old tool names** (`tools.go`): AI apps that keep an old tool list. mcp asks `compat.Tool`
  for any name it does not have.
  Apps that hold the old list also validate answers against the old output schemas, so each
  old name's structuredContent has the old fields (`shapes.go`; the old list is
  `testdata/old-tools.json`, checked by `TestOldToolAnswersMatchTheOldOutputSchemas`), and
  refusals carry no structuredContent.
- **Split tools** (`tools.go`, `data`, `passcodeScreen`): `data` (method + path) and
  `passcode_screen` (action), public for a day before the split, map to the single-purpose tools
  (`list_rows` ... `delete_file`, `passcode_screen_open|show|set|generate`). `set_site_access`
  and `change_site` (`setSiteAccess`, `changeSite`), public for a day after that, map to the
  access tools (`open_site_to_anyone`, `require_passcode`, `allow_only_named_people`,
  `add_site_viewers`, `remove_site_viewers`, `sign_out_site_visitors`) and the site tools
  (`take_site_offline`, `put_site_online`, `set_site_listed`, `rollback_site`, `restore_site`).
  `rollback_site`, `restore_site` and `sign_out_site_visitors` are current names again, with the
  old arguments. A passcode an old call sends is not used: `require_passcode` opens the screen.
- **The old skill** (`X-Skill-Version` on any request): `/v1/skills/...` serves the new skill.
- **Old photo picker**: `create_upload_link {site}` (no namespace, kind or archive) gets the
  current picker (namespace `photos`) plus the old `photos_url` and a note on where photos land.
  `create_upload_link` with `archive: true` or `kind: "archive"` gets `create_site_upload_link`.
- **Old push**: `SH.push` in the page helper (`web/static/auth.js`) and the old
  `/push/subscribe|unsubscribe` routes answer "not available"; remove both with the package.
- **Notice** (`notice.go`, `SH_COMPAT_NOTICE`, see `next/RUNBOOK.md`): every HTTP answer through
  compat has `Deprecation: true`, `Sunset: <date>` and `Link: </llms.txt>; rel="successor-version"`.
  With `on`, the old skill also gets `X-Skill-Notice`/`_notice`, and an old tool list one
  notice per MCP session.
- **Count**: table `compat_calls` (day, who, name, calls). `GET /v1/admin/compat`
  (operator) lists who still uses what, to email them before the cut-off.

The sunset is the constant `until` in `compat.go`, moved by `SH_COMPAT_UNTIL=YYYY-MM-DD`.
After it compat steps aside: old addresses get the service's `changed` refusal and old
tool names the "tools changed" error. Two weeks after the date in the repository,
`TestDeleteNextCompatTwoWeeksAfterSunset` fails with "delete next/compat".

## Deleting it

1. `rm -r next/compat`
2. `cmd/simple-host-next/main.go`: `Handler: compat.Wrap(a, admin.Counted(a))` back to
   `Handler: admin.Counted(a)`, and drop the import.
3. `mcp/mcp.go`: remove the block after "An old tool list" in `tools/call`, the `!compat.OldArgs(...)` condition above it, and the import.
4. `web/static/auth.js`: remove the `SH.push` stand-in.
5. `DROP TABLE compat_calls;`
