package lifecycle

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/sites/sitesroute"
	"github.com/vsriram/simple-host/host/web"
)

// routes are the lifecycle handlers on the shared route state.
type routes struct{ *sitesroute.Handler }

func (h routes) svc() Service { return Service{Service: h.SitesSvc()} }

// lifecycleSvc runs restore on the smaller lifecycle service, as before.
func (h routes) lifecycleSvc() Service { return Service{Service: h.LifecycleSvc()} }

// Register mounts delete, restore, Recently deleted, rename and offline
// (PATCH), visibility, keep and allowed origins, and the admin's delete by
// id. siteOpLimiter is the instance's shared site-operation limiter.
func Register(mux *http.ServeMux, h *sitesroute.Handler, authMiddleware, noticeMiddleware func(http.Handler) http.Handler, siteOpLimiter *httpx.RateLimiter) {
	l := routes{h}
	mux.Handle("DELETE /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(l.deleteSite)))))
	mux.Handle("PATCH /v1/sites/{sitename}", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(l.patchSite)))))
	mux.Handle("POST /v1/sites/{sitename}/restore", noticeMiddleware(authMiddleware(httpx.RateLimitByIP(siteOpLimiter, http.HandlerFunc(l.restoreSite)))))
	mux.Handle("GET /v1/me/deleted-sites", noticeMiddleware(authMiddleware(http.HandlerFunc(l.listDeletedSites))))
	mux.Handle("PUT /v1/sites/{sitename}/visibility", noticeMiddleware(authMiddleware(http.HandlerFunc(l.setVisibility))))
	mux.Handle("PUT /v1/sites/{sitename}/keep", noticeMiddleware(authMiddleware(http.HandlerFunc(l.setSiteKeep))))
	// The page origins allowed to call this site's API (visitor sign-in reads
	// them). Event and team sites never take extra origins.
	if !h.Ed.Storage().EventSites {
		mux.Handle("PUT /v1/sites/{sitename}/allowed-origins", noticeMiddleware(authMiddleware(http.HandlerFunc(l.setAllowedOrigins))))
	}
	mux.Handle("DELETE /v1/admin/sites/{id}", authMiddleware(h.AsSiteOwner(l.deleteSite)))
}

type deletedSiteResponse struct {
	Name         string    `json:"name"`
	DeletedAt    time.Time `json:"deleted_at"`
	PurgeAt      time.Time `json:"purge_at"`
	CustomDomain string    `json:"custom_domain,omitempty"`
}

func toDeletedSiteResponse(d model.DeletedSite, retention time.Duration) deletedSiteResponse {
	return deletedSiteResponse{Name: d.Name, DeletedAt: d.DeletedAt.UTC(), PurgeAt: d.PurgeAt(retention).UTC(), CustomDomain: d.CustomDomain.String}
}

// deleteSite moves a site to Recently deleted: marked in the database, and its
// files moved out of every served path, in that order with the commit last so
// a failure leaves the site live and whole.
func (h routes) deleteSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}
	expected, check := r.Context().Value(web.AdminSiteKey{}).(string)
	in := sites.DeleteInput{UserID: user.ID, Name: siteName, ExpectedSiteID: expected, CheckSiteID: check}
	if r.URL.Query().Has("confirm_name") {
		confirm := r.URL.Query().Get("confirm_name")
		in.ConfirmName = &confirm
	}
	if err := h.LifecycleSvc().Delete(r.Context(), in); err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "confirm_name_mismatch":
				httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: e.Message, Code: e.Code})
				return
			case "delete_not_found":
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "site not found"})
				return
			case "site_suspended":
				web.WriteSiteSuspended(w, e.Message)
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h routes) listDeletedSites(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	list, err := h.SitesSvc().ListDeletedSitesByUser(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]deletedSiteResponse, 0, len(list))
	for _, d := range list {
		out = append(out, toDeletedSiteResponse(d, h.Limits.DeletedRetention))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"sites": out, "retention_days": int(h.Limits.DeletedRetention / (24 * time.Hour))})
}

func (h routes) restoreSite(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	result, err := h.lifecycleSvc().Restore(r.Context(), RestoreInput{UserID: user.ID, Name: siteName, Handle: user.Handle.String})
	if err != nil {
		var e *sites.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "site_not_deleted":
				httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "that site is not deleted", Code: "site_not_deleted"})
				return
			case "deleted_not_found":
				httpx.WriteJSON(w, http.StatusNotFound, httpx.ErrorResponse{Error: "no site with that name in Recently deleted"})
				return
			case "restore_files":
				httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: "could not restore the site's files"})
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.ToSiteResponse(result.Site, "Restored with all its versions and saved data."))
}

func (h routes) setSiteOffline(w http.ResponseWriter, r *http.Request, name string, on bool) {
	user := auth.GetUser(r.Context())
	site, err := h.svc().SetSiteOffline(r.Context(), user.ID, name, on)
	if err != nil {
		sitesroute.WriteSitesError(w, err, name)
		return
	}
	log.Printf("site_offline on=%t site_id=%s name=%s by=%s", on, site.ID, site.Name, user.ID)
	note := "The site is back online at every address."
	if on {
		note = "The site is offline: every address shows “This site is offline” and visitor saves are refused. Nothing is deleted; switch it back on at any time."
	}
	httpx.WriteJSON(w, http.StatusOK, h.ToSiteResponse(site, note))
}

// patchSite is PATCH /v1/sites/{sitename}: {"name": "..."} renames the site,
// {"offline": true|false} takes it offline or back online. One change per
// request.
func (h routes) patchSite(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    *string `json:"name"`
		Offline *bool   `json:"offline"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || (req.Name == nil) == (req.Offline == nil) {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `invalid JSON body (expected {"name":"new-name"} or {"offline":true|false})`, Code: "invalid_request"})
		return
	}
	name := strings.TrimSpace(r.PathValue("sitename"))
	if req.Offline != nil {
		h.setSiteOffline(w, r, name, *req.Offline)
		return
	}
	h.renameSite(w, r, name, strings.TrimSpace(*req.Name))
}

func (h routes) renameSite(w http.ResponseWriter, r *http.Request, oldName, newName string) {
	user := auth.GetUser(r.Context())
	result, err := h.svc().Rename(r.Context(), RenameInput{UserID: user.ID, OldName: oldName, NewName: newName, Handle: user.Handle.String})
	if err != nil {
		var siteErr *sites.Error
		if errors.As(err, &siteErr) {
			switch siteErr.Code {
			case "invalid_name", "name_reserved":
				httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message, Code: siteErr.Code})
				return
			case "unchanged_name":
				httpx.WriteJSON(w, 400, httpx.ErrorResponse{Error: siteErr.Message})
				return
			case "site_exists":
				httpx.WriteJSON(w, http.StatusConflict, httpx.ErrorResponse{Error: "you already have a site with that name", Code: "site_exists"})
				return
			case "recently_deleted":
				httpx.WriteJSON(w, http.StatusConflict, map[string]any{"error": siteErr.Message, "code": "recently_deleted", "recently_deleted": true})
				return
			case "move_failed":
				httpx.WriteJSON(w, http.StatusInternalServerError, httpx.ErrorResponse{Error: "could not move site files"})
				return
			}
		}
		sitesroute.WriteSitesError(w, err, oldName)
		return
	}
	site, oldURL, newURL, domain := result.Site, result.OldURL, result.NewURL, result.Domain

	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id": site.ID, "old_name": oldName, "name": newName,
		"old_url": oldURL, "site_url": newURL, "old_url_status": "redirects",
		"message":                 "Site renamed. Links to the old address redirect to the new one until a new site takes the old name.",
		"custom_domain_unchanged": domain != "",
	})
}

// setAllowedOrigins lets a site owner list extra origins that may call the site's API cross-origin, so a page
// hosted anywhere can use this site as its backend. Owner-only.
func (h routes) setAllowedOrigins(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	var req struct {
		Origins []string `json:"origins"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&req); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "invalid JSON body (expected {\"origins\":[\"https://you.github.io\"]})"})
		return
	}
	clean, err := h.svc().SetAllowedOrigins(r.Context(), user.ID, siteName, req.Origins)
	var siteErr *sites.Error
	if errors.As(err, &siteErr) && (siteErr.Code == "invalid_origin" || siteErr.Code == "too_many_origins") {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: siteErr.Message})
		return
	}
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"site": siteName, "allowed_origins": clean})
}

// setVisibility toggles a site's showcase visibility ('public' | 'unlisted'); owner-only via GetSiteByUser.
func (h routes) setVisibility(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}

	siteName := strings.TrimSpace(r.PathValue("sitename"))
	if siteName == "" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "site name is required"})
		return
	}

	var body struct {
		Visibility string `json:"visibility"`
	}
	if !httpx.DecodeJSON(w, r, 64<<10, &body) {
		return
	}
	vis := strings.ToLower(strings.TrimSpace(body.Visibility))
	if vis != "public" && vis != "unlisted" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "visibility must be 'public' or 'unlisted'"})
		return
	}

	if err := h.svc().SetVisibility(r.Context(), VisibilityInput{UserID: user.ID, Name: siteName, Visibility: vis}); err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}

	httpx.WriteJSON(w, http.StatusOK, map[string]string{"name": siteName, "visibility": vis})
}

func (h routes) setSiteKeep(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	if user == nil {
		httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
		return
	}
	siteName := strings.TrimSpace(r.PathValue("sitename"))
	var req struct {
		Keep *bool `json:"keep"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil || req.Keep == nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `body must be {"keep": true} or {"keep": false}`})
		return
	}
	site, err := h.svc().SetSiteKeep(r.Context(), user.ID, siteName, *req.Keep)
	if err != nil {
		sitesroute.WriteSitesError(w, err, siteName)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": site.Name, "keep": *req.Keep})
}
