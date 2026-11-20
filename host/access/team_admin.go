package access

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
)

// Teams (owner decisions 2026-11-19): a team is an account that owns sites
// and that people join. There is one role: any member may do everything to
// the team's sites (with their own sign-in and owner=<team>), invite people
// and remove members, and delete the team once it owns no live sites. Nobody
// joins without accepting an invitation (owner decision A, 2026-11-19). A
// team always keeps at least one member. Who may act for a team is decided
// by ActsAsOwner alone.

const (
	// MaxTeamsPerPerson is how many teams one person may belong to.
	MaxTeamsPerPerson = 10
	// MaxPendingInvitations is how many live invitations one team may have.
	MaxPendingInvitations = 20
	// InvitationTTL is how long an invitation waits for an answer.
	InvitationTTL = 14 * 24 * time.Hour
)

// TeamError is a refused team change, with the HTTP status it answers.
type TeamError struct {
	Status        int
	Code, Message string
}

func (e *TeamError) Error() string { return e.Message }

func teamFail(status int, code, message string) error {
	return &TeamError{Status: status, Code: code, Message: message}
}

// CreateTeam makes a team named name with personID as its first member. The
// name becomes the team's handle, so its sites are at
// <site>.<name>.<domain>: the account address rules apply, and dots are
// refused.
func CreateTeam(ctx context.Context, db *sql.DB, personID, name string) (model.Team, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(name, ".") {
		return model.Team{}, teamFail(http.StatusBadRequest, "invalid_name", "a team name cannot contain dots: use lowercase letters, numbers and hyphens")
	}
	if !rules.VisitorHandle.MatchString(name) || !rules.HandleIsLabel(name) {
		return model.Team{}, teamFail(http.StatusBadRequest, "invalid_name", "a team name is 1 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen")
	}
	if rules.LabelReservedForNew(name) {
		return model.Team{}, teamFail(http.StatusConflict, "name_conflict", "that name is reserved for an address of this service; pick another")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Team{}, err
	}
	defer tx.Rollback()
	if err := repo.LockPerson(ctx, tx, personID); err != nil {
		return model.Team{}, err
	}
	if n, err := repo.CountTeamsOf(ctx, tx, personID); err != nil {
		return model.Team{}, err
	} else if n >= MaxTeamsPerPerson {
		return model.Team{}, teamFail(http.StatusConflict, "team_limit", fmt.Sprintf("you belong to %d teams, the most one person may; leave one first", MaxTeamsPerPerson))
	}
	var inUse bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE handle = $1) OR EXISTS (SELECT 1 FROM handle_aliases WHERE handle = $1)`, name).Scan(&inUse); err != nil {
		return model.Team{}, err
	}
	if inUse {
		return model.Team{}, teamFail(http.StatusConflict, "name_taken", "that name is already in use by a person or a team; pick another")
	}
	id, err := repo.InsertTeamAccount(ctx, tx)
	if err != nil {
		return model.Team{}, err
	}
	claimed, err := repo.ClaimHandle(ctx, tx, id, name)
	if err != nil {
		return model.Team{}, err
	}
	if !claimed {
		return model.Team{}, teamFail(http.StatusConflict, "name_conflict", "that name would collide with an existing web address; pick another")
	}
	if _, err := repo.AddTeamMember(ctx, tx, id, personID, personID); err != nil {
		return model.Team{}, err
	}
	if err := repo.InsertTeamAudit(ctx, tx, id, name, personID, "create", personID, ""); err != nil {
		return model.Team{}, err
	}
	var team model.Team
	if err := tx.QueryRowContext(ctx, `SELECT id, handle, created_at FROM users WHERE id = $1`, id).Scan(&team.ID, &team.Handle, &team.CreatedAt); err != nil {
		return model.Team{}, err
	}
	team.Members = 1
	return team, tx.Commit()
}

// ListTeams lists the teams personID belongs to.
func ListTeams(ctx context.Context, db *sql.DB, personID string) ([]model.Team, error) {
	return repo.ListTeamsOf(ctx, db, personID)
}

// memberTeam loads the team named handle for one of its members. Everyone
// else, like a name that is no team, gets team_not_found.
func memberTeam(ctx context.Context, q repo.Querier, personID, handle string) (model.User, error) {
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
	return teamFail(http.StatusNotFound, "team_not_found", "no team named "+strings.ToLower(strings.TrimSpace(handle))+" that you belong to; GET /v1/teams lists your teams")
}

// TeamMembers lists a team's members and its live invitations, for one of
// them.
func TeamMembers(ctx context.Context, db *sql.DB, personID, handle string) (model.Team, []model.TeamMember, []model.TeamInvitation, error) {
	team, err := memberTeam(ctx, db, personID, handle)
	if err != nil {
		return model.Team{}, nil, nil, err
	}
	members, err := repo.ListTeamMembers(ctx, db, team.ID)
	if err != nil {
		return model.Team{}, nil, nil, err
	}
	invitations, err := repo.ListTeamInvitations(ctx, db, team.ID)
	return model.Team{ID: team.ID, Handle: team.Handle.String, Members: len(members), CreatedAt: team.CreatedAt}, members, invitations, err
}

// InviteTeamMember records an invitation to join the team, addressed to an
// email (in its sign-in form, normalised by the caller), for a member. It
// makes nobody a member and never looks the address up: the answer is the
// same whether or not any account has it. The person signed in with that
// verified email sees it and accepts or declines it (AnswerInvitation).
func InviteTeamMember(ctx context.Context, db *sql.DB, personID, handle, email string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	team, err := memberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	if n, err := repo.CountLiveInvitations(ctx, tx, team.ID); err != nil {
		return err
	} else if n >= MaxPendingInvitations {
		return teamFail(http.StatusConflict, "invitation_limit", fmt.Sprintf("the team has %d invitations waiting for an answer, the most it may; cancel some first", MaxPendingInvitations))
	}
	id, err := repo.PutInvitation(ctx, tx, team.ID, personID, email, InvitationTTL)
	if err != nil {
		return err
	}
	if err := repo.InsertTeamAudit(ctx, tx, team.ID, team.Handle.String, personID, "invite", "", id); err != nil {
		return err
	}
	return tx.Commit()
}

// CancelInvitation cancels one of the team's pending invitations, for a member.
func CancelInvitation(ctx context.Context, db *sql.DB, personID, handle, id string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	team, err := memberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	if ok, err := repo.SetInvitationState(ctx, tx, team.ID, id, "cancelled"); err != nil {
		return err
	} else if !ok {
		return teamFail(http.StatusNotFound, "invitation_not_found", "this team has no pending invitation with that id")
	}
	if err := repo.InsertTeamAudit(ctx, tx, team.ID, team.Handle.String, personID, "cancel", "", id); err != nil {
		return err
	}
	return tx.Commit()
}

// InvitationsFor lists the live invitations to the signed-in person's
// verified email ("" when they have none: then none).
func InvitationsFor(ctx context.Context, db *sql.DB, verifiedEmail string) ([]model.TeamInvitation, error) {
	return repo.InvitationsFor(ctx, db, verifiedEmail)
}

// AnswerInvitation accepts or declines an invitation addressed to the
// signed-in person's verified email. Accepting makes them a member, under
// the same guards as ever, checked now: the team still exists, the
// invitation is still pending, and the person is in fewer than 10 teams. It
// returns the team's handle.
func AnswerInvitation(ctx context.Context, db *sql.DB, personID, verifiedEmail, id string, accept bool) (string, error) {
	notFound := teamFail(http.StatusNotFound, "invitation_not_found", "no invitation with that id is waiting for you: it was answered or cancelled, it expired, or the member who sent it has left the team")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	// The person's row is held for the 10-team count.
	if err := repo.LockPerson(ctx, tx, personID); err != nil {
		return "", err
	}
	teamID, _, err := repo.InvitationFor(ctx, tx, id, verifiedEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound
	} else if err != nil {
		return "", err
	}
	if err := repo.LockTeam(ctx, tx, teamID); errors.Is(err, sql.ErrNoRows) {
		return "", notFound
	} else if err != nil {
		return "", err
	}
	// Read again under the team's lock: it may have just been cancelled.
	teamID, invitedBy, err := repo.InvitationFor(ctx, tx, id, verifiedEmail)
	if errors.Is(err, sql.ErrNoRows) {
		return "", notFound
	} else if err != nil {
		return "", err
	}
	var teamHandle string
	if err := tx.QueryRowContext(ctx, `SELECT handle FROM users WHERE id = $1`, teamID).Scan(&teamHandle); err != nil {
		return "", err
	}
	state, action := "declined", "decline"
	if accept {
		state, action = "accepted", "accept"
		if n, err := repo.CountTeamsOf(ctx, tx, personID); err != nil {
			return "", err
		} else if n >= MaxTeamsPerPerson {
			return "", teamFail(http.StatusConflict, "team_limit", fmt.Sprintf("you belong to %d teams, the most one person may; leave one, then accept", MaxTeamsPerPerson))
		}
		if _, err := repo.AddTeamMember(ctx, tx, teamID, personID, invitedBy); err != nil {
			return "", err
		}
	}
	if _, err := repo.SetInvitationState(ctx, tx, teamID, id, state); err != nil {
		return "", err
	}
	if err := repo.InsertTeamAudit(ctx, tx, teamID, teamHandle, personID, action, personID, id); err != nil {
		return "", err
	}
	return teamHandle, tx.Commit()
}

// RemoveTeamMember removes the member named who (handle or account id) from
// the team, for a member; a member may remove themselves. The last member
// cannot leave: they delete the team instead.
func RemoveTeamMember(ctx context.Context, db *sql.DB, personID, handle, who string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	team, err := memberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	who = strings.ToLower(strings.TrimSpace(who))
	var memberID string
	err = tx.QueryRowContext(ctx, `
		SELECT u.id FROM team_members m JOIN users u ON u.id = m.user_id
		WHERE m.team_id = $1 AND (u.handle = $2 OR u.id::text = $2)`, team.ID, who).Scan(&memberID)
	if errors.Is(err, sql.ErrNoRows) {
		return teamFail(http.StatusNotFound, "member_not_found", "nobody of that address or id is a member of this team")
	}
	if err != nil {
		return err
	}
	n, err := repo.CountTeamMembers(ctx, tx, team.ID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return teamFail(http.StatusConflict, "last_member", "this is the team's last member, and a team always keeps one; to end the team, delete its sites, then delete the team (DELETE /v1/teams/<team>)")
	}
	if _, err := repo.RemoveTeamMember(ctx, tx, team.ID, memberID); err != nil {
		return err
	}
	if err := repo.InsertTeamAudit(ctx, tx, team.ID, team.Handle.String, personID, "remove_member", memberID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// TeamID is the id of the team handle names, for one of its members.
func TeamID(ctx context.Context, db *sql.DB, personID, handle string) (string, error) {
	team, err := memberTeam(ctx, db, personID, handle)
	return team.ID, err
}

// DeleteTeam deletes a team that owns no live sites, for a member, unless
// the operator suspended it or a site is taken down (EraseTeam).
// Its sites in Recently deleted go for good with it; the caller removes the
// returned account's files after the commit.
func DeleteTeam(ctx context.Context, db *sql.DB, personID, handle string) (model.ErasedAccount, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	defer tx.Rollback()
	team, err := memberTeam(ctx, tx, personID, handle)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return model.ErasedAccount{}, err
	}
	n, err := repo.CountLiveSites(ctx, tx, team.ID)
	if err != nil {
		return model.ErasedAccount{}, err
	}
	if n > 0 {
		return model.ErasedAccount{}, teamFail(http.StatusConflict, "team_has_sites", fmt.Sprintf("the team still owns %d site(s); delete them first (sites in Recently deleted go with the team)", n))
	}
	var hold *LifecycleError
	erased, err := EraseTeam(ctx, tx, team.ID, personID, false)
	if errors.As(err, &hold) {
		return model.ErasedAccount{}, teamFail(hold.Status, hold.Code, hold.Message)
	} else if err != nil {
		return model.ErasedAccount{}, err
	}
	return erased, tx.Commit()
}
