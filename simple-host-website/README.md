# Website Deploy Plugin

Deploy static websites to ideaflow.page from your AI coding IDE. Works with Claude Code, Codex CLI, and Cursor.

## Install

```bash
git clone git@github.com:vineetu/simple-host.git
cd simple-host/simple-host-website
bash setup.sh
```

Requires **Node.js** (for the MCP server).

The setup script detects which IDEs you have installed and configures each one automatically. Restart your IDE after running setup.

## Usage

After setup, just tell your AI assistant:

- "Deploy my website"
- "Register me for Website Deploy with my-email@example.com"
- "Check the status of my-site"
- "List my deployed sites"

The AI will guide you through registration, validate your site, and deploy it.

## What is Website Deploy?

Website Deploy serves **static files only** — HTML, CSS, JavaScript, images, and fonts. Your site will be live at `https://{sitename}.ideaflow.page`.

### What works

- Plain HTML/CSS/JS sites (no build step needed)
- Built output from React, Vue, Svelte, Angular (`npm run build` → deploy `dist/` or `build/`)
- Static site generator output (Hugo, Jekyll, Astro, Eleventy)

### What doesn't work

- Server-side rendering (Next.js SSR, Nuxt server routes)
- Backends (Node.js, Python, Go servers)
- Databases, API routes, PHP

### Limits

- Max compressed upload: 100 MB
- Max uncompressed: 500 MB
- Site names: lowercase letters, numbers, and hyphens only

## Template

The `template/` directory contains a ready-to-deploy example static site. Try it out:

> "Deploy the template folder as my-first-site"

## What gets installed

| IDE | MCP config | Skill |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | `~/.claude/skills/website-deploy/SKILL.md` |
| Codex CLI | `~/.codex/config.toml` | `~/.agents/skills/website-deploy/SKILL.md` |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/skills/website-deploy/SKILL.md` |

## Uninstall

Remove the MCP server entry from your IDE's config file and delete the skill file. The setup script does not modify anything outside the paths listed above.
