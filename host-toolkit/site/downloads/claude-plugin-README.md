# Simple Host for Claude

Describe a website to Claude and it goes online at its own address, with forms that save, private lists only you can read, visitor counts, and your own domain when you want one.

This plugin bundles the Simple Host skills and the Simple Host connector (`https://simple-host.app/mcp`). The first time Claude needs it, a Simple Host sign-in window opens: sign in with Google or an emailed code, then Allow. After that every chat is signed in.

## What you can ask for

- Publish a site from a description, then change it by asking. Earlier versions are kept: preview one or put it back.
- Save what visitors send (RSVPs, orders, sign-ups, votes, survey answers), keep it in a private list only you can read, choose who may save, and block someone.
- Undo: saved data keeps 30 days of history, and a deleted site can be restored for 7 days.
- See how many people visited, the most viewed pages and where visitors came from.
- Give a site a free `name.simple-host.app` address or connect your own domain.
- Download a copy of any site.

## What it sends and where

The connector talks only to `https://simple-host.app/mcp`, and only when Claude uses a Simple Host tool: it sends the site names, files, data and domain names that tool needs, never your chat history. Where the connector is not available (for example a coding agent without it), the skills tell Claude to use the Simple Host API at `https://simple-host.app` with `curl` and an API key you create by email sign-in. When you connect your own domain and ask Claude to add the DNS record for you, the `connect-domain` skill can call your registrar's API (Porkbun, GoDaddy or Vercel) with credentials you provide; otherwise it gives you the one record to add yourself. Pages you publish are public to anyone with the link. See the [privacy policy](https://simple-host.app/privacy.html) for what Simple Host keeps and for how long, and [support](https://simple-host.app/support) or support@simple-host.app for help.

## Install in Claude Code

```
/plugin marketplace add vineetu/simple-host-plugin
/plugin install simple-host@simple-host
```

## GitHub Copilot

This repository is also an [Agent Plugins](https://agent-plugins.org) package (`plugin.json`, `mcp.json`, `skills/` at the root), so Copilot can install it from the Awesome Copilot marketplace once listed. To connect Copilot in VS Code by hand: run **MCP: Add Server** from the Command Palette, choose **HTTP**, paste `https://simple-host.app/mcp`, name it `simple-host`, and sign in when asked.

## Source

Generated from [github.com/vineetu/simple-host](https://github.com/vineetu/simple-host) (`plugins/simple-host`). Please open issues there.

MIT licensed. Website: https://simple-host.app
