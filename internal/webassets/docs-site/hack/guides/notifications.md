# Notifications

## Get a webhook when a visitor writes
[What this is](/features.html#notify-webhook)


1. Choose your site's events and a public HTTPS receiver.
2. Create the subscription.

      **Dashboard**

   Open Notifications on your team's Website or the organiser's custom website screen. Choose the events, optional storage resource, and receiver URL.
      **API**
Send `POST /v1/sites/{sitename}/notifications` with `{"url":"https://receiver.example/hook","events":["record.added"]}`. For an organiser's website use `/v1/hack/events/{slug}/website/notifications`.   
3. Save the signing secret shown once in your receiver configuration. [Verify each message](/docs/webhooks/#verify-before-processing).


## Subscribe with MCP Events
[What this is](/features.html#notify-mcp-events)


1. In an app that supports MCP Events, call `events/list` and select an event.
2. Call `events/subscribe` with `name`, `arguments:{site,storage?}`, and `delivery:{mode:"webhook",url,secret}`. Supply the app's receiver and a `whsec_` secret, base64 of 24 to 64 bytes.
3. Verify the signed challenge, then return 2xx JSON with the same `challenge` and no `type` field. Refresh before `refreshBefore`; retain the returned `cursor` for replay.


## Test, pause, rotate, and read deliveries
[What this is](/features.html#notify-controls)


1. Press **Send test**, or POST to the subscription's `/test` route.
2. Read **Deliveries**, or GET `/v1/sites/{sitename}/notifications/{id}/deliveries`.
3. Press **Pause** or **Resume**, or PATCH that subscription with `{"paused":true}` or `{"paused":false}`.
4. Press **Rotate secret**, or POST to `/rotate-secret`. Update your receiver. For an MCP subscription, refresh through its app instead.


## Switch off sign-in alerts
[What this is](/features.html#notify-emails)


1. Open Account, Preferences, and clear **Email me after each new sign-in or connected app**.


<details>
<summary>Set up Zapier, n8n, Hermes, or OpenClaw</summary>

- Zapier: choose Webhooks by Zapier, Catch Hook, copy its URL, and Send test. Put a verifier before it for raw-byte signature checks.
- n8n: add a POST Webhook node, enable Raw Body for verification, test, publish, and copy its Production URL.
- Hermes: enable its webhook adapter and configure its route secret with the shown `whsec_` value; verify the `svix-*` aliases.
- OpenClaw: enable hooks, set `Authorization: Bearer <hook token>` as the custom header, and use an adapter to verify and map the envelope to `/hooks/agent`'s `message` payload.

</details>

## Errors you may see

| Code | Means | Do this |
|---|---|---|
| `invalid_url` (400) | Receiver refused | Use public HTTPS |
| `invalid_events` (400) | Event list refused | Choose listed events |
| `client_secret` (409) | MCP subscriber manages the secret | Refresh in the subscribing app |

## Next
- [Storage and saved data](/docs/guides/storage/)
- [Agents and MCP](/docs/guides/agents/)
