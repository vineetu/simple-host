package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	appsites "github.com/vsriram/simple-host/internal/app/sites"
	"github.com/vsriram/simple-host/internal/handler"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

type eventWebsitePublisher interface {
	PublishEventWebsite(http.ResponseWriter, *http.Request, handler.GroupRef, string, bool)
}

type eventWebsiteStorage interface {
	ServeEventWebsiteStorage(http.ResponseWriter, *http.Request, handler.GroupRef, string)
}

func (h *HackHandler) registerEventWebsite(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/website", wrap(h.getEventWebsite))
	mux.Handle("PATCH /v1/hack/events/{slug}/website", wrap(h.patchEventWebsite))
	mux.Handle("PUT /v1/hack/events/{slug}/website", wrap(h.putEventWebsiteArchive))
	mux.Handle("PUT /v1/hack/events/{slug}/website/files", wrap(h.putEventWebsiteFiles))
	mux.Handle("GET /v1/hack/events/{slug}/website/storage/{rest...}", wrap(h.eventWebsiteStorage))
	mux.Handle("PUT /v1/hack/events/{slug}/website/storage/{rest...}", wrap(h.eventWebsiteStorage))
	mux.Handle("POST /v1/hack/events/{slug}/website/storage/{rest...}", wrap(h.eventWebsiteStorage))
	mux.Handle("PATCH /v1/hack/events/{slug}/website/storage/{rest...}", wrap(h.eventWebsiteStorage))
	mux.Handle("DELETE /v1/hack/events/{slug}/website/storage/{rest...}", wrap(h.eventWebsiteStorage))
}

func (h *HackHandler) eventWebsiteStorage(w http.ResponseWriter, r *http.Request) {
	rest := r.PathValue("rest")
	readOnly := r.Method == http.MethodGet || (r.Method == http.MethodPost &&
		(strings.HasSuffix(rest, "/query") || strings.HasSuffix(rest, "/download-link")))
	a, ok := h.loadMember(w, r, r.PathValue("slug"), !readOnly, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" && !readOnly {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	storage, ok := h.sites.(eventWebsiteStorage)
	if !ok {
		httpx.WriteError(w, http.StatusServiceUnavailable, "website_unavailable", "event website storage is unavailable")
		return
	}
	if strings.TrimSpace(r.PathValue("rest")) == "" {
		httpx.WriteError(w, http.StatusNotFound, "not_found", "storage route not found")
		return
	}
	storage.ServeEventWebsiteStorage(w, r, groupRef(a.event), a.user.ID)
}

func (h *HackHandler) eventWebsiteState(ctx context.Context, ev model.Event) (map[string]any, error) {
	site, err := h.eventsSvc().SiteByUser(ctx, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false, "upload_limit_bytes": h.websiteUploadLimit(ctx, ev), "upload_limits": h.websiteUploadLimits(ctx, ev), "versions": []any{},
			"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug}, nil
	}
	if err != nil {
		return nil, err
	}
	versions, err := h.eventsSvc().SiteVersions(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	history := []map[string]any{}
	for _, v := range versions {
		if v.Status == "active" {
			history = append(history, map[string]any{"version_number": v.VersionNumber, "created_at": v.CreatedAt})
		}
	}
	return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": true, "upload_limit_bytes": h.websiteUploadLimit(ctx, ev), "upload_limits": h.websiteUploadLimits(ctx, ev), "versions": history,
		"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug,
		"active_version": site.ActiveVersion}, nil
}

func (h *HackHandler) getEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	state, err := h.eventWebsiteState(r.Context(), a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

func (h *HackHandler) patchEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Mode != "builtin" && req.Mode != "custom" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "mode must be builtin or custom")
		return
	}
	if req.Mode == "custom" {
		if _, err := h.eventsSvc().SiteByUser(r.Context(), a.event.AccountID, a.event.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteError(w, http.StatusConflict, "website_not_published", "publish an event website first")
			} else {
				httpx.WriteInternal(w)
			}
			return
		}
	}
	ev, err := h.eventsSvc().SetEventWebsiteModeLookup(r.Context(), a.event.ID, a.user.ID, req.Mode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	state, err := h.eventWebsiteState(r.Context(), ev)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

func (h *HackHandler) putEventWebsiteFiles(w http.ResponseWriter, r *http.Request) {
	h.putEventWebsite(w, r, true)
}

func (h *HackHandler) putEventWebsiteArchive(w http.ResponseWriter, r *http.Request) {
	h.putEventWebsite(w, r, false)
}

func (h *HackHandler) putEventWebsite(w http.ResponseWriter, r *http.Request, files bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	sites, ok := h.sites.(eventWebsitePublisher)
	if !ok {
		httpx.WriteError(w, http.StatusServiceUnavailable, "website_unavailable", "event website publishing is unavailable")
		return
	}
	// Buffer the small deployment response until the event host's switch has
	// committed. Visitors never see an old mode after a successful upload.
	rec := httptest.NewRecorder()
	sites.PublishEventWebsite(rec, r, groupRef(a.event), a.user.ID, files)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated {
		if _, err := h.eventsSvc().SetEventWebsiteModeLookup(r.Context(), a.event.ID, a.user.ID, "custom"); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeEventNotFound(w)
				return
			}
			httpx.WriteInternal(w)
			return
		}
	}
	for k, values := range res.Header {
		for _, value := range values {
			w.Header().Add(k, value)
		}
	}
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
}

func (h *HackHandler) websiteSitesSvc() *appsites.Service {
	return appsites.New(h.database, nil, nil, 0, appsites.Deps{
		WebsiteArchiveDefault: h.siteArchiveDefault,
		WebsiteLimit: func(ctx context.Context, user *model.User) (int64, bool) {
			return h.siteLimitFor(ctx, h.database, user)
		},
		MaxFilesPerSite: h.limits.MaxFilesPerSite,
	})
}

func (h *HackHandler) websiteUploadLimit(ctx context.Context, ev model.Event) int64 {
	return h.websiteSitesSvc().WebsiteUploadLimit(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

func (h *HackHandler) websiteUploadLimits(ctx context.Context, ev model.Event) string {
	return h.websiteSitesSvc().WebsiteUploadLimits(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

func groupRef(ev model.Event) handler.GroupRef {
	return handler.GroupRef{ID: ev.ID, Slug: ev.Slug, AccountID: ev.AccountID}
}
