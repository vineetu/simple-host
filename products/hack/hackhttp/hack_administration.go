package hackhttp

import (
	"context"
	"database/sql"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	"github.com/vsriram/simple-host/host/web"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/members"
	"github.com/vsriram/simple-host/products/hack/model"
)

// organiserWrite is loadMember for an organiser change to an event that has
// not ended.
func organiserWrite(w http.ResponseWriter, r *http.Request, svc *members.Service) (hackAccess, bool) {
	a, ok := loadMember(w, r, svc, r.PathValue("slug"), true, "organiser")
	if ok && a.event.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return a, false
	}
	return a, ok
}

func (h *HackHandler) createOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := organiserWrite(w, r, h.members)
	if !ok {
		return
	}
	code, expires, err := h.events.CreateOrganiserInvite(r.Context(), a.event.ID, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 201, map[string]any{"url": h.links.publicBaseURL + "/organiser/" + code, "expires_at": rfc3339Time(expires)})
}

func (h *HackHandler) revokeOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := organiserWrite(w, r, h.members)
	if !ok {
		return
	}
	if err := h.events.RevokeOrganiserInvite(r.Context(), a.event.ID); err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.WriteHeader(204)
}

func (h *HackHandler) getOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	caller, accountName := "", ""
	if u := auth.GetUser(r.Context()); u != nil {
		caller, accountName = u.ID, strings.TrimSpace(u.DisplayName.String)
	}
	if h.codes.limited(w, r, caller) {
		return
	}
	ev, err := h.events.OrganiserInvite(r.Context(), r.PathValue("code"))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, 404, "invalid_code", "invalid or expired invitation")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		httpx.WriteError(w, http.StatusForbidden, "event_taken_down", "this event has been taken down")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 200, map[string]any{"slug": ev.Slug, "title": ev.Title, "icon_url": h.links.iconURL(ev), "stage": ev.Stage, "organiser_name": ev.OrganiserName, "organisation": ev.Organisation, "role": "organiser", "account_name": accountName, "joinable": ev.Stage != "archived", "coc_default": HackDefaultCoC, "coc_text": ev.CocText})
}

func (h *HackHandler) acceptOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.codes.limited(w, r, user.ID) {
		return
	}
	var req struct {
		AcceptCoC   bool   `json:"accept_coc"`
		DisplayName string `json:"display_name"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	if !req.AcceptCoC {
		httpx.WriteError(w, 400, "coc_required", "you must accept the code of conduct")
		return
	}
	name := req.DisplayName
	ev, member, err := h.events.AcceptOrganiserInvite(r.Context(), r.PathValue("code"), user.ID, name)
	if err != nil {
		writeHackError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, h.views.eventView(r.Context(), ev, member, true))
}

func (h *HackHandler) removeOrganiser(w http.ResponseWriter, r *http.Request) {
	a, ok := organiserWrite(w, r, h.members)
	if !ok {
		return
	}
	id := r.PathValue("user_id")
	if !sites.UUIDShape.MatchString(id) {
		writeEventNotFound(w)
		return
	}
	if err := h.events.RemoveOrganiser(r.Context(), a.event, a.user.ID, id); err != nil {
		writeHackError(w, err)
		return
	}
	w.WriteHeader(204)
}

func (h *HackHandler) renameTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := organiserWrite(w, r, h.members)
	if !ok {
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	name, ok := checkHackLine(w, events.StripInvisible(req.Name), "team_name", 1, 80)
	if !ok {
		return
	}
	slug := r.PathValue("team")
	err := h.teams.RenameEventTeamLookup(r.Context(), a.event.ID, slug, name)
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if events.UniqueError(err) {
		httpx.WriteError(w, 409, "team_name_taken", "another team has that name")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"name": name, "slug": slug})
}

// exportAdministrationCSV answers participants.csv, teams.csv or
// entries.csv for the event's organisers.
func (h *HackHandler) exportAdministrationCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	kind := filepath.Base(r.URL.Path)
	build := map[string]func(context.Context, model.Event) ([][]string, error){
		"participants.csv": h.participantRows,
		"teams.csv":        h.teamRows,
		"entries.csv":      h.entryRows,
	}[kind]
	var rows [][]string
	if build != nil {
		var err error
		if rows, err = build(r.Context(), a.event); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, a.event.Slug, kind))
	writeCSV(w, rows)
}

func writeCSV(w http.ResponseWriter, rows [][]string) {
	cw := csv.NewWriter(w)
	defer cw.Flush()
	for _, row := range rows {
		for i := range row {
			row[i] = httpx.CSVSafe(row[i])
		}
		if err := cw.Write(row); err != nil {
			return
		}
	}
}

func rfc3339Or(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return rfc3339Time(t.Time)
}

func (h *HackHandler) participantRows(ctx context.Context, ev model.Event) ([][]string, error) {
	people, err := h.events.EventPeople(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	rows := [][]string{{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at", "city", "country", "age_range", "gender", "joined_via"}}
	for _, p := range people {
		if p.Role != "participant" || p.ApprovalStatus != "approved" {
			continue
		}
		rows = append(rows, []string{
			p.DisplayName, p.Email, p.TeamName.String, p.TeamSlug.String, rfc3339Time(p.JoinedAt), rfc3339Or(p.CocAcceptedAt),
			p.City, events.CountryName(p.Country), events.AgeRangeLabels[p.AgeRange], events.GenderLabels[p.Gender], events.JoinedViaLabel(p.JoinedVia),
		})
	}
	return rows, nil
}

func (h *HackHandler) teamRows(ctx context.Context, ev model.Event) ([][]string, error) {
	teams, err := h.teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	people, err := h.events.EventPeople(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	counts := map[string]int{}
	for _, p := range people {
		if p.TeamID.Valid {
			counts[p.TeamID.String]++
		}
	}
	rows := [][]string{{"name", "slug", "members", "site_url", "created_at"}}
	for _, t := range teams {
		rows = append(rows, []string{t.Name, t.Slug, strconv.Itoa(counts[t.ID]), h.links.teamSiteURL(ev.Slug, t.Slug), rfc3339Time(t.CreatedAt)})
	}
	return rows, nil
}

func (h *HackHandler) entryRows(ctx context.Context, ev model.Event) ([][]string, error) {
	stored, err := h.entries.EventEntries(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	teams, err := h.teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.EventTeam{}
	for _, t := range teams {
		byID[t.ID] = t
	}
	tracks, err := h.events.EventTracks(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	fields := entryapp.LoadRules(ev.EntryRules)
	rows := [][]string{{"team", "team_slug", "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at",
		"status", "submitted_at", "submit_auto", "problem", "audience", "track", "tools", "maturity", "missing", "excluded"}}
	for _, e := range stored {
		t := byID[e.TeamID]
		track, err := h.events.TeamTrackLookup(ctx, e.TeamID)
		if err != nil {
			return nil, err
		}
		check := entryapp.Evaluate(fields, e, h.links.entrySiteLive(ctx, ev, t.Slug, t.SiteTakenDownAt.Valid), len(tracks) > 0, track)
		trackName := ""
		if track != nil {
			trackName = track.Name
		}
		rows = append(rows, []string{
			t.Name, t.Slug, e.Title, e.Tagline, e.Description, e.VideoURL, e.CodeURL, strconv.FormatBool(e.HasScreenshot), rfc3339Time(e.UpdatedAt),
			e.Status(), rfc3339Or(e.SubmittedAt), strconv.FormatBool(e.SubmitAuto), e.Problem, e.Audience, trackName, strings.Join(e.Tools, "; "), e.Maturity,
			strings.Join(check.Missing, "; "), strconv.FormatBool(e.ExcludedAt.Valid),
		})
	}
	return rows, nil
}

type hackAdministrationSites interface {
	ExportEventProjects(http.ResponseWriter, *http.Request, web.GroupRef, []string, string)
	EventFileBytes(context.Context, string) (int64, error)
}

func (h *HackHandler) exportProjects(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	sites, ok := h.links.hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.event, "")
}
func (h *HackHandler) exportOwnTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := participantTeam(w, r, h.teams, a)
	if !ok {
		return
	}
	sites, ok := h.links.hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.event, team.Slug)
}
func (h *HackHandler) eventUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	u, err := h.events.EventStorageLookup(r.Context(), a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	sites, ok := h.links.hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	u.FileBytes, err = sites.EventFileBytes(r.Context(), a.event.AccountID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, u)
}

func (h *HackHandler) exportEventProjects(w http.ResponseWriter, r *http.Request, sites hackAdministrationSites, ev model.Event, ownSlug string) {
	teams, err := h.teams.SiteTeams(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	slugs := make([]string, 0, len(teams))
	for _, team := range teams {
		slugs = append(slugs, team.Slug)
	}
	sites.ExportEventProjects(w, r, groupRef(ev), slugs, ownSlug)
}
