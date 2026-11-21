package hackhttp

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Organiser team messages (0003-hack-notices-judges-messages.sql): a notice
// kind like any other, with a durable history row for the two read routes.

func teamMessageJSON(m model.TeamMessage) map[string]any {
	return map[string]any{
		"id":          m.ID,
		"text":        m.Body,
		"sender_name": m.SenderName,
		"from_team":   m.FromTeam,
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

func (h *participationHTTP) postTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		TeamIDs []string `json:"team_ids"`
		Text    string   `json:"text"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	text, err := teams.OrganiserMessage(a.event.Stage, req.TeamIDs, req.Text)
	if err != nil {
		writeHackError(w, err)
		return
	}
	sent, err := h.events.SendTeamMessage(r.Context(), a.event, req.TeamIDs, text, a.user.ID, a.member.DisplayName)
	if err != nil {
		writeMessageRefusal(w, err, "too many team messages for this event; try again later")
		return
	}
	messages := make([]map[string]any, 0, len(sent))
	for _, m := range sent {
		messages = append(messages, postedTeamMessageJSON(m))
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"messages": messages})
}

// writeMessageRefusal answers a refused message: a rate limit is 429 with
// Retry-After and the route's wording.
func writeMessageRefusal(w http.ResponseWriter, err error, limited string) {
	var rl *events.RateLimitError
	if !errors.As(err, &rl) {
		writeHackError(w, err)
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(rl.RetryAfter))
	httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", limited)
}

// getOrganiserTeamMessages is GET .../teams/{team}/messages: the organiser's
// read of one team's message history.
func (h *participationHTTP) getOrganiserTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	team, err := h.teams.FindTeam(r.Context(), a.event.ID, r.PathValue("team"))
	if err != nil {
		writeHackError(w, err)
		return
	}
	list, err := h.events.ListTeamMessages(r.Context(), team.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Reading the thread clears this organiser's Home line for the team.
	if a.member.Role == "organiser" && !a.admin {
		if err := h.events.OrganiserOpenedThread(r.Context(), a.event.ID, a.user.ID, team.ID, list); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	writeTeamMessageList(w, list)
}

// postMyTeamMessage is POST .../team/messages: a member of the team writes
// to the event's organisers. Only the caller's own current team's thread is
// ever written; there is no team id in the request.
func (h *participationHTTP) postMyTeamMessage(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !httpx.DecodeJSON(w, r, hackMaxBody, &req) {
		return
	}
	text, err := teams.TeamMessage(a.event.Stage, req.Text)
	if err != nil {
		writeHackError(w, err)
		return
	}
	sent, err := h.events.SendToOrganisers(r.Context(), a.event, a.user.ID, a.member.DisplayName, text)
	if err != nil {
		writeMessageRefusal(w, err, "your team sent too many messages to the organisers; try again later")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"message": postedTeamMessageJSON(sent)})
}

// getMyTeamMessages is GET .../team/messages: a participant's own team's
// message history, empty when they are on no team.
func (h *participationHTTP) getMyTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := loadMember(w, r, h.members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if !a.member.TeamID.Valid {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": []map[string]any{}})
		return
	}
	h.writeTeamMessages(w, r, a.member.TeamID.String)
}

func (h *participationHTTP) writeTeamMessages(w http.ResponseWriter, r *http.Request, teamID string) {
	list, err := h.events.ListTeamMessages(r.Context(), teamID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	writeTeamMessageList(w, list)
}

func writeTeamMessageList(w http.ResponseWriter, list []model.TeamMessage) {
	messages := make([]map[string]any, 0, len(list))
	for _, m := range list {
		messages = append(messages, teamMessageJSON(m))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": messages})
}
