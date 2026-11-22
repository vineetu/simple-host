package accesshttp

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/vsriram/simple-host/host/ops"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
)

// Teams: an account that owns sites and that
// people join, offered where accounts own their own sites (the Simple Host
// program; Simple Hack keeps its own teams). A member acts on the team's
// sites with their own sign-in by naming owner=<team> on the /v1/sites
// routes (auth.ScopeGate); these routes manage the team itself.
func (h *UserHandler) registerTeams(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
	if !h.ed.Identity().PersonalSites {
		return
	}
	wrap := func(f http.HandlerFunc) http.Handler { return noticeMiddleware(authMiddleware(teamPerson(f))) }
	mux.Handle("POST /v1/teams", wrap(h.createTeam))
	mux.Handle("GET /v1/teams", wrap(h.listTeams))
	mux.Handle("GET /v1/teams/{team}/members", wrap(h.listTeamMembers))
	mux.Handle("POST /v1/teams/{team}/members", wrap(h.inviteTeamMember))
	mux.Handle("DELETE /v1/teams/{team}/invitations/{invitation}", wrap(h.cancelTeamInvitation))
	mux.Handle("POST /v1/me/team-invitations/{invitation}", wrap(h.answerTeamInvitation))
	mux.Handle("DELETE /v1/teams/{team}/members/{member}", wrap(h.removeTeamMember))
	mux.Handle("DELETE /v1/teams/{team}", wrap(h.deleteTeam))
}

// invitedAnswer is the one answer to an invitation: the same whether or not
// any account has that email, so it tells nobody who has one.
var invitedAnswer = map[string]any{
	"invited":         true,
	"expires_in_days": int(accounts.InvitationTTL.Hours() / 24),
	"note":            "Nothing is sent. Whoever signs in with that email sees the invitation when signed in and can accept or decline it.",
}

// teamPerson lets a person's own credential through, an admin's included:
// never a credential acting for one site, a team, or a member acting for one.
func teamPerson(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.GetUser(r.Context())
		if user == nil {
			httpx.WriteJSON(w, http.StatusUnauthorized, httpx.ErrorResponse{Error: "unauthorized"})
			return
		}
		if user.Member != nil {
			httpx.WriteJSON(w, http.StatusForbidden, httpx.ErrorResponse{Error: "teams are managed with a person's own sign-in or key", Code: "forbidden"})
			return
		}
		next(w, r)
	}
}

func writeTeamError(w http.ResponseWriter, err error) {
	var te *accounts.TeamError
	if errors.As(err, &te) {
		httpx.WriteJSON(w, te.Status, httpx.ErrorResponse{Error: te.Message, Code: te.Code})
		return
	}
	log.Printf("teams: %v", err)
	httpx.WriteInternal(w)
}

func (h *UserHandler) createTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeTeamBody(w, r, &body) {
		return
	}
	team, err := accounts.CreateTeam(r.Context(), h.database, auth.GetUser(r.Context()).ID, body.Name)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, team)
}

func (h *UserHandler) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, invitations, err := ops.MyTeams(r.Context(), h.database, auth.GetUser(r.Context()))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": teams, "invitations": invitations})
}

func (h *UserHandler) listTeamMembers(w http.ResponseWriter, r *http.Request) {
	team, members, invitations, err := accounts.TeamMembers(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"team": team, "members": members, "invitations": invitations})
}

// inviteTeamMember records an invitation to an email address
// (accounts.InviteTeamMember) and gives the one answer, invitedAnswer, whoever
// the address belongs to.
func (h *UserHandler) inviteTeamMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !decodeTeamBody(w, r, &body) {
		return
	}
	email, ok := authrules.NormalEmail(body.Email)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "send the person's whole email address", Code: "invalid_email"})
		return
	}
	if err := accounts.InviteTeamMember(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), email); err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, invitedAnswer)
}

func (h *UserHandler) cancelTeamInvitation(w http.ResponseWriter, r *http.Request) {
	if err := accounts.CancelInvitation(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("invitation")); err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cancelled": r.PathValue("invitation")})
}

// answerTeamInvitation accepts or declines an invitation addressed to the
// signed-in person: {"answer": "accept"} or {"answer": "decline"}.
func (h *UserHandler) answerTeamInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	if !decodeTeamBody(w, r, &body) {
		return
	}
	if body.Answer != "accept" && body.Answer != "decline" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `send {"answer": "accept"} or {"answer": "decline"}`, Code: "invalid_request"})
		return
	}
	team, err := accounts.AnswerMyInvitation(r.Context(), h.database, auth.GetUser(r.Context()), r.PathValue("invitation"), body.Answer == "accept")
	if err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"team": team, "answer": body.Answer})
}

func (h *UserHandler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	if err := accounts.RemoveTeamMember(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("member")); err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": r.PathValue("member")})
}

// deleteTeam takes the team's site locks first, as an account deletion does,
// since its sites in Recently deleted go for good with it.
func (h *UserHandler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	c := ops.Cutoff{DB: h.database}
	if h.siteFiles != nil {
		c = h.siteFiles.cutoff()
	}
	if err := c.DeleteTeam(r.Context(), auth.GetUser(r.Context()).ID, r.PathValue("team")); err != nil {
		writeTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("team")})
}

func decodeTeamBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "send a JSON body: " + err.Error(), Code: "invalid_request"})
		return false
	}
	return true
}

// teamsForMe is the teams part of GET /v1/me: each team's id (stable: record
// it rather than the name) and handle.
func teamsForMe(teams []model.Team) []map[string]string {
	out := make([]map[string]string, 0, len(teams))
	for _, t := range teams {
		out = append(out, map[string]string{"id": t.ID, "handle": t.Handle})
	}
	return out
}
