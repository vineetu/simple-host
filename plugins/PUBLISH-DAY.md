# Switch day: plugin, skills and listings

Everything here was prepared on branch `next-publish` (based on `next`). Nothing has been
published or pushed. Version everywhere: Claude plugin and skills **0.28.0**, ChatGPT plugin
**1.1.0**. Do these in order, and only after the new service is live: the portals scan the live
server, and skills that name the new tools must not reach people while the old service answers.

## 0. Before you start

- The new service is switched in (`next/RUNBOOK.md`, "Switch") and `POST https://simple-host.app/mcp`
  `tools/list` returns 27 tools including `publish_site`.
- On this box: `git -C /home/ubuntu/workspace/wt-next-publish status` is clean on `next-publish`.

## 1. Put the commits on the branch the repository publishes from

The marketplace entry (`.claude-plugin/marketplace.json`) is read from the default branch of
`vineetu/simple-host`, so these paths must be on `main`:

- `.claude-plugin/marketplace.json`
- `plugins/simple-host/` (manifest, `.mcp.json`)
- `plugins/openai/` (the ChatGPT kit)
- `plugins/PUBLISH-DAY.md`
- `host/webassets/simple-host-website/` (the three skills, the skills `plugin.json`)
- `next/mcp/submission_tools_test.go` (keeps the kit and the tool list in step)

```
cd /home/ubuntu/workspace/simple-host        # never edit its working tree by hand
git merge --ff-only next-publish             # on `next`, after the switch commit
git checkout main && git merge next          # however the switch lands `next` on main
git push origin main
```

If the switch commit moves or deletes `host/`, change the skills path in
`scripts/publish-claude-plugin-repo.sh` and `scripts/build-openai-plugin.sh` (`DEFAULT_SKILLS`)
first. Both read `host/webassets/simple-host-website/skills`.

## 2. Claude plugin repository (what Claude's plugin directory lists)

```
cd /home/ubuntu/workspace/simple-host
DRY_RUN=1 bash scripts/publish-claude-plugin-repo.sh   # shows what changes in vineetu/simple-host-plugin
bash scripts/publish-claude-plugin-repo.sh             # commits, tags v0.28.0, pushes main and the tag
```

It copies the skills, `.mcp.json`, `plugin.json` and the icon, and needs the `claude` CLI and push
rights to `vineetu/simple-host-plugin`. The connector address `https://simple-host.app/mcp` is
unchanged.

## 3. ChatGPT package

```
bash scripts/build-openai-plugin.sh host
```

Outputs `dist/simple-host-openai-plugin.zip` and `dist/simple-host-openai-skills.zip` (the
build checks the kit against the cases first). `cd next && go test ./mcp -run
TestSubmissionToolsMatchListing` checks the kit's tool list against the code.

## 4. By hand: Anthropic's listing portal

Open the portal where the Simple Host connector or plugin was submitted. Keep the server address.
Replace the listed tool names with the 26 the model sees (the 27th, `passcode_screen`, is hidden):

`who_am_i`, `update_account`, `download_my_data`, `delete_account`, `create_team`,
`invite_to_team`, `accept_invite`, `remove_from_team`, `delete_team`, `list_sites`, `get_site`,
`publish_site`, `create_upload_link`, `read_site_file`, `change_site`, `delete_site`,
`set_site_access`, `set_site_address`, `site_stats`, `set_notifications`, `data`, `run_sql`,
`set_data_rules`, `get_recipe`, `admin_overview`, `set_hold`.

Set the plugin version to 0.28.0. Submit the change.

## 5. By hand: OpenAI's portal

1. platform.openai.com, Plugins, the `simple-host` plugin, a new draft (version 1.1.0 if no
   1.1.0 exists yet; otherwise raise it in `plugins/openai/plugin.json` and rebuild).
2. Upload `dist/simple-host-openai-plugin.zip`; the Skills tab takes
   `dist/simple-host-openai-skills.zip`.
3. MCP tab: **Scan Tools**. Expect 27. Verify the domain if asked.
4. Enter the reviewer password in the private field. Do every `TODO-OWNER` line in
   `plugins/openai/SUBMISSION.md` (run the eight cases in ChatGPT, new demo video, reviewer
   sign-in check), then submit.

## 6. Check that each client picked it up

- **Claude Code:** `/plugin marketplace update simple-host`, then `/plugin` and update
  `simple-host`. It should show 0.28.0; ask "put a one-page site online" and see `publish_site`.
- **claude.ai and the Claude apps:** Settings, Connectors, Simple Host: refresh (or disconnect and
  connect again). Ask "which Simple Host tools do you have". It should name `publish_site`, not
  `update_site`.
- **ChatGPT:** Settings, Apps, Simple Host: **Refresh**. A new chat should list the new names.

Old tool names still work for a while through the alias table, so a client that has not refreshed
keeps working. Remove nothing from the alias table until the checks above pass.
