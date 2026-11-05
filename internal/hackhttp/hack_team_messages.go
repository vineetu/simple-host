package hackhttp

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vsriram/simple-host/internal/app/hack/events"
	"github.com/vsriram/simple-host/internal/httpx"
	"github.com/vsriram/simple-host/internal/model"
	"github.com/vsriram/simple-host/internal/rules/sites"
)

// Organiser team messages (0003-hack-notices-judges-messages.sql): a notice
// kind like any other, with a durable history row for the two read routes.

func (h *HackHandler) registerTeamMessages(mux *http.ServeMux, wrap func(http.HandlerFunc) http.Handler) {
	mux.Handle("POST /v1/hack/events/{slug}/team-messages", wrap(h.postTeamMessages))
	mux.Handle("GET /v1/hack/events/{slug}/teams/{team}/messages", wrap(h.getOrganiserTeamMessages))
	mux.Handle("GET /v1/hack/events/{slug}/team/messages", wrap(h.getMyTeamMessages))
}

func teamMessageJSON(m model.TeamMessage) map[string]any {
	return map[string]any{
		"id":          m.ID,
		"text":        m.Body,
		"sender_name": m.SenderName,
		"created_at":  rfc3339Time(m.CreatedAt),
	}
}

func postedTeamMessageJSON(m events.TeamMessageSent) map[string]any {
	out := teamMessageJSON(m.TeamMessage)
	out["team_id"] = m.TeamID
	out["team_name"] = m.TeamName
	out["recipients"] = m.Recipients
	return out
}

// checkTeamIDs validates body.team_ids: 1..25 distinct ids. Which ids
// actually belong to this event is checked by the service (invalid_team).
func checkTeamIDs(w http.ResponseWriter, raw []string) ([]string, bool) {
	if len(raw) < 1 || len(raw) > 25 {
		httpx.WriteError(w, http.StatusBadRequest, "invalid_team", "send 1 to 25 team ids")
		return nil, false
	}
	seen := map[string]bool{}
	for _, id := range raw {
		if !sites.UUIDShape.MatchString(id) || seen[id] {
			httpx.WriteError(w, http.StatusBadRequest, "invalid_team", "team_ids must be distinct ids of this event's teams")
			return nil, false
		}
		seen[id] = true
	}
	return raw, true
}

func (h *HackHandler) postTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	if a.event.Stage == "archived" {
		httpx.WriteError(w, http.StatusConflict, "event_closed", "an ended event cannot be changed")
		return
	}
	var req struct {
		TeamIDs []string `json:"team_ids"`
		Text    string   `json:"text"`
	}
	if !decodeHackJSON(w, r, &req) {
		return
	}
	teamIDs, ok := checkTeamIDs(w, req.TeamIDs)
	if !ok {
		return
	}
	text, ok := checkHackText(w, req.Text, "text", 1, 1000)
	if !ok {
		return
	}
	sent, err := h.eventsSvc().SendTeamMessage(r.Context(), a.event, teamIDs, text, a.user.ID, a.member.DisplayName)
	if err != nil {
		var rl *events.RateLimitError
		if errors.As(err, &rl) {
			w.Header().Set("Retry-After", strconv.Itoa(rl.RetryAfter))
			httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many team messages for this event; try again later")
			return
		}
		writeEventsError(w, err)
		return
	}
	messages := make([]map[string]any, 0, len(sent))
	for _, m := range sent {
		messages = append(messages, postedTeamMessageJSON(m))
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"messages": messages})
}

// getOrganiserTeamMessages is GET .../teams/{team}/messages: the organiser's
// read of one team's message history.
func (h *HackHandler) getOrganiserTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	team, err := h.teamsSvc().TeamBySlugLookup(r.Context(), a.event.ID, strings.ToLower(strings.TrimSpace(r.PathValue("team"))))
	if errors.Is(err, sql.ErrNoRows) {
		httpx.WriteError(w, http.StatusNotFound, "team_not_found", "team not found")
		return
	}
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	h.writeTeamMessages(w, r, team.ID)
}

// getMyTeamMessages is GET .../team/messages: a participant's own team's
// message history, empty when they are on no team.
func (h *HackHandler) getMyTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := h.loadMember(w, r, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if !a.member.TeamID.Valid {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": []map[string]any{}})
		return
	}
	h.writeTeamMessages(w, r, a.member.TeamID.String)
}

func (h *HackHandler) writeTeamMessages(w http.ResponseWriter, r *http.Request, teamID string) {
	list, err := h.eventsSvc().ListTeamMessages(r.Context(), teamID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	messages := make([]map[string]any, 0, len(list))
	for _, m := range list {
		messages = append(messages, teamMessageJSON(m))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": messages})
}
