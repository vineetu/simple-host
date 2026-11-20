package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	appsites "github.com/vsriram/simple-host/host/sites"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *HackHandler) registerEventWebsite(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("GET /v1/hack/events/{slug}/website", wrap(h.getEventWebsite))
	mux.Handle("PATCH /v1/hack/events/{slug}/website", wrap(h.patchEventWebsite))
}

func (h *HackHandler) eventWebsiteState(ctx context.Context, ev hackmodel.Event) (map[string]any, error) {
	site, err := h.eventsSvc().SiteByUser(ctx, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false, "upload_limit_bytes": h.websiteUploadLimit(ctx, ev), "upload_limits": h.websiteUploadLimits(ctx, ev), "versions": []any{},
			"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug}, nil
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
		"custom_url": h.EventURL(ev.Slug), "builtin_url": h.publicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug,
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
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
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

func (h *HackHandler) websiteSitesSvc() *appsites.Service {
	return appsites.New(h.database, nil, nil, 0, appsites.Deps{
		WebsiteArchiveDefault: h.siteArchiveDefault,
		WebsiteLimit: func(ctx context.Context, user *model.User) (int64, bool) {
			return h.siteLimitFor(ctx, h.database, user)
		},
		MaxFilesPerSite: h.limits.MaxFilesPerSite,
	})
}

func (h *HackHandler) websiteUploadLimit(ctx context.Context, ev hackmodel.Event) int64 {
	return h.websiteSitesSvc().WebsiteUploadLimit(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

func (h *HackHandler) websiteUploadLimits(ctx context.Context, ev hackmodel.Event) string {
	return h.websiteSitesSvc().WebsiteUploadLimits(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

func groupRef(ev hackmodel.Event) handler.GroupRef {
	return handler.GroupRef{ID: ev.ID, Slug: ev.Slug, AccountID: ev.AccountID}
}
