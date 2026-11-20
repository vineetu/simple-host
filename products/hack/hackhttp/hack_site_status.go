package hackhttp

import (
	"context"
	"time"

	accountapp "github.com/vsriram/simple-host/host/access"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// Guided publishing, for the Website screen's state strip and polling (build
// plan §4.4, slice 3). GET only: the caller's own team. The member publishes
// with their own sign-in, naming owner (the event) on the site routes.

func (h *participationHTTP) getTeamSiteStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	ctx := r.Context()
	conn, err := h.connectionStatusJSON(ctx, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":               h.siteStatusJSON(ctx, a.event, team),
		"connection":         conn,
		"team":               map[string]any{"slug": team.Slug, "name": team.Name},
		"owner":              a.event.Slug,
		"api_base":           h.publicBaseURL,
		"mcp_url":            h.publicBaseURL + "/mcp",
		"upload_limit_bytes": h.websiteUploadLimit(ctx, a.event),
		"upload_limits":      h.websiteUploadLimits(ctx, a.event),
	})
}

// siteStatusJSON is the Website screen's richer site object: the existing
// /key fields plus when and how the live version went live, and when the
// deadline pin was taken.
func (h *participationHTTP) siteStatusJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) map[string]any {
	var site *model.Site
	if h.sites != nil {
		if info, st, err := h.sites.TeamSiteInfo(ctx, ev.AccountID, team.Slug); err != nil {
			log.Printf("hack: site status %s/%s: %v", ev.Slug, team.Slug, err)
		} else if info.Exists {
			site = &st
		}
	}
	out := h.teamSiteJSONFrom(ev, team, site, h.teamSitesReady(ev.Slug))
	out["published_at"] = nil
	out["published_via"] = nil
	out["pinned_version"] = nil
	out["pinned_at"] = nil
	if st, err := h.teams.WriteState(ctx, team.ID); err == nil {
		if st.PinnedVersion.Valid {
			out["pinned_version"] = st.PinnedVersion.Int64
		}
		if st.PinnedAt.Valid {
			out["pinned_at"] = rfc3339Time(st.PinnedAt.Time)
		}
	}
	if site != nil {
		if v, err := h.teams.ActiveSiteVersion(ctx, site.ID); err == nil {
			out["published_at"] = rfc3339Time(v.CreatedAt)
			// published_via: whatever the live deploy's ?author= recorded
			// (SIM-152), passed through as-is. Most deploys never set it, so
			// this is usually null -- the build plan says not to add a
			// column just to guess it, so this is a straight read, no
			// mapping to a fixed "Claude"/"zip"/"key" enum.
			if author := strings.TrimSpace(v.Author); author != "" {
				out["published_via"] = author
			}
		}
	}
	return out
}

// connectionStatusJSON is the Website screen's "AI app" state: whether the
// caller has connected an app (names only, never client ids, grants or
// devices). A connected app publishes the team's site as the person, naming
// the event as owner.
func (h *participationHTTP) connectionStatusJSON(ctx context.Context, userID string) (map[string]any, error) {
	conns, err := accountapp.ConnectedApps(ctx, h.database, userID)
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
		out["state"], out["app"], out["apps"], out["last_used_at"] = "connected", apps[0], apps, rfc3339Time(last)
	}
	return out, nil
}
