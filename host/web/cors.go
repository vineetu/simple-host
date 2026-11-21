package web

import (
	"net/http"
	"strings"
)

// corsAllowHeaders are the request headers a cross-origin caller may send to the
// management API: the API key, JSON/archive content type, the optional upload
// integrity digest, and the skill-version hint.
const corsAllowHeaders = "Content-Type, Idempotency-Key, X-API-Key, X-Content-Digest, X-Skill-Version, Authorization, " +
	"MCP-Protocol-Version, Mcp-Session-Id, Mcp-Method, Mcp-Name, Last-Event-ID"

// corsExposeHeaders lets a browser-based MCP client read the auth challenge
// (it carries the metadata address) and the transport headers.
const corsExposeHeaders = "RateLimit, RateLimit-Policy, RateLimit-Limit, RateLimit-Remaining, RateLimit-Reset, Retry-After, WWW-Authenticate, Mcp-Session-Id, ETag"

// CORS makes the management API callable from any website's browser JS so that,
// e.g., a web tool can deploy a site given the user's API key.
//
// This is safe to open to "*" because the API authenticates with the X-API-Key
// *header*, not cookies: browsers never attach it ambiently, so there is no CSRF
// surface — a cross-origin page can only succeed if it already holds a valid key
// (which only happens if the user gave it one). No Access-Control-Allow-
// Credentials is sent, so "*" is permitted by the CORS spec.
//
// The per-site storage and visitor sign-in endpoints are deliberately
// EXCLUDED: they run their own stricter per-site Origin policy and set their
// own Access-Control-Allow-Origin, so we must not double-handle them.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The per-site storage + visitor /me endpoints run their own stricter,
		// credentialed Origin policy (authorizeStateOrigin), so the permissive
		// "*" policy must not touch them. GET /v1/setup/state keeps no "*" grant.
		if strings.HasSuffix(r.URL.Path, "/state") || strings.Contains(r.URL.Path, "/storage/") || r.URL.Path == "/v1/storage-download" ||
			(strings.Contains(r.URL.Path, "/sites/") && (strings.HasSuffix(r.URL.Path, "/me") || strings.Contains(r.URL.Path, "/visitor/auth"))) {
			next.ServeHTTP(w, r)
			return
		}

		// The consent screen's decision endpoint is same-origin only: it is
		// the one place a signed-in person's click mints a code, so no other
		// origin gets a CORS grant to call it.
		if strings.HasPrefix(r.URL.Path, "/oauth/authorize") {
			next.ServeHTTP(w, r)
			return
		}

		// Open to any origin. Safe without credentials (header auth, not cookies).
		// That includes the connector's metadata, registration, token and /mcp
		// endpoints, which browser-based MCP clients call cross-origin with a
		// bearer token (never a cookie).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Add("Vary", "Origin")
		w.Header().Set("Access-Control-Expose-Headers", corsExposeHeaders)

		if r.Method == http.MethodOptions {
			// Preflight: answer here, never reaching auth/handlers.
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityHeaders adds X-Content-Type-Options: nosniff to every response.
//
// It deliberately does NOT set X-Frame-Options, Strict-Transport-Security, or
// Referrer-Policy: in every real deployment a TLS-terminating proxy fronts this
// binary (the app port is not publicly reachable) and that proxy already sets
// those edge headers. Re-setting them here produced *conflicting duplicates*
// (e.g. two X-Frame-Options values, which browsers then ignore, and a weaker
// HSTS max-age shadowing the proxy's). nosniff is kept because identical
// duplicates are harmless and it's valuable on API JSON if ever served direct.
// The admin UI's Content-Security-Policy — which the proxy does NOT set — is
// applied separately in AdminUICSP; its frame-ancestors 'none'
// supersedes X-Frame-Options on modern browsers.
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}
