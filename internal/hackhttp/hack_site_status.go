package hackhttp

import (
	"context"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
)

// Guided publishing, for the Website screen's state strip and polling (build
// plan §4.4, slice 3). GET only: the caller's own team, never leaked to
// anyone else (never client ids, grant ids, devices or tokens -- security
// point 4).

func (h *HackHandler) getTeamSiteStatus(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	ctx := r.Context()
	conn, err := h.connectionStatusJSON(ctx, a.event.ID, team, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"site":       h.siteStatusJSON(ctx, a.event, team),
		"connection": conn,
	})
}

// siteStatusJSON is the Website screen's richer site object: the existing
// /key fields plus when and how the live version went live, and when the
// deadline pin was taken.
func (h *HackHandler) siteStatusJSON(ctx context.Context, ev model.Event, team model.EventTeam) map[string]any {
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
	if st, err := h.teamsSvc().WriteState(ctx, team.ID); err == nil {
		if st.PinnedVersion.Valid {
			out["pinned_version"] = st.PinnedVersion.Int64
		}
		if st.PinnedAt.Valid {
			out["pinned_at"] = rfc3339Time(st.PinnedAt.Time)
		}
	}
	if site != nil {
		if v, err := h.teamsSvc().ActiveSiteVersion(ctx, site.ID); err == nil {
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

// connectionStatusJSON is the Website screen's "AI app" state: the caller's
// own connector or key status, plus which other CURRENT teammates have this
// team selected (names only -- never their app, client id or grant id;
// security point 4). A member who left the team is not in people, so their
// old grant is never counted even if it is still pointed at this team.
func (h *HackHandler) connectionStatusJSON(ctx context.Context, eventID string, team model.EventTeam, callerID string) (map[string]any, error) {
	status, err := h.teamsSvc().ConnectionStatus(ctx, eventID, team.ID, callerID, h.publicBaseURL+"/mcp")
	if err != nil {
		return nil, err
	}
	out := map[string]any{"state": status.State, "app": nil, "apps": []string{}, "last_used_at": nil, "other_team": nil, "teammates_connected": status.Teammates}
	if status.State == "connected" || status.State == "other_team" || status.State == "no_team" {
		apps := []string{}
		seen := map[string]bool{}
		for _, name := range status.Apps {
			name = httpx.CleanClientName(name)
			if !seen[name] {
				apps = append(apps, name)
				seen[name] = true
			}
		}
		out["apps"] = apps
		if len(apps) > 0 {
			out["app"] = apps[0]
		}
	}
	if status.LastUsedAt != nil {
		out["last_used_at"] = rfc3339Time(*status.LastUsedAt)
	}
	if status.OtherTeam != nil {
		out["other_team"] = map[string]any{"event": status.OtherTeam.Event, "team": status.OtherTeam.Team}
	}
	return out, nil
}
