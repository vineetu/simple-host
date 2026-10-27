package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/tarball"
)

type eventWebsitePublisher interface {
	PublishEventWebsite(http.ResponseWriter, *http.Request, db.Event, string, bool)
}

type eventWebsiteStorage interface {
	ServeEventWebsiteStorage(http.ResponseWriter, *http.Request, db.Event, string)
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
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	storage, ok := h.sites.(eventWebsiteStorage)
	if !ok {
		writeHackErr(w, http.StatusServiceUnavailable, "website_unavailable", "event website storage is unavailable")
		return
	}
	if strings.TrimSpace(r.PathValue("rest")) == "" {
		writeHackErr(w, http.StatusNotFound, "not_found", "storage route not found")
		return
	}
	storage.ServeEventWebsiteStorage(w, r, a.event, a.user.ID)
}

func (h *HackHandler) eventWebsiteState(ctx context.Context, ev db.Event) (map[string]any, error) {
	site, err := db.GetSiteByUser(ctx, h.database, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false, "upload_limit_bytes": h.websiteUploadLimit(ctx, ev), "upload_limits": h.websiteUploadLimits(ctx, ev), "versions": []any{},
			"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug}, nil
	}
	if err != nil {
		return nil, err
	}
	versions, err := db.ListVersionsBySite(ctx, h.database, site.ID)
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
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (h *HackHandler) patchEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Mode != "builtin" && req.Mode != "custom" {
		writeHackErr(w, http.StatusBadRequest, "invalid_request", "mode must be builtin or custom")
		return
	}
	if req.Mode == "custom" {
		if _, err := db.GetSiteByUser(r.Context(), h.database, a.event.AccountID, a.event.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeHackErr(w, http.StatusConflict, "website_not_published", "publish an event website first")
			} else {
				writeInternal(w)
			}
			return
		}
	}
	ev, err := db.SetEventWebsiteMode(r.Context(), h.database, a.event.ID, a.user.ID, req.Mode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		writeInternal(w)
		return
	}
	state, err := h.eventWebsiteState(r.Context(), ev)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, state)
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
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	sites, ok := h.sites.(eventWebsitePublisher)
	if !ok {
		writeHackErr(w, http.StatusServiceUnavailable, "website_unavailable", "event website publishing is unavailable")
		return
	}
	// Buffer the small deployment response until the event host's switch has
	// committed. Visitors never see an old mode after a successful upload.
	rec := httptest.NewRecorder()
	sites.PublishEventWebsite(rec, r, a.event, a.user.ID, files)
	res := rec.Result()
	defer res.Body.Close()
	if res.StatusCode == http.StatusOK || res.StatusCode == http.StatusCreated {
		if _, err := db.SetEventWebsiteMode(r.Context(), h.database, a.event.ID, a.user.ID, "custom"); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeEventNotFound(w)
				return
			}
			writeInternal(w)
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

func (h *HackHandler) websiteUploadLimit(ctx context.Context, ev db.Event) int64 {
	holder, err := db.GetUserByID(ctx, h.database, ev.AccountID)
	if err != nil {
		return maxSiteArchiveSize
	}
	limit, _ := siteLimitFor(ctx, h.database, &holder)
	return limit
}

func (h *HackHandler) websiteUploadLimits(ctx context.Context, ev db.Event) string {
	holder, err := db.GetUserByID(ctx, h.database, ev.AccountID)
	if err != nil {
		return "Upload limits are unavailable."
	}
	limit, override := siteLimitFor(ctx, h.database, &holder)
	total, file, entries := tarball.FileLimits(extractBudget(limit, override))
	return fmt.Sprintf("Maximum zip: %d MB; unpacked: %d MB total, %d MB per file, %d files.", limit>>20, total>>20, file>>20, entries)
}
