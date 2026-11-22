package auth

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
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
//
// The scope limits what the key itself can call, not what the pages it
// publishes do: a deploy key can publish code that runs when the owner opens
// their own site, where a signed-in owner has powers over its saved data.
// Every place a deploy key is made says so ("treat it like the site itself").
var deployRoutes = map[string]bool{
	"POST /v1/sites/{sitename}/photos":                            true,
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

// editorRoutes is everything an editor of one site of an account (not a
// member of it) may call with owner=<account>, and only on that site: every
// {sitename} route here must name it. Deploys, versions, rollback and preview
// links; the site's storage as its owner; its visit counts; and their own
// subscriptions to the site. Never
// deleting or renaming the site, its editors, domains, passcodes, version
// retention, visibility or anything of the account.
var editorRoutes = map[string]bool{
	"GET /v1/sites/{sitename}/analytics":                                         true,
	"GET /v1/sites/{sitename}/analytics/top":                                     true,
	"GET /v1/sites/{sitename}/analytics/geo":                                     true,
	"GET /v1/sites/{sitename}/storage/resources":                                 true,
	"GET /v1/sites/{sitename}/storage/usage":                                     true,
	"PUT /v1/sites/{sitename}/storage/resources/{name}":                          true,
	"DELETE /v1/sites/{sitename}/storage/resources/{name}":                       true,
	"GET /v1/sites/{sitename}/storage/kv/{name}/keys":                            true,
	"GET /v1/sites/{sitename}/storage/kv/{name}/keys/{key}":                      true,
	"PUT /v1/sites/{sitename}/storage/kv/{name}/keys/{key}":                      true,
	"DELETE /v1/sites/{sitename}/storage/kv/{name}/keys/{key}":                   true,
	"POST /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows":        true,
	"GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows":         true,
	"GET /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}":    true,
	"PATCH /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}":  true,
	"DELETE /v1/sites/{sitename}/storage/sqlite/{name}/tables/{table}/rows/{id}": true,
	"POST /v1/sites/{sitename}/storage/sqlite/{name}/query":                      true,
	"POST /v1/sites/{sitename}/storage/sqlite/{name}/execute":                    true,
	"POST /v1/sites/{sitename}/storage/sqlite/{name}/schema":                     true,
	"GET /v1/sites/{sitename}/storage/files/{name}/objects":                      true,
	"GET /v1/sites/{sitename}/storage/files/{name}/objects/{path...}":            true,
	"PUT /v1/sites/{sitename}/storage/files/{name}/objects/{path...}":            true,
	"DELETE /v1/sites/{sitename}/storage/files/{name}/objects/{path...}":         true,
	"POST /v1/sites/{sitename}/storage/files/{name}/download-link":               true,
	// The editor's own subscriptions to the site's saves.
	"GET /v1/sites/{sitename}/notifications":                     true,
	"POST /v1/sites/{sitename}/notifications":                    true,
	"PATCH /v1/sites/{sitename}/notifications/{id}":              true,
	"DELETE /v1/sites/{sitename}/notifications/{id}":             true,
	"POST /v1/sites/{sitename}/notifications/{id}/test":          true,
	"POST /v1/sites/{sitename}/notifications/{id}/rotate-secret": true,
	"GET /v1/sites/{sitename}/notifications/{id}/deliveries":     true,
}

// EditorAllows reports whether an editor of one site may call the route the
// mux registered as pattern (on that site; the gate checks the name).
func EditorAllows(pattern string) bool {
	return strings.Contains(pattern, "{sitename}") && (deployRoutes[pattern] || editorRoutes[pattern])
}

// WriteCheck is the product's write gate for a request that acts for an
// account it does not own (owner=<handle>): a refusal has a non-empty code.
// Nil on an install without a product gate.
type WriteCheck func(ctx context.Context, user *model.User, site string) (status int, code, msg string, err error)

// sitePathName is the {sitename} segment of a /v1/sites/{sitename}... path,
// unescaped as the mux matches it.
func sitePathName(r *http.Request) (string, bool) {
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/v1/sites/")
	if !ok {
		return "", false
	}
	seg, _, _ := strings.Cut(rest, "/")
	name, err := url.PathUnescape(seg)
	if err != nil || name == "" {
		return "", false
	}
	return name, true
}

// ScopeGate refuses a deploy-only key on every route deployRoutes does not
// list (403 deploy_only_key). It wraps the whole mux, so it sees every request
// that carries X-API-Key: REST routes, the page-data routes that read the key
// themselves, and the MCP server's calls back into the mux. Unknown, expired,
// admin and internal keys pass through untouched; whatever the route does
// with them is unchanged.
//
// It also answers owner=<handle> on the routes ownerRoutes accepts: a member
// of that team, or an editor of the one site the route names, acts as that
// account for this one request (accounts.ResolveOwner checks it live), anyone
// else is refused, and every other route ignores owner as before. An editor
// is held to EditorAllows on their own site (403 editor_scope). Every change
// such a request makes passes the product's write check first, when there is
// one (deploys check again under a lock, site.go). owner is read from the
// query only.
func ScopeGate(accountService *accounts.Service, mux *http.ServeMux, keyIdle time.Duration, writeCheck WriteCheck) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		// Never inherit an outer request's owner (an MCP tool call replays
		// REST requests on the /mcp request's context).
		var actingUser *model.User
		if owner := r.URL.Query().Get("owner"); owner != "" && ownerRoutes(pattern) {
			user, ok := actingOwner(w, r, accountService, key, owner, pattern, keyIdle, writeCheck)
			if !ok {
				return
			}
			actingUser = user
		}
		r = r.WithContext(accounts.WithActingOwner(r.Context(), key, actingUser))
		if DeployKeyAllows(pattern) {
			mux.ServeHTTP(w, r)
			return
		}
		scope, err := accountService.APIKeyScope(r.Context(), key, keyIdle)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("scope gate: %v", err)
			httpx.WriteInternal(w)
			return
		}
		if scope == model.KeyScopeDeploy {
			httpx.WriteError(w, http.StatusForbidden, "deploy_only_key", DeployOnlyMessage)
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// actingOwner is the account a request with owner=<account> acts for: the
// key's holder must be allowed to act for it, an editor only on their one
// site's routes, and a change must pass the product's write check. It
// writes the refusal and reports false when the request stops here.
func actingOwner(w http.ResponseWriter, r *http.Request, accountService *accounts.Service, key, owner, pattern string, keyIdle time.Duration, writeCheck WriteCheck) (*model.User, bool) {
	site := ""
	if strings.Contains(pattern, "{sitename}") {
		site, _ = sitePathName(r)
	}
	user, refusal, err := accountService.ResolveOwner(r.Context(), key, owner, site, keyIdle, strings.Contains(pattern, "/analytics"), pattern == "GET /v1/sites")
	if err != nil {
		log.Printf("scope gate: owner: %v", err)
		httpx.WriteInternal(w)
		return nil, false
	}
	if refusal != nil {
		httpx.WriteError(w, refusal.Status, refusal.Code, refusal.Message)
		return nil, false
	}
	if user != nil && (user.Member.Site != "" && !EditorAllows(pattern) || user.Member.Edits != nil && pattern != "GET /v1/sites") {
		httpx.WriteError(w, http.StatusForbidden, "editor_scope", "you edit only the site "+user.Member.Site+" of "+owner+": publish, roll back, its versions, saved data and visits; nothing else of the account")
		return nil, false
	}
	if user == nil || writeCheck == nil || !changes(r, pattern) {
		return user, true
	}
	status, code, msg, err := writeCheck(r.Context(), user, site)
	if err != nil {
		log.Printf("scope gate: write check: %v", err)
		httpx.WriteInternal(w)
		return nil, false
	}
	if code != "" {
		httpx.WriteError(w, status, code, msg)
		return nil, false
	}
	return user, true
}

// ownerRoutes are the routes that take owner=<handle>: every /v1/sites
// route, the Recently deleted list and the site traffic summary.
func ownerRoutes(pattern string) bool {
	_, path, _ := strings.Cut(pattern, " ")
	return path == "/v1/sites" || strings.HasPrefix(path, "/v1/sites/") ||
		pattern == "GET /v1/me/deleted-sites" || pattern == "GET /v1/analytics/sites"
}

// changes reports whether a request changes something. A preview link, a
// read-only SQL query or a stored-file link only opens existing data.
func changes(r *http.Request, pattern string) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return pattern != "POST /v1/sites/{sitename}/versions/{version}/preview-link" &&
		pattern != "POST /v1/sites/{sitename}/storage/sqlite/{name}/query" &&
		pattern != "POST /v1/sites/{sitename}/storage/files/{name}/download-link"
}
