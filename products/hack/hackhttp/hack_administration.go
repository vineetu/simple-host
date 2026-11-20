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
	"github.com/vsriram/simple-host/host/handler"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/rules/sites"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

func (h *hackBase) administrationWrite(w http.ResponseWriter, r *http.Request) (hackAccess, bool) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if ok && a.event.Stage == "archived" {
		httpx.WriteError(w, 409, "event_closed", "an ended event cannot be changed")
		return a, false
	}
	return a, ok
}

func (h *HackHandler) createOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
	if !ok {
		return
	}
	code, expires, err := h.events.CreateOrganiserInvite(r.Context(), a.event.ID, a.user.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.WriteJSON(w, 201, map[string]any{"url": h.publicBaseURL + "/organiser/" + code, "expires_at": rfc3339Time(expires)})
}

func (h *HackHandler) revokeOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
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
	if h.rateLimited(w, r, caller) {
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
	httpx.WriteJSON(w, 200, map[string]any{"slug": ev.Slug, "title": ev.Title, "icon_url": h.iconURL(ev), "stage": ev.Stage, "organiser_name": ev.OrganiserName, "organisation": ev.Organisation, "role": "organiser", "account_name": accountName, "joinable": ev.Stage != "archived", "coc_default": HackDefaultCoC, "coc_text": ev.CocText})
}

func (h *HackHandler) acceptOrganiserInvite(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if h.rateLimited(w, r, user.ID) {
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
	httpx.WriteJSON(w, 200, h.eventView(r.Context(), ev, member, true))
}

func (h *HackHandler) removeOrganiser(w http.ResponseWriter, r *http.Request) {
	a, ok := h.administrationWrite(w, r)
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
	a, ok := h.administrationWrite(w, r)
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

func (h *HackHandler) exportAdministrationCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	var rows [][]string
	kind := filepath.Base(r.URL.Path)
	switch kind {
	case "participants.csv":
		people, err := h.events.EventPeople(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		// D11: appended at the end, so existing spreadsheet imports keep working.
		rows = append(rows, []string{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at", "city", "country", "age_range", "gender", "joined_via"})
		for _, p := range people {
			if p.Role == "participant" && p.ApprovalStatus == "approved" {
				accepted := ""
				if p.CocAcceptedAt.Valid {
					accepted = rfc3339Time(p.CocAcceptedAt.Time)
				}
				rows = append(rows, []string{
					p.DisplayName, p.Email, p.TeamName.String, p.TeamSlug.String, rfc3339Time(p.JoinedAt), accepted,
					p.City, events.CountryName(p.Country), events.AgeRangeLabels[p.AgeRange], events.GenderLabels[p.Gender], events.JoinedViaLabel(p.JoinedVia),
				})
			}
		}
	case "teams.csv":
		teams, err := h.teams.EventTeams(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		people, err := h.events.EventPeople(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		counts := map[string]int{}
		for _, p := range people {
			if p.TeamID.Valid {
				counts[p.TeamID.String]++
			}
		}
		rows = append(rows, []string{"name", "slug", "members", "site_url", "created_at"})
		for _, t := range teams {
			rows = append(rows, []string{t.Name, t.Slug, strconv.Itoa(counts[t.ID]), h.teamSiteURL(a.event.Slug, t.Slug), rfc3339Time(t.CreatedAt)})
		}
	case "entries.csv":
		entries, err := h.entries.EventEntries(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		teams, err := h.teams.EventTeams(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		byID := map[string]model.EventTeam{}
		for _, t := range teams {
			byID[t.ID] = t
		}
		fields := entryapp.LoadRules(a.event.EntryRules)
		tracks, err := h.events.EventTracks(r.Context(), a.event.ID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		hasTracks := len(tracks) > 0
		// D11-style: the new columns are appended at the end, so existing
		// spreadsheet imports keep working (section 4.7).
		rows = append(rows, []string{"team", "team_slug", "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at",
			"status", "submitted_at", "submit_auto", "problem", "audience", "track", "tools", "maturity", "missing", "excluded"})
		for _, e := range entries {
			t := byID[e.TeamID]
			track, err := h.events.TeamTrackLookup(r.Context(), e.TeamID)
			if err != nil {
				httpx.WriteInternal(w)
				return
			}
			siteLive := h.entrySiteLive(r.Context(), a.event, t.Slug, t.SiteTakenDownAt.Valid)
			check := entryapp.Evaluate(fields, e, siteLive, hasTracks, track)
			trackName := ""
			if track != nil {
				trackName = track.Name
			}
			submittedAt := ""
			if e.SubmittedAt.Valid {
				submittedAt = rfc3339Time(e.SubmittedAt.Time)
			}
			rows = append(rows, []string{
				t.Name, t.Slug, e.Title, e.Tagline, e.Description, e.VideoURL, e.CodeURL, strconv.FormatBool(e.HasScreenshot), rfc3339Time(e.UpdatedAt),
				e.Status(), submittedAt, strconv.FormatBool(e.SubmitAuto), e.Problem, e.Audience, trackName, strings.Join(e.Tools, "; "), e.Maturity,
				strings.Join(check.Missing, "; "), strconv.FormatBool(e.ExcludedAt.Valid),
			})
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, a.event.Slug, kind))
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

type hackAdministrationSites interface {
	ExportEventProjects(http.ResponseWriter, *http.Request, handler.GroupRef, []string, string)
	EventFileBytes(context.Context, string) (int64, error)
}

func (h *HackHandler) exportProjects(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.event, "")
}
func (h *HackHandler) exportOwnTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := h.participantTeam(w, r, a)
	if !ok {
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.event, team.Slug)
}
func (h *HackHandler) eventUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	u, err := h.events.EventStorageLookup(r.Context(), a.event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	sites, ok := h.sites.(hackAdministrationSites)
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

// exportEventProjects resolves product-owned team names after authorization.
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
