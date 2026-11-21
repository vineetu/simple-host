package hackhttp

import (
	"context"
	"log"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// teamSiteJSON is a team's site as the events API shows it.
func (h *siteLinks) teamSiteJSON(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) map[string]any {
	var site *model.Site
	if h.hooks != nil {
		info, st, err := h.hooks.TeamSiteInfo(ctx, ev.AccountID, team.Slug)
		if err != nil {
			log.Printf("hack: team site %s/%s: %v", ev.Slug, team.Slug, err)
		} else if info.Exists {
			site = &st
		}
	}
	return h.teamSiteJSONFrom(ev, team, site, h.teamSitesReady(ev.Slug))
}

// teamSiteJSONFrom is teamSiteJSON with the site already read (nil: none).
func (h *siteLinks) teamSiteJSONFrom(ev hackmodel.Event, team hackmodel.EventTeam, site *model.Site, ready bool) map[string]any {
	out := map[string]any{
		"name":              team.Slug,
		"url":               h.teamSiteURL(ev.Slug, team.Slug),
		"ready":             ready,
		"exists":            false,
		"live_version":      nil,
		"taken_down":        team.SiteTakenDownAt.Valid,
		"taken_down_reason": team.SiteTakenDownReason,
	}
	if site != nil {
		out["exists"] = true
		out["live_version"] = site.ActiveVersion
		if site.Suspended() {
			out["taken_down"] = true
		}
	}
	return out
}

// teamList is what the organiser's and judges' lists read once per request
// instead of once per team.
type teamList struct {
	states   map[string]hackmodel.TeamWriteState
	members  map[string][]hackmodel.EventPerson // by team id
	versions map[string]bool                    // "<site id>/<n>" of every stored version
	sites    map[string]model.Site              // live sites of the holding account, by name
	ready    bool
}

func (h *siteLinks) loadTeamList(ctx context.Context, ev hackmodel.Event) (teamList, error) {
	h.pinDue(ctx, ev.ID)
	ready := h.teamSitesReady(ev.Slug)
	data, err := h.teams.LoadTeamList(ctx, ev)
	return teamList{states: data.States, members: data.Members, versions: data.Versions, sites: data.Sites, ready: ready}, err

}

func (tl teamList) site(name string) *model.Site {
	if s, ok := tl.sites[name]; ok {
		return &s
	}
	return nil
}

// teamDeadlineJSON adds the team's effective deadline fields to obj.
func (h *views) teamDeadlineJSON(ctx context.Context, team hackmodel.EventTeam, obj map[string]any) {
	obj["deadline"] = nil
	obj["frozen"] = false
	obj["extended"] = false
	obj["pinned_version"] = nil
	st, err := h.teams.WriteState(ctx, team.ID)
	if err != nil {
		return
	}
	teamStateJSON(st, obj)
}

// teamStateJSON fills the deadline fields from a team's write state.
func teamStateJSON(st hackmodel.TeamWriteState, obj map[string]any) {
	obj["extended"] = st.HasOverride
	obj["deadline"] = rfc3339UTC(st.Deadline)
	obj["frozen"] = st.Frozen()
	if st.PinnedVersion.Valid {
		obj["pinned_version"] = st.PinnedVersion.Int64
	}
}

// participantTeam loads the caller's team for a participant route: ok=false
// after writing the answer (409 no_team when on none).
func participantTeam(w http.ResponseWriter, r *http.Request, svc *teams.Service, a hackAccess) (hackmodel.EventTeam, bool) {
	team, err := svc.CallerTeam(r.Context(), a.member)
	if err != nil {
		writeHackError(w, err)
		return team, false
	}
	return team, true
}

// organiserTeam loads {team} of the event for an organiser write.
func (h *participationHTTP) organiserTeam(w http.ResponseWriter, r *http.Request) (hackAccess, hackmodel.EventTeam, bool) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return a, hackmodel.EventTeam{}, false
	}
	team, err := h.teams.OrganiserTeam(r.Context(), a.event, r.PathValue("team"))
	if err != nil {
		writeHackError(w, err)
		return a, team, false
	}
	return a, team, true
}

// teamOrganiserFull is the organiser's view of a team with its site and
// deadline.
func (h *participationHTTP) teamOrganiserFull(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam) (map[string]any, error) {
	tl, err := h.links.loadTeamList(ctx, ev)
	if err != nil {
		return nil, err
	}
	if fresh, err := h.teams.GetTeam(ctx, team.ID); err == nil {
		team = fresh
	}
	return h.teamOrganiserFrom(ctx, ev, team, tl)
}

// teamOrganiserFrom is the organiser's view of one team from a teamList.
func (h *participationHTTP) teamOrganiserFrom(ctx context.Context, ev hackmodel.Event, team hackmodel.EventTeam, tl teamList) (map[string]any, error) {
	obj := teamOrganiserJSONFrom(team, tl.members[team.ID], h.links.teamLinkURL(team.Code))
	track, err := h.teams.Track(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	obj["track"] = track
	obj["site"] = h.links.teamSiteJSONFrom(ev, team, tl.site(team.Slug), tl.ready)
	obj["deadline"], obj["frozen"], obj["extended"], obj["pinned_version"] = nil, false, false, nil
	if st, ok := tl.states[team.ID]; ok {
		teamStateJSON(st, obj)
	}
	obj["deadline_override"] = rfc3339UTC(team.DeadlineOverride)
	obj["pinned_url"] = nil
	if v, ok := obj["pinned_version"].(int64); ok {
		if u, ok := h.links.pinnedLink(ev, team, tl, int(v)); ok {
			obj["pinned_url"] = u
		}
	}
	return obj, nil
}

// setTeamDeadline gives one team until a later time (or takes that back).
func (h *participationHTTP) setTeamDeadline(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Deadline *string `json:"deadline"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	team, err := h.teams.SetDeadline(r.Context(), a.event, team, req.Deadline)
	h.writeTeamFull(w, r, a.event, team, err)
}

// writeTeamFull answers an organiser team write with the organiser's full
// view of the team, or the refusal.
func (h *participationHTTP) writeTeamFull(w http.ResponseWriter, r *http.Request, ev hackmodel.Event, team hackmodel.EventTeam, err error) {
	if err != nil {
		writeHackError(w, err)
		return
	}
	obj, err := h.teamOrganiserFull(r.Context(), ev, team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *participationHTTP) takeDownTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	err := h.teams.SetSiteTakenDown(r.Context(), h.links.hooks, a.event, team, true, req.Reason)
	h.writeTeamFull(w, r, a.event, team, err)
}

func (h *participationHTTP) restoreTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	err := h.teams.SetSiteTakenDown(r.Context(), h.links.hooks, a.event, team, false, "")
	h.writeTeamFull(w, r, a.event, team, err)
}

// myTeams lists the caller's teams, for the connector's choice of site.
func (h *participationHTTP) myTeams(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	teams, err := h.teams.MyTeams(r.Context(), user.ID)
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
			"site_url":    h.links.teamSiteURL(t.EventSlug, t.TeamSlug),
		})
	}
	// Teams the person is on that cannot publish now (deadline passed, site
	// taken down), so the consent page can say why instead of "join one".
	blocked, err := h.teams.BlockedTeamCount(r.Context(), user.ID, len(out))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": out, "blocked": blocked})
}

// pinDue pins the event's teams whose deadline has passed.
func (h *siteLinks) pinDue(ctx context.Context, eventID string) {
	if err := h.teams.PinDue(ctx, eventID); err != nil {
		log.Printf("hack: pin %s: %v", eventID, err)
	}
}

// removeOrphanTeamSites moves the event's sites whose team is gone to
// Recently deleted (the hourly sweep catches anything missed here).
func (h *siteLinks) removeOrphanTeamSites(ctx context.Context, ev hackmodel.Event) {
	var trash func(context.Context, string, string) error
	if h.hooks != nil {
		trash = h.hooks.TrashTeamSite
	}
	h.teams.RemoveOrphanTeamSites(ctx, ev, trash)
}

// requestEventCert asks for *.<event>.<SITE_DOMAIN> once the event opens.
func (h *siteLinks) requestEventCert(ev hackmodel.Event) {
	var request func(string)
	if h.hooks != nil {
		request = h.hooks.RequestSiteCert
	}
	h.teams.RequestEventCert(ev, request)
}

// RequestOpenEventCerts asks for the certificate of every event that is
// open, building or closed and has none yet (the back-fill; cheap: a request
// file is written only when missing).
func (h *HackHandler) RequestOpenEventCerts(ctx context.Context) {
	if h.links.hooks == nil {
		return
	}
	if err := h.teams.OpenEventSlugs(ctx, func(handle string) {
		if s, ok := h.links.hooks.(interface{ RequestSiteCertContext(context.Context, string) }); ok {
			s.RequestSiteCertContext(ctx, handle)
		} else {
			h.links.hooks.RequestSiteCert(handle)
		}
	}); err != nil {
		siteSweepReport{ctx: ctx}.Fail("list open events for certificates", err)
	}

}

// pinnedLink is the deadline version's preview link from a teamList: none
// for a site taken down (by the organiser or the platform) or gone.
func (h *siteLinks) pinnedLink(ev hackmodel.Event, team hackmodel.EventTeam, tl teamList, v int) (string, bool) {
	site := tl.site(team.Slug)
	if h.hooks == nil || !tl.ready || site == nil || site.Suspended() || team.SiteTakenDownAt.Valid {
		return "", false
	}
	if st, ok := tl.states[team.ID]; ok && (st.SiteTakenDown || st.EventTakenDown) {
		return "", false
	}
	if !tl.versions[site.ID+"/"+strconv.Itoa(v)] {
		return "", false
	}
	return h.hooks.TeamPreviewLinkFor(*site, ev.Slug, v)
}
