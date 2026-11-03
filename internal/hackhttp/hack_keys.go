package hackhttp

import (
	"context"
	"database/sql"
	"errors"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/rules/sites"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/teams"
	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
	"github.com/vsriram/simple-host/internal/model"
)

// Team sites, member keys, per-team deadlines and the organiser's team-site
// take-down (hosted events, M2; docs/designs/simple-hack-platform.md).

func (h *HackHandler) registerTeamSites(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("PUT /v1/hack/events/{slug}/site", wrap(h.putTeamSiteArchive))
	mux.Handle("GET /v1/hack/events/{slug}/site", wrap(h.getTeamSiteStatus))
	mux.Handle("GET /v1/hack/events/{slug}/key", wrap(h.getTeamKey))
	mux.Handle("POST /v1/hack/events/{slug}/key", wrap(h.makeTeamKey))
	mux.Handle("DELETE /v1/hack/events/{slug}/key", wrap(h.deleteTeamKey))
	mux.Handle("DELETE /v1/hack/events/{slug}/people/{user_id}/key", wrap(h.revokePersonKey))
	mux.Handle("PUT /v1/hack/events/{slug}/teams/{team}/deadline", wrap(h.setTeamDeadline))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/takedown", wrap(h.takeDownTeamSite))
	mux.Handle("POST /v1/hack/events/{slug}/teams/{team}/restore", wrap(h.restoreTeamSite))
	mux.Handle("GET /v1/hack/my-teams", wrap(h.myTeams))
}

// teamSiteJSON is a team's site as the events API shows it.
func (h *HackHandler) teamSiteJSON(ctx context.Context, ev db.Event, team db.EventTeam) map[string]any {
	var site *db.Site
	if h.sites != nil {
		info, st, err := h.sites.TeamSiteInfo(ctx, ev.AccountID, team.Slug)
		if err != nil {
			log.Printf("hack: team site %s/%s: %v", ev.Slug, team.Slug, err)
		} else if info.Exists {
			site = &st
		}
	}
	return h.teamSiteJSONFrom(ev, team, site, h.teamSitesReady(ev.Slug))
}

// teamSiteJSONFrom is teamSiteJSON with the site already read (nil: none).
func (h *HackHandler) teamSiteJSONFrom(ev db.Event, team db.EventTeam, site *db.Site, ready bool) map[string]any {
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
	states   map[string]db.TeamWriteState
	members  map[string][]db.EventPerson // by team id
	versions map[string]bool             // "<site id>/<n>" of every stored version
	sites    map[string]db.Site          // live sites of the holding account, by name
	ready    bool
}

func (h *HackHandler) loadTeamList(ctx context.Context, ev db.Event) (teamList, error) {
	h.pinDue(ctx, ev.ID)
	ready := h.teamSitesReady(ev.Slug)
	data, err := h.teamsSvc().LoadTeamList(ctx, ev)
	return teamList{states: data.States, members: data.Members, versions: data.Versions, sites: data.Sites, ready: ready}, err

}

func (tl teamList) site(name string) *db.Site {
	if s, ok := tl.sites[name]; ok {
		return &s
	}
	return nil
}

// teamDeadlineJSON adds the team's effective deadline fields to obj.
func (h *HackHandler) teamDeadlineJSON(ctx context.Context, team db.EventTeam, obj map[string]any) {
	obj["deadline"] = nil
	obj["frozen"] = false
	obj["extended"] = false
	obj["pinned_version"] = nil
	st, err := h.teamsSvc().WriteState(ctx, team.ID)
	if err != nil {
		return
	}
	h.teamStateJSON(st, obj)
}

// teamStateJSON fills the deadline fields from a team's write state.
func (h *HackHandler) teamStateJSON(st db.TeamWriteState, obj map[string]any) {
	obj["extended"] = st.HasOverride
	obj["deadline"] = rfc3339UTC(st.Deadline)
	obj["frozen"] = st.Frozen()
	if st.PinnedVersion.Valid {
		obj["pinned_version"] = st.PinnedVersion.Int64
	}
}

// participantTeam loads the caller's team for a participant route: ok=false
// after writing the answer (409 no_team when on none).
func (h *HackHandler) participantTeam(w http.ResponseWriter, r *http.Request, a hackAccess) (db.EventTeam, bool) {
	if !a.member.TeamID.Valid {
		httpx.WriteError(w, http.StatusConflict, "no_team", "join or start a team first")
		return db.EventTeam{}, false
	}
	team, err := h.teamsSvc().GetTeam(r.Context(), a.member.TeamID.String)
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusConflict, "no_team", "join or start a team first")
		return db.EventTeam{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return db.EventTeam{}, false
	}
	return team, true
}

func (h *HackHandler) keyJSON(ctx context.Context, a hackAccess) map[string]any {
	out := map[string]any{
		"key":                nil,
		"team":               nil,
		"site":               nil,
		"api_base":           h.publicBaseURL,
		"mcp_url":            h.publicBaseURL + "/mcp",
		"upload_limit_bytes": h.websiteUploadLimit(ctx, a.event),
		"upload_limits":      h.websiteUploadLimits(ctx, a.event),
	}
	if k, err := h.teamsSvc().Key(ctx, a.event.ID, a.user.ID); err == nil {
		out["key"] = map[string]any{
			"last4":        k.Last4,
			"created_at":   rfc3339Time(k.CreatedAt),
			"last_used_at": rfc3339Ptr(k.LastUsedAt),
		}
	}
	if a.member.TeamID.Valid {
		if team, err := h.teamsSvc().GetTeam(ctx, a.member.TeamID.String); err == nil {
			out["team"] = map[string]any{"slug": team.Slug, "name": team.Name}
			out["site"] = h.teamSiteJSON(ctx, a.event, team)
		}
	}
	return out
}

func rfc3339Ptr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return rfc3339Time(*t)
}

func (h *HackHandler) getTeamKey(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	httpx.WriteJSON(w, http.StatusOK, h.keyJSON(r.Context(), a))
}

// makeTeamKey mints the person's key for their team's site (their earlier
// one stops working). The key is shown once.
func (h *HackHandler) makeTeamKey(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	if !h.codesUser.Allow(a.user.ID) {
		httpx.WriteJSON(w, http.StatusTooManyRequests, httpx.ErrorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	key, err := auth.GenerateAPIKey()
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	team, k, err := h.teamsSvc().MintTeamKey(r.Context(), a.event, a.user.ID, key)
	if err != nil {
		var e *teams.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "event_not_found":
				writeEventNotFound(w)
				return
			case "no_team":
				httpx.WriteError(w, http.StatusConflict, "no_team", "join or start a team first")
				return
			default:
				status, msg := auth.TeamRefusal(e.Code)
				httpx.WriteError(w, status, e.Code, msg)
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	h.requestEventCert(a.event)
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{
		"key":        key,
		"last4":      k.Last4,
		"created_at": rfc3339Time(k.CreatedAt),
		"site":       h.teamSiteJSON(r.Context(), a.event, team),
		"api_base":   h.publicBaseURL,
		"mcp_url":    h.publicBaseURL + "/mcp",
	})
}

func (h *HackHandler) deleteTeamKey(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if err := h.teamsSvc().RevokeKey(r.Context(), a.event.ID, a.user.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// revokePersonKey: the organiser turns off one person's team key.
func (h *HackHandler) revokePersonKey(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	userID := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	if err := h.teamsSvc().RevokePersonKey(r.Context(), a.event.ID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// organiserTeam loads {team} of the event for an organiser write.
func (h *HackHandler) organiserTeam(w http.ResponseWriter, r *http.Request) (hackAccess, db.EventTeam, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return a, db.EventTeam{}, false
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return a, db.EventTeam{}, false
	}
	team, err := h.teamsSvc().TeamBySlug(r.Context(), a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return a, db.EventTeam{}, false
	}
	if err != nil {
		httpx.WriteInternal(w)
		return a, db.EventTeam{}, false
	}
	return a, team, true
}

// teamOrganiserFull is the organiser's view of a team with its site and
// deadline.
func (h *HackHandler) teamOrganiserFull(ctx context.Context, ev db.Event, team db.EventTeam) (map[string]any, error) {
	tl, err := h.loadTeamList(ctx, ev)
	if err != nil {
		return nil, err
	}
	if fresh, err := h.teamsSvc().GetTeam(ctx, team.ID); err == nil {
		team = fresh
	}
	return h.teamOrganiserFrom(ctx, ev, team, tl)
}

// teamOrganiserFrom is the organiser's view of one team from a teamList.
func (h *HackHandler) teamOrganiserFrom(ctx context.Context, ev db.Event, team db.EventTeam, tl teamList) (map[string]any, error) {
	obj := teamOrganiserJSONFrom(team, tl.members[team.ID], h.teamLinkURL(team.Code))
	track, err := h.teamsSvc().Track(ctx, team.ID)
	if err != nil {
		return nil, err
	}
	obj["track"] = track
	obj["site"] = h.teamSiteJSONFrom(ev, team, tl.site(team.Slug), tl.ready)
	obj["deadline"], obj["frozen"], obj["extended"], obj["pinned_version"] = nil, false, false, nil
	if st, ok := tl.states[team.ID]; ok {
		h.teamStateJSON(st, obj)
	}
	obj["deadline_override"] = rfc3339UTC(team.DeadlineOverride)
	obj["pinned_url"] = nil
	if v, ok := obj["pinned_version"].(int64); ok {
		if u, ok := h.pinnedLink(ev, team, tl, int(v)); ok {
			obj["pinned_url"] = u
		}
	}
	return obj, nil
}

// setTeamDeadline gives one team until a later time (or takes that back).
func (h *HackHandler) setTeamDeadline(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Deadline *string `json:"deadline"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Deadline == nil {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_deadline", "deadline is required (\"\" removes the extension)")
		return
	}
	var t sql.NullTime
	if strings.TrimSpace(*req.Deadline) != "" {
		loc, err := time.LoadLocation(a.event.TimeZone)
		if err != nil {
			loc = time.UTC
		}
		parsed, ok := parseEventTime(w, *req.Deadline, loc, "deadline", true)
		if !ok {
			return
		}
		t = parsed
	}
	team, err := h.teamsSvc().SetDeadline(r.Context(), a.event.ID, team, t)
	if err != nil {
		var e *teams.Error
		if errors.As(err, &e) {
			switch e.Code {
			case "no_event_deadline":
				httpx.WriteError(w, http.StatusConflict, "no_event_deadline", "set the event's submission deadline first; a team's own deadline extends it")
				return
			case "deadline_not_later":
				httpx.WriteError(w, http.StatusBadRequest, "deadline_not_later", "a team's own deadline must be after the event's deadline")
				return
			}
		}
		httpx.WriteInternal(w)
		return
	}
	h.pinDue(r.Context(), a.event.ID)
	h.writeTeamFull(w, r, a.event, team)
}

func (h *HackHandler) writeTeamFull(w http.ResponseWriter, r *http.Request, ev db.Event, team db.EventTeam) {
	obj, err := h.teamOrganiserFull(r.Context(), ev, team)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, obj)
}

func (h *HackHandler) takeDownTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength != 0 && !decodeHackJSON(w, r, &req) {
		return
	}
	reason, ok := checkHackLine(w, req.Reason, "reason", 0, 300)
	if !ok {
		return
	}
	if h.sites == nil {
		httpx.WriteInternal(w)
		return
	}
	if err := h.teamsSvc().SetSiteTakenDown(r.Context(), h.sites, a.event, team, true, reason); err != nil {
		log.Printf("hack: take down %s/%s: %v", a.event.Slug, team.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	h.writeTeamFull(w, r, a.event, team)
}

func (h *HackHandler) restoreTeamSite(w http.ResponseWriter, r *http.Request) {
	a, team, ok := h.organiserTeam(w, r)
	if !ok {
		return
	}
	if h.sites == nil {
		httpx.WriteInternal(w)
		return
	}
	err := h.teamsSvc().SetSiteTakenDown(r.Context(), h.sites, a.event, team, false, "")
	if errors.Is(err, model.ErrNotOrganiserTakedown) {
		httpx.WriteError(w, http.StatusConflict, "platform_takedown", "Simple Hack took this site down; only the platform can put it back")
		return
	}
	if err != nil {
		log.Printf("hack: restore %s/%s: %v", a.event.Slug, team.Slug, err)
		httpx.WriteInternal(w)
		return
	}
	h.writeTeamFull(w, r, a.event, team)
}

// myTeams lists the caller's teams, for the connector's choice of site.
func (h *HackHandler) myTeams(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	teams, err := h.teamsSvc().MyTeams(r.Context(), user.ID)
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
			"site_url":    h.teamSiteURL(t.EventSlug, t.TeamSlug),
		})
	}
	// Teams the person is on that cannot publish now (deadline passed, site
	// taken down), so the consent page can say why instead of "join one".
	blocked, err := h.teamsSvc().BlockedTeamCount(r.Context(), user.ID, len(out))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": out, "blocked": blocked})
}

// pinDue pins the event's teams whose deadline has passed.
func (h *HackHandler) pinDue(ctx context.Context, eventID string) {
	if err := h.teamsSvc().PinDue(ctx, eventID); err != nil {
		log.Printf("hack: pin %s: %v", eventID, err)
	}
}

// removeOrphanTeamSites moves the event's sites whose team is gone to
// Recently deleted (the hourly sweep catches anything missed here).
func (h *HackHandler) removeOrphanTeamSites(ctx context.Context, ev db.Event) {
	var trash func(context.Context, string, string) error
	if h.sites != nil {
		trash = h.sites.TrashTeamSite
	}
	h.teamsSvc().RemoveOrphanTeamSites(ctx, ev, trash)
}

// requestEventCert asks for *.<event>.<SITE_DOMAIN> once the event opens.
func (h *HackHandler) requestEventCert(ev db.Event) {
	var request func(string)
	if h.sites != nil {
		request = h.sites.RequestSiteCert
	}
	h.teamsSvc().RequestEventCert(ev, request)
}

// RequestOpenEventCerts asks for the certificate of every event that is
// open, building or closed and has none yet (the back-fill; cheap: a request
// file is written only when missing).
func (h *HackHandler) RequestOpenEventCerts(ctx context.Context) {
	if h.sites == nil {
		return
	}
	if err := h.teamsSvc().OpenEventSlugs(ctx, h.sites.RequestSiteCert); err != nil {
		log.Printf("hack: open events: %v", err)
	}

}

// pinnedLink is the deadline version's preview link from a teamList: none
// for a site taken down (by the organiser or the platform) or gone.
func (h *HackHandler) pinnedLink(ev db.Event, team db.EventTeam, tl teamList, v int) (string, bool) {
	site := tl.site(team.Slug)
	if h.sites == nil || !tl.ready || site == nil || site.Suspended() || team.SiteTakenDownAt.Valid {
		return "", false
	}
	if st, ok := tl.states[team.ID]; ok && (st.SiteTakenDown || st.EventTakenDown) {
		return "", false
	}
	if !tl.versions[site.ID+"/"+strconv.Itoa(v)] {
		return "", false
	}
	return h.sites.TeamPreviewLinkFor(*site, ev.Slug, v)
}

// putTeamSiteArchive publishes for the signed-in participant without minting a key.
func (h *HackHandler) putTeamSiteArchive(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	sites, ok := h.sites.(interface {
		PublishTeamArchive(http.ResponseWriter, *http.Request, db.User, string)
	})
	if !ok {
		httpx.WriteError(w, 503, "website_unavailable", "team website publishing is unavailable")
		return
	}
	sites.PublishTeamArchive(w, r, *a.user, team.ID)
}
