package hackhttp

import (
	"net/http"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// getTeamLink is the public GET /v1/hack/team-links/{code}: a team link or
// code previewed before joining, signed in or out. Same rate limiter as
// GET /v1/hack/join/{code}; never writes, never joins on GET (a link
// preview or an unfurl must not act). No member names, no emails -- only
// the team's own name and counts, and the caller's own status.
func (h *participationHTTP) getTeamLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, caller := auth.GetUser(ctx), ""
	if user != nil {
		caller = user.ID
	}
	if h.rateLimited(w, r, caller) {
		return
	}
	code := h.teamCodeFromInput(r.PathValue("code"))
	if code == "" {
		httpx.WriteError(w, http.StatusNotFound, "team_link_not_found", "that link or code doesn't match a team in this event")
		return
	}
	preview, err := h.teams.GetTeamLink(ctx, code, caller)
	if err != nil {
		writeHackError(w, err)
		return
	}
	ev, team, memberCount := preview.Event, preview.Team, preview.MemberCount
	var reason any
	switch {
	case team.Locked():
		reason = "locked"
	case memberCount >= ev.TeamSizeMax:
		reason = "full"
	case (ev.Stage != "open" && ev.Stage != "building") || preview.DeadlinePassed:
		reason = "teams_closed"
	}
	var track any
	if preview.Track != nil {
		track = map[string]any{"name": preview.Track.Name}
	}
	var you map[string]any
	if user != nil {
		you = map[string]any{"status": preview.YouStatus}
		if preview.HasOtherTeamName {
			you["team_name"] = preview.OtherTeamName
		}
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
			"applications_open": events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()),
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
	httpx.WriteJSON(w, http.StatusOK, body)
}
