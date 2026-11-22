// Package keys is a person's API keys and sign-out: list them, mint a named
// or deploy-only key, revoke one, rotate them all (which also disconnects
// apps and ends visitor sign-ins) and sign out, which revokes the key the
// request came with and the browser's SSO pass. Turning a key into a person
// is the core's (Service.GetUserByAPIKey); the api_keys table stays with the
// core's repo for that reason.
package keys
