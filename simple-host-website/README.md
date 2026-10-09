# Website Deploy skills

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

Every site also gets a small JSON backend (shared state, lists, and declared kinds: Page info, Submissions, Personal, Shared board) that its own pages can call. Reading it is public. Visitors sign in with Google (more providers later) or an emailed code on the site's own address via the hosted `https://simple-host.app/auth.js` helper; every save from a page needs a signed-in visitor, and a collection can be made private so only you can read it. Agents write with the site owner's API key. The `website-deploy` skill covers the pattern and `connect-domain` covers the optional domain.

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
