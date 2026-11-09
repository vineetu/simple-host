# Health

Service health and readiness.

## GET /healthz

<div data-operation="GET /healthz"></div>

Liveness probe

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Alive |

## GET /readyz

<div data-operation="GET /readyz"></div>

Readiness probe (DB connected)

**Responses:**

| Status | Description |
| --- | --- |
| 200 | Ready |
| 503 | Not ready |
