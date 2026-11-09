# Discovery

Public product, connector, and API discovery.

## GET /

<div data-operation="GET /"></div>

The selected Host or Hack landing. Accept text/markdown negotiates Markdown; HTML is the default. Both carry discovery Link headers and Vary: Accept.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the product landing page |

## GET /.well-known/agent-card.json

<div data-operation="GET /.well-known/agent-card.json"></div>

Public product identity, version, OAuth discovery, and generic tool overview. Hack lists event and selected-team tools with role rules, without account or event data.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the agent capability card |

## GET /.well-known/ai-catalog.json

<div data-operation="GET /.well-known/ai-catalog.json"></div>

Links the agent card, MCP server card, and OpenAPI document.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the AI catalog |

## GET /.well-known/api-catalog

<div data-operation="GET /.well-known/api-catalog"></div>

RFC 9727 linkset for this product's OpenAPI formats, role or deployment guide, and API reference.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the API catalog |

## GET /.well-known/ard.json

<div data-operation="GET /.well-known/ard.json"></div>

Product-specific resource names and identifiers with links to the server card, skills, and REST API.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the agent resource directory |

## GET /.well-known/mcp/server-card.json

<div data-operation="GET /.well-known/mcp/server-card.json"></div>

Public product identity, build version, icon, and local MCP endpoint. Available on Host and Hack.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the MCP server card |

## GET /index.md

<div data-operation="GET /index.md"></div>

The selected product landing copy, with this instance's canonical address.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the landing as Markdown |

## GET /llms.md

<div data-operation="GET /llms.md"></div>

Alias of /index.md.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the landing as Markdown |

## GET /mcp/server-card

<div data-operation="GET /mcp/server-card"></div>

Alias of /.well-known/mcp/server-card.json.

No authentication required.

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Read the MCP server card |
