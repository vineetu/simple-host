package domains

import (
	"net/http"
	"time"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// Handler is the shared route state with the custom-domain routes and the
// domain check on it.
type Handler struct{ *sitesroute.Handler }

func (h Handler) svc() Service { return Service{Service: h.OpsSitesSvc()} }

// Register mounts the custom-domain routes (one per site): the owner binds a
// hostname and gets the records to create; Caddy on-demand TLS asks
// /internal/tls-ask before issuing a certificate.
func Register(mux *http.ServeMux, sh *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h := Handler{sh}
	mux.Handle("POST /v1/sites/{sitename}/domain", authMiddleware(h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(h.bindDomain)))))
	mux.Handle("GET /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getDomain))))
	mux.Handle("DELETE /v1/sites/{sitename}/domain", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteDomain))))
	mux.Handle("POST /v1/sites/{sitename}/domain/check", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.DomainCheckLimiter, http.HandlerFunc(h.checkDomainNow)))))
	tlsAskLimiter := httpx.NewRateLimiterFor(h.Limits.RateTLSAsk.Burst, h.Limits.RateTLSAsk.Every)
	h.Caches.Rate("http.site.tlsAskLimiter", tlsAskLimiter, 10*time.Minute, 30*time.Minute)
	mux.Handle("GET /internal/tls-ask", httpx.RateLimitByIP(tlsAskLimiter, http.HandlerFunc(h.tlsAsk)))
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}", h.domainRedirect)
	mux.HandleFunc("GET /internal/domain-redirect/{handle}/{sitename}/{rest...}", h.domainRedirect)
}

// RegisterAdmin mounts the admin's domain actions on anyone's site, by id,
// run as its owner.
func RegisterAdmin(mux *http.ServeMux, sh *sitesroute.Handler, authMiddleware func(http.Handler) http.Handler) {
	h := Handler{sh}
	mux.Handle("POST /v1/admin/sites/{id}/domain", authMiddleware(h.AsSiteOwner(h.bindDomain)))
	mux.Handle("DELETE /v1/admin/sites/{id}/domain", authMiddleware(h.AsSiteOwner(h.deleteDomain)))
}
