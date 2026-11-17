# Notifications

## list_notifications

<div data-tool="list_notifications"></div>

List webhook subscriptions and their delivery status for one site, or every site on the account. Includes subscriptions created by connected apps.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| site | string |  | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |

## remove_notification

<div data-tool="remove_notification"></div>

Remove a webhook subscription from a site and stop its deliveries. Use the ID from list_notifications.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| id | string | yes | The subscription ID from list_notifications. |
| site | string | yes | The site's name as it appears in its address, e.g. `birthday-rsvp`: lowercase letters, numbers and hyphens. |
