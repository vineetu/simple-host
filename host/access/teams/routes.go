package teams

import (
	"database/sql"
	"net/http"

	"github.com/vsriram/simple-host/host/ops"

	accounts "github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/httpx"

	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"
)

// Teams: an account that owns sites and that
// people join, offered where accounts own their own sites (the Simple Host
// program; Simple Hack keeps its own teams). A member acts on the team's
// sites with their own sign-in by naming owner=<team> on the /v1/sites
// routes (auth.ScopeGate); these routes manage the team itself.
func (h *Handler) Register(mux *http.ServeMux, authMiddleware, noticeMiddleware func(http.Handler) http.Handler) {
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

// Handler serves the team routes. Cutoff deletes a team with its sites'
// locks and files (the account's site services, set by the composition).
type Handler struct {
	DB     *sql.DB
	Cutoff ops.Cutoff
}

// invitedAnswer is the one answer to an invitation: the same whether or not
// any account has that email, so it tells nobody who has one.
var invitedAnswer = map[string]any{
	"invited":         true,
	"expires_in_days": int(InvitationTTL.Hours() / 24),
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

func (h *Handler) createTeam(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !accounts.DecodeTeamBody(w, r, &body) {
		return
	}
	team, err := CreateTeam(r.Context(), h.DB, auth.GetUser(r.Context()).ID, body.Name)
	if err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, team)
}

func (h *Handler) listTeams(w http.ResponseWriter, r *http.Request) {
	teams, invitations, err := ops.MyTeams(r.Context(), h.DB, auth.GetUser(r.Context()))
	if err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"teams": teams, "invitations": invitations})
}

func (h *Handler) listTeamMembers(w http.ResponseWriter, r *http.Request) {
	team, members, invitations, err := TeamMembers(r.Context(), h.DB, auth.GetUser(r.Context()).ID, r.PathValue("team"))
	if err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"team": team, "members": members, "invitations": invitations})
}

// inviteTeamMember records an invitation to an email address
// (InviteTeamMember) and gives the one answer, invitedAnswer, whoever
// the address belongs to.
func (h *Handler) inviteTeamMember(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
	}
	if !accounts.DecodeTeamBody(w, r, &body) {
		return
	}
	email, ok := authrules.NormalEmail(body.Email)
	if !ok {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "send the person's whole email address", Code: "invalid_email"})
		return
	}
	if err := InviteTeamMember(r.Context(), h.DB, auth.GetUser(r.Context()).ID, r.PathValue("team"), email); err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusAccepted, invitedAnswer)
}

func (h *Handler) cancelTeamInvitation(w http.ResponseWriter, r *http.Request) {
	if err := CancelInvitation(r.Context(), h.DB, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("invitation")); err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"cancelled": r.PathValue("invitation")})
}

// answerTeamInvitation accepts or declines an invitation addressed to the
// signed-in person: {"answer": "accept"} or {"answer": "decline"}.
func (h *Handler) answerTeamInvitation(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Answer string `json:"answer"`
	}
	if !accounts.DecodeTeamBody(w, r, &body) {
		return
	}
	if body.Answer != "accept" && body.Answer != "decline" {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: `send {"answer": "accept"} or {"answer": "decline"}`, Code: "invalid_request"})
		return
	}
	team, err := AnswerMyInvitation(r.Context(), h.DB, auth.GetUser(r.Context()), r.PathValue("invitation"), body.Answer == "accept")
	if err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"team": team, "answer": body.Answer})
}

func (h *Handler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	if err := RemoveTeamMember(r.Context(), h.DB, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("member")); err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"removed": r.PathValue("member")})
}

// deleteTeam takes the team's site locks first, as an account deletion does,
// since its sites in Recently deleted go for good with it.
func (h *Handler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	if err := h.Cutoff.DeleteTeam(r.Context(), auth.GetUser(r.Context()).ID, r.PathValue("team")); err != nil {
		accounts.WriteTeamError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("team")})
}
