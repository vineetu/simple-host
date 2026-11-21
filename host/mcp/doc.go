// Package mcp serves the Model Context Protocol over Streamable HTTP, so a chat
// app (ChatGPT, Claude, Grok) can publish and manage Simple Host sites through
// tool calls.
//
// Every tool is an adapter over the REST handler that already exists: a tool
// builds one or more /v1 requests and serves them, in process, into the same
// router the public API uses, carrying the caller's own credential. Nothing
// here reimplements deploy, rollback or state, so an MCP call meets exactly the
// limits, validation and permissions of the equivalent REST call.
//
// Authentication is not this package's job. The caller of ServeHTTP has
// already proved who the request is for (an OAuth access token, or an API key)
// and attached a Caller to the request context with WithCaller.
//
// Adding a tool: declare it beside its neighbours in the family file
// (sites_read_tools.go, data_storage_tools.go, ...), calling the REST route
// with c.do; add its row to hostAnnotationTable (annotations_test.go) and its
// entry to plugins/openai/chatgpt-app-submission.json. Simple Hack serves it
// only if it is added to hostTools in products/hack/mcp/contribution.go.
package mcp
