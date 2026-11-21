package hackmembers

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/vsriram/simple-host/host/sqlkit"
	"github.com/vsriram/simple-host/products/hack/model"
	"github.com/vsriram/simple-host/products/hack/repository/hackevents"
)

type SignupAnswer = model.SignupAnswer

type ApplicantProfile = model.ApplicantProfile

// Join adds a person to an event with role, the code of conduct accepted now.
// A unique violation (already a member) is returned as is. Anyone but an
// organiser keeps the event's name taken for good (event_used_names).
func Join(ctx context.Context, q sqlkit.Querier, eventID, userID, role string) (EventMember, error) {
	m, err := scanMember(q.QueryRowContext(ctx, `INSERT INTO event_members (event_id, user_id, role, coc_accepted_at)
		VALUES ($1, $2, $3, now()) RETURNING `+memberColumns, eventID, userID, role))
	if err != nil {
		return EventMember{}, err
	}
	if !m.Organiser() {
		if err := hackevents.ReserveEventName(ctx, q, eventID); err != nil {
			return EventMember{}, err
		}
	}
	return WithDisplayName(ctx, q, m, nil)
}

// JoinParticipant is Join for a participant with their sign-up answers,
// pending when the event needs an organiser's approval.
func JoinParticipant(ctx context.Context, q sqlkit.Querier, eventID, userID string, answers []SignupAnswer, pending bool) (EventMember, error) {
	status := "approved"
	if pending {
		status = "pending"
	}
	return insertParticipant(ctx, q, eventID, userID, answers, status, "link", ApplicantProfile{})
}

// Apply is JoinParticipant for someone applying without a join link: always
// pending, with the profile they gave.
func Apply(ctx context.Context, q sqlkit.Querier, eventID, userID string, answers []SignupAnswer, p ApplicantProfile) (EventMember, error) {
	return insertParticipant(ctx, q, eventID, userID, answers, "pending", "application", p)
}

func insertParticipant(ctx context.Context, q sqlkit.Querier, eventID, userID string, answers []SignupAnswer, status, via string, p ApplicantProfile) (EventMember, error) {
	raw, err := json.Marshal(answers)
	if err != nil {
		return EventMember{}, err
	}
	m, err := scanMember(q.QueryRowContext(ctx, `INSERT INTO event_members
		(event_id, user_id, role, coc_accepted_at, signup_answers, approval_status, joined_via, city, country, age_range, gender)
		VALUES ($1, $2, 'participant', now(), $3, $4, $5, $6, $7, $8, $9) RETURNING `+memberColumns,
		eventID, userID, raw, status, via, p.City, p.Country, p.AgeRange, p.Gender))
	if err != nil {
		return EventMember{}, err
	}
	if err := hackevents.ReserveEventName(ctx, q, eventID); err != nil {
		return EventMember{}, err
	}
	return WithDisplayName(ctx, q, m, nil)
}

// Decide approves or rejects one pending participant; a rejected application
// forgets the profile it gave. False when there was no such application.
func Decide(ctx context.Context, q sqlkit.Querier, eventID, userID, actor, status string) (bool, error) {
	result, err := q.ExecContext(ctx, `UPDATE event_members SET approval_status=$4,approval_decided_at=now(),approval_decided_by=$3,city=CASE WHEN $4='rejected' AND joined_via='application' THEN '' ELSE city END,country=CASE WHEN $4='rejected' AND joined_via='application' THEN '' ELSE country END,age_range=CASE WHEN $4='rejected' AND joined_via='application' THEN '' ELSE age_range END,gender=CASE WHEN $4='rejected' AND joined_via='application' THEN '' ELSE gender END WHERE event_id=$1 AND user_id=$2 AND `+isParticipantSQL+` AND approval_status='pending'`, eventID, userID, actor, status)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// AdmitPending approves every participant waiting after joining by link
// (the organiser turned approval off).
func AdmitPending(ctx context.Context, tx *sql.Tx, eventID, userID string) (sql.Result, error) {
	return tx.ExecContext(ctx, `UPDATE event_members SET approval_status='approved', approval_decided_at=now(), approval_decided_by=$2 WHERE event_id=$1 AND `+isParticipantSQL+` AND approval_status='pending' AND joined_via='link'`, eventID, userID)
}

// SetTeam puts the member on teamID as a plain member, or takes them off
// (teamID invalid), resetting tenure, admin and the last removal notice; on a
// team they stop looking for one.
func SetTeam(ctx context.Context, q sqlkit.Querier, eventID, userID string, teamID sql.NullString) error {
	if !teamID.Valid {
		_, err := q.ExecContext(ctx, `
			UPDATE event_members SET team_id = NULL, team_removal_notice = '',
				team_joined_at = NULL, team_admin = false
			 WHERE event_id = $1 AND user_id = $2`, eventID, userID)
		return err
	}
	if _, err := q.ExecContext(ctx, `
		UPDATE event_members SET team_id = $3, team_removal_notice = '',
			team_joined_at = clock_timestamp(), team_admin = false
		 WHERE event_id = $1 AND user_id = $2`, eventID, userID, teamID.String); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `UPDATE event_members SET looking=false, looking_intro='' WHERE event_id=$1 AND user_id=$2`, eventID, userID)
	return err
}

// SetTeamAdmin makes the member an admin of their team, or not.
func SetTeamAdmin(ctx context.Context, q sqlkit.Querier, eventID, userID string, admin bool) error {
	_, err := q.ExecContext(ctx, `UPDATE event_members SET team_admin = $3 WHERE event_id = $1 AND user_id = $2`, eventID, userID, admin)
	return err
}

// PromoteSuccessor makes the team's longest-standing member its admin and
// returns who; sql.ErrNoRows when the team has nobody.
func PromoteSuccessor(ctx context.Context, q sqlkit.Querier, teamID string) (eventID, userID string, err error) {
	// NULLS LAST: Postgres sorts NULL first, which would hand admin to a member
	// with no team_joined_at ahead of one with an actual, earlier tenure.
	err = q.QueryRowContext(ctx, `
		UPDATE event_members SET team_admin = true
		 WHERE team_id = $1 AND user_id = (
		   SELECT user_id FROM event_members WHERE team_id = $1
		    ORDER BY team_joined_at NULLS LAST, user_id LIMIT 1)
		RETURNING event_id, user_id`, teamID).Scan(&eventID, &userID)
	return eventID, userID, err
}

// TeamAdmins is whether userID admins teamID and how many admins it has.
func TeamAdmins(ctx context.Context, q sqlkit.Querier, teamID, userID string) (isAdmin bool, admins int, err error) {
	err = q.QueryRowContext(ctx, `
		SELECT COALESCE(bool_or(user_id = $2 AND team_admin), false), COUNT(*) FILTER (WHERE team_admin)
		  FROM event_members WHERE team_id = $1`, teamID, userID).Scan(&isAdmin, &admins)
	return isAdmin, admins, err
}

// SetLooking sets whether an approved participant on no team is looking for
// one, with their note; false when no such member.
func SetLooking(ctx context.Context, q sqlkit.Querier, eventID, userID string, looking bool, intro string) (bool, error) {
	res, err := q.ExecContext(ctx, `UPDATE event_members SET looking=$3,looking_intro=$4 WHERE event_id=$1 AND user_id=$2 AND `+isApprovedParticipantSQL+` AND team_id IS NULL`, eventID, userID, looking, intro)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// SetRemovalNotice sets what the member is told after being taken off a team.
func SetRemovalNotice(ctx context.Context, q sqlkit.Querier, eventID, userID, notice string) error {
	_, err := q.ExecContext(ctx, `UPDATE event_members SET team_removal_notice=$3 WHERE event_id=$1 AND user_id=$2`, eventID, userID, notice)
	return err
}

// SetTeamRemovalNotices sets that notice for everyone on the team.
func SetTeamRemovalNotices(ctx context.Context, q sqlkit.Querier, teamID, notice string) error {
	_, err := q.ExecContext(ctx, `UPDATE event_members SET team_removal_notice=$2 WHERE team_id=$1`, teamID, notice)
	return err
}

// Remove deletes the membership row (the caller has dropped what waited on it).
func Remove(ctx context.Context, q sqlkit.Querier, eventID, userID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM event_members WHERE event_id = $1 AND user_id = $2`, eventID, userID)
	return err
}

// AnyoneButOrganisers is whether anyone but an organiser is in the event
// (whose name then stays taken after it is deleted).
func AnyoneButOrganisers(ctx context.Context, q sqlkit.Querier, eventID string) (bool, error) {
	var yes bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM event_members WHERE event_id = $1 AND NOT `+isOrganiserSQL+`)`, eventID).Scan(&yes)
	return yes, err
}
