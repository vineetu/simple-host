# Simple Hack for Claude

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Hack**, paste `https://simple-hack.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Hack from the **+** menu, or just ask.

[Open Claude connectors](https://claude.ai/new?modal=add-custom-connector#customize/connectors/yours). Name it **Simple Hack**, paste `https://simple-hack.app/mcp`, and choose **Add**. In the sign-in window, sign in with Google or an email code, then choose **Allow**. The connector works in the Claude web, desktop, and phone apps.

This local 0.2.12 package contains five hosted Simple Hack skills: `run-hackathon`, `join-hackathon`, `judge-hackathon`, `website-deploy-builder`, and `website-deploy`. Its `.mcp.json` points to `https://simple-hack.app/mcp`. Sign in through the app's trusted connection flow; the same connection covers a person's event roles. A participant selects their current team before publishing its website.

The skills do not ask for sign-in codes, API keys, team keys, passwords or passcodes in chat. They cover the hosted organiser, participant, judge, team website and custom event website workflows, including scoped KV, SQLite and raw-file resources. The package contains no credentials.

This ZIP is a local download; no Claude marketplace update or directory approval is claimed. Older marketplace copies are outside this reviewed package. Support: support@simple-host.app.
