package hackhttp

import (
	"context"
	"database/sql"
	"github.com/vsriram/simple-host/internal/httpx"
	rulesauth "github.com/vsriram/simple-host/internal/rules/auth"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/lib/pq"

	"github.com/vsriram/simple-host/internal/db"
)

// Guided publishing, for the Website screen's state strip and polling (build
// plan §4.4, slice 3). GET only: the caller's own team, never leaked to
// anyone else (never client ids, grant ids, devices or tokens -- security
// point 4).

// hackKeyMaxAge is how long a team key's own last use still counts as
// "using a key" on the Website screen, matching the build plan's 30 days.
const hackKeyMaxAge = 30 * 24 * time.Hour

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
func (h *HackHandler) siteStatusJSON(ctx context.Context, ev db.Event, team db.EventTeam) map[string]any {
	var site *db.Site
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
	if st, err := db.TeamWriteStateFor(ctx, h.database, team.ID, false); err == nil {
		if st.PinnedVersion.Valid {
			out["pinned_version"] = st.PinnedVersion.Int64
		}
		if st.PinnedAt.Valid {
			out["pinned_at"] = rfc3339Time(st.PinnedAt.Time)
		}
	}
	if site != nil {
		if v, err := db.GetActiveSiteVersion(ctx, h.database, site.ID); err == nil {
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

// connGrant is one person's latest personal connector connection for the
// Simple Hack MCP resource (scope "sites events"; a legacy team-bound grant,
// scope "sites team:<uuid>", carries no selected_team_id and is not
// "matching" here -- it never appears in this map).
type connGrant struct {
	SelectedTeamID sql.NullString
	LastUsedAt     time.Time
	AppName        string
}

// latestEventsGrantsByUser returns, for each of userIDs that has one, their
// single most-recently-used personal connector grant for resource (an
// "events" grant: the kind that carries selected_team_id). Rows are read
// newest-last-used first and the first matching row per user wins, so a
// user's older events-scope grant is still found even if their very latest
// grant is a stale legacy "team:" one.
func latestEventsGrantsByUser(ctx context.Context, q *sql.DB, userIDs []string, resource string) (map[string]connGrant, error) {
	out := map[string]connGrant{}
	if len(userIDs) == 0 {
		return out, nil
	}
	rows, err := q.QueryContext(ctx, `
		SELECT g.user_id, g.scope, g.selected_team_id, g.last_used_at, c.client_name
		  FROM oauth_grants g JOIN oauth_clients c ON c.client_id = g.client_id
		 WHERE g.user_id = ANY($1::uuid[]) AND g.resource = $2
		 ORDER BY g.last_used_at DESC`, pq.Array(userIDs), resource)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var userID, scope, clientName string
		var selectedTeamID sql.NullString
		var lastUsedAt time.Time
		if err := rows.Scan(&userID, &scope, &selectedTeamID, &lastUsedAt, &clientName); err != nil {
			return nil, err
		}
		if seen[userID] {
			continue
		}
		if _, events, ok := rulesauth.GrantKind(scope); !ok || !events {
			continue
		}
		seen[userID] = true
		out[userID] = connGrant{SelectedTeamID: selectedTeamID, LastUsedAt: lastUsedAt, AppName: httpx.CleanClientName(clientName)}
	}
	return out, rows.Err()
}

// otherTeamDisplay is the event title and team name of a grant's selected
// team, for "Connected in Claude, but it's set to <other team>". false when
// the team or its event no longer exists.
func otherTeamDisplay(ctx context.Context, database *sql.DB, teamID string) (eventTitle, teamName string, ok bool) {
	err := database.QueryRowContext(ctx, `
		SELECT e.title, t.name FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, teamID).
		Scan(&eventTitle, &teamName)
	return eventTitle, teamName, err == nil
}

// connectionStatusJSON is the Website screen's "AI app" state: the caller's
// own connector or key status, plus which other CURRENT teammates have this
// team selected (names only -- never their app, client id or grant id;
// security point 4). A member who left the team is not in people, so their
// old grant is never counted even if it is still pointed at this team.
func (h *HackHandler) connectionStatusJSON(ctx context.Context, eventID string, team db.EventTeam, callerID string) (map[string]any, error) {
	people, err := db.ListTeamPeople(ctx, h.database, team.ID)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(people))
	for _, p := range people {
		if p.Role == "participant" && p.ApprovalStatus == "approved" {
			ids = append(ids, p.UserID)
		}
	}
	resource := h.publicBaseURL + "/mcp"
	latest, err := latestEventsGrantsByUser(ctx, h.database, ids, resource)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"state": "none", "app": nil, "last_used_at": nil, "other_team": nil, "teammates_connected": []string{},
	}
	var teammates []string
	callerSeen := false
	for _, p := range people {
		if p.Role != "participant" || p.ApprovalStatus != "approved" {
			continue
		}
		g, ok := latest[p.UserID]
		if p.UserID == callerID {
			callerSeen = true
			if !ok || !g.SelectedTeamID.Valid {
				continue
			}
			out["last_used_at"] = rfc3339Time(g.LastUsedAt)
			out["app"] = g.AppName
			if g.SelectedTeamID.String == team.ID {
				out["state"] = "connected"
			} else {
				out["state"] = "other_team"
				if evTitle, teamName, ok2 := otherTeamDisplay(ctx, h.database, g.SelectedTeamID.String); ok2 {
					out["other_team"] = map[string]any{"event": evTitle, "team": teamName}
				}
			}
			continue
		}
		if ok && g.SelectedTeamID.Valid && g.SelectedTeamID.String == team.ID {
			teammates = append(teammates, p.DisplayName)
		}
	}
	_ = callerSeen // the caller is always in people (participantTeam already proved membership)
	if out["state"] == "none" {
		if k, err := db.GetTeamKey(ctx, h.database, eventID, callerID); err == nil && k.TeamID == team.ID && k.LastUsedAt != nil {
			if time.Since(*k.LastUsedAt) <= hackKeyMaxAge {
				out["state"] = "key"
				out["last_used_at"] = rfc3339Time(*k.LastUsedAt)
			}
		}
	}
	if teammates == nil {
		teammates = []string{}
	}
	out["teammates_connected"] = teammates
	return out, nil
}
