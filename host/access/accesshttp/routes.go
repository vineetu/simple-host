package accesshttp

import (
	"database/sql"
	"net/http"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/gate"
	"github.com/vsriram/simple-host/host/access/profile"
	"github.com/vsriram/simple-host/host/edition"

	"github.com/vsriram/simple-host/host/access/accountdata"
	"github.com/vsriram/simple-host/host/access/operator"
	"github.com/vsriram/simple-host/host/access/suspension"

	"github.com/vsriram/simple-host/host/access/editors"

	"github.com/vsriram/simple-host/host/httpx"
)

// Site editors: a person who may publish, update and roll back one site of
// an account without belonging to it (accounts.ActsAsOwner). The account
// itself, or a member of the team with owner=<team>, manages them; an editor
// never reaches these routes (auth.ScopeGate holds editors to EditorAllows).
// Offered where accounts own their own sites, like teams.
func RegisterEditors(mux *http.ServeMux, h *AccessHandler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	if !h.Ed.Identity().PersonalSites {
		return
	}
	editors.Register(mux, h.Database, authMiddleware, noticeMiddleware)
}

// RegisterAccountRoutes mounts the account's own routes and the operator's
// account routes; siteOpLimiter and exportLimiter are the site routes' own,
// shared with them.
func RegisterAccountRoutes(mux *http.ServeMux, h *AccessHandler, authMiddleware func(http.Handler) http.Handler, siteOpLimiter, exportLimiter *httpx.RateLimiter) {
	(&operator.Handler{Database: h.Database, Ed: h.Ed, Reporting: h.Reporting}).Register(mux, authMiddleware, siteOpLimiter)
	(&profile.Patch{Database: h.Database, Hosts: h.Hosts, SiteFiles: h.SiteFiles, Limits: h.Limits, Ed: h.Ed}).Register(mux, authMiddleware)
	(&accountdata.Handler{Database: h.Database, Hosts: h.Hosts, Limits: h.Limits, Mailer: h.Mailer, Sites: h.SitesSvc(),
		SiteFiles: h.SiteFiles, SiteArchive: h.SiteArchive, Cutoff: h.Cutoff()}).Register(mux, authMiddleware, siteOpLimiter, exportLimiter)
	(&suspension.Handler{Hosts: h.Hosts, Sites: h.SitesSvc(), Cutoff: h.Cutoff(), Pages: h.Pipeline()}).Register(mux, authMiddleware)
}

// AccessHandler is the site gate and the visitor's ways through it
// (host/access/gate), with what the account routes read from it.
type AccessHandler = gate.Handler

// OAuthHandler is provider sign-in and the SSO hop (host/access/gate).
type OAuthHandler = gate.OAuthHandler

// OAuthSettings configures provider sign-in (gate.OAuthSettings).
type OAuthSettings = gate.OAuthSettings

// NewOAuthHandler is gate.NewOAuthHandler.
func NewOAuthHandler(database *sql.DB, service *accounts.Service, cfg OAuthSettings, access *AccessHandler, editions ...*edition.Edition) *OAuthHandler {
	return gate.NewOAuthHandler(database, service, cfg, access, editions...)
}
