# Agents and MCP

## Connect an app
[What this is](/features.html#connector)


1. Open [Connect your AI app](/get-started) and follow the steps for your app.
2. For a custom MCP connection, enter `https://simple-hack.app/mcp`. Complete sign-in in the browser and choose **Allow**.
3. Return to chat and ask for the job. Keep sign-in codes and keys in the browser, outside the conversation.
4. If the app cached an older tool list, refresh the connector in that app before trying again.


## Install the skills
[What this is](/features.html#skill-website-deploy)


1. Open [Get started](/get-started) and expand the installation instructions for your coding agent.
2. Install the skill folders in the directory that agent reads, then restart the agent or invoke the skill again.
3. Use Website Deploy before publishing. Use Website Deploy Builder to plan saved data and access. 

   <!--sh:hosted-->
   Use the five reviewed Simple Hack skills linked from Get started.
   <!--/sh:hosted-->
   <!--sh:self-->Download this install's [skills.zip](/skills.zip) and use this server's connector address.<!--/sh:self-->


## Give an agent the references
[What this is](/features.html#agents-discovery)


1. Point it at [/llms.txt](/llms.txt) for this product's operating instructions.
2. For exact request fields, read [the OpenAPI contract](/openapi.json). For sign-in, read [/auth.md](/auth.md).
3. Ask the connector for a page recipe before wiring storage; use the recipe's setup calls and browser code.
4. Read error `code` values before retrying. Follow a skill update notice by reinstalling the skills from Get started.



## Open an event screen in chat
[What this is](/features.html#dashboard-mcp-apps-hack)


1. Select the event with the Simple Hack connector.
2. Ask to open the organiser or judge screen in a chat app supporting MCP Apps.
3. Choose the job on that screen and confirm changes there. Use the website link when the app offers a link instead of an embedded screen.


## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `sign_in_required` (401) | Connection needs sign-in | Reconnect in the browser |
| `invalid_token` (401) | Connector token expired or revoked | Reconnect in the browser |
| `method_not_allowed` (405) | Wrong HTTP method | Read the API reference |

## Next
- [Sites and deploy](/docs/guides/publish/)
- [Sign-in and access](/docs/guides/access/)
