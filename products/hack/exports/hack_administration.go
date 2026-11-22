package exports

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/web"
	entryapp "github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

// exportAdministrationCSV answers participants.csv, teams.csv or
// entries.csv for the event's organisers.
func (h *Handler) exportAdministrationCSV(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
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
		if rows, err = build(r.Context(), a.Event); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s-%s"`, a.Event.Slug, kind))
	core.WriteCSV(w, rows)
}

func rfc3339Or(t sql.NullTime) string {
	if !t.Valid {
		return ""
	}
	return core.RFC3339Time(t.Time)
}

func (h *Handler) participantRows(ctx context.Context, ev model.Event) ([][]string, error) {
	people, err := h.Events.EventPeople(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	rows := [][]string{{"name", "email", "team", "team_slug", "joined_at", "coc_accepted_at", "city", "country", "age_range", "gender", "joined_via"}}
	for _, p := range people {
		if p.Role != "participant" || p.ApprovalStatus != "approved" {
			continue
		}
		rows = append(rows, []string{
			p.DisplayName, p.Email, p.TeamName.String, p.TeamSlug.String, core.RFC3339Time(p.JoinedAt), rfc3339Or(p.CocAcceptedAt),
			p.City, events.CountryName(p.Country), events.AgeRangeLabels[p.AgeRange], events.GenderLabels[p.Gender], events.JoinedViaLabel(p.JoinedVia),
		})
	}
	return rows, nil
}

func (h *Handler) teamRows(ctx context.Context, ev model.Event) ([][]string, error) {
	teams, err := h.Teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	people, err := h.Events.EventPeople(ctx, ev.ID)
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
		rows = append(rows, []string{t.Name, t.Slug, strconv.Itoa(counts[t.ID]), h.Links.TeamSiteURL(ev.Slug, t.Slug), core.RFC3339Time(t.CreatedAt)})
	}
	return rows, nil
}

func (h *Handler) entryRows(ctx context.Context, ev model.Event) ([][]string, error) {
	stored, err := h.Entries.EventEntries(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	teams, err := h.Teams.EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	byID := map[string]model.EventTeam{}
	for _, t := range teams {
		byID[t.ID] = t
	}
	tracks, err := h.Events.EventTracks(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	fields := entryapp.LoadRules(ev.EntryRules)
	rows := [][]string{{"team", "team_slug", "title", "tagline", "description", "video_url", "code_url", "has_screenshot", "updated_at",
		"status", "submitted_at", "submit_auto", "problem", "audience", "track", "tools", "maturity", "missing", "excluded"}}
	for _, e := range stored {
		t := byID[e.TeamID]
		track, err := h.Events.TeamTrackLookup(ctx, e.TeamID)
		if err != nil {
			return nil, err
		}
		check := entryapp.Evaluate(fields, e, h.Links.EntrySiteLive(ctx, ev, t.Slug, t.SiteTakenDownAt.Valid), len(tracks) > 0, track)
		trackName := ""
		if track != nil {
			trackName = track.Name
		}
		rows = append(rows, []string{
			t.Name, t.Slug, e.Title, e.Tagline, e.Description, e.VideoURL, e.CodeURL, strconv.FormatBool(e.HasScreenshot), core.RFC3339Time(e.UpdatedAt),
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

func (h *Handler) exportProjects(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	sites, ok := h.Links.Hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.Event, "")
}
func (h *Handler) exportOwnTeam(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	team, ok := core.ParticipantTeam(w, r, h.Teams, a)
	if !ok {
		return
	}
	sites, ok := h.Links.Hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	h.exportEventProjects(w, r, sites, a.Event, team.Slug)
}
func (h *Handler) eventUsage(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	u, err := h.Events.EventStorageLookup(r.Context(), a.Event)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	sites, ok := h.Links.Hooks.(hackAdministrationSites)
	if !ok {
		httpx.WriteInternal(w)
		return
	}
	u.FileBytes, err = sites.EventFileBytes(r.Context(), a.Event.AccountID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, u)
}

func (h *Handler) exportEventProjects(w http.ResponseWriter, r *http.Request, sites hackAdministrationSites, ev model.Event, ownSlug string) {
	teams, err := h.Teams.SiteTeams(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	slugs := make([]string, 0, len(teams))
	for _, team := range teams {
		slugs = append(slugs, team.Slug)
	}
	sites.ExportEventProjects(w, r, core.GroupRef(ev), slugs, ownSlug)
}
