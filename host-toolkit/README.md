# Website Deploy Toolkit download site

`site/` is the recovered source of the existing owner site at
https://website-deploy-toolkit.vineetu.simple-host.app/ (live version 23, updated 2026-10-05). Its page, demo video, screenshots, icons and historical 0.9.7
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
