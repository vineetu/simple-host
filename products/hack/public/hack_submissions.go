package public

import (
	"context"
	"database/sql"
	"net/http"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/model"
)

// An explicit public card keeps judging and organiser metadata out of this view.
type hackSubmission struct {
	hackResultCard
	TeamName      string `json:"team_name"`
	Team          string `json:"team"`
	ScreenshotURL string `json:"screenshot_url,omitempty"`
	teamID        string
}

// visibleSubmissions is shared by participant reads, the public feed and page.
func VisibleSubmissions(ctx context.Context, db *sql.DB, ev model.Event, siteURL func(string, string) string, screenshotBase string) ([]hackSubmission, error) {
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

// submissionCards is entries.Submissions as public cards, with live site
// links when siteURL is given; onlyTeam (a team slug) limits it to one team.
func submissionCards(ctx context.Context, db *sql.DB, ev model.Event, siteURL func(string, string) string, screenshotBase, onlyTeam string) ([]hackSubmission, error) {
	subs, err := entries.New(db, nil).Submissions(ctx, ev, onlyTeam)
	if err != nil || len(subs) == 0 {
		return []hackSubmission{}, err
	}
	live := map[string]string{}
	if siteURL != nil {
		cards, err := events.New(db, nil).GalleryCards(ctx, ev.ID, ev.AccountID)
		if err != nil {
			return nil, err
		}
		for _, c := range cards {
			live[c.ID] = core.GalleryLink(siteURL, ev.Slug, c.Slug)
		}
	}
	out := make([]hackSubmission, 0, len(subs))
	for _, sub := range subs {
		out = append(out, submissionCard(sub, live[sub.Team.ID], screenshotBase))
	}
	return out, nil
}

func submissionCard(sub entries.Submission, siteURL, screenshotBase string) hackSubmission {
	e, t := sub.Entry, sub.Team
	c := hackSubmission{hackResultCard: hackResultCard{Title: e.Title, Tagline: e.Tagline, Problem: e.Problem, Audience: e.Audience, Tools: core.NonNilStrings(e.Tools), Maturity: e.Maturity, Track: sub.Track, VideoURL: e.VideoURL, CodeURL: e.CodeURL, SiteURL: siteURL, HasScreenshot: e.HasScreenshot}, TeamName: t.Name, Team: t.Slug, teamID: t.ID}
	if e.VideoURL != "" {
		c.VideoHost = core.URLHost(e.VideoURL)
	}
	if e.CodeURL != "" {
		c.CodeHost = core.URLHost(e.CodeURL)
	}
	if c.HasScreenshot {
		c.ScreenshotURL = screenshotBase + "/" + t.Slug + "/screenshot"
	}
	return c
}

func (h *handler) getSubmissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if a.Event.TakenDown() {
		core.WriteEventNotFound(w)
		return
	}
	if !entries.SubmissionsOpen(a.Event) {
		httpx.WriteError(w, 409, "submissions_not_open", "Submissions open to everyone when judging starts")
		return
	}
	cards, err := VisibleSubmissions(r.Context(), h.DB, a.Event, h.Links.SubmissionSiteURL(), "/v1/hack/events/"+a.Event.Slug+"/submissions")
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"submissions": cards})
}

func (h *handler) getSubmissionScreenshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	teamID, err := visibleSubmissionTeam(r.Context(), h.DB, a.Event, r.PathValue("team"))
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if teamID != "" {
		h.ServeTeamScreenshot(w, r, teamID)
		return
	}
	httpx.WriteError(w, 404, "team_not_found", "team not found")
}
