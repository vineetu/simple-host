package auth

import (
	"database/sql"
	"errors"
	"log"
	"net/http"

	"github.com/vsriram/simple-host/internal/db"
)

// deployRoutes is everything a deploy-only key may call, by the pattern the
// mux registered. It is the one place the deploy scope is decided: a route
// missing here is refused to a deploy key, so a new route is never reachable
// by one until someone adds it on purpose. A deploy key creates, updates,
// rolls back and lists the account's sites, reads their versions and files,
// and makes preview links; it never deletes, renames, touches domains, keys,
// saved data or the account. /mcp is listed because the MCP server serves
// each tool call back through this same gate as the caller, so a tool is
// allowed exactly when the REST route behind it is.
var deployRoutes = map[string]bool{
	"GET /v1/sites":                                               true,
	"POST /v1/sites/{sitename}":                                   true,
	"PUT /v1/sites/{sitename}":                                    true,
	"POST /v1/sites/{sitename}/files":                             true,
	"PUT /v1/sites/{sitename}/files":                              true,
	"GET /v1/sites/{sitename}/versions":                           true,
	"GET /v1/sites/{sitename}/versions/{version}/files":           true,
	"GET /v1/sites/{sitename}/versions/{version}/files/{path...}": true,
	"PUT /v1/sites/{sitename}/active-version":                     true,
	"POST /v1/sites/{sitename}/versions/{version}/preview-link":   true,
	"GET /mcp":    true,
	"POST /mcp":   true,
	"DELETE /mcp": true,
}

// DeployKeyAllows reports whether a deploy-only key may call the route the
// mux registered as pattern.
func DeployKeyAllows(pattern string) bool { return deployRoutes[pattern] }

// DeployOnlyMessage is the refusal a deploy-only key gets everywhere else.
const DeployOnlyMessage = "this is a deploy-only key: it can create, update, roll back and list sites and make preview links, nothing else. " +
	"Use a full key for this (the Keys panel on your Simple Host page makes one)."

// ScopeGate refuses a deploy-only key on every route deployRoutes does not
// list (403 deploy_only_key). It wraps the whole mux, so it sees every request
// that carries X-API-Key: REST routes, the page-data routes that read the key
// themselves, and the MCP server's calls back into the mux. The key is looked
// up only when the route is not a deploy route, so deploys cost nothing extra.
// Unknown, expired, admin and internal keys pass through untouched; whatever
// the route does with them is unchanged.
func ScopeGate(database *sql.DB, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			mux.ServeHTTP(w, r)
			return
		}
		if _, pattern := mux.Handler(r); DeployKeyAllows(pattern) {
			mux.ServeHTTP(w, r)
			return
		}
		scope, err := db.APIKeyScope(r.Context(), database, key)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("scope gate: %v", err)
			writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
			return
		}
		if scope == db.KeyScopeDeploy {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: DeployOnlyMessage, Code: "deploy_only_key"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}
