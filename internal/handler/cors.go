package handler

import (
	"net/http"
	"strings"
)

// corsAllowHeaders are the request headers a cross-origin caller may send to the
// management API: the API key, JSON/archive content type, the optional upload
// integrity digest, and the skill-version hint.
const corsAllowHeaders = "Content-Type, X-API-Key, X-Content-Digest, X-Skill-Version"

// CORS makes the management API callable from any website's browser JS so that,
// e.g., a web tool can deploy a site given the user's API key.
//
// This is safe to open to "*" because the API authenticates with the X-API-Key
// *header*, not cookies: browsers never attach it ambiently, so there is no CSRF
// surface — a cross-origin page can only succeed if it already holds a valid key
// (which only happens if the user gave it one). No Access-Control-Allow-
// Credentials is sent, so "*" is permitted by the CORS spec.
//
// The per-site /state endpoints are deliberately EXCLUDED: they run their own
// stricter per-site Origin policy (authorizeStateOrigin / optionsSiteState) and
// set their own Access-Control-Allow-Origin, so we must not double-handle them.
func CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The per-site state + collections endpoints run their own stricter,
		// credentialed Origin policy (authorizeStateOrigin), so the permissive
		// "*" policy must not touch them.
		if strings.HasSuffix(r.URL.Path, "/state") || strings.Contains(r.URL.Path, "/collections/") {
			next.ServeHTTP(w, r)
			return
		}

		// Open to any origin. Safe without credentials (header auth, not cookies).
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Add("Vary", "Origin")

		if r.Method == http.MethodOptions {
			// Preflight: answer here, never reaching auth/handlers.
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", corsAllowHeaders)
			w.Header().Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
