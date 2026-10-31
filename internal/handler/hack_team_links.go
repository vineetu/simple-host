package handler

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/vsriram/simple-host/internal/db"
)

// getTeamLink is the public GET /v1/hack/team-links/{code}: a team link or
// code previewed before joining, signed in or out. Same rate limiter as
// GET /v1/hack/join/{code}; never writes, never joins on GET (a link
// preview or an unfurl must not act). No member names, no emails -- only
// the team's own name and counts, and the caller's own status.
func (h *HackHandler) getTeamLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var user *db.User
	caller := ""
	if k := r.Header.Get("X-API-Key"); k != "" {
		if u, err := db.GetUserByAPIKey(ctx, h.database, k); err == nil {
			user = &u
			caller = u.ID
		}
	}
	if h.rateLimited(w, r, caller) {
		return
	}
	code := h.teamCodeFromInput(r.PathValue("code"))
	if code == "" {
		writeHackErr(w, http.StatusNotFound, "team_link_not_found", "that link or code doesn't match a team in this event")
		return
	}
	team, err := db.GetEventTeamByCodeOnly(ctx, h.database, code)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_link_not_found", "that link or code doesn't match a team in this event")
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	ev, err := db.GetEventByID(ctx, h.database, team.EventID)
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeHackErr(w, http.StatusNotFound, "team_link_not_found", "that link or code doesn't match a team in this event")
		return
	}
	memberCount, err := db.CountTeamMembers(ctx, h.database, team.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	var deadlinePassed bool
	if err := h.database.QueryRowContext(ctx, `
		SELECT COALESCE(`+db.EffectiveDeadlineSQL+` <= clock_timestamp(), false)
		  FROM event_teams t JOIN events e ON e.id = t.event_id WHERE t.id = $1`, team.ID).Scan(&deadlinePassed); err != nil {
		writeInternal(w)
		return
	}
	var reason any
	switch {
	case team.Locked():
		reason = "locked"
	case memberCount >= ev.TeamSizeMax:
		reason = "full"
	case (ev.Stage != "open" && ev.Stage != "building") || deadlinePassed:
		reason = "teams_closed"
	}
	var track any
	if t, err := db.TeamTrack(ctx, h.database, team.ID); err == nil && t != nil {
		track = map[string]any{"name": t.Name}
	}
	you, err := h.teamLinkYouJSON(ctx, user, ev, team)
	if err != nil {
		writeInternal(w)
		return
	}
	body := map[string]any{
		"event": map[string]any{
			"slug":  ev.Slug,
			"title": ev.Title,
			"url":   h.EventURL(ev.Slug),
			"stage": ev.Stage,
			// Matches the public application API: the organiser's switch
			// alone isn't enough, since an unlisted or closed-stage event
			// never actually takes an application either (Codex review,
			// 2026-11-01; was the raw ApplicationsOpen column).
			"applications_open": hackApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()),
		},
		"team": map[string]any{
			"name":          team.Name,
			"member_count":  memberCount,
			"team_size_max": ev.TeamSizeMax,
			"track":         track,
		},
		"joinable": reason == nil,
		"reason":   reason,
		"you":      you,
	}
	writeJSON(w, http.StatusOK, body)
}

// teamLinkYouJSON is nil signed out, else the caller's status relative to
// this team: not_in_event, pending, rejected, judge, organiser, no_team,
// on_this_team or on_other_team (+ team_name, the other team's own name,
// never a member's). A real database error is returned to the caller
// instead of being folded into "not_in_event": that status means "this
// account genuinely has no row in this event," and a lookup failure proves
// no such thing -- silently downgrading the error used to return a
// misleading 200 (Grok security review, 2026-11-01).
func (h *HackHandler) teamLinkYouJSON(ctx context.Context, user *db.User, ev db.Event, team db.EventTeam) (map[string]any, error) {
	if user == nil {
		return nil, nil
	}
	member, err := db.GetEventMember(ctx, h.database, ev.ID, user.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{"status": "not_in_event"}, nil
	}
	if err != nil {
		return nil, err
	}
	switch member.Role {
	case "organiser":
		return map[string]any{"status": "organiser"}, nil
	case "judge":
		return map[string]any{"status": "judge"}, nil
	}
	status, err := db.MemberApprovalStatus(ctx, h.database, ev.ID, user.ID)
	if err != nil {
		return nil, err
	}
	if status == "pending" || status == "rejected" {
		return map[string]any{"status": status}, nil
	}
	if !member.TeamID.Valid {
		return map[string]any{"status": "no_team"}, nil
	}
	if member.TeamID.String == team.ID {
		return map[string]any{"status": "on_this_team"}, nil
	}
	other, err := db.GetEventTeamByID(ctx, h.database, member.TeamID.String)
	if err != nil {
		return map[string]any{"status": "on_other_team"}, nil
	}
	return map[string]any{"status": "on_other_team", "team_name": other.Name}, nil
}
