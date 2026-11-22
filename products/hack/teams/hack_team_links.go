package teams

import (
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// getTeamLink is the public GET /v1/hack/team-links/{code}: a preview that never joins, with no names or emails.
func (h *handler) getTeamLink(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	user, caller := auth.GetUser(ctx), ""
	if user != nil {
		caller = user.ID
	}
	if h.Codes.Limited(w, r, caller) {
		return
	}
	preview, err := h.Teams.GetTeamLink(ctx, h.Links.TeamCodeFromInput(r.PathValue("code")), caller)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	ev, team, memberCount := preview.Event, preview.Team, preview.MemberCount
	reason := preview.JoinRefusal()
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
			"url":   h.Links.EventURL(ev.Slug),
			"stage": ev.Stage,
			// Matches the public application API: the organiser's switch alone isn't enough, since an unlisted or closed-stage event never actually takes an application either.
			"applications_open": events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()),
		},
		"team": map[string]any{
			"name":          team.Name,
			"member_count":  memberCount,
			"team_size_max": ev.TeamSizeMax,
			"track":         track,
		},
		"joinable": reason == "",
		"reason":   core.OrNil(reason),
		"you":      you,
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}
