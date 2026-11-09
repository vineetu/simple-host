# Notifications

## list_notifications

<div data-tool="list_notifications"></div>

**List notifications**

List your visitor-save subscriptions for a selected team site or an organiser website. Omit both to list your subscriptions.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string |  | Event slug for the organiser website, as in hack_event_storage tools. |
| site | string |  | Selected team site; omit when using event. |

## remove_notification

<div data-tool="remove_notification"></div>

**Remove notification**

Remove a webhook subscription from a site and stop its deliveries. Use the ID from list_notifications.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| event | string |  | Event slug for the organiser website, as in hack_event_storage tools. |
| id | string | yes | Subscription ID. |
| site | string |  | Selected team site; omit when using event. |
