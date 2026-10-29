# Website Deploy Toolkit download site

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

Open Claude, then **Settings**, then **Connectors**, then **Browse connectors**. Search for **Simple Host**, choose **Connect**, sign in, then choose **Allow**. It works on web, desktop, and phone. Claude also lists a Simple Host plugin (**Customize** → **Plugins** → **Discover**) that adds the connector and skills in one step.

`site/` is the recovered source of the existing owner site at
https://website-deploy-toolkit.vineetu.simple-host.app/ (current plugin 1.0.0, updated 2026-10-29: a kit for creating a **new** ChatGPT plugin, package `simple-host`, since the portal will not let the old draft `app-6ab570c7e41c81918c88f0026f2e1895` gain an MCP server through a package upload). Its demo video, icons and historical 0.9.7
archive were recovered byte-for-byte before editing. Do not replace the
historical ZIP or the demo and listing artifacts when updating current skills.
`downloads/simple-host-openai-plugin-new.zip` is the new plugin's package (name `simple-host`,
not the old draft's bound `app-...` id); `downloads/simple-host-openai-plugin.zip` is kept as
the old draft's last-built 1.0.2 package in case that route is ever revisited.

The maintained coding-agent skill source is `simple-host-website/skills/`. The connector-only
copy in `openai-plugin/skills/` deliberately omits API keys and command-line
fallbacks and is the source for all three public OpenAI ZIPs. `FALLBACK=1 bash scripts/build-openai-plugin.sh` builds the three
current ZIPs in `dist/`; copy the plugin ZIP into `site/downloads/simple-host-openai-plugin-new.zip`
(the new plugin's package) and the skills ZIP into `site/downloads/simple-host-openai-skills.zip`,
keeping only 0.9.17 and 0.9.7 of the versioned toolkit ZIPs. `openai-plugin/plugin.json` owns
the package name and version: `simple-host` 1.0.0 for the new plugin (the old draft's last
build, package `app-6ab570c7e41c81918c88f0026f2e1895` version 1.0.2, stays only in
`site/downloads/simple-host-openai-plugin.zip`).
The toolkit ZIP has the `website-deploy-toolkit` identity and no MCP or app
binding. The full `simple-host` ZIP retains `mcp.json`; the portal skills ZIP
contains only skill directories. `site/downloads/chatgpt-app-submission.json` is a copy of
`openai-plugin/chatgpt-app-submission.json`; refresh both together.

Your person address opens your showcase or a site you choose. Ask the connector to make a site your home page, save a bio, pin projects or set their order. Custom homes can use the live showcase feed. Available on hosted Simple Host and small-box installs.

Publish: the site belongs to the owner's account (handle `vineetu`, key in `~/.sh-vineetu-key`). A PUT replaces every file, so always upload the whole `site/` directory:
`cd host-toolkit/site && tar czf /tmp/kit.tgz index.html downloads shots && curl -sS -X PUT https://simple-host.app/v1/sites/website-deploy-toolkit -H "X-API-Key: $(cat ~/.sh-vineetu-key)" -H 'Content-Type: application/gzip' --data-binary @/tmp/kit.tgz`
