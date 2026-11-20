package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	accounts "github.com/vsriram/simple-host/host/access"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/auth"
	authrules "github.com/vsriram/simple-host/host/access/rules"
	"github.com/vsriram/simple-host/host/model"
)

// Teams (owner decisions 2026-11-19): an account that owns sites and that
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
			writeJSON(w, http.StatusUnauthorized, errorResponse{Error: "unauthorized"})
			return
		}
		if user.Member != nil {
			writeJSON(w, http.StatusForbidden, errorResponse{Error: "teams are managed with a person's own sign-in or key", Code: "forbidden"})
			return
		}
		next(w, r)
	}
}

func writeTeamError(w http.ResponseWriter, err error) {
	var te *accounts.TeamError
	if errors.As(err, &te) {
		writeJSON(w, te.Status, errorResponse{Error: te.Message, Code: te.Code})
		return
	}
	log.Printf("teams: %v", err)
	writeInternal(w)
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
	writeJSON(w, http.StatusCreated, team)
}

func (h *UserHandler) listTeams(w http.ResponseWriter, r *http.Request) {
	user := auth.GetUser(r.Context())
	teams, err := accounts.ListTeams(r.Context(), h.database, user.ID)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	invitations, err := h.invitationsFor(r.Context(), user)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"teams": teams, "invitations": invitations})
}

// inviteeEmail is the one address invitations reach the signed-in person
// at: their sign-in email, when the platform counts it as verified
// (visitorEmail), in its sign-in form (authrules.NormalEmail: lower case, no
// +tag folding). An account whose username is not an email, or whose email
// is unverified, gets "" and matches no invitation. Invitations follow the
// address, not the account: if a person's sign-in email changes, they see
// those sent to the new address and no longer those sent to the old one.
func inviteeEmail(ctx context.Context, database *sql.DB, user *model.User) (string, error) {
	email, err := visitorEmail(ctx, database, user.ID)
	if err != nil || !strings.EqualFold(email, user.Username) {
		return "", err
	}
	email, _ = authrules.NormalEmail(email)
	return email, nil
}

// invitationsFor lists the invitations waiting for the signed-in person.
func (h *UserHandler) invitationsFor(ctx context.Context, user *model.User) ([]model.TeamInvitation, error) {
	email, err := inviteeEmail(ctx, h.database, user)
	if err != nil {
		return nil, err
	}
	return accounts.InvitationsFor(ctx, h.database, email)
}

func (h *UserHandler) listTeamMembers(w http.ResponseWriter, r *http.Request) {
	team, members, invitations, err := accounts.TeamMembers(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team, "members": members, "invitations": invitations})
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
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send the person's whole email address", Code: "invalid_email"})
		return
	}
	if err := accounts.InviteTeamMember(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), email); err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, invitedAnswer)
}

func (h *UserHandler) cancelTeamInvitation(w http.ResponseWriter, r *http.Request) {
	if err := accounts.CancelInvitation(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("invitation")); err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": r.PathValue("invitation")})
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
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: `send {"answer": "accept"} or {"answer": "decline"}`, Code: "invalid_request"})
		return
	}
	user := auth.GetUser(r.Context())
	email, err := inviteeEmail(r.Context(), h.database, user)
	if err != nil {
		writeTeamError(w, err)
		return
	}
	team, err := accounts.AnswerInvitation(r.Context(), h.database, user.ID, email, r.PathValue("invitation"), body.Answer == "accept")
	if err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"team": team, "answer": body.Answer})
}

func (h *UserHandler) removeTeamMember(w http.ResponseWriter, r *http.Request) {
	if err := accounts.RemoveTeamMember(r.Context(), h.database, auth.GetUser(r.Context()).ID, r.PathValue("team"), r.PathValue("member")); err != nil {
		writeTeamError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": r.PathValue("member")})
}

// deleteTeam takes the team's site locks first, as an account deletion does,
// since its sites in Recently deleted go for good with it.
func (h *UserHandler) deleteTeam(w http.ResponseWriter, r *http.Request) {
	personID := auth.GetUser(r.Context()).ID
	if h.siteFiles != nil {
		id, err := accounts.TeamID(r.Context(), h.database, personID, r.PathValue("team"))
		if err != nil {
			writeTeamError(w, err)
			return
		}
		unlock, err := h.siteFiles.lockAccountSites(r.Context(), id)
		if err != nil {
			writeTeamError(w, err)
			return
		}
		defer unlock()
	}
	erased, err := accounts.DeleteTeam(r.Context(), h.database, personID, r.PathValue("team"))
	if err != nil {
		writeTeamError(w, err)
		return
	}
	if h.siteFiles != nil {
		if err := h.siteFiles.eraseAccountFiles(r.Context(), erased); err != nil {
			log.Printf("delete team %s: files: %v", erased.UserID, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": r.PathValue("team")})
}

func decodeTeamBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "send a JSON body: " + err.Error(), Code: "invalid_request"})
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
