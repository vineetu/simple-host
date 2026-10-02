package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/internal/db"
)

func votingStatus(ev db.Event, s db.VotingSettings, now time.Time) string {
	if !s.Enabled || !ev.GalleryOpen || ev.Stage == "draft" || ev.TakenDown() {
		return "disabled"
	}
	if !s.OpensAt.Valid || !s.ClosesAt.Valid {
		return "disabled"
	}
	if !now.Before(s.ClosesAt.Time) {
		return "closed"
	}
	if ev.Stage == "archived" {
		return "disabled"
	}
	if now.Before(s.OpensAt.Time) {
		return "upcoming"
	}
	return "open"
}

func votingSettingsJSON(s db.VotingSettings) map[string]any {
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
	s, err := db.GetVotingSettings(r.Context(), h.database, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	out := votingSettingsJSON(s)
	out["status"] = votingStatus(a.event, s, time.Now())
	writeJSON(w, 200, out)
}

func (h *HackHandler) putVotingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		Enabled     bool   `json:"enabled"`
		OpensAt     string `json:"opens_at"`
		ClosesAt    string `json:"closes_at"`
		Eligibility string `json:"eligibility"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if req.Eligibility != "all_signed_in" && req.Eligibility != "event_members" && req.Eligibility != "participants" {
		writeHackErr(w, 400, "invalid_voting_eligibility", "choose all_signed_in, event_members or participants")
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
	if req.Enabled && (!opens.Valid || !closes.Valid || !closes.Time.After(opens.Time)) {
		writeHackErr(w, 400, "invalid_voting_window", "opening and closing times are required, with closing after opening")
		return
	}
	tx, err := h.database.BeginTx(r.Context(), nil)
	if err != nil {
		writeInternal(w)
		return
	}
	defer tx.Rollback()
	ev, err := db.GetEventForUpdate(r.Context(), tx, a.event.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.Stage == "archived" {
		writeHackErr(w, 409, "event_closed", "an ended event cannot be changed")
		return
	}
	s, err := db.GetVotingSettings(r.Context(), tx, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	if req.Enabled && !ev.GalleryOpen {
		writeHackErr(w, 409, "gallery_closed", "open the project gallery before opening voting")
		return
	}
	s.Enabled, s.OpensAt, s.ClosesAt, s.Eligibility = req.Enabled, opens, closes, req.Eligibility
	if err = db.SaveVotingSettings(r.Context(), tx, ev.ID, s); err != nil {
		writeInternal(w)
		return
	}
	if err = tx.Commit(); err != nil {
		writeInternal(w)
		return
	}
	out := votingSettingsJSON(s)
	out["status"] = votingStatus(ev, s, time.Now())
	writeJSON(w, 200, out)
}

func (h *HackHandler) patchDirectoryListed(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	if a.admin && a.member.JoinedAt.IsZero() {
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
		writeHackErr(w, 400, "invalid_directory_listed", "listed must be true or false")
		return
	}
	if err := db.SetDirectoryListed(r.Context(), h.database, a.event.ID, *req.Listed); err != nil {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"listed": *req.Listed})
}

func (h *HackHandler) getVote(w http.ResponseWriter, r *http.Request) {
	ev, err := db.GetEventBySlug(r.Context(), h.database, r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	s, err := db.GetVotingSettings(r.Context(), h.database, ev.ID)
	if err != nil {
		writeInternal(w)
		return
	}
	status := votingStatus(ev, s, time.Now())
	out := map[string]any{"event": ev.Slug, "title": ev.Title, "status": status,
		"opens_at": rfc3339UTC(s.OpensAt), "closes_at": rfc3339UTC(s.ClosesAt),
		"eligibility": s.Eligibility, "options": []any{}, "ranking": []any{}}
	if status != "disabled" {
		options, err := db.VoteOptions(r.Context(), h.database, ev.ID, ev.AccountID)
		if err != nil {
			writeInternal(w)
			return
		}
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
			final, err := db.VoteFinalRanking(r.Context(), h.database, ev.ID, ev.AccountID)
			if err != nil {
				writeInternal(w)
				return
			}
			ranking := make([]map[string]any, 0, len(final))
			for _, v := range final {
				ranking = append(ranking, map[string]any{"team": v.Slug, "team_name": v.Name, "votes": v.Count})
			}
			out["ranking"] = ranking
		}
	}
	writeJSON(w, 200, out)
}

func (h *HackHandler) getMyVote(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	ev, err := db.GetEventBySlug(r.Context(), h.database, r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	selected, err := db.OwnVote(r.Context(), h.database, ev.ID, db.BaseEmail(user.Username))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"team": selected})
}

func (h *HackHandler) putVote(w http.ResponseWriter, r *http.Request) {
	user := hackNeedUser(w, r)
	if user == nil {
		return
	}
	if user.IsAdmin || user.Username == "" {
		writeHackErr(w, 403, "vote_ineligible", "sign in with a personal account to vote")
		return
	}
	var req struct {
		Team string `json:"team"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	if !hackGallerySlug(req.Team) {
		writeHackErr(w, 400, "invalid_team", "choose a team from the voting page")
		return
	}
	ev, err := db.GetEventBySlug(r.Context(), h.database, r.PathValue("slug"))
	if errors.Is(err, sql.ErrNoRows) {
		writeEventNotFound(w)
		return
	}
	if err != nil {
		writeInternal(w)
		return
	}
	if ev.Stage == "draft" || ev.TakenDown() {
		writeEventNotFound(w)
		return
	}
	created, err := db.CastVote(r.Context(), h.database, ev.ID, ev.AccountID, db.BaseEmail(user.Username), req.Team)
	switch err {
	case nil:
	case db.ErrVoteClosed:
		writeHackErr(w, 409, "voting_closed", "voting is not open")
		return
	case db.ErrVoteIneligible:
		writeHackErr(w, 403, "vote_ineligible", "this account is not eligible to vote")
		return
	case db.ErrVoteOwnTeam:
		writeHackErr(w, 409, "own_team_vote", "you cannot vote for your own team")
		return
	case db.ErrVoteTeamNotFound:
		writeHackErr(w, 404, "team_not_found", "choose a live team project")
		return
	case db.ErrVoteDuplicate:
		writeHackErr(w, 409, "duplicate_vote", "you already voted for this team; choose another to change your vote")
		return
	default:
		writeInternal(w)
		return
	}
	writeJSON(w, 200, map[string]any{"team": req.Team, "changed": !created})
}

func (h *HackHandler) getDirectory(w http.ResponseWriter, r *http.Request) {
	events, err := db.ListDirectoryEvents(r.Context(), h.database)
	if err != nil {
		writeInternal(w)
		return
	}
	now := time.Now()
	groups := map[string][]map[string]any{"now": {}, "upcoming": {}, "past": {}}
	for _, ev := range events {
		group := "now"
		if ev.Stage == "archived" || (ev.EndsAt.Valid && !now.Before(ev.EndsAt.Time)) {
			group = "past"
		} else if ev.StartsAt.Valid && now.Before(ev.StartsAt.Time) {
			group = "upcoming"
		}
		groups[group] = append(groups[group], map[string]any{
			"slug": ev.Slug, "title": ev.Title, "tagline": ev.Tagline, "stage": ev.Stage,
			"time_zone": ev.TimeZone, "starts_at": rfc3339UTC(ev.StartsAt), "ends_at": rfc3339UTC(ev.EndsAt),
			"url": h.EventURL(ev.Slug), "icon_url": h.iconURLSlug(ev.Slug),
		})
	}
	writeJSON(w, 200, groups)
}
