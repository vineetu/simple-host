package deploy

import (
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

// routes are the deploy handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

// Register mounts the deploy routes: the archive upload (POST/PUT
// /v1/sites/{sitename}), the JSON deploy (.../files) and photo saves, behind
// the shared upload limiter and create idempotency.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	d := routes{h}
	mux.Handle("POST /v1/sites/{sitename}", authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(d.createSite))))))
	mux.Handle("PUT /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(d.updateSite)))))
	// JSON deploy: inline file contents, same auth and rate-limit chain as the archive upload.
	mux.Handle("POST /v1/sites/{sitename}/files", authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(noticeMiddleware(http.HandlerFunc(d.createSiteFiles))))))
	mux.Handle("PUT /v1/sites/{sitename}/files", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, http.HandlerFunc(d.updateSiteFiles)))))
	photos := h.Ed.Storage().Photos
	mux.Handle("POST /v1/sites/{sitename}/photos", web.PhotosHostOnly(photos, authMiddleware(httpx.RateLimitByIP(h.UploadLimiter, h.CreateIdempotency.Wrap(http.HandlerFunc(d.addPhotos))))))
}
