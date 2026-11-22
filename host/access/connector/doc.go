// Package connector is the OAuth 2.1 authorization server and the remote MCP
// endpoint it protects (connector.go): discovery, client registration,
// /oauth/authorize with its consent screen (connector_authorize.go), the
// token, revoke and /mcp routes and BearerAuth, which turns an access token
// into an in-process credential for the REST routes (connector_token.go),
// and the plugin reviewer's password sign-in (reviewer.go). It is the one
// Access package that imports host/mcp: it hosts the MCP server. Grants and
// tokens are the core's (access.Connector).
package connector
