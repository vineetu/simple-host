package teamsites

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) eventWebsiteState(ctx context.Context, ev hackmodel.Event) (map[string]any, error) {
	site, err := h.Events.SiteByUser(ctx, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false, "upload_limit_bytes": h.Website.UploadLimit(ctx, ev), "upload_limits": h.Website.UploadLimits(ctx, ev), "versions": []any{},
			"custom_url": h.Links.EventURL(ev.Slug), "builtin_url": h.Links.PublicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug}, nil
	}
	if err != nil {
		return nil, err
	}
	versions, err := h.Events.SiteVersions(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	history := []map[string]any{}
	for _, v := range versions {
		if v.Status == "active" {
			history = append(history, map[string]any{"version_number": v.VersionNumber, "created_at": v.CreatedAt})
		}
	}
	return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": true, "upload_limit_bytes": h.Website.UploadLimit(ctx, ev), "upload_limits": h.Website.UploadLimits(ctx, ev), "versions": history,
		"custom_url": h.Links.EventURL(ev.Slug), "builtin_url": h.Links.PublicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug,
		"active_version": site.ActiveVersion}, nil
}

func (h *handler) getEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	state, err := h.eventWebsiteState(r.Context(), a.Event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, state)
}

func (h *handler) patchEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.Event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	var req struct {
		Mode string `json:"mode"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Mode != "builtin" && req.Mode != "custom" {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_request", "mode must be builtin or custom")
		return
	}
	if req.Mode == "custom" {
		if _, err := h.Events.SiteByUser(r.Context(), a.Event.AccountID, a.Event.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteError(w, http.StatusConflict, "website_not_published", "publish an event website first")
			} else {
				httpx.WriteInternal(w)
			}
			return
		}
	}
	ev, err := h.Events.SetEventWebsiteModeLookup(r.Context(), a.Event.ID, a.User.ID, req.Mode)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			core.WriteEventNotFound(w)
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
