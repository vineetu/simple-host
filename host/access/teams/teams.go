package teams

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/vsriram/simple-host/host/access"
	"github.com/vsriram/simple-host/host/access/repo"
	"github.com/vsriram/simple-host/host/model"
	rules "github.com/vsriram/simple-host/host/rules/sites"
)

const (
	// MaxTeamsPerPerson is how many teams one person may belong to.
	MaxTeamsPerPerson = 10
	// MaxPendingInvitations is how many live invitations one team may have.
	MaxPendingInvitations = 20
	// InvitationTTL is how long an invitation waits for an answer.
	InvitationTTL = 14 * 24 * time.Hour
)

// CreateTeam makes a team named name with personID as its first member. The
// name becomes the team's handle, so its sites are at
// <site>.<name>.<domain>: the account address rules apply, and dots are
// refused.
func CreateTeam(ctx context.Context, db *sql.DB, personID, name string) (model.Team, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(name, ".") {
		return model.Team{}, access.TeamFail(http.StatusBadRequest, "invalid_name", "a team name cannot contain dots: use lowercase letters, numbers and hyphens")
	}
	if !rules.VisitorHandle.MatchString(name) || !rules.HandleIsLabel(name) {
		return model.Team{}, access.TeamFail(http.StatusBadRequest, "invalid_name", "a team name is 1 to 39 lowercase letters, numbers or hyphens, not starting or ending with a hyphen")
	}
	if rules.LabelReservedForNew(name) {
		return model.Team{}, access.TeamFail(http.StatusConflict, "name_conflict", "that name is reserved for an address of this service; pick another")
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
		return model.Team{}, access.TeamFail(http.StatusConflict, "team_limit", fmt.Sprintf("you belong to %d teams, the most one person may; leave one first", MaxTeamsPerPerson))
	}
	if inUse, err := repo.HandleTaken(ctx, tx, name, true); err != nil {
		return model.Team{}, err
	} else if inUse {
		return model.Team{}, access.TeamFail(http.StatusConflict, "name_taken", "that name is already in use by a person or a team; pick another")
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
		return model.Team{}, access.TeamFail(http.StatusConflict, "name_conflict", "that name would collide with an existing web address; pick another")
	}
	if _, err := repo.AddTeamMember(ctx, tx, id, personID, personID); err != nil {
		return model.Team{}, err
	}
	if err := repo.InsertTeamAudit(ctx, tx, id, name, personID, "create", personID, ""); err != nil {
		return model.Team{}, err
	}
	u, err := repo.GetUserByID(ctx, tx, id)
	if err != nil {
		return model.Team{}, err
	}
	team := model.Team{ID: u.ID, Handle: u.Handle.String, CreatedAt: u.CreatedAt, Members: 1}
	return team, tx.Commit()
}

// TeamMembers lists a team's members and its live invitations, for one of
// them.
func TeamMembers(ctx context.Context, db *sql.DB, personID, handle string) (model.Team, []model.TeamMember, []model.TeamInvitation, error) {
	team, err := access.MemberTeam(ctx, db, personID, handle)
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
	team, err := access.MemberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	if n, err := repo.CountLiveInvitations(ctx, tx, team.ID); err != nil {
		return err
	} else if n >= MaxPendingInvitations {
		return access.TeamFail(http.StatusConflict, "invitation_limit", fmt.Sprintf("the team has %d invitations waiting for an answer, the most it may; cancel some first", MaxPendingInvitations))
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
	team, err := access.MemberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	if ok, err := repo.SetInvitationState(ctx, tx, team.ID, id, "cancelled"); err != nil {
		return err
	} else if !ok {
		return access.TeamFail(http.StatusNotFound, "invitation_not_found", "this team has no pending invitation with that id")
	}
	if err := repo.InsertTeamAudit(ctx, tx, team.ID, team.Handle.String, personID, "cancel", "", id); err != nil {
		return err
	}
	return tx.Commit()
}

// AnswerInvitation accepts or declines an invitation addressed to the
// signed-in person's verified email. Accepting makes them a member, under
// the same guards as ever, checked now: the team still exists, the
// invitation is still pending, and the person is in fewer than 10 teams. It
// returns the team's handle.
func AnswerInvitation(ctx context.Context, db *sql.DB, personID, verifiedEmail, id string, accept bool) (string, error) {
	notFound := access.TeamFail(http.StatusNotFound, "invitation_not_found", "no invitation with that id is waiting for you: it was answered or cancelled, it expired, or the member who sent it has left the team")
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
	team, err := repo.GetUserByID(ctx, tx, teamID)
	if err != nil {
		return "", err
	}
	teamHandle := team.Handle.String
	state, action := "declined", "decline"
	if accept {
		state, action = "accepted", "accept"
		if n, err := repo.CountTeamsOf(ctx, tx, personID); err != nil {
			return "", err
		} else if n >= MaxTeamsPerPerson {
			return "", access.TeamFail(http.StatusConflict, "team_limit", fmt.Sprintf("you belong to %d teams, the most one person may; leave one, then accept", MaxTeamsPerPerson))
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
	team, err := access.MemberTeam(ctx, tx, personID, handle)
	if err != nil {
		return err
	}
	if err := repo.LockTeam(ctx, tx, team.ID); err != nil {
		return err
	}
	who = strings.ToLower(strings.TrimSpace(who))
	memberID, err := repo.TeamMemberID(ctx, tx, team.ID, who)
	if errors.Is(err, sql.ErrNoRows) {
		return access.TeamFail(http.StatusNotFound, "member_not_found", "nobody of that address or id is a member of this team")
	}
	if err != nil {
		return err
	}
	n, err := repo.CountTeamMembers(ctx, tx, team.ID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return access.TeamFail(http.StatusConflict, "last_member", "this is the team's last member, and a team always keeps one; to end the team, delete its sites, then delete the team (DELETE /v1/teams/<team>)")
	}
	if _, err := repo.RemoveTeamMember(ctx, tx, team.ID, memberID); err != nil {
		return err
	}
	if err := repo.InsertTeamAudit(ctx, tx, team.ID, team.Handle.String, personID, "remove_member", memberID, ""); err != nil {
		return err
	}
	return tx.Commit()
}

// AnswerMyInvitation accepts or declines an invitation addressed to the
// signed-in person (AnswerInvitation); it returns the team's handle.
func AnswerMyInvitation(ctx context.Context, db *sql.DB, user *model.User, id string, accept bool) (string, error) {
	email, err := access.InviteeEmail(ctx, db, user)
	if err != nil {
		return "", err
	}
	return AnswerInvitation(ctx, db, user.ID, email, id, accept)
}
