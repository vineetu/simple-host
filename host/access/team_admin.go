package access

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/httpx"
	"github.com/vsriram/simple-host/host/model"
)

// Teams: a team is an account that owns sites
// and that people join. There is one role: any member may do everything to
// the team's sites (with their own sign-in and owner=<team>), invite people
// and remove members, and delete the team once it owns no live sites. Nobody
// joins without accepting an invitation. A
// team always keeps at least one member. Who may act for a team is decided
// by ActsAsOwner alone.

// TeamError is a refused team change, with the HTTP status it answers.
type TeamError struct {
	Status        int
	Code, Message string
}

func (e *TeamError) Error() string { return e.Message }

// TeamFail is a TeamError: teams and site editors refuse with it.
func TeamFail(status int, code, message string) error {
	return &TeamError{Status: status, Code: code, Message: message}
}

// WriteTeamError answers a TeamError with its status, anything else as an
// internal error.
func WriteTeamError(w http.ResponseWriter, err error) {
	var te *TeamError
	if errors.As(err, &te) {
		httpx.WriteJSON(w, te.Status, httpx.ErrorResponse{Error: te.Message, Code: te.Code})
		return
	}
	log.Printf("teams: %v", err)
	httpx.WriteInternal(w)
}

// DecodeTeamBody reads a team or editor route's small JSON body, refusing
// unknown fields.
func DecodeTeamBody(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		httpx.WriteJSON(w, http.StatusBadRequest, httpx.ErrorResponse{Error: "send a JSON body: " + err.Error(), Code: "invalid_request"})
		return false
	}
	return true
}

// ListTeams lists the teams personID belongs to.
func ListTeams(ctx context.Context, db *sql.DB, personID string) ([]model.Team, error) {
	return repo.ListTeamsOf(ctx, db, personID)
}

// MemberTeam loads the team named handle for one of its members. Everyone
// else, like a name that is no team, gets team_not_found.
func MemberTeam(ctx context.Context, q repo.Querier, personID, handle string) (model.User, error) {
	team, err := repo.GetTeamByHandle(ctx, q, handle)
	if errors.Is(err, sql.ErrNoRows) {
		return model.User{}, teamNotFound(handle)
	}
	if err != nil {
		return model.User{}, err
	}
	ok, err := ActsAsOwner(ctx, q, personID, team.ID, "")
	if err != nil {
		return model.User{}, err
	}
	if !ok {
		return model.User{}, teamNotFound(handle)
	}
	return team, nil
}

func teamNotFound(handle string) error {
	return TeamFail(http.StatusNotFound, "team_not_found", "no team named "+strings.ToLower(strings.TrimSpace(handle))+" that you belong to; GET /v1/teams lists your teams")
}

// InvitationsFor lists the live invitations to the signed-in person's
// verified email ("" when they have none: then none).
func InvitationsFor(ctx context.Context, db *sql.DB, verifiedEmail string) ([]model.TeamInvitation, error) {
	return repo.InvitationsFor(ctx, db, verifiedEmail)
}

// TeamID is the id of the team handle names, for one of its members.
func TeamID(ctx context.Context, db *sql.DB, personID, handle string) (string, error) {
	team, err := MemberTeam(ctx, db, personID, handle)
	return team.ID, err
}

// LockTeamToDelete is the team handle names, for a member, row-locked
// inside tx (host/ops DeleteTeam).
func LockTeamToDelete(ctx context.Context, tx *sql.Tx, personID, handle string) (model.User, error) {
	team, err := MemberTeam(ctx, tx, personID, handle)
	if err != nil {
		return model.User{}, err
	}
	return team, repo.LockTeam(ctx, tx, team.ID)
}

// TeamDeletionRefusal is why a member may not delete the team: it still
// owns live sites, or (hold, from TeamErasureRefusal) it is held.
func TeamDeletionRefusal(liveSites int, hold error) error {
	if liveSites > 0 {
		return TeamFail(http.StatusConflict, "team_has_sites", fmt.Sprintf("the team still owns %d site(s); delete them first (sites in Recently deleted go with the team)", liveSites))
	}
	var h *LifecycleError
	if errors.As(hold, &h) {
		return TeamFail(h.Status, h.Code, h.Message)
	}
	return hold
}
