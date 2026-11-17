# Skills

Discover and fetch this server’s agent skills and references.

## GET /v1/skills

<div data-operation="GET /v1/skills"></div>

Public catalog of the skills this server bundles (name + description + fetch URL), so any agent can search them over HTTP. No auth.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Skill catalog |

## GET /v1/skills/{name}

<div data-operation="GET /v1/skills/{name}"></div>

Returns the raw SKILL.md for the named skill. Public, no auth.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| name | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The skill's SKILL.md |
| 404 | No such skill |

## GET /v1/skills/{name}/SKILL.md

<div data-operation="GET /v1/skills/{name}/SKILL.md"></div>

Same response as /v1/skills/{name}. Public, no auth.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| name | path | string | yes |  |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The skill's SKILL.md |
| 404 | No such skill |

## GET /v1/skills/{name}/references/{file}

<div data-operation="GET /v1/skills/{name}/references/{file}"></div>

A skill's SKILL.md routes to reference documents under its references/ directory. A folder install has them on disk; install methods that fetch only SKILL.md by URL do not, so SKILL.md cites each reference by absolute URL as well and this endpoint serves it. Public, no auth.

**Parameters:**

| Name | In | Type | Required | Description |
| --- | --- | --- | --- | --- |
| name | path | string | yes |  |
| file | path | string | yes | The reference filename, e.g. backend.md |

**Responses:**

| Status | Description |
| --- | --- |
| 200 | The reference document |
| 400 | Invalid skill or reference name |
| 404 | No such reference |
