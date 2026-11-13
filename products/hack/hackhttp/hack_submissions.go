package hackhttp

import (
	"context"
	"database/sql"
	"net/http"
	"sort"
	"strings"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
)

// An explicit public card keeps judging and organiser metadata out of this view.
type hackSubmission struct {
	hackResultCard
	TeamName      string `json:"team_name"`
	Team          string `json:"team"`
	ScreenshotURL string `json:"screenshot_url,omitempty"`
	teamID        string
}

func submissionsOpen(ev model.Event) bool {
	return !ev.TakenDown() && (ev.Stage == "judging" || ev.Stage == "results" || ev.Stage == "archived")
}

// visibleSubmissions is shared by participant reads, the public feed and page.
func visibleSubmissions(ctx context.Context, db *sql.DB, ev model.Event, siteURL func(string, string) string, screenshotBase string) ([]hackSubmission, error) {
	return submissionCards(ctx, db, ev, siteURL, screenshotBase, "")
}

// visibleSubmissionTeam returns the team ID behind one visible submission
// with a screenshot, or "" when that team's entry is not visible.
func visibleSubmissionTeam(ctx context.Context, db *sql.DB, ev model.Event, team string) (string, error) {
	cards, err := submissionCards(ctx, db, ev, nil, "", team)
	if err != nil || len(cards) == 0 || !cards[0].HasScreenshot {
		return "", err
	}
	return cards[0].teamID, nil
}

// submissionCards builds the cards; onlyTeam (a team slug) limits it to one team.
func submissionCards(ctx context.Context, db *sql.DB, ev model.Event, siteURL func(string, string) string, screenshotBase, onlyTeam string) ([]hackSubmission, error) {
	out := make([]hackSubmission, 0)
	if !submissionsOpen(ev) {
		return out, nil
	}
	es := entries.New(db, nil)
	stored, err := es.Entries(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	tt, err := teams.New(db).EventTeams(ctx, ev.ID)
	if err != nil {
		return nil, err
	}
	byTeam := make(map[string]model.EventEntry, len(stored))
	for _, e := range stored {
		byTeam[e.TeamID] = e
	}
	tracks := map[string]*model.EventTrack{}
	if onlyTeam == "" {
		if tracks, err = es.TeamTracks(ctx, ev.ID); err != nil {
			return nil, err
		}
	}
	live := map[string]string{}
	if siteURL != nil {
		cards, err := events.New(db).GalleryCards(ctx, ev.ID, ev.AccountID)
		if err != nil {
			return nil, err
		}
		for _, c := range cards {
			live[c.ID] = hackGalleryLink(siteURL, ev.Slug, c.Slug)
		}
	}
	codeOff := entries.LoadRules(ev.EntryRules)["code_url"] == "off"
	for _, t := range tt {
		if onlyTeam != "" && t.Slug != onlyTeam {
			continue
		}
		e, ok := byTeam[t.ID]
		if !ok || e.Status() != "submitted" || e.ExcludedAt.Valid {
			continue
		}
		code := e.CodeURL
		if codeOff {
			code = ""
		}
		c := hackSubmission{hackResultCard: hackResultCard{Title: e.Title, Tagline: e.Tagline, Problem: e.Problem, Audience: e.Audience, Tools: nonNilStrings(e.Tools), Maturity: e.Maturity, Track: tracks[t.ID], VideoURL: e.VideoURL, CodeURL: code, SiteURL: live[t.ID], HasScreenshot: e.HasScreenshot}, TeamName: t.Name, Team: t.Slug, teamID: t.ID}
		if e.VideoURL != "" {
			c.VideoHost = hackURLHost(e.VideoURL)
		}
		if code != "" {
			c.CodeHost = hackURLHost(code)
		}
		if c.HasScreenshot {
			c.ScreenshotURL = screenshotBase + "/" + t.Slug + "/screenshot"
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].TeamName) < strings.ToLower(out[j].TeamName) })
	return out, nil
}

func (h *HackHandler) submissionSiteURL() func(string, string) string {
	if h.sites == nil {
		return nil
	}
	return h.sites.TeamSiteURL
}

func (h *HackHandler) getSubmissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if a.event.TakenDown() {
		writeEventNotFound(w)
		return
	}
	if !submissionsOpen(a.event) {
		httpx.WriteError(w, 409, "submissions_not_open", "Submissions open to everyone when judging starts")
		return
	}
	cards, err := visibleSubmissions(r.Context(), h.database, a.event, h.submissionSiteURL(), "/v1/hack/events/"+a.event.Slug+"/submissions")
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"submissions": cards})
}

func (h *HackHandler) getSubmissionScreenshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	teamID, err := visibleSubmissionTeam(r.Context(), h.database, a.event, r.PathValue("team"))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if teamID != "" {
		h.serveTeamScreenshot(w, r, teamID)
		return
	}
	httpx.WriteError(w, 404, "team_not_found", "team not found")
}
