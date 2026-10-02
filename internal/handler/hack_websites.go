package handler

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/vsriram/simple-host/internal/db"
)

type eventWebsitePublisher interface {
	PublishEventWebsite(http.ResponseWriter, *http.Request, db.Event, string, bool)
}

func (h *HackHandler) registerEventWebsite(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/website", wrap(h.getEventWebsite))
	mux.Handle("PATCH /v1/hack/events/{slug}/website", wrap(h.patchEventWebsite))
	mux.Handle("PUT /v1/hack/events/{slug}/website", wrap(h.putEventWebsiteArchive))
	mux.Handle("PUT /v1/hack/events/{slug}/website/files", wrap(h.putEventWebsiteFiles))
}

func (h *HackHandler) eventWebsiteState(ctx context.Context, ev db.Event) (map[string]any, error) {
	site, err := db.GetSiteByUser(ctx, h.database, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false,
			"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": true,
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
