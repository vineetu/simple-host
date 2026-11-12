package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
)

// PublicEventJSON is a credentialless, read-only feed for an event's custom
// website. It exposes only the information already eligible for public pages.
func (h *HackHandler) PublicEventJSON(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	ev, err := h.eventsSvc().EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
	if err == nil {
		paperHackEvent(r, ev)
	}
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	out := map[string]any{
		"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline,
		"about": ev.About, "rules": ev.Rules, "prizes": ev.Prizes, "overall_prizes": append([]string{}, ev.OverallPrizes...),
		"organiser_name": ev.OrganiserName, "organisation": ev.Organisation,
		"stage": ev.Stage, "time_zone": ev.TimeZone,
		"starts_at": rfc3339UTC(ev.StartsAt), "ends_at": rfc3339UTC(ev.EndsAt),
		"submission_deadline": rfc3339UTC(ev.SubmissionDeadline),
		"url":                 h.EventURL(ev.Slug), "builtin_url": strings.TrimRight(h.publicBaseURL, "/") + "/e/" + ev.Slug,
		"icon_url": h.iconURL(ev), "color": hackEventAccent(ev), "theme": ev.Theme,
		"gallery": []any{}, "results": []any{}, "vote_ranking": []any{},
		"vote_status": "disabled", "tracks": []any{}, "announcements": []any{},
		"sponsors": []any{}, "faq": []any{}, "schedule": []any{}, "fixed_schedule": []any{},
	}
	if open := events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()); open {
		out["applications_open"], out["apply_url"] = true, strings.TrimRight(h.publicBaseURL, "/")+"/e/"+ev.Slug+"/apply"
	} else {
		out["applications_open"] = false
	}
	if n, t, _, err := h.eventsSvc().CountEventMembersLookup(r.Context(), ev.ID); err == nil {
		out["participants"], out["teams"] = n, t
	} else {
		httpx.WriteInternal(w)
		return
	}
	tracks, err := h.eventsSvc().EventTracks(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	tr := make([]any, 0, len(tracks))
	for _, v := range tracks {
		tr = append(tr, map[string]any{"name": v.Name, "challenge": v.Challenge, "prize": v.Prize})
	}
	out["tracks"] = tr
	content, err := readHackContent(r.Context(), h.database, ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	sponsors := make([]any, 0, len(content.Sponsors))
	for _, s := range content.Sponsors {
		item := map[string]any{"name": s.Name, "tier": s.Tier, "url": s.URL}
		if cleanLogo(s.LogoData) {
			item["logo_data"] = s.LogoData
		}
		sponsors = append(sponsors, item)
	}
	out["sponsors"], out["faq"], out["schedule"] = sponsors, content.FAQ, hackPublicSchedule(content.Schedule)
	// The event's real times as fixed schedule rows (hack_schedule.go), the
	// same ones the built-in page lists.
	fixed, err := h.fixedScheduleFor(r.Context(), ev, nil, false)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out["fixed_schedule"] = fixed
	announcements, err := h.eventsSvc().EventAnnouncements(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	aa := make([]any, 0, len(announcements))
	for _, a := range announcements {
		aa = append(aa, map[string]any{"title": a.Title, "body": a.Body, "body_html": a.BodyHTML, "pinned": a.Pinned, "created_at": a.CreatedAt.UTC().Format(time.RFC3339)})
	}
	out["announcements"] = aa
	if h.sites != nil && hackGalleryPublic(ev, h.sites.TeamSitesReady) {
		cards, err := h.eventsSvc().GalleryCards(r.Context(), ev.ID, ev.AccountID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		gg := make([]any, 0, len(cards))
		for _, c := range hackGalleryCards(ev.Slug, cards, h.sites.TeamSiteURL) {
			item := map[string]any{"title": c.Title, "tagline": c.Tagline, "team": c.Team, "url": c.URL, "tools": nonNilStrings(c.Tools)}
			if c.Shot != "" {
				item["screenshot_url"] = strings.TrimRight(h.publicBaseURL, "/") + "/e/" + ev.Slug + c.Shot
			}
			gg = append(gg, item)
		}
		out["gallery"] = gg
	}
	res, err := h.entries.EventResults(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	if res.Published {
		if view := hackResultsFromSnapshot(res, ev.PublicScores, ev.PublicRanks, ev.OverallPrizes); view != nil {
			live := h.liveResultSites(r.Context(), ev)
			rr := make([]any, 0, len(view.Rows))
			for _, row := range view.Rows {
				item := map[string]any{"team_name": row.TeamName, "track_name": row.TrackName, "track_prize": row.TrackPrize, "track_winner": row.TrackWinner, "overall": row.Overall, "overall_prize": row.OverallPrize}
				if ev.PublicRanks {
					item["rank"], item["tied"] = row.Rank, row.Tied
				}
				if ev.PublicScores {
					item["score"], item["score_mode"] = row.Score, row.ScoreMode
				}
				if row.Card != nil {
					card := *row.Card
					card.SiteURL = live[row.TeamID]
					item["entry"] = card
				}
				rr = append(rr, item)
			}
			out["results"], out["results_published"] = rr, true
			out["full_ranking"] = view.FullRanking
		}
	}
	voting, err := h.entries.VotingSettings(r.Context(), ev.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	status := votingStatus(ev, voting, time.Now())
	out["vote_status"] = status
	if status == "closed" {
		ranking, err := h.entries.VoteFinalRankingLookup(r.Context(), ev.ID, ev.AccountID)
		if err != nil {
			httpx.WriteInternal(w)
			return
		}
		vv := make([]any, 0, len(ranking))
		for _, v := range ranking {
			vv = append(vv, map[string]any{"team": v.Slug, "team_name": v.Name, "votes": v.Count})
		}
		out["vote_ranking"] = vv
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}
