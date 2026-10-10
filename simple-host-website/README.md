# Website Deploy skills

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

[Open Claude connectors](https://claude.ai/new?modal=add-custom-connector#customize/connectors/yours). Name it **Simple Host**, paste `https://simple-host.app/mcp`, and choose **Add**. In the sign-in window, sign in with Google or an email code, then choose **Allow**. The connector works in the Claude web, desktop, and phone apps.

Deploy static websites to simple-host.app from your AI app or coding agent.

## Install

- Coding agents (Claude Code, Codex, Cursor and others): `npx skills add vineetu/simple-host`
- Chat apps (ChatGPT, Claude, Grok): add the connector `https://simple-host.app/mcp`
- Claude Code plugin: the `simple-host` plugin (skills plus the connector)

The steps for each app are on https://simple-host.app/install.html. Restart your agent after installing.

The local stdio MCP server in `mcp-server/` (5 tools: register, verify, deploy, status, list) and
`setup.sh` are legacy; the connector above replaces them.

## Usage

After setup, just tell your AI assistant:

- "Deploy my website"
- "Register me for Website Deploy with my-email@example.com"
- "Check the status of my-site"
- "List my deployed sites"

The AI will guide you through registration, validate your site, and deploy it.

## What is Website Deploy?

Website Deploy serves **static files only** — HTML, CSS, JavaScript, images, and fonts. Your site will be live at its own address, `https://{sitename}.{handle}.simple-host.app/`. Every site gets built-in traffic analytics, computed server-side from access logs with nothing to add to your pages. Views and unique visitors are split by who was asking — `person`, `bot` and `infra` (uptime probes) — so `person` is the number that means real audience. Want a nicer address? Take a free `<name>.simple-host.app` or connect your own domain — subdomain or apex (e.g. `recipes.brand.com` / `brand.com`) — see the `connect-domain` skill.

Every site also gets a small backend: owner-declared KV, SQLite and file resources, each with its own read/write policy, that its own pages can call. Visitors sign in with Google (more providers later) or an emailed code on the site's own address via the hosted `https://simple-host.app/auth.js` helper — this visitor sign-in is never the same as the owner's account sign-in, and a page never calls the account API or holds an API key. Every save from a page needs a signed-in visitor unless the resource is explicitly open, and a resource can be set to `read:"own"` or `"owner"` so only the right person reads it. Agents write with the site owner's API key. Existing sites may still use the deprecated shared state, lists and declared kinds (Page info, Submissions, Personal, Shared board) — existing sites only; the connector no longer offers tools for them. The `website-deploy` skill covers the pattern and `connect-domain` covers the optional domain.

### What works

- Plain HTML/CSS/JS sites (no build step needed)
- Built output from React, Vue, Svelte, Angular (`npm run build` → deploy `dist/` or `build/`)
- Static site generator output (Hugo, Jekyll, Astro, Eleventy)

### What doesn't work

- Server-side rendering (Next.js SSR, Nuxt server routes)
- Backends (Node.js, Python, Go servers)
- Databases, API routes, PHP

### Limits

- Max compressed upload: 100 MB by default (a server may allow more)
- Max uncompressed: 500 MB
- Site names: lowercase letters, numbers, and hyphens only

## Example site

The `template/` directory in this plugin is a ready-to-deploy example static site. Try it out:

> "Deploy the template folder as my-first-site"

## What gets installed

| IDE | MCP config | Skill |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | `~/.claude/skills/` |
| ChatGPT desktop, Codex CLI | `~/.codex/config.toml` | `~/.agents/skills/` |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/skills/` |

Every bundled skill is copied as a whole directory — `website-deploy` ships a
`references/` folder alongside its `SKILL.md`.

## Uninstall

Remove the MCP server entry from your IDE's config file and delete the skill file. The setup script does not modify anything outside the paths listed above.

Your person address opens your showcase or a site you choose. Ask the connector to make a site your home page, save a bio, pin projects or set their order. Custom homes can use the live showcase feed. Available on hosted Simple Host and small-box installs.

Build sites in your own AI app or coding agent; Simple Host publishes and manages them.
