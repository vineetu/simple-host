# Existing local credentials and account setup

Use the signed-in Simple Host connector. If it is unavailable, ask the person to connect or reconnect it through the app's trusted browser window. Never ask for or process an emailed sign-in code, API key, password or site passcode in chat.

For a coding environment without the connector, an already configured owner credential may be read from the local secret store or `~/.website-deploy/config.json` and sent as `X-API-Key` over HTTPS. Do not print it, pass it through a hosted page, or commit it. If no local credential exists, pause the agent's authenticated API calls while the person completes account setup and key configuration through the trusted Simple Host browser/dashboard. Resume only when the local client is configured.

[Open ChatGPT Plugins](https://chatgpt.com/plugins). Click **Add**, then choose **Add custom MCP server**. Name it **Simple Host**, paste `https://simple-host.app/mcp`, and save. In the sign-in window, sign in with Google or an email code, then choose **Allow**. In a chat, pick Simple Host from the **+** menu, or just ask.

Open Claude, then **Settings**, then **Connectors**, then **Browse connectors**. Search for **Simple Host**, choose **Connect**, sign in, then choose **Allow**. It works on web, desktop, and phone. Claude also lists a Simple Host plugin (**Customize** → **Plugins** → **Discover**) that adds the connector and skills in one step.

In Meta Muse, open **Settings**, then **Connectors**, then **Add custom connector**. Paste `https://simple-host.app/mcp` as the MCP URL and `https://simple-host.app/.well-known/muse-client.json` as the Client ID (not an email), sign in, then choose **Allow**.
