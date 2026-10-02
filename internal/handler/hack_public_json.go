package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
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
		writeHackErr(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
		return
	}
	ev, err := db.GetEventBySlug(r.Context(), h.database, strings.ToLower(r.PathValue("slug")))
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	out := map[string]any{
		"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline,
		"about": ev.About, "rules": ev.Rules, "prizes": ev.Prizes,
		"organiser_name": ev.OrganiserName, "organisation": ev.Organisation,
		"stage": ev.Stage, "time_zone": ev.TimeZone,
		"starts_at": rfc3339UTC(ev.StartsAt), "ends_at": rfc3339UTC(ev.EndsAt),
		"submission_deadline": rfc3339UTC(ev.SubmissionDeadline),
		"url":                 h.EventURL(ev.Slug), "builtin_url": strings.TrimRight(h.publicBaseURL, "/") + "/e/" + ev.Slug,
		"icon_url": h.iconURL(ev),
		"gallery":  []any{}, "results": []any{}, "vote_ranking": []any{},
		"vote_status": "disabled", "tracks": []any{}, "announcements": []any{},
		"sponsors": []any{}, "faq": []any{}, "schedule": []any{},
	}
	if n, t, _, err := db.CountEventMembers(r.Context(), h.database, ev.ID); err == nil {
		out["participants"], out["teams"] = n, t
	} else {
		writeInternal(w)
		return
	}
	tracks, err := db.ListEventTracks(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	tr := make([]any, 0, len(tracks))
	for _, v := range tracks {
		tr = append(tr, map[string]any{"name": v.Name, "challenge": v.Challenge, "prize": v.Prize})
	}
	out["tracks"] = tr
	content, err := readHackContent(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
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
	out["sponsors"], out["faq"], out["schedule"] = sponsors, content.FAQ, content.Schedule
	announcements, err := db.ListEventAnnouncements(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	aa := make([]any, 0, len(announcements))
	for _, a := range announcements {
		aa = append(aa, map[string]any{"title": a.Title, "body": a.Body, "created_at": a.CreatedAt.UTC().Format(time.RFC3339)})
	}
	out["announcements"] = aa
	if h.sites != nil && hackGalleryPublic(ev, h.sites.TeamSitesReady) {
		cards, err := db.ListGalleryCards(r.Context(), h.database, ev.ID, ev.AccountID)
		if err != nil {
			writeInternal(w)
			return
		}
		gg := make([]any, 0, len(cards))
		for _, c := range hackGalleryCards(ev.Slug, cards, h.sites.TeamSiteURL) {
			item := map[string]any{"title": c.Title, "tagline": c.Tagline, "team": c.Team, "url": c.URL}
			if c.Shot != "" {
				item["screenshot_url"] = strings.TrimRight(h.publicBaseURL, "/") + "/e/" + ev.Slug + c.Shot
			}
			gg = append(gg, item)
		}
		out["gallery"] = gg
	}
	res, err := db.GetEventResults(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if res.Published {
		if view := hackResultsFromSnapshot(res, ev.PublicScores, ev.PublicRanks); view != nil {
			rr := make([]any, 0, len(view.Rows))
			for _, row := range view.Rows {
				item := map[string]any{"team_name": row.TeamName, "track_name": row.TrackName, "track_prize": row.TrackPrize, "track_winner": row.TrackWinner}
				if ev.PublicRanks {
					item["rank"], item["tied"] = row.Rank, row.Tied
				}
				if ev.PublicScores {
					item["score"], item["score_mode"] = row.Score, row.ScoreMode
				}
				rr = append(rr, item)
			}
			out["results"], out["results_published"] = rr, true
			out["full_ranking"] = view.FullRanking
		}
	}
	voting, err := db.GetVotingSettings(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	status := votingStatus(ev, voting, time.Now())
	out["vote_status"] = status
	if status == "closed" {
		ranking, err := db.VoteFinalRanking(r.Context(), h.database, ev.ID, ev.AccountID)
		if err != nil {
			writeInternal(w)
			return
		}
		vv := make([]any, 0, len(ranking))
		for _, v := range ranking {
			vv = append(vv, map[string]any{"team": v.Slug, "team_name": v.Name, "votes": v.Count})
		}
		out["vote_ranking"] = vv
	}
	writeJSON(w, http.StatusOK, out)
}
