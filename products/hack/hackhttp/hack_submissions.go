package hackhttp

import (
	"context"
	"database/sql"
	"net/http"

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

// submissionCards is entries.Submissions as public cards, with live site
// links when siteURL is given; onlyTeam (a team slug) limits it to one team.
func submissionCards(ctx context.Context, db *sql.DB, ev model.Event, siteURL func(string, string) string, screenshotBase, onlyTeam string) ([]hackSubmission, error) {
	subs, err := entries.New(db, nil).Submissions(ctx, ev, onlyTeam)
	if err != nil || len(subs) == 0 {
		return []hackSubmission{}, err
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
	out := make([]hackSubmission, 0, len(subs))
	for _, sub := range subs {
		out = append(out, submissionCard(sub, live[sub.Team.ID], screenshotBase))
	}
	return out, nil
}

// submissionCard is one submission's public card.
func submissionCard(sub entries.Submission, siteURL, screenshotBase string) hackSubmission {
	e, t := sub.Entry, sub.Team
	c := hackSubmission{hackResultCard: hackResultCard{Title: e.Title, Tagline: e.Tagline, Problem: e.Problem, Audience: e.Audience, Tools: nonNilStrings(e.Tools), Maturity: e.Maturity, Track: sub.Track, VideoURL: e.VideoURL, CodeURL: e.CodeURL, SiteURL: siteURL, HasScreenshot: e.HasScreenshot}, TeamName: t.Name, Team: t.Slug, teamID: t.ID}
	if e.VideoURL != "" {
		c.VideoHost = hackURLHost(e.VideoURL)
	}
	if e.CodeURL != "" {
		c.CodeHost = hackURLHost(e.CodeURL)
	}
	if c.HasScreenshot {
		c.ScreenshotURL = screenshotBase + "/" + t.Slug + "/screenshot"
	}
	return c
}

func (h *siteLinks) submissionSiteURL() func(string, string) string {
	if h.hooks == nil {
		return nil
	}
	return h.hooks.TeamSiteURL
}

func (h *participationHTTP) getSubmissions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if a.event.TakenDown() {
		writeEventNotFound(w)
		return
	}
	if !entries.SubmissionsOpen(a.event) {
		httpx.WriteError(w, 409, "submissions_not_open", "Submissions open to everyone when judging starts")
		return
	}
	cards, err := visibleSubmissions(r.Context(), h.database, a.event, h.links.submissionSiteURL(), "/v1/hack/events/"+a.event.Slug+"/submissions")
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"submissions": cards})
}

func (h *participationHTTP) getSubmissionScreenshot(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
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
