# Website Deploy Toolkit download site

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

[Open Claude connectors](https://claude.ai/new?modal=add-custom-connector#customize/connectors/yours). Name it **Simple Host**, paste `https://simple-host.app/mcp`, and choose **Add**. In the sign-in window, sign in with Google or an email code, then choose **Allow**. The connector works in the Claude web, desktop, and phone apps.

`site/` is the recovered source of the existing owner site at
https://website-deploy-toolkit.vineetu.simple-host.app/ (current package 0.9.17, updated 2026-10-09). Its page, demo video, screenshots, icons and historical 0.9.7
archive were recovered byte-for-byte before editing. Do not replace the
historical ZIP or the demo and listing artifacts when updating current skills.

The maintained coding-agent skill source is `simple-host-website/skills/`. The connector-only
copy in `openai-plugin/skills/` deliberately omits API keys and command-line
fallbacks and is the source for all three public OpenAI ZIPs. `FALLBACK=1 bash scripts/build-openai-plugin.sh` builds the three
current ZIPs in `dist/`; copy them into `site/downloads/` and retain the prior
versioned toolkit ZIP. `openai-plugin/plugin.json` owns the package version.
The toolkit ZIP has the `website-deploy-toolkit` identity and no MCP or app
binding. The full `simple-host` ZIP retains `mcp.json`; the portal skills ZIP
contains only skill directories. The historical portal JSON/checklist remain
labeled as snapshots, not current submissions.

Your person address opens your showcase or a site you choose. Ask the connector to make a site your home page, save a bio, pin projects or set their order. Custom homes can use the live showcase feed. Available on hosted Simple Host and small-box installs.
