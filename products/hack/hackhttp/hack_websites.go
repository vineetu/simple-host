package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/host/httpx"
	appsites "github.com/vsriram/simple-host/host/sites"
	"github.com/vsriram/simple-host/host/web"
	productconfig "github.com/vsriram/simple-host/products/hack/config"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *publicEventHTTP) eventWebsiteState(ctx context.Context, ev hackmodel.Event) (map[string]any, error) {
	site, err := h.events.SiteByUser(ctx, ev.AccountID, ev.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": false, "upload_limit_bytes": h.website.uploadLimit(ctx, ev), "upload_limits": h.website.uploadLimits(ctx, ev), "versions": []any{},
			"custom_url": h.links.EventURL(ev.Slug), "builtin_url": h.links.publicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug}, nil
	}
	if err != nil {
		return nil, err
	}
	versions, err := h.events.SiteVersions(ctx, site.ID)
	if err != nil {
		return nil, err
	}
	history := []map[string]any{}
	for _, v := range versions {
		if v.Status == "active" {
			history = append(history, map[string]any{"version_number": v.VersionNumber, "created_at": v.CreatedAt})
		}
	}
	return map[string]any{"mode": ev.WebsiteMode, "has_custom_page": true, "upload_limit_bytes": h.website.uploadLimit(ctx, ev), "upload_limits": h.website.uploadLimits(ctx, ev), "versions": history,
		"custom_url": h.links.EventURL(ev.Slug), "builtin_url": h.links.publicBaseURL + "/e/" + ev.Slug, "site": ev.ID, "owner": ev.Slug,
		"active_version": site.ActiveVersion}, nil
}

func (h *publicEventHTTP) getEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
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

func (h *publicEventHTTP) patchEventWebsite(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
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
		if _, err := h.events.SiteByUser(r.Context(), a.event.AccountID, a.event.ID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				httpx.WriteError(w, http.StatusConflict, "website_not_published", "publish an event website first")
			} else {
				httpx.WriteInternal(w)
			}
			return
		}
	}
	ev, err := h.events.SetEventWebsiteModeLookup(r.Context(), a.event.ID, a.user.ID, req.Mode)
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

// websiteLimits are the instance's site size limits, as an event or team
// website's upload form states them.
type websiteLimits struct {
	database *sql.DB
	limits   *productconfig.Limits
	// archiveDefault is the per-site size cap for an account with no
	// MAX_ARCHIVE_MB_OVERRIDES entry (SetSiteArchiveDefault).
	archiveDefault int64
}

func (l *websiteLimits) sites() *appsites.Service {
	return appsites.New(l.database, nil, nil, 0, appsites.Deps{
		WebsiteArchiveDefault: l.archiveDefault,
		Limits:                l.limits.Limits,
		MaxFilesPerSite:       l.limits.MaxFilesPerSite,
	})
}

// uploadLimit is the largest zip the event's websites take, in bytes.
func (l *websiteLimits) uploadLimit(ctx context.Context, ev hackmodel.Event) int64 {
	return l.sites().WebsiteUploadLimit(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

// uploadLimits is the upload limits in words.
func (l *websiteLimits) uploadLimits(ctx context.Context, ev hackmodel.Event) string {
	return l.sites().WebsiteUploadLimits(ctx, appsites.WebsiteLimitsInput{AccountID: ev.AccountID})
}

func groupRef(ev hackmodel.Event) web.GroupRef {
	return web.GroupRef{ID: ev.ID, Slug: ev.Slug, AccountID: ev.AccountID}
}
