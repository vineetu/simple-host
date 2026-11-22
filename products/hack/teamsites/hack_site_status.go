package teamsites

import (
	"context"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"log"
	"net/http"
	"strings"

	accountapp "github.com/vsriram/simple-host/host/access"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// Guided publishing, for the Website screen's state strip and polling. GET only: the caller's own team. The member publishes with their own sign-in, naming owner (the event) on the site routes.

func (h *handler) getTeamSiteStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := core.ParticipantTeam(w, r, h.Teams, a)
	if !ok {
		return
	}
	ctx := r.Context()
	conn, err := h.connectionStatusJSON(ctx, a.User.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":               h.siteStatusJSON(ctx, a.Event, team),
		"connection":         conn,
		"team":               map[string]any{"slug": team.Slug, "name": team.Name},
		"owner":              a.Event.Slug,
		"api_base":           h.Links.PublicBaseURL,
		"mcp_url":            h.Links.PublicBaseURL + "/mcp",
		"upload_limit_bytes": h.Website.UploadLimit(ctx, a.Event),
		"upload_limits":      h.Website.UploadLimits(ctx, a.Event),
	})
}

// siteStatusJSON is the Website screen's richer site object: the existing
// /key fields plus when and how the live version went live, and when the
// deadline pin was taken.
func (h *handler) siteStatusJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) map[string]any {
	var site *model.Site
	if h.Links.Hooks != nil {
		if info, st, err := h.Links.Hooks.TeamSiteInfo(ctx, ev.AccountID, team.Slug); err != nil {
			log.Printf("hack: site status %s/%s: %v", ev.Slug, team.Slug, err)
		} else if info.Exists {
			site = &st
		}
	}
	out := h.Links.TeamSiteJSONFrom(ev, team, site, h.Links.TeamSitesReady(ev.Slug))
	out["published_at"] = nil
	out["published_via"] = nil
	out["pinned_version"] = nil
	out["pinned_at"] = nil
	if st, err := h.Teams.WriteState(ctx, team.EventID, team.ID); err == nil {
		if st.PinnedVersion.Valid {
			out["pinned_version"] = st.PinnedVersion.Int64
		}
		if st.PinnedAt.Valid {
			out["pinned_at"] = core.RFC3339Time(st.PinnedAt.Time)
		}
	}
	if site != nil {
		if v, err := h.Teams.ActiveSiteVersion(ctx, site.ID); err == nil {
			out["published_at"] = core.RFC3339Time(v.CreatedAt)
			// published_via: whatever the live deploy's ?author= recorded, passed through as-is.
			if author := strings.TrimSpace(v.Author); author != "" {
				out["published_via"] = author
			}
		}
	}
	return out
}

// connectionStatusJSON is the Website screen's "AI app" state: whether the
// caller has connected an app (names only, never client ids, grants or
// devices).
func (h *handler) connectionStatusJSON(ctx context.Context, userID string) (map[string]any, error) {
	conns, err := accountapp.ConnectedApps(ctx, h.DB, userID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": "none", "app": nil, "apps": []string{}, "last_used_at": nil, "teammates_connected": []string{}}
	apps := []string{}
	var last time.Time
	for _, c := range conns {
		apps = append(apps, httpx.CleanClientName(c.ClientName))
		if c.LastUsedAt.After(last) {
			last = c.LastUsedAt
		}
	}
	if len(apps) > 0 {
		out["state"], out["app"], out["apps"], out["last_used_at"] = "connected", apps[0], apps, core.RFC3339Time(last)
	}
	return out, nil
}
