package public

import (
	"context"
	"net/http"
	"strings"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

// PublicEventJSON is a credentialless, read-only feed for an event's custom
// website. It exposes only the information already eligible for public pages.
func (h *handler) PublicEventJSON(w http.ResponseWriter, r *http.Request) {
	if !publicFeedPreflight(w, r) {
		return
	}
	ev, err := h.Events.EventBySlug(r.Context(), strings.ToLower(r.PathValue("slug")))
	if err == nil {
		core.PaperEvent(r, ev)
		if ev.TakenDown() {
			core.WriteEventNotFound(w)
			return
		}
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	out, err := h.publicEventFeed(r.Context(), ev)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// publicFeedPreflight sets the feed's open CORS and no-store headers and
// answers OPTIONS and wrong methods itself; true when the read should go on.
func publicFeedPreflight(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
	w.Header().Set("Cache-Control", "no-store")
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		return true
	case http.MethodOptions:
		w.WriteHeader(http.StatusNoContent)
	default:
		w.Header().Set("Allow", "GET, HEAD, OPTIONS")
		httpx.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET only")
	}
	return false
}

// publicEventFeed is the whole feed: the event's own fields, then each part
// read in turn. Any part failing fails the feed.
func (h *handler) publicEventFeed(ctx context.Context, ev hackmodel.Event) (map[string]any, error) {
	out := h.publicEventFields(ev)
	parts := []func(context.Context, hackmodel.Event, map[string]any) error{
		h.feedSubmissions, h.feedCounts, h.feedTracks, h.feedContent, h.feedFixedSchedule,
		h.feedAnnouncements, h.feedGallery, h.feedResults, h.feedVoting,
	}
	for _, part := range parts {
		if err := part(ctx, ev, out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (h *handler) publicEventPath(ev hackmodel.Event, suffix string) string {
	return strings.TrimRight(h.Links.PublicBaseURL, "/") + "/e/" + ev.Slug + suffix
}

// publicEventFields are the feed's fields that come straight from the event,
// with empty lists for the parts that are only filled when public.
func (h *handler) publicEventFields(ev hackmodel.Event) map[string]any {
	out := map[string]any{
		"submissions_public": ev.SubmissionsPublic,
		"slug":               ev.Slug, "title": ev.Title, "tagline": ev.Tagline,
		"about": ev.About, "rules": ev.Rules, "prizes": ev.Prizes, "overall_prizes": append([]string{}, ev.OverallPrizes...),
		"organiser_name": ev.OrganiserName, "organisation": ev.Organisation,
		"stage": ev.Stage, "time_zone": ev.TimeZone,
		"starts_at": core.RFC3339(ev.StartsAt), "ends_at": core.RFC3339(ev.EndsAt),
		"submission_deadline": core.RFC3339(ev.SubmissionDeadline),
		"url":                 h.Links.EventURL(ev.Slug), "builtin_url": h.publicEventPath(ev, ""),
		"icon_url": h.Links.IconURL(ev), "color": core.EventAccent(ev), "theme": ev.Theme,
		"gallery": []any{}, "results": []any{}, "vote_ranking": []any{},
		"applications_open": false,
	}
	if events.ApplicationsOpenNow(ev.ApplicationsOpen, ev.DirectoryListed, ev.Stage, ev.TakenDown()) {
		out["applications_open"], out["apply_url"] = true, h.publicEventPath(ev, "/apply")
	}
	return out
}

// feedSubmissions are the visible submissions when the organiser made them public.
func (h *handler) feedSubmissions(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	out["submissions"] = []hackSubmission{}
	if !ev.SubmissionsPublic {
		return nil
	}
	subs, err := VisibleSubmissions(ctx, h.DB, ev, h.Links.SubmissionSiteURL(), h.publicEventPath(ev, "/submissions"))
	out["submissions"] = subs
	return err
}

func (h *handler) feedCounts(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	n, t, _, err := h.Events.CountEventMembersLookup(ctx, ev)
	out["participants"], out["teams"] = n, t
	return err
}

func (h *handler) feedTracks(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	tracks, err := h.Events.EventTracks(ctx, ev.ID)
	tr := make([]any, 0, len(tracks))
	for _, v := range tracks {
		tr = append(tr, map[string]any{"name": v.Name, "challenge": v.Challenge, "prize": v.Prize})
	}
	out["tracks"] = tr
	return err
}

// feedContent is the sponsors (logo only when clean), FAQ and the editable
// schedule without the organiser's prompts.
func (h *handler) feedContent(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	content, err := h.Events.EventContent(ctx, ev.ID)
	sponsors := make([]any, 0, len(content.Sponsors))
	for _, s := range content.Sponsors {
		item := map[string]any{"name": s.Name, "tier": s.Tier, "url": s.URL}
		if events.LogoValid(s.LogoData) {
			item["logo_data"] = s.LogoData
		}
		sponsors = append(sponsors, item)
	}
	out["sponsors"], out["faq"], out["schedule"] = sponsors, content.FAQ, core.PublicSchedule(content.Schedule)
	return err
}

// feedFixedSchedule is the event's real times as fixed schedule rows
// (hack_schedule.go), the same ones the built-in page lists.
func (h *handler) feedFixedSchedule(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	fixed, err := h.FixedScheduleFor(ctx, ev, nil, false)
	out["fixed_schedule"] = fixed
	return err
}

func (h *handler) feedAnnouncements(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	anns, err := h.Events.EventAnnouncements(ctx, ev.ID)
	aa := make([]any, 0, len(anns))
	for _, a := range anns {
		aa = append(aa, map[string]any{"title": a.Title, "body": a.Body, "body_html": a.BodyHTML, "pinned": a.Pinned, "created_at": a.CreatedAt.UTC().Format(time.RFC3339)})
	}
	out["announcements"] = aa
	return err
}

// feedGallery is the live team sites, only while the gallery is public.
func (h *handler) feedGallery(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	if h.Links.Hooks == nil || !hackGalleryPublic(ev, h.Links.Hooks.TeamSitesReady) {
		return nil
	}
	cards, err := h.Events.GalleryCards(ctx, ev.ID, ev.AccountID)
	gg := make([]any, 0, len(cards))
	for _, c := range hackGalleryCards(ev.Slug, cards, h.Links.Hooks.TeamSiteURL) {
		item := map[string]any{"title": c.Title, "tagline": c.Tagline, "team": c.Team, "url": c.URL, "tools": core.NonNilStrings(c.Tools)}
		if c.Shot != "" {
			item["screenshot_url"] = h.publicEventPath(ev, c.Shot)
		}
		gg = append(gg, item)
	}
	out["gallery"] = gg
	return err
}

// feedResults is the published results, as public as the organiser chose,
// with each card's site link taken from the sites live right now.
func (h *handler) feedResults(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	res, err := h.Entries.EventResults(ctx, ev.ID)
	if err != nil || !res.Published {
		return err
	}
	view := hackResultsFromSnapshot(res, ev.PublicScores, ev.PublicRanks, ev.OverallPrizes)
	if view == nil {
		return nil
	}
	out["results"] = publicResultRows(view.Rows, ev.PublicRanks, ev.PublicScores, h.Links.LiveResultSites(ctx, h.Events, ev))
	out["results_published"], out["full_ranking"] = true, view.FullRanking
	return nil
}

// publicResultRows are the results rows on the wire: rank and score only
// when public, the card with its live site link.
func publicResultRows(rows []hackResultsRow, publicRanks, publicScores bool, live map[string]string) []any {
	rr := make([]any, 0, len(rows))
	for _, row := range rows {
		item := map[string]any{"team_name": row.TeamName, "track_name": row.TrackName, "track_prize": row.TrackPrize, "track_winner": row.TrackWinner, "overall": row.Overall, "overall_prize": row.OverallPrize}
		if publicRanks {
			item["rank"], item["tied"] = row.Rank, row.Tied
		}
		if publicScores {
			item["score"], item["score_mode"] = row.Score, row.ScoreMode
		}
		if row.Card != nil {
			card := *row.Card
			card.SiteURL = live[row.TeamID]
			item["entry"] = card
		}
		rr = append(rr, item)
	}
	return rr
}

// feedVoting is the people's choice status, and the final ranking once closed.
func (h *handler) feedVoting(ctx context.Context, ev hackmodel.Event, out map[string]any) error {
	voting, err := h.Entries.VotingSettings(ctx, ev.ID)
	if err != nil {
		return err
	}
	status := core.VotingStatus(ev, voting, time.Now())
	out["vote_status"] = status
	if status != "closed" {
		return nil
	}
	ranking, err := h.Entries.VoteFinalRankingLookup(ctx, ev.ID, ev.AccountID)
	vv := make([]any, 0, len(ranking))
	for _, v := range ranking {
		vv = append(vv, map[string]any{"team": v.Slug, "team_name": v.Name, "votes": v.Count})
	}
	out["vote_ranking"] = vv
	return err
}
