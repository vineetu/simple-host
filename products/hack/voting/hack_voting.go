package voting

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/entries"
	"github.com/vsriram/simple-host/products/hack/app/events"
	hackmodel "github.com/vsriram/simple-host/products/hack/model"
)

func votingSettingsJSON(s entries.VotingConfig) map[string]any {
	return map[string]any{
		"enabled": s.Enabled, "opens_at": core.RFC3339(s.OpensAt),
		"closes_at": core.RFC3339(s.ClosesAt), "eligibility": s.Eligibility,
		"directory_listed": s.DirectoryListed,
	}
}

func (h *handler) getVotingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	result, err := h.Entries.ReadVotingSettings(r.Context(), a.Event, time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	out := votingSettingsJSON(result.Settings)
	out["status"] = result.Status
	out["votes_to_remove"] = result.Counts
	httpx.WriteJSON(w, 200, out)
}

func (h *handler) putVotingSettings(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
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
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	v, err := votingInput(a.Event, req.Enabled, req.OpensAt, req.ClosesAt, req.Eligibility)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	ev, s, removed, err := h.Entries.ChangeVotingSettings(r.Context(), a.Event.ID, v, req.ConfirmVoteRemoval)
	var confirm *entries.VoteRemovalConfirmation
	if errors.As(err, &confirm) {
		httpx.WriteJSON(w, 409, map[string]any{"code": "vote_removal_confirmation", "error": "Confirm removal of earlier votes.", "votes_to_remove": confirm.Count})
		return
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	out := votingSettingsJSON(s)
	out["status"] = entries.VotingStatus(ev, s, time.Now())
	out["votes_removed"] = removed
	httpx.WriteJSON(w, 200, out)
}

// votingInput reads a voting settings body: the eligibility, then the
// window's times in the event's own zone (UTC when it has none that loads).
func votingInput(ev hackmodel.Event, enabled bool, opensAt, closesAt, eligibility string) (entries.VotingConfig, error) {
	v := entries.VotingSettingsValue(enabled, sql.NullTime{}, sql.NullTime{}, eligibility)
	if err := entries.ValidateVotingEligibility(eligibility); err != nil {
		return v, err
	}
	loc, err := time.LoadLocation(ev.TimeZone)
	if err != nil {
		loc = time.UTC
	}
	if strings.TrimSpace(opensAt) != "" {
		if v.OpensAt, err = events.ParseEventTime(opensAt, loc, "opens_at"); err != nil {
			return v, err
		}
	}
	if strings.TrimSpace(closesAt) != "" {
		v.ClosesAt, err = events.ParseEventTime(closesAt, loc, "closes_at")
	}
	return v, err
}

func (h *handler) patchDirectoryListed(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	if !entries.CanChangeDirectory(a.Admin, !a.Member.JoinedAt.IsZero()) {
		core.WriteEventNotFound(w)
		return
	}
	var req struct {
		Listed *bool `json:"listed"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if req.Listed == nil {
		httpx.WriteError(w, 400, "invalid_directory_listed", "listed must be true or false")
		return
	}
	if err := h.Entries.SetDirectoryListed(r.Context(), a.Event.ID, *req.Listed); err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"listed": *req.Listed})
}

func (h *handler) getVote(w http.ResponseWriter, r *http.Request) {
	ev, loaded, err := h.Entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		core.PaperEvent(r, ev)
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	result, err := h.Entries.ReadPublicVote(r.Context(), ev, time.Now)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	s, status := result.Settings, result.Status
	out := map[string]any{"event": ev.Slug, "title": ev.Title, "status": status,
		"opens_at": core.RFC3339(s.OpensAt), "closes_at": core.RFC3339(s.ClosesAt),
		// time_zone lets the public vote page show the window in the event's own zone instead of the visitor's browser zone.
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
				"title": title, "tagline": v.Tagline, "url": h.Links.TeamSiteURL(ev.Slug, v.Slug)})
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

func (h *handler) getMyVote(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	ev, loaded, err := h.Entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		core.PaperEvent(r, ev)
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	_, selected, eligible, err := h.Entries.ReadMyVote(r.Context(), ev, user.ID, user.Username)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"team": selected, "eligible": eligible})
}

func (h *handler) putVote(w http.ResponseWriter, r *http.Request) {
	user := core.NeedUser(w, r)
	if user == nil {
		return
	}
	if !entries.CanCastVote(user.Username) {
		httpx.WriteError(w, 403, "vote_ineligible", "sign in with a personal account to vote")
		return
	}
	var req struct {
		Team string `json:"team"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	if !entries.ValidVoteTeam(req.Team) {
		httpx.WriteError(w, 400, "invalid_team", "choose a team from the voting page")
		return
	}
	ev, loaded, err := h.Entries.VotingEvent(r.Context(), r.PathValue("slug"))
	if loaded {
		core.PaperEvent(r, ev)
	}
	if err != nil {
		core.WriteError(w, err)
		return
	}
	created, err := h.Entries.CastVote(r.Context(), ev.ID, ev.AccountID, user.ID, req.Team)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	httpx.WriteJSON(w, 200, map[string]any{"team": req.Team, "changed": !created})
}
