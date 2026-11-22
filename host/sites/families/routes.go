package families

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
)

// Handler is the shared route state with the family routes and the family
// check on it.
type Handler struct{ *sitesroute.Handler }

func (h Handler) svc() Service { return Service{Service: h.SitesSvc()} }

// Register mounts the owner's and the admin's address-family routes.
func Register(mux *http.ServeMux, sh *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	h := Handler{sh}
	checkIP := h.FamilyCheckLimiter
	if checkIP == nil {
		checkIP = httpx.NewRateLimiterFor(h.Limits.RateFamilyCheck.Burst, h.Limits.RateFamilyCheck.Every)
	}
	mux.Handle("POST /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.createFamily))))
	mux.Handle("GET /v1/me/address-families", noticeMiddleware(authMiddleware(http.HandlerFunc(h.listFamilies))))
	mux.Handle("GET /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.getFamily))))
	mux.Handle("PATCH /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.patchFamily))))
	mux.Handle("DELETE /v1/me/address-families/{suffix}", noticeMiddleware(authMiddleware(http.HandlerFunc(h.deleteFamily))))
	mux.Handle("POST /v1/me/address-families/{suffix}/check", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(checkIP, http.HandlerFunc(h.checkFamilyNow)))))
	mux.Handle("GET /v1/admin/address-families", authMiddleware(http.HandlerFunc(h.adminListFamilies)))
	mux.Handle("POST /v1/admin/users/{id}/address-families", authMiddleware(http.HandlerFunc(h.adminCreateFamily)))
	mux.Handle("PUT /v1/admin/address-families/{id}/cert-mode", authMiddleware(http.HandlerFunc(h.adminSetFamilyCert)))
	mux.Handle("PUT /v1/admin/address-families/{id}/proof-exempt", authMiddleware(http.HandlerFunc(h.adminSetFamilyProofExempt)))
	mux.Handle("POST /v1/admin/address-families/{id}/check", authMiddleware(http.HandlerFunc(h.adminCheckFamily)))
	mux.Handle("DELETE /v1/admin/address-families/{id}", authMiddleware(http.HandlerFunc(h.adminDeleteFamily)))
}
