package teamsites

import (
	"context"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func (h *handler) organiserTeam(w http.ResponseWriter, r *http.Request) (core.Access, hackmodel.EventTeam, bool) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return a, hackmodel.EventTeam{}, false
	}
	team, err := h.Teams.OrganiserTeam(r.Context(), a.Event, r.PathValue("team"))
	if err != nil {
		core.WriteError(w, err)
		return a, team, false
	}
	return a, team, true
}

func (h *handler) teamOrganiserFull(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) (map[string]any, error) {
	tl, err := h.Links.LoadTeamList(ctx, ev)
	if err != nil {
		return nil, err
	}
	if fresh, err := h.Teams.GetTeam(ctx, team.ID); err == nil {
		team = fresh
	}
	return h.TeamOrganiserFrom(ctx, ev, team, tl)
}

func (h *handler) setTeamDeadline(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Deadline *string `json:"deadline"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	team, err := h.Teams.SetDeadline(r.Context(), a.Event, team, req.Deadline)
	h.writeTeamFull(w, r, a.Event, team, err)
}

// writeTeamFull answers an organiser team write with the organiser's full
// view of the team, or the refusal.
func (h *handler) writeTeamFull(w http.ResponseWriter, r *http.Request, ev hackmodel.Event, team hackmodel.EventTeam, err error) {
	if err != nil {
		core.WriteError(w, err)
		return
	}
	obj, err := h.teamOrganiserFull(r.Context(), ev, team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *handler) takeDownTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	err := h.Teams.SetSiteTakenDown(r.Context(), h.Links.Hooks, a.Event, team, true, req.Reason)
	h.writeTeamFull(w, r, a.Event, team, err)
}

func (h *handler) restoreTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	err := h.Teams.SetSiteTakenDown(r.Context(), h.Links.Hooks, a.Event, team, false, "")
	h.writeTeamFull(w, r, a.Event, team, err)
}

func (h *handler) myTeams(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	teams, err := h.Teams.MyTeams(r.Context(), user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := make([]map[string]any, 0, len(teams))
	for _, t := range teams {
		out = append(out, map[string]any{
			"team_id":     t.TeamID,
			"team_name":   t.TeamName,
			"team_slug":   t.TeamSlug,
			"event_slug":  t.EventSlug,
			"event_title": t.EventTitle,
			"site_url":    h.Links.TeamSiteURL(t.EventSlug, t.TeamSlug),
		})
	}
	// Teams the person is on that cannot publish now (deadline passed, site
	// taken down), so the consent page can say why instead of "join one".
	blocked, err := h.Teams.BlockedTeamCount(r.Context(), user.ID, len(out))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": out, "blocked": blocked})
}
