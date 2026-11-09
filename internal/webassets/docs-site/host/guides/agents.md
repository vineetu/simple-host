# Agents and MCP

## Connect an app
[What this is](/features.html#connector)


1. Open [Connect your AI app](/install.html) and follow the steps for your app.
2. For a custom MCP connection, enter `https://simple-host.app/mcp`. Complete sign-in in the browser and choose **Allow**.
3. Return to chat and ask for the job. Keep sign-in codes and keys in the browser, outside the conversation.
4. If the app cached an older tool list, refresh the connector in that app before trying again.


## Install the skills
[What this is](/features.html#skill-website-deploy)


1. Open [Get started](/install.html) and expand the installation instructions for your coding agent.
2. Install the skill folders in the directory that agent reads, then restart the agent or invoke the skill again.
3. Use Website Deploy before publishing. Use Website Deploy Builder to plan saved data and access. Follow Connect Domain when connecting an address.

   <!--sh:hosted-->
   For coding agents, run `npx skills add vineetu/simple-host`.
   <!--/sh:hosted-->
   <!--sh:self-->Download this install's [skills.zip](/skills.zip) and use this server's connector address.<!--/sh:self-->


## Give an agent the references
[What this is](/features.html#agents-discovery)


1. Point it at [/llms.txt](/llms.txt) for this product's operating instructions.
2. For exact request fields, read [the OpenAPI contract](/openapi.json). For sign-in, read [/auth.md](/auth.md).
3. Ask the connector for a page recipe before wiring storage; use the recipe's setup calls and browser code.
4. Read error `code` values before retrying. Follow a skill update notice by reinstalling the skills from Get started.



## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `sign_in_required` (401) | Connection needs sign-in | Reconnect in the browser |
| `invalid_token` (401) | Connector token expired or revoked | Reconnect in the browser |
| `method_not_allowed` (405) | Wrong HTTP method | Read the API reference |

## Next
- [Sites and deploy](/docs/guides/publish/)
- [Sign-in and access](/docs/guides/access/)
