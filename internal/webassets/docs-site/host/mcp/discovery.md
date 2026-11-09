# Finding tools

## find_tools

<div data-tool="find_tools"></div>

**Find more Simple Host tools**

Find tools for a job: own domain or short free address, passcode, rollback, rename, delete and restore, visitor numbers, adding or editing your own entry on a shared site, home page, export, notifications, moving a WordPress site, or more storage. Returns matching tools and arguments; call your pick with run_tool.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| limit | integer |  |  |
| query | string | yes | The job in a few words; empty lists the other tools. |

Reached through find_tools and run_tool.

## run_tool

<div data-tool="run_tool"></div>

**Run a Simple Host tool**

Run a Simple Host tool by name with its arguments, from find_tools or a skill, recipe, or result naming a tool outside your list. The same checks apply; ask the person first where that tool's description says so.

**Parameters:**

| Name | Type | Required | Description |
| --- | --- | --- | --- |
| arguments | object |  | The tool's arguments; defaults to {}. |
| name | string | yes | The tool to call. |

Reached through find_tools and run_tool.
