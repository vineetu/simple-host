package auth

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"net/url"
	"strings"

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
//
// The scope limits what the key itself can call, not what the pages it
// publishes do: a deploy key can publish code that runs when the owner opens
// their own site, where a signed-in owner has powers over its saved data.
// Every place a deploy key is made says so ("treat it like the site itself").
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

// teamRoutes is everything a member's team key (db.KeyScopeTeam, hosted
// events) may call, and only on the team's own site: every {sitename} route
// here must name it. Deploys, versions, rollback and preview links; the
// site's saved data as its owner (declaring kinds so visitors' saves are
// private, reading what visitors sent, Page info, removing an item, who may
// save); and its visit counts. Never deleting or renaming the site, domains,
// passcodes, version retention, visibility, open writes or anything of an
// account. GET /v1/sites lists the team's site only (site.go).
var teamRoutes = map[string]bool{
	"GET /v1/me":                                             true, // answers who the key acts for (user.go)
	"GET /v1/sites/{sitename}/data":                          true,
	"GET /v1/sites/{sitename}/data/{coll}":                   true,
	"PUT /v1/sites/{sitename}/data/{coll}":                   true,
	"DELETE /v1/sites/{sitename}/data/{coll}/items/{id}":     true,
	"GET /v1/sites/{sitename}/data/{coll}/kind":              true,
	"PUT /v1/sites/{sitename}/data/{coll}/kind":              true,
	"GET /v1/sites/{sitename}/collections":                   true,
	"GET /v1/sites/{sitename}/collections/{coll}":            true,
	"GET /v1/sites/{sitename}/collections/{coll}/export.csv": true,
	"GET /v1/sites/{sitename}/savers":                        true,
	"PUT /v1/sites/{sitename}/savers":                        true,
	"POST /v1/sites/{sitename}/savers/block":                 true,
	"GET /v1/sites/{sitename}/analytics":                     true,
	"GET /v1/sites/{sitename}/analytics/top":                 true,
	"GET /v1/sites/{sitename}/analytics/geo":                 true,
}

// TeamKeyAllows reports whether a team key may call the route the mux
// registered as pattern (on its own site; the gate checks the name).
func TeamKeyAllows(pattern string) bool { return deployRoutes[pattern] || teamRoutes[pattern] }

// TeamOnlyMessage is the refusal a team key gets outside its routes.
const TeamOnlyMessage = "this is a team key: it publishes and looks after your team's site only. " +
	"Use your own sign-in on simple-hack.app for anything else."

var teamKeys bool

// SetTeamKeys turns on team keys (EVENTS=hosted). Off, the gate never looks
// for them, so other instances pay nothing for them.
func SetTeamKeys(on bool) { teamKeys = on }

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
// themselves, and the MCP server's calls back into the mux. The key is looked
// up only when the route is not a deploy route, so deploys cost nothing extra.
// Unknown, expired, admin and internal keys pass through untouched; whatever
// the route does with them is unchanged.
//
// On a hosted-events instance (SetTeamKeys) it also holds team keys to
// teamRoutes and deployRoutes on their own team's site (403 team_key_scope,
// 403 team_site_only), refuses them every change once the team may no
// longer change its site (the deadline, an ended or taken-down event, a site
// the organiser took down; deploys check again under a lock, site.go), and
// refuses a team key whose person is no longer on the team.
func ScopeGate(database *sql.DB, mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			mux.ServeHTTP(w, r)
			return
		}
		_, pattern := mux.Handler(r)
		if teamKeys {
			isTeam, live, id, err := db.TeamScopeOfKey(r.Context(), database, key)
			if err != nil {
				log.Printf("scope gate: team key: %v", err)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if isTeam {
				teamGate(w, r, mux, database, pattern, live, id)
				return
			}
		}
		if DeployKeyAllows(pattern) {
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

// teamGate serves a request made with a team credential, or refuses it.
func teamGate(w http.ResponseWriter, r *http.Request, mux *http.ServeMux, database *sql.DB, pattern string, live bool, id db.TeamIdentity) {
	if !TeamKeyAllows(pattern) {
		writeJSON(w, http.StatusForbidden, errorResponse{Error: TeamOnlyMessage, Code: "team_key_scope"})
		return
	}
	if !live {
		writeJSON(w, http.StatusUnauthorized, errorResponse{
			Error: "this team key stopped working: its person is no longer on that team. Get a new key from your event page on simple-hack.app.",
			Code:  "team_key_inactive",
		})
		return
	}
	if strings.Contains(pattern, "{sitename}") {
		name, ok := sitePathName(r)
		if !ok || !strings.EqualFold(name, id.TeamSlug) {
			writeJSON(w, http.StatusForbidden, errorResponse{
				Error: "this team key works on your team's site only: " + id.TeamSlug,
				Code:  "team_site_only",
			})
			return
		}
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		// Every change stops with the team's deadline; a preview link only
		// opens what is already stored, and MCP calls come back through
		// here as the REST call they stand for.
		if pattern != "POST /v1/sites/{sitename}/versions/{version}/preview-link" && !strings.HasSuffix(pattern, " /mcp") {
			st, err := db.TeamWriteStateFor(r.Context(), database, id.TeamID, false)
			if err != nil {
				log.Printf("scope gate: team state: %v", err)
				writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "internal server error"})
				return
			}
			if code := st.WriteRefusal(); code != "" {
				status, msg := TeamRefusal(code)
				writeJSON(w, status, errorResponse{Error: msg, Code: code})
				return
			}
		}
	}
	mux.ServeHTTP(w, r)
}

// TeamRefusal is the status and message for a TeamWriteState refusal code.
func TeamRefusal(code string) (int, string) {
	switch code {
	case "event_taken_down":
		return http.StatusForbidden, "this event has been taken down"
	case "event_closed":
		return http.StatusConflict, "this event has ended, so its team sites no longer change"
	case "team_site_taken_down":
		return http.StatusForbidden, "the organiser has taken your team's site down; ask them about it"
	case "submissions_closed":
		return http.StatusConflict, "the submission deadline has passed, so your team's site and entry are frozen. Ask the organiser if you need more time"
	}
	return http.StatusConflict, "your team cannot change its site now"
}
