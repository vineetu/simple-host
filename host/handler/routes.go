package handler

import (
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/data/notifications"
	"github.com/vsriram/simple-host/host/data/push"

	"github.com/vsriram/simple-host/host/data/files"
	"github.com/vsriram/simple-host/host/data/kv"
	"github.com/vsriram/simple-host/host/data/resources"
	"github.com/vsriram/simple-host/host/data/rows"
	"github.com/vsriram/simple-host/host/data/sqlconsole"

	"github.com/vsriram/simple-host/host/data/visitors"

	"github.com/vsriram/simple-host/host/data/usage"

	"github.com/vsriram/simple-host/host/access/accesshttp"

	appstorage "github.com/vsriram/simple-host/host/data"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/mcp"
	"github.com/vsriram/simple-host/host/secrets"
	secretscall "github.com/vsriram/simple-host/host/secrets/call"
	secretsmanage "github.com/vsriram/simple-host/host/secrets/manage"
	"github.com/vsriram/simple-host/host/sites/siteshttp"
	"github.com/vsriram/simple-host/host/web"
)

// Register mounts the site, access, data and secrets routes on mux. The rate
// limiters for site operations and exports are shared between the site
// routes and the account routes.
func Register(mux *http.ServeMux, h *siteshttp.SiteHandler, a *accesshttp.AccessHandler, d *appstorage.Handler, keys *secrets.Store, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	secretsmanage.Register(mux, h.Database, h.Hosts, keys, authMiddleware, noticeMiddleware)
	secretscall.Register(mux, d, a.StorageCaller, keys)
	notifications.Register(mux, d, authMiddleware, noticeMiddleware)
	accesshttp.RegisterEditors(mux, a, authMiddleware, noticeMiddleware)
	push.Register(mux, d, a.PasscodeGate, a.StorageCaller, authMiddleware, noticeMiddleware)
	d.RegisterStorageRoutes(mux)
	kv.Register(mux, d, a.StorageCaller)
	rows.Register(mux, d, a.StorageCaller)
	sqlconsole.Register(mux, d, a.StorageCaller)
	files.Register(mux, d, a.StorageCaller)
	resources.Register(mux, d, a.StorageCaller)
	usage.Register(mux, d, a.StorageCaller)
	visitors.Register(mux, d, a.StorageCaller)
	// Delete, rename and restore each take per-site locks: rate-limited so a
	// loop cannot pile up locks or disk moves.
	siteOpLimiter := httpx.NewRateLimiterFor(h.Limits.RateSiteOps.Burst, h.Limits.RateSiteOps.Every)
	h.Caches.Rate("http.site.siteOpLimiter", siteOpLimiter, 10*time.Minute, 30*time.Minute)
	// Owner downloads are heavy (a tar of every site): RATE_LIMIT_EXPORT per
	// address, like the signed export link; shared with the account export.
	exportLimiter := httpx.NewRateLimiterFor(h.Limits.RateExport.Burst, h.Limits.RateExport.Every)
	h.Caches.Rate("http.site.exportLimiter", exportLimiter, 10*time.Minute, 30*time.Minute)
	h.RegisterRoutes(mux, authMiddleware, noticeMiddleware, siteOpLimiter, exportLimiter, a.DeleteSitePasscode)
	accesshttp.RegisterAccountRoutes(mux, a, authMiddleware, siteOpLimiter, exportLimiter)
	a.RegisterGateRoutes(mux, authMiddleware, noticeMiddleware, siteOpLimiter)
	mux.HandleFunc("GET /recipes/{topic}", serveRecipe)
}

// serveRecipe is the connector's page recipe (get_page_recipe) for agents
// that read llms.txt without the connector: the same text, with a
// placeholder site.
func serveRecipe(w http.ResponseWriter, r *http.Request) {
	topic := strings.TrimSuffix(r.PathValue("topic"), ".md")
	text, err := mcp.PageRecipe(topic, "")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	body := []byte(text)
	if web.InstanceHosts != nil {
		body = web.InstanceHosts.Apply(body)
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write(body)
}
