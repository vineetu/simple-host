package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/auth"
	"github.com/vsriram/simple-host/internal/db"
)

// Team sites, member keys, per-team deadlines and the organiser's team-site
// take-down (hosted events, M2; docs/designs/simple-hack-platform.md).

func (h *HackHandler) registerTeamSites(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
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
	states  map[string]db.TeamWriteState
	members map[string][]db.EventPerson // by team id
	sites   map[string]db.Site          // live sites of the holding account, by name
	ready   bool
}

func (h *HackHandler) loadTeamList(ctx context.Context, ev db.Event) (teamList, error) {
	h.pinDue(ctx, ev.ID)
	tl := teamList{sites: map[string]db.Site{}, members: map[string][]db.EventPerson{}, ready: h.teamSitesReady(ev.Slug)}
	people, err := db.ListEventPeople(ctx, h.database, ev.ID)
	if err != nil {
		return tl, err
	}
	for _, p := range people {
		if p.TeamID.Valid {
			tl.members[p.TeamID.String] = append(tl.members[p.TeamID.String], p)
		}
	}
	if tl.states, err = db.TeamWriteStatesForEvent(ctx, h.database, ev.ID); err != nil {
		return tl, err
	}
	sites, err := db.ListSitesByUser(ctx, h.database, ev.AccountID)
	if err != nil {
		return tl, err
	}
	for _, s := range sites {
		tl.sites[s.Name] = s
	}
	return tl, nil
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
	st, err := db.TeamWriteStateFor(ctx, h.database, team.ID, false)
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
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return db.EventTeam{}, false
	}
	team, err := db.GetEventTeamByID(r.Context(), h.database, a.member.TeamID.String)
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return db.EventTeam{}, false
	}
	if err != nil {
		writeInternal(w)
		return db.EventTeam{}, false
	}
	return team, true
}

func (h *HackHandler) keyJSON(ctx context.Context, a hackAccess) map[string]any {
	out := map[string]any{
		"key":      nil,
		"team":     nil,
		"site":     nil,
		"api_base": h.publicBaseURL,
		"mcp_url":  h.publicBaseURL + "/mcp",
	}
	if k, err := db.GetTeamKey(ctx, h.database, a.event.ID, a.user.ID); err == nil {
		out["key"] = map[string]any{
			"last4":        k.Last4,
			"created_at":   rfc3339Time(k.CreatedAt),
			"last_used_at": rfc3339Ptr(k.LastUsedAt),
		}
	}
	if a.member.TeamID.Valid {
		if team, err := db.GetEventTeamByID(ctx, h.database, a.member.TeamID.String); err == nil {
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
	writeJSON(w, http.StatusOK, h.keyJSON(r.Context(), a))
}

// makeTeamKey mints the person's key for their team's site (their earlier
// one stops working). The key is shown once.
func (h *HackHandler) makeTeamKey(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		writeHackErr(w, http.StatusConflict, "event_closed", "this event has ended")
		return
	}
	if !h.codesUser.allow(a.user.ID) {
		writeJSON(w, http.StatusTooManyRequests, errorResponse{Error: "rate limit exceeded, slow down", Code: "rate_limited"})
		return
	}
	key, err := auth.GenerateAPIKey()
	if err != nil {
		writeInternal(w)
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	// Read the membership again under a lock: a move or removal that
	// finished meanwhile must not leave a key bound to the old team.
	var teamID sql.NullString
	err = tx.QueryRowContext(r.Context(), `
		SELECT team_id FROM event_members WHERE event_id = $1 AND user_id = $2 AND role = 'participant' FOR UPDATE`,
		a.event.ID, a.user.ID).Scan(&teamID)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if !teamID.Valid {
		writeHackErr(w, http.StatusConflict, "no_team", "join or start a team first")
		return
	}
	team, err := db.GetEventTeamByID(r.Context(), tx, teamID.String)
	if err != nil {
		writeInternal(w)
		return
	}
	// A key is made only while it could publish.
	if st, err := db.TeamWriteStateFor(r.Context(), tx, team.ID, false); err != nil {
		writeInternal(w)
		return
	} else if code := st.WriteRefusal(); code != "" {
		status, msg := auth.TeamRefusal(code)
		writeHackErr(w, status, code, msg)
		return
	}
	k, err := db.ReplaceTeamKey(r.Context(), tx, a.event.ID, team.ID, a.user.ID, key, "team "+team.Slug+" · "+a.event.Slug)
	if err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.requestEventCert(a.event)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, map[string]any{
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
	if _, err := db.RevokeTeamKeys(r.Context(), h.database, a.event.ID, a.user.ID); err != nil {
		writeInternal(w)
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
	if !uuidShape.MatchString(userID) {
		writeEventNotFound(w)
		return
	}
	if _, err := db.GetEventMember(r.Context(), h.database, a.event.ID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeEventNotFound(w)
			return
		}
		writeInternal(w)
		return
	}
	if _, err := db.RevokeTeamKeys(r.Context(), h.database, a.event.ID, userID); err != nil {
		writeInternal(w)
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
		writeHackErr(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return a, db.EventTeam{}, false
	}
	team, err := db.GetEventTeamBySlug(r.Context(), h.database, a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		writeHackErr(w, http.StatusNotFound, "team_not_found", "team not found")
		return a, db.EventTeam{}, false
	}
	if err != nil {
		writeInternal(w)
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
	if fresh, err := db.GetEventTeamByID(ctx, h.database, team.ID); err == nil {
		team = fresh
	}
	return h.teamOrganiserFrom(ctx, ev, team, tl)
}

// teamOrganiserFrom is the organiser's view of one team from a teamList.
func (h *HackHandler) teamOrganiserFrom(ctx context.Context, ev db.Event, team db.EventTeam, tl teamList) (map[string]any, error) {
	obj := teamOrganiserJSONFrom(team, tl.members[team.ID])
	obj["site"] = h.teamSiteJSONFrom(ev, team, tl.site(team.Slug), tl.ready)
	obj["deadline"], obj["frozen"], obj["extended"], obj["pinned_version"] = nil, false, false, nil
	if st, ok := tl.states[team.ID]; ok {
		h.teamStateJSON(st, obj)
	}
	obj["deadline_override"] = rfc3339UTC(team.DeadlineOverride)
	obj["pinned_url"] = nil
	if v, ok := obj["pinned_version"].(int64); ok && h.sites != nil && tl.ready && tl.site(team.Slug) != nil {
		if u, _, ok := h.sites.TeamPreviewLink(ctx, ev.AccountID, ev.Slug, team.Slug, int(v)); ok {
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
		writeHackErr(w, http.StatusBadRequest, "invalid_deadline", "deadline is required (\"\" removes the extension)")
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
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	var eventDeadline sql.NullTime
	if err := tx.QueryRowContext(r.Context(), `SELECT submission_deadline FROM events WHERE id = $1 FOR UPDATE`, a.event.ID).Scan(&eventDeadline); err != nil {
		writeInternal(w)
		return
	}
	if t.Valid && !eventDeadline.Valid {
		writeHackErr(w, http.StatusConflict, "no_event_deadline", "set the event's submission deadline first; a team's own deadline extends it")
		return
	}
	if t.Valid && !t.Time.After(eventDeadline.Time) {
		writeHackErr(w, http.StatusBadRequest, "deadline_not_later", "a team's own deadline must be after the event's deadline")
		return
	}
	// FOR UPDATE on the team row: a deploy let in holds it FOR SHARE.
	if _, err := tx.ExecContext(r.Context(), `SELECT 1 FROM event_teams WHERE id = $1 FOR UPDATE`, team.ID); err != nil {
		writeInternal(w)
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE event_teams SET deadline_override = $2 WHERE id = $1`, team.ID, nullTimeArg(t)); err != nil {
		writeInternal(w)
		return
	}
	if err := db.UnpinReopened(r.Context(), tx, a.event.ID); err != nil {
		writeInternal(w)
		return
	}
	if err := tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	h.pinDue(r.Context(), a.event.ID)
	h.writeTeamFull(w, r, a.event, team)
}

func nullTimeArg(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func (h *HackHandler) writeTeamFull(w http.ResponseWriter, r *http.Request, ev db.Event, team db.EventTeam) {
	obj, err := h.teamOrganiserFull(r.Context(), ev, team)
	if err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, http.StatusOK, obj)
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
		writeInternal(w)
		return
	}
	if err := h.sites.SetTeamSiteTakenDown(r.Context(), a.event.AccountID, team.ID, team.Slug, true, reason); err != nil {
		log.Printf("hack: take down %s/%s: %v", a.event.Slug, team.Slug, err)
		writeInternal(w)
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
		writeInternal(w)
		return
	}
	err := h.sites.SetTeamSiteTakenDown(r.Context(), a.event.AccountID, team.ID, team.Slug, false, "")
	if errors.Is(err, ErrNotOrganiserTakedown) {
		writeHackErr(w, http.StatusConflict, "platform_takedown", "Simple Hack took this site down; only the platform can put it back")
		return
	}
	if err != nil {
		log.Printf("hack: restore %s/%s: %v", a.event.Slug, team.Slug, err)
		writeInternal(w)
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
	teams, err := db.ListMyTeams(r.Context(), h.database, user.ID)
	if err != nil {
		writeInternal(w)
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
	writeJSON(w, http.StatusOK, map[string]any{"teams": out})
}

// pinDue pins the event's teams whose deadline has passed.
func (h *HackHandler) pinDue(ctx context.Context, eventID string) {
	if err := db.PinDueTeams(ctx, h.database, eventID); err != nil {
		log.Printf("hack: pin %s: %v", eventID, err)
	}
}

// removeOrphanTeamSites moves the event's sites whose team is gone to
// Recently deleted (the hourly sweep catches anything missed here).
func (h *HackHandler) removeOrphanTeamSites(ctx context.Context, ev db.Event) {
	if h.sites == nil {
		return
	}
	orphans, err := db.OrphanTeamSites(ctx, h.database, ev.AccountID)
	if err != nil {
		log.Printf("hack: orphan sites: %v", err)
		return
	}
	for _, s := range orphans {
		if err := h.sites.TrashTeamSite(ctx, s.UserID, s.Name); err != nil {
			log.Printf("hack: remove site %s/%s: %v", ev.Slug, s.Name, err)
		}
	}
}

// requestEventCert asks for *.<event>.<SITE_DOMAIN> once the event opens.
func (h *HackHandler) requestEventCert(ev db.Event) {
	if h.sites == nil || ev.TakenDown() {
		return
	}
	switch ev.Stage {
	case "open", "building", "closed":
		h.sites.RequestSiteCert(ev.Slug)
	}
}

// RequestOpenEventCerts asks for the certificate of every event that is
// open, building or closed and has none yet (the back-fill; cheap: a request
// file is written only when missing).
func (h *HackHandler) RequestOpenEventCerts(ctx context.Context) {
	if h.sites == nil {
		return
	}
	rows, err := h.database.QueryContext(ctx, `SELECT slug FROM events WHERE stage IN ('open','building','closed') AND taken_down_at IS NULL`)
	if err != nil {
		log.Printf("hack: open events: %v", err)
		return
	}
	defer rows.Close()
	for rows.Next() {
		var slug string
		if rows.Scan(&slug) == nil {
			h.sites.RequestSiteCert(slug)
		}
	}
}
