package hackhttp

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
)

// votingStatus keeps the existing handler callers on the shared voting rule.
func votingStatus(ev model.Event, s model.VotingSettings, now time.Time) string {
	return entries.VotingStatus(ev, entries.VotingSettingsValue(s.Enabled, s.OpensAt, s.ClosesAt, s.Eligibility), now)
}

func votingSettingsJSON(s entries.VotingConfig) map[string]any {
	return map[string]any{
		"enabled": s.Enabled, "opens_at": rfc3339UTC(s.OpensAt),
		"closes_at": rfc3339UTC(s.ClosesAt), "eligibility": s.Eligibility,
		"directory_listed": s.DirectoryListed,
	}
}

func (h *HackHandler) getVotingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	result, err := h.entries.ReadVotingSettings(r.Context(), a.event, time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := votingSettingsJSON(result.Settings)
	out["status"] = result.Status
	out["votes_to_remove"] = result.Counts
	httpx.WriteJSON(w, 200, out)
}

func (h *HackHandler) putVotingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		ConfirmVoteRemoval bool   `json:"confirm_vote_removal"`
		Enabled            bool   `json:"enabled"`
		OpensAt            string `json:"opens_at"`
		ClosesAt           string `json:"closes_at"`
		Eligibility        string `json:"eligibility"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if err := entries.ValidateVotingEligibility(req.Eligibility); err != nil {
		e := err.(*entries.Error)
		httpx.WriteError(w, 400, e.Code, e.Message)
		return
	}
	loc, err := time.LoadLocation(a.event.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	var opens, closes sql.NullTime
	if strings.TrimSpace(req.OpensAt) != "" {
		opens, ok = parseEventTime(w, req.OpensAt, loc, "opens_at", true)
		if !ok {
			return
		}
	}
	if strings.TrimSpace(req.ClosesAt) != "" {
		closes, ok = parseEventTime(w, req.ClosesAt, loc, "closes_at", true)
		if !ok {
			return
		}
	}
	if err := entries.ValidateVotingWindow(entries.VotingSettingsValue(req.Enabled, opens, closes, req.Eligibility)); err != nil {
		e := err.(*entries.Error)
		httpx.WriteError(w, 400, e.Code, e.Message)
		return
	}
	ev, s, removed, err := h.entries.SaveVotingSettings(r.Context(), a.event.ID, entries.VotingSettingsValue(req.Enabled, opens, closes, req.Eligibility), req.ConfirmVoteRemoval)
	if err != nil {
		switch e := err.(type) {
		case *entries.VoteRemovalConfirmation:
			httpx.WriteJSON(w, 409, map[string]any{"code": "vote_removal_confirmation", "error": "Confirm removal of earlier votes.", "votes_to_remove": e.Count})
		case *entries.Error:
			httpx.WriteError(w, 409, e.Code, e.Message)
		default:
			httpx.WriteInternal(w)
		}
		return
	}
	out := votingSettingsJSON(s)
	out["status"] = entries.VotingStatus(ev, s, time.Now())
	out["votes_removed"] = removed
	httpx.WriteJSON(w, 200, out)
}

func (h *HackHandler) patchDirectoryListed(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	if !entries.CanChangeDirectory(a.admin, !a.member.JoinedAt.IsZero()) {
		writeEventNotFound(w)
		return
	}
	var req struct {
		Listed *bool `json:"listed"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Listed == nil {
		httpx.WriteError(w, 400, "invalid_directory_listed", "listed must be true or false")
		return
	}
	if err := h.entries.SetDirectoryListed(r.Context(), a.event.ID, *req.Listed); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"listed": *req.Listed})
}

func (h *HackHandler) getVote(w http.ResponseWriter, r *http.Request) {
	ev, loaded, err := h.entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		paperHackEvent(r, ev)
	}
	if e, ok := err.(*entries.Error); ok && e.Code == "event_not_found" {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	result, err := h.entries.ReadPublicVote(r.Context(), ev, time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	s, status := result.Settings, result.Status
	out := map[string]any{"event": ev.Slug, "title": ev.Title, "status": status,
		"opens_at": rfc3339UTC(s.OpensAt), "closes_at": rfc3339UTC(s.ClosesAt),
		// time_zone lets the public vote page show the window in the
		// event's own zone instead of the visitor's browser zone (Grok
		// SIM-346 S5).
		"time_zone": ev.TimeZone, "eligibility": s.Eligibility, "options": []any{}, "ranking": []any{}}
	if status != "disabled" {
		options := result.Options
		items := make([]map[string]any, 0, len(options))
		for _, v := range options {
			title := v.Title
			if title == "" {
				title = v.Name
			}
			items = append(items, map[string]any{"team": v.Slug, "team_name": v.Name,
				"title": title, "tagline": v.Tagline, "url": h.teamSiteURL(ev.Slug, v.Slug)})
		}
		out["options"] = items
		if status == "closed" {
			final := result.Ranking
			ranking := make([]map[string]any, 0, len(final))
			for _, v := range final {
				ranking = append(ranking, map[string]any{"team": v.Slug, "team_name": v.Name, "votes": v.Count})
			}
			out["ranking"] = ranking
		}
	}
	httpx.WriteJSON(w, 200, out)
}

func (h *HackHandler) getMyVote(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	ev, loaded, err := h.entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		paperHackEvent(r, ev)
	}
	if e, ok := err.(*entries.Error); ok && e.Code == "event_not_found" {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	_, selected, eligible, err := h.entries.ReadMyVote(r.Context(), ev, user.Username, user.IsAdmin)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"team": selected, "eligible": eligible})
}

func (h *HackHandler) putVote(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if !entries.CanCastVote(user.Username, user.IsAdmin) {
		httpx.WriteError(w, 403, "vote_ineligible", "sign in with a personal account to vote")
		return
	}
	var req struct {
		Team string `json:"team"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if !entries.ValidVoteTeam(req.Team) {
		httpx.WriteError(w, 400, "invalid_team", "choose a team from the voting page")
		return
	}
	ev, loaded, err := h.entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		paperHackEvent(r, ev)
	}
	if e, ok := err.(*entries.Error); ok && e.Code == "event_not_found" {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	created, err := h.entries.CastVote(r.Context(), ev.ID, ev.AccountID, model.BaseEmail(user.Username), req.Team)
	if err != nil {
		if e, ok := err.(*entries.Error); ok {
			status := 0
			switch e.Code {
			case "voting_closed", "own_team_vote", "duplicate_vote":
				status = 409
			case "vote_ineligible":
				status = 403
			case "team_not_found":
				status = 404
			}
			if status == 0 {
				httpx.WriteInternal(w)
			} else {
				httpx.WriteError(w, status, e.Code, e.Message)
			}
		} else {
			httpx.WriteInternal(w)
		}
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"team": req.Team, "changed": !created})
}

// getDirectory serves the public directory groups. The grouping and the
// applications_open rule now live in internal/app/hack/events (Directory)
// and the rules it calls; this handler keeps transport: it calls the
// service, then builds the exact same JSON, including the url/icon_url/
// color/apply_url fields it computes from its own config (cleanup 3.2
// slice 4).
func (h *HackHandler) getDirectory(w http.ResponseWriter, r *http.Request) {
	dir, err := h.events.Directory(r.Context(), time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	groups := map[string][]map[string]any{
		"now":      hackDirectoryRows(h, dir.Now),
		"upcoming": hackDirectoryRows(h, dir.Upcoming),
		"past":     hackDirectoryRows(h, dir.Past),
	}
	httpx.WriteJSON(w, 200, groups)
}

// hackDirectoryRows builds getDirectory's JSON rows for one group: the
// service's typed Row plus the url/icon_url/color/apply_url fields only the
// transport can compute.
func hackDirectoryRows(h *HackHandler, rows []events.Row) []map[string]any {
	out := []map[string]any{}
	for _, ev := range rows {
		row := map[string]any{
			"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline, "stage": ev.Stage,
			"time_zone": ev.TimeZone, "starts_at": rfc3339UTC(ev.StartsAt), "ends_at": rfc3339UTC(ev.EndsAt),
			"url": h.EventURL(ev.Slug), "icon_url": h.iconURLSlug(ev.Slug), "color": hackEventColor(ev.Slug, ev.AccentColor),
		}
		row["applications_open"] = ev.ApplicationsOpen
		if ev.ApplicationsOpen {
			row["apply_url"] = strings.TrimRight(h.publicBaseURL, "/") + "/e/" + ev.Slug + "/apply"
		}
		out = append(out, row)
	}
	return out
}
