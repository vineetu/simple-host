package messages

import (
	"errors"
	"net/http"
	"strconv"

	core "github.com/vsriram/simple-host/products/hack/core"

	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/products/hack/app/events"
	"github.com/vsriram/simple-host/products/hack/app/teams"
	"github.com/vsriram/simple-host/products/hack/model"
)

// Organiser team messages: a notice kind like any other, with a durable history row for the two read routes.

func teamMessageJSON(m model.TeamMessage) map[string]any {
	return map[string]any{
		"id":          m.ID,
		"text":        m.Body,
		"sender_name": m.SenderName,
		"from_team":   m.FromTeam,
		"created_at":  core.RFC3339Time(m.CreatedAt),
	}
}

func postedTeamMessageJSON(m TeamMessageSent) map[string]any {
	out := teamMessageJSON(m.TeamMessage)
	out["team_id"] = m.TeamID
	out["team_name"] = m.TeamName
	out["recipients"] = m.Recipients
	return out
}

func (h *handler) postTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "organiser")
	if !ok {
		return
	}
	var req struct {
		TeamIDs []string `json:"team_ids"`
		Text    string   `json:"text"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	text, err := teams.OrganiserMessage(a.Event.Stage, req.TeamIDs, req.Text)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	sent, err := h.sendTeamMessage(r.Context(), a.Event, req.TeamIDs, text, a.User.ID)
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
		core.WriteError(w, err)
		return
	}
	w.Header().Set("Retry-After", strconv.Itoa(rl.RetryAfter))
	httpx.WriteError(w, http.StatusTooManyRequests, "rate_limited", limited)
}

func (h *handler) getOrganiserTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "organiser")
	if !ok {
		return
	}
	team, err := h.Teams.FindTeam(r.Context(), a.Event.ID, r.PathValue("team"))
	if err != nil {
		core.WriteError(w, err)
		return
	}
	list, err := h.listTeamMessages(r.Context(), team.ID)
	if err != nil {
		httpx.WriteInternal(w)
		return
	}
	// Reading the thread clears this organiser's Home line for the team.
	if a.Member.Role == "organiser" && !a.Admin {
		if err := h.organiserOpenedThread(r.Context(), a.Event.ID, a.User.ID, team.ID, list); err != nil {
			httpx.WriteInternal(w)
			return
		}
	}
	writeTeamMessageList(w, list)
}

// postMyTeamMessage is POST .../team/messages: a member of the team writes
// to the event's organisers. Only the caller's own current team's thread is
// ever written; there is no team id in the request.
func (h *handler) postMyTeamMessage(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), true, "participant")
	if !ok {
		return
	}
	var req struct {
		Text string `json:"text"`
	}
	if !httpx.DecodeJSON(w, r, core.MaxBody, &req) {
		return
	}
	text, err := teams.TeamMessage(a.Event.Stage, req.Text)
	if err != nil {
		core.WriteError(w, err)
		return
	}
	sent, err := h.sendToOrganisers(r.Context(), a.Event, a.User.ID, text)
	if err != nil {
		writeMessageRefusal(w, err, "your team sent too many messages to the organisers; try again later")
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"message": postedTeamMessageJSON(sent)})
}

func (h *handler) getMyTeamMessages(w http.ResponseWriter, r *http.Request) {
	a, ok := core.LoadMember(w, r, h.Members, r.PathValue("slug"), false, "participant")
	if !ok {
		return
	}
	if !a.Member.TeamID.Valid {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"messages": []map[string]any{}})
		return
	}
	h.writeTeamMessages(w, r, a.Member.TeamID.String)
}

func (h *handler) writeTeamMessages(w http.ResponseWriter, r *http.Request, teamID string) {
	list, err := h.listTeamMessages(r.Context(), teamID)
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
